package plexapi

import "fmt"

// Path builders own the wire grammar for every endpoint the typed surface
// models: the endpoint paths, Plex's literal single-character filter
// operators, and the rating-key validation applied before any URL
// interpolation. Consumers that decode into their own domain types (via
// FetchMetadata / FetchMetadataList / FetchDirectory, or Get) compose these
// builders instead of re-owning path strings — a filter or path fix then
// lands in one place, not in every importing repo.
//
// The `>=` in the history and recently-added filters is a wire contract:
// one literal `>`, unencoded. Plex silently ignores a malformed (`>>=`) or
// URL-encoded operator and returns the UNFILTERED listing, which on a large
// server blows the read cap; Go's url.Parse preserves the literal form.

// SessionsPath returns the active-sessions endpoint path
// (GET /status/sessions).
func SessionsPath() string { return "/status/sessions" }

// SectionsPath returns the library-sections directory endpoint path
// (GET /library/sections).
func SectionsPath() string { return "/library/sections" }

// HistoryPath returns the watch-history endpoint path filtered server-side
// to entries viewed at or after sinceUnix, newest first. The filter is
// literally `viewedAt>=N` (see the package comment on operator encoding).
func HistoryPath(sinceUnix int64) string {
	return fmt.Sprintf("/status/sessions/history/all?sort=viewedAt:desc&viewedAt>=%d", sinceUnix)
}

// SectionItemsPath returns the full-listing endpoint path for a library
// section, validating the section key first.
func SectionItemsPath(section RatingKey) (string, error) {
	if err := section.Validate(); err != nil {
		return "", err
	}
	return "/library/sections/" + section.String() + "/all", nil
}

// RecentlyAddedPath returns a section's listing path filtered server-side
// to items of metadataType added at or after sinceUnix, newest first. The
// filter is literally `addedAt>=N` (see the package comment on operator
// encoding).
func RecentlyAddedPath(section RatingKey, metadataType int, sinceUnix int64) (string, error) {
	if err := section.Validate(); err != nil {
		return "", err
	}
	return fmt.Sprintf("/library/sections/%s/all?type=%d&sort=addedAt:desc&addedAt>=%d",
		section.String(), metadataType, sinceUnix), nil
}

// MetadataPath returns the metadata endpoint path for one library item,
// validating the key first. The endpoint is polymorphic: the same path
// returns a movie, show, season, or episode depending on the key.
func MetadataPath(key RatingKey) (string, error) {
	if err := key.Validate(); err != nil {
		return "", err
	}
	return "/library/metadata/" + key.String(), nil
}

// ChildrenPath returns the direct-children endpoint path for an item (a
// show's seasons, a season's episodes), validating the key first.
func ChildrenPath(key RatingKey) (string, error) {
	if err := key.Validate(); err != nil {
		return "", err
	}
	return "/library/metadata/" + key.String() + "/children", nil
}

// AllLeavesPath returns the leaf-descendants endpoint path for an item
// (every episode of a show), validating the key first.
func AllLeavesPath(key RatingKey) (string, error) {
	if err := key.Validate(); err != nil {
		return "", err
	}
	return "/library/metadata/" + key.String() + "/allLeaves", nil
}
