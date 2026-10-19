package plexapi

import (
	"context"
	"fmt"
	"iter"
	"math"
)

// walkPages yields every row of an offset-paged listing from page.Start,
// page.Size rows per request, one request in flight. It stops at an empty
// page, once the offset reaches the total, or after a first page holding
// more rows than asked (the server ignored paging). A page from a nonzero
// offset holding more rows than asked and ending past the total yields only
// an error. The first error ends the walk, as does the growth bound
// (rowLimit). A non-nil wait runs before every request.
func walkPages[T any](ctx context.Context, page Page, wait func(context.Context) error, fetch func(ctx context.Context, page Page) ([]T, int64, error)) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		if err := page.validate(); err != nil {
			var zero T
			yield(zero, err)
			return
		}
		p := pager{next: page, wait: wait}
		for walkPage(ctx, &p, fetch, yield) {
		}
	}
}

func walkPage[T any](ctx context.Context, p *pager, fetch func(ctx context.Context, page Page) ([]T, int64, error), yield func(T, error) bool) bool {
	var zero T
	if p.wait != nil {
		if err := p.wait(ctx); err != nil {
			yield(zero, err)
			return false
		}
	}
	rows, total, err := fetch(ctx, p.next)
	if err == nil && p.offsetIgnored(len(rows), total) {
		err = fmt.Errorf("plexapi: server ignored the page offset and sent %d rows from %d of %d", len(rows), p.next.Start, total)
	}
	if err != nil {
		yield(zero, err)
		return false
	}
	for _, r := range rows {
		if !yield(r, nil) {
			return false
		}
	}
	done, err := p.advance(len(rows), total)
	if err != nil {
		yield(zero, err)
	}
	return !done
}

type pager struct {
	wait                 func(context.Context) error
	next                 Page
	pages, read, maxRows int64
}

// offsetIgnored reports whether a page from a nonzero offset holds more rows
// than asked and ends past total: its rows are not the requested window.
// It compares against total-start so the sum cannot overflow.
func (p *pager) offsetIgnored(n int, total int64) bool {
	start := int64(p.next.Start)
	return start > 0 && n > p.next.Size && (total < start || int64(n) > total-start)
}

// advance reports whether the walk has ended after a page of n rows from a
// listing of total rows; the error says why an ended walk is incomplete.
func (p *pager) advance(n int, total int64) (bool, error) {
	if n > math.MaxInt-p.next.Start {
		return true, fmt.Errorf("plexapi: page offset overflows past %d", p.next.Start)
	}
	end := p.next.Start + n
	if p.pages == 0 {
		if n > p.next.Size {
			if int64(end) < total {
				return true, fmt.Errorf("plexapi: server ignored paging and sent rows %d to %d of %d", p.next.Start, end, total)
			}
			return true, nil
		}
		p.maxRows = rowLimit(total, p.next.Size)
	}
	p.pages++
	p.read += int64(n)
	p.next.Start = end
	switch {
	case n == 0 || int64(end) >= total:
		return true, nil
	case p.read >= p.maxRows:
		return true, fmt.Errorf("plexapi: listing still growing after %d rows in %d pages (offset %d of %d)", p.read, p.pages, end, total)
	}
	return false, nil
}

// rowLimit ends a walk whose total keeps growing: total + size rows read,
// saturating at math.MaxInt64. It counts rows, not requests, so a server
// capping pages below size is still read in full; with full pages it allows
// ceil(total/size)+1 requests.
func rowLimit(total int64, size int) int64 {
	if total > math.MaxInt64-int64(size) {
		return math.MaxInt64
	}
	return total + int64(size)
}

// walkUpdated walks a listing that fetch filters to rows updated at or after
// a bound, ascending by UpdatedAt, reading every page from offset 0 with room
// for size new rows and yielding at most size of them. It never skips by
// offset: a row is new unless it sits at the bound with a key already yielded
// there, so a row that moves, goes or arrives mid-walk cannot hide an unread one.
func walkUpdated(ctx context.Context, sinceUnix int64, size int, wait func(context.Context) error, fetch func(ctx context.Context, since int64, size int) ([]Item, int64, error)) iter.Seq2[Item, error] {
	return func(yield func(Item, error) bool) {
		if size <= 0 {
			yield(Item{}, fmt.Errorf("invalid page size %d", size))
			return
		}
		w := updatedWalk{bound: sinceUnix, size: size, seen: map[string]struct{}{}}
		for w.step(ctx, wait, fetch, yield) {
		}
	}
}

// updatedWalk is walkUpdated's cursor. Invariant: every row updated before
// bound has been yielded, and seen holds the keys yielded at bound itself;
// a seen key that leaves bound costs one row of room, never a skipped row.
// bound and seen come only from the consumed prefix of a page (take): taking
// them from rows past it would count an unyielded row as read.
type updatedWalk struct {
	seen          map[string]struct{}
	bound         int64
	read, maxRows int64
	size          int
	started       bool
}

func (w *updatedWalk) want() int {
	if w.size > math.MaxInt-len(w.seen) {
		return math.MaxInt
	}
	return len(w.seen) + w.size
}

func (w *updatedWalk) step(ctx context.Context, wait func(context.Context) error, fetch func(ctx context.Context, since int64, size int) ([]Item, int64, error), yield func(Item, error) bool) bool {
	if wait != nil {
		if err := wait(ctx); err != nil {
			yield(Item{}, err)
			return false
		}
	}
	want := w.want()
	rows, total, err := fetch(ctx, w.bound, want)
	if err == nil {
		err = w.check(rows, want, total)
	}
	if err != nil {
		yield(Item{}, err)
		return false
	}
	used, fresh := w.take(rows)
	for i := range fresh {
		if !yield(fresh[i], nil) {
			return false
		}
	}
	if used == len(rows) && int64(used) >= total {
		return false
	}
	if err := w.advance(rows[:used], fresh, total); err != nil {
		yield(Item{}, err)
		return false
	}
	return true
}

func (w *updatedWalk) check(rows []Item, want int, total int64) error {
	n := len(rows)
	switch {
	case n > want:
		return fmt.Errorf("plexapi: server ignored the page size and sent %d rows for %d", n, want)
	case int64(n) > total:
		return fmt.Errorf("plexapi: server sent %d rows of a listing of %d (totalSize missing or wrong)", n, total)
	case n == 0 && total > 0:
		return fmt.Errorf("plexapi: server sent an empty page of a listing of %d updated at or after %d", total, w.bound)
	}
	keys := make(map[string]struct{}, n)
	prev := w.bound
	for i := range rows {
		it := &rows[i]
		if it.RatingKey == "" {
			return fmt.Errorf("plexapi: server sent an item updated at %d with no ratingKey", it.UpdatedAt)
		}
		if _, dup := keys[it.RatingKey]; dup {
			return fmt.Errorf("plexapi: server sent ratingKey %s twice in one answer", it.RatingKey)
		}
		keys[it.RatingKey] = struct{}{}
		switch u := it.UpdatedAt; {
		case i == 0 && u < prev:
			return fmt.Errorf("plexapi: server ignored the updatedAt filter: item %s updated at %d is below %d", it.RatingKey, u, prev)
		case u < prev:
			return fmt.Errorf("plexapi: server ignored the updatedAt sort: item %s updated at %d follows %d, out of order", it.RatingKey, u, prev)
		default:
			prev = u
		}
	}
	return nil
}

func (w *updatedWalk) take(rows []Item) (used int, fresh []Item) {
	fresh = make([]Item, 0, min(len(rows), w.size))
	for i := range rows {
		if _, ok := w.seen[rows[i].RatingKey]; ok && rows[i].UpdatedAt == w.bound {
			continue
		}
		if fresh = append(fresh, rows[i]); len(fresh) == w.size {
			return i + 1, fresh
		}
	}
	return len(rows), fresh
}

func (w *updatedWalk) advance(consumed, fresh []Item, total int64) error {
	if !w.started {
		w.maxRows = rowLimit(total, w.size)
		w.started = true
	}
	w.read += int64(len(fresh))
	switch {
	case len(fresh) == 0:
		return fmt.Errorf("plexapi: server sent no unread row updated at or after %d (%d rows of %d, all read)", w.bound, len(consumed), total)
	case w.read >= w.maxRows:
		return fmt.Errorf("plexapi: updated-since listing still growing after %d rows (%d at or after %d)", w.read, total, w.bound)
	}
	if last := consumed[len(consumed)-1].UpdatedAt; last != w.bound {
		w.bound = last
		clear(w.seen)
	}
	for i := range fresh {
		if fresh[i].UpdatedAt == w.bound {
			w.seen[fresh[i].RatingKey] = struct{}{}
		}
	}
	return nil
}
