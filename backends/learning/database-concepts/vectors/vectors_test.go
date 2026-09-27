package vectors

import (
	"context"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/alexvervloet/learn-go/backends/learning/database-concepts/dbtest"
	"github.com/alexvervloet/learn-go/backends/learning/database-concepts/indexes"
	"github.com/alexvervloet/learn-go/backends/learning/database-concepts/seed"
)

var seedOnce sync.Once

// dataset loads the books and their embeddings once for the package.
func dataset(t testing.TB) *pgxpool.Pool {
	t.Helper()

	p := dbtest.Pool(t)

	seedOnce.Do(func() {
		ctx := context.Background()

		if _, err := p.Exec(ctx,
			"TRUNCATE authors, books, customers, orders, order_items, book_embeddings RESTART IDENTITY CASCADE"); err != nil {
			t.Fatalf("truncating: %v", err)
		}
		if err := seed.Load(ctx, p, seed.Default()); err != nil {
			t.Fatalf("seeding: %v", err)
		}

		start := time.Now()

		n, err := Load(ctx, p, 500)
		if err != nil {
			t.Fatalf("loading embeddings: %v", err)
		}

		t.Logf("embedded %d books in %v", n, time.Since(start).Round(time.Millisecond))
	})

	return p
}

// TestEmbedIsDeterministicAndNormalised, because every later test assumes both.
func TestEmbedIsDeterministicAndNormalised(t *testing.T) {
	for _, text := range []string{
		"The Quick River 1",
		"the quick river 1",
		"",
		"a",
	} {
		a, b := Embed(text), Embed(text)

		if len(a.Slice()) != Dimensions {
			t.Errorf("%q: %d dimensions, want %d", text, len(a.Slice()), Dimensions)
		}

		for i := range a.Slice() {
			if a.Slice()[i] != b.Slice()[i] {
				t.Fatalf("%q: two calls differ at dimension %d", text, i)
			}
		}

		norm := Norm(a)
		if math.Abs(norm-1) > 1e-6 {
			t.Errorf("%q: norm is %f, want 1", text, norm)
		}
	}

	// Case-insensitive, because Fields plus ToLower is the whole tokeniser.
	upper, lower := Embed("The Quick River"), Embed("the QUICK river")
	for i := range upper.Slice() {
		if upper.Slice()[i] != lower.Slice()[i] {
			t.Fatalf("case changed the vector at dimension %d", i)

		}
	}

	// Empty text gets a real unit vector rather than NaN, which is the case that would poison
	// every distance silently.
	empty := Embed("")
	if math.IsNaN(float64(empty.Slice()[0])) {
		t.Error("the empty string produced NaN")
	}
	if n := Norm(empty); math.Abs(n-1) > 1e-6 {
		t.Errorf("the empty string's norm is %f, want 1", n)
	}
}

// TestNormalisedVectorsMakeL2AndCosineAgree measures the claim in the package doc.
func TestNormalisedVectorsMakeL2AndCosineAgree(t *testing.T) {
	db := dataset(t)
	ctx := context.Background()

	query := Embed("The Quick River")

	const k = 20

	byL2, err := Search(ctx, db, L2, query, k)
	if err != nil {
		t.Fatal(err)
	}

	byCosine, err := Search(ctx, db, Cosine, query, k)
	if err != nil {
		t.Fatal(err)
	}

	byInner, err := Search(ctx, db, InnerProduct, query, k)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("top 3 by L2:     %v", IDs(byL2)[:3])
	t.Logf("top 3 by cosine: %v", IDs(byCosine)[:3])
	t.Logf("top 3 by inner:  %v", IDs(byInner)[:3])

	// The arithmetic: for unit vectors, L2 distance = sqrt(2 * cosine distance).
	for i := range byL2 {
		predicted := math.Sqrt(2 * byCosine[i].Distance)

		if math.Abs(predicted-byL2[i].Distance) > 1e-3 {
			t.Errorf("row %d: cosine %f predicts an L2 of %f, got %f",
				i, byCosine[i].Distance, predicted, byL2[i].Distance)
		}
	}

	t.Logf("first result: L2 %.6f, cosine %.6f, sqrt(2*cosine) = %.6f",
		byL2[0].Distance, byCosine[0].Distance, math.Sqrt(2*byCosine[0].Distance))

	// The rankings agree on DISTANCE, which is the claim. They do not always agree on which row,
	// and finding that out was the useful part of writing this test.
	//
	// L2 put book 8272 first and cosine put 950 first, with identical distances at every position.
	// The seeded titles come from 8 adjectives and 8 nouns, so thousands of books hash to the same
	// vector shape and hundreds are exactly equidistant from any query. Which of them a query
	// returns is whatever order the scan produced.
	//
	// This is the third time in this module that a missing tiebreaker produced a surprise: keyset
	// pagination needs one, a ROWS window frame needs one, and so does vector search. The
	// difference here is that vector search CANNOT have one. Adding `, book_id` to the ORDER BY
	// makes the expression stop matching the index, so the query becomes a full scan. Ties in
	// vector search are either accepted or removed from the data.
	for i := range byL2 {
		predicted := math.Sqrt(2 * byCosine[i].Distance)

		if math.Abs(predicted-byL2[i].Distance) > 1e-3 {
			t.Errorf("position %d: the distances diverge, %f against %f",
				i, byL2[i].Distance, predicted)
		}
	}

	t.Logf("the top %d contains %d distinct distances, so %d of the positions are ties",
		k, DistinctDistances(byCosine), k-DistinctDistances(byCosine))

	if DistinctDistances(byCosine) == k {
		t.Log("no ties in this result set, so L2 and cosine should agree on the rows too")

		for i := range byL2 {
			if byL2[i].BookID != byCosine[i].BookID {
				t.Errorf("no ties, yet L2 and cosine disagree at position %d: %d vs %d",
					i, byL2[i].BookID, byCosine[i].BookID)
				break
			}
		}
	}

	// And the inner product's sign, which is the detail that catches people.
	if byInner[0].Distance > 0 {
		t.Errorf("the best inner-product match has distance %f; pgvector negates it, so "+
			"the best match should be the most negative", byInner[0].Distance)
	}

	t.Logf("the best <#> match is %.6f and the worst of the %d is %.6f: more negative is better",
		byInner[0].Distance, k, byInner[k-1].Distance)
}

// TestExactSearchScansEverything is the baseline, and the reason an index is wanted at all.
func TestExactSearchScansEverything(t *testing.T) {
	db := dataset(t)
	ctx := context.Background()

	query := Embed("The Quick River")

	a, err := indexes.Explain(ctx, db, `
		SELECT book_id FROM book_embeddings ORDER BY embedding <=> $1::vector LIMIT 10`, query.String())
	if err != nil {
		t.Fatal(err)
	}

	t.Log(a.Summary())

	if !a.Uses("Seq Scan") {
		t.Errorf("with no vector index this must be a Seq Scan, got %v", a.NodeTypes())
	}

	var rows int
	if err := db.QueryRow(ctx, "SELECT count(*) FROM book_embeddings").Scan(&rows); err != nil {
		t.Fatal(err)
	}

	t.Logf("exact search computed %d distances to return 10, in %.2fms",
		rows, a.ExecutionTime)
}

// TestHNSWTradesRecallForSpeed is the measurement this package exists for, and it found two things I
// did not know.
//
// First: hnsw.ef_search below the LIMIT makes the query return FEWER ROWS THAN ASKED FOR. A LIMIT 50
// with ef_search = 10 returns 10 rows. Not 50 worse rows, 10 rows. No error, no warning. ef_search is
// the size of the candidate list the graph walk keeps, and the index cannot return more rows than it
// kept, so the LIMIT is silently capped. pgvector's default is 40, which means any query asking for more
// than 40 results is quietly truncated until someone raises it.
//
// Second: at ef_search = 400 the planner stops using the index at all and does a sequential scan, which
// is why the recall column reads 100% there. pgvector's cost estimate rises with ef_search, and past a
// point the scan wins. So "turn ef_search up until recall is good enough" can silently turn the index
// off, and the plan is the only place that says so.
func TestHNSWTradesRecallForSpeed(t *testing.T) {
	db := dataset(t)
	ctx := context.Background()

	query := Embed("The Quick River")

	const k = 50

	exact, err := Search(ctx, db, Cosine, query, k)
	if err != nil {
		t.Fatal(err)
	}

	exactPlan, err := indexes.Explain(ctx, db,
		"SELECT book_id FROM book_embeddings ORDER BY embedding <=> $1::vector LIMIT $2",
		query.String(), k)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("the exact top %d holds %d distinct distances, so recall by ID alone is not "+
		"meaningful here; see Recall in vectors.go", k, DistinctDistances(exact))

	// The index, inside a transaction so the table goes back to exact afterwards.
	tx := dbtest.Tx(t)

	start := time.Now()

	// m is how many links each node keeps, ef_construction how wide the search is while building.
	// Both defaults (16 and 64) are conservative; raising them costs build time and memory and
	// buys recall.
	if _, err := tx.Exec(ctx, `
		CREATE INDEX idx_embeddings_hnsw ON book_embeddings
		 USING hnsw (embedding vector_cosine_ops) WITH (m = 16, ef_construction = 64)`); err != nil {
		t.Fatal(err)
	}

	buildTime := time.Since(start)

	var indexSize string
	if err := tx.QueryRow(ctx,
		"SELECT pg_size_pretty(pg_relation_size('idx_embeddings_hnsw'))").Scan(&indexSize); err != nil {
		t.Fatal(err)
	}

	t.Logf("HNSW built in %v, %s, for %.1f MB of vectors",
		buildTime.Round(time.Millisecond), indexSize, 10000*float64(Dimensions*4+8)/(1<<20))

	t.Log("ef_search  rows  recall  strict  exec ms  index")

	for _, ef := range []int{10, 40, 100, 400} {
		if _, err := tx.Exec(ctx, "SET LOCAL hnsw.ef_search = "+itoa(ef)); err != nil {
			t.Fatal(err)
		}

		approx, err := Search(ctx, tx, Cosine, query, k)
		if err != nil {
			t.Fatal(err)
		}

		plan, err := indexes.Explain(ctx, tx,
			"SELECT book_id FROM book_embeddings ORDER BY embedding <=> $1::vector LIMIT $2",
			query.String(), k)
		if err != nil {
			t.Fatal(err)
		}

		usedIndex := plan.UsesIndex("idx_embeddings_hnsw")

		t.Logf("%9d %5d  %5.1f%%  %5.1f%%  %7.2f  %v",
			ef, len(approx), Recall(exact, approx)*100,
			RecallStrict(exact, approx)*100, plan.ExecutionTime, usedIndex)

		// The finding, asserted: below the LIMIT, ef_search caps the row count.
		if ef < k && usedIndex {
			if len(approx) > ef {
				t.Errorf("ef_search=%d returned %d rows, which is more than the "+
					"candidate list; pgvector changed its behaviour", ef, len(approx))
			}
			if len(approx) == k {
				t.Errorf("ef_search=%d returned the full %d rows, so the cap no "+
					"longer applies", ef, k)
			}
		}

		// At or above the LIMIT it returns what was asked for.
		if ef >= k && len(approx) != k {
			t.Errorf("ef_search=%d returned %d rows, want %d", ef, len(approx), k)
		}

		// Every row returned is a real row, whatever the recall.
		for _, n := range approx {
			if n.BookID == 0 || n.Title == "" {
				t.Errorf("ef_search=%d returned an empty row: %+v", ef, n)
			}
		}
	}

	t.Logf("exact search was %.2fms over every row", exactPlan.ExecutionTime)
	t.Log("at 10,000 rows the index is 20 to 80 times faster per query and the table is only " +
		"31 MB, so the scan is already quick in absolute terms. The index costs 19 MB and " +
		"456ms to build, which at this size is not obviously worth it.")
}

// TestIVFFlatOnAnEmptyTableIsUseless is the failure mode that produces no error at all.
//
// IVFFlat clusters the rows that exist when the index is built. A migration that runs CREATE TABLE then
// CREATE INDEX, with the application inserting afterwards, builds the clusters from nothing: one list
// holding everything, forever, until someone reindexes.
//
// The first version of this test tried to compare the two indexes side by side using a savepoint, which
// does not work and is worth writing down. A savepoint does not isolate the parent from the child: `tx`
// sees everything `sp` does until `sp` rolls back. So both halves of the comparison queried the same
// index and reported the same number. The two states have to be measured SEQUENTIALLY on one connection.
func TestIVFFlatOnAnEmptyTableIsUseless(t *testing.T) {
	db := dataset(t)
	ctx := context.Background()

	query := Embed("The Quick River")

	const k = 50

	exact, err := Search(ctx, db, Cosine, query, k)
	if err != nil {
		t.Fatal(err)
	}

	tx := dbtest.Tx(t)

	// measure builds an index the given way and reports what it costs.
	measure := func(name string, build func() error) (recall float64, execMS float64, size int64) {
		t.Helper()

		if err := build(); err != nil {
			t.Fatalf("%s: %v", name, err)
		}

		if _, err := tx.Exec(ctx, "SET LOCAL ivfflat.probes = 10"); err != nil {
			t.Fatal(err)
		}

		got, err := Search(ctx, tx, Cosine, query, k)
		if err != nil {
			t.Fatal(err)
		}

		plan, err := indexes.Explain(ctx, tx,
			"SELECT book_id FROM book_embeddings ORDER BY embedding <=> $1::vector LIMIT $2",
			query.String(), k)
		if err != nil {
			t.Fatal(err)
		}

		if !plan.UsesIndex("idx_embeddings_ivf") {
			t.Errorf("%s: the query did not use the index: %v", name, plan.NodeTypes())
		}

		if err := tx.QueryRow(ctx,
			"SELECT pg_relation_size('idx_embeddings_ivf')").Scan(&size); err != nil {
			t.Fatal(err)
		}

		t.Logf("%-22s recall %.1f%%  %.2fms  %d bytes  %s",
			name, Recall(exact, got)*100, plan.ExecutionTime, size, plan.Summary())

		return Recall(exact, got), plan.ExecutionTime, size
	}

	// The correct order: data first, then the index.
	goodRecall, goodMS, goodSize := measure("built on 10,000 rows", func() error {
		_, err := tx.Exec(ctx, `
			CREATE INDEX idx_embeddings_ivf ON book_embeddings
			 USING ivfflat (embedding vector_cosine_ops) WITH (lists = 100)`)
		return err
	})

	// The wrong order, which is what a migration does by default.
	badRecall, badMS, badSize := measure("built on an empty table", func() error {
		if _, err := tx.Exec(ctx, "DROP INDEX idx_embeddings_ivf"); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			"CREATE TEMP TABLE saved AS SELECT * FROM book_embeddings"); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "DELETE FROM book_embeddings"); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			CREATE INDEX idx_embeddings_ivf ON book_embeddings
			 USING ivfflat (embedding vector_cosine_ops) WITH (lists = 100)`); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, "INSERT INTO book_embeddings SELECT * FROM saved")
		return err
	})

	t.Logf("recall %.1f%% against %.1f%%, %.2fms against %.2fms, %d bytes against %d",
		goodRecall*100, badRecall*100, goodMS, badMS, goodSize, badSize)

	// Recall does not catch it, and that is the finding rather than a gap in the test. With 10,000
	// near-duplicate vectors, one cluster returns rows just as close as a hundred clusters would.
	if badRecall < goodRecall {
		t.Logf("recall did drop, by %.1f points", (goodRecall-badRecall)*100)
	} else {
		t.Log("recall did not drop, so recall is not the signal for this bug")
	}

	// The size and the time are, and both in the opposite direction from what I guessed. I expected
	// the one-cluster index to be SMALLER, having no centres to store. It is twice as big, 33.3 MB
	// against 16.9, and twice as slow, 0.33ms against 0.17.
	//
	// Both come from the same thing. With a hundred lists, CREATE INDEX sorts the vectors into
	// clusters and packs the pages. With one list, the index was built empty and then grown one
	// INSERT at a time, so it carries the same page-split slack the partial index in the indexes
	// package did, and a probe has to walk all of it because there is nothing to skip.
	if badSize <= goodSize {
		t.Errorf("expected the empty-table index to be larger, got %d against %d",
			badSize, goodSize)
	} else {
		t.Logf("the empty-table index is %.1fx the size, because it was grown by INSERT "+
			"rather than built, and has one list to skip nothing with",
			float64(badSize)/float64(goodSize))
	}

	if badMS <= goodMS {
		t.Logf("the empty-table index was not slower on this run (%.2fms against %.2fms); "+
			"at 10,000 rows one list still fits in cache", badMS, goodMS)
	} else {
		t.Logf("and %.1fx slower per query, because probes = 10 against 1 list scans "+
			"everything", badMS/goodMS)
	}

	t.Log("the rule: never CREATE INDEX on a vector column before the data exists, and REINDEX " +
		"after the first bulk load. Same rule as the partial index in the indexes package, " +
		"arrived at for a completely different reason.")
}

// TestTheOrderByMustMatchExactly is the vector version of "a function on the column defeats the index".
func TestTheOrderByMustMatchExactly(t *testing.T) {
	// The dataset has to exist; every query here goes through the transaction with the index.
	_ = dataset(t)
	ctx := context.Background()

	query := Embed("The Quick River")

	tx := dbtest.Tx(t)

	if _, err := tx.Exec(ctx, `
		CREATE INDEX idx_embeddings_hnsw ON book_embeddings
		 USING hnsw (embedding vector_cosine_ops)`); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name      string
		sql       string
		wantIndex bool
	}{
		{
			"column <=> param",
			"SELECT book_id FROM book_embeddings ORDER BY embedding <=> $1::vector LIMIT 10",
			true,
		},
		{
			// A different operator from the one the index was built with. The index is
			// vector_cosine_ops, so it cannot answer an L2 ordering.
			"the wrong operator for the index",
			"SELECT book_id FROM book_embeddings ORDER BY embedding <-> $1::vector LIMIT 10",
			false,
		},
		{
			// Arithmetic around the distance. Ordering by 1 - (a <=> b) is the same
			// ranking reversed, and the index cannot serve it.
			"arithmetic around the distance",
			"SELECT book_id FROM book_embeddings ORDER BY 1 - (embedding <=> $1::vector) DESC LIMIT 10",
			false,
		},
		{
			// No LIMIT. An approximate index exists to return the top k; without a k
			// there is nothing for it to do.
			"no LIMIT",
			"SELECT book_id FROM book_embeddings ORDER BY embedding <=> $1::vector",
			false,
		},
	} {
		a, err := indexes.Explain(ctx, tx, tc.sql, query.String())
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}

		used := a.UsesIndex("idx_embeddings_hnsw")

		t.Logf("%-34s index=%-5v  %s", tc.name, used, a.Summary())

		if used != tc.wantIndex {
			t.Errorf("%s: index used = %v, want %v", tc.name, used, tc.wantIndex)
		}
	}
}

// TestStorageArithmetic, because the size of a vector table surprises people and it is just
// multiplication.
func TestStorageArithmetic(t *testing.T) {
	db := dataset(t)
	ctx := context.Background()

	var (
		rows                 int
		tableSize, booksSize int64
	)
	if err := db.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM book_embeddings),
		       pg_total_relation_size('book_embeddings'),
		       pg_total_relation_size('books')`).Scan(&rows, &tableSize, &booksSize); err != nil {
		t.Fatal(err)
	}

	perRow := float64(tableSize) / float64(rows)

	// A vector(n) is 4 bytes per dimension plus an 8-byte header.
	rawVector := Dimensions*4 + 8

	t.Logf("%d embeddings: %.1f MB total, %.0f bytes per row", rows,
		float64(tableSize)/(1<<20), perRow)
	t.Logf("a vector(%d) is %d bytes of payload, so the rest is the row header, the model "+
		"column, the primary key index and page overhead", Dimensions, rawVector)
	t.Logf("books itself is %.1f MB, so the embeddings are %.1fx the data they describe",
		float64(booksSize)/(1<<20), float64(tableSize)/float64(booksSize))

	if perRow < float64(rawVector) {
		t.Errorf("%.0f bytes per row is less than the %d-byte vector, which cannot be right",
			perRow, rawVector)
	}

	// The arithmetic worth carrying: at 1536 dimensions, which is OpenAI's small model.
	t.Logf("at 1536 dimensions the same %d rows would be about %.0f MB of vectors alone",
		rows, float64(rows)*float64(1536*4+8)/(1<<20))
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}

	neg := n < 0
	if neg {
		n = -n
	}

	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}

	if neg {
		return "-" + string(digits)
	}

	return string(digits)
}
