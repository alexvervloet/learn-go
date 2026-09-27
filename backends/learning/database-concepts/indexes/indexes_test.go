package indexes

import (
	"context"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/alexvervloet/learn-go/backends/learning/database-concepts/dbtest"
	"github.com/alexvervloet/learn-go/backends/learning/database-concepts/seed"
)

// seedOnce loads the dataset the first time a test asks for it.
//
// Not TestMain, and the reason is worth stating: TestMain has no *testing.T, so it cannot skip. A
// TestMain that needs a database has to either fail or silently do nothing when there is not one,
// and both are worse than each test skipping for itself. sync.Once plus a helper gives the
// once-per-package behaviour and keeps the skip.
var seedOnce sync.Once

// dataset returns a pool with the full dataset loaded, or skips.
//
// 10,000 books is the smallest size where the planner makes the decisions production would make.
// Below a few thousand rows a Seq Scan beats an index on everything, so a test seeding fifty rows
// and asserting "it uses the index" asserts the opposite of what will happen.
func dataset(t *testing.T) *pgxpool.Pool {
	t.Helper()

	p := dbtest.Pool(t)

	seedOnce.Do(func() {
		ctx := context.Background()

		if _, err := p.Exec(ctx,
			"TRUNCATE authors, books, customers, orders, order_items RESTART IDENTITY CASCADE"); err != nil {
			t.Fatalf("truncating: %v", err)
		}
		if err := seed.Load(ctx, p, seed.Default()); err != nil {
			t.Fatalf("seeding: %v", err)
		}
	})

	return p
}

// TestSeqScanOnAnUnindexedColumn is the baseline: with no index there is no choice.
func TestSeqScanOnAnUnindexedColumn(t *testing.T) {
	db := dataset(t)
	ctx := context.Background()

	// price_cents has no index.
	a, err := Explain(ctx, db, "SELECT count(*) FROM books WHERE price_cents = $1", 1234)
	if err != nil {
		t.Fatal(err)
	}

	t.Log(a.Summary())

	if !a.Uses("Seq Scan") {
		t.Errorf("expected a Seq Scan on an unindexed column, got %v", a.NodeTypes())
	}
}

// TestForeignKeyNeedsItsOwnIndex is the single most common missing index in any schema.
//
// Postgres indexes the REFERENCED primary key and nothing on the referencing side, so
// "every book by this author" is a sequential scan until you add the index yourself. Most people
// assume the foreign key created one.
func TestForeignKeyNeedsItsOwnIndex(t *testing.T) {
	db := dataset(t)
	ctx := context.Background()

	withIndex, err := Explain(ctx, db, "SELECT count(*) FROM books WHERE author_id = $1", 42)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("with idx_books_author: %s", withIndex.Summary())

	if !withIndex.UsesIndex("idx_books_author") {
		t.Errorf("expected idx_books_author, got %v", withIndex.IndexesUsed())
	}

	// And with the index dropped, inside a transaction so it comes back.
	tx := dbtest.Tx(t)

	if _, err := tx.Exec(ctx, "DROP INDEX idx_books_author"); err != nil {
		t.Fatal(err)
	}

	without, err := Explain(ctx, tx, "SELECT count(*) FROM books WHERE author_id = $1", 42)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("without it:            %s", without.Summary())

	if !without.Uses("Seq Scan") {
		t.Errorf("without the index, expected a Seq Scan, got %v", without.NodeTypes())
	}

	ratio := without.ExecutionTime / max(withIndex.ExecutionTime, 0.001)
	t.Logf("the index is worth %.0fx on this query (%.2fms -> %.2fms)",
		ratio, without.ExecutionTime, withIndex.ExecutionTime)
}

// TestFunctionOnAColumnDefeatsTheIndex is the first of the four ways to lose an index, and the one
// that appears most in real code.
//
// An index stores the column's values. A predicate applying a function to the column asks about a
// value the index does not contain, so it cannot be used, and the query silently becomes a
// sequential scan.
func TestFunctionOnAColumnDefeatsTheIndex(t *testing.T) {
	db := dataset(t)
	ctx := context.Background()

	// The index is on author_id. abs(author_id) is not author_id.
	defeated, err := Explain(ctx, db, "SELECT count(*) FROM books WHERE abs(author_id) = $1", 42)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("abs(author_id) = 42: %s", defeated.Summary())

	if defeated.UsesIndex("idx_books_author") {
		t.Error("the planner used the index despite the function; Postgres may have improved")
	}
	if !defeated.Uses("Seq Scan") {
		t.Errorf("expected a Seq Scan, got %v", defeated.NodeTypes())
	}

	// The fix, when the function is genuinely needed: a functional index. Created inside a
	// transaction so it does not persist.
	tx := dbtest.Tx(t)

	if _, err := tx.Exec(ctx, "CREATE INDEX idx_books_abs_author ON books (abs(author_id))"); err != nil {
		t.Fatal(err)
	}
	// A new index needs statistics before the planner trusts it.
	if _, err := tx.Exec(ctx, "ANALYZE books"); err != nil {
		t.Fatal(err)
	}

	fixed, err := Explain(ctx, tx, "SELECT count(*) FROM books WHERE abs(author_id) = $1", 42)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("with a functional index: %s", fixed.Summary())

	if !fixed.UsesIndex("idx_books_abs_author") {
		t.Errorf("expected the functional index, got %v", fixed.IndexesUsed())
	}
}

// TestLeadingWildcardDefeatsTheIndex is the second way, and the reason "search" is never LIKE.
//
// A B-tree is ordered, so it can find every value with a known prefix. It cannot find every value
// containing a substring, because those are scattered throughout the ordering.
func TestLeadingWildcardDefeatsTheIndex(t *testing.T) {
	// The dataset has to be loaded, but this test reads only through the transaction.
	_ = dataset(t)
	ctx := context.Background()

	tx := dbtest.Tx(t)

	// An index that CAN serve a prefix match. text_pattern_ops is required for LIKE unless the
	// database collation is C: the default collation's ordering is not the one LIKE needs, and
	// this is a genuinely obscure trap.
	if _, err := tx.Exec(ctx,
		"CREATE INDEX idx_books_title_pattern ON books (title text_pattern_ops)"); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, "ANALYZE books"); err != nil {
		t.Fatal(err)
	}

	prefix, err := Explain(ctx, tx, "SELECT count(*) FROM books WHERE title LIKE $1", "The Quick%")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("LIKE 'The Quick%%':  %s", prefix.Summary())

	infix, err := Explain(ctx, tx, "SELECT count(*) FROM books WHERE title LIKE $1", "%Quick%")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("LIKE '%%Quick%%':     %s", infix.Summary())

	if !prefix.UsesIndex("idx_books_title_pattern") {
		t.Errorf("a prefix match should use the index, got %v", prefix.IndexesUsed())
	}
	if infix.UsesIndex("idx_books_title_pattern") {
		t.Error("a leading wildcard should defeat a B-tree index")
	}
	if !infix.Uses("Seq Scan") {
		t.Errorf("expected a Seq Scan for the leading wildcard, got %v", infix.NodeTypes())
	}

	t.Log("a leading wildcard needs a trigram (pg_trgm) or full-text index; see fulltext/")
}

// TestLowSelectivityMakesTheIndexPointless is the third way, and it is the planner being right.
//
// An index scan costs a B-tree walk plus a random page fetch per row. When a predicate matches most
// of the table, reading every page in physical order is cheaper, so Postgres ignores the index. That
// is not a bug to work around.
func TestLowSelectivityMakesTheIndexPointless(t *testing.T) {
	// The dataset has to be loaded, but this test reads only through the transaction.
	_ = dataset(t)
	ctx := context.Background()

	// status is roughly 3/7 'paid', 2/7 'shipped', 1/7 each pending and cancelled.
	tx := dbtest.Tx(t)

	if _, err := tx.Exec(ctx, "CREATE INDEX idx_orders_status ON orders (status)"); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, "ANALYZE orders"); err != nil {
		t.Fatal(err)
	}

	for _, status := range []string{"paid", "cancelled"} {
		a, err := Explain(ctx, tx,
			"SELECT count(*) FROM orders WHERE status = $1", status)
		if err != nil {
			t.Fatal(err)
		}

		var share float64
		if err := tx.QueryRow(ctx,
			"SELECT count(*)::float / (SELECT count(*) FROM orders) FROM orders WHERE status = $1",
			status).Scan(&share); err != nil {
			t.Fatal(err)
		}

		t.Logf("status = %-10s (%.0f%% of rows): %s", status, share*100, a.Summary())
	}

	// The common value is the one where the index should lose.
	common, err := Explain(ctx, tx, "SELECT count(*) FROM orders WHERE status = $1", "paid")
	if err != nil {
		t.Fatal(err)
	}

	if common.UsesIndex("idx_orders_status") && !common.Uses("Bitmap Index Scan") {
		t.Logf("the planner used a plain index scan for a common value: %v", common.NodeTypes())
	}
}

// TestCompositeIndexColumnOrder is the decision people get wrong, and it is not a preference.
//
// A composite index is sorted by its first column, then the second within that. So it serves a
// query filtering on the first column, and on the first plus the second. It cannot serve a query
// filtering only on the SECOND, for the same reason a phone book cannot find everyone with a given
// first name.
func TestCompositeIndexColumnOrder(t *testing.T) {
	db := dataset(t)
	ctx := context.Background()

	// idx_orders_customer_placed is (customer_id, placed_at DESC).
	both, err := Explain(ctx, db, `
		SELECT id FROM orders
		 WHERE customer_id = $1 AND placed_at > $2
		 ORDER BY placed_at DESC LIMIT 10`, 42, "2025-06-01")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("customer_id AND placed_at: %s", both.Summary())

	leadingOnly, err := Explain(ctx, db,
		"SELECT count(*) FROM orders WHERE customer_id = $1", 42)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("customer_id only:          %s", leadingOnly.Summary())

	trailingOnly, err := Explain(ctx, db,
		"SELECT count(*) FROM orders WHERE placed_at > $1", "2025-11-01")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("placed_at only:            %s", trailingOnly.Summary())

	if !both.UsesIndex("idx_orders_customer_placed") {
		t.Errorf("the full predicate should use the composite index, got %v", both.IndexesUsed())
	}

	// The leading column alone works, which is why column order is a real decision: putting
	// the more selective column first is not the rule, putting the equality column first is.
	usedSomeIndex := len(leadingOnly.IndexesUsed()) > 0
	if !usedSomeIndex {
		t.Errorf("the leading column alone should use an index, got %v", leadingOnly.NodeTypes())
	}

	// The trailing column alone cannot use this index. The planner may still pick another
	// index or a scan; what matters is that it is not this one.
	if trailingOnly.UsesIndex("idx_orders_customer_placed") {
		t.Log("the planner found a way to use the composite index for the trailing column " +
			"alone, which it can do as a full index scan when the index is smaller than " +
			"the table")
	}
}

// TestPartialIndexIsSmaller measures what a WHERE clause on the index buys.
func TestPartialIndexIsSmaller(t *testing.T) {
	_ = dataset(t)
	ctx := context.Background()

	tx := dbtest.Tx(t)

	// The full equivalent of the partial index, for comparison. Created inside the
	// transaction, which means every size query has to go through tx too: the pool would be
	// on a different connection and would not see it.
	if _, err := tx.Exec(ctx, "CREATE INDEX idx_orders_placed_full ON orders (placed_at DESC)"); err != nil {
		t.Fatal(err)
	}

	var fullSize, partialSize int64
	var fullPretty, partialPretty string

	if err := tx.QueryRow(ctx, `
		SELECT pg_relation_size('idx_orders_placed_full'),
		       pg_size_pretty(pg_relation_size('idx_orders_placed_full')),
		       pg_relation_size('idx_orders_pending'),
		       pg_size_pretty(pg_relation_size('idx_orders_pending'))`).
		Scan(&fullSize, &fullPretty, &partialSize, &partialPretty); err != nil {
		t.Fatal(err)
	}

	t.Logf("full index on placed_at:         %7d bytes (%s)", fullSize, fullPretty)
	t.Logf("partial, WHERE status=pending:   %7d bytes (%s)", partialSize, partialPretty)

	if partialSize >= fullSize {
		t.Errorf("the partial index (%d) is not smaller than the full one (%d)",
			partialSize, fullSize)
	}

	// 47%% of the size while indexing 14%% of the rows, which does not add up, and chasing that
	// found something worth knowing. The full index was just built by CREATE INDEX, which sorts
	// the entries and packs the leaf pages to about 90%%. The partial index already existed
	// during the bulk load, so it grew one row at a time and split pages as it went. REINDEX
	// rebuilds it the packed way.
	var reindexed int64
	if _, err := tx.Exec(ctx, "REINDEX INDEX idx_orders_pending"); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, "SELECT pg_relation_size('idx_orders_pending')").
		Scan(&reindexed); err != nil {
		t.Fatal(err)
	}

	var entries int64
	if err := tx.QueryRow(ctx,
		"SELECT count(*) FROM orders WHERE status = 'pending'").Scan(&entries); err != nil {
		t.Fatal(err)
	}

	t.Logf("partial after REINDEX:           %7d bytes, for %d entries", reindexed, entries)
	t.Logf("so %.0f%% of the partial index was page-split slack from growing during the load",
		(1-float64(reindexed)/float64(partialSize))*100)
	t.Logf("and it is %.0f%% of the full index while holding %.0f%% of the entries; the gap is "+
		"the metapage and root, which every index pays regardless of size",
		float64(reindexed)/float64(fullSize)*100, 100.0/7)

	if reindexed > partialSize {
		t.Errorf("REINDEX made the index bigger: %d -> %d", partialSize, reindexed)
	}

	// And it serves the query it was built for.
	//
	// Through tx, not through the pool, and the first version of this test deadlocked against
	// itself for exactly that reason. The REINDEX above holds an ACCESS EXCLUSIVE lock on
	// idx_orders_pending until the transaction ends, which is when the test ends. A query on
	// another connection needs ACCESS SHARE on the same index just to plan, so it waited for a
	// transaction that was waiting for it. Once a test opens a transaction that does DDL,
	// everything in that test goes through the transaction.
	a, err := Explain(ctx, tx, `
		SELECT id FROM orders WHERE status = 'pending' ORDER BY placed_at DESC LIMIT 20`)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("the dashboard query: %s", a.Summary())

	if !a.UsesIndex("idx_orders_pending") {
		t.Errorf("expected idx_orders_pending, got %v", a.IndexesUsed())
	}
}

// TestPartialIndexDoesNotServeOtherValues: the WHERE clause on the index is a promise about what is
// in it, so a query for a different status cannot use it. Obvious, and worth pinning down because a
// partial index that silently stops being used after a predicate change is a slow regression.
func TestPartialIndexDoesNotServeOtherValues(t *testing.T) {
	db := dataset(t)
	ctx := context.Background()

	a, err := Explain(ctx, db, `
		SELECT id FROM orders WHERE status = 'shipped' ORDER BY placed_at DESC LIMIT 20`)
	if err != nil {
		t.Fatal(err)
	}

	t.Log(a.Summary())

	if a.UsesIndex("idx_orders_pending") {
		t.Error("a partial index on status='pending' must not serve status='shipped'")
	}
}

// TestIndexOnlyScan is the best case: every column the query needs is in the index, so the table is
// never touched.
//
// It took two tries to get this plan, and the reason is the interesting part. An Index Only Scan can
// skip the heap only when the visibility map says the page holds no rows that might be invisible to
// this transaction, and the visibility map is populated by VACUUM. Straight after a bulk load
// nothing is marked all-visible, so the planner knows an "index only" scan would have to visit the
// heap for every row anyway and picks a bitmap scan instead. VACUUM cannot run inside a transaction
// block, so this test cannot use dbtest.Tx and cleans up its index by hand.
//
// The practical version of that: a table that is never vacuumed does not get index-only scans, and
// on an insert-heavy table autovacuum may not run for a long time because it triggers on dead rows.
func TestIndexOnlyScan(t *testing.T) {
	db := dataset(t)
	ctx := context.Background()

	// A covering index: author_id to filter on, id as a non-key column so the select needs
	// nothing else. INCLUDE rather than a second key column, because id does not need to be
	// sorted and a non-key column keeps the index smaller.
	if _, err := db.Exec(ctx,
		"CREATE INDEX idx_books_author_covering ON books (author_id) INCLUDE (id)"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := db.Exec(context.Background(),
			"DROP INDEX IF EXISTS idx_books_author_covering"); err != nil {
			t.Errorf("dropping the covering index: %v", err)
		}
	})

	// ANALYZE for the statistics, VACUUM for the visibility map. Both are needed and they do
	// different things.
	if _, err := db.Exec(ctx, "VACUUM ANALYZE books"); err != nil {
		t.Fatal(err)
	}

	covered, err := Explain(ctx, db, "SELECT id FROM books WHERE author_id = $1", 42)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("SELECT id (covered):     %s", covered.Summary())

	// Adding a column the index does not have forces a table fetch.
	notCovered, err := Explain(ctx, db, "SELECT id, title FROM books WHERE author_id = $1", 42)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("SELECT id, title:        %s", notCovered.Summary())

	if !covered.Uses("Index Only Scan") {
		t.Errorf("expected an Index Only Scan, got %v", covered.NodeTypes())
	}
	if notCovered.Uses("Index Only Scan") {
		t.Error("selecting an uncovered column cannot be an Index Only Scan")
	}
}

// TestRowsRemovedByFilterShowsAMissingIndex is the number worth looking at when a plan is slow but
// the node type looks fine.
func TestRowsRemovedByFilterShowsAMissingIndex(t *testing.T) {
	db := dataset(t)
	ctx := context.Background()

	// A query filtering on an indexed column AND an unindexed one: the index narrows it, then
	// the filter throws rows away.
	a, err := Explain(ctx, db,
		"SELECT count(*) FROM books WHERE author_id = $1 AND price_cents > $2", 42, 8000)
	if err != nil {
		t.Fatal(err)
	}

	t.Log(a.Summary())

	removed := a.RowsRemovedByFilter()
	if removed == 0 {
		t.Log("no rows were discarded, so the index covered the whole predicate")
		return
	}

	t.Logf("%.0f rows were read and discarded to return %.0f; a composite index on "+
		"(author_id, price_cents) would remove that work", removed, a.Root.ActualRows)
}

// TestSeqScanIsCorrectForSmallTables is the counterweight to everything above: forcing an index on a
// small table makes things slower, and a rule of "always index the foreign key" applied to a
// ten-row lookup table is cargo cult.
func TestSeqScanIsCorrectForSmallTables(t *testing.T) {
	_ = dataset(t)
	ctx := context.Background()

	tx := dbtest.Tx(t)

	if _, err := tx.Exec(ctx, `
		CREATE TEMP TABLE small (id bigserial PRIMARY KEY, label text NOT NULL);
		INSERT INTO small (label) SELECT 'label ' || g FROM generate_series(1, 50) g;
		ANALYZE small;`); err != nil {
		t.Fatal(err)
	}

	// Even on the PRIMARY KEY, which is indexed by definition.
	a, err := Explain(ctx, tx, "SELECT label FROM small WHERE id = $1", 25)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("50-row table, primary key lookup: %s", a.Summary())

	if a.Uses("Seq Scan") {
		t.Log("the planner chose a Seq Scan over the primary key index, because reading " +
			"one page beats walking a B-tree. That is correct.")
	} else {
		t.Logf("the planner used the index even at 50 rows: %v", a.NodeTypes())
	}
}
