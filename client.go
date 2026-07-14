package plexapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cplieger/httpx/v2"
)

// Read caps per endpoint class. A single item or a session/history page fits
// well inside maxBodyBytes; a full library-section listing can be an order
// of magnitude larger.
const (
	maxBodyBytes     = 10 << 20 // 10 MB: metadata, sessions, history, server info
	maxListBodyBytes = 40 << 20 // 40 MB: full section listings (SectionItems)
)

// Transport/retry defaults. Attempt counts are total (httpx v2 semantics:
// 3 = first try + 2 retries). The per-attempt response-header timeout lives
// on the transport, NOT as an http.Client.Timeout: a client-level timeout
// would wrap the retry round-tripper and cap the whole sequence, defeating
// the retries it sits above; on the transport a stalled attempt fails as a
// retryable net.Error instead.
const (
	defaultMaxAttempts      = 3
	defaultBaseDelay        = 200 * time.Millisecond
	defaultRequestTimeout   = 2 * time.Minute
	perAttemptHeaderTimeout = 15 * time.Second
)

// Client is a Plex Media Server API client for one base URL + token.
// A single Client is safe for concurrent use. Construct with New.
type Client struct {
	httpClient *http.Client
	baseURL    *url.URL
	token      string
	timeout    time.Duration
}

// Option configures New.
type Option func(*options)

type options struct {
	httpClient *http.Client
	onRetry    httpx.OnRetry
	caPEM      []byte
	timeout    time.Duration
	attempts   int
	baseDelay  time.Duration
}

// WithHTTPClient supplies a caller-owned *http.Client, replacing the
// built-in transport entirely (no retry round-tripper, no CA pinning, no
// redirect policy are installed — the caller owns all of it). Intended for
// tests and callers with bespoke transport needs.
func WithHTTPClient(hc *http.Client) Option {
	return func(o *options) { o.httpClient = hc }
}

// WithCACertPEM pins the CA(s) in pem as the sole TLS trust anchors, for a
// Plex behind a self-signed or private CA. Verification stays ON. The
// caller owns reading the PEM (the library does no file I/O); an empty pem
// is an error at construction.
func WithCACertPEM(pem []byte) Option {
	return func(o *options) { o.caPEM = pem }
}

// WithMaxAttempts sets the TOTAL number of attempts per GET including the
// first (default 3, minimum 1 — 1 disables retries). Writes are never
// retried regardless.
func WithMaxAttempts(n int) Option {
	return func(o *options) { o.attempts = n }
}

// WithBaseDelay sets the initial retry backoff (default 200ms).
func WithBaseDelay(d time.Duration) Option {
	return func(o *options) { o.baseDelay = d }
}

// WithTimeout sets the per-request ceiling applied ONLY when the caller's
// context has no deadline (default 2m). A caller deadline is always the
// authoritative budget and is never undercut.
func WithTimeout(d time.Duration) Option {
	return func(o *options) { o.timeout = d }
}

// WithOnRetry installs a per-retry observability hook (attempt number,
// request, response, error), forwarded to the underlying round-tripper.
// Consumers use it to surface a retry counter metric.
func WithOnRetry(fn httpx.OnRetry) Option {
	return func(o *options) { o.onRetry = fn }
}

// New parses and validates baseURL (http/https scheme, non-empty host) and
// returns a Client. Unless WithHTTPClient overrides it, the transport is:
// OS trust store or the pinned CA from WithCACertPEM, a per-attempt
// response-header timeout, an httpx retry round-tripper (429/502/503/504 +
// transient transport errors, honoring Retry-After), and a refuse-all
// redirect policy so the token can never ride a hostile 3xx. Construction
// warns via slog when baseURL is plain http to a non-local host (the token
// would transit unencrypted).
func New(baseURL, token string, opts ...Option) (*Client, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid Plex server URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("plex server URL must use http or https scheme, got %q", parsed.Scheme)
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("plex server URL must include a host: %q", baseURL)
	}

	o := options{
		timeout:   defaultRequestTimeout,
		attempts:  defaultMaxAttempts,
		baseDelay: defaultBaseDelay,
	}
	for _, opt := range opts {
		opt(&o)
	}

	hc := o.httpClient
	if hc == nil {
		hc, err = newHTTPClient(&o)
		if err != nil {
			return nil, err
		}
	}
	warnIfPlaintextURL(parsed)
	return &Client{baseURL: parsed, token: token, httpClient: hc, timeout: o.timeout}, nil
}

// ForToken returns a Client for the same server and transport but a
// different token — the per-user client for user-scoped writes (Plex
// records a stream-selection PUT against the requesting token's user).
// The underlying connection pool is shared.
func (c *Client) ForToken(token string) *Client {
	clone := *c
	clone.token = token
	return &clone
}

// BaseURL returns the configured server base URL (for deriving a websocket
// URL or logging the host).
func (c *Client) BaseURL() *url.URL { return c.baseURL }

// Token returns the client's token (for cache-eviction comparisons; never
// log it).
func (c *Client) Token() string { return c.token }

// HTTPClient returns the underlying *http.Client, so a websocket dialer can
// share the same transport (CA trust, redirect policy).
func (c *Client) HTTPClient() *http.Client { return c.httpClient }

// newHTTPClient assembles the hardened default transport stack.
func newHTTPClient(o *options) (*http.Client, error) {
	var base *http.Transport
	if len(o.caPEM) > 0 {
		tr, err := httpx.CATransport(o.caPEM)
		if err != nil {
			return nil, fmt.Errorf("pinning Plex CA: %w", err)
		}
		base = tr
	} else {
		dt, ok := http.DefaultTransport.(*http.Transport)
		if !ok {
			return nil, errors.New("http.DefaultTransport is not *http.Transport; cannot clone base transport")
		}
		base = dt.Clone()
	}
	base.ResponseHeaderTimeout = perAttemptHeaderTimeout

	rtOpts := []httpx.RTOption{
		httpx.WithRTMaxAttempts(o.attempts),
		httpx.WithRTBaseDelay(o.baseDelay),
	}
	if o.onRetry != nil {
		rtOpts = append(rtOpts, httpx.WithOnRetry(o.onRetry))
	}
	return &http.Client{
		Transport: httpx.NewRetryRoundTripper(base, rtOpts...),
		// Plex's API does not issue redirects; refuse to follow any. Go's
		// default policy forwards custom headers (X-Plex-Token included) on
		// cross-origin redirects — a hostile 302 would exfiltrate the token.
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}, nil
}

// warnIfPlaintextURL emits one construction-time warning when the server
// URL is http:// to a non-loopback, non-docker-short-name host: the token
// transits the network unencrypted. A dotless hostname is treated as a
// docker network name (trusted bridge) and stays quiet.
func warnIfPlaintextURL(u *url.URL) {
	if u == nil || u.Scheme != "http" {
		return
	}
	host := u.Hostname()
	if host == "" || host == "localhost" {
		return
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() {
			return
		}
	} else if !strings.Contains(host, ".") {
		return
	}
	slog.Warn("plex server URL is http:// to a non-local host; X-Plex-Token will transit unencrypted. "+
		"Front Plex with a TLS proxy and use https:// for off-LAN deployments.",
		"host", host)
}

// requestContext applies the client's default timeout only when the
// caller's context carries no deadline.
func (c *Client) requestContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok || c.timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, c.timeout)
}

// resolvePath validates that path is server-relative and resolves it
// against the base URL. An absolute ("https://evil/x") or scheme-relative
// ("//evil/x") reference would override the configured host via
// ResolveReference and leak the token to that origin; every legitimate
// Plex path is host-relative, so those are rejected outright.
func (c *Client) resolvePath(path string) (string, error) {
	ref, err := url.Parse(path)
	if err != nil {
		return "", fmt.Errorf("parsing path %q: %w", path, err)
	}
	if ref.IsAbs() || ref.Host != "" {
		return "", fmt.Errorf("plex request path must be relative to the configured server, got %q", path)
	}
	return c.baseURL.ResolveReference(ref).String(), nil
}

// do issues one authenticated request and decodes the JSON body into
// result (skipped when result is nil or the body is empty — some Plex
// endpoints return an empty body instead of an empty container). 404 maps
// to ErrNotFound, other non-200s to *StatusError; bodies are capped at
// maxBytes with the overflow reported as *ResponseTooLargeError.
func (c *Client) do(ctx context.Context, method, path string, maxBytes int64, result any) error {
	ctx, cancel := c.requestContext(ctx)
	defer cancel()

	target, err := c.resolvePath(path)
	if err != nil {
		return fmt.Errorf("plex %s %s: %w", method, path, err)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, http.NoBody)
	if err != nil {
		return fmt.Errorf("plex %s %s: building request: %w", method, path, err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Plex-Token", c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("plex %s %s: %w", method, path, redactedTransportErr(err))
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusNotFound:
		httpx.DrainClose(resp.Body)
		return ErrNotFound
	case resp.StatusCode != http.StatusOK:
		httpx.DrainClose(resp.Body)
		return &StatusError{Method: method, Path: path, Status: resp.Status, Code: resp.StatusCode}
	}

	if result == nil {
		httpx.DrainClose(resp.Body)
		return nil
	}
	body, err := httpx.ReadLimitedBody(resp.Body, maxBytes)
	if err != nil {
		var tooLarge *httpx.ResponseTooLargeError
		if errors.As(err, &tooLarge) {
			// Operator-facing breadcrumb (a stable log contract): an
			// over-cap body almost always means an unfiltered or oversized
			// response class, worth alerting on rather than just failing
			// the one call.
			slog.Warn("plex API response exceeded read cap; body truncated, likely an unfiltered or oversized response",
				"method", method, "path", path, "cap_bytes", maxBytes)
			return &ResponseTooLargeError{Path: path, Limit: maxBytes}
		}
		return fmt.Errorf("plex %s %s: reading body: %w", method, path, err)
	}
	if len(body) == 0 {
		return nil
	}
	if err := json.Unmarshal(body, result); err != nil {
		return fmt.Errorf("plex %s %s: decoding response: %w", method, path, err)
	}
	return nil
}

// redactedTransportErr strips the full URL a *url.Error embeds (defense in
// depth: the URL never carries the token, but the reduced form also keeps
// error strings stable for log grammars).
func redactedTransportErr(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

// Get fetches a server-relative path and decodes the JSON response into
// result. It is the escape hatch for endpoints without a typed method
// (decode through MC[T] for container-wrapped payloads); the same
// hardening (path guard, redirect refusal, retries, body cap) applies.
func (c *Client) Get(ctx context.Context, path string, result any) error {
	return c.do(ctx, http.MethodGet, path, maxBodyBytes, result)
}

// put issues a PUT (no body, like Plex's parameterized mutation endpoints)
// and discards the response. Never retried.
func (c *Client) put(ctx context.Context, path string) error {
	return c.do(ctx, http.MethodPut, path, maxBodyBytes, nil)
}

// fetchMetadata decodes {"MediaContainer":{"Metadata":[...]}} lists, the
// dominant Plex response shape.
func fetchMetadata[T any](ctx context.Context, c *Client, path string, maxBytes int64) ([]T, error) {
	var resp MC[struct {
		Metadata []T `json:"Metadata"`
	}]
	if err := c.do(ctx, http.MethodGet, path, maxBytes, &resp); err != nil {
		return nil, err
	}
	return resp.MediaContainer.Metadata, nil
}

// fetchDirectory decodes {"MediaContainer":{"Directory":[...]}} lists
// (library sections).
func fetchDirectory[T any](ctx context.Context, c *Client, path string) ([]T, error) {
	var resp MC[struct {
		Directory []T `json:"Directory"`
	}]
	if err := c.do(ctx, http.MethodGet, path, maxBodyBytes, &resp); err != nil {
		return nil, err
	}
	return resp.MediaContainer.Directory, nil
}
