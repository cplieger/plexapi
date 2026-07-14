package plexapi

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

// Sections returns every library section. Filter by Section.Type
// (SectionTypeMovie, SectionTypeShow) app-side.
func (c *Client) Sections(ctx context.Context) ([]Section, error) {
	return fetchDirectory[Section](ctx, c, "/library/sections")
}

// SectionItems returns all items in a library section — the full listing
// used to index a library (rating keys, titles, years, GUIDs). Uses the
// large-body cap: a big section's listing is far larger than any other
// Plex response.
func (c *Client) SectionItems(ctx context.Context, sectionKey RatingKey) ([]Item, error) {
	if err := sectionKey.Validate(); err != nil {
		return nil, err
	}
	return fetchMetadata[Item](ctx, c, "/library/sections/"+sectionKey.String()+"/all", maxListBodyBytes)
}

// RecentlyAdded returns a section's items of the given metadata type added
// at or after sinceUnix, newest first, filtered server-side.
//
// The filter operator is literally `addedAt>=` — a single `>`, unencoded.
// Plex silently ignores a malformed or URL-encoded operator and returns the
// UNFILTERED listing, which on a large library blows the read cap; Go's
// url.Parse preserves the literal `>=` on the wire.
func (c *Client) RecentlyAdded(ctx context.Context, sectionKey RatingKey, metadataType int, sinceUnix int64) ([]Item, error) {
	if err := sectionKey.Validate(); err != nil {
		return nil, err
	}
	path := fmt.Sprintf("/library/sections/%s/all?type=%d&sort=addedAt:desc&addedAt>=%d",
		sectionKey.String(), metadataType, sinceUnix)
	return fetchMetadata[Item](ctx, c, path, maxListBodyBytes)
}

// Metadata fetches one library item by rating key. The endpoint is
// polymorphic: the same call returns a movie, show, season, or episode
// Item depending on what the key addresses. Returns ErrNotFound when the
// key no longer exists.
func (c *Client) Metadata(ctx context.Context, key RatingKey) (*Item, error) {
	items, err := c.metadataList(ctx, key, "")
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
	return c.metadataList(ctx, key, "/children")
}

// AllLeaves returns an item's leaf descendants (every episode of a show).
func (c *Client) AllLeaves(ctx context.Context, key RatingKey) ([]Item, error) {
	return c.metadataList(ctx, key, "/allLeaves")
}

// metadataList is the shared /library/metadata/{key}[suffix] fetch.
func (c *Client) metadataList(ctx context.Context, key RatingKey, suffix string) ([]Item, error) {
	if err := key.Validate(); err != nil {
		return nil, err
	}
	return fetchMetadata[Item](ctx, c, "/library/metadata/"+key.String()+suffix, maxBodyBytes)
}

// ItemExists reports whether the rating key currently addresses an item:
// (true, nil) on 200, (false, nil) on a definitive 404. Any other failure
// (auth, rate limit, 5xx, transport) returns an error — existence could not
// be determined, and callers deciding "is this item stale?" must fail
// closed on it. The body is discarded without decoding.
func (c *Client) ItemExists(ctx context.Context, key RatingKey) (bool, error) {
	if err := key.Validate(); err != nil {
		return false, err
	}
	err := c.do(ctx, http.MethodGet, "/library/metadata/"+key.String(), maxBodyBytes, nil)
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
	return fetchMetadata[Item](ctx, c, "/library/all?"+url.Values{"guid": {guid}}.Encode(), maxBodyBytes)
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

// ContainerTotalSize returns the totalSize of the container at path
// (typically a /library/sections/{key}/all?type=N filter) by requesting a
// single item and reading the totalSize field. The body field is used
// rather than the X-Plex-Container-Total-Size header because the header is
// not populated for type-filtered queries.
func (c *Client) ContainerTotalSize(ctx context.Context, path string) (int64, error) {
	req, err := url.Parse(path)
	if err != nil {
		return 0, fmt.Errorf("parsing path %q: %w", path, err)
	}
	q := req.Query()
	q.Set("X-Plex-Container-Start", "0")
	q.Set("X-Plex-Container-Size", "1")
	req.RawQuery = q.Encode()

	var resp MC[struct {
		TotalSize int64 `json:"totalSize"`
	}]
	if err := c.Get(ctx, req.String(), &resp); err != nil {
		return 0, err
	}
	return resp.MediaContainer.TotalSize, nil
}
