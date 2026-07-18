package plexapi

import (
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestPathBuilders pins the exact wire path each builder produces — the
// single-owner grammar consumers compose instead of hand-building strings.
// The `>=` assertions are the wire contract: one literal `>`, never doubled
// and never URL-encoded (Plex silently ignores a malformed operator and
// returns the unfiltered set).
func TestPathBuilders(t *testing.T) {
	tests := []struct {
		name    string
		build   func() (string, error)
		want    string
		wantErr bool
	}{
		{
			name: "sessions", build: func() (string, error) { return string(SessionsPath()), nil },
			want: "/status/sessions",
		},
		{
			name: "sections", build: func() (string, error) { return string(SectionsPath()), nil },
			want: "/library/sections",
		},
		{
			name: "history", build: func() (string, error) { return string(HistoryPath(1700000000)), nil },
			want: "/status/sessions/history/all?sort=viewedAt:desc&viewedAt>=1700000000",
		},
		{
			name: "section items", build: func() (string, error) { p, err := SectionItemsPath("2"); return string(p), err },
			want: "/library/sections/2/all",
		},
		{
			name: "section items invalid key", build: func() (string, error) { p, err := SectionItemsPath("2; DROP"); return string(p), err },
			wantErr: true,
		},
		{
			name: "recently added", build: func() (string, error) { p, err := RecentlyAddedPath("5", 4, 1700000000); return string(p), err },
			want: "/library/sections/5/all?type=4&sort=addedAt:desc&addedAt>=1700000000",
		},
		{
			name: "recently added invalid key", build: func() (string, error) { p, err := RecentlyAddedPath("abc", 4, 0); return string(p), err },
			wantErr: true,
		},
		{
			name: "metadata", build: func() (string, error) { p, err := MetadataPath("42"); return string(p), err },
			want: "/library/metadata/42",
		},
		{
			name: "metadata invalid key", build: func() (string, error) { p, err := MetadataPath("../etc"); return string(p), err },
			wantErr: true,
		},
		{
			name: "children", build: func() (string, error) { p, err := ChildrenPath("7"); return string(p), err },
			want: "/library/metadata/7/children",
		},
		{
			name: "children invalid key", build: func() (string, error) { p, err := ChildrenPath("x"); return string(p), err },
			wantErr: true,
		},
		{
			name: "all leaves", build: func() (string, error) { p, err := AllLeavesPath("7"); return string(p), err },
			want: "/library/metadata/7/allLeaves",
		},
		{
			name: "all leaves invalid key", build: func() (string, error) { p, err := AllLeavesPath(""); return string(p), err },
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.build()
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				if !strings.Contains(err.Error(), "invalid rating key") {
					t.Errorf("err = %v, want the invalid-rating-key wording", err)
				}
				return
			}
			if got != tt.want {
				t.Errorf("path = %q, want %q", got, tt.want)
			}
			if strings.Contains(got, ">>") {
				t.Errorf("path %q contains a doubled > operator; Plex silently ignores it", got)
			}
			if strings.Contains(got, "%3E") {
				t.Errorf("path %q URL-encodes the > operator; Plex silently ignores encoded operators", got)
			}
		})
	}
}

// TestBuilderCapClasses pins each builder's descriptor type — the
// compile-time contract binding an endpoint to its read-cap class, so the
// same endpoint cannot silently read under two different caps at different
// call sites (the drift that put a consumer's RecentlyAdded under the
// general cap while the typed method used the list cap).
func TestBuilderCapClasses(t *testing.T) {
	requirePath := func(Path) {}
	requireListPath := func(ListPath) {}

	requirePath(SessionsPath())
	requirePath(SectionsPath())
	requirePath(HistoryPath(0)) // deliberate: the general cap is the unfiltered-fallback tripwire

	if p, err := SectionItemsPath("1"); err != nil {
		t.Fatal(err)
	} else {
		requireListPath(p)
	}
	if p, err := RecentlyAddedPath("1", 4, 0); err != nil {
		t.Fatal(err)
	} else {
		requireListPath(p)
	}
	if p, err := MetadataPath("1"); err != nil {
		t.Fatal(err)
	} else {
		requirePath(p)
	}
	if p, err := ChildrenPath("1"); err != nil {
		t.Fatal(err)
	} else {
		requirePath(p)
	}
	if p, err := AllLeavesPath("1"); err != nil {
		t.Fatal(err)
	} else {
		requirePath(p)
	}
}

// consumerItem is a consumer-owned decode type (deliberately NOT Item) —
// the shape FetchMetadata exists to serve.
type consumerItem struct {
	RatingKey string  `json:"ratingKey"`
	Title     string  `json:"title"`
	Index     FlexInt `json:"index"`
}

// consumerSection is a consumer-owned Directory decode type.
type consumerSection struct {
	Key  string `json:"key"`
	Type string `json:"type"`
}

// TestFetchMetadataConsumerType pins the exported decode kernel: a
// consumer-owned type decodes through the Metadata envelope with the full
// hardened pipeline underneath, composed with a path builder.
func TestFetchMetadataConsumerType(t *testing.T) {
	srv, seen := fixtureServer(t, map[string]string{
		"/status/sessions/history/all": `{"MediaContainer":{"Metadata":[
			{"ratingKey":"55","title":"Ep","index":"3"}]}}`,
	})
	c := newTestClient(t, srv)
	got, err := FetchMetadata[consumerItem](t.Context(), c, HistoryPath(1700000000))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].RatingKey != "55" || int(got[0].Index) != 3 {
		t.Errorf("FetchMetadata = %+v", got)
	}
	if !strings.Contains((*seen)[0], "viewedAt>=1700000000") {
		t.Errorf("request %q lacks the literal viewedAt>= filter", (*seen)[0])
	}
}

// TestFetchDirectoryConsumerType pins the Directory counterpart.
func TestFetchDirectoryConsumerType(t *testing.T) {
	srv, _ := fixtureServer(t, map[string]string{
		"/library/sections": `{"MediaContainer":{"Directory":[
			{"key":"1","type":"movie"},{"key":"2","type":"show"}]}}`,
	})
	c := newTestClient(t, srv)
	got, err := FetchDirectory[consumerSection](t.Context(), c, SectionsPath())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[1].Type != "show" {
		t.Errorf("FetchDirectory = %+v", got)
	}
}

// TestFetchCapClasses pins the cap split: FetchMetadata enforces the
// general cap while FetchMetadataList admits the same body under a raised
// list cap — the exported mirror of the SectionItems/Sessions split.
func TestFetchCapClasses(t *testing.T) {
	payload := `{"MediaContainer":{"Metadata":[{"ratingKey":"1","title":"` + strings.Repeat("x", 200) + `"}]}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(payload))
	}))
	t.Cleanup(srv.Close)
	c := newTestClient(t, srv, WithMaxBodyBytes(16), WithMaxListBodyBytes(1<<20))

	if _, err := FetchMetadata[consumerItem](t.Context(), c, "/x"); err == nil {
		t.Error("FetchMetadata under a tiny general cap should overflow")
	}
	if _, err := FetchMetadataList[consumerItem](t.Context(), c, "/x"); err != nil {
		t.Errorf("FetchMetadataList under the raised list cap: %v", err)
	}
}

// TestBaseTransport pins the protocol-upgrade seam: the accessor returns an
// independent clone of the configured base transport (CA trust + the
// per-attempt header timeout, no retry wrapper), and returns nil when the
// caller supplied its own http.Client.
func TestBaseTransport(t *testing.T) {
	t.Run("nil for WithHTTPClient", func(t *testing.T) {
		c, err := New("http://plex:32400", "tok", WithHTTPClient(&http.Client{}))
		if err != nil {
			t.Fatal(err)
		}
		if c.BaseTransport() != nil {
			t.Error("BaseTransport must be nil when the caller owns the transport")
		}
	})
	t.Run("clone carries header timeout and is independent", func(t *testing.T) {
		c, err := New("http://plex:32400", "tok")
		if err != nil {
			t.Fatal(err)
		}
		bt := c.BaseTransport()
		if bt == nil {
			t.Fatal("BaseTransport = nil for a default-constructed client")
		}
		if bt.ResponseHeaderTimeout != perAttemptHeaderTimeout {
			t.Errorf("ResponseHeaderTimeout = %v, want %v", bt.ResponseHeaderTimeout, perAttemptHeaderTimeout)
		}
		bt.ResponseHeaderTimeout = 0
		if again := c.BaseTransport(); again.ResponseHeaderTimeout != perAttemptHeaderTimeout {
			t.Error("mutating a returned clone leaked into the client's base transport")
		}
	})
	t.Run("carries the pinned CA", func(t *testing.T) {
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`ok`))
		}))
		t.Cleanup(srv.Close)
		pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
		c, err := New(srv.URL, "tok", WithCACertPEM(pemBytes))
		if err != nil {
			t.Fatal(err)
		}
		// A bare client over the returned base transport must complete the
		// TLS handshake against the pinned self-signed server — the exact
		// trust reuse a websocket dialer needs.
		hc := &http.Client{Transport: c.BaseTransport()}
		resp, err := hc.Get(srv.URL)
		if err != nil {
			t.Fatalf("GET over BaseTransport: %v", err)
		}
		resp.Body.Close()
		// And a DefaultTransport-based client must NOT trust it, proving
		// the pin (not the OS store) carried the handshake above.
		plain := &http.Client{}
		if resp, err := plain.Get(srv.URL); err == nil {
			resp.Body.Close()
			t.Error("unpinned client trusted the self-signed server; pin assertion is vacuous")
		}
	})
	t.Run("ForToken derivation shares the base transport", func(t *testing.T) {
		c, err := New("http://plex:32400", "tok")
		if err != nil {
			t.Fatal(err)
		}
		u := c.ForToken("other")
		if u.BaseTransport() == nil {
			t.Error("ForToken clone lost the base transport")
		}
	})
}
