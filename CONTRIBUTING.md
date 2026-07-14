# Contributing to plexapi

Notes on the surface, the security contract, and the local workflow. The
library is shaped by its consumers (a Prometheus exporter, a language-sync
daemon, a watch-history remapper); it is a focused client, not a full Plex
SDK.

## Scope: consumer-led, security-first

Endpoints are added when a real consumer needs them, implemented completely
or not at all. The current surface covers library metadata (sections, items,
children/leaves, GUID resolution), sessions and history, server identity and
statistics, per-user stream selection, and the plex.tv shared-servers call.
The README's "Unsupported by design" table is the contract — notably: no
library-management writes, no WebSocket layer (consumers own reconnect
policy; the client exposes `BaseURL`/`HTTPClient` so a dialer can share the
transport), and no TLS verification bypass, ever.

## Security invariants (do not weaken)

These hold on every request, by construction, and changing any of them is a
breaking security review, not a refactor:

- The `X-Plex-Token` travels only in a request header — never a query
  string, never a log line, never an error string.
- Redirects are refused outright (`http.ErrUseLastResponse`): Go forwards
  custom headers on cross-origin redirects, so following one could hand the
  token to a hostile host.
- Request paths must be server-relative; absolute or scheme-relative
  references (which would re-target URL resolution) are rejected before a
  request is built.
- `RatingKey` values are validated numeric before URL interpolation.
- A self-signed Plex is supported by pinning its CA (`WithCACertPEM`) with
  verification ON; there is no insecure-skip option to add.
- Transport errors are reduced to their cause (`url.Error` unwrapped) so
  error strings never embed full request URLs.

## Resilience model

GETs ride an httpx retry round-tripper (429/502/503/504 + transient
transport errors, honoring `Retry-After`); writes are never retried (body
replay stays off, so a mutation applies at most once). A per-attempt
response-header timeout keeps a stalled attempt retryable; the per-request
default timeout applies only when the caller's context has no deadline. Keep
new endpoints on `do`/`fetchMetadata`/`fetchDirectory` so they inherit all
of it; `Get` is the documented escape hatch for unmodeled endpoints.

## Wire-format notes

Plex responses are polymorphic (`Item`) and numerically inconsistent
(`FlexInt` absorbs number-or-quoted-string fields). Server-side filters use
literal single-character comparators (`viewedAt>=N`): an encoded or doubled
operator is silently ignored and Plex returns the full unfiltered set, so
the literal form is a pinned wire contract with tests asserting the exact
`RawQuery`.

## Local workflow

```sh
go build ./... && go vet ./...
go test -race ./...
golangci-lint run ./...
```

Tests run against `httptest` fixtures (no live Plex needed); the security
invariants each have a pinning test (token-in-header-only, path-guard
rejection, redirect refusal, CA pinning end-to-end). CI (`ci / validate`)
runs the same battery via the shared cplieger/ci workflows; conventional
commits drive git-cliff release versioning.
