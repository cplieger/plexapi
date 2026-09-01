package plexapi

import (
	"context"
	"fmt"
)

// StreamSelection names which stream to select on which media part. The two
// IDs are a struct rather than two adjacent ints because a transposition
// type-checked and produced a request against a part that does not exist —
// detectable only after the round trip, as a Plex error about the wrong
// object.
//
// Both fields are required. The zero value selects nothing meaningful: part
// 0 is not a Plex part id, so a zero StreamSelection reaches the server and
// is refused there rather than silently retargeting.
type StreamSelection struct {
	// PartID is the media part whose stream selection changes (Part.ID).
	PartID int
	// StreamID is the stream to select (Stream.ID). Zero disables the track
	// for a subtitle selection; see DisableSubtitles for the named form.
	StreamID int
}

// SetAudioStream selects the audio stream for a media part.
//
// Plex records stream-selection writes against the REQUESTING TOKEN's user
// (unlike reads, which are not user-scoped): selecting for another user
// requires that user's token — use ForToken. Mutations are applied at most
// once (never retried).
func (c *Client) SetAudioStream(ctx context.Context, sel StreamSelection) error {
	return c.put(ctx, fmt.Sprintf("/library/parts/%d?audioStreamID=%d&allParts=1", sel.PartID, sel.StreamID))
}

// SetSubtitleStream selects the subtitle stream for a media part. Same
// user-scoping contract as SetAudioStream.
func (c *Client) SetSubtitleStream(ctx context.Context, sel StreamSelection) error {
	return c.put(ctx, fmt.Sprintf("/library/parts/%d?subtitleStreamID=%d&allParts=1", sel.PartID, sel.StreamID))
}

// DisableSubtitles turns subtitles off for a media part (stream ID 0).
func (c *Client) DisableSubtitles(ctx context.Context, partID int) error {
	return c.SetSubtitleStream(ctx, StreamSelection{PartID: partID, StreamID: 0})
}
