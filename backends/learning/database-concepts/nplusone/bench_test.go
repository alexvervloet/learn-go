package nplusone

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
)

// BenchmarkStrategies prices the four shapes against a local Postgres.
//
// Read the numbers knowing they understate the problem badly. A unix socket round trip is tens of
// microseconds; a managed database in the same region is one to two milliseconds. The benchmark below
// therefore also measures the local round trip so the README can do the arithmetic honestly instead of
// claiming a speedup that only holds on this laptop.
func BenchmarkStrategies(b *testing.B) {
	db := dataset(b)
	ctx := context.Background()

	for _, tc := range []struct {
		name string
		fn   func(context.Context, Querier, int) ([]Author, error)
	}{
		{"Naive (51 round trips)", Naive},
		{"Join (1)", Join},
		{"TwoQueries (2)", TwoQueries},
		{"Batched (2)", Batched},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := tc.fn(ctx, db, limit); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkRoundTrip is the constant the rest of it scales by: what one trip to the database costs
// when the query itself costs nothing.
func BenchmarkRoundTrip(b *testing.B) {
	db := dataset(b)
	ctx := context.Background()

	b.Run("SELECT 1", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			var n int
			if err := db.QueryRow(ctx, "SELECT 1").Scan(&n); err != nil {
				b.Fatal(err)
			}
		}
	})

	// And the same thing through a batch, which is the measurement that isolates pipelining from
	// everything else.
	b.Run("50 statements, one batch", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			batch := newBatchOfOnes(50)

			results := db.SendBatch(ctx, batch)
			for range 50 {
				var n int
				if err := results.QueryRow().Scan(&n); err != nil {
					b.Fatal(err)
				}
			}
			if err := results.Close(); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("50 statements, 50 round trips", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			for range 50 {
				var n int
				if err := db.QueryRow(ctx, "SELECT 1").Scan(&n); err != nil {
					b.Fatal(err)
				}
			}
		}
	})
}

// BenchmarkFanOut answers the question the join/two-query choice actually turns on: how many children
// per parent before repeating the parent columns costs more than the extra round trip.
func BenchmarkFanOut(b *testing.B) {
	db := dataset(b)
	ctx := context.Background()

	// A small limit means few children per query and a large one means many, which moves the
	// balance. The absolute numbers are local; the crossing point is not.
	for _, n := range []int{1, 10, 50, 200} {
		b.Run("Join/"+itoa(n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := Join(ctx, db, n); err != nil {
					b.Fatal(err)
				}
			}
		})

		b.Run("TwoQueries/"+itoa(n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := TwoQueries(ctx, db, n); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkPreparedStatementCache measures what pgx's default statement cache is worth, because it is
// the reason "51 statements" is cheaper on the second call than the first.
func BenchmarkPreparedStatementCache(b *testing.B) {
	db := dataset(b)
	ctx := context.Background()

	// The same SQL text every time: pgx describes and caches it once.
	b.Run("cached", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			var n int
			if err := db.QueryRow(ctx,
				"SELECT count(*) FROM books WHERE author_id = $1", 42).Scan(&n); err != nil {
				b.Fatal(err)
			}
		}
	})

	// Different SQL text every time, which is what building a query with fmt.Sprintf does. The
	// cache never hits, and every call pays a Parse and a Describe.
	b.Run("uncacheable (new SQL each call)", func(b *testing.B) {
		i := 0

		b.ReportAllocs()
		for b.Loop() {
			i++
			// A comment makes the text unique without changing the plan, which
			// isolates the cache miss from everything else.
			sql := "SELECT count(*) FROM books WHERE author_id = $1 -- " + itoa(i)

			var n int
			if err := db.QueryRow(ctx, sql, 42).Scan(&n); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}

	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}

	return string(digits)
}

// newBatchOfOnes builds a batch of n trivial statements.
func newBatchOfOnes(n int) *pgx.Batch {
	batch := &pgx.Batch{}
	for range n {
		batch.Queue("SELECT 1")
	}
	return batch
}
