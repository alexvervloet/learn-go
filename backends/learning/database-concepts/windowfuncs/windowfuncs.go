// Package windowfuncs covers the SQL that removes a loop from Go.
//
// # What a window function is
//
// An aggregate collapses rows: GROUP BY customer_id turns 5,000 orders into 1,000 totals. A window
// function computes across a set of rows and keeps every row. So "each order, with that customer's
// running total" is one query rather than a GROUP BY plus a join back, or a fetch plus a loop.
//
// # Why it belongs in a Go repo
//
// Because the alternative is so tempting. Fetching 5,000 rows and computing a running total in a Go
// loop is fifteen lines, easy to read, easy to test, and wrong in a way that only shows up later: it
// pulls every row over the wire to compute something the database can compute while it scans. The tests
// measure both.
//
// And measuring it twice did not say what I expected either time.
//
// The Go loop is faster at every size: 1.5x at 10 customers, 2.1x at 100, 1.6x at 1000. Both versions
// return identical results and both return every row, so the window function's four extra columns
// (running total, sequence, lag, lead) are pure cost on a local socket.
//
// So I built the case the window function was supposed to win, where it FILTERS and fewer rows cross
// the wire: the top 3 orders per customer, 2,830 rows instead of 5,000. The Go version is still faster,
// 2.35ms against 3.39ms. It is not close.
//
// What the window function does win is memory and bytes. 932 KB and 5,679 allocations against 1.92 MB
// and 10,021. Half the memory, 1.44x slower.
//
// # The honest conclusion
//
// On a unix socket to a local Postgres, pushing a cheap per-row calculation into SQL costs time and
// saves memory. The received wisdom ("do it in the database") is a claim about a REMOTE database, where
// the row count sets the bytes on the wire and the bytes on the wire set the latency, and it does not
// survive being measured locally.
//
// Which means the machine-independent number is the one to reason about, exactly as in the nplusone
// package: how many rows and how many bytes cross the connection. The tests assert on that and log the
// timings.
//
// The reasons to reach for a window function that survive all of this:
//
//	the result feeds into more SQL, so there is no Go loop to put the calculation in
//	the calculation is a ranking, a percentile or a gaps-and-islands grouping, which is
//	  genuinely hard to get right by hand and is four lines here
//	the per-partition boundary conditions in the Go version are where the bugs live, and
//	  RunningTotalsInGo below is where the fifteen lines become thirty
//
// # The frame is where the bugs are
//
// Every window function has a frame, and the default is not what people assume:
//
//	with ORDER BY     RANGE BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW
//	without ORDER BY  RANGE BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING
//
// So sum(x) OVER (ORDER BY d) is a running total and sum(x) OVER () is a grand total, from the presence
// of one clause. And RANGE is not ROWS: RANGE includes every PEER of the current row, meaning every row
// with the same ORDER BY value. A running total over a day column with several rows per day jumps to
// the end of each day rather than accumulating row by row, and the numbers look plausible.
package windowfuncs

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Querier is the subset these functions need.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// OrderTotal is one order with its position in the customer's history.
type OrderTotal struct {
	CustomerID   int64
	OrderID      int64
	TotalCents   int64
	RunningCents int64
	Rank         int32
	PrevCents    *int64
	NextCents    *int64
}

// RunningTotals is the query. One pass, every row, the running total computed as Postgres scans.
//
// PARTITION BY restarts the window per customer, which is the part a Go loop has to remember to do and
// the part that is one clause here.
func RunningTotals(ctx context.Context, q Querier, customers int) ([]OrderTotal, error) {
	rows, err := q.Query(ctx, `
		SELECT o.customer_id,
		       o.id,
		       o.total_cents,
		       sum(o.total_cents) OVER (PARTITION BY o.customer_id ORDER BY o.placed_at, o.id) AS running,
		       row_number()       OVER (PARTITION BY o.customer_id ORDER BY o.placed_at, o.id) AS seq,
		       lag(o.total_cents)  OVER (PARTITION BY o.customer_id ORDER BY o.placed_at, o.id) AS prev,
		       lead(o.total_cents) OVER (PARTITION BY o.customer_id ORDER BY o.placed_at, o.id) AS next
		  FROM orders o
		 WHERE o.customer_id <= $1
		 ORDER BY o.customer_id, o.placed_at, o.id`, customers)
	if err != nil {
		return nil, fmt.Errorf("running totals: %w", err)
	}

	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (OrderTotal, error) {
		var o OrderTotal
		err := r.Scan(&o.CustomerID, &o.OrderID, &o.TotalCents, &o.RunningCents,
			&o.Rank, &o.PrevCents, &o.NextCents)
		return o, err
	})
}

// RunningTotalsInGo is the alternative, written as well as it can be written.
//
// Nothing is wrong with this code. It is the comparison, not a bad example.
func RunningTotalsInGo(ctx context.Context, q Querier, customers int) ([]OrderTotal, error) {
	rows, err := q.Query(ctx, `
		SELECT customer_id, id, total_cents
		  FROM orders
		 WHERE customer_id <= $1
		 ORDER BY customer_id, placed_at, id`, customers)
	if err != nil {
		return nil, fmt.Errorf("selecting orders: %w", err)
	}

	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (OrderTotal, error) {
		var o OrderTotal
		err := r.Scan(&o.CustomerID, &o.OrderID, &o.TotalCents)
		return o, err
	})
	if err != nil {
		return nil, fmt.Errorf("scanning orders: %w", err)
	}

	var (
		running  int64
		seq      int32
		lastCust int64 = -1
	)

	for i := range out {
		if out[i].CustomerID != lastCust {
			running, seq, lastCust = 0, 0, out[i].CustomerID
		}

		running += out[i].TotalCents
		seq++

		out[i].RunningCents = running
		out[i].Rank = seq

		// lag and lead, which is where the Go version stops being fifteen lines. The
		// boundary conditions are per-partition, not per-slice, and getting them wrong is
		// silent.
		if i > 0 && out[i-1].CustomerID == out[i].CustomerID {
			prev := out[i-1].TotalCents
			out[i].PrevCents = &prev
		}
		if i+1 < len(out) && out[i+1].CustomerID == out[i].CustomerID {
			next := out[i+1].TotalCents
			out[i].NextCents = &next
		}
	}

	return out, nil
}

// Ranking is the three ranking functions side by side, which is the only way to remember which is
// which.
type Ranking struct {
	Name       string
	TotalCents int64
	RowNumber  int32
	Rank       int32
	DenseRank  int32
	Percentile float64
}

// TopCustomers ranks customers by spend.
//
//	row_number  1 2 3 4   always distinct, ties broken arbitrarily
//	rank        1 2 2 4   ties share, and the next value SKIPS
//	dense_rank  1 2 2 3   ties share, and the next value does not skip
//
// Which one to use is a product decision, not a technical one. A leaderboard showing "3rd place" after
// two people tie for first wants dense_rank; a pagination key wants row_number, because it is the only
// one guaranteed unique.
func TopCustomers(ctx context.Context, q Querier, limit int) ([]Ranking, error) {
	rows, err := q.Query(ctx, `
		WITH spend AS (
		     SELECT c.id, c.name, coalesce(sum(o.total_cents), 0) AS total
		       FROM customers c
		       LEFT JOIN orders o ON o.customer_id = c.id
		      GROUP BY c.id, c.name
		)
		SELECT name, total,
		       row_number() OVER (ORDER BY total DESC, id),
		       rank()       OVER (ORDER BY total DESC),
		       dense_rank() OVER (ORDER BY total DESC),
		       percent_rank() OVER (ORDER BY total DESC)
		  FROM spend
		 ORDER BY total DESC, id
		 LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("ranking customers: %w", err)
	}

	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Ranking, error) {
		var x Ranking
		err := r.Scan(&x.Name, &x.TotalCents, &x.RowNumber, &x.Rank, &x.DenseRank, &x.Percentile)
		return x, err
	})
}

// TopNPerGroup is the query a window function is genuinely the best tool for: the N biggest orders per
// customer.
//
// A window function cannot be used in WHERE, because WHERE runs before the window is computed. So it
// needs a subquery or a CTE, and that is not a limitation to work around, it is the evaluation order.
//
// The LATERAL version in the nplusone package is faster when there is an index on (customer_id,
// total_cents), because it can stop after N rows per customer. This version reads every row and ranks
// it. Both are correct; which is faster depends entirely on whether the index exists.
func TopNPerGroup(ctx context.Context, q Querier, n int) ([]OrderTotal, error) {
	rows, err := q.Query(ctx, `
		SELECT customer_id, id, total_cents, running, seq
		  FROM (
		       SELECT customer_id, id, total_cents,
		              sum(total_cents) OVER (PARTITION BY customer_id) AS running,
		              row_number()     OVER (PARTITION BY customer_id ORDER BY total_cents DESC, id) AS seq
		         FROM orders
		  ) ranked
		 WHERE seq <= $1
		 ORDER BY customer_id, seq`, n)
	if err != nil {
		return nil, fmt.Errorf("top %d per group: %w", n, err)
	}

	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (OrderTotal, error) {
		var o OrderTotal
		err := r.Scan(&o.CustomerID, &o.OrderID, &o.TotalCents, &o.RunningCents, &o.Rank)
		return o, err
	})
}

// Frame is one row of the frame comparison.
type Frame struct {
	Day         string
	TotalCents  int64
	RangeSum    int64
	RowsSum     int64
	GroupsSum   int64
	MovingAvg   float64
	GrandTotal  int64
	DaysInFrame int32
}

// FrameComparison is the query that shows RANGE, ROWS and GROUPS disagreeing on the same data.
//
// This is the subtlest thing in the package. All three are "the running total", all three are correct,
// and they return different numbers:
//
//	ROWS   BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW   counts physical rows. Stops at this row.
//	RANGE  BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW   counts rows whose ORDER BY value is <=
//	                                                     this one, so it includes every PEER and
//	                                                     jumps to the end of the day.
//	GROUPS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW   counts peer GROUPS, which for a running
//	                                                     total is the same as RANGE.
//
// RANGE is the default, which means the default running total over a non-unique ORDER BY column is
// probably not the one intended.
func FrameComparison(ctx context.Context, q Querier, days int) ([]Frame, error) {
	rows, err := q.Query(ctx, `
		WITH daily AS (
		     SELECT date_trunc('day', placed_at)::date AS day, total_cents
		       FROM orders
		      -- make_interval rather than ($1 || ' days')::interval, which is the form that
		      -- appears in every answer online. The concatenation makes Postgres infer $1 as
		      -- text, and pgx then refuses to encode an int into a text parameter:
		      -- "unable to encode 14 into text format for text (OID 25)". make_interval's
		      -- argument is typed, so the parameter resolves to int.
		      WHERE placed_at >= (SELECT max(placed_at) FROM orders) - make_interval(days => $1)
		)
		SELECT to_char(day, 'YYYY-MM-DD'),
		       total_cents,
		       sum(total_cents) OVER (ORDER BY day RANGE  BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW),
		       sum(total_cents) OVER (ORDER BY day ROWS   BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW),
		       sum(total_cents) OVER (ORDER BY day GROUPS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW),
		       avg(total_cents) OVER (ORDER BY day ROWS   BETWEEN 6 PRECEDING AND CURRENT ROW),
		       sum(total_cents) OVER (),
		       count(*)         OVER (ORDER BY day RANGE BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW)
		  FROM daily
		 ORDER BY day, total_cents`, days)
	if err != nil {
		return nil, fmt.Errorf("frame comparison: %w", err)
	}

	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Frame, error) {
		var f Frame
		err := r.Scan(&f.Day, &f.TotalCents, &f.RangeSum, &f.RowsSum, &f.GroupsSum,
			&f.MovingAvg, &f.GrandTotal, &f.DaysInFrame)
		return f, err
	})
}

// Gap is a run of consecutive days with orders, found by the gaps-and-islands trick.
type Gap struct {
	CustomerID int64
	StartDay   string
	EndDay     string
	Days       int32
}

// Islands finds runs of consecutive days on which a customer ordered.
//
// The trick is worth knowing because it comes up constantly (streaks, sessions, uptime windows) and it
// is not obvious: subtract the row number from the date. Consecutive dates increment by one and so does
// the row number, so the difference is CONSTANT within a run and changes at every gap. Group by that
// difference.
//
// Four lines of SQL for something that is a nested loop with state in Go.
func Islands(ctx context.Context, q Querier, customerID int64) ([]Gap, error) {
	rows, err := q.Query(ctx, `
		WITH days AS (
		     SELECT DISTINCT customer_id, date_trunc('day', placed_at)::date AS day
		       FROM orders WHERE customer_id = $1
		), grouped AS (
		     SELECT customer_id, day,
		            day - (row_number() OVER (PARTITION BY customer_id ORDER BY day))::int AS island
		       FROM days
		)
		SELECT customer_id,
		       to_char(min(day), 'YYYY-MM-DD'),
		       to_char(max(day), 'YYYY-MM-DD'),
		       count(*)::int
		  FROM grouped
		 GROUP BY customer_id, island
		 ORDER BY min(day)`, customerID)
	if err != nil {
		return nil, fmt.Errorf("finding islands: %w", err)
	}

	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Gap, error) {
		var g Gap
		err := r.Scan(&g.CustomerID, &g.StartDay, &g.EndDay, &g.Days)
		return g, err
	})
}
