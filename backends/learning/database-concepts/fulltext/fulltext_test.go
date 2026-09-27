package fulltext

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/alexvervloet/learn-go/backends/learning/database-concepts/dbtest"
	"github.com/alexvervloet/learn-go/backends/learning/database-concepts/indexes"
	"github.com/alexvervloet/learn-go/backends/learning/database-concepts/seed"
)

var seedOnce sync.Once

func dataset(t testing.TB) *pgxpool.Pool {
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

// TestStemmingIsWhatMakesItSearch shows the transformation, because every later behaviour follows from
// it.
func TestStemmingIsWhatMakesItSearch(t *testing.T) {
	db := dataset(t)
	ctx := context.Background()

	for _, tc := range []struct {
		config, text, want string
	}{
		// 'the' is a stop word and is dropped entirely, positions and all. 'running'
		// stems to 'run'.
		{"english", "The Running Dogs", "'dog':3 'run':2"},

		// Same words, no dictionary. 'simple' lower-cases and does nothing else, so
		// 'the' is kept and 'running' stays 'running'.
		{"simple", "The Running Dogs", "'dogs':3 'running':2 'the':1"},

		// Which is why a search for "run" finds nothing in a 'simple' column and
		// everything in an 'english' one.
		{"english", "runs ran running runner", "'ran':2 'run':1,3 'runner':4"},
	} {
		got, err := Lexemes(ctx, db, tc.config, tc.text)
		if err != nil {
			t.Fatal(err)
		}

		t.Logf("%-8s %-24q -> %s", tc.config, tc.text, got)

		if got != tc.want {
			t.Errorf("to_tsvector(%s, %q):\n  got  %s\n  want %s",
				tc.config, tc.text, got, tc.want)
		}
	}
}

// TestParsersDifferOnTheSameInput is the table that decides which parser a search box uses.
func TestParsersDifferOnTheSameInput(t *testing.T) {
	db := dataset(t)
	ctx := context.Background()

	const query = `quick river OR "golden garden" -shadow`

	for _, parser := range []Parser{Plain, Phrase, WebSearch} {
		got, err := ParsedQuery(ctx, db, parser, query)
		if err != nil {
			t.Errorf("%s: %v", parser, err)
			continue
		}

		t.Logf("%-22s %s", parser, got)
	}

	// websearch_to_tsquery is the only one that understands the quotes, the OR and the minus.
	web, err := ParsedQuery(ctx, db, WebSearch, query)
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"|", "<->", "!"} {
		if !strings.Contains(web, want) {
			t.Errorf("websearch_to_tsquery dropped %q: %s", want, web)
		}
	}

	// And the reason not to use to_tsquery on user input.
	_, err = ParsedQuery(ctx, db, Raw, "c++ &")
	if err == nil {
		t.Error("expected to_tsquery to reject malformed input")
	} else {
		t.Logf("to_tsquery on user input: %v", err)
	}

	// websearch_to_tsquery takes the same string without complaint.
	safe, err := ParsedQuery(ctx, db, WebSearch, "c++ &")
	if err != nil {
		t.Errorf("websearch_to_tsquery should never raise, got %v", err)
	} else {
		t.Logf("websearch_to_tsquery on the same input: %s", safe)
	}
}

// TestSearchUsesTheGINIndex started as "assert the GIN index is used" and became something more
// useful, because at 10,000 rows Postgres never uses it.
//
// Two versions of the test failed before this one. Asserting the plan for the query Search actually
// runs (with LIMIT 20) failed because a scan finds 20 of 137 matches after reading 1,320 rows and
// stops. Asserting it for count(*), where every match has to be found, failed too: the whole books
// table is about 1 MB, so a sequential scan reads it in a few hundred page fetches and a GIN walk plus
// scattered heap fetches cannot beat that.
//
// The planner is right both times, and the honest lesson is that a GIN index needs a table big enough
// to matter. enable_seqscan = off below forces the index path so the two can be priced against each
// other, which is what that setting is for: it is a diagnostic, never a production setting.
func TestSearchUsesTheGINIndex(t *testing.T) {
	db := dataset(t)
	ctx := context.Background()

	var (
		matches, total int
		tableSize      string
		indexSize      string
	)
	if err := db.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM books
		         WHERE search @@ websearch_to_tsquery('english', 'quick river')),
		       (SELECT count(*) FROM books),
		       pg_size_pretty(pg_relation_size('books')),
		       pg_size_pretty(pg_relation_size('idx_books_search'))`).
		Scan(&matches, &total, &tableSize, &indexSize); err != nil {
		t.Fatal(err)
	}

	t.Logf("%d of %d titles match (%.1f%%); books is %s and idx_books_search is %s",
		matches, total, float64(matches)/float64(total)*100, tableSize, indexSize)

	seqScan, err := indexes.Explain(ctx, db, `
		SELECT count(*) FROM books
		 WHERE search @@ websearch_to_tsquery('english', 'quick river')`)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("as the planner chooses:      %s", seqScan.Summary())

	if !seqScan.Uses("Seq Scan") {
		t.Logf("the planner used an index at this size: %v", seqScan.NodeTypes())
	}

	// Force the index path to price it. SET LOCAL so it reverts with the transaction.
	tx := dbtest.Tx(t)

	if _, err := tx.Exec(ctx, "SET LOCAL enable_seqscan = off"); err != nil {
		t.Fatal(err)
	}

	forced, err := indexes.Explain(ctx, tx, `
		SELECT count(*) FROM books
		 WHERE search @@ websearch_to_tsquery('english', 'quick river')`)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("with enable_seqscan = off:   %s", forced.Summary())

	// This is the assertion worth making: the GIN index CAN serve the predicate. Whether the
	// planner picks it is a cost decision that depends on the table size.
	if !forced.UsesIndex("idx_books_search") {
		t.Errorf("expected the GIN index when a scan is forbidden, got %v", forced.IndexesUsed())
	}

	t.Logf("the planner preferred the scan, and its estimated cost says why: %.0f for the "+
		"scan against %.0f for the index", seqScan.Root.TotalCost, forced.Root.TotalCost)

	if forced.Root.TotalCost < seqScan.Root.TotalCost {
		t.Errorf("the planner chose the more expensive plan: %.0f over %.0f",
			seqScan.Root.TotalCost, forced.Root.TotalCost)
	}

	// And the part the estimate gets wrong. The costs say the index is about 1.6x more
	// expensive; the measured times are 0.79ms and 0.81ms, which is a tie. Cost units are not
	// milliseconds and are not calibrated to any machine: they are a model whose constants
	// (seq_page_cost, random_page_cost, cpu_tuple_cost) default to values chosen for spinning
	// disks. random_page_cost defaults to 4.0, and on an SSD 1.1 is closer to the truth, which is
	// the single most common planner tuning change there is.
	t.Logf("the estimate says %.1fx, the clock says %.2fx; cost units are a model, not "+
		"milliseconds",
		forced.Root.TotalCost/seqScan.Root.TotalCost,
		forced.ExecutionTime/seqScan.ExecutionTime)

	// And the version that cannot use the index whatever the plan: to_tsvector computed in the
	// query rather than read from the generated column. The index is on the column, and this
	// expression is not the column, so it is the same trap as applying a function to an indexed
	// column. This is the measurement that matters most in the package.
	expression, err := indexes.Explain(ctx, db, `
		SELECT count(*) FROM books
		 WHERE to_tsvector('english', title) @@ websearch_to_tsquery('english', 'quick river')`)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("to_tsvector recomputed:      %s", expression.Summary())

	if expression.UsesIndex("idx_books_search") {
		t.Error("the planner matched an expression against a column index, which it cannot do")
	}
	if !expression.Uses("Seq Scan") {
		t.Errorf("expected a Seq Scan, got %v", expression.NodeTypes())
	}

	if expression.ExecutionTime <= seqScan.ExecutionTime {
		t.Errorf("recomputing the vector for every row (%.2fms) should cost more than "+
			"reading the stored one (%.2fms)", expression.ExecutionTime, seqScan.ExecutionTime)
	}

	t.Logf("reading the generated column is %.0fx faster than recomputing it (%.2fms vs %.2fms), "+
		"and that gap does not depend on any index",
		expression.ExecutionTime/seqScan.ExecutionTime,
		seqScan.ExecutionTime, expression.ExecutionTime)
}

// TestSearchFindsAndRanks, against the seeded titles, which are "The <adjective> <noun> <n>".
func TestSearchFindsAndRanks(t *testing.T) {
	db := dataset(t)
	ctx := context.Background()

	hits, err := Search(ctx, db, WebSearch, "quick river", 10)
	if err != nil {
		t.Fatal(err)
	}

	if len(hits) == 0 {
		t.Fatal("no hits for 'quick river'; the dataset may not be loaded")
	}

	t.Logf("%d hits for 'quick river'", len(hits))
	for i, h := range hits {
		if i < 5 {
			t.Logf("  %.6f  %s", h.Rank, h.Headline)
		}
	}

	// Both words, because websearch ANDs bare terms.
	for _, h := range hits {
		lower := strings.ToLower(h.Title)
		if !strings.Contains(lower, "quick") || !strings.Contains(lower, "river") {
			t.Errorf("%q matched a two-word query without both words", h.Title)
		}
	}

	// Ranks come back in descending order.
	for i := 1; i < len(hits); i++ {
		if hits[i-1].Rank < hits[i].Rank {
			t.Errorf("rank %f then %f is not descending", hits[i-1].Rank, hits[i].Rank)
		}
	}

	// The headline marks the matched words.
	if !strings.Contains(hits[0].Headline, "<b>") {
		t.Errorf("ts_headline produced no markup: %q", hits[0].Headline)
	}
}

// TestStopWordsAreNotStored, which is the cause of "my search for 'the' returns nothing".
func TestStopWordsAreNotStored(t *testing.T) {
	db := dataset(t)
	ctx := context.Background()

	// Every seeded title starts with "The".
	var titles int
	if err := db.QueryRow(ctx, "SELECT count(*) FROM books WHERE title LIKE 'The %'").
		Scan(&titles); err != nil {
		t.Fatal(err)
	}

	hits, err := Search(ctx, db, WebSearch, "the", 10)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("%d titles begin with 'The', and a full text search for 'the' returns %d",
		titles, len(hits))

	if titles == 0 {
		t.Fatal("no titles start with 'The'; the seed data changed")
	}
	if len(hits) != 0 {
		t.Errorf("expected no hits for a stop word, got %d", len(hits))
	}

	// The lexemes say why.
	lex, err := Lexemes(ctx, db, "english", "The Quick River 1")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("'The Quick River 1' is stored as %s, with no 'the' in it", lex)
}

// TestTrigramHandlesTypos is what a tsvector cannot do at all.
func TestTrigramHandlesTypos(t *testing.T) {
	// The dataset has to exist; the queries go through the transaction that owns the index.
	_ = dataset(t)
	ctx := context.Background()

	tx := dbtest.Tx(t)

	// pg_trgm and its index, inside the transaction. The extension is created by the migration;
	// the index is not, because it is large and only this test needs it.
	if _, err := tx.Exec(ctx, "CREATE EXTENSION IF NOT EXISTS pg_trgm"); err != nil {
		t.Skipf("pg_trgm is not available: %v", err)
	}
	if _, err := tx.Exec(ctx,
		"CREATE INDEX idx_books_title_trgm ON books USING gin (title gin_trgm_ops)"); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, "ANALYZE books"); err != nil {
		t.Fatal(err)
	}

	// A misspelling. The tsquery version finds nothing.
	typo := "Quik"

	viaSearch, err := Search(ctx, tx, WebSearch, typo, 5)
	if err != nil {
		t.Fatal(err)
	}

	viaTrigram, err := SimilarTitles(ctx, tx, typo, 5)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("full text search for %q: %d hits", typo, len(viaSearch))
	t.Logf("trigram similarity for %q: %d hits", typo, len(viaTrigram))

	for i, h := range viaTrigram {
		if i < 3 {
			t.Logf("  %.3f  %s", h.Rank, h.Title)
		}
	}

	if len(viaSearch) != 0 {
		t.Errorf("a tsquery matched a misspelling, which it should not: %v", viaSearch)
	}
	if len(viaTrigram) == 0 {
		t.Error("trigram similarity found nothing for a one-letter typo")
	}

	// And the leading-wildcard case from the indexes package, which a trigram index CAN serve.
	a, err := indexes.Explain(ctx, tx,
		"SELECT count(*) FROM books WHERE title LIKE $1", "%Quick%")
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("LIKE '%%Quick%%' with a trigram index: %s", a.Summary())

	if !a.UsesIndex("idx_books_title_trgm") {
		t.Errorf("expected the trigram index to serve a leading wildcard, got %v",
			a.IndexesUsed())
	}
}

// TestWeightedRankingIsTheWholeTuningModel, and the {D,C,B,A} order is the trap.
func TestWeightedRankingIsTheWholeTuningModel(t *testing.T) {
	db := dataset(t)
	ctx := context.Background()

	hits, err := Weighted(ctx, db, "quick", 10)
	if err != nil {
		t.Fatal(err)
	}

	if len(hits) == 0 {
		t.Fatal("no weighted hits for 'quick'")
	}

	t.Logf("%d hits, title weighted A and author weighted B", len(hits))
	for i, h := range hits {
		if i < 5 {
			t.Logf("  %.6f  %s", h.Rank, h.Title)
		}
	}

	for i := 1; i < len(hits); i++ {
		if hits[i-1].Rank < hits[i].Rank {
			t.Errorf("rank %f then %f is not descending", hits[i-1].Rank, hits[i].Rank)
		}
	}
}

// TestGeneratedColumnCannotGoStale is the argument for the generated column over a trigger, pinned down.
func TestGeneratedColumnCannotGoStale(t *testing.T) {
	_ = dataset(t)
	ctx := context.Background()

	tx := dbtest.Tx(t)

	var id int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO books (author_id, title, isbn, published, price_cents)
		VALUES (1, 'The Silent Compass 99999', '9990000000001', '2025-01-01', 1000)
		RETURNING id`).Scan(&id); err != nil {
		t.Fatal(err)
	}

	var found bool
	if err := tx.QueryRow(ctx, `
		SELECT search @@ websearch_to_tsquery('english', 'silent compass')
		  FROM books WHERE id = $1`, id).Scan(&found); err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Error("the generated column was not populated on INSERT")
	}

	// Now change the title and check the vector followed, with no trigger and no application
	// code involved.
	if _, err := tx.Exec(ctx,
		"UPDATE books SET title = 'The Crimson Harvest 99999' WHERE id = $1", id); err != nil {
		t.Fatal(err)
	}

	var stillMatchesOld, matchesNew bool
	if err := tx.QueryRow(ctx, `
		SELECT search @@ websearch_to_tsquery('english', 'silent compass'),
		       search @@ websearch_to_tsquery('english', 'crimson harvest')
		  FROM books WHERE id = $1`, id).Scan(&stillMatchesOld, &matchesNew); err != nil {
		t.Fatal(err)
	}

	if stillMatchesOld {
		t.Error("the tsvector still matches the old title")
	}
	if !matchesNew {
		t.Error("the tsvector does not match the new title")
	}

	// And it cannot be written directly, which is what makes it impossible to get out of sync.
	_, err := tx.Exec(ctx, "UPDATE books SET search = to_tsvector('english', 'anything') WHERE id = $1", id)
	if err == nil {
		t.Error("a generated column should reject a direct write")
	} else {
		t.Logf("writing to the generated column directly: %v", err)
	}
}
