package nplusone

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/alexvervloet/learn-go/backends/learning/database-concepts/dbtest"
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

const limit = 50

// TestAllStrategiesAgree is the test that has to pass before any of the counting means anything. A
// faster query returning different data is not an optimisation.
func TestAllStrategiesAgree(t *testing.T) {
	db := dataset(t)
	ctx := context.Background()

	want, err := Naive(ctx, db, limit)
	if err != nil {
		t.Fatal(err)
	}

	if len(want) != limit {
		t.Fatalf("expected %d authors, got %d; is the dataset loaded?", limit, len(want))
	}

	var books int
	for _, a := range want {
		books += len(a.Books)
	}
	t.Logf("%d authors, %d books between them", len(want), books)

	for _, tc := range []struct {
		name string
		fn   func(context.Context, Querier, int) ([]Author, error)
	}{
		{"Join", Join},
		{"TwoQueries", TwoQueries},
		{"Batched", Batched},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.fn(ctx, db, limit)
			if err != nil {
				t.Fatal(err)
			}

			if !reflect.DeepEqual(got, want) {
				t.Errorf("%s returned different data from Naive", tc.name)

				if len(got) != len(want) {
					t.Fatalf("  %d authors vs %d", len(got), len(want))
				}
				for i := range got {
					if !reflect.DeepEqual(got[i], want[i]) {
						t.Fatalf("  first difference at author %d (%d): %d books vs %d",
							i, want[i].ID, len(got[i].Books), len(want[i].Books))
					}
				}
			}
		})
	}
}

// TestRoundTripCounts is the assertion that survives being run anywhere.
//
// A timing test for N+1 passes on a unix socket and fails on a laptop under load. The number of round
// trips is a property of the code.
func TestRoundTripCounts(t *testing.T) {
	db := dataset(t)
	ctx := context.Background()

	cases := []struct {
		name           string
		fn             func(context.Context, Querier, int) ([]Author, error)
		wantRoundTrips int
		wantStatements int
	}{
		{"Naive", Naive, limit + 1, limit + 1},
		{"Join", Join, 1, 1},
		{"TwoQueries", TwoQueries, 2, 2},

		// The interesting row. Two round trips (the authors query, then the whole batch)
		// and still 51 statements for the database to parse and plan. Batching is not
		// free, it moves the cost off the network and leaves it on the server.
		{"Batched", Batched, 2, limit + 1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := NewCounter(db)

			if _, err := tc.fn(ctx, c, limit); err != nil {
				t.Fatal(err)
			}

			t.Logf("%-12s %s", tc.name, c)

			if c.RoundTrips() != tc.wantRoundTrips {
				t.Errorf("round trips: got %d, want %d", c.RoundTrips(), tc.wantRoundTrips)
			}
			if c.Statements() != tc.wantStatements {
				t.Errorf("statements: got %d, want %d", c.Statements(), tc.wantStatements)
			}
		})
	}
}

// TestJoinSendsTheParentRepeatedly measures the join's actual cost, which is not in the plan.
func TestJoinSendsTheParentRepeatedly(t *testing.T) {
	db := dataset(t)
	ctx := context.Background()

	var (
		authors   int
		bookRows  int
		joinRows  int
		nameBytes int
	)

	if err := db.QueryRow(ctx, `
		WITH a AS (SELECT id, name FROM authors ORDER BY id LIMIT $1)
		SELECT (SELECT count(*) FROM a),
		       (SELECT count(*) FROM books WHERE author_id IN (SELECT id FROM a)),
		       (SELECT count(*) FROM a LEFT JOIN books b ON b.author_id = a.id),
		       (SELECT sum(length(name)) FROM a LEFT JOIN books b ON b.author_id = a.id)`,
		limit).Scan(&authors, &bookRows, &joinRows, &nameBytes); err != nil {
		t.Fatal(err)
	}

	// Two queries send each author's name once and each book once.
	twoQueryBytes := 0
	if err := db.QueryRow(ctx,
		"SELECT sum(length(name)) FROM (SELECT name FROM authors ORDER BY id LIMIT $1) a",
		limit).Scan(&twoQueryBytes); err != nil {
		t.Fatal(err)
	}

	t.Logf("%d authors, %d books", authors, bookRows)
	t.Logf("join rows:          %d", joinRows)
	t.Logf("author name bytes:  %d through the join, %d through two queries (%.1fx)",
		nameBytes, twoQueryBytes, float64(nameBytes)/float64(twoQueryBytes))

	if joinRows < bookRows {
		t.Errorf("a LEFT JOIN cannot return fewer rows (%d) than there are children (%d)",
			joinRows, bookRows)
	}
	if nameBytes <= twoQueryBytes {
		t.Errorf("the join should repeat the parent columns: %d vs %d", nameBytes, twoQueryBytes)
	}

	t.Log("with a short name that is nothing. With a parent row holding a description or a " +
		"JSONB column, this is why two queries can beat one join.")
}

// TestLateralBeatsFetchingEverything is the case = ANY cannot do: the top 3 per author.
func TestLateralBeatsFetchingEverything(t *testing.T) {
	db := dataset(t)
	ctx := context.Background()

	const topN = 3

	got, err := TopNPerAuthor(ctx, db, limit, topN)
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != limit {
		t.Fatalf("expected %d authors, got %d", limit, len(got))
	}

	returned := 0
	for _, a := range got {
		if len(a.Books) > topN {
			t.Errorf("author %d got %d books, more than the limit of %d",
				a.ID, len(a.Books), topN)
		}
		returned += len(a.Books)

		// Descending by price, which is what the inner ORDER BY asked for.
		for i := 1; i < len(a.Books); i++ {
			if a.Books[i-1].PriceCents < a.Books[i].PriceCents {
				t.Errorf("author %d: book %d (%d) is cheaper than the next (%d)",
					a.ID, i-1, a.Books[i-1].PriceCents, a.Books[i].PriceCents)
			}
		}
	}

	// What the alternative would have read.
	var everything int
	if err := db.QueryRow(ctx, `
		SELECT count(*) FROM books
		 WHERE author_id IN (SELECT id FROM authors ORDER BY id LIMIT $1)`,
		limit).Scan(&everything); err != nil {
		t.Fatal(err)
	}

	t.Logf("LATERAL returned %d rows to answer 'top %d per author'", returned, topN)
	t.Logf("fetching everything and slicing in Go reads %d (%.1fx)",
		everything, float64(everything)/float64(returned))

	if everything <= returned {
		t.Errorf("expected the naive version to read more rows: %d vs %d", everything, returned)
	}
}

// TestBatchMustBeFullyRead pins down the pgx.Batch mistake that breaks the NEXT query rather than
// this one, which is what makes it hard to find.
func TestBatchMustBeFullyRead(t *testing.T) {
	db := dataset(t)
	ctx := context.Background()

	// A dedicated connection, because the point is that the connection is left unusable and a
	// pool would hand that connection to another test.
	conn, err := db.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()

	batch := &pgx.Batch{}
	batch.Queue("SELECT 1")
	batch.Queue("SELECT 2")
	batch.Queue("SELECT 3")

	results := conn.SendBatch(ctx, batch)

	// Read one of three, then close.
	var n int
	if err := results.QueryRow().Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("expected 1, got %d", n)
	}

	// Close with results unread. pgx reads and discards the rest rather than leaving the
	// connection broken, which is a deliberate choice on its part and worth knowing: the
	// unread statements still executed.
	if err := results.Close(); err != nil {
		t.Errorf("Close with unread results: %v", err)
	}

	// The connection still works, because Close drained it.
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := conn.QueryRow(ctx, "SELECT 42").Scan(&n); err != nil {
		t.Fatalf("the connection is unusable after a partially read batch: %v", err)
	}
	if n != 42 {
		t.Errorf("expected 42, got %d", n)
	}

	t.Log("pgx drains the batch during Close, so a partially read batch costs the work of " +
		"every statement and returns none of it. Skipping Close entirely is the version " +
		"that breaks the connection.")
}

// TestCounterCountsConcurrently, because a Counter handed to concurrent callers is the normal case
// and an int would race.
func TestCounterCountsConcurrently(t *testing.T) {
	db := dataset(t)
	ctx := context.Background()

	c := NewCounter(db)

	const goroutines = 8

	var wg sync.WaitGroup
	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()

			var n int
			if err := c.QueryRow(ctx, "SELECT 1").Scan(&n); err != nil {
				t.Errorf("querying: %v", err)
			}
		}()
	}
	wg.Wait()

	if c.RoundTrips() != goroutines {
		t.Errorf("counted %d round trips, want %d", c.RoundTrips(), goroutines)
	}
}
