package plexapi

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// credentialMarkers are the header, parameter and field names a Plex or arr
// credential travels under; a recorded answer holding one leaks it.
var credentialMarkers = []string{"x-plex-token", "x-api-key", "apikey=", "accesstoken", "authtoken"}

// placeholders are the values testdata/README.md's scrub writes in place of
// each server-identifying field.
var placeholders = map[string]func(string) bool{
	"machineIdentifier":  func(v string) bool { return v == "machine-id-0" },
	"clientIdentifier":   func(v string) bool { return v == "client-id-0" },
	"uuid":               isPlaceholderUUID,
	"librarySectionUUID": isPlaceholderUUID,
}

func isPlaceholderUUID(v string) bool { return strings.HasPrefix(v, "00000000-0000-4000-8000-") }

func TestFixturesHoldNoSecret(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no recorded answers under testdata")
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			lower := strings.ToLower(string(b))
			for _, m := range credentialMarkers {
				if strings.Contains(lower, m) {
					t.Errorf("holds %q", m)
				}
			}
			var doc any
			if err := json.Unmarshal(b, &doc); err != nil {
				t.Fatalf("not JSON: %v", err)
			}
			walkFields(doc, func(key, value string) {
				if isPlaceholder, scrubbed := placeholders[key]; scrubbed && !isPlaceholder(value) {
					t.Errorf("%s %q is not a scrub placeholder", key, value)
				}
			})
		})
	}
}

func walkFields(doc any, fn func(key, value string)) {
	switch v := doc.(type) {
	case map[string]any:
		for k, e := range v {
			if s, ok := e.(string); ok {
				fn(k, s)
				continue
			}
			walkFields(e, fn)
		}
	case []any:
		for _, e := range v {
			walkFields(e, fn)
		}
	}
}
