// Package plexapi is a typed, resilient client for the Plex Media Server
// HTTP API, plus a small client for the plex.tv account API.
//
// [New] builds the server [Client] and [NewTV] the plex.tv [TV] client.
//
// # Security model
//
// The X-Plex-Token grants full server access, so the client defends it on
// every request:
//
//   - The token travels only in the X-Plex-Token header, never a query
//     string.
//   - Redirects are never followed, because Go forwards custom headers on a
//     cross-origin redirect.
//   - Every request path must be server-relative. An absolute or
//     scheme-relative reference is rejected.
//   - A self-signed Plex works by pinning its CA with [WithCACertPEM], and TLS
//     verification stays on.
//
// # Resilience model
//
// GET requests on the server client retry 429, 502, 503 and 504 answers and
// transient transport errors with jittered exponential backoff, honoring
// Retry-After. A PUT is never retried, so a change is applied at most once per
// call. Response bodies are size-capped before decode.
//
// # Wire model
//
// [MC] is Plex's MediaContainer envelope, [Item] the polymorphic metadata item
// and [FlexInt] a numeric field Plex sends as a number or a quoted string. The
// path builders carry each endpoint's path and read cap, and
// [Client.FetchMetadata], [Client.FetchMetadataList] and
// [Client.FetchDirectory] decode them into a caller-owned type. [Client.Get] is
// the escape hatch for an endpoint with no typed method. docs/how-it-works.md
// covers every option, cap and error.
package plexapi
