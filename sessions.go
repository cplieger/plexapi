package plexapi

import "context"

// Sessions returns the currently playing sessions (GET /status/sessions).
// Session items populate User, Player, Session, TranscodeSession, and the
// Media graph; a direct-play session has no TranscodeSession.
func (c *Client) Sessions(ctx context.Context) ([]Item, error) {
	return FetchMetadata[Item](ctx, c, SessionsPath())
}

// History returns watch-history entries viewed at or after sinceUnix,
// newest first, filtered server-side (see HistoryPath for the
// literal-operator wire contract: one `>`, unencoded — Plex silently
// ignores a malformed operator and returns the FULL history).
func (c *Client) History(ctx context.Context, sinceUnix int64) ([]Item, error) {
	return FetchMetadata[Item](ctx, c, HistoryPath(sinceUnix))
}
