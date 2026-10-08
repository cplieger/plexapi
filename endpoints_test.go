package plexapi

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/cplieger/xmlx"
)

// fixtureServer serves canned JSON per path prefix and records requests.
func fixtureServer(t *testing.T, routes map[string]string) (*httptest.Server, *[]string) {
	t.Helper()
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.RequestURI)
		// Longest matching prefix wins: a bare "/" route would otherwise
		// shadow everything on random map order.
		best := ""
		for prefix := range routes {
			if strings.HasPrefix(r.URL.Path, prefix) && len(prefix) > len(best) {
				best = prefix
			}
		}
		if best == "" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(routes[best]))
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

func TestSections(t *testing.T) {
	srv, _ := fixtureServer(t, map[string]string{
		"/library/sections": `{"MediaContainer":{"Directory":[
			{"key":"1","title":"Movies","type":"movie"},
			{"key":"2","title":"TV","type":"show"}]}}`,
	})
	got, err := newTestClient(t, srv).Sections(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Key != "1" || got[1].Type != SectionTypeShow {
		t.Errorf("Sections = %+v", got)
	}
}

func TestSectionItems(t *testing.T) {
	srv, seen := fixtureServer(t, map[string]string{
		"/library/sections/2/all": `{"MediaContainer":{"Metadata":[
			{"ratingKey":"100","title":"Show A","year":2020,"guid":"plex://show/abc",
			 "Guid":[{"id":"tvdb://81189"},{"id":"imdb://tt0903747"}]}]}}`,
	})
	got, err := newTestClient(t, srv).SectionItems(t.Context(), "2")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].RatingKey != "100" || len(got[0].GUIDs) != 2 {
		t.Errorf("SectionItems = %+v", got)
	}
	if (*seen)[0] != "GET /library/sections/2/all" {
		t.Errorf("request = %q", (*seen)[0])
	}
	// Invalid section key must be rejected before any request.
	if _, err := newTestClient(t, srv).SectionItems(t.Context(), "2; DROP"); err == nil {
		t.Error("non-numeric section key accepted")
	}
}

func TestRecentlyAddedWireFilter(t *testing.T) {
	srv, seen := fixtureServer(t, map[string]string{
		"/library/sections/2/all": `{"MediaContainer":{"Metadata":[]}}`,
	})
	_, err := newTestClient(t, srv).RecentlyAdded(t.Context(), "2", MetadataTypeEpisode, 1700000000)
	if err != nil {
		t.Fatal(err)
	}
	// The literal single-char `>=` operator is a wire contract: Plex
	// silently ignores an encoded or doubled operator and returns the
	// unfiltered listing.
	if !strings.Contains((*seen)[0], "addedAt>=1700000000") {
		t.Errorf("request %q lacks literal addedAt>= filter", (*seen)[0])
	}
	if !strings.Contains((*seen)[0], "type=4") {
		t.Errorf("request %q lacks type filter", (*seen)[0])
	}
}

func TestHistoryWireFilter(t *testing.T) {
	srv, seen := fixtureServer(t, map[string]string{
		"/status/sessions/history/all": `{"MediaContainer":{"Metadata":[
			{"ratingKey":"55","type":"episode","accountID":"7","librarySectionID":3}]}}`,
	})
	got, err := newTestClient(t, srv).History(t.Context(), 1700000000)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || int(got[0].AccountID) != 7 || int(got[0].LibrarySectionID) != 3 {
		t.Errorf("History = %+v", got)
	}
	if !strings.Contains((*seen)[0], "viewedAt>=1700000000") {
		t.Errorf("request %q lacks literal viewedAt>= filter", (*seen)[0])
	}
}

func TestMetadataPolymorphic(t *testing.T) {
	srv, _ := fixtureServer(t, map[string]string{
		"/library/metadata/42": `{"MediaContainer":{"Metadata":[
			{"ratingKey":"42","type":"episode","title":"Ep","parentIndex":"2","index":5,
			 "grandparentTitle":"Show","Media":[{"id":1,"Part":[{"id":10,"Stream":[
				{"id":100,"streamType":2,"languageCode":"eng","selected":true},
				{"id":101,"streamType":3,"languageCode":"fre","forced":true}]}]}]}]}}`,
	})
	it, err := newTestClient(t, srv).Metadata(t.Context(), "42")
	if err != nil {
		t.Fatal(err)
	}
	if it.SeasonNum() != 2 || it.EpisodeNum() != 5 {
		t.Errorf("S%dE%d, want S2E5 (FlexInt string+number)", it.SeasonNum(), it.EpisodeNum())
	}
	streams := it.Media[0].Part[0].Stream
	if !streams[0].IsAudio() || !streams[1].IsSubtitle() || !streams[1].Forced {
		t.Errorf("streams = %+v", streams)
	}
}

func TestMetadataEmptyIsNotFound(t *testing.T) {
	srv, _ := fixtureServer(t, map[string]string{
		"/library/metadata/42": `{"MediaContainer":{"Metadata":[]}}`,
	})
	_, err := newTestClient(t, srv).Metadata(t.Context(), "42")
	if !IsNotFound(err) {
		t.Errorf("err = %v, want ErrNotFound for empty metadata", err)
	}
}

func TestChildrenAndAllLeaves(t *testing.T) {
	srv, seen := fixtureServer(t, map[string]string{
		"/library/metadata/7/children":  `{"MediaContainer":{"Metadata":[{"ratingKey":"71"}]}}`,
		"/library/metadata/7/allLeaves": `{"MediaContainer":{"Metadata":[{"ratingKey":"72"},{"ratingKey":"73"}]}}`,
	})
	c := newTestClient(t, srv)
	kids, err := c.Children(t.Context(), "7")
	if err != nil || len(kids) != 1 {
		t.Errorf("Children = %v, %v", kids, err)
	}
	leaves, err := c.AllLeaves(t.Context(), "7")
	if err != nil || len(leaves) != 2 {
		t.Errorf("AllLeaves = %v, %v", leaves, err)
	}
	joined := strings.Join(*seen, " ")
	if !strings.Contains(joined, "/children") || !strings.Contains(joined, "/allLeaves") {
		t.Errorf("requests = %v", *seen)
	}
}

func TestItemExists(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		wantExists bool
		wantErr    bool
	}{
		{name: "200 exists", status: 200, wantExists: true},
		{name: "404 does not", status: 404},
		{name: "401 undetermined", status: 401, wantErr: true},
		{name: "500 undetermined", status: 500, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
			}))
			defer srv.Close()
			got, err := newTestClient(t, srv, WithMaxAttempts(1)).ItemExists(t.Context(), "5")
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.wantExists {
				t.Errorf("exists = %v, want %v", got, tt.wantExists)
			}
		})
	}
	t.Run("invalid key rejected", func(t *testing.T) {
		c, _ := New("http://plex:32400", "tok")
		if _, err := c.ItemExists(t.Context(), "abc"); err == nil {
			t.Error("non-numeric key accepted")
		}
	})
}

func TestShowForEpisodeGUID(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "single show", want: "9", body: `{"MediaContainer":{"Metadata":[
			{"grandparentRatingKey":"9"},{"grandparentRatingKey":"9"}]}}`},
		{name: "ambiguous yields empty", want: "", body: `{"MediaContainer":{"Metadata":[
			{"grandparentRatingKey":"9"},{"grandparentRatingKey":"8"}]}}`},
		{name: "malformed grandparent yields empty", want: "", body: `{"MediaContainer":{"Metadata":[
			{"grandparentRatingKey":"nope"}]}}`},
		{name: "no matches yields empty", want: "", body: `{"MediaContainer":{"Metadata":[]}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, seen := fixtureServer(t, map[string]string{"/library/all": tt.body})
			got, err := newTestClient(t, srv).ShowForEpisodeGUID(t.Context(), "plex://episode/abc")
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("show = %q, want %q", got, tt.want)
			}
			if !strings.Contains((*seen)[0], "guid=plex%3A%2F%2Fepisode%2Fabc") {
				t.Errorf("request = %q, want encoded guid param", (*seen)[0])
			}
		})
	}
	t.Run("empty guid short-circuits", func(t *testing.T) {
		c, _ := New("http://plex:32400", "tok")
		items, err := c.ItemsByGUID(t.Context(), "")
		if err != nil || items != nil {
			t.Errorf("ItemsByGUID(\"\") = %v, %v", items, err)
		}
	})
}

func TestCountSectionItems(t *testing.T) {
	srv, seen := fixtureServer(t, map[string]string{
		"/library/sections/2/all": `{"MediaContainer":{"totalSize":4360}}`,
	})
	got, err := newTestClient(t, srv).CountSectionItems(t.Context(), "2", 4)
	if err != nil {
		t.Fatal(err)
	}
	if got != 4360 {
		t.Errorf("totalSize = %d", got)
	}
	req := (*seen)[0]
	for _, want := range []string{"type=4", "X-Plex-Container-Start=0", "X-Plex-Container-Size=1"} {
		if !strings.Contains(req, want) {
			t.Errorf("request %q lacks %q", req, want)
		}
	}

	t.Run("unfiltered omits type param", func(t *testing.T) {
		srv2, seen2 := fixtureServer(t, map[string]string{
			"/library/sections/3/all": `{"MediaContainer":{"totalSize":12}}`,
		})
		got, err := newTestClient(t, srv2).CountSectionItems(t.Context(), "3", 0)
		if err != nil {
			t.Fatal(err)
		}
		if got != 12 {
			t.Errorf("totalSize = %d", got)
		}
		if strings.Contains((*seen2)[0], "type=") {
			t.Errorf("unfiltered request %q must not carry a type param", (*seen2)[0])
		}
	})
	t.Run("invalid section key rejected before any request", func(t *testing.T) {
		c, _ := New("http://plex:32400", "tok")
		if _, err := c.CountSectionItems(t.Context(), "3; DROP", 4); err == nil {
			t.Error("non-numeric section key accepted")
		}
	})
}

// TestSessionsDecodesSessionGraph pins the LIVE /status/sessions wire shape,
// quoted ids included (verified 2026-08-21 against Plex 1.43.3): Media.id,
// Part.id and Stream.id arrive as JSON strings here while the same fields
// on /library/metadata/<key> arrive as bare numbers (TestMetadataPolymorphic
// pins that side).
func TestSessionsDecodesSessionGraph(t *testing.T) {
	srv, _ := fixtureServer(t, map[string]string{
		"/status/sessions": `{"MediaContainer":{"Metadata":[
			{"ratingKey":"1","sessionKey":"3","title":"Movie",
			 "User":{"id":"7","title":"alice"},
			 "Player":{"device":"TV","product":"Plex for LG","state":"playing","machineIdentifier":"m1","local":true},
			 "Session":{"location":"lan","bandwidth":20000},
			 "TranscodeSession":{"videoDecision":"transcode","audioDecision":"copy"},
			 "Media":[{"id":"55","videoResolution":"1080","bitrate":8000,
				"Part":[{"id":"66","decision":"transcode","Stream":[
					{"id":"77","streamType":2,"languageCode":"eng","selected":true}]}]}]}]}}`,
	})
	got, err := newTestClient(t, srv).Sessions(t.Context())
	if err != nil {
		t.Fatalf("Sessions() = %v, want a decoded session", err)
	}
	if len(got) != 1 {
		t.Fatalf("Sessions() returned %d sessions, want 1", len(got))
	}
	s := got[0]
	if s.User == nil || s.User.Title != "alice" || int(s.User.ID) != 7 {
		t.Errorf("User = %+v", s.User)
	}
	if s.Player == nil || !s.Player.Local || s.Player.MachineIdentifier != "m1" {
		t.Errorf("Player = %+v", s.Player)
	}
	if s.TranscodeSession == nil || s.TranscodeSession.VideoDecision != "transcode" {
		t.Errorf("TranscodeSession = %+v", s.TranscodeSession)
	}
	if s.Media[0].VideoResolution != "1080" || s.Media[0].Part[0].Decision != "transcode" {
		t.Errorf("Media = %+v", s.Media)
	}
	if got, want := int(s.Media[0].ID), 55; got != want {
		t.Errorf("Media[0].ID = %d, want %d (quoted id on /status/sessions)", got, want)
	}
	if got, want := int(s.Media[0].Part[0].ID), 66; got != want {
		t.Errorf("Part[0].ID = %d, want %d (quoted id on /status/sessions)", got, want)
	}
	if got, want := int(s.Media[0].Part[0].Stream[0].ID), 77; got != want {
		t.Errorf("Stream[0].ID = %d, want %d (quoted id on /status/sessions)", got, want)
	}
}

// TestIdentityAndAdminAccount pins the real /accounts shape (verified live
// 2026-07 against Plex 1.43.3): the id-0 managed placeholder comes first,
// the owner is id 1, and shared users follow under their plex.tv global
// ids. The `seen` assertion pins that AdminAccount never consults
// /myplex/account.
func TestIdentityAndAdminAccount(t *testing.T) {
	srv, seen := fixtureServer(t, map[string]string{
		"/myplex/account": `{"MyPlex":{"username":"admin@example.com"}}`,
		"/accounts": `{"MediaContainer":{"Account":[
			{"id":0,"name":""},{"id":1,"name":"Owner"},{"id":19646554,"name":"kid"}]}}`,
		"/": `{"MediaContainer":{"friendlyName":"borg","machineIdentifier":"m-1",
			"version":"1.41.0","platform":"Linux","myPlexSubscription":true,
			"transcoderActiveVideoSessions":2}}`,
	})
	c := newTestClient(t, srv)

	id, err := c.Identity(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if id.FriendlyName != "borg" || !id.MyPlexSubscription || id.TranscoderActiveVideoSessions != 2 {
		t.Errorf("Identity = %+v", id)
	}

	acct, err := c.AdminAccount(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if acct.ID != 1 || acct.Name != "Owner" {
		t.Errorf("AdminAccount = %+v, want ID=1 Name=Owner", acct)
	}
	for _, req := range *seen {
		if strings.Contains(req, "/myplex/account") {
			t.Errorf("AdminAccount consulted /myplex/account (%s); owner resolution must use /accounts id 1 only", req)
		}
	}
}

// TestAdminAccountPlaceholderNeverMatches pins the exact production
// failure: an accounts list whose only empty-name entry is the id-0
// placeholder must never resolve as the admin when the owner is absent.
func TestAdminAccountPlaceholderNeverMatches(t *testing.T) {
	srv, _ := fixtureServer(t, map[string]string{
		"/accounts": `{"MediaContainer":{"Account":[{"id":0,"name":""},{"id":2,"name":"kid"}]}}`,
	})
	if _, err := newTestClient(t, srv).AdminAccount(t.Context()); err == nil ||
		!strings.Contains(err.Error(), "not found in system accounts") {
		t.Errorf("err = %v", err)
	}
}

func TestProvidersAndStatistics(t *testing.T) {
	srv, seen := fixtureServer(t, map[string]string{
		"/media/providers": `{"MediaContainer":{"friendlyName":"borg","MediaProvider":[
			{"identifier":"com.plexapp.plugins.library","Feature":[
				{"type":"content","Directory":[
					{"title":"Movies","id":"1","type":"movie","durationTotal":1000,"storageTotal":2000}]}]}]}}`,
		"/statistics/resources": `{"MediaContainer":{"StatisticsResources":[
			{"hostCpuUtilization":12.5,"hostMemoryUtilization":40.0}]}}`,
		"/statistics/bandwidth": `{"MediaContainer":{"StatisticsBandwidth":[
			{"bytes":1024,"at":1700000000}]}}`,
	})
	c := newTestClient(t, srv)

	prov, err := c.Providers(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	dir := prov.MediaProviders[0].Features[0].Directories[0]
	if dir.StorageTotal != 2000 {
		t.Errorf("Providers directory = %+v", dir)
	}
	if !strings.Contains((*seen)[0], "includeStorage=1") {
		t.Errorf("providers request = %q", (*seen)[0])
	}

	res, err := c.StatisticsResources(t.Context(), 6)
	if err != nil || len(res) != 1 || res[0].HostCPUUtilization != 12.5 {
		t.Errorf("StatisticsResources = %v, %v", res, err)
	}
	bw, err := c.StatisticsBandwidth(t.Context(), 6)
	if err != nil || len(bw) != 1 || bw[0].Bytes != 1024 {
		t.Errorf("StatisticsBandwidth = %v, %v", bw, err)
	}
	joined := strings.Join(*seen, " ")
	if !strings.Contains(joined, "timespan=6") {
		t.Errorf("requests = %v", *seen)
	}
}

// TestStatisticsWithoutPlexPass pins graceful degradation: the endpoints
// 404 without Plex Pass and surface as ErrNotFound.
func TestStatisticsWithoutPlexPass(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	_, err := newTestClient(t, srv).StatisticsResources(t.Context(), 6)
	if !IsNotFound(err) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestStreamSelectionPaths(t *testing.T) {
	srv, seen := fixtureServer(t, map[string]string{"/library/parts/": `{}`})
	c := newTestClient(t, srv)
	if err := c.SetAudioStream(t.Context(), StreamSelection{PartID: 10, StreamID: 100}); err != nil {
		t.Fatal(err)
	}
	if err := c.SetSubtitleStream(t.Context(), StreamSelection{PartID: 10, StreamID: 200}); err != nil {
		t.Fatal(err)
	}
	if err := c.DisableSubtitles(t.Context(), 10); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"PUT /library/parts/10?audioStreamID=100&allParts=1",
		"PUT /library/parts/10?subtitleStreamID=200&allParts=1",
		"PUT /library/parts/10?subtitleStreamID=0&allParts=1",
	}
	for i, w := range want {
		if (*seen)[i] != w {
			t.Errorf("request[%d] = %q, want %q", i, (*seen)[i], w)
		}
	}
}

func TestSharedServers(t *testing.T) {
	t.Run("parses users", func(t *testing.T) {
		var gotPath, gotToken string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath, gotToken = r.URL.Path, r.Header.Get("X-Plex-Token")
			_, _ = w.Write([]byte(`<MediaContainer>
				<SharedServer userID="7" username="alice" accessToken="tok-a"/>
				<SharedServer userID="8" username="bob" accessToken="tok-b"/>
			</MediaContainer>`))
		}))
		defer srv.Close()
		tv := NewTV("admin-token", WithTVBaseURL(srv.URL))
		got, err := tv.SharedServers(t.Context(), "machine-1")
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 || got[0].Username != "alice" || got[1].AccessToken != "tok-b" {
			t.Errorf("SharedServers = %+v", got)
		}
		if gotPath != "/api/servers/machine-1/shared_servers" || gotToken != "admin-token" {
			t.Errorf("path=%q token=%q", gotPath, gotToken)
		}
	})
	t.Run("empty body is zero servers", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		defer srv.Close()
		got, err := NewTV("t", WithTVBaseURL(srv.URL)).SharedServers(t.Context(), "m")
		if err != nil || got != nil {
			t.Errorf("= %v, %v", got, err)
		}
	})
	t.Run("amplifying document rejected before the decode", func(t *testing.T) {
		// Well inside the wire cap, but its nesting would grow the
		// decoder's element stack one heap entry per three bytes; the
		// preflight refuses it without tokenizing.
		deep := strings.Repeat("<MediaContainer>", sharedServersLimits.MaxDepth+2)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(deep))
		}))
		defer srv.Close()
		got, err := NewTV("t", WithTVBaseURL(srv.URL)).SharedServers(t.Context(), "m")
		if got != nil {
			t.Errorf("SharedServers = %+v, want nil", got)
		}
		if !errors.Is(err, xmlx.ErrLimit) {
			t.Fatalf("err = %v, want an xmlx.ErrLimit", err)
		}
		le, ok := errors.AsType[*xmlx.LimitError](err)
		if !ok || le.Kind != xmlx.KindDepth {
			t.Errorf("err = %v, want KindDepth", err)
		}
	})
	t.Run("a real shared_servers document passes the preflight", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
			<MediaContainer friendlyName="srv" identifier="com.plexapp.plugins.library">
				<SharedServer id="1" username="alice" email="a@example.test" accessToken="tok-a" acceptedAt="1700000000">
					<Section id="9" key="1" title="Movies" type="movie" shared="1"/>
				</SharedServer>
			</MediaContainer>`))
		}))
		defer srv.Close()
		got, err := NewTV("t", WithTVBaseURL(srv.URL)).SharedServers(t.Context(), "m")
		if err != nil {
			t.Fatalf("SharedServers = %v, want the document accepted", err)
		}
		if len(got) != 1 || got[0].Username != "alice" {
			t.Errorf("SharedServers = %+v", got)
		}
	})
	t.Run("non-200 is StatusError", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}))
		defer srv.Close()
		_, err := NewTV("t", WithTVBaseURL(srv.URL)).SharedServers(t.Context(), "m")
		se, ok := errors.AsType[*StatusError](err)
		if !ok || se.Code != 401 {
			t.Fatalf("err = %v, want 401 StatusError", err)
		}
		if se.Path != "/api/servers/m/shared_servers" {
			t.Errorf("StatusError.Path = %q, want the real request path", se.Path)
		}
	})
	t.Run("machine id is path-escaped", func(t *testing.T) {
		var gotURI string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotURI = r.RequestURI
			_, _ = w.Write([]byte(`<MediaContainer/>`))
		}))
		defer srv.Close()
		_, err := NewTV("t", WithTVBaseURL(srv.URL)).SharedServers(t.Context(), "m/../../evil")
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(gotURI, "/../") {
			t.Errorf("traversal reached the wire: %q", gotURI)
		}
	})
}

// TestNewFieldsPreservePresence pins that the optional library fields stay
// nil when Plex omits them or sends null, so a consumer never reads an
// absent size as a zero-byte file or an absent scan time as 1970.
func TestNewFieldsPreservePresence(t *testing.T) {
	srv, _ := fixtureServer(t, map[string]string{
		"/library/sections/1/all": `{"MediaContainer":{"totalSize":5,"Metadata":[
			{"ratingKey":"1","Media":[{"videoCodec":"hevc","Part":[{"id":1}]}]},
			{"ratingKey":"2","Media":[{"Part":[{"id":2,"size":null}]}]},
			{"ratingKey":"3","Media":[{"Part":[{"id":3,"size":0}]}]},
			{"ratingKey":"4","viewCount":"2","lastViewedAt":"1700000000","Media":[{"Part":[{"id":4,"size":"5000000000"}]}]},
			{"ratingKey":"5","viewCount":0,"lastViewedAt":1600000000,"Media":[{"Part":[{"id":5,"size":5000000000}]}]}]}}`,
		"/library/sections": `{"MediaContainer":{"Directory":[
			{"key":"1","title":"Movies","type":"movie","scannedAt":1700000123},
			{"key":"2","title":"TV","type":"show"}]}}`,
	})
	c := newTestClient(t, srv)
	items, _, err := c.SectionItemsPage(t.Context(), "1", 0, Page{Size: 5})
	if err != nil {
		t.Fatalf("SectionItemsPage() = %v", err)
	}
	if len(items) != 5 {
		t.Fatalf("SectionItemsPage() returned %d items, want 5", len(items))
	}
	size := func(i int) *FlexInt64 { return items[i].Media[0].Part[0].Size }
	if size(0) != nil || size(1) != nil {
		t.Errorf("absent and null Part.size = %v, %v, want nil, nil", size(0), size(1))
	}
	for i, want := range map[int]int64{2: 0, 3: 5000000000, 4: 5000000000} {
		if size(i) == nil || int64(*size(i)) != want {
			t.Errorf("item %d Part.Size = %v, want non-nil %d", i, size(i), want)
		}
	}
	if items[0].Media[0].VideoCodec != "hevc" {
		t.Errorf("Media.VideoCodec = %q, want hevc", items[0].Media[0].VideoCodec)
	}
	if items[0].LastViewedAt != nil || items[0].ViewCount != nil {
		t.Errorf("absent lastViewedAt/viewCount = %v/%v, want nil/nil", items[0].LastViewedAt, items[0].ViewCount)
	}
	if lv, vc := items[3].LastViewedAt, items[3].ViewCount; lv == nil || int64(*lv) != 1700000000 || vc == nil || int(*vc) != 2 {
		t.Errorf("quoted lastViewedAt/viewCount = %v/%v, want 1700000000/2", lv, vc)
	}
	if vc := items[4].ViewCount; vc == nil || int(*vc) != 0 {
		t.Errorf("viewCount 0 = %v, want non-nil 0", vc)
	}

	sections, err := c.Sections(t.Context())
	if err != nil {
		t.Fatalf("Sections() = %v", err)
	}
	if s := sections[0].ScannedAt; s == nil || int64(*s) != 1700000123 {
		t.Errorf("Section.ScannedAt = %v, want 1700000123", s)
	}
	if sections[1].ScannedAt != nil {
		t.Errorf("absent scannedAt = %v, want nil", *sections[1].ScannedAt)
	}
}

// TestSessionsHardwareTranscodeFields pins the hardware-transcode fields in
// the python-plexapi TranscodeSession shape, and that a malformed flag
// leaves every other field of the payload decodable.
func TestSessionsHardwareTranscodeFields(t *testing.T) {
	srv, _ := fixtureServer(t, map[string]string{
		"/status/sessions": `{"MediaContainer":{"Metadata":[
			{"sessionKey":"1","TranscodeSession":{"videoDecision":"transcode","sourceVideoCodec":"hevc","videoCodec":"h264",
				"transcodeHwRequested":true,"transcodeHwFullPipeline":true,
				"transcodeHwDecoding":"vaapi","transcodeHwDecodingTitle":"Intel VAAPI",
				"transcodeHwEncoding":"vaapi","transcodeHwEncodingTitle":"Intel VAAPI"}},
			{"sessionKey":"2","TranscodeSession":{"videoDecision":"transcode",
				"transcodeHwRequested":true,"transcodeHwFullPipeline":false,"transcodeHwEncoding":"vaapi"}},
			{"sessionKey":"3","TranscodeSession":{"videoDecision":"transcode"}},
			{"sessionKey":"4","User":{"id":"9","title":"bob"},
			 "TranscodeSession":{"videoDecision":"transcode","transcodeHwRequested":"maybe","transcodeHwDecoding":"nvdec"},
			 "Media":[{"id":"8","Part":[{"id":"9","size":"700"}]}]}]}}`,
	})
	got, err := newTestClient(t, srv).Sessions(t.Context())
	if err != nil {
		t.Fatalf("Sessions() = %v, want a decoded payload despite one malformed flag", err)
	}
	if len(got) != 4 {
		t.Fatalf("Sessions() returned %d sessions, want 4", len(got))
	}
	flag := func(b *FlexBool) string {
		if b == nil {
			return "absent"
		}
		if !b.Valid() {
			return "invalid"
		}
		return strconv.FormatBool(b.Bool())
	}
	tests := []struct {
		name               string
		requested, full    string
		decoding, encoding string
	}{
		{name: "hardware decode and encode", requested: "true", full: "true", decoding: "vaapi", encoding: "vaapi"},
		{name: "requested, software decode", requested: "true", full: "false", encoding: "vaapi"},
		{name: "no hardware fields", requested: "absent", full: "absent"},
		{name: "malformed flag", requested: "invalid", full: "absent", decoding: "nvdec"},
	}
	for i, tt := range tests {
		ts := got[i].TranscodeSession
		if ts == nil {
			t.Errorf("%s: TranscodeSession = nil", tt.name)
			continue
		}
		if r, f := flag(ts.TranscodeHwRequested), flag(ts.TranscodeHwFullPipeline); r != tt.requested || f != tt.full {
			t.Errorf("%s: requested/full = %s/%s, want %s/%s", tt.name, r, f, tt.requested, tt.full)
		}
		if ts.TranscodeHwDecoding != tt.decoding || ts.TranscodeHwEncoding != tt.encoding {
			t.Errorf("%s: decoding/encoding = %q/%q, want %q/%q", tt.name, ts.TranscodeHwDecoding, ts.TranscodeHwEncoding, tt.decoding, tt.encoding)
		}
	}
	bad := got[3]
	if bad.User == nil || bad.User.Title != "bob" || int(bad.Media[0].ID) != 8 {
		t.Errorf("fields beside the malformed flag = %+v, want them decoded", bad)
	}
	if s := bad.Media[0].Part[0].Size; s == nil || int64(*s) != 700 {
		t.Errorf("Part.Size beside the malformed flag = %v, want 700", s)
	}
}

func TestActivities(t *testing.T) {
	srv, seen := fixtureServer(t, map[string]string{
		"/activities": `{"MediaContainer":{"size":6,"Activity":[
			{"uuid":"a1","type":"library.update.section","title":"Scanning Movies","subtitle":"x","progress":42.5,
			 "Context":{"librarySectionID":"3"}},
			{"uuid":"a2","type":"media.generate.credits","title":"Detecting Credits","progress":-1,
			 "Context":{"librarySectionID":4,"other":{"nested":true}}},
			{"uuid":"a3","type":"provider.epg.load","title":"Refreshing EPG"},
			{"uuid":"a4","type":"butler.task","progress":7,"Context":"not-an-object"},
			{"uuid":"a5","type":"x","Context":{"librarySectionID":null}},
			{"uuid":"a6","type":"x","Context":{"librarySectionID":"s-6"}}]}}`,
	})
	got, err := newTestClient(t, srv).Activities(t.Context())
	if err != nil {
		t.Fatalf("Activities() = %v", err)
	}
	if len(*seen) != 1 || (*seen)[0] != "GET /activities" {
		t.Errorf("requests = %v, want one GET /activities", *seen)
	}
	if len(got) != 6 {
		t.Fatalf("Activities() returned %d, want 6", len(got))
	}
	prog := func(p *float64) string {
		if p == nil {
			return "absent"
		}
		return strconv.FormatFloat(*p, 'f', -1, 64)
	}
	want := []struct{ uuid, typ, title, section, progress string }{
		{"a1", "library.update.section", "Scanning Movies", "3", "42.5"},
		{"a2", "media.generate.credits", "Detecting Credits", "4", "-1"},
		{"a3", "provider.epg.load", "Refreshing EPG", "", "absent"},
		{"a4", "butler.task", "", "", "7"},
		{"a5", "x", "", "", "absent"},
		{"a6", "x", "", "s-6", "absent"},
	}
	for i, w := range want {
		a := got[i]
		if a.UUID != w.uuid || a.Type != w.typ || a.Title != w.title || a.LibrarySectionID != w.section || prog(a.Progress) != w.progress {
			t.Errorf("Activities()[%d] = {%q %q %q section %q progress %s}, want {%q %q %q section %q progress %s}",
				i, a.UUID, a.Type, a.Title, a.LibrarySectionID, prog(a.Progress), w.uuid, w.typ, w.title, w.section, w.progress)
		}
	}
	if got[0].Subtitle != "x" {
		t.Errorf("Subtitle = %q, want x", got[0].Subtitle)
	}
}

func TestUpdateStatus(t *testing.T) {
	const secret = "secret-token-value"
	srv, seen := fixtureServer(t, map[string]string{
		"/updater/status": `{"MediaContainer":{"canInstall":false,"checkedAt":1715109491,
			"downloadURL":"https://plex.tv/downloads/latest/5?X-Plex-Token=` + secret + `","status":0,
			"Release":[{"key":"https://plex.tv/updater/releases/1","version":"1.43.4.1-abc","state":"available",
				"downloadURL":"https://plex.tv/d?X-Plex-Token=` + secret + `","added":"notes","fixed":"notes"}]}}`,
	})
	got, err := newTestClient(t, srv).UpdateStatus(t.Context())
	if err != nil {
		t.Fatalf("UpdateStatus() = %v", err)
	}
	if len(*seen) != 1 || (*seen)[0] != "GET /updater/status" {
		t.Errorf("requests = %v, want one GET /updater/status", *seen)
	}
	if got.CheckedAt == nil || int64(*got.CheckedAt) != 1715109491 || got.Status == nil || *got.Status != 0 {
		t.Errorf("UpdateStatus() = %+v, want checkedAt 1715109491 and status 0", got)
	}
	if len(got.Releases) != 1 || got.Releases[0].Version != "1.43.4.1-abc" || got.Releases[0].State != "available" {
		t.Errorf("Releases = %+v", got.Releases)
	}
	if dump := fmt.Sprintf("%+v %+v", *got, got.Releases); strings.Contains(dump, secret) {
		t.Errorf("decoded UpdateStatus carries the token: %s", dump)
	}
}

func TestUpdateStatusAbsentFields(t *testing.T) {
	srv, _ := fixtureServer(t, map[string]string{"/updater/status": `{"MediaContainer":{}}`})
	got, err := newTestClient(t, srv).UpdateStatus(t.Context())
	if err != nil {
		t.Fatalf("UpdateStatus() = %v", err)
	}
	if got.CheckedAt != nil || got.Status != nil || len(got.Releases) != 0 {
		t.Errorf("UpdateStatus() of an empty container = %+v, want nil fields", got)
	}
}

func TestUpdateStatusNotFound(t *testing.T) {
	srv, _ := fixtureServer(t, map[string]string{})
	if _, err := newTestClient(t, srv).UpdateStatus(t.Context()); !errors.Is(err, ErrNotFound) {
		t.Errorf("UpdateStatus() on 404 = %v, want ErrNotFound", err)
	}
}
