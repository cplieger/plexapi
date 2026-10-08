package plexapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
)

// MC is the generic MediaContainer envelope Plex wraps every JSON response
// in. Typed methods decode through it internally; it is exported for use
// with Get on endpoints the typed surface does not model.
type MC[T any] struct {
	MediaContainer T `json:"MediaContainer"`
}

// FlexInt decodes a Plex JSON field that may arrive as a number or a quoted
// numeric string. Null, absent, and empty-string values decode to 0.
type FlexInt int

// UnmarshalJSON accepts a JSON number, a quoted numeric string, null, or an
// empty string. Anything else (non-numeric text, floats, objects) is a
// parse error.
func (f *FlexInt) UnmarshalJSON(data []byte) error {
	if len(data) == 0 || bytes.Equal(data, []byte("null")) {
		*f = 0
		return nil
	}
	if data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return fmt.Errorf("flexint: decode string: %w", err)
		}
		if s == "" {
			*f = 0
			return nil
		}
		n, err := strconv.Atoi(s)
		if err != nil {
			return fmt.Errorf("flexint: parse %q: %w", s, err)
		}
		*f = FlexInt(n)
		return nil
	}
	var num json.Number
	if err := json.Unmarshal(data, &num); err != nil {
		return fmt.Errorf("flexint: decode number: %w", err)
	}
	n, err := strconv.Atoi(num.String())
	if err != nil {
		return fmt.Errorf("flexint: parse %s: %w", num.String(), err)
	}
	*f = FlexInt(n)
	return nil
}

// FlexInt64 is FlexInt for 64-bit values (file sizes, unix timestamps).
// Null, absent, and empty-string values decode to 0; a pointer field of
// this type stays nil when the field is absent or null.
type FlexInt64 int64

var _ json.Unmarshaler = (*FlexInt64)(nil)

// UnmarshalJSON accepts a JSON number, a quoted numeric string, null, or an
// empty string. Anything else is a parse error.
func (f *FlexInt64) UnmarshalJSON(data []byte) error {
	if len(data) == 0 || bytes.Equal(data, []byte("null")) {
		*f = 0
		return nil
	}
	if data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return fmt.Errorf("flexint64: decode string: %w", err)
		}
		if s == "" {
			*f = 0
			return nil
		}
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return fmt.Errorf("flexint64: parse %q: %w", s, err)
		}
		*f = FlexInt64(n)
		return nil
	}
	var num json.Number
	if err := json.Unmarshal(data, &num); err != nil {
		return fmt.Errorf("flexint64: decode number: %w", err)
	}
	n, err := strconv.ParseInt(num.String(), 10, 64)
	if err != nil {
		return fmt.Errorf("flexint64: parse %s: %w", num.String(), err)
	}
	*f = FlexInt64(n)
	return nil
}

// FlexBool decodes a Plex JSON flag sent as true/false, 0/1, "0"/"1", or
// "true"/"false". Null decodes to false.
//
// Decoding never fails: any other value decodes to false and Valid reports
// false. A wrong wire type on one session field must not make the whole
// MediaContainer undecodable. The zero value is a valid false. FlexBool is
// decode-only: it has no MarshalJSON, so encoding one writes {}.
type FlexBool struct {
	value   bool
	invalid bool
}

var _ json.Unmarshaler = (*FlexBool)(nil)

// Bool returns the decoded value; false when Valid is false.
func (b *FlexBool) Bool() bool { return b.value }

// Valid reports whether the wire value was one of the accepted forms.
func (b *FlexBool) Valid() bool { return !b.invalid }

// UnmarshalJSON decodes the accepted forms and never returns an error.
func (b *FlexBool) UnmarshalJSON(data []byte) error {
	switch string(data) {
	case "true", "1", `"1"`, `"true"`:
		*b = FlexBool{value: true}
	case "false", "0", `"0"`, `"false"`, "null":
		*b = FlexBool{}
	default:
		*b = FlexBool{invalid: true}
	}
	return nil
}

// RatingKey is Plex's opaque numeric-string identifier for a library item
// (movie, show, season, episode) or section. The type exists so keys are
// validated once at the API boundary instead of being interpolated into URL
// paths unchecked.
type RatingKey string

// String returns the key as a plain string.
func (r RatingKey) String() string { return string(r) }

// Validate reports whether the key is a non-empty numeric string. Anything
// else indicates a programming error (a foreign identifier in a
// rating-key slot) that must not reach URL construction.
func (r RatingKey) Validate() error {
	if _, err := strconv.Atoi(string(r)); err != nil {
		return fmt.Errorf("invalid rating key %q", string(r))
	}
	return nil
}

// Item is Plex's polymorphic metadata item: episodes, seasons, shows,
// movies, history entries, and live sessions all share this wire shape with
// different fields populated. Field presence follows the endpoint: a
// library listing populates identity + GUIDs, a session adds User / Player
// / TranscodeSession, a metadata fetch adds the Media→Part→Stream graph.
//
// LastViewedAt and ViewCount are the token's account's watch state, nil
// when absent; an AddedAt <= 0 is unknown.
type Item struct {
	LastViewedAt         *FlexInt64        `json:"lastViewedAt,omitempty"`
	ViewCount            *FlexInt          `json:"viewCount,omitempty"`
	User                 *SessionUser      `json:"User,omitempty"`
	TranscodeSession     *TranscodeSession `json:"TranscodeSession,omitempty"`
	Session              *SessionBandwidth `json:"Session,omitempty"`
	Player               *Player           `json:"Player,omitempty"`
	GrandparentRatingKey string            `json:"grandparentRatingKey"`
	Key                  string            `json:"key"`
	RatingKey            string            `json:"ratingKey"`
	GrandparentKey       string            `json:"grandparentKey"`
	Title                string            `json:"title"`
	ParentTitle          string            `json:"parentTitle"`
	GrandparentTitle     string            `json:"grandparentTitle"`
	Type                 string            `json:"type"`
	GUID                 string            `json:"guid"`
	LibrarySectionTitle  string            `json:"librarySectionTitle"`
	SessionKey           string            `json:"sessionKey"`
	ParentRatingKey      string            `json:"parentRatingKey"`
	GUIDs                []GUID            `json:"Guid"`
	Media                []Media           `json:"Media"`
	Label                []Label           `json:"Label"`
	AddedAt              int64             `json:"addedAt"`
	ViewedAt             int64             `json:"viewedAt"`
	Year                 int               `json:"year"`
	Index                FlexInt           `json:"index"`
	ParentIndex          FlexInt           `json:"parentIndex"`
	LibrarySectionID     FlexInt           `json:"librarySectionID"`
	AccountID            FlexInt           `json:"accountID"`
}

// SeasonNum returns the season index (ParentIndex), 0 when absent.
func (i *Item) SeasonNum() int { return int(i.ParentIndex) }

// EpisodeNum returns the episode index (Index), 0 when absent.
func (i *Item) EpisodeNum() int { return int(i.Index) }

// GUID is one external-identity entry from an item's Guid array
// (e.g. "imdb://tt0903747", "tvdb://81189", "plex://episode/<hash>").
type GUID struct {
	ID string `json:"id"`
}

// Label is a label tag on a metadata item.
type Label struct {
	Tag string `json:"tag"`
}

// Media is one media rendition of an item, wrapping its parts.
//
// ID is FlexInt because the Media→Part→Stream graph is the one place Plex
// disagrees with itself about the wire type of an id: /library/metadata/<key>
// and /library/sections/<n>/all send a bare number, /status/sessions sends
// the same field quoted (verified live 2026-08-21 against Plex 1.43.3). A
// plain int makes an active session's payload undecodable and takes every
// other field on the MediaContainer down with it.
type Media struct {
	VideoResolution string  `json:"videoResolution"`
	VideoCodec      string  `json:"videoCodec"`
	Part            []Part  `json:"Part"`
	ID              FlexInt `json:"id"`
	Bitrate         int     `json:"bitrate"`
}

// Part is one file of a Media, wrapping its streams. Decision is populated
// on session responses (the transcoder's per-part verdict). ID is FlexInt
// for the reason given on Media. Size is the file size in bytes: nil when
// absent or null, non-nil 0 when Plex sends 0.
type Part struct {
	Size     *FlexInt64 `json:"size,omitempty"`
	Decision string     `json:"decision"`
	Stream   []Stream   `json:"Stream"`
	ID       FlexInt    `json:"id"`
}

// StreamType identifies the kind of stream. The integer values are the
// Plex wire format.
type StreamType int

// Stream-type wire values.
const (
	StreamTypeVideo    StreamType = 1
	StreamTypeAudio    StreamType = 2
	StreamTypeSubtitle StreamType = 3
)

// Stream is a single video/audio/subtitle stream on a Part. ID is FlexInt for
// the reason given on Media.
type Stream struct {
	LanguageCode         string     `json:"languageCode"`
	LanguageTag          string     `json:"languageTag"`
	DisplayTitle         string     `json:"displayTitle"`
	ExtendedDisplayTitle string     `json:"extendedDisplayTitle"`
	Title                string     `json:"title"`
	Codec                string     `json:"codec"`
	AudioChannelLayout   string     `json:"audioChannelLayout"`
	ID                   FlexInt    `json:"id"`
	StreamType           StreamType `json:"streamType"`
	Channels             int        `json:"channels"`
	Selected             bool       `json:"selected"`
	Forced               bool       `json:"forced"`
	HearingImpaired      bool       `json:"hearingImpaired"`
	VisualImpaired       bool       `json:"visualImpaired"`
}

// IsAudio reports whether the stream is an audio track.
func (s *Stream) IsAudio() bool { return s.StreamType == StreamTypeAudio }

// IsSubtitle reports whether the stream is a subtitle track.
func (s *Stream) IsSubtitle() bool { return s.StreamType == StreamTypeSubtitle }

// SessionUser is the User element on a session or history item.
type SessionUser struct {
	Title string  `json:"title"`
	ID    FlexInt `json:"id"`
}

// Player is the Player element on a session item.
type Player struct {
	Device            string `json:"device"`
	Product           string `json:"product"`
	State             string `json:"state"`
	MachineIdentifier string `json:"machineIdentifier"`
	Local             bool   `json:"local"`
}

// SessionBandwidth is the Session element on a session item.
type SessionBandwidth struct {
	Location  string `json:"location"`
	Bandwidth int    `json:"bandwidth"`
}

// TranscodeSession is the TranscodeSession element on a session item; its
// decision fields distinguish direct play / direct stream / transcode.
//
// The TranscodeHw* fields report hardware transcoding: the two flags are nil
// when Plex omits them, and TranscodeHwDecoding / TranscodeHwEncoding name
// the hardware API Plex reports for that stage (for example "vaapi"),
// empty when it reports none.
type TranscodeSession struct {
	TranscodeHwRequested    *FlexBool `json:"transcodeHwRequested,omitempty"`
	TranscodeHwFullPipeline *FlexBool `json:"transcodeHwFullPipeline,omitempty"`
	TranscodeHwDecoding     string    `json:"transcodeHwDecoding"`
	TranscodeHwEncoding     string    `json:"transcodeHwEncoding"`
	VideoDecision           string    `json:"videoDecision"`
	AudioDecision           string    `json:"audioDecision"`
	SubtitleDecision        string    `json:"subtitleDecision"`
	SourceVideoCodec        string    `json:"sourceVideoCodec"`
	SourceAudioCodec        string    `json:"sourceAudioCodec"`
	VideoCodec              string    `json:"videoCodec"`
	AudioCodec              string    `json:"audioCodec"`
}

// Section is a library section from GET /library/sections. ScannedAt is
// the last scan as unix seconds, nil when Plex omits it.
type Section struct {
	ScannedAt *FlexInt64 `json:"scannedAt,omitempty"`
	Key       string     `json:"key"`
	Title     string     `json:"title"`
	Type      string     `json:"type"`
}

// Section type strings and metadata type IDs used in section filters.
const (
	// SectionTypeMovie and SectionTypeShow are the section "type" values.
	SectionTypeMovie = "movie"
	SectionTypeShow  = "show"
	// TypeEpisode is the metadata "type" string on episode items.
	TypeEpisode = "episode"
	// MetadataTypeEpisode is the numeric type ID for ?type= filters.
	MetadataTypeEpisode = 4
)

// ServerIdentity is the server info from GET / (the union of the fields the
// consumers read; the endpoint returns more).
type ServerIdentity struct {
	FriendlyName                  string `json:"friendlyName"`
	MachineIdentifier             string `json:"machineIdentifier"`
	Version                       string `json:"version"`
	Platform                      string `json:"platform"`
	PlatformVersion               string `json:"platformVersion"`
	MyPlexSubscription            bool   `json:"myPlexSubscription"`
	TranscoderActiveVideoSessions int    `json:"transcoderActiveVideoSessions"`
}

// Account is a Plex system account from GET /accounts.
type Account struct {
	Name string `json:"name"`
	ID   int    `json:"id"`
}

// MediaProviders models GET /media/providers?includeStorage=1: the per-
// library duration/storage rollups behind library metrics.
type MediaProviders struct {
	FriendlyName      string          `json:"friendlyName"`
	MachineIdentifier string          `json:"machineIdentifier"`
	Version           string          `json:"version"`
	MediaProviders    []MediaProvider `json:"MediaProvider"`
}

// MediaProvider is one provider entry in MediaProviders.
type MediaProvider struct {
	Identifier string            `json:"identifier"`
	Features   []ProviderFeature `json:"Feature"`
}

// ProviderFeature is one feature of a MediaProvider; content features
// carry the library directories.
type ProviderFeature struct {
	Type        string              `json:"type"`
	Directories []ProviderDirectory `json:"Directory"`
}

// ProviderDirectory is one library directory entry in MediaProviders.
type ProviderDirectory struct {
	Title         string `json:"title"`
	ID            string `json:"id"`
	Type          string `json:"type"`
	DurationTotal int64  `json:"durationTotal"`
	StorageTotal  int64  `json:"storageTotal"`
}

// StatisticsResource is one host CPU/memory sample from the Plex Pass
// endpoint GET /statistics/resources.
type StatisticsResource struct {
	HostCPUUtilization    float64 `json:"hostCpuUtilization"`
	HostMemoryUtilization float64 `json:"hostMemoryUtilization"`
}

// StatisticsBandwidth is one bandwidth sample from the Plex Pass endpoint
// GET /statistics/bandwidth.
type StatisticsBandwidth struct {
	Bytes int64 `json:"bytes"`
	At    int   `json:"at"`
}

// HistoryEntry is one watch-history row from WalkHistory. Every decoded row
// is returned as Plex sent it: RatingKey is empty when Plex omits it, which
// it does for a play of an item since deleted, and ViewedAt is 0 when
// absent, so the caller decides which rows it can use. AccountID is the
// server-local account (see Accounts), 0 when absent.
//
// HistoryKey is Plex's per-row key, kept verbatim when it is at most 512
// bytes and empty otherwise. It is opaque: never parse it.
type HistoryEntry struct {
	RatingKey  string
	HistoryKey string
	ViewedAt   int64
	AccountID  int64
}

// Activity is one server background task from GET /activities (a library
// scan, media analysis, credits detection). Progress is a percentage, -1
// when Plex reports it as indeterminate and nil when absent.
// LibrarySectionID is the section the task works on, read from the
// activity's Context, empty for a server-wide task.
type Activity struct {
	Progress         *float64
	UUID             string
	Type             string
	Title            string
	Subtitle         string
	LibrarySectionID string
}

// UpdateStatus is the server's update check from GET /updater/status.
// CheckedAt is the last check as unix seconds and Status the updater's
// error code (0 means no error); both are nil when absent. Download URLs
// are not decoded, because Plex embeds the server token in them.
type UpdateStatus struct {
	CheckedAt *FlexInt64      `json:"checkedAt"`
	Status    *int            `json:"status"`
	Releases  []UpdateRelease `json:"Release"`
}

// UpdateRelease is one release in UpdateStatus. State is Plex's release
// state ("available", "downloaded", "skipped", ...), passed through as sent.
type UpdateRelease struct {
	Version string `json:"version"`
	State   string `json:"state"`
}
