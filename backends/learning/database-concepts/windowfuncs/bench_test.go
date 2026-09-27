package windowfuncs

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
)

// BenchmarkRunningTotal is the measurement the package doc's claim rests on.
//
// Both versions return byte-identical results, so this is a straight price comparison at several set
// sizes. The interesting output is where the lines cross, not the absolute numbers.
func BenchmarkRunningTotal(b *testing.B) {
	db := dataset(b)
	ctx := context.Background()

	for _, customers := range []int{10, 100, 1000} {
		b.Run("SQL/"+itoa(customers), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := RunningTotals(ctx, db, customers); err != nil {
					b.Fatal(err)
				}
			}
		})

		b.Run("Go/"+itoa(customers), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := RunningTotalsInGo(ctx, db, customers); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkTopNPerGroup is the case the window function is for: it FILTERS, so fewer rows cross the
// wire. This is the comparison BenchmarkRunningTotal does not make.
func BenchmarkTopNPerGroup(b *testing.B) {
	db := dataset(b)
	ctx := context.Background()

	const n = 3

	b.Run("window function, filtered in SQL", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := TopNPerGroup(ctx, db, n); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("fetch everything, slice in Go", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := topNInGo(ctx, db, n); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// topNInGo is the alternative: every order, sorted, then trimmed per customer.
func topNInGo(ctx context.Context, q Querier, n int) ([]OrderTotal, error) {
	rows, err := q.Query(ctx, `
		SELECT customer_id, id, total_cents
		  FROM orders
		 ORDER BY customer_id, total_cents DESC, id`)
	if err != nil {
		return nil, err
	}

	all, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (OrderTotal, error) {
		var o OrderTotal
		err := r.Scan(&o.CustomerID, &o.OrderID, &o.TotalCents)
		return o, err
	})
	if err != nil {
		return nil, err
	}

	out := make([]OrderTotal, 0, len(all))

	var (
		last  int64 = -1
		count int32
	)

	for _, o := range all {
		if o.CustomerID != last {
			last, count = o.CustomerID, 0
		}

		count++
		if count > int32(n) {
			continue
		}

		o.Rank = count
		out = append(out, o)
	}

	return out, nil
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
