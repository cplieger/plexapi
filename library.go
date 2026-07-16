package plexapi

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

// Sections returns every library section. Filter by Section.Type
// (SectionTypeMovie, SectionTypeShow) app-side.
func (c *Client) Sections(ctx context.Context) ([]Section, error) {
	return FetchDirectory[Section](ctx, c, SectionsPath())
}

// SectionItems returns all items in a library section — the full listing
// used to index a library (rating keys, titles, years, GUIDs). Uses the
// large-body cap: a big section's listing is far larger than any other
// Plex response.
func (c *Client) SectionItems(ctx context.Context, sectionKey RatingKey) ([]Item, error) {
	path, err := SectionItemsPath(sectionKey)
	if err != nil {
		return nil, err
	}
	return FetchMetadataList[Item](ctx, c, path)
}

// RecentlyAdded returns a section's items of the given metadata type added
// at or after sinceUnix, newest first, filtered server-side (see
// RecentlyAddedPath for the literal-operator wire contract).
func (c *Client) RecentlyAdded(ctx context.Context, sectionKey RatingKey, metadataType int, sinceUnix int64) ([]Item, error) {
	path, err := RecentlyAddedPath(sectionKey, metadataType, sinceUnix)
	if err != nil {
		return nil, err
	}
	return FetchMetadataList[Item](ctx, c, path)
}

// Metadata fetches one library item by rating key. The endpoint is
// polymorphic: the same call returns a movie, show, season, or episode
// Item depending on what the key addresses. Returns ErrNotFound when the
// key no longer exists.
func (c *Client) Metadata(ctx context.Context, key RatingKey) (*Item, error) {
	path, err := MetadataPath(key)
	if err != nil {
		return nil, err
	}
	items, err := FetchMetadata[Item](ctx, c, path)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, ErrNotFound
	}
	return &items[0], nil
}

// Children returns an item's direct children (a show's seasons, a season's
// episodes).
func (c *Client) Children(ctx context.Context, key RatingKey) ([]Item, error) {
	path, err := ChildrenPath(key)
	if err != nil {
		return nil, err
	}
	return FetchMetadata[Item](ctx, c, path)
}

// AllLeaves returns an item's leaf descendants (every episode of a show).
func (c *Client) AllLeaves(ctx context.Context, key RatingKey) ([]Item, error) {
	path, err := AllLeavesPath(key)
	if err != nil {
		return nil, err
	}
	return FetchMetadata[Item](ctx, c, path)
}

// ItemExists reports whether the rating key currently addresses an item:
// (true, nil) on 200, (false, nil) on a definitive 404. Any other failure
// (auth, rate limit, 5xx, transport) returns an error — existence could not
// be determined, and callers deciding "is this item stale?" must fail
// closed on it. The body is discarded without decoding.
func (c *Client) ItemExists(ctx context.Context, key RatingKey) (bool, error) {
	path, err := MetadataPath(key)
	if err != nil {
		return false, err
	}
	err = c.do(ctx, http.MethodGet, path, c.maxBody, nil)
	switch {
	case err == nil:
		return true, nil
	case IsNotFound(err):
		return false, nil
	default:
		return false, err
	}
}

// ItemsByGUID returns every library item matching an external GUID
// (e.g. "plex://episode/<hash>", "imdb://tt0903747") via /library/all.
// An unknown GUID yields an empty slice (Plex answers 200 with no items).
func (c *Client) ItemsByGUID(ctx context.Context, guid string) ([]Item, error) {
	if guid == "" {
		return nil, nil
	}
	return FetchMetadata[Item](ctx, c, "/library/all?"+url.Values{"guid": {guid}}.Encode())
}

// ShowForEpisodeGUID resolves an episode GUID to the rating key of the show
// currently containing it — the handle back from a watch-history episode
// GUID to its show after a library rebuild. Returns ("", nil) when the GUID
// matches nothing or when matches disagree on their show (an ambiguous GUID
// that must not drive a decision); a non-nil error means the lookup could
// not be completed.
func (c *Client) ShowForEpisodeGUID(ctx context.Context, episodeGUID string) (string, error) {
	items, err := c.ItemsByGUID(ctx, episodeGUID)
	if err != nil {
		return "", err
	}
	show := ""
	for i := range items {
		gp := items[i].GrandparentRatingKey
		if _, err := strconv.Atoi(gp); err != nil {
			return "", nil // malformed grandparent: refuse to guess
		}
		switch {
		case show == "":
			show = gp
		case show != gp:
			return "", nil // one GUID under multiple shows: ambiguous
		}
	}
	return show, nil
}

// ContainerTotalSize returns the number of items in a library section,
// optionally filtered to one metadata type (metadataType > 0 adds ?type=N;
// 0 means unfiltered). It requests a single item and reads the container's
// totalSize body field — the X-Plex-Container-Total-Size header is used
// nowhere because it is not populated for type-filtered queries.
func (c *Client) ContainerTotalSize(ctx context.Context, section RatingKey, metadataType int) (int64, error) {
	path, err := SectionItemsPath(section)
	if err != nil {
		return 0, err
	}
	q := url.Values{}
	if metadataType > 0 {
		q.Set("type", strconv.Itoa(metadataType))
	}
	q.Set("X-Plex-Container-Start", "0")
	q.Set("X-Plex-Container-Size", "1")

	var resp MC[struct {
		TotalSize int64 `json:"totalSize"`
	}]
	if err := c.Get(ctx, path+"?"+q.Encode(), &resp); err != nil {
		return 0, err
	}
	return resp.MediaContainer.TotalSize, nil
}
