package windowfuncs

import (
	"context"
	"reflect"
	"sync"
	"testing"

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

// TestSQLAndGoAgree is the test that makes the comparison honest.
func TestSQLAndGoAgree(t *testing.T) {
	db := dataset(t)
	ctx := context.Background()

	const customers = 200

	inSQL, err := RunningTotals(ctx, db, customers)
	if err != nil {
		t.Fatal(err)
	}

	inGo, err := RunningTotalsInGo(ctx, db, customers)
	if err != nil {
		t.Fatal(err)
	}

	if len(inSQL) != len(inGo) {
		t.Fatalf("%d rows from SQL, %d from Go", len(inSQL), len(inGo))
	}

	t.Logf("%d orders across %d customers", len(inSQL), customers)

	for i := range inSQL {
		if !reflect.DeepEqual(inSQL[i], inGo[i]) {
			t.Fatalf("row %d differs:\n  SQL %+v\n  Go  %+v", i, inSQL[i], inGo[i])
		}
	}

	// And the running total is actually running: the last row of each partition equals the sum
	// of that partition.
	sums := map[int64]int64{}
	last := map[int64]int64{}

	for _, o := range inSQL {
		sums[o.CustomerID] += o.TotalCents
		last[o.CustomerID] = o.RunningCents
	}

	for id, sum := range sums {
		if last[id] != sum {
			t.Errorf("customer %d: the final running total is %d, the sum is %d",
				id, last[id], sum)
		}
	}
}

// TestRankingFunctionsDiffer pins down which is which, using a real tie.
func TestRankingFunctionsDiffer(t *testing.T) {
	// The dataset has to exist; this test builds its own rows in the transaction.
	_ = dataset(t)
	ctx := context.Background()

	// A table built to have ties, because the seeded spend almost never ties and the three
	// functions are identical without one.
	tx := dbtest.Tx(t)

	if _, err := tx.Exec(ctx, `
		CREATE TEMP TABLE scores (name text, points int);
		INSERT INTO scores VALUES
		    ('a', 100), ('b', 90), ('c', 90), ('d', 80), ('e', 80), ('f', 70);`); err != nil {
		t.Fatal(err)
	}

	rows, err := tx.Query(ctx, `
		SELECT name, points,
		       row_number() OVER (ORDER BY points DESC, name),
		       rank()       OVER (ORDER BY points DESC),
		       dense_rank() OVER (ORDER BY points DESC)
		  FROM scores ORDER BY points DESC, name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	type row struct {
		name                     string
		points, num, rank, dense int32
	}

	var got []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.name, &r.points, &r.num, &r.rank, &r.dense); err != nil {
			t.Fatal(err)
		}
		got = append(got, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	t.Log("name  points  row_number  rank  dense_rank")
	for _, r := range got {
		t.Logf("%-5s %6d %11d %5d %11d", r.name, r.points, r.num, r.rank, r.dense)
	}

	want := []struct{ num, rank, dense int32 }{
		{1, 1, 1}, // a, 100
		{2, 2, 2}, // b, 90
		{3, 2, 2}, // c, 90 tied
		{4, 4, 3}, // d, 80: rank skipped to 4, dense_rank did not
		{5, 4, 3}, // e, 80 tied
		{6, 6, 4}, // f, 70
	}

	if len(got) != len(want) {
		t.Fatalf("expected %d rows, got %d", len(want), len(got))
	}

	for i := range want {
		if got[i].num != want[i].num || got[i].rank != want[i].rank || got[i].dense != want[i].dense {
			t.Errorf("row %d (%s): got (%d,%d,%d), want (%d,%d,%d)",
				i, got[i].name, got[i].num, got[i].rank, got[i].dense,
				want[i].num, want[i].rank, want[i].dense)
		}
	}
}

// TestFrameDefaultIsRangeNotRows is the trap, measured.
func TestFrameDefaultIsRangeNotRows(t *testing.T) {
	db := dataset(t)
	ctx := context.Background()

	frames, err := FrameComparison(ctx, db, 14)
	if err != nil {
		t.Fatal(err)
	}

	if len(frames) < 10 {
		t.Fatalf("only %d rows in the window; the seeded date range may be too narrow",
			len(frames))
	}

	t.Logf("%d rows over the last 14 days", len(frames))
	t.Log("day          total     RANGE       ROWS     GROUPS  rows in frame")

	differed := 0

	for i, f := range frames {
		if i < 8 {
			t.Logf("%s %8d %10d %10d %10d %8d",
				f.Day, f.TotalCents, f.RangeSum, f.RowsSum, f.GroupsSum, f.DaysInFrame)
		}

		if f.RangeSum != f.RowsSum {
			differed++
		}

		// GROUPS and RANGE agree for a running total, which is worth pinning so the claim in
		// the doc comment is not just an assertion.
		if f.GroupsSum != f.RangeSum {
			t.Errorf("row %d: GROUPS (%d) and RANGE (%d) disagree",
				i, f.GroupsSum, f.RangeSum)
		}
	}

	if differed == 0 {
		t.Error("RANGE and ROWS agreed on every row, so there are no peers in the data and " +
			"the test proves nothing")
	} else {
		t.Logf("RANGE and ROWS differ on %d of %d rows, because several orders share a day "+
			"and RANGE includes every peer", differed, len(frames))
	}

	// One more thing the output above makes obvious and I had not thought about: the ROWS column
	// is not monotonic when read in display order. The window's ORDER BY is the day alone, so
	// within a day Postgres picks whatever row order it likes, and it is not the day-then-total
	// order the outer query asked for.
	//
	// So a ROWS frame over a non-unique ORDER BY gives a DIFFERENT answer per row on every run.
	// The fix is the same one that makes keyset pagination work: add a tiebreaker to the window's
	// ORDER BY until it is unique.
	monotonic := true
	for i := 1; i < len(frames); i++ {
		if frames[i].RowsSum < frames[i-1].RowsSum {
			monotonic = false
			break
		}
	}

	if monotonic {
		t.Log("the ROWS running total happened to come out monotonic, which it need not: " +
			"the window's ORDER BY is the day alone and ties are ordered arbitrarily")
	} else {
		t.Log("the ROWS running total is not monotonic in display order, because the " +
			"window's ORDER BY (day) does not match the query's (day, total_cents). " +
			"A ROWS frame needs a unique ORDER BY to be deterministic.")
	}

	// sum(x) OVER () is the grand total, the same on every row.
	for i := 1; i < len(frames); i++ {
		if frames[i].GrandTotal != frames[0].GrandTotal {
			t.Errorf("sum() OVER () changed between rows: %d then %d",
				frames[0].GrandTotal, frames[i].GrandTotal)
		}
	}
}

// TestTopNPerGroupReturnsN checks the case a plain GROUP BY cannot express at all.
func TestTopNPerGroupReturnsN(t *testing.T) {
	db := dataset(t)
	ctx := context.Background()

	const n = 3

	got, err := TopNPerGroup(ctx, db, n)
	if err != nil {
		t.Fatal(err)
	}

	perCustomer := map[int64]int{}
	for _, o := range got {
		perCustomer[o.CustomerID]++

		if o.Rank > n {
			t.Errorf("customer %d: row %d has seq %d, above the limit",
				o.CustomerID, o.OrderID, o.Rank)
		}
	}

	over := 0
	for id, count := range perCustomer {
		if count > n {
			over++
			t.Errorf("customer %d got %d rows", id, count)
		}
	}

	t.Logf("%d rows for %d customers, at most %d each", len(got), len(perCustomer), n)

	// Descending by total within each customer.
	for i := 1; i < len(got); i++ {
		if got[i].CustomerID != got[i-1].CustomerID {
			continue
		}
		if got[i-1].TotalCents < got[i].TotalCents {
			t.Errorf("customer %d: %d then %d is not descending",
				got[i].CustomerID, got[i-1].TotalCents, got[i].TotalCents)
		}
	}
}

// TestIslandsFindsStreaks, on data built for it so the expected answer is known rather than inspected.
func TestIslandsFindsStreaks(t *testing.T) {
	// The dataset has to exist; this test builds its own rows in the transaction.
	_ = dataset(t)
	ctx := context.Background()

	tx := dbtest.Tx(t)

	// One customer, orders on 1-3 January, then 10-11, then 20. Three islands.
	var customerID int64
	if err := tx.QueryRow(ctx,
		"INSERT INTO customers (name, email) VALUES ('islands', 'islands@example.test') RETURNING id").
		Scan(&customerID); err != nil {
		t.Fatal(err)
	}

	for _, day := range []string{
		"2025-01-01", "2025-01-02", "2025-01-03",
		"2025-01-10", "2025-01-11",
		"2025-01-20",
	} {
		if _, err := tx.Exec(ctx, `
			INSERT INTO orders (customer_id, status, placed_at, total_cents)
			VALUES ($1, 'paid', $2::date + interval '9 hours', 100)`,
			customerID, day); err != nil {
			t.Fatalf("inserting %s: %v", day, err)
		}
	}

	got, err := Islands(ctx, tx, customerID)
	if err != nil {
		t.Fatal(err)
	}

	want := []Gap{
		{CustomerID: customerID, StartDay: "2025-01-01", EndDay: "2025-01-03", Days: 3},
		{CustomerID: customerID, StartDay: "2025-01-10", EndDay: "2025-01-11", Days: 2},
		{CustomerID: customerID, StartDay: "2025-01-20", EndDay: "2025-01-20", Days: 1},
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}

	for _, g := range got {
		t.Logf("%s to %s, %d days", g.StartDay, g.EndDay, g.Days)
	}
}

// TestFilteringInSQLMovesFewerRows is the assertion that does not depend on the machine.
//
// The benchmark says the Go version is faster on a unix socket. This says how much less data the SQL
// version moves, which is the number that decides it when the database is not on the same machine.
func TestFilteringInSQLMovesFewerRows(t *testing.T) {
	db := dataset(t)
	ctx := context.Background()

	const n = 3

	filtered, err := TopNPerGroup(ctx, db, n)
	if err != nil {
		t.Fatal(err)
	}

	var everything int
	if err := db.QueryRow(ctx, "SELECT count(*) FROM orders").Scan(&everything); err != nil {
		t.Fatal(err)
	}

	t.Logf("filtered in SQL: %d rows", len(filtered))
	t.Logf("fetch and slice: %d rows (%.2fx)",
		everything, float64(everything)/float64(len(filtered)))

	if len(filtered) >= everything {
		t.Errorf("the window function returned %d of %d rows, so it filtered nothing",
			len(filtered), everything)
	}

	// The measured local numbers, for the record rather than as an assertion: the Go version is
	// 1.44x faster and uses 2.06x the memory (2.35ms/1.92MB against 3.39ms/932KB). The ratio
	// that travels is the row count above.
	t.Log("locally the Go version wins on time and loses on memory; the row count is what " +
		"decides it over a network")
}
