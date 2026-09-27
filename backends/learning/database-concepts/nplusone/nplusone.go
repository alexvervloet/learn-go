// Package nplusone is the N+1 query problem, which in Go you have to write out by hand.
//
// # It looks different here
//
// In SQLAlchemy the N+1 is invisible. You load a list of authors, you touch author.books in a loop,
// and the ORM issues a query per author behind an attribute access. Nothing in the source says
// "query". That is why the Python mirror of this module spends its first file just making the
// queries visible with echo=True.
//
// pgx has no lazy loading, so the equivalent Go code contains a literal loop with a literal Query
// call inside it. You can see it. And people still write it, constantly, because the loop is usually
// somewhere else: a handler calls a service in a loop, or a GraphQL resolver runs per field, or a
// helper that takes one ID gets called from a range statement three files away.
//
// So the lesson is not "beware the ORM". It is that the shape of the problem survives the absence of
// an ORM, and the only reliable defence is counting queries in a test.
//
// # The four fixes, and when each is right
//
//	JOIN               one query, one round trip. The rows repeat the parent's columns once per
//	                   child, so a parent with 50 children arrives 50 times. Fine for small
//	                   fan-out, wasteful for wide parent rows.
//	WHERE id = ANY($1) two queries, two round trips. Fetch the parents, collect the IDs, fetch
//	                   every child in one go, group in Go. No duplication. This is what a
//	                   dataloader does.
//	pgx.Batch          N+1 statements, ONE round trip. Keeps the N+1 shape and removes the cost
//	                   of it. The escape hatch when the queries genuinely differ.
//	LATERAL join       one query, for the case the others cannot do: the top 3 children per
//	                   parent. A plain join plus a limit in Go reads every child to discard
//	                   most of them.
//
// # Why this is hard to see locally
//
// A query over a unix socket to a local Postgres costs tens of microseconds. Over a network to a
// managed database it costs one to two milliseconds, almost all of it round trip. So the N+1 that is
// 4x slower on a laptop is 100x slower in production, and the fix looks like a micro-optimisation
// right up to the moment it is an outage. The tests here count round trips, which is the number that
// does not depend on where the database is.
package nplusone

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/jackc/pgx/v5"
)

// Author is a parent row with its children attached.
type Author struct {
	ID    int64
	Name  string
	Books []Book
}

// Book is a child row.
type Book struct {
	ID         int64
	Title      string
	PriceCents int32
}

// Querier is the three methods these functions need, plus the batch one.
//
// Declared here rather than imported, again: pgx does not ship an interface, and naming the methods
// at the point of use is what makes Counter below possible without touching any caller.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults
}

// Counter wraps a Querier and counts round trips.
//
// This is the whole reason the functions below take an interface. A count is the only assertion about
// N+1 that holds on every machine: "this returns the same data in 2 queries rather than 201" is a
// fact about the code, and "this is 40ms faster" is a fact about one laptop.
//
// The counts are atomic because a test can hand the same Counter to concurrent callers.
type Counter struct {
	Querier

	queries atomic.Int64
	batches atomic.Int64

	// statements counts the statements inside batches, so a batch of 200 shows up as 200
	// statements and 1 round trip. That difference is the point of pgx.Batch.
	statements atomic.Int64
}

// NewCounter wraps q.
func NewCounter(q Querier) *Counter { return &Counter{Querier: q} }

// Query counts and delegates.
func (c *Counter) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	c.queries.Add(1)
	return c.Querier.Query(ctx, sql, args...)
}

// QueryRow counts and delegates.
func (c *Counter) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	c.queries.Add(1)
	return c.Querier.QueryRow(ctx, sql, args...)
}

// SendBatch counts one round trip and however many statements the batch holds.
func (c *Counter) SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults {
	c.batches.Add(1)
	c.statements.Add(int64(b.Len()))
	return c.Querier.SendBatch(ctx, b)
}

// RoundTrips is the number that matters: every query and every batch is one wait for the network.
func (c *Counter) RoundTrips() int { return int(c.queries.Load() + c.batches.Load()) }

// Statements is how much SQL the database actually parsed and planned.
func (c *Counter) Statements() int { return int(c.queries.Load() + c.statements.Load()) }

// Reset zeroes the counts, so one Counter can measure several calls.
func (c *Counter) Reset() {
	c.queries.Store(0)
	c.batches.Store(0)
	c.statements.Store(0)
}

// String reports both numbers.
func (c *Counter) String() string {
	return fmt.Sprintf("%d round trips, %d statements", c.RoundTrips(), c.Statements())
}

var _ Querier = (*Counter)(nil)

// Naive is the bug. One query for the authors, then one per author for the books.
//
// Nothing here is clever or unusual, which is the point: this is the obvious way to write it, it is
// correct, and it issues limit+1 queries.
func Naive(ctx context.Context, q Querier, limit int) ([]Author, error) {
	rows, err := q.Query(ctx,
		"SELECT id, name FROM authors ORDER BY id LIMIT $1", limit)
	if err != nil {
		return nil, fmt.Errorf("selecting authors: %w", err)
	}

	authors, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Author, error) {
		var a Author
		err := r.Scan(&a.ID, &a.Name)
		return a, err
	})
	if err != nil {
		return nil, fmt.Errorf("scanning authors: %w", err)
	}

	// Here it is. A query inside a loop.
	//
	// Indexing rather than ranging by value, because a range gives a copy and appending to
	// a.Books would append to the copy. That is a separate Go trap and it bites here.
	for i := range authors {
		bookRows, err := q.Query(ctx,
			"SELECT id, title, price_cents FROM books WHERE author_id = $1 ORDER BY id",
			authors[i].ID)
		if err != nil {
			return nil, fmt.Errorf("selecting books for author %d: %w", authors[i].ID, err)
		}

		books, err := pgx.CollectRows(bookRows, scanBook)
		if err != nil {
			return nil, fmt.Errorf("scanning books for author %d: %w", authors[i].ID, err)
		}

		authors[i].Books = books
	}

	return authors, nil
}

// Join is one query. The parent's columns repeat once per child.
//
// The cost is in the wire format, not the plan: an author with 50 books sends the author's name 50
// times. With a name that is short this is nothing. With a parent row holding a description or a JSON
// blob it is the reason the join is slower than two queries, and that surprises people.
func Join(ctx context.Context, q Querier, limit int) ([]Author, error) {
	rows, err := q.Query(ctx, `
		SELECT a.id, a.name, b.id, b.title, b.price_cents
		  FROM (SELECT id, name FROM authors ORDER BY id LIMIT $1) a
		  LEFT JOIN books b ON b.author_id = a.id
		 ORDER BY a.id, b.id`, limit)
	if err != nil {
		return nil, fmt.Errorf("joining: %w", err)
	}
	defer rows.Close()

	var (
		authors []Author
		current *Author
	)

	for rows.Next() {
		var (
			authorID   int64
			name       string
			bookID     *int64
			title      *string
			priceCents *int32
		)

		if err := rows.Scan(&authorID, &name, &bookID, &title, &priceCents); err != nil {
			return nil, fmt.Errorf("scanning the join: %w", err)
		}

		// A new parent starts when the ID changes, which only works because of the ORDER BY.
		// A join without one can interleave parents and this grouping silently produces
		// duplicates.
		if current == nil || current.ID != authorID {
			authors = append(authors, Author{ID: authorID, Name: name})

			// The pointer is taken AFTER the append and refreshed on every new
			// parent, which is the only reason this is safe: append may move the
			// backing array, and a pointer held across that writes into the old one.
			// The compiler will not mention it.
			current = &authors[len(authors)-1]
		}

		// LEFT JOIN, so an author with no books gives one row of NULLs. Scanning those into
		// pointers is how you tell "no books" from "a book with an empty title".
		if bookID != nil {
			current.Books = append(current.Books, Book{
				ID:         *bookID,
				Title:      *title,
				PriceCents: *priceCents,
			})
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading the join: %w", err)
	}

	return authors, nil
}

// TwoQueries is the dataloader pattern: fetch the parents, then fetch every child at once.
//
// = ANY($1) rather than IN with a built list, for two reasons. It takes one parameter instead of N,
// so it stays under the 65535-parameter protocol limit, and the plan is the same whatever the length,
// so Postgres reuses it instead of planning a new statement per batch size.
func TwoQueries(ctx context.Context, q Querier, limit int) ([]Author, error) {
	rows, err := q.Query(ctx, "SELECT id, name FROM authors ORDER BY id LIMIT $1", limit)
	if err != nil {
		return nil, fmt.Errorf("selecting authors: %w", err)
	}

	authors, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Author, error) {
		var a Author
		err := r.Scan(&a.ID, &a.Name)
		return a, err
	})
	if err != nil {
		return nil, fmt.Errorf("scanning authors: %w", err)
	}

	if len(authors) == 0 {
		return authors, nil
	}

	ids := make([]int64, len(authors))
	index := make(map[int64]int, len(authors))

	for i, a := range authors {
		ids[i] = a.ID
		index[a.ID] = i
	}

	bookRows, err := q.Query(ctx, `
		SELECT author_id, id, title, price_cents
		  FROM books WHERE author_id = ANY($1) ORDER BY author_id, id`, ids)
	if err != nil {
		return nil, fmt.Errorf("selecting books: %w", err)
	}
	defer bookRows.Close()

	for bookRows.Next() {
		var (
			authorID int64
			b        Book
		)

		if err := bookRows.Scan(&authorID, &b.ID, &b.Title, &b.PriceCents); err != nil {
			return nil, fmt.Errorf("scanning books: %w", err)
		}

		i, ok := index[authorID]
		if !ok {
			// Impossible given the query, and checked anyway: without this, a stray
			// author_id would panic on the map's zero value and write to authors[0].
			return nil, fmt.Errorf("book %d has unexpected author %d", b.ID, authorID)
		}

		authors[i].Books = append(authors[i].Books, b)
	}

	if err := bookRows.Err(); err != nil {
		return nil, fmt.Errorf("reading books: %w", err)
	}

	return authors, nil
}

// Batched keeps the query-per-author shape and sends them all in one round trip.
//
// This is pipelining, not a transaction: pgx writes every statement, then reads every result. So it
// still parses and plans N statements, and the database still does N index lookups. What disappears
// is N-1 network waits, which on a remote database is almost all of the cost.
//
// The reason to reach for this rather than = ANY: when the queries are genuinely different. A
// dashboard needing six unrelated aggregates has no single query, and six statements in one round
// trip beats six round trips.
func Batched(ctx context.Context, q Querier, limit int) ([]Author, error) {
	rows, err := q.Query(ctx, "SELECT id, name FROM authors ORDER BY id LIMIT $1", limit)
	if err != nil {
		return nil, fmt.Errorf("selecting authors: %w", err)
	}

	authors, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Author, error) {
		var a Author
		err := r.Scan(&a.ID, &a.Name)
		return a, err
	})
	if err != nil {
		return nil, fmt.Errorf("scanning authors: %w", err)
	}

	if len(authors) == 0 {
		return authors, nil
	}

	batch := &pgx.Batch{}

	for i := range authors {
		// Queue returns a *QueuedQuery whose callbacks run during Close, which is the
		// alternative to reading the results positionally below. The positional form is
		// used here because it is easier to see what is happening.
		batch.Queue("SELECT id, title, price_cents FROM books WHERE author_id = $1 ORDER BY id",
			authors[i].ID)
	}

	results := q.SendBatch(ctx, batch)

	// Close must happen on every path, and its error must be checked. A batch whose results are
	// not all read leaves unread data on the connection, and pgx deals with that by breaking the
	// connection, so the failure shows up in whatever query runs next. Close is deferred for the
	// error paths and called explicitly at the end for its error; the second call is a no-op.
	defer func() { _ = results.Close() }()

	for i := range authors {
		bookRows, err := results.Query()
		if err != nil {
			return nil, fmt.Errorf("batch result %d: %w", i, err)
		}

		books, err := pgx.CollectRows(bookRows, scanBook)
		if err != nil {
			return nil, fmt.Errorf("scanning batch result %d: %w", i, err)
		}

		authors[i].Books = books
	}

	if err := results.Close(); err != nil {
		return nil, fmt.Errorf("closing the batch: %w", err)
	}

	return authors, nil
}

// TopNPerAuthor is the case a join and = ANY cannot do efficiently: the N most expensive books per
// author.
//
// Without LATERAL the options are to fetch every book and slice in Go, which reads 10,000 rows to
// return 60, or a window function with an outer filter, which also reads every row and then discards.
// LATERAL runs the inner query once per outer row with its own LIMIT, so the index gives it the top N
// and stops.
//
// The keyword worth remembering is that the inner query may reference a.id, which a plain subquery in
// FROM may not. That is the whole difference.
func TopNPerAuthor(ctx context.Context, q Querier, limit, n int) ([]Author, error) {
	rows, err := q.Query(ctx, `
		SELECT a.id, a.name, b.id, b.title, b.price_cents
		  FROM (SELECT id, name FROM authors ORDER BY id LIMIT $1) a
		  LEFT JOIN LATERAL (
		        SELECT id, title, price_cents
		          FROM books
		         WHERE author_id = a.id
		         ORDER BY price_cents DESC, id
		         LIMIT $2
		  ) b ON true
		 ORDER BY a.id, b.price_cents DESC, b.id`, limit, n)
	if err != nil {
		return nil, fmt.Errorf("lateral join: %w", err)
	}
	defer rows.Close()

	var (
		authors []Author
		current *Author
	)

	for rows.Next() {
		var (
			authorID   int64
			name       string
			bookID     *int64
			title      *string
			priceCents *int32
		)

		if err := rows.Scan(&authorID, &name, &bookID, &title, &priceCents); err != nil {
			return nil, fmt.Errorf("scanning the lateral join: %w", err)
		}

		if current == nil || current.ID != authorID {
			authors = append(authors, Author{ID: authorID, Name: name})
			current = &authors[len(authors)-1]
		}

		if bookID != nil {
			current.Books = append(current.Books, Book{
				ID: *bookID, Title: *title, PriceCents: *priceCents,
			})
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading the lateral join: %w", err)
	}

	return authors, nil
}

// CountRows is a small helper so the tests can report how much data crossed the wire.
func CountRows(ctx context.Context, q Querier, sql string, args ...any) (int, error) {
	var n int
	if err := q.QueryRow(ctx, "SELECT count(*) FROM ("+sql+") t", args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("counting rows: %w", err)
	}
	return n, nil
}

func scanBook(r pgx.CollectableRow) (Book, error) {
	var b Book
	err := r.Scan(&b.ID, &b.Title, &b.PriceCents)
	return b, err
}
