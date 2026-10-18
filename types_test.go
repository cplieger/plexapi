package plexapi

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func recordedServer(t *testing.T, name string) *httptest.Server {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestDecodeRecordedSections(t *testing.T) {
	sections, err := newTestClient(t, recordedServer(t, "sections.json")).Sections(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	types := map[string]int{}
	for _, s := range sections {
		types[s.Type]++
		if s.Key == "" || s.ScannedAt == nil || *s.ScannedAt <= 0 {
			t.Errorf("section %+v: want a key and a scannedAt", s)
		}
	}
	if types[SectionTypeMovie] != 4 || types[SectionTypeShow] != 2 || len(sections) != 7 {
		t.Errorf("section types = %v of %d, want 4 movie and 2 show of 7", types, len(sections))
	}
}

func TestDecodePartFileAndStreamKey(t *testing.T) {
	t.Run("movie with two sidecars", func(t *testing.T) {
		item, err := newTestClient(t, recordedServer(t, "movie-two-sidecars.json")).Metadata(t.Context(), "677294")
		if err != nil {
			t.Fatal(err)
		}
		if len(item.Media) != 1 || len(item.Media[0].Part) != 1 {
			t.Fatalf("media graph = %+v, want one media with one part", item.Media)
		}
		part := item.Media[0].Part[0]
		const wantFile = "/media/movies/2001 A Space Odyssey (1968) {imdb-tt0062622}/2001 A Space Odyssey (1968) {imdb-tt0062622} - [Bluray-1080p][EAC3 5.1][DV HDR10][x265]-CtrlHD.mkv"
		if part.File != wantFile {
			t.Errorf("Part.File = %q, want %q", part.File, wantFile)
		}
		var sidecars []string
		embedded := 0
		for _, s := range part.Stream {
			if s.Key == "" {
				embedded++
				continue
			}
			sidecars = append(sidecars, s.Key+" "+s.LanguageTag)
			if !s.IsSubtitle() {
				t.Errorf("stream %s with a key is type %d, want a subtitle", s.Key, s.StreamType)
			}
		}
		if want := []string{"/library/streams/4549038 en", "/library/streams/4549039 fr"}; !slices.Equal(sidecars, want) {
			t.Errorf("sidecar keys = %q, want %q", sidecars, want)
		}
		if embedded == 0 {
			t.Error("no embedded stream decoded with an empty Key")
		}
	})

	t.Run("recently added", func(t *testing.T) {
		items, err := newTestClient(t, recordedServer(t, "recently-added.json")).RecentlyAdded(t.Context(), "1", MetadataTypeMovie, 1791504000)
		if err != nil {
			t.Fatal(err)
		}
		if len(items) != 4 {
			t.Fatalf("items = %d, want 4", len(items))
		}
		for i, it := range items {
			if it.Type != SectionTypeMovie || len(it.Media) == 0 || len(it.Media[0].Part) == 0 || it.Media[0].Part[0].File == "" {
				t.Errorf("item %s: type %q, media %+v, want a movie with a part file", it.RatingKey, it.Type, it.Media)
			}
			if i > 0 && it.AddedAt > items[i-1].AddedAt {
				t.Errorf("item %s added at %d after %d, want newest first", it.RatingKey, it.AddedAt, items[i-1].AddedAt)
			}
		}
	})

	t.Run("one file under two episodes", func(t *testing.T) {
		items, err := newTestClient(t, recordedServer(t, "two-episode-file.json")).SectionItems(t.Context(), "2")
		if err != nil {
			t.Fatal(err)
		}
		if len(items) != 2 || items[0].RatingKey == items[1].RatingKey {
			t.Fatalf("items = %d, want two distinct episodes", len(items))
		}
		var files []string
		for _, it := range items {
			for _, m := range it.Media {
				for _, p := range m.Part {
					files = append(files, p.File)
				}
			}
		}
		if len(files) != 2 || files[0] == "" || files[0] != files[1] {
			t.Errorf("part files = %q, want one non-empty path under both episodes", files)
		}
	})
}

// TestDecodeUpgradedFilePair pins what Plex 1.43.4 did when an upgrade
// replaced an episode's file: the item kept its ratingKey and addedAt while
// its Media and Part got new ids and the new path, so an addedAt-based
// recently-added read does not see an upgrade.
func TestDecodeUpgradedFilePair(t *testing.T) {
	decode := func(name string) Item {
		t.Helper()
		items, err := newTestClient(t, recordedServer(t, name)).SectionItems(t.Context(), "2")
		if err != nil {
			t.Fatal(err)
		}
		if len(items) != 1 || len(items[0].Media) != 1 || len(items[0].Media[0].Part) != 1 {
			t.Fatalf("%s: want one item with one media and one part, got %+v", name, items)
		}
		return items[0]
	}
	before, after := decode("upgraded-file-before.json"), decode("upgraded-file-after.json")
	if before.RatingKey != after.RatingKey || before.Type != "episode" {
		t.Fatalf("rating keys %q and %q of type %q, want one episode", before.RatingKey, after.RatingKey, before.Type)
	}
	if before.AddedAt <= 0 || before.AddedAt != after.AddedAt {
		t.Errorf("addedAt %d then %d, want one known value kept across the upgrade", before.AddedAt, after.AddedAt)
	}
	bm, am := before.Media[0], after.Media[0]
	if bm.ID == am.ID || bm.Part[0].ID == am.Part[0].ID {
		t.Errorf("media ids %d then %d, part ids %d then %d, want new ids", bm.ID, am.ID, bm.Part[0].ID, am.Part[0].ID)
	}
	if bf, af := bm.Part[0].File, am.Part[0].File; bf == "" || af == "" || bf == af {
		t.Errorf("part files %q then %q, want two different paths", bf, af)
	}
}
