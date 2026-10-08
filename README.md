# plexapi

[![Go Reference](https://pkg.go.dev/badge/github.com/cplieger/plexapi/v2.svg)](https://pkg.go.dev/github.com/cplieger/plexapi/v2) [![Go version](https://img.shields.io/github/go-mod/go-version/cplieger/plexapi)](https://github.com/cplieger/plexapi/blob/main/go.mod)

plexapi lets your Go tool read a Plex Media Server and set each user's audio and subtitle tracks. Its built-in transport keeps the X-Plex-Token on that server.

It saves you writing your own Plex client, which needs the token header, the JSON envelope Plex wraps every answer in, retries and size limits. You supply the token, because it has no sign-in flow. It needs Go 1.27.1 or later, depends on [httpx](https://github.com/cplieger/httpx) and [xmlx](https://github.com/cplieger/xmlx) by the same author, and is licensed under Apache-2.0.

## Why use it

plexapi is built for Go tools that run beside a Plex Media Server, such as exporters and sync services.

- Typed calls read library items, live sessions, watch history, server statistics and a server's shared users.
- It sets a user's audio or subtitle track with that user's token, never retried.
- The token travels only in the `X-Plex-Token` header, and request paths cannot name another host. The built-in transport refuses every redirect.
- It retries 429, 502, 503 and 504 answers and transient network errors with backoff, honoring `Retry-After`.
- Response bodies are capped before decoding.
- A self-signed server works by pinning its CA, and TLS verification stays on.

Consider [go-plex-client](https://github.com/jrudio/go-plex-client) if your tool reacts to live WebSocket events, marks items watched or gets a token through the plex.tv/link PIN flow. Consider [plexgo](https://github.com/LukeHagar/plexgo) if you need the whole Plex Media Server and plex.tv API from an SDK generated from the Plex OpenAPI specification.

## Install

```sh
go get github.com/cplieger/plexapi/v2@latest
```

## Usage

```go
// Token is a named type: a swapped token and URL is a compile error.
client, err := plexapi.New("http://plex:32400", plexapi.Token(token))
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

// Plex records a track change against the user whose token sent it,
// so change a user's tracks with a client holding that user's token.
userClient := client.ForToken(plexapi.Token(userToken))
err = userClient.SetSubtitleStream(ctx, plexapi.StreamSelection{PartID: partID, StreamID: streamID})

// plex.tv: the shared users of a server, with their access tokens.
tv := plexapi.NewTV(plexapi.Token(adminToken))
shared, err := tv.SharedServers(ctx, machineID)
```

A rating key is the number Plex gives each library item, and a GUID is an item's external ID such as `imdb://tt0903747`. For a server behind a self-signed certificate, pin its CA with `WithCACertPEM`. Verification stays on, and your code reads the PEM file:

```go
pem, _ := os.ReadFile(caPath) // the caller owns file I/O
client, err := plexapi.New(serverURL, plexapi.Token(token), plexapi.WithCACertPEM(pem))
```

## API

- `New(baseURL, token, ...Option)` builds a server client. `ForToken` gives the same server and connection pool another user's token.
- Options are `WithCACertPEM`, `WithMaxAttempts`, `WithBaseDelay`, `WithTimeout`, `WithMaxBodyBytes`, `WithMaxListBodyBytes`, `WithLogger`, `WithOnRetry` and `WithHTTPClient`.
- Library reads are `Sections`, `SectionItems`, `SectionItemsPage`, `WalkSectionItems`, `RecentlyAdded`, `Metadata`, `Children`, `AllLeaves`, `ItemExists`, `ItemsByGUID`, `ShowForEpisodeGUID` and `CountSectionItems`.
- Activity and server reads are `Sessions`, `History`, `WalkHistory`, `Activities`, `UpdateStatus`, `Identity`, `Accounts`, `AdminAccount`, `Providers`, `StatisticsResources` and `StatisticsBandwidth`.
- Track changes are `SetAudioStream`, `SetSubtitleStream` and `DisableSubtitles`. `NewTV(token).SharedServers(machineID)` lists a server's shared users from plex.tv.
- `WalkSectionItems` and `WalkHistory` read a large listing one `Page` at a time.
- `FlexInt64` and `FlexBool` read fields Plex sends in more than one form.
- To decode into your own types, pass a path builder such as `MetadataPath` to `FetchMetadata`, `FetchMetadataList` or `FetchDirectory`. `Get` calls any endpoint no typed call covers.
- Errors are `ErrNotFound`, `*StatusError`, `*ResponseTooLargeError`, `IsNotFound` and `IsConfigError`.

The full reference is on [pkg.go.dev](https://pkg.go.dev/github.com/cplieger/plexapi/v2). [How plexapi works](docs/how-it-works.md) explains each group's behavior.

## How it protects the token

The token grants full access to the server, so the client defends it on every request:

- The token goes in the `X-Plex-Token` header only, never in a query string, so a logged URL cannot leak it.
- Redirects are refused. Go forwards custom headers on a cross-origin redirect, so a hostile 302 would hand the token to another host. The Plex API issues no redirects.
- A request path must be relative to the server. An absolute or scheme-relative path, which would point the request at another host, is rejected before any request is built.
- Rating keys are checked to be numbers before they go into a URL.
- Pinning a CA keeps TLS verification on and trusts only that CA. The built-in transport has no option that turns verification off.
- `New` logs a warning when the base URL is plain `http://` to a host that is not local, because the token would travel unencrypted. It treats `localhost`, a loopback address and a hostname without a dot, such as the Docker container name `plex`, as local.
- Transport errors are reduced to their cause, so error text never holds a full request URL.

Redirect refusal and TLS verification belong to the built-in transport. A client you pass with `WithHTTPClient` replaces it, so that client must refuse redirects and verify TLS itself.

## How it handles failures

- GET requests are retried on 429, 502, 503 and 504 and on transient network errors, with jittered exponential backoff that honors `Retry-After`. `WithMaxAttempts(1)` turns retries off.
- A track change is a PUT and is sent at most once, never retried.
- Each attempt times out after 15 seconds without response headers, which makes a stalled attempt a retryable error. `WithTimeout` sets a per-request limit, 2 minutes by default, that applies only when your context has no deadline. Your deadline always wins.
- Response bodies are capped before decoding, at 10 MB by default and 40 MB for full section listings. Both are configurable, and a body over the cap returns `*ResponseTooLargeError` instead of a cut-off decode.

## Unsupported by design

These are deliberate non-goals:

| Feature | Rationale |
| --- | --- |
| Library management writes (edit metadata, delete items, trigger scans) | plexapi is built for tools that read a library and pick tracks, so track selection is the only write it models. |
| WebSocket notifications | A different transport whose reconnect policy belongs to each app. `BaseTransport()` and `RedirectPolicy()` let your own dialer reuse the hardened transport. |
| Full plex.tv account surface (devices, friends, PINs) | `SharedServers` is the one account call the client covers. |
| Insecure TLS (`InsecureSkipVerify`) | Pin the CA instead. The built-in transport always verifies TLS. |
| Response caching or request coalescing | Your code owns caching. The client holds no cache and takes no locks of its own. |

## Documentation

- [How plexapi works](docs/how-it-works.md) is for developers who need each call's contract, the defaults, the read caps, the types and the error classes.

## Contributing

Issues and pull requests are welcome. See [CONTRIBUTING.md](CONTRIBUTING.md).

## Disclaimer

This project is built with care and follows security best practices, but it is
intended for personal / self-hosted use. No guarantees of fitness for production
environments. Use at your own risk.

This project was built with AI-assisted tooling using
[Claude](https://claude.com), [GPT](https://openai.com), and
[Kiro](https://kiro.dev). The human maintainer defines architecture,
supervises implementation, and makes all final decisions.

## License

Apache-2.0. See [LICENSE](LICENSE).
