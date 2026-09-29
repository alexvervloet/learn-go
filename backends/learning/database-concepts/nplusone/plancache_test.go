package nplusone

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/alexvervloet/learn-go/backends/learning/database-concepts/dbtest"
	"github.com/alexvervloet/learn-go/backends/learning/database-concepts/indexes"
)

// TestGenericPlanRegression is the strangest thing this module found, and it was found by accident.
//
// # How it turned up
//
// BenchmarkFanOut/Join/1 reported 93µs run on its own and 552µs run after BenchmarkStrategies. Same
// code, same data, same process, 5.9x apart. Not the Go garbage collector: GOGC=800 changed nothing.
// The cause is on the Postgres side, and `plan_cache_mode=force_custom_plan` removes it entirely
// (94.6µs vs 573µs), which is what identified it.
//
// # The mechanism
//
// pgx prepares every statement and caches it per connection, keyed on the SQL text. Postgres plans a
// prepared statement with the actual parameter values for its first five executions, then compares
// the average cost of those custom plans against the cost of a GENERIC plan, one built without
// knowing the parameters. If the generic plan looks no worse, it switches to it permanently and stops
// replanning.
//
// BenchmarkStrategies runs the join with LIMIT 50. After five of those, Postgres adopts a generic
// plan suited to fetching many parents. BenchmarkFanOut then runs the identical SQL with LIMIT 1, gets
// the plan chosen for 50, and is 6x slower.
//
// # Why it matters outside a benchmark
//
// This is the same shape as the production incident where one endpoint gets slow after a deploy and
// recovers when you restart the process or fail over. Parameter-dependent selectivity plus a cached
// prepared statement is the ingredient list: a query filtering on a tenant ID where one tenant has a
// million rows and the rest have ten, or anything with a parameterised LIMIT or a date range.
//
// The knobs, from least to most drastic: `SET plan_cache_mode = force_custom_plan` for the session,
// which gives up plan reuse; different SQL text for the cases with different selectivity, so they get
// separate cache entries; or pgx's QueryExecModeExec, which does not prepare at all.
func TestGenericPlanRegression(t *testing.T) {
	_ = dataset(t)
	ctx := context.Background()

	tx := dbtest.Tx(t)

	// PREPARE by hand rather than relying on pgx's cache, because the point is to control which
	// plan is used and read both.
	//
	// A prepared statement belongs to the SESSION, not the transaction: rolling the transaction back
	// does not remove it, and the pooled connection carries it into whatever runs next. The first
	// version used a fixed name, so `go test -count=2` failed the second time with 42P05 ("prepared
	// statement already exists"). So the name is unique per run, and cleanup deallocates it. The
	// cleanup is registered after dbtest.Tx's, so it runs first, while the transaction is still open.
	name := "authors_with_books_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	t.Cleanup(func() { _, _ = tx.Exec(ctx, "DEALLOCATE "+name) })

	if _, err := tx.Exec(ctx, `
		PREPARE `+name+` (int) AS
		SELECT a.id, a.name, b.id, b.title, b.price_cents
		  FROM (SELECT id, name FROM authors ORDER BY id LIMIT $1) a
		  LEFT JOIN books b ON b.author_id = a.id
		 ORDER BY a.id, b.id`); err != nil {
		t.Fatal(err)
	}

	// SET LOCAL, so the setting reverts with the transaction and cannot leak to the next test
	// through the pooled connection. A plain SET here would outlive this test.
	plan := func(mode string) indexes.Analysis {
		t.Helper()

		if _, err := tx.Exec(ctx, "SET LOCAL plan_cache_mode = "+mode); err != nil {
			t.Fatalf("setting %s: %v", mode, err)
		}

		// Explain lives in the indexes package, which is where reading a plan is the
		// subject. Reusing it here is the whole reason it takes a Querier.
		a, err := indexes.Explain(ctx, tx, "EXECUTE "+name+"(1)")
		if err != nil {
			t.Fatalf("explaining with %s: %v", mode, err)
		}

		return a
	}

	custom := plan("force_custom_plan")
	generic := plan("force_generic_plan")

	t.Logf("custom plan for LIMIT 1:  %s", custom.Summary())
	t.Logf("generic plan for LIMIT 1: %s", generic.Summary())

	// The structural assertion, which is the one that holds on any machine: the two plans are
	// not the same plan.
	customTypes := custom.NodeTypes()
	genericTypes := generic.NodeTypes()

	same := len(customTypes) == len(genericTypes)
	if same {
		for i := range customTypes {
			if customTypes[i] != genericTypes[i] {
				same = false
				break
			}
		}
	}

	if same {
		t.Errorf("expected different plans, both are %v; Postgres may have improved its "+
			"generic plan for a parameterised LIMIT", customTypes)
	}

	// The timing is logged, not asserted, for the reason stated all over this module.
	if generic.ExecutionTime > custom.ExecutionTime {
		t.Logf("the generic plan is %.1fx slower here (%.3fms vs %.3fms)",
			generic.ExecutionTime/custom.ExecutionTime,
			generic.ExecutionTime, custom.ExecutionTime)
	} else {
		t.Logf("the generic plan was not slower on this run (%.3fms vs %.3fms); the plans "+
			"still differ, and which one wins depends on the parameter",
			generic.ExecutionTime, custom.ExecutionTime)
	}

	// And the number of rows each plan expected, which is where the bad decision comes from.
	t.Logf("rows estimated: custom %.0f, generic %.0f; actual %.0f",
		custom.Root.PlanRows, generic.Root.PlanRows, custom.Root.ActualRows)
}
