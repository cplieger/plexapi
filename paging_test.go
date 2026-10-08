package plexapi

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// pageServer serves one page per request from the test's page function,
// recording each request's RawQuery.
type pageServer struct {
	mu      sync.Mutex
	queries []string
}

// pageReply is one served page; a nonzero status replaces the body.
type pageReply struct {
	keys   []int
	total  int64
	status int
}

func (p *pageServer) recorded() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.queries...)
}

func newPageServer(t *testing.T, page func(Page) pageReply) (*Client, *pageServer) {
	t.Helper()
	ps := &pageServer{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ps.mu.Lock()
		ps.queries = append(ps.queries, r.URL.RawQuery)
		ps.mu.Unlock()
		start, errStart := strconv.Atoi(r.URL.Query().Get("X-Plex-Container-Start"))
		size, errSize := strconv.Atoi(r.URL.Query().Get("X-Plex-Container-Size"))
		if errStart != nil || errSize != nil {
			http.Error(w, "missing paging parameters", http.StatusBadRequest)
			return
		}
		reply := page(Page{Start: start, Size: size})
		if reply.status != 0 {
			w.WriteHeader(reply.status)
			return
		}
		rows := make([]string, 0, len(reply.keys))
		for _, k := range reply.keys {
			rows = append(rows, fmt.Sprintf(`{"ratingKey":"%d","viewedAt":%d}`, k, 1700000000+k))
		}
		fmt.Fprintf(w, `{"MediaContainer":{"totalSize":%d,"size":%d,"Metadata":[%s]}}`, reply.total, len(reply.keys), strings.Join(rows, ","))
	}))
	t.Cleanup(srv.Close)
	return newTestClient(t, srv, WithMaxAttempts(1)), ps
}

// window returns the keys page selects from a listing of n rows keyed 0..n-1.
func window(p Page, n int) []int {
	var keys []int
	for k := p.Start; k < p.Start+p.Size && k < n; k++ {
		keys = append(keys, k)
	}
	return keys
}

// listing serves pages of a fixed listing of n rows.
func listing(n int) func(Page) pageReply {
	return func(p Page) pageReply { return pageReply{keys: window(p, n), total: int64(n)} }
}

// collectItems drains a section walk into rating keys and errors.
func collectItems(seq func(func(Item, error) bool)) (keys []string, errs []error) {
	for it, err := range seq {
		if err != nil {
			errs = append(errs, err)
			continue
		}
		keys = append(keys, it.RatingKey)
	}
	return keys, errs
}

func TestWalkSectionItemsPagesInOrder(t *testing.T) {
	c, ps := newPageServer(t, listing(7))
	keys, errs := collectItems(c.WalkSectionItems(t.Context(), "2", MetadataTypeEpisode, Page{Size: 3}, nil))
	if len(errs) != 0 {
		t.Fatalf("WalkSectionItems errors = %v, want none", errs)
	}
	if got, want := strings.Join(keys, ","), "0,1,2,3,4,5,6"; got != want {
		t.Errorf("WalkSectionItems keys = %s, want %s", got, want)
	}
	wantQueries := []string{
		"X-Plex-Container-Size=3&X-Plex-Container-Start=0&type=4",
		"X-Plex-Container-Size=3&X-Plex-Container-Start=3&type=4",
		"X-Plex-Container-Size=3&X-Plex-Container-Start=6&type=4",
	}
	if got := ps.recorded(); strings.Join(got, "|") != strings.Join(wantQueries, "|") {
		t.Errorf("RawQuery per page = %q, want %q", got, wantQueries)
	}
}

// TestWalkSectionItemsOversizedFirstPageComplete pins that a first page
// holding more rows than asked, and at least its totalSize, ends the walk
// cleanly after one request.
func TestWalkSectionItemsOversizedFirstPageComplete(t *testing.T) {
	c, ps := newPageServer(t, func(Page) pageReply { return pageReply{keys: window(Page{Size: 7}, 7), total: 7} })
	keys, errs := collectItems(c.WalkSectionItems(t.Context(), "2", 0, Page{Size: 3}, nil))
	if len(errs) != 0 || len(keys) != 7 {
		t.Errorf("WalkSectionItems over an oversized first page = %d keys, errors %v, want 7 keys and none", len(keys), errs)
	}
	if n := len(ps.recorded()); n != 1 {
		t.Errorf("requests = %d, want 1 (the walk ends after an oversized first page)", n)
	}
}

// TestWalkSectionItemsOversizedFirstPageFromStartComplete pins that an
// oversized first page is judged by the offset it reaches, not its length:
// from Start 3 of 7 rows, a page holding all four remaining rows completes
// the walk.
func TestWalkSectionItemsOversizedFirstPageFromStartComplete(t *testing.T) {
	c, ps := newPageServer(t, func(p Page) pageReply {
		return pageReply{keys: window(Page{Start: p.Start, Size: 100}, 7), total: 7}
	})
	keys, errs := collectItems(c.WalkSectionItems(t.Context(), "2", 0, Page{Start: 3, Size: 2}, nil))
	if len(errs) != 0 {
		t.Errorf("WalkSectionItems(Start 3, Size 2) over a 4-row first page of 7 errors = %v, want none", errs)
	}
	if got, want := strings.Join(keys, ","), "3,4,5,6"; got != want {
		t.Errorf("WalkSectionItems(Start 3, Size 2) keys = %s, want %s", got, want)
	}
	if n := len(ps.recorded()); n != 1 {
		t.Errorf("requests = %d, want 1", n)
	}
}

// TestWalkSectionItemsOversizedFirstPageShortOfTotal pins the one ignored-
// paging shape the walk can detect: a first page holding more rows than
// asked. When that page still ends short of its totalSize, the walk ends
// with an error rather than re-reading rows from another offset.
func TestWalkSectionItemsOversizedFirstPageShortOfTotal(t *testing.T) {
	c, ps := newPageServer(t, func(Page) pageReply { return pageReply{keys: window(Page{Size: 5}, 5), total: 10} })
	keys, errs := collectItems(c.WalkSectionItems(t.Context(), "2", 0, Page{Size: 3}, nil))
	if len(keys) != 5 || len(errs) != 1 {
		t.Errorf("WalkSectionItems = %d keys, errors %v, want 5 keys and one error", len(keys), errs)
	}
	if n := len(ps.recorded()); n != 1 {
		t.Errorf("requests = %d, want 1", n)
	}
}

// TestWalkSectionItemsOversizedPagePastTotalFromStart pins that a first page
// from a nonzero Start ending past its totalSize proves the server ignored
// the offset, so the walk yields one error and none of its rows.
func TestWalkSectionItemsOversizedPagePastTotalFromStart(t *testing.T) {
	tests := []struct {
		name  string
		page  Page
		reply pageReply
	}{
		{name: "full_listing", page: Page{Start: 3, Size: 2}, reply: pageReply{keys: window(Page{Size: 7}, 7), total: 7}},
		{name: "total_below_start", page: Page{Start: 5, Size: 2}, reply: pageReply{keys: window(Page{Size: 3}, 3), total: 3}},
		{name: "min_total", page: Page{Start: 1, Size: 1}, reply: pageReply{keys: window(Page{Size: 2}, 2), total: math.MinInt64}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, ps := newPageServer(t, func(Page) pageReply { return tt.reply })
			keys, errs := collectItems(c.WalkSectionItems(t.Context(), "2", 0, tt.page, nil))
			if len(keys) != 0 || len(errs) != 1 {
				t.Errorf("WalkSectionItems(%+v) over %d rows of total %d = keys %v, errors %v, want no keys and one error", tt.page, len(tt.reply.keys), tt.reply.total, keys, errs)
			}
			if n := len(ps.recorded()); n != 1 {
				t.Errorf("requests = %d, want 1", n)
			}
		})
	}
}

// TestWalkSectionItemsLaterPageIgnoringOffset pins that the offset check
// covers every page: a second page carrying the full listing yields one
// error and none of its rows, after the first page's rows.
func TestWalkSectionItemsLaterPageIgnoringOffset(t *testing.T) {
	c, ps := newPageServer(t, func(p Page) pageReply {
		if p.Start == 0 {
			return pageReply{keys: window(p, 7), total: 7}
		}
		return pageReply{keys: window(Page{Size: 7}, 7), total: 7}
	})
	keys, errs := collectItems(c.WalkSectionItems(t.Context(), "2", 0, Page{Size: 2}, nil))
	if got := strings.Join(keys, ","); got != "0,1" || len(errs) != 1 {
		t.Errorf("WalkSectionItems(Size 2) with a full second page = keys %s, errors %v, want keys 0,1 and one error", got, errs)
	}
	if n := len(ps.recorded()); n != 2 {
		t.Errorf("requests = %d, want 2", n)
	}
}

// TestWalkSectionItemsOversizedPagePastTotalFromZero pins that a first page
// from Start 0 is never out of window: rows past its totalSize are yielded
// and the walk ends cleanly.
func TestWalkSectionItemsOversizedPagePastTotalFromZero(t *testing.T) {
	c, ps := newPageServer(t, func(Page) pageReply { return pageReply{keys: window(Page{Size: 8}, 8), total: 7} })
	keys, errs := collectItems(c.WalkSectionItems(t.Context(), "2", 0, Page{Size: 3}, nil))
	if len(keys) != 8 || len(errs) != 0 {
		t.Errorf("WalkSectionItems(Size 3) over 8 rows of total 7 = %d keys, errors %v, want 8 keys and none", len(keys), errs)
	}
	if n := len(ps.recorded()); n != 1 {
		t.Errorf("requests = %d, want 1", n)
	}
}

// TestWalkSectionItemsShortPagesAdvanceByRowsRead pins that the next offset
// follows the rows actually returned, so a server that caps its page size
// below the request loses no rows.
func TestWalkSectionItemsShortPagesAdvanceByRowsRead(t *testing.T) {
	c, ps := newPageServer(t, func(p Page) pageReply {
		return pageReply{keys: window(Page{Start: p.Start, Size: 2}, 5), total: 5}
	})
	keys, errs := collectItems(c.WalkSectionItems(t.Context(), "2", 0, Page{Size: 3}, nil))
	if len(errs) != 0 {
		t.Fatalf("WalkSectionItems errors = %v, want none", errs)
	}
	if got, want := strings.Join(keys, ","), "0,1,2,3,4"; got != want {
		t.Errorf("WalkSectionItems keys = %s, want %s", got, want)
	}
	if n := len(ps.recorded()); n != 3 {
		t.Errorf("requests = %d, want 3", n)
	}
}

// requestsBeforeEachWait returns a wait that records how many requests ps
// had received at each call.
func requestsBeforeEachWait(ps *pageServer, seen *[]int) func(context.Context) error {
	return func(context.Context) error {
		*seen = append(*seen, len(ps.recorded()))
		return nil
	}
}

// Short pages put request boundaries between multiples of page.Size, so
// only a wait at the real request boundary paces each one.
func TestWalkSectionItemsWaitsBeforeEveryRequest(t *testing.T) {
	c, ps := newPageServer(t, func(p Page) pageReply {
		return pageReply{keys: window(Page{Start: p.Start, Size: 2}, 5), total: 5}
	})
	var seen []int
	keys, errs := collectItems(c.WalkSectionItems(t.Context(), "2", 0, Page{Size: 3}, requestsBeforeEachWait(ps, &seen)))
	if len(errs) != 0 || len(keys) != 5 {
		t.Fatalf("WalkSectionItems over 2-row pages = %d keys, errors %v, want 5 keys and none", len(keys), errs)
	}
	if got, want := fmt.Sprint(seen), "[0 1 2]"; got != want {
		t.Errorf("requests already sent at each wait = %s, want %s (one wait right before each of 3 requests)", got, want)
	}
}

func TestWalkHistoryWaitsBeforeEveryRequest(t *testing.T) {
	c, ps := newPageServer(t, func(p Page) pageReply {
		return pageReply{keys: window(Page{Start: p.Start, Size: 2}, 5), total: 5}
	})
	var seen []int
	rows := 0
	for _, err := range c.WalkHistory(t.Context(), 0, Page{Size: 3}, requestsBeforeEachWait(ps, &seen)) {
		if err != nil {
			t.Fatalf("WalkHistory over 2-row pages error %v", err)
		}
		rows++
	}
	if rows != 5 {
		t.Errorf("WalkHistory over 2-row pages = %d rows, want 5", rows)
	}
	if got, want := fmt.Sprint(seen), "[0 1 2]"; got != want {
		t.Errorf("requests already sent at each wait = %s, want %s (one wait right before each of 3 requests)", got, want)
	}
}

func TestWalkSectionItemsWaitErrorEndsWalkWithoutRequest(t *testing.T) {
	c, ps := newPageServer(t, listing(9))
	errStop := errors.New("budget closed")
	calls := 0
	wait := func(context.Context) error {
		calls++
		if calls == 2 {
			return errStop
		}
		return nil
	}
	keys, errs := collectItems(c.WalkSectionItems(t.Context(), "2", 0, Page{Size: 3}, wait))
	if got := strings.Join(keys, ","); got != "0,1,2" || len(errs) != 1 || !errors.Is(errs[0], errStop) {
		t.Errorf("WalkSectionItems with a wait failing before page 2 = keys %s, errors %v, want keys 0,1,2 and the wait's error once", got, errs)
	}
	if n := len(ps.recorded()); n != 1 {
		t.Errorf("requests = %d, want 1 (none after the failed wait)", n)
	}
}

func TestWalkSectionItemsTotalGrowsMidWalk(t *testing.T) {
	c, ps := newPageServer(t, func(p Page) pageReply {
		if p.Start == 0 {
			return pageReply{keys: window(p, 6), total: 6}
		}
		return pageReply{keys: window(p, 8), total: 8}
	})
	keys, errs := collectItems(c.WalkSectionItems(t.Context(), "2", 0, Page{Size: 3}, nil))
	if len(errs) != 0 {
		t.Fatalf("WalkSectionItems errors = %v, want none", errs)
	}
	if got, want := strings.Join(keys, ","), "0,1,2,3,4,5,6,7"; got != want {
		t.Errorf("WalkSectionItems keys = %s, want %s (rows added during the walk are read)", got, want)
	}
	if n := len(ps.recorded()); n != 3 {
		t.Errorf("requests = %d, want 3", n)
	}
}

func TestWalkSectionItemsStopsAtEmptyPage(t *testing.T) {
	c, ps := newPageServer(t, func(p Page) pageReply {
		if p.Start >= 3 {
			return pageReply{total: 10}
		}
		return pageReply{keys: window(p, 10), total: 10}
	})
	keys, errs := collectItems(c.WalkSectionItems(t.Context(), "2", 0, Page{Size: 3}, nil))
	if len(errs) != 0 || len(keys) != 3 {
		t.Errorf("WalkSectionItems = %d keys, errors %v, want 3 keys and none", len(keys), errs)
	}
	if n := len(ps.recorded()); n != 2 {
		t.Errorf("requests = %d, want 2 (stop at the empty page)", n)
	}
}

func TestWalkSectionItemsYieldsFirstErrorOnce(t *testing.T) {
	c, ps := newPageServer(t, func(p Page) pageReply {
		if p.Start == 6 {
			return pageReply{status: http.StatusBadGateway}
		}
		return pageReply{keys: window(p, 12), total: 12}
	})
	keys, errs := collectItems(c.WalkSectionItems(t.Context(), "2", 0, Page{Size: 3}, nil))
	if len(keys) != 6 {
		t.Errorf("keys before the failing page = %d, want 6", len(keys))
	}
	if len(errs) != 1 {
		t.Fatalf("errors = %v, want exactly one", errs)
	}
	var se *StatusError
	if !errors.As(errs[0], &se) || se.Code != http.StatusBadGateway {
		t.Errorf("error = %v, want a *StatusError 502", errs[0])
	}
	if n := len(ps.recorded()); n != 3 {
		t.Errorf("requests = %d, want 3 (no page after the error)", n)
	}
}

func TestWalkSectionItemsChecksContextBetweenPages(t *testing.T) {
	c, ps := newPageServer(t, listing(9))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var keys []string
	var errs []error
	for it, err := range c.WalkSectionItems(ctx, "2", 0, Page{Size: 3}, nil) {
		if err != nil {
			errs = append(errs, err)
			continue
		}
		keys = append(keys, it.RatingKey)
		if len(keys) == 3 {
			cancel()
		}
	}
	if len(keys) != 3 || len(errs) != 1 || !errors.Is(errs[0], context.Canceled) {
		t.Errorf("walk cancelled after page 0 = %d keys, errors %v, want 3 keys and one context.Canceled", len(keys), errs)
	}
	if n := len(ps.recorded()); n != 1 {
		t.Errorf("requests = %d, want 1 (no request after cancellation)", n)
	}
}

// shortPages serves a stable listing of n rows, capping each page at cap
// rows whatever size was asked.
func shortPages(n, cap int) func(Page) pageReply {
	return func(p Page) pageReply {
		return pageReply{keys: window(Page{Start: p.Start, Size: cap}, n), total: int64(n)}
	}
}

// A server capping every page below the request still has its whole
// listing read: the growth bound counts rows, not requests.
func TestWalkSectionItemsStableTotalShortPagesReadInFull(t *testing.T) {
	c, ps := newPageServer(t, shortPages(1500, 300))
	keys, errs := collectItems(c.WalkSectionItems(t.Context(), "2", 0, Page{Size: 500}, nil))
	if len(keys) != 1500 || len(errs) != 0 {
		t.Errorf("WalkSectionItems(Size 500) over 300-row pages of 1500 = %d keys, errors %v, want 1500 keys and none", len(keys), errs)
	}
	if n := len(ps.recorded()); n != 5 {
		t.Errorf("requests = %d, want 5", n)
	}
}

func TestWalkHistoryStableTotalShortPagesReadInFull(t *testing.T) {
	c, ps := newPageServer(t, shortPages(1500, 300))
	rows, errs := 0, 0
	for _, err := range c.WalkHistory(t.Context(), 0, Page{Size: 500}, nil) {
		if err != nil {
			errs++
			continue
		}
		rows++
	}
	if rows != 1500 || errs != 0 {
		t.Errorf("WalkHistory(Size 500) over 300-row pages of 1500 = %d rows, %d errors, want 1500 and 0", rows, errs)
	}
	if n := len(ps.recorded()); n != 5 {
		t.Errorf("requests = %d, want 5", n)
	}
}

// TestWalkSectionItemsHardRowLimitShortPages pins that the growth bound
// still ends a walk whose short pages chase a totalSize that keeps growing:
// page.Size rows past the first total (6+3), reached on the fifth 2-row page.
func TestWalkSectionItemsHardRowLimitShortPages(t *testing.T) {
	c, ps := newPageServer(t, func(p Page) pageReply {
		if p.Start == 0 {
			return pageReply{keys: window(Page{Size: 2}, 6), total: 6}
		}
		return pageReply{keys: window(Page{Start: p.Start, Size: 2}, p.Start+2), total: int64(p.Start) + 100}
	})
	keys, errs := collectItems(c.WalkSectionItems(t.Context(), "2", 0, Page{Size: 3}, nil))
	if n := len(ps.recorded()); n != 5 {
		t.Errorf("requests = %d, want 5 (10 rows read reaches the 9-row bound)", n)
	}
	if len(keys) != 10 || len(errs) != 1 {
		t.Errorf("WalkSectionItems = %d keys, errors %v, want 10 keys and one growth-limit error", len(keys), errs)
	}
}

// TestWalkSectionItemsHardPageLimit pins the bound against a server whose
// totalSize keeps growing: with full pages, page.Size rows past the first
// total is ceil(6/3)+1 requests, then an error, because the walk is
// incomplete.
func TestWalkSectionItemsHardPageLimit(t *testing.T) {
	c, ps := newPageServer(t, func(p Page) pageReply {
		if p.Start == 0 {
			return pageReply{keys: window(p, 6), total: 6}
		}
		return pageReply{keys: window(p, p.Start+p.Size), total: int64(p.Start) + 100}
	})
	keys, errs := collectItems(c.WalkSectionItems(t.Context(), "2", 0, Page{Size: 3}, nil))
	if n := len(ps.recorded()); n != 3 {
		t.Errorf("requests = %d, want 3 (ceil(6/3)+1)", n)
	}
	if len(keys) != 9 || len(errs) != 1 {
		t.Errorf("WalkSectionItems = %d keys, errors %v, want 9 keys and one page-limit error", len(keys), errs)
	}
}

// TestWalkSectionItemsPageLimitAtMaxTotal pins that the page budget saturates
// rather than wrapping when the first page reports totalSize MaxInt64 with a
// page size of 1: the walk reads page 1, finds it empty, and ends cleanly.
func TestWalkSectionItemsPageLimitAtMaxTotal(t *testing.T) {
	c, _ := newPageServer(t, func(p Page) pageReply {
		if p.Start == 0 {
			return pageReply{keys: []int{0}, total: math.MaxInt64}
		}
		return pageReply{total: math.MaxInt64}
	})
	keys, errs := collectItems(c.WalkSectionItems(t.Context(), "2", 0, Page{Size: 1}, nil))
	if len(keys) != 1 || len(errs) != 0 {
		t.Errorf("WalkSectionItems(totalSize MaxInt64, size 1) = %d keys, errors %v, want 1 key and none", len(keys), errs)
	}
}

// TestWalkHistoryOffsetOverflowSendsNoNegativeStart pins that a walk whose
// next offset would pass math.MaxInt ends with one error instead of
// wrapping into a negative X-Plex-Container-Start.
func TestWalkHistoryOffsetOverflowSendsNoNegativeStart(t *testing.T) {
	c, ps := newPageServer(t, func(p Page) pageReply {
		if p.Start < 0 {
			return pageReply{status: http.StatusBadRequest}
		}
		return pageReply{keys: []int{0}, total: math.MaxInt64}
	})
	rows, errs := 0, 0
	for _, err := range c.WalkHistory(t.Context(), 0, Page{Start: math.MaxInt, Size: 1}, nil) {
		if err != nil {
			errs++
			continue
		}
		rows++
	}
	if rows != 1 || errs != 1 {
		t.Errorf("WalkHistory(Start MaxInt) = %d rows, %d errors, want 1 and 1", rows, errs)
	}
	got := ps.recorded()
	if len(got) != 1 || strings.Contains(strings.Join(got, "|"), "X-Plex-Container-Start=-") {
		t.Errorf("WalkHistory(Start MaxInt) RawQuery per page = %q, want one request and no negative start", got)
	}
}

func TestWalkSectionItemsConsumerBreak(t *testing.T) {
	c, ps := newPageServer(t, listing(9))
	n := 0
	for _, err := range c.WalkSectionItems(t.Context(), "2", 0, Page{Size: 3}, nil) {
		if err != nil {
			t.Fatalf("unexpected error %v", err)
		}
		n++
		if n == 2 {
			break
		}
	}
	if got := len(ps.recorded()); got != 1 {
		t.Errorf("requests after a break on page 0 = %d, want 1", got)
	}
}

func TestWalkSectionItemsRejectsBadArgumentsWithoutRequest(t *testing.T) {
	c, ps := newPageServer(t, listing(3))
	for _, tc := range []struct {
		name    string
		section RatingKey
		page    Page
	}{
		{name: "zero page size", section: "2", page: Page{Size: 0}},
		{name: "negative start", section: "2", page: Page{Start: -1, Size: 3}},
		{name: "invalid section", section: "../2", page: Page{Size: 3}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			keys, errs := collectItems(c.WalkSectionItems(t.Context(), tc.section, 0, tc.page, nil))
			if len(keys) != 0 || len(errs) != 1 {
				t.Errorf("WalkSectionItems(%q, %+v) = %d keys, errors %v, want one error", tc.section, tc.page, len(keys), errs)
			}
		})
	}
	for _, page := range []Page{{Size: 0}, {Start: -1, Size: 2}} {
		t.Run(fmt.Sprintf("history start %d size %d", page.Start, page.Size), func(t *testing.T) {
			n, errs := 0, 0
			for _, err := range c.WalkHistory(t.Context(), 0, page, nil) {
				if err != nil {
					errs++
					continue
				}
				n++
			}
			if n != 0 || errs != 1 {
				t.Errorf("WalkHistory(%+v) = %d rows, %d errors, want 0 and 1", page, n, errs)
			}
		})
	}
	if n := len(ps.recorded()); n != 0 {
		t.Errorf("requests = %d, want 0", n)
	}
}

// TestWalkSectionItemsStartsAtPageStart pins that a walk begins at
// Page.Start and advances by the rows read from there.
func TestWalkSectionItemsStartsAtPageStart(t *testing.T) {
	c, ps := newPageServer(t, listing(7))
	keys, errs := collectItems(c.WalkSectionItems(t.Context(), "2", 0, Page{Start: 3, Size: 2}, nil))
	if len(errs) != 0 {
		t.Fatalf("WalkSectionItems(Start 3) errors = %v, want none", errs)
	}
	if got, want := strings.Join(keys, ","), "3,4,5,6"; got != want {
		t.Errorf("WalkSectionItems(Start 3) keys = %s, want %s", got, want)
	}
	wantQueries := []string{
		"X-Plex-Container-Size=2&X-Plex-Container-Start=3",
		"X-Plex-Container-Size=2&X-Plex-Container-Start=5",
	}
	if got := ps.recorded(); strings.Join(got, "|") != strings.Join(wantQueries, "|") {
		t.Errorf("RawQuery per page = %q, want %q", got, wantQueries)
	}
}

func TestSectionItemsPageReturnsTotal(t *testing.T) {
	c, _ := newPageServer(t, listing(7))
	items, total, err := c.SectionItemsPage(t.Context(), "2", 0, Page{Start: 6, Size: 3})
	if err != nil || total != 7 || len(items) != 1 || items[0].RatingKey != "6" {
		t.Errorf("SectionItemsPage(start 6) = %d items, total %d, err %v, want 1 item (key 6), total 7", len(items), total, err)
	}
}

func TestWalkHistoryPagesOldestFirst(t *testing.T) {
	c, ps := newPageServer(t, listing(5))
	var keys []string
	for e, err := range c.WalkHistory(t.Context(), 1700000000, Page{Size: 2}, nil) {
		if err != nil {
			t.Fatalf("WalkHistory error %v", err)
		}
		keys = append(keys, e.RatingKey)
		if want := 1700000000 + int64(len(keys)-1); e.ViewedAt != want {
			t.Errorf("entry %s ViewedAt = %d, want %d", e.RatingKey, e.ViewedAt, want)
		}
	}
	if got, want := strings.Join(keys, ","), "0,1,2,3,4"; got != want {
		t.Errorf("WalkHistory keys = %s, want %s", got, want)
	}
	wantQueries := []string{
		"sort=viewedAt:asc&viewedAt>=1700000000&X-Plex-Container-Start=0&X-Plex-Container-Size=2",
		"sort=viewedAt:asc&viewedAt>=1700000000&X-Plex-Container-Start=2&X-Plex-Container-Size=2",
		"sort=viewedAt:asc&viewedAt>=1700000000&X-Plex-Container-Start=4&X-Plex-Container-Size=2",
	}
	if got := ps.recorded(); strings.Join(got, "|") != strings.Join(wantQueries, "|") {
		t.Errorf("RawQuery per page = %q, want %q", got, wantQueries)
	}
}

func TestWalkHistoryYieldsFirstErrorOnce(t *testing.T) {
	c, ps := newPageServer(t, func(p Page) pageReply {
		if p.Start == 2 {
			return pageReply{status: http.StatusInternalServerError}
		}
		return pageReply{keys: window(p, 6), total: 6}
	})
	n, errs := 0, 0
	for _, err := range c.WalkHistory(t.Context(), 0, Page{Size: 2}, nil) {
		if err != nil {
			errs++
			continue
		}
		n++
	}
	if n != 2 || errs != 1 || len(ps.recorded()) != 2 {
		t.Errorf("WalkHistory = %d rows, %d errors, %d requests, want 2, 1, 2", n, errs, len(ps.recorded()))
	}
}

// TestWalkHistoryEntryFields pins the row contract: every decoded row is
// yielded, empty rating keys and shared viewedAt seconds included, and the
// history key is kept verbatim up to 512 bytes.
func TestWalkHistoryEntryFields(t *testing.T) {
	long512 := "/status/sessions/history/" + strings.Repeat("9", 512-len("/status/sessions/history/"))
	long513 := long512 + "9"
	srv, _ := fixtureServer(t, map[string]string{
		"/status/sessions/history/all": `{"MediaContainer":{"totalSize":7,"Metadata":[
			{"historyKey":"/status/sessions/history/12","ratingKey":"10","accountID":1,"viewedAt":1700000000},
			{"historyKey":"abc-12","ratingKey":"11","accountID":"7","viewedAt":1700000000},
			{"historyKey":"` + long512 + `","ratingKey":"12","accountID":"5000000000","viewedAt":1700000001},
			{"historyKey":"` + long513 + `","ratingKey":"13","viewedAt":1700000002},
			{"ratingKey":"14","viewedAt":1700000003},
			{"historyKey":"h6","accountID":1,"viewedAt":1700000004},
			{"historyKey":"h7","ratingKey":"15"}]}}`,
	})
	var got []HistoryEntry
	for e, err := range newTestClient(t, srv).WalkHistory(t.Context(), 0, Page{Size: 10}, nil) {
		if err != nil {
			t.Fatalf("WalkHistory error %v", err)
		}
		got = append(got, e)
	}
	want := []HistoryEntry{
		{RatingKey: "10", HistoryKey: "/status/sessions/history/12", AccountID: 1, ViewedAt: 1700000000},
		{RatingKey: "11", HistoryKey: "abc-12", AccountID: 7, ViewedAt: 1700000000},
		{RatingKey: "12", HistoryKey: long512, AccountID: 5000000000, ViewedAt: 1700000001},
		{RatingKey: "13", ViewedAt: 1700000002},
		{RatingKey: "14", ViewedAt: 1700000003},
		{HistoryKey: "h6", AccountID: 1, ViewedAt: 1700000004},
		{RatingKey: "15", HistoryKey: "h7"},
	}
	if len(got) != len(want) {
		t.Fatalf("WalkHistory yielded %d rows, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// A play of a deleted item arrives with no ratingKey and with fields the
// row type does not model (titles, indexes, a date string, a device id).
// It decodes to its history key, play time and account, as live servers
// send it.
func TestWalkHistoryDeletedItemRow(t *testing.T) {
	srv, _ := fixtureServer(t, map[string]string{
		"/status/sessions/history/all": `{"MediaContainer":{"size":2,"totalSize":2,"offset":0,"Metadata":[
			{"historyKey":"/status/sessions/history/105","librarySectionID":"3","title":"Track 1","grandparentTitle":"Artist A","parentTitle":"Album A","type":"track","index":1,"parentIndex":1,"viewedAt":1700000300,"accountID":1,"deviceID":3},
			{"historyKey":"/status/sessions/history/108","librarySectionID":"1","title":"Movie 12","type":"movie","originallyAvailableAt":"1999-09-09","viewedAt":1700000500,"accountID":2,"deviceID":4}]}}`,
	})
	var got []HistoryEntry
	for e, err := range newTestClient(t, srv).WalkHistory(t.Context(), 0, Page{Size: 10}, nil) {
		if err != nil {
			t.Fatalf("WalkHistory error %v", err)
		}
		got = append(got, e)
	}
	want := []HistoryEntry{
		{HistoryKey: "/status/sessions/history/105", ViewedAt: 1700000300, AccountID: 1},
		{HistoryKey: "/status/sessions/history/108", ViewedAt: 1700000500, AccountID: 2},
	}
	if !slices.Equal(got, want) {
		t.Errorf("WalkHistory rows = %+v, want %+v", got, want)
	}
}
