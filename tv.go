package plexapi

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/cplieger/httpx/v5"
	"github.com/cplieger/xmlx"
)

// plexTVBase is the plex.tv API origin. Tests override this per instance
// via WithTVBaseURL.
const plexTVBase = "https://plex.tv"

// SharedServer is one <SharedServer> element from the plex.tv
// shared_servers endpoint: a user the server is shared with, and the
// user-scoped access token for it.
type SharedServer struct {
	UserID      string `xml:"userID,attr"`
	Username    string `xml:"username,attr"`
	AccessToken string `xml:"accessToken,attr"`
}

// sharedServersXML is the response envelope for shared_servers.
type sharedServersXML struct {
	XMLName      xml.Name       `xml:"MediaContainer"`
	SharedServer []SharedServer `xml:"SharedServer"`
}

// sharedServersLimits bounds the shared_servers document before encoding/xml
// tokenizes it. The shape is fixed and tiny: one MediaContainer holding a flat
// list of attribute-only SharedServer elements, so every bound sits far above
// the real contract while still rejecting the amplification a wire-only byte cap
// admits.
var sharedServersLimits = xmlx.Limits{
	MaxTextRunBytes: 8 << 10,
	MaxTokenBytes:   16 << 10,
	MaxTagAttrs:     32,
	MaxDepth:        8,
	MaxElements:     50_000,
}

// TV is a client for the plex.tv account API (as opposed to a local Plex
// Media Server). It always verifies TLS against the OS trust store and
// never follows redirects, so the admin token cannot be forwarded off
// plex.tv by a compromised redirect or CDN front.
type TV struct {
	httpClient *http.Client
	base       string
	token      Token
}

// TVOption configures NewTV.
type TVOption func(*TV)

// WithTVHTTPClient replaces the built-in hardened client (tests).
func WithTVHTTPClient(hc *http.Client) TVOption {
	return func(t *TV) { t.httpClient = hc }
}

// WithTVBaseURL replaces the plex.tv origin (tests).
func WithTVBaseURL(base string) TVOption {
	return func(t *TV) { t.base = base }
}

// NewTV returns a plex.tv client authenticated with the given token.
func NewTV(token Token, opts ...TVOption) *TV {
	t := &TV{
		token: token,
		base:  plexTVBase,
		httpClient: &http.Client{
			Timeout:       30 * time.Second,
			CheckRedirect: httpx.RefuseAllRedirects,
		},
	}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

// SharedServers returns the users the identified server is shared with,
// including their user-scoped access tokens. An empty response body (which
// plex.tv sometimes returns instead of an empty <MediaContainer/>) yields
// zero servers, not a parse error.
func (t *TV) SharedServers(ctx context.Context, machineIdentifier string) ([]SharedServer, error) {
	apiPath := "/api/servers/" + url.PathEscape(machineIdentifier) + "/shared_servers"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.base+apiPath, http.NoBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/xml")
	req.Header.Set("X-Plex-Token", string(t.token))

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("plex.tv shared_servers: %w", httpx.LogSafeError(err))
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		httpx.DrainClose(resp.Body)
		return nil, &StatusError{Method: http.MethodGet, Path: apiPath, Status: resp.Status, Code: resp.StatusCode}
	}
	body, err := httpx.ReadLimitedBody(resp.Body, DefaultMaxBodyBytes)
	if err != nil {
		if _, ok := errors.AsType[*httpx.ResponseTooLargeError](err); ok {
			return nil, &ResponseTooLargeError{Path: apiPath, Limit: DefaultMaxBodyBytes}
		}
		return nil, fmt.Errorf("plex.tv shared_servers: reading body: %w", err)
	}
	if len(body) == 0 {
		return nil, nil
	}
	if err := xmlx.Preflight(body, sharedServersLimits); err != nil {
		return nil, fmt.Errorf("plex.tv shared_servers: %w", err)
	}
	var result sharedServersXML
	if err := xml.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parsing shared_servers XML: %w", err)
	}
	return result.SharedServer, nil
}
