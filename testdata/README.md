# Recorded answers

Each JSON file here is one answer from a real Plex Media Server 1.43.4, recorded with `curl` and a read-only `GET` on 2026-10-17, 2026-10-18 and 2026-10-19. The token went in the `X-Plex-Token` header only, so no URL or answer carried it.

| File | Request | What it pins |
| --- | --- | --- |
| `sections.json` | `GET /library/sections` | The section directory: keys, types, titles, `scannedAt` and locations |
| `movie-two-sidecars.json` | `GET /library/metadata/<key>` for a movie with two sidecar subtitle files | `Part.file`, and `Stream.key` on the two sidecar streams only, never on an embedded track |
| `two-episode-file.json` | `GET /library/sections/<key>/all?type=4&id=<a>,<b>` for two episodes stored in one file | One `Part.file` path under both episodes |
| `recently-added.json` | `GET /library/sections/<key>/all?type=1&sort=addedAt:desc&addedAt>=1791504000` | A recently-added read of a movie section, newest first |
| `upgraded-file-before.json` | `GET /library/sections/<key>/all?type=4`, cut to the one episode's row with `size` set to 1, recorded before its file was upgraded | The episode as it stood: its `addedAt`, `updatedAt`, media, part and file |
| `upgraded-file-after.json` | `GET /library/sections/<key>/all?type=4&id=<key>` for the same episode, after Plex scanned the upgraded file | An upgrade keeps `ratingKey` and `addedAt`, moves `updatedAt`, and gives the media and part new ids and the new `Part.file` |
| `updated-since.json` | `GET /library/sections/<key>/all?type=4&sort=updatedAt:asc&updatedAt>=1792200404&X-Plex-Container-Start=0&X-Plex-Container-Size=50` | An updated-since read: every row at or after the bound and ascending by `updatedAt`, eight rows sharing one second, and the upgraded episode last with its moved `updatedAt` and kept `addedAt` |

## Scrubbing

Before a file was copied in, a script rewrote these values:

- `machineIdentifier` and `clientIdentifier` became `machine-id-0` and `client-id-0`.
- Every `uuid` and `librarySectionUUID` became `00000000-0000-4000-8000-<n>`, numbered in order of first appearance, so equal values stay equal.
- Every `tagKey` became `tagkey-<n>` the same way.
- Any `X-Plex-Token` query parameter was removed.
- Account, family and host names in titles, collections and paths became a neutral word.

The script refused to write a file that still held `X-Plex-Token`, `X-Api-Key`, `apikey=`, `accessToken`, `authToken` or the server's host name. `TestFixturesHoldNoSecret` checks the five credential markers and the placeholder forms on every file, and gitleaks scans the directory in CI.
