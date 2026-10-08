# How plexapi works

This page covers the contract of each plexapi call group, for developers who need the defaults, the read caps, the types and the error classes before they build on it. The generated reference is on [pkg.go.dev](https://pkg.go.dev/github.com/cplieger/plexapi/v2).

## Building a client

`New(baseURL, token, ...Option)` checks that the base URL uses `http` or `https` and names a host, then builds the transport. `Token` is its own type, so passing the token and the URL in the wrong order is a compile error rather than a URL parse failure at run time. `ForToken` and `NewTV` take a `Token` too. An untyped string literal still converts to `Token`, which is why `New` still validates the URL.

| Option | Default | What it sets |
| --- | --- | --- |
| `WithCACertPEM(pem)` | the OS trust store | The CA certificates to trust, for a server behind a self-signed or private CA. Your code reads the PEM file |
| `WithMaxAttempts(n)` | 3 | Total attempts per GET, the first one included. 1 turns retries off |
| `WithBaseDelay(d)` | 200 ms | The first retry backoff |
| `WithTimeout(d)` | 2 minutes | The per-request limit, used only when the context has no deadline |
| `WithMaxBodyBytes(n)` | 10 MB | The read cap for metadata, sessions, history and server information. A value of 0 or less is ignored |
| `WithMaxListBodyBytes(n)` | 40 MB | The read cap for full section listings. A value of 0 or less is ignored |
| `WithLogger(l)` | `slog.Default()` | Where the client's own warnings go, the plain-HTTP warning and the over-cap warning |
| `WithOnRetry(fn)` | none | A hook called on each retry, for a retry counter |
| `WithHTTPClient(hc)` | the built-in transport | Your own `*http.Client`, which replaces the retries, the CA pin and the redirect policy entirely. Meant for tests |

`ForToken(token)` returns a client for the same server and connection pool with a different token. Use it for track changes, which Plex records against the user whose token sent the request.

`BaseURL()` returns a copy of the server URL, so changing it never retargets the client. `Token()` returns the token, for comparing tokens, building the plex.tv client or authenticating your own WebSocket dial. Never log it or put it in a URL.

`BaseTransport()` returns an independent copy of the hardened transport, with the same CA trust and per-attempt header timeout and without the retry layer. `RedirectPolicy()` returns the redirect function. Together they let your own WebSocket dialer keep the CA pin and refuse redirects. `BaseTransport()` returns nil under `WithHTTPClient`, and `RedirectPolicy()` returns nil when that client sets none.

## Protecting the token

The token grants full access to the server. On every request the client applies four protections itself, whatever transport carries the request:

- It sends the token only in the `X-Plex-Token` header, never in a query string.
- It rejects an absolute or scheme-relative request path before it builds the request, because resolving one would point the request at another host.
- It checks that a rating key is a number before it goes into a URL.
- It reduces a transport error to its cause, so error text never holds a full request URL.

The built-in transport also refuses every redirect, because Go forwards custom headers on a cross-origin redirect.

With a pinned CA, TLS verification stays on, trusts only the CA you supply and requires TLS 1.2 or later. The built-in transport has no option that turns verification off.

Redirect refusal, the CA pin, the retries and the per-attempt timeout belong to the built-in transport. A client you pass with `WithHTTPClient` replaces all four, so that client must refuse redirects and verify TLS itself. The four protections in the list above and the plain-HTTP warning still apply, because the client applies them itself, outside any transport.

`New` logs one warning when the base URL is plain `http://` to a host that is not local, because the token would travel unencrypted. `localhost`, a loopback IP address and a hostname without a dot, such as a Docker container name, count as local. For a server reached from outside your network, put a TLS proxy in front of Plex and use `https://`.

## Retries and timeouts

GET requests go through a retry layer from httpx. It retries 429, 502, 503 and 504 answers and transient network errors with jittered exponential backoff, and it honors `Retry-After` on each of those answers. A PUT is never retried, so a track change is applied at most once per call.

Each attempt times out after 15 seconds without response headers. That turns a stalled attempt into a retryable error instead of one that hangs the whole sequence. `WithTimeout` applies only when your context has no deadline, and your own deadline is always the budget for the whole call.

## Read caps

Every response body is capped before it is decoded. Section listings, from `SectionItems`, `SectionItemsPage`, `WalkSectionItems` and `RecentlyAdded`, use the list cap. Everything else uses the general cap. A body over its cap returns `*ResponseTooLargeError` with the path and the limit, and the client logs one warning, `plexapi: response exceeded read cap`, through its logger. A cut-off body is never decoded.

Watch history uses the general cap on purpose, in `WalkHistory` too. If Plex ignored the history filter and sent the full history, the cap turns that into an error instead of a large decode.

## Library calls

- `Sections` lists every library section. Filter by `Section.Type`, comparing with `SectionTypeMovie` or `SectionTypeShow`.
- `SectionItems(key)` lists every item in a section, with rating keys, titles, years and GUIDs.
- `RecentlyAdded(key, type, sinceUnix)` lists a section's items of one metadata type added since a time, newest first.
- `Metadata(key)` returns one item. The same call returns a movie, show, season or episode, depending on the key. It returns `ErrNotFound` when the key no longer exists.
- `Children(key)` returns a show's seasons or a season's episodes. `AllLeaves(key)` returns every episode of a show.
- `ItemExists(key)` returns true on a 200 and false on a 404. Any other failure returns an error, such as an auth error, a rate limit, a 5xx or a network error. A caller deciding whether an item is gone never mistakes an unknown answer for "gone".
- `ItemsByGUID(guid)` returns every item matching an external ID such as `imdb://tt0903747` or `plex://episode/<hash>`. An unknown GUID returns an empty slice.
- `ShowForEpisodeGUID(guid)` returns the rating key of the show that holds an episode. It returns `""` when nothing matches, when the matches belong to different shows, or when a match carries a malformed show key, because it refuses to guess.
- `SectionItemsPage(key, type, page)` returns one page of a section and the section's `totalSize`. A `Page` names the offset of the first row (`Start`) and the number of rows (`Size`). `type` 0 is unfiltered, as in `CountSectionItems`.
- `WalkSectionItems(key, type, page, wait)` returns an iterator over a section from `page.Start`, `page.Size` items per request, so no single answer holds the whole section. It ends at an empty page or after `totalSize` items. If the listing keeps growing, the walk ends with an error once it has read `page.Size` items more than the first page's `totalSize`. The page that crosses that count is still read in full. The count is in items, not requests, so a server that sends pages shorter than `page.Size` is still read in full. The first error ends the walk and is yielded once. An item added or removed during a walk can be skipped or read twice.
- A walk's `wait` is a function or `nil`. The walk calls it right before every page request, after a page shorter than `page.Size` too, so you can pace the requests. An error from it ends the walk like a failed request.
- A walk can tell that a server ignored paging only when a page holds more rows than asked. A first page like that ends the walk, with an error when it ends short of its `totalSize`. When such a page starts past row 0 and ends past `totalSize`, the server ignored the start offset. The walk then returns only an error, without that page's rows.
- `CountSectionItems(key, type)` returns the number of items in a section, filtered to one metadata type, or unfiltered when `type` is 0. It reads the `totalSize` field of a one-item page, because Plex leaves the total-size header empty on type-filtered queries.

## Sessions, history and server calls

- `Sessions()` returns what is playing now. Session items carry `User`, `Player`, `Session`, `TranscodeSession` and the media graph. A direct-play session has no `TranscodeSession`.
- `History(sinceUnix)` returns watch history since a time, newest first, filtered by the server.
- `WalkHistory(sinceUnix, page, wait)` returns an iterator over watch history since a time, oldest first, one page at a time. Plays recorded during the walk come after the read position, so earlier pages do not shift. It ends, fails and calls `wait` as `WalkSectionItems` does. Each `HistoryEntry` holds the rating key, account ID, view time and history key as Plex sent them. A rating key can be empty and a missing view time reads as 0, so your code decides which rows to use. A history key longer than 512 bytes is left empty.
- `Activities()` returns the server's running background tasks, such as a library scan or credits detection. `Progress` is a percentage, -1 when Plex cannot tell and nil when absent. `LibrarySectionID` is the section the task works on, empty for a server-wide task.
- `UpdateStatus()` returns the server's last update check and the releases it found, with their state. It returns `ErrNotFound` when the server has no updater. Download links are left out, because Plex puts the server token in them.
- `Identity()` returns the server name, machine ID, version, platform, Plex Pass status and active transcode count.
- `Accounts()` returns the server's local accounts, the IDs history entries refer to. `AdminAccount()` returns the owner, which is always account ID 1.
- `Providers()` returns duration and storage totals per library.
- `StatisticsResources(timespan)` and `StatisticsBandwidth(timespan)` return CPU, memory and bandwidth samples. They need Plex Pass and return `ErrNotFound` without it, so a caller can skip them cleanly.

History and recently-added filters write Plex's comparison with one literal, unencoded `>`, as in `viewedAt>=1700000000`. Plex ignores a doubled or URL-encoded operator and returns the full unfiltered set, so the tests pin the literal form.

## Track changes

`SetAudioStream` and `SetSubtitleStream` take a `StreamSelection{PartID, StreamID}`. The two IDs are a struct so they cannot be swapped by mistake. Both are required. Part 0 is not a Plex part, so a zero `StreamSelection` reaches the server and is refused there. `DisableSubtitles(partID)` turns subtitles off.

Plex records a track change against the user whose token sent it, while reads are not user-scoped. To change another user's tracks, use a client from `ForToken` with that user's token.

## The plex.tv client

`NewTV(token)` returns a client for plex.tv, and `SharedServers(machineID)` lists the users a server is shared with, each with a user-scoped access token. Its built-in HTTP client has a 30-second timeout, refuses redirects, verifies TLS against the OS trust store and does not retry.

The answer is XML, read under the 10 MB cap and checked by xmlx against fixed size and depth limits before it is parsed. An empty body returns zero servers rather than an error. `WithTVHTTPClient` and `WithTVBaseURL` replace the client and the address, for tests. A client passed with `WithTVHTTPClient` must set its own timeout and refuse redirects itself.

## Path builders and your own types

The path builders own every endpoint path the typed calls use, the rating-key check and the literal filter operator:

- `SessionsPath()`, `SectionsPath()` and `HistoryPath(sinceUnix)` return a `Path`.
- `MetadataPath(key)`, `ChildrenPath(key)` and `AllLeavesPath(key)` check the key, then return a `Path`.
- `HistoryPagePath(sinceUnix, page)` checks the page, then returns a `Path` for one page of history, oldest first.
- `SectionItemsPath(key)`, `SectionItemsPagePath(key, type, page)` and `RecentlyAddedPath(key, type, sinceUnix)` check their arguments, then return a `ListPath`.

A `Path` decodes under the general cap and a `ListPath` under the list cap. `FetchMetadata[T]` and `FetchDirectory[T]` accept a `Path`, and `FetchMetadataList[T]` accepts only a `ListPath`, so using the wrong cap is a compile error. They decode the response into your own type `T` over the same transport. Use them when your tool has its own data model, instead of building paths by hand.

The three are generic methods, which Go 1.27 added, and Go does not allow a generic method in an interface. If you mock the client, wrap them in non-generic methods of your own.

`Get(ctx, path, &result)` reaches an endpoint no typed call covers, with the same path check and read cap, over the same transport as the typed calls.

## Types

- `MC[T]` is the `MediaContainer` envelope Plex wraps every JSON answer in, for decoding with `Get`.
- `Item` is Plex's one shape for library entries, sessions and history rows, with different fields filled in by each endpoint.
- `FlexInt` reads a field Plex sends as either a number or a quoted string. Null, a missing field and an empty string read as 0. `FlexInt64` does the same for sizes and timestamps.
- `FlexBool` reads a flag Plex sends as `true`/`false`, `0`/`1` or the same values quoted. Any other value reads as false with `Valid()` false, and never fails the decode, because one malformed session field would otherwise lose the whole answer. It only decodes, so encoding one writes `{}`.
- Fields that can be missing are pointers that stay nil when Plex leaves them out: `Item.LastViewedAt` and `Item.ViewCount`, `Part.Size`, `Section.ScannedAt`, and the `TranscodeHwRequested` and `TranscodeHwFullPipeline` flags on `TranscodeSession`. A part with no size is never read as a zero-byte file. `LastViewedAt` and `ViewCount` are the watch state of the account whose token made the request.
- `RatingKey` is an item or section key, checked to be a number.
- `Page` selects one page of a paged listing. `Start` is the offset of the first row and `Size` the number of rows, more than 0.
- `Media`, `Part` and `Stream` are an item's media files, their parts and their audio, video and subtitle tracks.
- `Section`, `ServerIdentity`, `Account`, `SharedServer` and the statistics types match their calls above.

Every `id` on `Media`, `Part` and `Stream` is a `FlexInt`, because `/status/sessions` sends those IDs quoted while the library endpoints send them bare. Convert with `int(part.ID)` to build a `StreamSelection`. The test fixtures for those shapes were checked against Plex Media Server 1.43.3.

## Errors

- `ErrNotFound` is returned when the server answers 404. `IsNotFound(err)` matches it. The plex.tv client returns a `*StatusError` for a 404 instead.
- `*StatusError` carries `Method`, `Path`, `Status` and `Code` for any other non-200 answer, after retries run out.
- `*ResponseTooLargeError` carries `Path` and `Limit` for a body over its cap.
- `IsConfigError(err)` reports a 4xx answer other than 408 and 429. That is a configuration or authorization problem that will not fix itself. A false result does not mean the error is transient. `ErrNotFound`, `*ResponseTooLargeError` and a rejected rating key also fail again on a retry.
