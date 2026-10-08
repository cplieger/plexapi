package plexapi

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
)

// Identity returns the server identity from GET / (name, machine ID,
// version, platform, Plex Pass subscription, active transcode count).
func (c *Client) Identity(ctx context.Context) (*ServerIdentity, error) {
	var resp MC[ServerIdentity]
	if err := c.Get(ctx, "/", &resp); err != nil {
		return nil, err
	}
	return &resp.MediaContainer, nil
}

// Accounts returns the server's system accounts (GET /accounts): the
// local account IDs history entries reference.
func (c *Client) Accounts(ctx context.Context) ([]Account, error) {
	var resp MC[struct {
		Account []Account `json:"Account"`
	}]
	if err := c.Get(ctx, "/accounts", &resp); err != nil {
		return nil, err
	}
	return resp.MediaContainer.Account, nil
}

// ownerAccountID is the server-local system-account id Plex reserves for
// the server owner in GET /accounts (id 0 is the managed placeholder).
const ownerAccountID = 1

// AdminAccount resolves the server's admin (owner) system account: the
// owner is always account id 1 in the system accounts list.
//
// It deliberately does not consult /myplex/account: that endpoint's
// username decodes as "" and previously name-matched the id-0 placeholder
// account, so consumers comparing session user ids against the admin id
// skipped every owner event. Verified live 2026-07 against Plex 1.43.3.
func (c *Client) AdminAccount(ctx context.Context) (*Account, error) {
	accounts, err := c.Accounts(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetching system accounts: %w", err)
	}
	for _, a := range accounts {
		if a.ID == ownerAccountID {
			return &a, nil
		}
	}
	return nil, fmt.Errorf("owner account (id %d) not found in system accounts", ownerAccountID)
}

// Providers returns the media-provider tree (GET /media/providers with
// storage rollups) — per-library duration and storage totals.
func (c *Client) Providers(ctx context.Context) (*MediaProviders, error) {
	var resp MC[MediaProviders]
	if err := c.Get(ctx, "/media/providers?includeStorage=1", &resp); err != nil {
		return nil, err
	}
	return &resp.MediaContainer, nil
}

// StatisticsResources returns host CPU/memory samples from the Plex Pass
// endpoint /statistics/resources over the trailing timespan bucket. The
// endpoint 404s (ErrNotFound) without Plex Pass; callers degrade
// gracefully.
func (c *Client) StatisticsResources(ctx context.Context, timespan int) ([]StatisticsResource, error) {
	var resp MC[struct {
		StatisticsResources []StatisticsResource `json:"StatisticsResources"`
	}]
	if err := c.Get(ctx, "/statistics/resources?timespan="+strconv.Itoa(timespan), &resp); err != nil {
		return nil, err
	}
	return resp.MediaContainer.StatisticsResources, nil
}

// StatisticsBandwidth returns bandwidth samples from the Plex Pass endpoint
// /statistics/bandwidth. 404s (ErrNotFound) without Plex Pass.
func (c *Client) StatisticsBandwidth(ctx context.Context, timespan int) ([]StatisticsBandwidth, error) {
	var resp MC[struct {
		StatisticsBandwidth []StatisticsBandwidth `json:"StatisticsBandwidth"`
	}]
	if err := c.Get(ctx, "/statistics/bandwidth?timespan="+strconv.Itoa(timespan), &resp); err != nil {
		return nil, err
	}
	return resp.MediaContainer.StatisticsBandwidth, nil
}

// Activities returns the server's running background tasks
// (GET /activities). An activity whose Context is absent or not an object
// decodes with an empty LibrarySectionID rather than failing the call.
func (c *Client) Activities(ctx context.Context) ([]Activity, error) {
	var resp MC[struct {
		Activity []activityRow `json:"Activity"`
	}]
	if err := c.Get(ctx, "/activities", &resp); err != nil {
		return nil, err
	}
	out := make([]Activity, 0, len(resp.MediaContainer.Activity))
	for i := range resp.MediaContainer.Activity {
		r := &resp.MediaContainer.Activity[i]
		out = append(out, Activity{
			Progress:         r.Progress,
			UUID:             r.UUID,
			Type:             r.Type,
			Title:            r.Title,
			Subtitle:         r.Subtitle,
			LibrarySectionID: contextSectionID(r.Context),
		})
	}
	return out, nil
}

// activityRow is the wire shape of one activity. Context is free-form
// (the spec types it as an object with any keys), so it is kept raw.
type activityRow struct {
	Progress *float64        `json:"progress"`
	UUID     string          `json:"uuid"`
	Type     string          `json:"type"`
	Title    string          `json:"title"`
	Subtitle string          `json:"subtitle"`
	Context  json.RawMessage `json:"Context"`
}

// contextSectionID reads librarySectionID from an activity Context: a
// string verbatim, a number as its literal, anything else as "".
func contextSectionID(raw json.RawMessage) string {
	var fields struct {
		LibrarySectionID json.RawMessage `json:"librarySectionID"`
	}
	if json.Unmarshal(raw, &fields) != nil {
		return ""
	}
	var s string
	if json.Unmarshal(fields.LibrarySectionID, &s) == nil {
		return s
	}
	var n json.Number
	if json.Unmarshal(fields.LibrarySectionID, &n) == nil {
		return n.String()
	}
	return ""
}

// UpdateStatus returns the server's update check (GET /updater/status).
// ErrNotFound when the server does not expose the updater.
func (c *Client) UpdateStatus(ctx context.Context) (*UpdateStatus, error) {
	var resp MC[UpdateStatus]
	if err := c.Get(ctx, "/updater/status", &resp); err != nil {
		return nil, err
	}
	return &resp.MediaContainer, nil
}
