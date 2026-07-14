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

// MyPlexUsername returns the username of the plex.tv account the server is
// signed into (GET /myplex/account).
func (c *Client) MyPlexUsername(ctx context.Context) (string, error) {
	var resp struct {
		Username string `json:"username"`
	}
	if err := c.Get(ctx, "/myplex/account", &resp); err != nil {
		return "", err
	}
	return resp.Username, nil
}

// AdminAccount resolves the server's admin system account by matching the
// myplex username against the system accounts list.
func (c *Client) AdminAccount(ctx context.Context) (*Account, error) {
	username, err := c.MyPlexUsername(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetching account: %w", err)
	}
	accounts, err := c.Accounts(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetching system accounts: %w", err)
	}
	for _, a := range accounts {
		if a.Name == username {
			return &a, nil
		}
	}
	return nil, fmt.Errorf("admin user %q not found in system accounts", username)
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
