package plexapi

import (
	"context"
	"iter"
	"net/http"
	"net/url"
	"strconv"
)

// Sections returns every library section. Filter by Section.Type
// (SectionTypeMovie, SectionTypeShow) app-side.
func (c *Client) Sections(ctx context.Context) ([]Section, error) {
	return c.FetchDirectory[Section](ctx, SectionsPath())
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
	return c.FetchMetadataList[Item](ctx, path)
}

// SectionItemsPage returns one page of a section listing (see
// SectionItemsPagePath) and the section's totalSize, 0 when Plex omits it.
// It decodes under the list cap.
func (c *Client) SectionItemsPage(ctx context.Context, section RatingKey, metadataType int, page Page) ([]Item, int64, error) {
	path, err := SectionItemsPagePath(section, metadataType, page)
	if err != nil {
		return nil, 0, err
	}
	var resp MC[struct {
		Metadata  []Item `json:"Metadata"`
		TotalSize int64  `json:"totalSize"`
	}]
	if err := c.do(ctx, http.MethodGet, string(path), c.maxListBody, &resp); err != nil {
		return nil, 0, err
	}
	return resp.MediaContainer.Metadata, resp.MediaContainer.TotalSize, nil
}

// WalkSectionItems pages through a section listing from page.Start,
// page.Size items per request with one request in flight; metadataType
// filters as in SectionItemsPagePath. It ends at an empty page or after
// totalSize items. A listing that is still growing ends with an error once
// the walk has read totalSize+page.Size rows, counted from the first page's
// totalSize; the page that crosses that count is read in full. The first
// error is yielded once, an incomplete walk and a cancelled ctx included. An item added or removed mid-walk can be skipped or read twice.
// A non-nil wait is called immediately before every page request, so a
// caller can pace the walk's requests; its error ends the walk the same way.
func (c *Client) WalkSectionItems(ctx context.Context, section RatingKey, metadataType int, page Page, wait func(context.Context) error) iter.Seq2[Item, error] {
	return walkPages(ctx, page, wait, func(ctx context.Context, p Page) ([]Item, int64, error) {
		return c.SectionItemsPage(ctx, section, metadataType, p)
	})
}

// RecentlyAdded returns a section's items of the given metadata type added
// at or after sinceUnix, newest first, filtered server-side (see
// RecentlyAddedPath for the literal-operator wire contract).
func (c *Client) RecentlyAdded(ctx context.Context, sectionKey RatingKey, metadataType int, sinceUnix int64) ([]Item, error) {
	path, err := RecentlyAddedPath(sectionKey, metadataType, sinceUnix)
	if err != nil {
		return nil, err
	}
	return c.FetchMetadataList[Item](ctx, path)
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
	items, err := c.FetchMetadata[Item](ctx, path)
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
	return c.FetchMetadata[Item](ctx, path)
}

// AllLeaves returns an item's leaf descendants (every episode of a show).
func (c *Client) AllLeaves(ctx context.Context, key RatingKey) ([]Item, error) {
	path, err := AllLeavesPath(key)
	if err != nil {
		return nil, err
	}
	return c.FetchMetadata[Item](ctx, path)
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
	err = c.do(ctx, http.MethodGet, string(path), c.maxBody, nil)
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
	return c.FetchMetadata[Item](ctx, Path("/library/all?"+url.Values{"guid": {guid}}.Encode()))
}

// ShowForEpisodeGUID resolves an episode GUID to the rating key of the show
// currently containing it. Returns ("", nil) when the GUID matches nothing
// or when matches disagree on their show (an ambiguous GUID that must not
// drive a decision); a non-nil error means the lookup could not be
// completed.
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

// CountSectionItems returns the number of items in a library section,
// optionally filtered to one metadata type (metadataType > 0 adds ?type=N;
// 0 means unfiltered). It requests a single item and reads the container's
// totalSize body field — the X-Plex-Container-Total-Size header is used
// nowhere because it is not populated for type-filtered queries.
func (c *Client) CountSectionItems(ctx context.Context, section RatingKey, metadataType int) (int64, error) {
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
	if err := c.Get(ctx, string(path)+"?"+q.Encode(), &resp); err != nil {
		return 0, err
	}
	return resp.MediaContainer.TotalSize, nil
}
