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

	"github.com/cplieger/httpx/v5"
)

// DefaultMaxBodyBytes caps metadata, session, history, and server-info
// responses (10 MB).
const DefaultMaxBodyBytes = 10 << 20

// DefaultMaxListBodyBytes caps full section listings (40 MB).
const DefaultMaxListBodyBytes = 40 << 20

// Transport/retry defaults. Attempt counts are total (3 = first try + 2
// retries). The per-attempt response-header timeout lives on the
// transport, not as an http.Client.Timeout, which would cap the whole
// retry sequence instead of just one attempt.
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
	token         Token
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
// redirect policy are installed). Intended for tests and callers with
// bespoke transport needs.
func WithHTTPClient(hc *http.Client) Option {
	return func(o *options) { o.httpClient = hc }
}

// WithCACertPEM pins the CA(s) in pem as the sole TLS trust anchors, for a
// Plex behind a self-signed or private CA. The caller owns reading the PEM
// (the library does no file I/O); an empty pem is an error at construction.
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
// context has no deadline (default 2m).
func WithTimeout(d time.Duration) Option {
	return func(o *options) { o.timeout = d }
}

// WithOnRetry installs a per-retry observability hook (attempt number,
// request, response, error), forwarded to the underlying round-tripper.
func WithOnRetry(fn httpx.OnRetry) Option {
	return func(o *options) { o.onRetry = fn }
}

// WithLogger sets the slog.Logger for the client's own diagnostics (the
// construction-time plaintext-URL warning and the over-cap response
// warning). Defaults to slog.Default().
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
// (default DefaultMaxListBodyBytes). Non-positive values are ignored.
func WithMaxListBodyBytes(n int64) Option {
	return func(o *options) {
		if n > 0 {
			o.maxListBody = n
		}
	}
}

// Token is a Plex authentication token, the credential sent as X-Plex-Token.
//
// It is a distinct type so it cannot be transposed with the server URL on
// [New]: the two are adjacent and both are strings on the wire, and a named
// type makes a swapped pair of variables a compile error.
type Token string

// New parses and validates baseURL (http/https scheme, non-empty host) and
// returns a Client. Unless WithHTTPClient overrides it, the transport is:
// OS trust store or the pinned CA from WithCACertPEM, a per-attempt
// response-header timeout, an httpx retry round-tripper (429/502/503/504 +
// transient transport errors, honoring Retry-After), and a refuse-all
// redirect policy. Construction warns via slog when baseURL is plain http
// to a non-local host.
func New(baseURL string, token Token, opts ...Option) (*Client, error) {
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
func (c *Client) ForToken(token Token) *Client {
	clone := *c
	clone.token = token
	return &clone
}

// BaseURL returns a deep copy of the configured server base URL (for
// deriving a websocket URL or logging the host). Mutating it never
// re-targets the client.
func (c *Client) BaseURL() *url.URL {
	return c.baseURL.Clone()
}

// Token returns the client's token. Sanctioned in-process uses: comparing
// tokens for cache eviction/rotation, constructing the plex.tv client
// (NewTV) with the same credential, and authenticating a caller-owned
// protocol upgrade (the X-Plex-Token header on a websocket dial). Never
// log it, and never place it in a URL.
func (c *Client) Token() Token { return c.token }

// RedirectPolicy returns the client's redirect policy (its CheckRedirect
// function) so a caller-owned protocol upgrade — a websocket dialer — can
// enforce the same policy on its own http.Client. Nil when the client was
// built via WithHTTPClient without a CheckRedirect.
func (c *Client) RedirectPolicy() httpx.CheckRedirect { return c.httpClient.CheckRedirect }

// BaseTransport returns an independent clone of the hardened base transport
// the client was constructed with — the same CA trust and per-attempt
// response-header timeout, without the retry round-tripper. It is the seam
// for a caller-owned protocol upgrade (a WebSocket dialer) that must share
// the client's trust settings while owning its own dial policy. Mutating
// the returned clone never affects the client. Returns nil when the client
// was built with WithHTTPClient.
func (c *Client) BaseTransport() *http.Transport {
	if c.baseTransport == nil {
		return nil
	}
	return c.baseTransport.Clone()
}

// newHTTPClient assembles the hardened default transport stack, returning
// the client and the base transport under its retry round-tripper.
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

	// httpx's TransportConfig.MaxAttempts: 0 means "unset, take the
	// default"; WithMaxAttempts's contract is "minimum 1 — 1 disables
	// retries", so n < 1 maps to -1 (try once) rather than 0, which would
	// silently restore the default 3.
	attempts := o.attempts
	if attempts < 1 {
		attempts = -1
	}

	return httpx.NewRetryClient(base, httpx.RefuseAllRedirects, httpx.TransportConfig{
		MaxAttempts: attempts,
		BaseDelay:   o.baseDelay,
		OnRetry:     o.onRetry,
	}), base, nil
}

// warnIfPlaintextURL emits one construction-time warning when the server
// URL is http:// to a non-loopback, non-docker-short-name host. A dotless
// hostname is treated as a docker network name and stays quiet.
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
// against the base URL. An absolute or scheme-relative reference would
// override the configured host via ResolveReference and leak the token to
// that origin.
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
// result (skipped when result is nil or the body is empty). 404 maps to
// ErrNotFound, other non-200s to *StatusError; bodies are capped at
// maxBytes with the overflow reported as *ResponseTooLargeError.
func (c *Client) do(ctx context.Context, method, path string, maxBytes int64, result any) error {
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
	req.Header.Set("X-Plex-Token", string(c.token))

	resp, err := c.httpClient.Do(req)
	if err != nil {
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

// decodeBody stream-decodes a capped JSON body into result. The over-cap
// check outranks every decode error, so an oversized body always surfaces
// as the typed *ResponseTooLargeError rather than a truncation-shaped
// decode error. An empty body decodes to nothing, since some Plex
// endpoints answer 200 with no payload. Trailing non-whitespace after the
// JSON value stays an error, matching json.Unmarshal's contract.
func (c *Client) decodeBody(method, path string, body io.Reader, maxBytes int64, result any) error {
	cr := &countingReader{r: io.LimitReader(body, maxBytes+1)}
	dec := json.NewDecoder(cr)
	decErr := dec.Decode(result)

	overCap := func() error {
		c.logger.Warn("plexapi: response exceeded read cap",
			"method", method, "path", path, "cap_bytes", maxBytes)
		return &ResponseTooLargeError{Path: path, Limit: maxBytes}
	}
	// drain consumes the capped remainder without buffering, restoring the
	// pre-streaming ordering (over-cap wins over any decode error) while
	// keeping the streaming memory profile.
	drain := func() { _, _ = io.Copy(io.Discard, cr) }

	if decErr == nil {
		// The decoder stops at the end of the first JSON value; probe from
		// its buffer BEFORE draining the raw remainder (the drain bypasses
		// that buffer).
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
	_, isSyntax := errors.AsType[*json.SyntaxError](err)
	_, isType := errors.AsType[*json.UnmarshalTypeError](err)
	return isSyntax || isType
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
// result. It is the escape hatch for endpoints without a typed method; the
// same hardening (path guard, redirect refusal, retries, body cap) applies.
func (c *Client) Get(ctx context.Context, path string, result any) error {
	return c.do(ctx, http.MethodGet, path, c.maxBody, result)
}

// put issues a PUT (no body) and discards the response. Never retried.
func (c *Client) put(ctx context.Context, path string) error {
	return c.do(ctx, http.MethodPut, path, c.maxBody, nil)
}

// FetchMetadata fetches a general-cap endpoint and decodes the
// {"MediaContainer":{"Metadata":[...]}} envelope into the caller-owned item
// type T. Compose it with the path builders (HistoryPath, MetadataPath,
// ...); use FetchMetadataList for the ListPath builders (SectionItemsPath,
// RecentlyAddedPath).
//
// A generic METHOD (Go 1.27): the type parameter is the caller's item type,
// so a consumer that wants to mock this surface wraps it in a non-generic
// method of its own (see plex-language-sync's internal/plex adapter).
func (c *Client) FetchMetadata[T any](ctx context.Context, path Path) ([]T, error) {
	return fetchMetadata[T](ctx, c, string(path), c.maxBody)
}

// FetchMetadataList is FetchMetadata under the large-listing read cap
// (WithMaxListBodyBytes). It accepts only ListPath.
func (c *Client) FetchMetadataList[T any](ctx context.Context, path ListPath) ([]T, error) {
	return fetchMetadata[T](ctx, c, string(path), c.maxListBody)
}

// FetchDirectory fetches a general-cap endpoint and decodes the
// {"MediaContainer":{"Directory":[...]}} envelope (library sections) into
// the caller-owned type T.
func (c *Client) FetchDirectory[T any](ctx context.Context, path Path) ([]T, error) {
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
