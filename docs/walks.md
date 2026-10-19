# Walking a large listing

This page covers the calls that read a listing too large for one answer, for developers who page through a section or read what changed in it. `WalkSectionItems` and `WalkHistory` page by offset. `UpdatedSince` reads the items changed since a time. Each returns an iterator that yields one row at a time, or one error that ends the walk.

## Pacing a walk

- A walk's `wait` is a function or `nil`. The walk calls it right before every request, after a page shorter than asked too, so you can pace the requests.
- An error from `wait` ends the walk like a failed request.

## Paging by offset

- `WalkSectionItems(key, type, page, wait)` returns an iterator over a section from `page.Start`, `page.Size` items per request, so no single answer holds the whole section. It ends at an empty page or after `totalSize` items. The first error ends the walk and is yielded once.
- If the listing keeps growing, the walk ends with an error once it has read `page.Size` items more than the first page's `totalSize`. The page that crosses that count is still read in full. The count is in items, not requests, so a server that sends pages shorter than `page.Size` is still read in full.
- An item added or removed during a walk can be skipped or read twice.
- A walk can tell that a server ignored paging only when a page holds more rows than asked. A first page like that ends the walk, with an error when it ends short of its `totalSize`. When such a page starts past row 0 and ends past `totalSize`, the server ignored the start offset. The walk then returns only an error, without that page's rows.
- `WalkHistory` pages through watch history by offset too. [How plexapi works](how-it-works.md#sessions-history-and-server-calls) covers what it returns.

## Changes since a time

- `UpdatedSince(key, type, sinceUnix, size, wait)` returns an iterator over a section's movies (`MetadataTypeMovie`) or episodes (`MetadataTypeEpisode`) whose `updatedAt` is at or after a time, oldest change first. Plex moves `updatedAt` on a file upgrade, a rescan and a metadata change. Every request brings up to `size` new items.
- Each request starts again at the first item of the newest `updatedAt` the walk has returned. It skips the items of that second it has already returned, by their rating key. So an item updated, added or removed during the walk never makes it skip another. An item updated during the walk comes again at its new place.
- Inside one second, each request also makes room for the items of that second already returned. A second shared by many items costs larger requests.
- Code that stores the largest `updatedAt` it has read and resumes from it reads the items of that second again.

## When UpdatedSince stops with an error

`UpdatedSince` ends with an error instead of leaving a gap. These answers end it:

- an item below the bound, or items out of order
- an item with no rating key, or one rating key twice
- more items than asked, or more than its `totalSize`
- no items while its `totalSize` says there are some
- no item the walk has not returned yet.

It also ends with an error once it has returned `size` items more than the first answer's `totalSize`. A `size` below 1 is an error before any request. A failed request or `wait` ends it as it ends `WalkSectionItems`.
