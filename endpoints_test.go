package plexapi

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fixtureServer serves canned JSON per path prefix and records requests.
func fixtureServer(t *testing.T, routes map[string]string) (*httptest.Server, *[]string) {
	t.Helper()
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.RequestURI)
		// Longest matching prefix wins (a bare "/" route would otherwise
		// shadow everything on random map order).
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

func TestContainerTotalSize(t *testing.T) {
	srv, seen := fixtureServer(t, map[string]string{
		"/library/sections/2/all": `{"MediaContainer":{"totalSize":4360}}`,
	})
	got, err := newTestClient(t, srv).ContainerTotalSize(t.Context(), "2", 4)
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
		got, err := newTestClient(t, srv2).ContainerTotalSize(t.Context(), "3", 0)
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
		if _, err := c.ContainerTotalSize(t.Context(), "3; DROP", 4); err == nil {
			t.Error("non-numeric section key accepted")
		}
	})
}

func TestSessionsDecodesSessionGraph(t *testing.T) {
	srv, _ := fixtureServer(t, map[string]string{
		"/status/sessions": `{"MediaContainer":{"Metadata":[
			{"ratingKey":"1","sessionKey":"3","title":"Movie",
			 "User":{"id":"7","title":"alice"},
			 "Player":{"device":"TV","product":"Plex for LG","state":"playing","machineIdentifier":"m1","local":true},
			 "Session":{"location":"lan","bandwidth":20000},
			 "TranscodeSession":{"videoDecision":"transcode","audioDecision":"copy"},
			 "Media":[{"videoResolution":"1080","bitrate":8000,"Part":[{"decision":"transcode"}]}]}]}}`,
	})
	got, err := newTestClient(t, srv).Sessions(t.Context())
	if err != nil {
		t.Fatal(err)
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
}

// TestIdentityAndAdminAccount pins the real /accounts shape (verified live
// 2026-07 against Plex 1.43.3): the id-0 managed placeholder with an empty
// name comes first, the owner is id 1 under the server-local display name,
// and shared users follow under their plex.tv global ids. The
// /myplex/account fixture carries the real enveloped email-form payload
// that made name-matching resolve the id-0 placeholder; the `seen`
// assertion pins that AdminAccount no longer consults it at all.
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
	if err := c.SetAudioStream(t.Context(), 10, 100); err != nil {
		t.Fatal(err)
	}
	if err := c.SetSubtitleStream(t.Context(), 10, 200); err != nil {
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
	t.Run("non-200 is StatusError", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}))
		defer srv.Close()
		_, err := NewTV("t", WithTVBaseURL(srv.URL)).SharedServers(t.Context(), "m")
		var se *StatusError
		if err == nil || !errors.As(err, &se) || se.Code != 401 {
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
