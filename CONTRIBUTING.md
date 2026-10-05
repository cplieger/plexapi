# Contributing to plexapi

The [shared rules](https://github.com/cplieger/.github/blob/main/CONTRIBUTING.md) for commits, releases, synced files and checks apply here.

## Rules

- Every Plex server request goes through `Client.do`, directly or by way of `Get`, `put` or a fetch method. A direct `http.Client.Do` call skips its token header, path guard, typed status errors, URL-free transport errors and read cap.
