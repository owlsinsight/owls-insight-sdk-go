package owls

import (
	"context"
	"iter"
	"net/http"
	"net/url"
	"strconv"
)

const (
	defaultPageSize    = 1000
	maxPageSize        = 5000
	defaultPageRetries = 3
)

// PageOptions controls IterHistoryOdds and IterHistoryProps.
type PageOptions struct {
	// PageSize is the rows per request: default 1000, at most the API's 5000.
	PageSize int
	// MaxPages stops after this many pages; 0 reads until a short page.
	MaxPages int
	// MaxRetries is the retries per page on 429 and 503, honouring Retry-After.
	// 0 means the default of 3; a negative value disables retries.
	MaxRetries int
}

func (o *PageOptions) resolve() (pageSize, maxPages, maxRetries int) {
	pageSize, maxRetries = defaultPageSize, defaultPageRetries
	if o == nil {
		return pageSize, 0, maxRetries
	}
	if o.PageSize != 0 {
		pageSize = min(max(o.PageSize, 1), maxPageSize)
	}
	switch {
	case o.MaxRetries < 0:
		maxRetries = 0
	case o.MaxRetries > 0:
		maxRetries = o.MaxRetries
	}
	return pageSize, max(o.MaxPages, 0), maxRetries
}

// page reads pages of a history endpoint in order with limit/offset and yields
// their rows, stopping after a page shorter than the page size. An error is
// yielded once, then the sequence ends.
func page[Row any, Resp any](ctx context.Context, c *Client, path string, base url.Values, o *PageOptions,
	rows func(*Resp) []Row) iter.Seq2[Row, error] {
	return func(yield func(Row, error) bool) {
		pageSize, maxPages, maxRetries := o.resolve()
		for n, offset := 0, 0; maxPages == 0 || n < maxPages; n, offset = n+1, offset+pageSize {
			q := url.Values{}
			for k, v := range base {
				q[k] = v
			}
			q.Set("limit", strconv.Itoa(pageSize))
			q.Set("offset", strconv.Itoa(offset))
			resp := new(Resp)
			if err := c.request(ctx, http.MethodGet, path, q, resp, maxRetries); err != nil {
				var zero Row
				yield(zero, err)
				return
			}
			got := rows(resp)
			for _, r := range got {
				if !yield(r, nil) {
					return
				}
			}
			if len(got) < pageSize {
				return
			}
		}
	}
}

// IterHistoryOdds yields every odds snapshot of one archived game, one page at a
// time, in order. This is the intended way to export a game's history: do not fan
// out one request per book or market in parallel. p.Limit and p.Offset are ignored.
//
//	for s, err := range c.IterHistoryOdds(ctx, owls.HistoryOddsParams{EventID: id}, nil) {
//		if err != nil { return err }
//		rows = append(rows, s)
//	}
func (c *Client) IterHistoryOdds(ctx context.Context, p HistoryOddsParams, o *PageOptions) iter.Seq2[OddsSnapshot, error] {
	return page(ctx, c, "/api/v1/history/odds", p.query(), o, func(r *HistoryOddsResponse) []OddsSnapshot {
		if r.Data == nil {
			return nil
		}
		return r.Data.Snapshots
	})
}

// IterHistoryProps yields every prop snapshot of one archived game, one page at a
// time. Same contract as IterHistoryOdds.
func (c *Client) IterHistoryProps(ctx context.Context, p HistoryPropsParams, o *PageOptions) iter.Seq2[PropsSnapshot, error] {
	return page(ctx, c, "/api/v1/history/props", p.query(), o, func(r *HistoryPropsResponse) []PropsSnapshot {
		if r.Data == nil {
			return nil
		}
		return r.Data.Snapshots
	})
}
