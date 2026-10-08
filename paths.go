package plexapi

import (
	"fmt"
	"net/url"
	"strconv"
)

// Path builders own the wire grammar for every endpoint the typed surface
// models: the endpoint paths, Plex's literal single-character filter
// operators, the rating-key validation applied before any URL
// interpolation, and — through the Path/ListPath return types — the read-cap
// class each endpoint decodes under.
//
// The `>=` in the history and recently-added filters is a wire contract:
// one literal `>`, unencoded. Plex silently ignores a malformed (`>>=`) or
// URL-encoded operator and returns the UNFILTERED listing, which on a large
// server blows the read cap; Go's url.Parse preserves the literal form.

// Path is a server-relative endpoint path whose response decodes under the
// general read cap (WithMaxBodyBytes). Construct one explicitly
// (plexapi.Path("/x")) only for an endpoint no builder models.
type Path string

// ListPath is a server-relative full-listing endpoint path whose response
// decodes under the large-listing read cap (WithMaxListBodyBytes).
// Produced by the listing builders (SectionItemsPath, SectionItemsPagePath,
// RecentlyAddedPath); Client.FetchMetadataList accepts only this type.
type ListPath string

// SessionsPath returns the active-sessions endpoint path
// (GET /status/sessions).
func SessionsPath() Path { return "/status/sessions" }

// SectionsPath returns the library-sections directory endpoint path
// (GET /library/sections).
func SectionsPath() Path { return "/library/sections" }

// HistoryPath returns the watch-history endpoint path filtered server-side
// to entries viewed at or after sinceUnix, newest first. It is
// deliberately a general-cap Path, not a ListPath: an over-cap error here
// is the tripwire for the malformed-operator failure mode (Plex answering
// with the FULL unfiltered history).
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
// to items of metadataType added at or after sinceUnix, newest first. A
// recently-added window is a section listing (ListPath): a generous window
// on a large section outgrows the general cap.
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

// Page selects an offset window of a paged listing: Start is the offset of
// the first row (X-Plex-Container-Start, >= 0) and Size the row count per
// request (X-Plex-Container-Size, > 0). A walk starts at Start and reads
// Size rows per request.
type Page struct {
	Start int
	Size  int
}

func (p Page) validate() error {
	if p.Start < 0 || p.Size <= 0 {
		return fmt.Errorf("invalid page: start %d, size %d", p.Start, p.Size)
	}
	return nil
}

// SectionItemsPagePath returns one page of a section listing; metadataType
// > 0 adds a ?type= filter (0 is unfiltered). It validates the section key
// and the page.
func SectionItemsPagePath(section RatingKey, metadataType int, page Page) (ListPath, error) {
	path, err := SectionItemsPath(section)
	if err != nil {
		return "", err
	}
	if err := page.validate(); err != nil {
		return "", err
	}
	q := url.Values{}
	if metadataType > 0 {
		q.Set("type", strconv.Itoa(metadataType))
	}
	q.Set("X-Plex-Container-Start", strconv.Itoa(page.Start))
	q.Set("X-Plex-Container-Size", strconv.Itoa(page.Size))
	return path + ListPath("?"+q.Encode()), nil
}

// HistoryPagePath returns one page of watch history viewed at or after
// sinceUnix, oldest first, so plays recorded during a walk land after the
// read position instead of shifting earlier pages. It validates the page.
// A general-cap Path for the reason given on HistoryPath.
func HistoryPagePath(sinceUnix int64, page Page) (Path, error) {
	if err := page.validate(); err != nil {
		return "", err
	}
	return historyPagePath(sinceUnix, page), nil
}

func historyPagePath(sinceUnix int64, page Page) Path {
	return Path(fmt.Sprintf("/status/sessions/history/all?sort=viewedAt:asc&viewedAt>=%d&X-Plex-Container-Start=%d&X-Plex-Container-Size=%d",
		sinceUnix, page.Start, page.Size))
}
