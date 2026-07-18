package plexapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// newTestClient builds a Client against srv with fast retries.
func newTestClient(t *testing.T, srv *httptest.Server, opts ...Option) *Client {
	t.Helper()
	opts = append([]Option{WithBaseDelay(time.Millisecond)}, opts...)
	c, err := New(srv.URL, "test-token", opts...)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestNewValidation(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr string
	}{
		{name: "ftp scheme", url: "ftp://plex:32400", wantErr: "http or https"},
		{name: "no host", url: "http://", wantErr: "host"},
		{name: "garbage", url: "http://plex:32400\x7f", wantErr: "invalid"},
		{name: "valid http", url: "http://plex:32400"},
		{name: "valid https with path", url: "https://plex.example.com/plex"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(tt.url, "tok")
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("New(%q) error = %v", tt.url, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("New(%q) error = %v, want containing %q", tt.url, err, tt.wantErr)
			}
		})
	}
}

func TestNewRejectsEmptyCAPEM(t *testing.T) {
	if _, err := New("https://plex:32400", "tok", WithCACertPEM([]byte("not a pem"))); err == nil {
		t.Error("New with garbage CA PEM succeeded")
	}
}

// TestTokenTravelsInHeaderOnly pins the token-confidentiality contract:
// X-Plex-Token is a header, the URL never carries it.
func TestTokenTravelsInHeaderOnly(t *testing.T) {
	var gotToken, gotURI string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.Header.Get("X-Plex-Token")
		gotURI = r.RequestURI
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	if err := c.Get(t.Context(), "/identity", nil); err != nil {
		t.Fatal(err)
	}
	if gotToken != "test-token" {
		t.Errorf("X-Plex-Token header = %q", gotToken)
	}
	if strings.Contains(gotURI, "test-token") {
		t.Errorf("token leaked into URI %q", gotURI)
	}
	if accept := "application/json"; !strings.Contains(gotURI, "identity") {
		t.Errorf("unexpected URI %q (accept=%s)", gotURI, accept)
	}
}

// TestPathGuard pins the same-origin defense: absolute and scheme-relative
// references must be rejected before any request is sent.
func TestPathGuard(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("request escaped the path guard")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	for _, path := range []string{
		"https://evil.example.com/steal",
		"http://evil.example.com/steal",
		"//evil.example.com/steal",
	} {
		err := c.Get(t.Context(), path, nil)
		if err == nil || !strings.Contains(err.Error(), "must be relative") {
			t.Errorf("Get(%q) error = %v, want path-guard rejection", path, err)
		}
	}
}

// TestRedirectRefused pins the redirect policy: a 302 is surfaced as a
// StatusError, never followed (following would forward X-Plex-Token).
func TestRedirectRefused(t *testing.T) {
	var followed atomic.Bool
	evil := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		followed.Store(true)
	}))
	defer evil.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, evil.URL, http.StatusFound)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	err := c.Get(t.Context(), "/x", nil)
	var se *StatusError
	if !errors.As(err, &se) || se.Code != http.StatusFound {
		t.Errorf("Get through 302 = %v, want StatusError 302", err)
	}
	if followed.Load() {
		t.Error("client followed the redirect")
	}
}

func TestDoStatusMapping(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		check  func(t *testing.T, err error)
	}{
		{name: "404 is ErrNotFound", status: 404, check: func(t *testing.T, err error) {
			t.Helper()
			if !IsNotFound(err) {
				t.Errorf("err = %v, want ErrNotFound", err)
			}
		}},
		{name: "401 is StatusError", status: 401, check: func(t *testing.T, err error) {
			t.Helper()
			var se *StatusError
			if !errors.As(err, &se) || se.Code != 401 {
				t.Errorf("err = %v, want StatusError 401", err)
			}
		}},
		{name: "empty body is fine", status: 200, body: "", check: func(t *testing.T, err error) {
			t.Helper()
			if err != nil {
				t.Errorf("err = %v", err)
			}
		}},
		{name: "malformed JSON errors", status: 200, body: "{truncated", check: func(t *testing.T, err error) {
			t.Helper()
			if err == nil || !strings.Contains(err.Error(), "decoding") {
				t.Errorf("err = %v, want decode error", err)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()
			var out map[string]any
			err := newTestClient(t, srv, WithMaxAttempts(1)).Get(t.Context(), "/x", &out)
			tt.check(t, err)
		})
	}
}

func TestDoBodyCap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		big := strings.Repeat("x", int(DefaultMaxBodyBytes)+10)
		_, _ = w.Write([]byte(`{"pad":"` + big + `"}`))
	}))
	defer srv.Close()
	var out map[string]any
	err := newTestClient(t, srv).Get(t.Context(), "/x", &out)
	var tle *ResponseTooLargeError
	if !errors.As(err, &tle) || tle.Limit != DefaultMaxBodyBytes {
		t.Errorf("err = %v, want ResponseTooLargeError with limit %d", err, DefaultMaxBodyBytes)
	}
}

// TestStreamDecodeEdges pins the streaming-decode contract of decodeBody:
// over-cap always wins as the typed error (valid or truncated JSON alike,
// including via trailing content), exactly-at-cap decodes, and trailing
// non-whitespace stays an error like json.Unmarshal's.
func TestStreamDecodeEdges(t *testing.T) {
	serve := func(t *testing.T, payload string) *Client {
		t.Helper()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(payload))
		}))
		t.Cleanup(srv.Close)
		return newTestClient(t, srv, WithMaxBodyBytes(64))
	}
	var out map[string]any

	t.Run("over cap with valid JSON prefix is the typed error", func(t *testing.T) {
		c := serve(t, `{"pad":"`+strings.Repeat("x", 200)+`"}`)
		var tle *ResponseTooLargeError
		if err := c.Get(t.Context(), "/x", &out); !errors.As(err, &tle) || tle.Limit != 64 {
			t.Errorf("err = %v, want ResponseTooLargeError limit 64", err)
		}
	})
	t.Run("valid value with huge trailing content is over-cap not trailing", func(t *testing.T) {
		c := serve(t, `{}`+strings.Repeat(" ", 200))
		var tle *ResponseTooLargeError
		if err := c.Get(t.Context(), "/x", &out); !errors.As(err, &tle) {
			t.Errorf("err = %v, want ResponseTooLargeError", err)
		}
	})
	t.Run("exactly at cap decodes", func(t *testing.T) {
		payload := `{"pad":"` + strings.Repeat("x", 64-10) + `"}`
		if int64(len(payload)) != 64 {
			t.Fatalf("fixture drifted: len = %d, want 64", len(payload))
		}
		c := serve(t, payload)
		if err := c.Get(t.Context(), "/x", &out); err != nil {
			t.Errorf("exactly-at-cap Get: %v", err)
		}
	})
	t.Run("trailing non-whitespace within cap is a decode error", func(t *testing.T) {
		c := serve(t, `{} garbage`)
		err := c.Get(t.Context(), "/x", &out)
		if err == nil || !strings.Contains(err.Error(), "decoding response") {
			t.Errorf("err = %v, want a decoding-response error", err)
		}
	})
	t.Run("truncated JSON within cap is a decode error", func(t *testing.T) {
		c := serve(t, `{"pad":`)
		err := c.Get(t.Context(), "/x", &out)
		if err == nil || !strings.Contains(err.Error(), "decoding response") {
			t.Errorf("err = %v, want a decoding-response error", err)
		}
	})
	t.Run("trailing whitespace within cap decodes clean", func(t *testing.T) {
		c := serve(t, `{}   `)
		if err := c.Get(t.Context(), "/x", &out); err != nil {
			t.Errorf("trailing-whitespace Get: %v", err)
		}
	})
}

// TestGetRetriesTransient pins transparent retry: a 503 then 200 sequence
// succeeds without caller involvement.
func TestGetRetriesTransient(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"MediaContainer":{}}`))
	}))
	defer srv.Close()
	var out MC[map[string]any]
	if err := newTestClient(t, srv).Get(t.Context(), "/x", &out); err != nil {
		t.Fatalf("Get = %v after transient 503", err)
	}
	if calls.Load() != 2 {
		t.Errorf("server saw %d calls, want 2", calls.Load())
	}
}

// TestPutNeverRetried pins the mutation contract: a PUT that answers 503 is
// NOT retried (at-most-once application).
func TestPutNeverRetried(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	err := c.SetAudioStream(t.Context(), 1, 2)
	var se *StatusError
	if !errors.As(err, &se) || se.Code != 503 {
		t.Fatalf("err = %v, want StatusError 503", err)
	}
	if calls.Load() != 1 {
		t.Errorf("PUT was attempted %d times, want exactly 1", calls.Load())
	}
}

func TestOnRetryHook(t *testing.T) {
	var calls atomic.Int32
	var hookCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv, WithOnRetry(func(int, *http.Request, *http.Response, error) {
		hookCalls.Add(1)
	}))
	if err := c.Get(t.Context(), "/x", nil); err != nil {
		t.Fatal(err)
	}
	if hookCalls.Load() != 2 {
		t.Errorf("retry hook fired %d times, want 2", hookCalls.Load())
	}
}

func TestForToken(t *testing.T) {
	var gotToken string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.Header.Get("X-Plex-Token")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	u := c.ForToken("user-token")
	if err := u.Get(t.Context(), "/x", nil); err != nil {
		t.Fatal(err)
	}
	if gotToken != "user-token" {
		t.Errorf("token = %q, want user-token", gotToken)
	}
	if u.httpClient != c.httpClient {
		t.Error("ForToken did not share the transport")
	}
	if c.Token() != "test-token" {
		t.Error("original client token mutated")
	}
}

// TestWithTimeoutDefaultOnlyWithoutCallerDeadline pins the request-timeout
// wiring end-to-end (via httpx.ContextWithDefaultTimeout): the WithTimeout
// default bounds a request only when the caller brought no deadline, and a
// caller deadline is never undercut by a smaller default.
func TestWithTimeoutDefaultOnlyWithoutCallerDeadline(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(150 * time.Millisecond):
			_, _ = w.Write([]byte(`{}`))
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	c := newTestClient(t, srv, WithTimeout(30*time.Millisecond), WithMaxAttempts(1))

	// No caller deadline: the 30ms default applies and the slow response
	// exceeds it.
	if err := c.Get(context.Background(), "/", nil); err == nil {
		t.Error("expected the WithTimeout default to bound the request")
	}
	// Generous caller deadline: authoritative, never undercut by the 30ms
	// default, so the slow response completes.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.Get(ctx, "/", nil); err != nil {
		t.Errorf("caller deadline was undercut by the smaller default: %v", err)
	}
}

func TestIsConfigError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "401 fatal", err: &StatusError{Code: 401}, want: true},
		{name: "403 fatal", err: &StatusError{Code: 403}, want: true},
		{name: "404 fatal", err: &StatusError{Code: 404}, want: true},
		{name: "408 transient", err: &StatusError{Code: 408}, want: false},
		{name: "429 transient", err: &StatusError{Code: 429}, want: false},
		{name: "500 transient", err: &StatusError{Code: 500}, want: false},
		{name: "503 transient", err: &StatusError{Code: 503}, want: false},
		{name: "transport transient", err: errors.New("dial tcp: refused"), want: false},
		{name: "nil", err: nil, want: false},
		{name: "wrapped fatal", err: errors.Join(errors.New("ctx"), &StatusError{Code: 400}), want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsConfigError(tt.err); got != tt.want {
				t.Errorf("IsConfigError = %v, want %v", got, tt.want)
			}
		})
	}
}
