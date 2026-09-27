// Package seed fills the schema with enough rows that a query plan is interesting.
//
// # Why the row count matters
//
// Postgres chooses a plan from statistics, and below a few thousand rows a sequential scan beats an
// index scan on everything: reading eight pages in order is cheaper than walking a B-tree and then
// fetching scattered rows. So a test seeding fifty rows and asserting "it uses the index" asserts
// the opposite of what the planner will do, and a test seeding fifty rows and measuring a speedup
// measures noise.
//
// Every number here is chosen so the planner's decision is the one production would make:
//
//	10,000 books over 200 authors   enough that a sequential scan is measurably worse
//	5,000 orders over 1,000 customers
//	~15,000 order items
//
// # COPY, not INSERT
//
// The seeder uses pgx's CopyFrom, which speaks the COPY protocol. The benchmark in pgxdemo measures
// what that is worth; the short version is that it is the difference between a seeder that takes a
// second and one that takes a minute, and it is why this package can afford 10,000 rows.
package seed

import (
	"context"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Counts describes how much data to generate.
type Counts struct {
	Authors   int
	Books     int
	Customers int
	Orders    int
}

// Default returns the sizes the module's tests use.
func Default() Counts {
	return Counts{Authors: 200, Books: 10_000, Customers: 1_000, Orders: 5_000}
}

// Small returns sizes for a test that only needs correctness, not a realistic plan.
func Small() Counts {
	return Counts{Authors: 5, Books: 20, Customers: 5, Orders: 10}
}

// Load fills the tables. It assumes they are empty.
//
// Deterministic: the generator is seeded with a fixed value, so two runs produce identical data and
// a query plan can be compared between them. A seeder using the global rand source makes every
// EXPLAIN output different and every measurement unrepeatable.
func Load(ctx context.Context, db bulkLoader, c Counts) error {
	r := rand.New(rand.NewPCG(1, 2))

	if err := loadAuthors(ctx, db, r, c.Authors); err != nil {
		return fmt.Errorf("authors: %w", err)
	}
	if err := loadBooks(ctx, db, r, c); err != nil {
		return fmt.Errorf("books: %w", err)
	}
	if err := loadCustomers(ctx, db, c.Customers); err != nil {
		return fmt.Errorf("customers: %w", err)
	}
	if err := loadOrders(ctx, db, r, c); err != nil {
		return fmt.Errorf("orders: %w", err)
	}

	// ANALYZE is not optional. Postgres plans from statistics, and after a bulk load there are
	// none: the planner assumes a default row count and picks a plan for a table it thinks has
	// 2,550 rows. Every index test in this module would fail without this line, and the
	// failure looks like "the index is not used" rather than "the statistics are stale".
	if _, err := db.Exec(ctx, "ANALYZE authors, books, customers, orders, order_items"); err != nil {
		return fmt.Errorf("analyze: %w", err)
	}

	return nil
}

// bulkLoader is what Load needs: CopyFrom for the bulk inserts and Exec for ANALYZE.
type bulkLoader interface {
	CopyFrom(ctx context.Context, table pgx.Identifier, cols []string, src pgx.CopyFromSource) (int64, error)
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

func loadAuthors(ctx context.Context, db bulkLoader, r *rand.Rand, n int) error {
	countries := []string{"GB", "US", "FR", "DE", "JP", "BR", "IN", "NG"}

	rows := make([][]any, n)
	for i := range rows {
		rows[i] = []any{
			fmt.Sprintf("Author %04d", i),
			countries[r.IntN(len(countries))],
		}
	}

	_, err := db.CopyFrom(ctx, pgx.Identifier{"authors"}, []string{"name", "country"},
		pgx.CopyFromRows(rows))
	return err
}

func loadBooks(ctx context.Context, db bulkLoader, r *rand.Rand, c Counts) error {
	// A vocabulary small enough that full-text queries match a useful number of rows, and
	// varied enough that the GIN index has work to do.
	adjectives := []string{"Quick", "Silent", "Golden", "Hollow", "Distant", "Broken", "Endless", "Crimson"}
	nouns := []string{"River", "Mountain", "Garden", "Machine", "Kingdom", "Shadow", "Compass", "Harvest"}

	rows := make([][]any, c.Books)
	base := time.Date(1990, 1, 1, 0, 0, 0, 0, time.UTC)

	for i := range rows {
		rows[i] = []any{
			int64(r.IntN(c.Authors) + 1), // authors get ids 1..n
			fmt.Sprintf("The %s %s %d", adjectives[r.IntN(len(adjectives))],
				nouns[r.IntN(len(nouns))], i),
			fmt.Sprintf("978-%010d", i),
			base.AddDate(0, r.IntN(400), 0),
			r.IntN(9000) + 500,
		}
	}

	_, err := db.CopyFrom(ctx, pgx.Identifier{"books"},
		[]string{"author_id", "title", "isbn", "published", "price_cents"},
		pgx.CopyFromRows(rows))
	return err
}

func loadCustomers(ctx context.Context, db bulkLoader, n int) error {
	rows := make([][]any, n)
	for i := range rows {
		rows[i] = []any{
			fmt.Sprintf("customer%04d@example.com", i),
			fmt.Sprintf("Customer %04d", i),
		}
	}

	_, err := db.CopyFrom(ctx, pgx.Identifier{"customers"}, []string{"email", "name"},
		pgx.CopyFromRows(rows))
	return err
}

func loadOrders(ctx context.Context, db bulkLoader, r *rand.Rand, c Counts) error {
	statuses := []string{"pending", "paid", "paid", "paid", "shipped", "shipped", "cancelled"}

	orders := make([][]any, c.Orders)
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	for i := range orders {
		orders[i] = []any{
			int64(r.IntN(c.Customers) + 1),
			base.Add(time.Duration(r.IntN(365*24)) * time.Hour),
			statuses[r.IntN(len(statuses))],
		}
	}

	if _, err := db.CopyFrom(ctx, pgx.Identifier{"orders"},
		[]string{"customer_id", "placed_at", "status"}, pgx.CopyFromRows(orders)); err != nil {
		return err
	}

	// Order lines, one to five per order. The composite primary key forbids the same book
	// twice in one order, so the book ids per order have to be distinct.
	var items [][]any
	for orderID := 1; orderID <= c.Orders; orderID++ {
		used := make(map[int]bool)

		for range r.IntN(5) + 1 {
			bookID := r.IntN(c.Books) + 1
			if used[bookID] {
				continue
			}
			used[bookID] = true

			items = append(items, []any{
				int64(orderID), int64(bookID), r.IntN(3) + 1, r.IntN(9000) + 500,
			})
		}
	}

	// The trigger on order_items fires per row, so loading 15,000 lines runs 15,000 UPDATEs
	// on orders. That is the cost of the denormalised column, it is real, and it is measured
	// in the README. Disabling the trigger for the bulk load and recomputing once afterwards
	// is the production answer; keeping it on here is deliberate, so the seeder pays what a
	// real insert path pays.
	_, err := db.CopyFrom(ctx, pgx.Identifier{"order_items"},
		[]string{"order_id", "book_id", "quantity", "unit_cents"}, pgx.CopyFromRows(items))
	return err
}
