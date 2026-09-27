package seed

import (
	"context"
	"testing"
	"time"

	"github.com/alexvervloet/learn-go/backends/learning/database-concepts/dbtest"
)

// TestLoadIsDeterministic is the property every measurement in this module depends on: two runs
// produce identical data, so a query plan can be compared between them.
func TestLoadIsDeterministic(t *testing.T) {
	p := dbtest.Pool(t)
	ctx := context.Background()

	fingerprint := func() string {
		t.Helper()

		var fp string
		err := p.QueryRow(ctx, `
			SELECT md5(string_agg(t, '|' ORDER BY t))
			  FROM (
			        SELECT title || ':' || price_cents::text AS t FROM books ORDER BY id LIMIT 200
			  ) s`).Scan(&fp)
		if err != nil {
			t.Fatalf("fingerprint: %v", err)
		}
		return fp
	}

	dbtest.Truncate(t, "authors", "books", "customers", "orders", "order_items")
	if err := Load(ctx, p, Small()); err != nil {
		t.Fatalf("first load: %v", err)
	}
	first := fingerprint()

	dbtest.Truncate(t, "authors", "books", "customers", "orders", "order_items")
	if err := Load(ctx, p, Small()); err != nil {
		t.Fatalf("second load: %v", err)
	}
	second := fingerprint()

	if first != second {
		t.Errorf("two loads produced different data:\n  %s\n  %s\n"+
			"a seeder using the global rand source makes every measurement unrepeatable",
			first, second)
	}
}

func TestLoadProducesTheRightShape(t *testing.T) {
	p := dbtest.Pool(t)
	ctx := context.Background()

	dbtest.Truncate(t, "authors", "books", "customers", "orders", "order_items")

	c := Small()
	if err := Load(ctx, p, c); err != nil {
		t.Fatalf("Load: %v", err)
	}

	counts := map[string]int{
		"authors":   c.Authors,
		"books":     c.Books,
		"customers": c.Customers,
		"orders":    c.Orders,
	}
	for table, want := range counts {
		var got int
		if err := p.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&got); err != nil {
			t.Fatalf("counting %s: %v", table, err)
		}
		if got != want {
			t.Errorf("%s has %d rows, want %d", table, got, want)
		}
	}

	// Every order has at least one line, so the trigger ran and the totals are non-zero.
	var ordersWithoutLines int
	err := p.QueryRow(ctx, `
		SELECT count(*) FROM orders o
		 WHERE NOT EXISTS (SELECT 1 FROM order_items i WHERE i.order_id = o.id)`).
		Scan(&ordersWithoutLines)
	if err != nil {
		t.Fatal(err)
	}
	if ordersWithoutLines != 0 {
		t.Errorf("%d orders have no lines", ordersWithoutLines)
	}

	// And the denormalised totals agree with the lines, which checks the trigger survived a
	// COPY. A trigger that fires on INSERT but not on COPY would leave every total at zero,
	// and that is worth pinning down rather than assuming.
	var mismatches int
	err = p.QueryRow(ctx, `
		SELECT count(*)
		  FROM orders o
		  JOIN (
		        SELECT order_id, SUM(quantity * unit_cents) AS computed
		          FROM order_items GROUP BY order_id
		  ) s ON s.order_id = o.id
		 WHERE o.total_cents <> s.computed`).Scan(&mismatches)
	if err != nil {
		t.Fatal(err)
	}
	if mismatches != 0 {
		t.Errorf("%d orders have a total that disagrees with their lines; the trigger did "+
			"not fire for COPY", mismatches)
	}
}

// TestAnalyzeRuns is a guard on the line most likely to be deleted as noise. Without ANALYZE the
// planner works from default statistics and every index test in this module fails in a way that
// looks like "the index is not used".
func TestAnalyzeRuns(t *testing.T) {
	p := dbtest.Pool(t)
	ctx := context.Background()

	dbtest.Truncate(t, "authors", "books", "customers", "orders", "order_items")
	if err := Load(ctx, p, Small()); err != nil {
		t.Fatal(err)
	}

	var lastAnalyze *time.Time
	err := p.QueryRow(ctx,
		"SELECT last_analyze FROM pg_stat_user_tables WHERE relname = 'books'").Scan(&lastAnalyze)
	if err != nil {
		t.Fatalf("reading pg_stat_user_tables: %v", err)
	}

	if lastAnalyze == nil {
		t.Fatal("books has never been analyzed; Load must run ANALYZE or every plan is wrong")
	}
	if time.Since(*lastAnalyze) > time.Minute {
		t.Errorf("last_analyze is %v old, so Load's ANALYZE did not run", time.Since(*lastAnalyze))
	}
}
