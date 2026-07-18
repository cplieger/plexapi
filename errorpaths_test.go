package plexapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Error-path coverage: every typed endpoint must propagate a transport-level
// failure rather than swallowing it, and the invalid-path guards must fire
// before any request.

func TestEndpointsPropagateServerErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()
	c := newTestClient(t, srv, WithMaxAttempts(1))
	ctx := t.Context()

	checks := []struct {
		name string
		call func() error
	}{
		{"Identity", func() error { _, err := c.Identity(ctx); return err }},
		{"Accounts", func() error { _, err := c.Accounts(ctx); return err }},
		{"AdminAccount", func() error { _, err := c.AdminAccount(ctx); return err }},
		{"Providers", func() error { _, err := c.Providers(ctx); return err }},
		{"StatisticsBandwidth", func() error { _, err := c.StatisticsBandwidth(ctx, 6); return err }},
		{"Sections", func() error { _, err := c.Sections(ctx); return err }},
		{"SectionItems", func() error { _, err := c.SectionItems(ctx, "1"); return err }},
		{"RecentlyAdded", func() error { _, err := c.RecentlyAdded(ctx, "1", MetadataTypeEpisode, 0); return err }},
		{"Metadata", func() error { _, err := c.Metadata(ctx, "1"); return err }},
		{"Children", func() error { _, err := c.Children(ctx, "1"); return err }},
		{"AllLeaves", func() error { _, err := c.AllLeaves(ctx, "1"); return err }},
		{"ItemsByGUID", func() error { _, err := c.ItemsByGUID(ctx, "imdb://tt1"); return err }},
		{"ShowForEpisodeGUID", func() error { _, err := c.ShowForEpisodeGUID(ctx, "plex://episode/x"); return err }},
		{"ContainerTotalSize", func() error { _, err := c.ContainerTotalSize(ctx, "1", 0); return err }},
		{"Sessions", func() error { _, err := c.Sessions(ctx); return err }},
		{"History", func() error { _, err := c.History(ctx, 0); return err }},
	}
	for _, tc := range checks {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(); err == nil {
				t.Errorf("%s returned nil error on 502", tc.name)
			}
		})
	}
}

func TestKeyedEndpointsRejectInvalidKeys(t *testing.T) {
	c, err := New("http://plex:32400", "tok")
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	checks := []struct {
		name string
		call func() error
	}{
		{"Metadata", func() error { _, err := c.Metadata(ctx, "abc"); return err }},
		{"Children", func() error { _, err := c.Children(ctx, "abc"); return err }},
		{"AllLeaves", func() error { _, err := c.AllLeaves(ctx, "abc"); return err }},
		{"RecentlyAdded", func() error { _, err := c.RecentlyAdded(ctx, "abc", 4, 0); return err }},
		{"SectionItems", func() error { _, err := c.SectionItems(ctx, "abc"); return err }},
	}
	for _, tc := range checks {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			if err == nil || !strings.Contains(err.Error(), "invalid rating key") {
				t.Errorf("%s(abc) error = %v, want invalid-rating-key rejection", tc.name, err)
			}
		})
	}
}

func TestResolvePathRejectsGarbage(t *testing.T) {
	c, err := New("http://plex:32400", "tok")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.resolvePath("http://[::1]:namedport"); err == nil {
		t.Error("unparseable path accepted")
	}
	if err := c.Get(t.Context(), "http://[::1]:namedport", nil); err == nil {
		t.Error("Get with unparseable path succeeded")
	}
}

func TestContainerTotalSizeRejectsBadSection(t *testing.T) {
	c, err := New("http://plex:32400", "tok")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.ContainerTotalSize(t.Context(), "not-a-key", 0); err == nil {
		t.Error("non-numeric section key accepted")
	}
}

func TestWithTVHTTPClientOption(t *testing.T) {
	hc := &http.Client{}
	tv := NewTV("tok", WithTVHTTPClient(hc))
	if tv.httpClient != hc {
		t.Error("WithTVHTTPClient did not install the client")
	}
}

func TestSharedServersTransportError(t *testing.T) {
	tv := NewTV("tok", WithTVBaseURL("http://127.0.0.1:1"))
	if _, err := tv.SharedServers(t.Context(), "m"); err == nil {
		t.Error("nil error for unreachable plex.tv")
	}
}

func TestSharedServersMalformedXML(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<MediaContainer><SharedServer"))
	}))
	defer srv.Close()
	if _, err := NewTV("t", WithTVBaseURL(srv.URL)).SharedServers(t.Context(), "m"); err == nil {
		t.Error("nil error for malformed XML")
	}
}

func TestSharedServersOversizedBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(make([]byte, DefaultMaxBodyBytes+10))
	}))
	defer srv.Close()
	_, err := NewTV("t", WithTVBaseURL(srv.URL)).SharedServers(t.Context(), "m")
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("err = %v, want too-large rejection", err)
	}
}
