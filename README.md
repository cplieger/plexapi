# plexapi

[![Go Reference](https://pkg.go.dev/badge/github.com/cplieger/plexapi.svg)](https://pkg.go.dev/github.com/cplieger/plexapi)
[![Go version](https://img.shields.io/github/go-mod/go-version/cplieger/plexapi)](https://github.com/cplieger/plexapi/blob/main/go.mod)
[![Test coverage](https://img.shields.io/endpoint?url=https://raw.githubusercontent.com/cplieger/plexapi/badges/coverage.json)](https://github.com/cplieger/plexapi/actions/workflows/coverage.yml)
[![OpenSSF Best Practices](https://www.bestpractices.dev/projects/13605/badge)](https://www.bestpractices.dev/projects/13605)
[![OpenSSF Scorecard](https://api.scorecard.dev/projects/github.com/cplieger/plexapi/badge)](https://scorecard.dev/viewer/?uri=github.com/cplieger/plexapi)

> Typed, resilient Go client for the Plex Media Server HTTP API

Library metadata, sessions, watch history, server identity and statistics,
GUID resolution, and per-user stream selection — over a transport that
defends the `X-Plex-Token` by construction and retries transient failures
transparently. Plus a small client for the plex.tv account API
(shared-server user tokens).

One runtime dependency: [httpx](https://github.com/cplieger/httpx) (retry
round-tripper, CA pinning, bounded reads).

## Install

```sh
go get github.com/cplieger/plexapi@latest
```

## Usage

```go
client, err := plexapi.New("http://plex:32400", token)
if err != nil { ... }

// Library indexing: sections and their items (rating keys, GUIDs, years).
sections, err := client.Sections(ctx)
items, err := client.SectionItems(ctx, plexapi.RatingKey(sections[0].Key))

// One item; the endpoint is polymorphic (movie/show/season/episode).
item, err := client.Metadata(ctx, "49915")
episodes, err := client.AllLeaves(ctx, "1345") // every episode of a show

// Live sessions and server-side-filtered watch history.
sessions, err := client.Sessions(ctx)
history, err := client.History(ctx, time.Now().Add(-24*time.Hour).Unix())

// Per-user stream selection: writes are recorded against the REQUESTING
// token's user, so select with that user's token.
userClient := client.ForToken(userToken)
err = userClient.SetSubtitleStream(ctx, partID, streamID)

// plex.tv: the shared users of a server, with their access tokens.
tv := plexapi.NewTV(adminToken)
shared, err := tv.SharedServers(ctx, machineID)
```

A Plex behind a self-signed certificate pins its CA (verification stays on;
there is no skip option):

```go
pem, _ := os.ReadFile(caPath) // the caller owns file I/O
client, err := plexapi.New(serverURL, token, plexapi.WithCACertPEM(pem))
```

## Security model

The token grants full server access; the client defends it on every request:

- Token in the `X-Plex-Token` header only — never a query string, so URL
  logging can't leak it.
- Redirects are refused outright. Go's default policy forwards custom
  headers on cross-origin redirects, so a hostile 302 would exfiltrate the
  token; Plex's API issues no redirects, so none are followed.
- Request paths must be server-relative: an absolute or scheme-relative
  reference (which would re-target URL resolution at another host) is
  rejected before any request is built.
- Rating keys are validated numeric before URL interpolation.
- CA pinning for self-signed servers keeps TLS verification on, trusting
  only the supplied CA; there is deliberately no insecure-skip option.
- Construction warns when the base URL is plain `http://` to a non-local
  host (the token would transit unencrypted).
- Transport errors are reduced to their cause so error strings never embed
  full request URLs.

## Resilience model

- GETs ride an httpx retry round-tripper: 429/502/503/504 and transient
  transport errors retried with jittered exponential backoff, honoring
  `Retry-After` on 429. `WithMaxAttempts(1)` disables retries.
- Writes (`PUT` stream selection) are applied at most once — never retried.
- A per-attempt response-header timeout makes a stalled attempt fail as a
  retryable error instead of hanging the sequence; a per-request default
  timeout (`WithTimeout`, default 2m) applies only when the caller's context
  has no deadline, so a caller deadline is always the authoritative budget.
- Response bodies are size-capped before decode (defaults 10 MB; 40 MB for
  full section listings; both configurable), with overflow reported as
  `*ResponseTooLargeError` rather than a truncated decode.

## API

- **Constructor:** `New(baseURL, token, ...Option)` — options `WithCACertPEM`, `WithMaxAttempts` (total, default 3), `WithBaseDelay`, `WithTimeout`, `WithMaxBodyBytes`/`WithMaxListBodyBytes` (read caps), `WithLogger` (routes the client's own diagnostics; default `slog.Default()`), `WithOnRetry` (retry-counter hook), `WithHTTPClient` (caller-owned transport, tests).
- **Derived clients:** `(*Client).ForToken(token)` — same server + shared connection pool, different token (the per-user write path).
- **Library:** `Sections`, `SectionItems(key)`, `RecentlyAdded(key, type, sinceUnix)`, `Metadata(key)`, `Children(key)`, `AllLeaves(key)`, `ItemExists(key)` (fail-closed: an undetermined check is an error, never "gone"), `ItemsByGUID(guid)`, `ShowForEpisodeGUID(guid)` (ambiguity yields `""`, refusing to guess), `ContainerTotalSize(path)`.
- **Sessions & history:** `Sessions()`, `History(sinceUnix)` — history and recently-added filters use Plex's literal single-char `>=` operator (a malformed or encoded operator is silently ignored by Plex, returning the full unfiltered set; the literal form is a pinned wire contract).
- **Server:** `Identity()`, `Accounts()`, `MyPlexUsername()`, `AdminAccount()`, `Providers()` (per-library duration/storage), `StatisticsResources(timespan)` / `StatisticsBandwidth(timespan)` (Plex Pass; 404 → `ErrNotFound` for graceful degradation).
- **Stream selection:** `SetAudioStream(partID, streamID)`, `SetSubtitleStream(partID, streamID)`, `DisableSubtitles(partID)` — user-scoped by requesting token.
- **plex.tv:** `NewTV(token, ...TVOption)`, `(*TV).SharedServers(machineID)`.
- **Types:** `MC[T]` (the MediaContainer envelope, for `Get` escape-hatch decoding), `Item` (Plex's polymorphic metadata item: library entries, sessions, and history rows are one wire shape), `FlexInt` (absorbs Plex's number-or-quoted-string fields), `RatingKey` (validated identifier), the `Media`→`Part`→`Stream` graph, `Section`, `ServerIdentity`, `Account`, `SharedServer`, statistics types.
- **Errors:** `ErrNotFound` + `IsNotFound(err)`, `StatusError{Method, Path, Status, Code}`, `ResponseTooLargeError{Path, Limit}`, `IsConfigError(err)` (a 4xx other than 408/429 is a configuration/authorization failure that will not self-heal; everything else is transient).
- **Escape hatch:** `Get(ctx, path, &result)` for endpoints without a typed method, with the same hardening (path guard, redirect refusal, retries, body caps).

## Unsupported by design

Deliberate non-goals, not TODOs:

| Feature | Rationale |
| --- | --- |
| Library management writes (edit metadata, delete items, scan triggers) | The consumers are read-and-select tools; the only mutations modeled are stream selections. |
| WebSocket notifications | A different transport with app-specific reconnect policy; consumers own it (the client exposes `BaseURL`/`HTTPClient` so a dialer can share the transport and trust settings). |
| Full plex.tv account surface (devices, friends, PINs) | `SharedServers` is the one account call a consumer needs. |
| Insecure TLS (`InsecureSkipVerify`) | Pin the CA instead; verification never turns off. |
| Response caching / request coalescing | Callers own their caching layer; the client stays lock-free and stateless per request. |

## Disclaimer

This project is built with care and follows security best practices, but it is
intended for personal / self-hosted use. No guarantees of fitness for production
environments. Use at your own risk.

This project was built with AI-assisted tooling using [Claude Opus](https://www.anthropic.com/claude)
and [Kiro](https://kiro.dev). The human maintainer defines architecture,
supervises implementation, and makes all final decisions.

## License

GPL-3.0 — see [LICENSE](LICENSE).
