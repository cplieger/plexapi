package plexapi

import "fmt"

// Path builders own the wire grammar for every endpoint the typed surface
// models: the endpoint paths, Plex's literal single-character filter
// operators, the rating-key validation applied before any URL
// interpolation, and — through the Path/ListPath return types — the read-cap
// class each endpoint decodes under. Consumers that decode into their own
// domain types (via FetchMetadata / FetchMetadataList / FetchDirectory, or
// Get) compose these builders instead of re-owning path strings — a filter,
// path, or cap-class fix then lands in one place, not in every importing
// repo.
//
// The `>=` in the history and recently-added filters is a wire contract:
// one literal `>`, unencoded. Plex silently ignores a malformed (`>>=`) or
// URL-encoded operator and returns the UNFILTERED listing, which on a large
// server blows the read cap; Go's url.Parse preserves the literal form.

// Path is a server-relative endpoint path whose response decodes under the
// general read cap (WithMaxBodyBytes). The builder returning it decides the
// cap class, so a call site cannot silently read a listing-sized endpoint
// under the smaller cap or vice versa; construct one explicitly
// (plexapi.Path("/x")) only for an endpoint no builder models.
type Path string

// ListPath is a server-relative full-listing endpoint path whose response
// decodes under the large-listing read cap (WithMaxListBodyBytes) — section
// listings are an order of magnitude larger than any other Plex response.
// Produced by the listing builders (SectionItemsPath, RecentlyAddedPath);
// FetchMetadataList accepts only this type.
type ListPath string

// SessionsPath returns the active-sessions endpoint path
// (GET /status/sessions).
func SessionsPath() Path { return "/status/sessions" }

// SectionsPath returns the library-sections directory endpoint path
// (GET /library/sections).
func SectionsPath() Path { return "/library/sections" }

// HistoryPath returns the watch-history endpoint path filtered server-side
// to entries viewed at or after sinceUnix, newest first. The filter is
// literally `viewedAt>=N` (see the package comment on operator encoding).
// It is deliberately a general-cap Path, not a ListPath: a filtered history
// window fits the general cap, and an over-cap error here is the tripwire
// for the malformed-operator failure mode (Plex answering with the FULL
// unfiltered history).
func HistoryPath(sinceUnix int64) Path {
	return Path(fmt.Sprintf("/status/sessions/history/all?sort=viewedAt:desc&viewedAt>=%d", sinceUnix))
}

// SectionItemsPath returns the full-listing endpoint path for a library
// section, validating the section key first.
func SectionItemsPath(section RatingKey) (ListPath, error) {
	if err := section.Validate(); err != nil {
		return "", err
	}
	return ListPath("/library/sections/" + section.String() + "/all"), nil
}

// RecentlyAddedPath returns a section's listing path filtered server-side
// to items of metadataType added at or after sinceUnix, newest first. The
// filter is literally `addedAt>=%d` (see the package comment on operator
// encoding). A recently-added window is a section listing (ListPath): a
// generous window on a large section outgrows the general cap.
func RecentlyAddedPath(section RatingKey, metadataType int, sinceUnix int64) (ListPath, error) {
	if err := section.Validate(); err != nil {
		return "", err
	}
	return ListPath(fmt.Sprintf("/library/sections/%s/all?type=%d&sort=addedAt:desc&addedAt>=%d",
		section.String(), metadataType, sinceUnix)), nil
}

// MetadataPath returns the metadata endpoint path for one library item,
// validating the key first. The endpoint is polymorphic: the same path
// returns a movie, show, season, or episode depending on the key.
func MetadataPath(key RatingKey) (Path, error) {
	if err := key.Validate(); err != nil {
		return "", err
	}
	return Path("/library/metadata/" + key.String()), nil
}

// ChildrenPath returns the direct-children endpoint path for an item (a
// show's seasons, a season's episodes), validating the key first.
func ChildrenPath(key RatingKey) (Path, error) {
	if err := key.Validate(); err != nil {
		return "", err
	}
	return Path("/library/metadata/" + key.String() + "/children"), nil
}

// AllLeavesPath returns the leaf-descendants endpoint path for an item
// (every episode of a show), validating the key first.
func AllLeavesPath(key RatingKey) (Path, error) {
	if err := key.Validate(); err != nil {
		return "", err
	}
	return Path("/library/metadata/" + key.String() + "/allLeaves"), nil
}
