package plexapi

import (
	"context"
	"fmt"
)

// Sessions returns the currently playing sessions (GET /status/sessions).
// Session items populate User, Player, Session, TranscodeSession, and the
// Media graph; a direct-play session has no TranscodeSession.
func (c *Client) Sessions(ctx context.Context) ([]Item, error) {
	return fetchMetadata[Item](ctx, c, "/status/sessions", c.maxBody)
}

// History returns watch-history entries viewed at or after sinceUnix,
// newest first, filtered server-side.
//
// The filter is literally `viewedAt>=N` — one `>`, unencoded. Plex silently
// ignores a malformed operator (`>>=`, or a URL-encoded one) and returns
// the FULL history, which on a long-lived server is tens of thousands of
// entries and blows the read cap; the single-character form is a wire
// contract, preserved verbatim by url.Parse.
func (c *Client) History(ctx context.Context, sinceUnix int64) ([]Item, error) {
	path := fmt.Sprintf("/status/sessions/history/all?sort=viewedAt:desc&viewedAt>=%d", sinceUnix)
	return fetchMetadata[Item](ctx, c, path, c.maxBody)
}
