package plexapi

import (
	"context"
	"encoding/pem"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// warnRecorder captures slog warnings for assertion.
type warnRecorder struct {
	mu   sync.Mutex
	msgs []string
}

func (r *warnRecorder) Enabled(context.Context, slog.Level) bool { return true }
func (r *warnRecorder) Handle(_ context.Context, rec slog.Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.msgs = append(r.msgs, rec.Message)
	return nil
}
func (r *warnRecorder) WithAttrs([]slog.Attr) slog.Handler { return r }
func (r *warnRecorder) WithGroup(string) slog.Handler      { return r }

func captureLog(t *testing.T) *warnRecorder {
	t.Helper()
	rec := &warnRecorder{}
	prev := slog.Default()
	slog.SetDefault(slog.New(rec))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return rec
}

func TestWarnIfPlaintextURL(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		wantWarn bool
	}{
		{name: "https never warns", url: "https://plex.example.com:32400"},
		{name: "http localhost quiet", url: "http://localhost:32400"},
		{name: "http loopback ip quiet", url: "http://127.0.0.1:32400"},
		{name: "http docker short name quiet", url: "http://plex:32400"},
		{name: "http remote host warns", url: "http://plex.example.com:32400", wantWarn: true},
		{name: "http remote ipv4 warns", url: "http://203.0.113.7:32400", wantWarn: true},
		{name: "http remote ipv6 warns", url: "http://[2001:db8::1]:32400", wantWarn: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := captureLog(t)
			if _, err := New(tt.url, "tok"); err != nil {
				t.Fatal(err)
			}
			warned := false
			for _, m := range rec.msgs {
				if strings.Contains(m, "unencrypted") {
					warned = true
				}
			}
			if warned != tt.wantWarn {
				t.Errorf("warned = %v, want %v (msgs %v)", warned, tt.wantWarn, rec.msgs)
			}
		})
	}
}

// TestCAPinnedTLS pins the WithCACertPEM path end-to-end: a TLS server whose
// self-signed certificate is pinned verifies; without the pin it fails.
func TestCAPinnedTLS(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"MediaContainer":{"friendlyName":"pinned"}}`))
	}))
	defer srv.Close()
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})

	t.Run("pinned CA verifies", func(t *testing.T) {
		c, err := New(srv.URL, "tok", WithCACertPEM(pemBytes))
		if err != nil {
			t.Fatal(err)
		}
		id, err := c.Identity(t.Context())
		if err != nil {
			t.Fatalf("Identity over pinned TLS: %v", err)
		}
		if id.FriendlyName != "pinned" {
			t.Errorf("Identity = %+v", id)
		}
	})
	t.Run("unpinned fails verification", func(t *testing.T) {
		c, err := New(srv.URL, "tok", WithMaxAttempts(1))
		if err != nil {
			t.Fatal(err)
		}
		if err := c.Get(t.Context(), "/", nil); err == nil {
			t.Error("Get succeeded against an untrusted self-signed cert")
		}
	})
}

// TestTransportErrorRedacted pins that a dial failure's error text does not
// embed the full request URL (url.Error unwrapping).
func TestTransportErrorRedacted(t *testing.T) {
	c, err := New("http://127.0.0.1:1", "tok", WithMaxAttempts(1), WithHTTPClient(&http.Client{}))
	if err != nil {
		t.Fatal(err)
	}
	gotErr := c.Get(t.Context(), "/library/sections", nil)
	if gotErr == nil {
		t.Skip("port 1 unexpectedly reachable")
	}
	if strings.Contains(gotErr.Error(), "http://127.0.0.1:1/library/sections") {
		t.Errorf("full URL embedded in transport error: %v", gotErr)
	}
	if !strings.Contains(gotErr.Error(), "/library/sections") {
		t.Errorf("path context missing from error: %v", gotErr)
	}
}

func TestAccessors(t *testing.T) {
	c, err := New("http://plex:32400/base", "tok")
	if err != nil {
		t.Fatal(err)
	}
	want, _ := url.Parse("http://plex:32400/base")
	if c.BaseURL().String() != want.String() {
		t.Errorf("BaseURL = %v", c.BaseURL())
	}
	if (&ResponseTooLargeError{Path: "/x", Limit: 5}).Error() == "" {
		t.Error("empty ResponseTooLargeError message")
	}
	if (&StatusError{Method: "GET", Path: "/x", Status: "401 Unauthorized", Code: 401}).Error() == "" {
		t.Error("empty StatusError message")
	}
}

// TestWithLoggerRoutesDiagnostics pins the logger seam: both library log
// sites (plaintext warning, over-cap warning) route through the configured
// logger, so a consumer can quiet or redirect them.
func TestWithLoggerRoutesDiagnostics(t *testing.T) {
	rec := &warnRecorder{}
	custom := slog.New(rec)

	// Construction-time plaintext warning goes to the custom logger, not
	// the (recording) default.
	def := captureLog(t)
	c, err := New("http://plex.example.com:32400", "tok", WithLogger(custom))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	rec.mu.Lock()
	for _, m := range rec.msgs {
		if strings.Contains(m, "unencrypted") {
			found = true
		}
	}
	rec.mu.Unlock()
	if !found {
		t.Error("plaintext warning did not reach the configured logger")
	}
	for _, m := range def.msgs {
		if strings.Contains(m, "unencrypted") {
			t.Error("plaintext warning leaked to slog.Default despite WithLogger")
		}
	}

	// Over-cap warning goes to the same configured logger.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(make([]byte, 64))
	}))
	defer srv.Close()
	c2, err := New(srv.URL, "tok", WithLogger(custom), WithMaxBodyBytes(16))
	if err != nil {
		t.Fatal(err)
	}
	if err := c2.Get(t.Context(), "/x", &struct{}{}); err == nil {
		t.Fatal("over-cap Get returned nil error")
	}
	capWarn := false
	rec.mu.Lock()
	for _, m := range rec.msgs {
		if strings.Contains(m, "read cap") {
			capWarn = true
		}
	}
	rec.mu.Unlock()
	if !capWarn {
		t.Error("over-cap warning did not reach the configured logger")
	}
	_ = c
}

// TestBodyCapOptions pins the configurable caps: the general cap and the
// list cap are independent, and ForToken inherits both.
func TestBodyCapOptions(t *testing.T) {
	payload := `{"MediaContainer":{"Metadata":[{"ratingKey":"1","title":"` + strings.Repeat("x", 200) + `"}]}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(payload))
	}))
	defer srv.Close()

	t.Run("raised list cap admits a big listing", func(t *testing.T) {
		c, err := New(srv.URL, "tok", WithMaxBodyBytes(16), WithMaxListBodyBytes(1<<20))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := c.SectionItems(t.Context(), "1"); err != nil {
			t.Errorf("SectionItems under raised list cap: %v", err)
		}
		// The general cap still applies to non-list endpoints.
		if _, err := c.Sessions(t.Context()); err == nil {
			t.Error("Sessions under tiny general cap should overflow")
		}
	})
	t.Run("lowered list cap rejects", func(t *testing.T) {
		c, err := New(srv.URL, "tok", WithMaxListBodyBytes(16))
		if err != nil {
			t.Fatal(err)
		}
		_, err = c.SectionItems(t.Context(), "1")
		var tle *ResponseTooLargeError
		if !errors.As(err, &tle) || tle.Limit != 16 {
			t.Errorf("err = %v, want ResponseTooLargeError limit 16", err)
		}
	})
	t.Run("non-positive values ignored", func(t *testing.T) {
		c, err := New(srv.URL, "tok", WithMaxBodyBytes(0), WithMaxListBodyBytes(-5))
		if err != nil {
			t.Fatal(err)
		}
		if c.maxBody != DefaultMaxBodyBytes || c.maxListBody != DefaultMaxListBodyBytes {
			t.Errorf("caps = (%d,%d), want defaults", c.maxBody, c.maxListBody)
		}
	})
	t.Run("ForToken inherits caps and logger", func(t *testing.T) {
		c, err := New(srv.URL, "tok", WithMaxListBodyBytes(1<<20))
		if err != nil {
			t.Fatal(err)
		}
		u := c.ForToken("other")
		if u.maxListBody != 1<<20 || u.logger != c.logger {
			t.Error("ForToken dropped configuration")
		}
	})
}
