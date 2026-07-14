package plexapi

import (
	"context"
	"fmt"
)

// SetAudioStream selects the audio stream for a media part.
//
// Plex records stream-selection writes against the REQUESTING TOKEN's user
// (unlike reads, which are not user-scoped): selecting for another user
// requires that user's token — use ForToken. Falling back to the admin
// token writes to the admin's view and silently drops the target user's
// preference. Mutations are applied at most once (never retried).
func (c *Client) SetAudioStream(ctx context.Context, partID, streamID int) error {
	return c.put(ctx, fmt.Sprintf("/library/parts/%d?audioStreamID=%d&allParts=1", partID, streamID))
}

// SetSubtitleStream selects the subtitle stream for a media part. Same
// user-scoping contract as SetAudioStream.
func (c *Client) SetSubtitleStream(ctx context.Context, partID, streamID int) error {
	return c.put(ctx, fmt.Sprintf("/library/parts/%d?subtitleStreamID=%d&allParts=1", partID, streamID))
}

// DisableSubtitles turns subtitles off for a media part (stream ID 0).
func (c *Client) DisableSubtitles(ctx context.Context, partID int) error {
	return c.SetSubtitleStream(ctx, partID, 0)
}
