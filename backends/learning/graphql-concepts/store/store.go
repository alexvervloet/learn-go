// Package store is the data behind the schema, with a query counter.
//
// # Why in memory and why it counts
//
// The point of this module is the N+1 problem and what a dataloader does about it, and that is a question about
// HOW MANY QUERIES ran, not how long they took. An in-memory store with a counter answers it exactly, on any
// machine, with no database to skip when it is missing.
//
// The same rule as the rest of this repo: assert the machine-independent quantity. A test asserting "resolving
// 50 authors' books made 1 query rather than 50" is a fact about the code. One asserting "it was 40ms faster" is
// a fact about one laptop.
//
// The Delay field exists so the benchmarks can model a real database, where the round trip dominates.
package store

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// Author is a row.
type Author struct {
	ID   string
	Name string
}

// Book is a row.
type Book struct {
	ID         string
	Title      string
	PriceCents int32
	AuthorID   string
}

// ErrNotFound is returned for a missing id.
var ErrNotFound = errors.New("not found")

// Store holds the data.
type Store struct {
	mu sync.RWMutex

	authors map[string]Author
	books   map[string]Book

	// authorBooks is the index a real schema would need, and having it means the store is not the
	// thing being measured: the query count is about the RESOLVERS, not about a missing index.
	authorBooks map[string][]string

	// Delay is added to every query, so a benchmark can model a database round trip. Zero in tests,
	// because a test that sleeps is a test that gets skipped.
	Delay time.Duration

	// pool bounds how many queries can be in flight, modelling a connection pool.
	//
	// # Why this matters and why the first benchmark was wrong without it
	//
	// GraphQL resolves SIBLING FIELDS CONCURRENTLY. So 20 authors' books resolvers run in 20
	// goroutines, and against a store with no concurrency limit their 20 one-millisecond queries
	// take one millisecond in total, not twenty.
	//
	// Which made the first version of the benchmark report the naive resolver as FASTER than the
	// batched one at every latency, and it was not measuring anything wrong: an N+1 against an
	// unbounded database really does cost one query's latency.
	//
	// The cost is on the DATABASE: 21 connections instead of 2, and 21 queries of work instead of 2.
	// A real service has a pool (backend-concepts measures a pool of 10 serving 50 goroutines), so
	// the 21st query waits for a connection and the latency appears. That is what this models.
	pool chan struct{}

	queries atomic.Int64
	rows    atomic.Int64
}

// New builds a store with n authors and books per author.
func New(authors, booksPerAuthor int) *Store {
	s := &Store{
		authors:     make(map[string]Author, authors),
		books:       make(map[string]Book, authors*booksPerAuthor),
		authorBooks: make(map[string][]string, authors),
	}

	for a := 1; a <= authors; a++ {
		authorID := "author-" + strconv.Itoa(a)

		s.authors[authorID] = Author{
			ID:   authorID,
			Name: "Author " + strconv.Itoa(a),
		}

		for b := 1; b <= booksPerAuthor; b++ {
			bookID := fmt.Sprintf("book-%d-%d", a, b)

			s.books[bookID] = Book{
				ID:         bookID,
				Title:      fmt.Sprintf("Book %d by Author %d", b, a),
				PriceCents: int32(1000 + (a*b)%500),
				AuthorID:   authorID,
			}

			s.authorBooks[authorID] = append(s.authorBooks[authorID], bookID)
		}
	}

	return s
}

// Queries returns how many queries have run.
func (s *Store) Queries() int64 { return s.queries.Load() }

// Rows returns how many rows have been returned.
//
// The second number that matters. A dataloader turns 50 queries into 1 and returns the same rows, so the query
// count falls and the row count does not. A test asserting only on queries would pass for an implementation that
// fetched the whole table once.
func (s *Store) Rows() int64 { return s.rows.Load() }

// Reset zeroes the counters.
func (s *Store) Reset() {
	s.queries.Store(0)
	s.rows.Store(0)
}

// SetMaxConcurrent bounds in-flight queries, modelling a connection pool.
//
// Zero or less means unbounded, which is the default and is not what any real database does.
func (s *Store) SetMaxConcurrent(n int) {
	if n <= 0 {
		s.pool = nil
		return
	}

	s.pool = make(chan struct{}, n)
}

func (s *Store) record(rows int) {
	s.queries.Add(1)
	s.rows.Add(int64(rows))

	if s.Delay <= 0 {
		return
	}

	// Acquire a "connection" for the duration of the query. Without the pool every concurrent
	// resolver sleeps at the same time and 20 queries of 1ms take 1ms; with it, 20 queries through a
	// pool of 4 take 5ms, which is what a real service sees.
	if s.pool != nil {
		s.pool <- struct{}{}
		defer func() { <-s.pool }()
	}

	time.Sleep(s.Delay)
}

// AuthorByID is the single-row lookup, which is the one a naive resolver calls per item.
func (s *Store) AuthorByID(ctx context.Context, id string) (Author, error) {
	if err := ctx.Err(); err != nil {
		return Author{}, err
	}

	s.mu.RLock()
	author, ok := s.authors[id]
	s.mu.RUnlock()

	s.record(boolToInt(ok))

	if !ok {
		return Author{}, fmt.Errorf("%w: author %s", ErrNotFound, id)
	}

	return author, nil
}

// AuthorsByIDs is the batched lookup, which is what a dataloader calls once.
//
// Returns a MAP rather than a slice, and that is the important part of a batch function's signature: the caller
// asked for a set of ids and some of them may not exist, so a slice would have to be the same length and order
// as the input with a hole for each miss. A map makes the absence explicit and the ordering someone else's
// problem.
func (s *Store) AuthorsByIDs(ctx context.Context, ids []string) (map[string]Author, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	out := make(map[string]Author, len(ids))

	s.mu.RLock()
	for _, id := range ids {
		if author, ok := s.authors[id]; ok {
			out[id] = author
		}
	}
	s.mu.RUnlock()

	s.record(len(out))

	return out, nil
}

// BooksByAuthor is the per-author lookup.
func (s *Store) BooksByAuthor(ctx context.Context, authorID string) ([]Book, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.RLock()
	books := s.booksFor(authorID)
	s.mu.RUnlock()

	s.record(len(books))

	return books, nil
}

// BooksByAuthors is the batched version.
func (s *Store) BooksByAuthors(ctx context.Context, authorIDs []string) (map[string][]Book, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	out := make(map[string][]Book, len(authorIDs))

	rows := 0

	s.mu.RLock()
	for _, id := range authorIDs {
		books := s.booksFor(id)
		if len(books) > 0 {
			out[id] = books
			rows += len(books)
		}
	}
	s.mu.RUnlock()

	s.record(rows)

	return out, nil
}

// booksFor reads the index. Called with the read lock held.
func (s *Store) booksFor(authorID string) []Book {
	ids := s.authorBooks[authorID]

	books := make([]Book, 0, len(ids))
	for _, id := range ids {
		if book, ok := s.books[id]; ok {
			books = append(books, book)
		}
	}

	return books
}

// BookByID looks up one book.
func (s *Store) BookByID(ctx context.Context, id string) (Book, error) {
	if err := ctx.Err(); err != nil {
		return Book{}, err
	}

	s.mu.RLock()
	book, ok := s.books[id]
	s.mu.RUnlock()

	s.record(boolToInt(ok))

	if !ok {
		return Book{}, fmt.Errorf("%w: book %s", ErrNotFound, id)
	}

	return book, nil
}

// Authors lists authors in id order.
func (s *Store) Authors(ctx context.Context, limit int) ([]Author, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.RLock()

	ids := make([]string, 0, len(s.authors))
	for id := range s.authors {
		ids = append(ids, id)
	}

	// Sorted by the numeric suffix, so "author-10" comes after "author-9". A plain string sort would
	// put it after "author-1", which is the classic lexicographic-ordering bug and would make every
	// pagination test assert the wrong thing.
	slices.SortFunc(ids, compareNumericSuffix)

	if limit > 0 && limit < len(ids) {
		ids = ids[:limit]
	}

	authors := make([]Author, 0, len(ids))
	for _, id := range ids {
		authors = append(authors, s.authors[id])
	}

	s.mu.RUnlock()

	s.record(len(authors))

	return authors, nil
}

// BooksPage is one page of books, in id order, for the pagination tests.
//
// Keyset, not offset, and the reason is the same as in backend-concepts: offset reads everything it skips and
// repeats or drops rows when the data changes underneath it. A GraphQL connection's cursor is exactly the
// keyset cursor with a different name.
func (s *Store) BooksPage(ctx context.Context, after string, limit int) ([]Book, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}

	s.mu.RLock()

	ids := make([]string, 0, len(s.books))
	for id := range s.books {
		ids = append(ids, id)
	}

	slices.SortFunc(ids, compareBookID)

	start := 0
	if after != "" {
		// The cursor names a POSITION IN THE DATA, so the scan finds it rather than counting. In a
		// database this is a WHERE clause and an index seek; here it is a search over a sorted
		// slice, which has the same property: the page after a cursor does not move when a row is
		// inserted before it.
		if i, found := slices.BinarySearchFunc(ids, after, compareBookID); found {
			start = i + 1
		} else {
			start = i
		}
	}

	// One more than asked for, which is how hasNextPage is computed without a second query. A
	// count(*) would cost a full scan to answer the same question.
	end := min(start+limit+1, len(ids))

	page := make([]Book, 0, end-start)
	for _, id := range ids[start:end] {
		page = append(page, s.books[id])
	}

	s.mu.RUnlock()

	hasMore := len(page) > limit
	if hasMore {
		page = page[:limit]
	}

	s.record(len(page))

	return page, hasMore, nil
}

// CountBooks is the expensive one, kept separate so a test can measure what totalCount costs.
func (s *Store) CountBooks(ctx context.Context) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}

	s.mu.RLock()
	n := len(s.books)
	s.mu.RUnlock()

	// Counted as a query returning every row, which is what count(*) does in a database: it reads
	// them all. Modelling it as a cheap lookup would hide the thing the test is measuring.
	s.record(n)

	return n, nil
}

// CreateBook inserts a book.
func (s *Store) CreateBook(ctx context.Context, b Book) (Book, error) {
	if err := ctx.Err(); err != nil {
		return Book{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.authors[b.AuthorID]; !ok {
		s.record(0)
		return Book{}, fmt.Errorf("%w: author %s", ErrNotFound, b.AuthorID)
	}

	if b.ID == "" {
		b.ID = fmt.Sprintf("book-new-%d", len(s.books)+1)
	}

	s.books[b.ID] = b
	s.authorBooks[b.AuthorID] = append(s.authorBooks[b.AuthorID], b.ID)

	s.record(1)

	return b, nil
}

func compareNumericSuffix(a, b string) int {
	return compareSuffixes(a, b, 1)
}

// compareBookID orders "book-<author>-<n>" by both numbers.
func compareBookID(a, b string) int {
	return compareSuffixes(a, b, 2)
}

// compareSuffixes compares the last n dash-separated numeric parts.
//
// Written out rather than using a string sort because "book-1-10" must come after "book-1-9", and every
// pagination assertion in this module depends on the order being the one a database's ORDER BY would produce.
func compareSuffixes(a, b string, n int) int {
	aParts := splitLast(a, n)
	bParts := splitLast(b, n)

	for i := range n {
		av, bv := aParts[i], bParts[i]

		if av != bv {
			if av < bv {
				return -1
			}
			return 1
		}
	}

	return 0
}

func splitLast(s string, n int) []int {
	out := make([]int, n)

	// Walk backwards collecting numeric segments.
	end := len(s)

	for i := n - 1; i >= 0; i-- {
		start := end

		for start > 0 && s[start-1] >= '0' && s[start-1] <= '9' {
			start--
		}

		v, err := strconv.Atoi(s[start:end])
		if err != nil {
			v = 0
		}

		out[i] = v

		end = start
		if end > 0 {
			end--
		}
	}

	return out
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
