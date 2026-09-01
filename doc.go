// Package plexapi is a typed, resilient client for the Plex Media Server
// HTTP API, plus a small client for the plex.tv account API.
//
// # Security model
//
// The X-Plex-Token grants full server access, so the client defends it on
// every request:
//
//   - The token travels only in the X-Plex-Token header, never a query
//     string.
//   - Redirects are never followed, since Go forwards custom headers
//     (including X-Plex-Token) on cross-origin redirects.
//   - Every request path must be server-relative; an absolute or
//     scheme-relative reference is rejected.
//   - A self-signed Plex is supported by pinning its CA (WithCACertPEM),
//     with TLS verification always on and no "insecure skip verify" option.
//   - Construction warns (once, via slog) when the base URL is plain http
//     to a non-local host.
//
// # Resilience model
//
// On the server Client, GET requests ride an httpx retry round-tripper:
// 429/502/503/504 and transient transport errors are retried with jittered
// exponential backoff, honoring Retry-After. Writes (PUT) are never
// retried, so a mutation is applied at most once per call. WithTimeout
// applies only when the caller's context carries no deadline. Response
// bodies are size-capped before decode.
//
// The plex.tv TV client is outside this model: a minimal client (30s
// timeout, refuse-all redirects, no retry transport) for one fixed public
// endpoint whose sole production caller owns its own retry policy.
//
// # Wire model
//
// Plex wraps every JSON payload in a MediaContainer envelope and returns
// polymorphic metadata items (an episode, a season, a show, a movie, and a
// live session all share one shape with different fields populated). MC[T]
// is the envelope, Item is the polymorphic metadata item, and FlexInt
// absorbs Plex's habit of returning numeric fields as either numbers or
// quoted strings depending on the endpoint. Get is the escape hatch for
// endpoints with no typed method.
//
// The exported path builders (SessionsPath, HistoryPath, MetadataPath,
// ...) carry the endpoint paths, rating-key validation, the literal
// filter-operator contract, and — via their Path/ListPath return types —
// each endpoint's read-cap class; FetchMetadata / FetchMetadataList /
// FetchDirectory decode into any caller-owned type over the same hardened
// transport, with the cap class enforced at compile time. The typed Item
// methods are composition over exactly these pieces.
//
// Those three decoders are generic METHODS (Go 1.27), so they cannot appear
// in an interface: a consumer that mocks this client wraps them in
// non-generic methods of its own.
package plexapi
