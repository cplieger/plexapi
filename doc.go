// Package plexapi is a typed, resilient client for the Plex Media Server
// HTTP API, plus a small client for the plex.tv account API.
//
// # Security model
//
// The X-Plex-Token grants full server access, so the client defends it in
// depth, on every request, by construction:
//
//   - The token travels only in the X-Plex-Token header, never a query
//     string, so it cannot leak through URL logging.
//   - Redirects are never followed: Go's default policy forwards custom
//     headers (including X-Plex-Token) on cross-origin redirects, so a
//     hostile 302 (MITM, DNS poisoning, compromised upstream) would
//     otherwise exfiltrate the token.
//   - Every request path must be server-relative. An absolute or
//     scheme-relative reference would override the configured host via URL
//     resolution and send the token to that origin; the client rejects it.
//   - A self-signed Plex is supported by pinning its CA (WithCACertPEM):
//     TLS verification stays ON, trusting only that CA. There is no
//     "insecure skip verify" option.
//   - Construction warns (once, via slog) when the base URL is plain http
//     to a non-local host, because the token would transit unencrypted.
//
// # Resilience model
//
// GET requests ride an httpx retry round-tripper: 429/502/503/504 and
// transient transport errors (timeouts, resets, DNS) are retried with
// jittered exponential backoff, honoring Retry-After on 429. Writes (PUT)
// go through the same client but are never retried (body replay is not
// enabled), so a mutation is applied at most once per call. A per-attempt
// response-header timeout on the transport makes a stalled attempt fail as
// a retryable error instead of hanging the sequence; a per-request default
// timeout (WithTimeout) applies only when the caller's context carries no
// deadline, so a caller deadline is always the authoritative budget.
// Response bodies are size-capped before decode.
//
// # Wire model
//
// Plex wraps every JSON payload in a MediaContainer envelope and returns
// polymorphic metadata items (an episode, a season, a show, a movie, and a
// live session all share one shape with different fields populated). The
// package mirrors that honestly: MC[T] is the envelope, Item is the
// polymorphic metadata item, and FlexInt absorbs Plex's habit of returning
// numeric fields as either numbers or quoted strings depending on the
// endpoint. Typed methods cover the endpoints the consumers use; Get is the
// documented escape hatch for anything else (for example the Plex Pass
// statistics endpoints have typed helpers, but a new undocumented endpoint
// can be reached without waiting for a release).
package plexapi
