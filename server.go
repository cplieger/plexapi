package plexapi

import (
	"context"
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
// the server owner in GET /accounts (id 0 is the managed placeholder with
// an empty name). Sessions and watch history report the owner under this
// same server-local id, so resolving the admin here keeps the returned
// Account.ID in the id space consumers compare session/history ids
// against.
const ownerAccountID = 1

// AdminAccount resolves the server's admin (owner) system account: the
// owner is always account id 1 in the system accounts list.
//
// It deliberately does not consult /myplex/account. That endpoint's
// username is the plex.tv account email wrapped in a {"MyPlex":{...}}
// envelope; the previous implementation decoded a top-level username
// (silently yielding ""), name-matched it against the id-0 placeholder
// account (name=""), and returned the placeholder as the admin — so
// consumers comparing session user ids against the admin id skipped every
// owner event. Verified live 2026-07 against Plex 1.43.3.
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
