package plexapi

import (
	"context"
	"iter"
	"net/http"
)

// Sessions returns the currently playing sessions (GET /status/sessions).
// Session items populate User, Player, Session, TranscodeSession, and the
// Media graph; a direct-play session has no TranscodeSession.
func (c *Client) Sessions(ctx context.Context) ([]Item, error) {
	return c.FetchMetadata[Item](ctx, SessionsPath())
}

// History returns watch-history entries viewed at or after sinceUnix,
// newest first, filtered server-side (see HistoryPath for the
// literal-operator wire contract).
func (c *Client) History(ctx context.Context, sinceUnix int64) ([]Item, error) {
	return c.FetchMetadata[Item](ctx, HistoryPath(sinceUnix))
}

// WalkHistory pages through watch history viewed at or after sinceUnix,
// oldest first (see HistoryPagePath), from page.Start, page.Size rows per
// request, under the general read cap. It stops, fails and calls a non-nil
// wait as WalkSectionItems does.
func (c *Client) WalkHistory(ctx context.Context, sinceUnix int64, page Page, wait func(context.Context) error) iter.Seq2[HistoryEntry, error] {
	return walkPages(ctx, page, wait, func(ctx context.Context, p Page) ([]HistoryEntry, int64, error) {
		var resp MC[struct {
			Metadata  []historyRow `json:"Metadata"`
			TotalSize int64        `json:"totalSize"`
		}]
		path := historyPagePath(sinceUnix, p) // walkPages validated the page
		if err := c.do(ctx, http.MethodGet, string(path), c.maxBody, &resp); err != nil {
			return nil, 0, err
		}
		entries := make([]HistoryEntry, 0, len(resp.MediaContainer.Metadata))
		for _, r := range resp.MediaContainer.Metadata {
			entries = append(entries, r.entry())
		}
		return entries, resp.MediaContainer.TotalSize, nil
	})
}

// maxHistoryKeyBytes bounds HistoryEntry.HistoryKey. A longer key is
// dropped rather than cut, because a cut key could collide with another.
const maxHistoryKeyBytes = 512

type historyRow struct {
	RatingKey  string    `json:"ratingKey"`
	HistoryKey string    `json:"historyKey"`
	ViewedAt   FlexInt64 `json:"viewedAt"`
	AccountID  FlexInt64 `json:"accountID"`
}

func (r *historyRow) entry() HistoryEntry {
	e := HistoryEntry{RatingKey: r.RatingKey, ViewedAt: int64(r.ViewedAt), AccountID: int64(r.AccountID)}
	if len(r.HistoryKey) <= maxHistoryKeyBytes {
		e.HistoryKey = r.HistoryKey
	}
	return e
}
