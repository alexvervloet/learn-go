// Package pagination is offset against keyset, and the reason the answer is not "it depends".
//
// # The two shapes
//
//	offset  LIMIT 20 OFFSET 4000
//	keyset  WHERE (created_at, id) < ($1, $2) ORDER BY created_at DESC, id DESC LIMIT 20
//
// Offset is what everyone writes first, because it maps onto a page number and a page number is what the UI
// asks for. It has two problems and only one of them is about speed.
//
// # Problem one: OFFSET reads and discards
//
// Postgres has no way to jump to the 4,000th row. It produces rows in order and throws the first 4,000 away.
// So page 1 reads 20 rows and page 200 reads 4,020, and the cost of a page grows linearly with its number.
// The tests here measure that: the plan's "Rows Removed" and the actual row count say it outright, and no
// timing is needed to prove it.
//
// # Problem two, which is worse: OFFSET skips rows
//
// Between the request for page 1 and the request for page 2, someone inserts a row. Every row shifts down by
// one, and the row that was last on page 1 is now first on page 2, so the client sees it twice. Delete a row
// instead and a row is skipped entirely, never shown to anyone.
//
// That is not a rare race. On any feed ordered by "newest first", where new rows arrive at the top, it
// happens on every page transition where anything was written. TestOffsetSkipsRows and
// TestKeysetDoesNotSkipRows demonstrate both.
//
// # What keyset costs
//
//	no page numbers. You cannot jump to page 7, only forward and back from where you are.
//	no total count, unless you run a second query for it, and on a large table that count is
//	  the slow part.
//	the sort key must be UNIQUE, or the same bug comes back. (created_at) alone is not unique,
//	  so two rows with the same timestamp straddle a page boundary and one is lost.
//	the cursor has to encode every column of the sort key, and changing the sort order changes
//	  the cursor format.
//
// # So which
//
// Keyset for anything a user scrolls, anything an API client iterates, and anything where the table grows.
// Offset for an admin table of 200 rows where a page number is genuinely the feature. The failure mode of
// choosing offset is not slowness at first; it is a support ticket about a missing record, six months later,
// that nobody can reproduce.
package pagination

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Event is one row of the feed.
type Event struct {
	ID        int64
	Actor     string
	Kind      string
	CreatedAt time.Time
	Payload   string
}

// Querier is the subset these functions need.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Page is a result page plus what the client needs to ask for the next one.
type Page struct {
	Events []Event

	// Next is empty when there are no more rows. Empty, rather than an error or a flag, because a
	// client loop reading "while next is not empty" cannot get it wrong.
	Next string

	// HasMore is the same information as a bool, kept because an API's JSON usually wants it and
	// because deriving it correctly needs the trick in Keyset below.
	HasMore bool
}

// Offset is the query everyone writes first.
func Offset(ctx context.Context, q Querier, limit, offset int) ([]Event, error) {
	if limit <= 0 {
		return nil, errors.New("limit must be positive")
	}

	rows, err := q.Query(ctx, `
		SELECT id, actor, kind, created_at, payload
		  FROM events
		 ORDER BY created_at DESC, id DESC
		 LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("offset page at %d: %w", offset, err)
	}

	return collect(rows)
}

// Keyset is the query to use.
//
// Two details make this correct rather than nearly correct.
//
// The comparison is a ROW comparison, `(created_at, id) < ($1, $2)`, not `created_at < $1 AND id < $2`.
// The second is wrong: it excludes rows with the same timestamp and a higher id, which are exactly the rows
// on the boundary. Postgres compares row values lexicographically, which is what "everything after this
// point in the sort order" means, and it can use a composite index for it.
//
// It asks for limit+1 rows and returns limit. That is how HasMore is computed without a second query: if the
// database had one more row to give, there is another page. A count(*) would cost a full scan to answer the
// same question.
func Keyset(ctx context.Context, q Querier, limit int, cursor string) (Page, error) {
	if limit <= 0 {
		return Page{}, errors.New("limit must be positive")
	}

	var (
		rows pgx.Rows
		err  error
	)

	if cursor == "" {
		rows, err = q.Query(ctx, `
			SELECT id, actor, kind, created_at, payload
			  FROM events
			 ORDER BY created_at DESC, id DESC
			 LIMIT $1`, limit+1)
	} else {
		var c Cursor

		c, err = DecodeCursor(cursor)
		if err != nil {
			return Page{}, err
		}

		rows, err = q.Query(ctx, `
			SELECT id, actor, kind, created_at, payload
			  FROM events
			 WHERE (created_at, id) < ($2, $3)
			 ORDER BY created_at DESC, id DESC
			 LIMIT $1`, limit+1, c.CreatedAt, c.ID)
	}

	if err != nil {
		return Page{}, fmt.Errorf("keyset page after %q: %w", cursor, err)
	}

	events, err := collect(rows)
	if err != nil {
		return Page{}, err
	}

	page := Page{Events: events}

	if len(events) > limit {
		page.Events = events[:limit]
		page.HasMore = true

		last := page.Events[limit-1]
		page.Next = EncodeCursor(Cursor{CreatedAt: last.CreatedAt, ID: last.ID})
	}

	return page, nil
}

// KeysetWrong is the version with the mistake, kept because the mistake is subtle and common.
//
// `created_at < $1 AND id < $2` looks equivalent to a row comparison and is not. It drops every row that
// shares the cursor's timestamp, which on a table where timestamps collide means dropping rows silently.
func KeysetWrong(ctx context.Context, q Querier, limit int, after Cursor) ([]Event, error) {
	rows, err := q.Query(ctx, `
		SELECT id, actor, kind, created_at, payload
		  FROM events
		 WHERE created_at < $2 AND id < $3
		 ORDER BY created_at DESC, id DESC
		 LIMIT $1`, limit, after.CreatedAt, after.ID)
	if err != nil {
		return nil, fmt.Errorf("wrong keyset page: %w", err)
	}

	return collect(rows)
}

// KeysetSingleColumn is the other version with the mistake: a sort key that is not unique.
//
// Ordering by created_at alone and cursoring on created_at alone loses every row that shares a timestamp
// with the last row of a page, or repeats them, depending on which comparison is used. A non-unique
// pagination key is the same bug as offset pagination, arrived at by a different route.
func KeysetSingleColumn(ctx context.Context, q Querier, limit int, after time.Time) ([]Event, error) {
	rows, err := q.Query(ctx, `
		SELECT id, actor, kind, created_at, payload
		  FROM events
		 WHERE created_at < $2
		 ORDER BY created_at DESC
		 LIMIT $1`, limit, after)
	if err != nil {
		return nil, fmt.Errorf("single-column keyset page: %w", err)
	}

	return collect(rows)
}

// Cursor is the position in the sort order.
//
// Both columns of the ORDER BY, because a cursor is only as unique as the key it encodes.
type Cursor struct {
	CreatedAt time.Time
	ID        int64
}

// EncodeCursor turns a Cursor into an opaque string.
//
// Opaque on purpose, and base64 of a plain format rather than JSON. Three reasons in order of importance:
//
// A client that can read the cursor will parse it, and then the cursor format is a public API that cannot
// change. Base64 does not prevent that, it just makes it obviously a bad idea.
//
// The encoding must round-trip the timestamp exactly. RFC 3339 with nanoseconds does; a Unix second does
// not, and a cursor that loses precision straddles rows that share a second. That is the same
// non-unique-key bug in a different place.
//
// It must be URL-safe, because it goes in a query string. base64.RawURLEncoding, so no padding to escape.
func EncodeCursor(c Cursor) string {
	raw := c.CreatedAt.UTC().Format(time.RFC3339Nano) + "|" + strconv.FormatInt(c.ID, 10)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// ErrBadCursor is returned for anything that does not decode.
//
// A sentinel, so a handler can turn it into a 400 rather than a 500. A malformed cursor is the client's
// fault and an API that returns 500 for it cannot be monitored.
var ErrBadCursor = errors.New("malformed cursor")

// DecodeCursor parses what EncodeCursor produced.
func DecodeCursor(s string) (Cursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return Cursor{}, fmt.Errorf("%w: not base64: %w", ErrBadCursor, err)
	}

	parts := strings.Split(string(raw), "|")
	if len(parts) != 2 {
		return Cursor{}, fmt.Errorf("%w: expected 2 fields, got %d", ErrBadCursor, len(parts))
	}

	createdAt, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return Cursor{}, fmt.Errorf("%w: bad timestamp: %w", ErrBadCursor, err)
	}

	id, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return Cursor{}, fmt.Errorf("%w: bad id: %w", ErrBadCursor, err)
	}

	return Cursor{CreatedAt: createdAt, ID: id}, nil
}

// Count is the total, kept separate because it is the expensive part.
//
// An exact count(*) on a large table is a full scan or a full index scan, every time, and it cannot be
// cached usefully because it changes. This is why "10 of 4,238,911 pages" is rare on big sites and
// "load more" is common.
//
// The cheap alternative, when an estimate will do, is reltuples from pg_class, which ANALYZE maintains.
// CountEstimate below does that and the test measures how far off it is.
func Count(ctx context.Context, q Querier) (int64, error) {
	var n int64
	if err := q.QueryRow(ctx, "SELECT count(*) FROM events").Scan(&n); err != nil {
		return 0, fmt.Errorf("counting events: %w", err)
	}
	return n, nil
}

// CountEstimate reads the planner's row estimate instead of counting.
//
// Accurate to within a few percent on a table that is analysed, arbitrarily wrong on one that is not, and
// constant time either way. For "about 4.2 million results" that is the right trade.
func CountEstimate(ctx context.Context, q Querier) (int64, error) {
	var n float64
	if err := q.QueryRow(ctx,
		"SELECT reltuples FROM pg_class WHERE relname = 'events'").Scan(&n); err != nil {
		return 0, fmt.Errorf("estimating the count: %w", err)
	}

	// -1 means "never analysed", which is a real state and not a count.
	if n < 0 {
		return 0, errors.New("the table has never been analysed, so there is no estimate")
	}

	return int64(n), nil
}

func collect(rows pgx.Rows) ([]Event, error) {
	events, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Event, error) {
		var e Event
		err := r.Scan(&e.ID, &e.Actor, &e.Kind, &e.CreatedAt, &e.Payload)
		return e, err
	})
	if err != nil {
		return nil, fmt.Errorf("scanning events: %w", err)
	}

	return events, nil
}
