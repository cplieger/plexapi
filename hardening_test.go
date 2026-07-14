package plexapi

import (
	"context"
	"encoding/pem"
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
