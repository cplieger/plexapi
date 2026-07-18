package plexapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cplieger/httpx/v3"
)

// Default read caps per endpoint class. A single item or a session/history
// page fits well inside the general cap; a full library-section listing can
// be an order of magnitude larger. Both are configurable (WithMaxBodyBytes,
// WithMaxListBodyBytes) for deployments whose libraries outgrow them.
const (
	// DefaultMaxBodyBytes caps metadata, session, history, and server-info
	// responses (10 MB).
	DefaultMaxBodyBytes = 10 << 20
	// DefaultMaxListBodyBytes caps full section listings (40 MB).
	DefaultMaxListBodyBytes = 40 << 20
)

// Transport/retry defaults. Attempt counts are total (3 = first try + 2
// retries). The per-attempt response-header timeout lives
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
	httpClient    *http.Client
	baseTransport *http.Transport
	logger        *slog.Logger
	baseURL       *url.URL
	token         string
	timeout       time.Duration
	maxBody       int64
	maxListBody   int64
}

// Option configures New.
type Option func(*options)

type options struct {
	httpClient  *http.Client
	logger      *slog.Logger
	onRetry     httpx.OnRetry
	caPEM       []byte
	timeout     time.Duration
	attempts    int
	baseDelay   time.Duration
	maxBody     int64
	maxListBody int64
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

// WithLogger sets the slog.Logger for the client's own diagnostics (the
// construction-time plaintext-URL warning and the over-cap response
// warning). Defaults to slog.Default(); pass a level-filtered or discard
// logger to quiet them.
func WithLogger(l *slog.Logger) Option {
	return func(o *options) {
		if l != nil {
			o.logger = l
		}
	}
}

// WithMaxBodyBytes sets the read cap for metadata, session, history, and
// server-info responses (default DefaultMaxBodyBytes). Non-positive values
// are ignored.
func WithMaxBodyBytes(n int64) Option {
	return func(o *options) {
		if n > 0 {
			o.maxBody = n
		}
	}
}

// WithMaxListBodyBytes sets the read cap for full section listings
// (default DefaultMaxListBodyBytes) — the knob for libraries large enough
// that a section's listing outgrows the default. Non-positive values are
// ignored.
func WithMaxListBodyBytes(n int64) Option {
	return func(o *options) {
		if n > 0 {
			o.maxListBody = n
		}
	}
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
		logger:      slog.Default(),
		timeout:     defaultRequestTimeout,
		attempts:    defaultMaxAttempts,
		baseDelay:   defaultBaseDelay,
		maxBody:     DefaultMaxBodyBytes,
		maxListBody: DefaultMaxListBodyBytes,
	}
	for _, opt := range opts {
		opt(&o)
	}

	hc := o.httpClient
	var base *http.Transport
	if hc == nil {
		hc, base, err = newHTTPClient(&o)
		if err != nil {
			return nil, err
		}
	}
	warnIfPlaintextURL(o.logger, parsed)
	return &Client{
		baseURL:       parsed,
		token:         token,
		httpClient:    hc,
		baseTransport: base,
		logger:        o.logger,
		timeout:       o.timeout,
		maxBody:       o.maxBody,
		maxListBody:   o.maxListBody,
	}, nil
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

// BaseURL returns a copy of the configured server base URL (for deriving a
// websocket URL or logging the host). It is a clone: mutating it never
// re-targets the client, whose every request resolves against the internal
// original — handing out that pointer would reopen the origin-mutation
// class the server-relative path guard exists to close.
func (c *Client) BaseURL() *url.URL {
	u := *c.baseURL
	return &u
}

// Token returns the client's token. Sanctioned in-process uses: comparing
// tokens for cache eviction/rotation, constructing the plex.tv client
// (NewTV) with the same credential, and authenticating a caller-owned
// protocol upgrade (the X-Plex-Token header on a websocket dial). Never
// log it, and never place it in a URL.
func (c *Client) Token() string { return c.token }

// RedirectPolicy returns the client's redirect policy (its CheckRedirect
// function) so a caller-owned protocol upgrade — a websocket dialer — can
// enforce the same policy on its own http.Client without access to the
// live client. Pair it with BaseTransport, which carries the trust half of
// that seam. Nil when the client was built via WithHTTPClient without a
// CheckRedirect (net/http's follow-all default; the caller owns policy on
// that path).
func (c *Client) RedirectPolicy() httpx.CheckRedirect { return c.httpClient.CheckRedirect }

// BaseTransport returns an independent clone of the hardened base transport
// the client was constructed with — the same CA trust (WithCACertPEM or the
// OS store) and per-attempt response-header timeout, WITHOUT the retry
// round-tripper. It is the seam for a caller-owned protocol upgrade (a
// WebSocket dialer) that must share the client's trust settings while
// owning its own dial policy: the retry wrapper's base transport is not
// otherwise reachable, and rebuilding a transport from scratch silently
// drops a pinned CA. Mutating the returned clone never affects the client.
// Returns nil when the client was built with WithHTTPClient (the caller
// already owns that transport).
func (c *Client) BaseTransport() *http.Transport {
	if c.baseTransport == nil {
		return nil
	}
	return c.baseTransport.Clone()
}

// newHTTPClient assembles the hardened default transport stack, returning
// the client and the base transport under its retry round-tripper (retained
// so BaseTransport can clone it).
func newHTTPClient(o *options) (*http.Client, *http.Transport, error) {
	var base *http.Transport
	if len(o.caPEM) > 0 {
		tr, err := httpx.CATransport(o.caPEM)
		if err != nil {
			return nil, nil, fmt.Errorf("pinning Plex CA: %w", err)
		}
		base = tr
	} else {
		dt, err := httpx.CloneDefaultTransport()
		if err != nil {
			return nil, nil, fmt.Errorf("building base transport: %w", err)
		}
		base = dt
	}
	base.ResponseHeaderTimeout = perAttemptHeaderTimeout

	// httpx v3 TransportConfig: MaxAttempts 0 means "unset, take the default";
	// a NEGATIVE value means exactly one attempt. plexapi's WithMaxAttempts
	// contract is "minimum 1 — 1 disables retries", so any n < 1 maps to -1
	// (try once) rather than being handed to the struct raw, where 0 would
	// silently mean 3 total attempts.
	attempts := o.attempts
	if attempts < 1 {
		attempts = -1
	}
	return &http.Client{
		Transport: httpx.NewRetryRoundTripper(base, httpx.TransportConfig{
			MaxAttempts: attempts,
			BaseDelay:   o.baseDelay,
			OnRetry:     o.onRetry,
		}),
		// Plex's API does not issue redirects; refuse to follow any. Go's
		// default policy forwards custom headers (X-Plex-Token included) on
		// cross-origin redirects — a hostile 302 would exfiltrate the token.
		CheckRedirect: httpx.RefuseAllRedirects,
	}, base, nil
}

// warnIfPlaintextURL emits one construction-time warning when the server
// URL is http:// to a non-loopback, non-docker-short-name host: the token
// transits the network unencrypted. A dotless hostname is treated as a
// docker network name (trusted bridge) and stays quiet. Routed through the
// configured logger so a deliberate plaintext deployment can quiet it.
func warnIfPlaintextURL(logger *slog.Logger, u *url.URL) {
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
	logger.Warn("plex server URL is http:// to a non-local host; X-Plex-Token will transit unencrypted. "+
		"Front Plex with a TLS proxy and use https:// for off-LAN deployments.",
		"host", host)
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
	// The client's WithTimeout default applies only when the caller brought
	// no deadline of its own (a caller deadline is never undercut).
	ctx, cancel := httpx.ContextWithDefaultTimeout(ctx, c.timeout)
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
		// LogSafeError strips the URL a *url.Error embeds (defense in depth:
		// the URL never carries the token, and the reduced form keeps error
		// strings stable for log grammars).
		return fmt.Errorf("plex %s %s: %w", method, path, httpx.LogSafeError(err))
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
	return c.decodeBody(method, path, resp.Body, maxBytes, result)
}

// decodeBody stream-decodes a capped JSON body into result. The decoder
// pulls through a counting reader bounded at cap+1 bytes, so peak memory is
// the decoder's window plus the decoded values — not a full body buffer
// followed by a decode copy (which doubled peak memory on 40 MB listings).
// The +1 probe byte distinguishes exactly-at-cap from over-cap (mirroring
// httpx.ReadLimitedBody), and the over-cap check outranks every decode
// error, so an oversized body always surfaces as the typed
// *ResponseTooLargeError — never as a truncation-shaped decode error. An
// empty body (zero bytes) decodes to nothing: some Plex endpoints answer
// 200 with no payload instead of an empty container. Trailing
// non-whitespace after the JSON value stays an error, matching
// json.Unmarshal's contract.
func (c *Client) decodeBody(method, path string, body io.Reader, maxBytes int64, result any) error {
	cr := &countingReader{r: io.LimitReader(body, maxBytes+1)}
	dec := json.NewDecoder(cr)
	decErr := dec.Decode(result)

	overCap := func() error {
		// Operator-facing breadcrumb: an over-cap body almost always means
		// an unfiltered or oversized response class, worth surfacing in
		// logs beyond the one failed call. Routed through the configured
		// logger.
		c.logger.Warn("plexapi: response exceeded read cap",
			"method", method, "path", path, "cap_bytes", maxBytes)
		return &ResponseTooLargeError{Path: path, Limit: maxBytes}
	}
	// drain consumes the capped remainder without buffering (best-effort:
	// a read error mid-drain just stops the count). The buffered variant
	// always read the whole capped body BEFORE classifying, so over-cap
	// had to win over any decode error — a decoder that aborts on the
	// first garbage byte of a 50 MB body must still report the typed
	// over-cap error, not the garbage. The drain restores that ordering
	// while keeping the streaming memory profile.
	drain := func() { _, _ = io.Copy(io.Discard, cr) }

	if decErr == nil {
		// The decoder stops at the end of the first JSON value; reject
		// trailing non-whitespace like json.Unmarshal does. Probe from the
		// decoder's buffer BEFORE draining the raw remainder (the drain
		// bypasses that buffer).
		_, tokErr := dec.Token()
		drain()
		if cr.n > maxBytes {
			return overCap()
		}
		if !errors.Is(tokErr, io.EOF) {
			return fmt.Errorf("plex %s %s: decoding response: trailing data after JSON value", method, path)
		}
		return nil
	}
	drain()
	switch {
	case cr.n > maxBytes:
		return overCap()
	case errors.Is(decErr, io.EOF) && cr.n == 0:
		return nil // empty body
	case errors.Is(decErr, io.EOF), errors.Is(decErr, io.ErrUnexpectedEOF), isJSONError(decErr):
		return fmt.Errorf("plex %s %s: decoding response: %w", method, path, decErr)
	default:
		return fmt.Errorf("plex %s %s: reading body: %w", method, path, decErr)
	}
}

// isJSONError reports whether err is a JSON parse/shape error (as opposed
// to a transport read error surfaced through the decoder).
func isJSONError(err error) bool {
	var syn *json.SyntaxError
	var typ *json.UnmarshalTypeError
	return errors.As(err, &syn) || errors.As(err, &typ)
}

// countingReader counts the bytes read through it, so decodeBody can
// detect an over-cap body after a streaming decode.
type countingReader struct {
	r io.Reader
	n int64
}

func (cr *countingReader) Read(p []byte) (int, error) {
	n, err := cr.r.Read(p)
	cr.n += int64(n)
	return n, err
}

// Get fetches a server-relative path and decodes the JSON response into
// result. It is the escape hatch for endpoints without a typed method
// (decode through MC[T] for container-wrapped payloads); the same
// hardening (path guard, redirect refusal, retries, body cap) applies.
func (c *Client) Get(ctx context.Context, path string, result any) error {
	return c.do(ctx, http.MethodGet, path, c.maxBody, result)
}

// put issues a PUT (no body, like Plex's parameterized mutation endpoints)
// and discards the response. Never retried.
func (c *Client) put(ctx context.Context, path string) error {
	return c.do(ctx, http.MethodPut, path, c.maxBody, nil)
}

// FetchMetadata fetches a general-cap endpoint and decodes the
// {"MediaContainer":{"Metadata":[...]}} envelope — the dominant Plex
// response shape — into the caller-owned item type T. It is the exported
// decode kernel for consumers that keep their own domain models: the same
// generic the typed Item methods are built on (Go methods cannot be
// type-parameterized, so it is a package function taking the client).
// Compose it with the path builders (HistoryPath, MetadataPath, ...): the
// builder's return type carries the endpoint's read-cap class, so a
// listing-sized endpoint cannot compile against the general cap — use
// FetchMetadataList for the ListPath builders (SectionItemsPath,
// RecentlyAddedPath).
func FetchMetadata[T any](ctx context.Context, c *Client, path Path) ([]T, error) {
	return fetchMetadata[T](ctx, c, string(path), c.maxBody)
}

// FetchMetadataList is FetchMetadata under the large-listing read cap
// (WithMaxListBodyBytes). It accepts only ListPath — the full-listing
// endpoints (SectionItemsPath, RecentlyAddedPath), whose responses on a big
// library are an order of magnitude larger than any other Plex response.
func FetchMetadataList[T any](ctx context.Context, c *Client, path ListPath) ([]T, error) {
	return fetchMetadata[T](ctx, c, string(path), c.maxListBody)
}

// FetchDirectory fetches a general-cap endpoint and decodes the
// {"MediaContainer":{"Directory":[...]}} envelope (library sections) into
// the caller-owned type T. The Directory counterpart of FetchMetadata.
func FetchDirectory[T any](ctx context.Context, c *Client, path Path) ([]T, error) {
	var resp MC[struct {
		Directory []T `json:"Directory"`
	}]
	if err := c.do(ctx, http.MethodGet, string(path), c.maxBody, &resp); err != nil {
		return nil, err
	}
	return resp.MediaContainer.Directory, nil
}

// fetchMetadata is the cap-parameterized core behind FetchMetadata and
// FetchMetadataList.
func fetchMetadata[T any](ctx context.Context, c *Client, path string, maxBytes int64) ([]T, error) {
	var resp MC[struct {
		Metadata []T `json:"Metadata"`
	}]
	if err := c.do(ctx, http.MethodGet, path, maxBytes, &resp); err != nil {
		return nil, err
	}
	return resp.MediaContainer.Metadata, nil
}
