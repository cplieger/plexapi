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
// (pager.rowLimit). A non-nil wait runs before every request.
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
		p.maxRows = p.rowLimit(total)
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

// rowLimit ends a walk whose total keeps growing: total + p.next.Size rows
// read, saturating at math.MaxInt64. It counts rows, not requests, so a
// server capping pages below p.next.Size is still read in full; with full
// pages it allows ceil(total/p.next.Size)+1 requests.
func (p *pager) rowLimit(total int64) int64 {
	size := int64(p.next.Size)
	if total > math.MaxInt64-size {
		return math.MaxInt64
	}
	return total + size
}
