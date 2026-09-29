package dbtest

import (
	"context"
	"testing"
)

// TestPoolConnectsAndMigrates is the smoke test for the harness itself. Everything else in the
// module depends on it, so a clear failure here saves reading nine other packages' output.
func TestPoolConnectsAndMigrates(t *testing.T) {
	p := Pool(t)

	var one int
	if err := p.QueryRow(context.Background(), "SELECT 1").Scan(&one); err != nil {
		t.Fatalf("SELECT 1: %v", err)
	}
	if one != 1 {
		t.Errorf("SELECT 1 returned %d", one)
	}

	// The migrations ran, so the tables exist.
	for _, table := range []string{"authors", "books", "customers", "orders", "order_items", "accounts"} {
		var exists bool
		err := p.QueryRow(context.Background(),
			"SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = $1)",
			table).Scan(&exists)
		if err != nil {
			t.Fatalf("checking %s: %v", table, err)
		}
		if !exists {
			t.Errorf("table %s does not exist; the migrations did not run", table)
		}
	}
}

// TestRollbackIsolationWrites is the property the whole harness rests on: a row written in one test is
// invisible to the next.
//
// Two tests in source order, because that is the only way to observe it from inside the suite.
func TestRollbackIsolationWrites(t *testing.T) {
	tx := Tx(t)

	_, err := tx.Exec(context.Background(),
		"INSERT INTO authors (name, country) VALUES ('Isolation Test', 'XX')")
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	// Visible inside the transaction.
	var count int
	if err := tx.QueryRow(context.Background(),
		"SELECT count(*) FROM authors WHERE name = 'Isolation Test'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("count inside the transaction = %d, want 1", count)
	}
}

func TestRollbackIsolationIsUndone(t *testing.T) {
	// A fresh transaction, so this cannot see the previous test's uncommitted row.
	tx := Tx(t)

	var count int
	if err := tx.QueryRow(context.Background(),
		"SELECT count(*) FROM authors WHERE name = 'Isolation Test'").Scan(&count); err != nil {
		t.Fatal(err)
	}

	if count != 0 {
		t.Errorf("count = %d: the previous test's row survived, so isolation is broken", count)
	}
}

// TestUncommittedRowsAreInvisibleToThePool is the limitation stated as a test, because relying on
// rollback isolation and then querying through the pool is a confusing way to lose an afternoon.
func TestUncommittedRowsAreInvisibleToThePool(t *testing.T) {
	tx := Tx(t)
	p := Pool(t)

	ctx := context.Background()

	if _, err := tx.Exec(ctx,
		"INSERT INTO authors (name) VALUES ('Only In The Transaction')"); err != nil {
		t.Fatal(err)
	}

	// The pool is a DIFFERENT connection, so it cannot see an uncommitted row.
	var count int
	if err := p.QueryRow(ctx,
		"SELECT count(*) FROM authors WHERE name = 'Only In The Transaction'").Scan(&count); err != nil {
		t.Fatal(err)
	}

	if count != 0 {
		t.Errorf("the pool saw %d uncommitted rows; the isolation level is not what this "+
			"harness assumes", count)
	}

	t.Log("rows written in a dbtest.Tx are invisible through dbtest.Pool. Code under test " +
		"must therefore receive the transaction, not reach for the pool itself.")
}

// TestTruncateResetsSequences, which rollback isolation cannot do and which a test asserting on a
// specific ID needs.
func TestTruncateResetsSequences(t *testing.T) {
	p := Pool(t)
	ctx := context.Background()

	Truncate(t, "authors")

	var id int64
	if err := p.QueryRow(ctx,
		"INSERT INTO authors (name) VALUES ('First') RETURNING id").Scan(&id); err != nil {
		t.Fatal(err)
	}

	if id != 1 {
		t.Errorf("first id after TRUNCATE RESTART IDENTITY = %d, want 1", id)
	}

	Truncate(t, "authors")

	if err := p.QueryRow(ctx,
		"INSERT INTO authors (name) VALUES ('Again') RETURNING id").Scan(&id); err != nil {
		t.Fatal(err)
	}
	if id != 1 {
		t.Errorf("id after a second TRUNCATE = %d, want 1", id)
	}

	Truncate(t, "authors")
}

// TestGeneratedColumnIsMaintained: the books.search tsvector is GENERATED, so it cannot drift and
// nothing has to remember to update it.
func TestGeneratedColumnIsMaintained(t *testing.T) {
	tx := Tx(t)
	ctx := context.Background()

	var authorID int64
	if err := tx.QueryRow(ctx,
		"INSERT INTO authors (name) VALUES ('Generated') RETURNING id").Scan(&authorID); err != nil {
		t.Fatal(err)
	}

	var search string
	err := tx.QueryRow(ctx,
		`INSERT INTO books (author_id, title, isbn, published, price_cents)
		 VALUES ($1, 'The Quick Brown Foxes', 'gen-1', '2020-01-01', 100)
		 RETURNING search::text`, authorID).Scan(&search)
	if err != nil {
		t.Fatal(err)
	}

	// English stemming: "foxes" becomes "fox", and "the" is a stop word and disappears.
	for _, want := range []string{"quick", "brown", "fox"} {
		if !contains(search, want) {
			t.Errorf("search = %q, want it to contain %q", search, want)
		}
	}
	if contains(search, "'the'") {
		t.Errorf("search = %q, expected 'the' to be a stop word", search)
	}

	t.Logf("search = %s", search)

	// An UPDATE to the title updates the generated column too, with nothing remembering to.
	if err := tx.QueryRow(ctx,
		`UPDATE books SET title = 'Sleeping Dogs' WHERE isbn = 'gen-1' RETURNING search::text`).
		Scan(&search); err != nil {
		t.Fatal(err)
	}

	if !contains(search, "sleep") || !contains(search, "dog") {
		t.Errorf("after the update, search = %q", search)
	}
	if contains(search, "fox") {
		t.Errorf("after the update, search still contains the old title: %q", search)
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

// TestDenormalisedTotalIsMaintained: the trigger keeps orders.total_cents correct through every
// path, which is the only thing that makes the denormalisation defensible.
func TestDenormalisedTotalIsMaintained(t *testing.T) {
	tx := Tx(t)
	ctx := context.Background()

	var authorID, customerID, orderID int64
	var bookA, bookB int64

	mustScan := func(sql string, args ...any) int64 {
		t.Helper()
		var id int64
		if err := tx.QueryRow(ctx, sql, args...).Scan(&id); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
		return id
	}

	authorID = mustScan("INSERT INTO authors (name) VALUES ('T') RETURNING id")
	customerID = mustScan("INSERT INTO customers (email, name) VALUES ('t@example.com', 'T') RETURNING id")
	bookA = mustScan(`INSERT INTO books (author_id, title, isbn, published, price_cents)
	                  VALUES ($1, 'A', 'trg-a', '2020-01-01', 1000) RETURNING id`, authorID)
	bookB = mustScan(`INSERT INTO books (author_id, title, isbn, published, price_cents)
	                  VALUES ($1, 'B', 'trg-b', '2020-01-01', 2000) RETURNING id`, authorID)
	orderID = mustScan("INSERT INTO orders (customer_id) VALUES ($1) RETURNING id", customerID)

	total := func() int {
		t.Helper()
		var n int
		if err := tx.QueryRow(ctx, "SELECT total_cents FROM orders WHERE id = $1", orderID).
			Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	if got := total(); got != 0 {
		t.Errorf("a new order's total = %d, want 0", got)
	}

	// INSERT
	if _, err := tx.Exec(ctx,
		"INSERT INTO order_items (order_id, book_id, quantity, unit_cents) VALUES ($1, $2, 2, 1000)",
		orderID, bookA); err != nil {
		t.Fatal(err)
	}
	if got := total(); got != 2000 {
		t.Errorf("after the first line, total = %d, want 2000", got)
	}

	if _, err := tx.Exec(ctx,
		"INSERT INTO order_items (order_id, book_id, quantity, unit_cents) VALUES ($1, $2, 1, 2000)",
		orderID, bookB); err != nil {
		t.Fatal(err)
	}
	if got := total(); got != 4000 {
		t.Errorf("after the second line, total = %d, want 4000", got)
	}

	// UPDATE, which is the case a delta-based trigger gets wrong.
	if _, err := tx.Exec(ctx,
		"UPDATE order_items SET quantity = 5 WHERE order_id = $1 AND book_id = $2",
		orderID, bookA); err != nil {
		t.Fatal(err)
	}
	if got := total(); got != 7000 {
		t.Errorf("after the quantity update, total = %d, want 7000", got)
	}

	// A price change on the same line, so both OLD and NEW matter.
	if _, err := tx.Exec(ctx,
		"UPDATE order_items SET unit_cents = 500 WHERE order_id = $1 AND book_id = $2",
		orderID, bookA); err != nil {
		t.Fatal(err)
	}
	if got := total(); got != 4500 {
		t.Errorf("after the price update, total = %d, want 4500", got)
	}

	// DELETE, where there is no NEW row and the trigger has to use OLD.
	if _, err := tx.Exec(ctx,
		"DELETE FROM order_items WHERE order_id = $1 AND book_id = $2", orderID, bookA); err != nil {
		t.Fatal(err)
	}
	if got := total(); got != 2000 {
		t.Errorf("after the delete, total = %d, want 2000", got)
	}

	// And the last line going leaves 0, not NULL, which is what the COALESCE is for.
	if _, err := tx.Exec(ctx, "DELETE FROM order_items WHERE order_id = $1", orderID); err != nil {
		t.Fatal(err)
	}
	if got := total(); got != 0 {
		t.Errorf("with no lines, total = %d, want 0", got)
	}
}
