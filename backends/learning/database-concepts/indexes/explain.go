// Package indexes measures what Postgres actually does with a query, using EXPLAIN (ANALYZE).
//
// # Why assert on the plan and not on the time
//
// A test asserting "this query takes under 5 ms" fails on a loaded CI runner and passes on a fast
// laptop with a warm cache, so it gets marked flaky and deleted. A test asserting "this query uses
// an Index Scan and not a Seq Scan" is deterministic: the planner's choice depends on the
// statistics, not on the machine.
//
// So every test here reads the plan. The timings are logged for interest and asserted only where
// the ratio is enormous.
//
// # Reading a plan
//
// The node types that matter, cheapest first:
//
//	Index Only Scan   the index has every column the query needs, so the table is never read
//	Index Scan        walk the index, then fetch each matching row from the table
//	Bitmap Index Scan build a bitmap of pages, then read them in physical order. Postgres
//	                  chooses this over an Index Scan when many rows match, because reading
//	                  pages in order beats random access
//	Seq Scan          read every page
//
// A Seq Scan is not a failure. On a small table, or a query returning most of the rows, it is the
// right plan and forcing an index makes things slower. TestSeqScanIsCorrectForSmallTables measures
// that.
package indexes

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Plan is the part of EXPLAIN's JSON output this package looks at.
//
// Postgres returns a deeply nested structure with dozens of fields. Decoding only what is needed
// keeps the tests readable, and the raw JSON is kept on the side for when a failure needs the rest.
type Plan struct {
	NodeType     string  `json:"Node Type"`
	RelationName string  `json:"Relation Name"`
	IndexName    string  `json:"Index Name"`
	ActualRows   float64 `json:"Actual Rows"`

	// PlanRows is what the planner ESTIMATED, and the ratio between it and ActualRows is the
	// first number to look at in a plan that makes no sense. An estimate off by 1000x means the
	// statistics are stale or the predicate is one Postgres cannot estimate, and every join
	// choice above that node was made on a wrong number.
	PlanRows float64 `json:"Plan Rows"`

	ActualLoops float64 `json:"Actual Loops"`
	TotalCost   float64 `json:"Total Cost"`
	ActualTotal float64 `json:"Actual Total Time"`
	Filter      string  `json:"Filter"`
	IndexCond   string  `json:"Index Cond"`
	RowsRemoved float64 `json:"Rows Removed by Filter"`
	Plans       []Plan  `json:"Plans"`
}

// explainResult is EXPLAIN's top-level JSON shape.
type explainResult struct {
	Plan          Plan    `json:"Plan"`
	ExecutionTime float64 `json:"Execution Time"`
	PlanningTime  float64 `json:"Planning Time"`
}

// Analysis is what Explain returns.
type Analysis struct {
	Root          Plan
	ExecutionTime float64 // milliseconds
	PlanningTime  float64
	Raw           string
}

// Querier is the subset of pgx this package needs, so it works with a pool or a transaction.
type Querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Explain runs a query under EXPLAIN (ANALYZE, FORMAT JSON) and returns the plan.
//
// ANALYZE means the query is EXECUTED, which is the point: without it the row counts are estimates
// and a plan that the planner thinks returns 10 rows and actually returns 100,000 looks fine. It
// also means an EXPLAIN ANALYZE of an INSERT really inserts, which is why every caller here is
// inside a rolled-back transaction.
//
// BUFFERS is deliberately off. It reports cache hits, which depend on what ran before, and a test
// asserting on them is asserting on test ordering.
func Explain(ctx context.Context, db Querier, query string, args ...any) (Analysis, error) {
	var raw string

	sql := "EXPLAIN (ANALYZE, FORMAT JSON) " + query

	if err := db.QueryRow(ctx, sql, args...).Scan(&raw); err != nil {
		return Analysis{}, fmt.Errorf("explaining %q: %w", truncate(query), err)
	}

	// FORMAT JSON returns a one-element array.
	var results []explainResult
	if err := json.Unmarshal([]byte(raw), &results); err != nil {
		return Analysis{}, fmt.Errorf("parsing the plan: %w", err)
	}
	if len(results) == 0 {
		return Analysis{}, fmt.Errorf("empty plan for %q", truncate(query))
	}

	return Analysis{
		Root:          results[0].Plan,
		ExecutionTime: results[0].ExecutionTime,
		PlanningTime:  results[0].PlanningTime,
		Raw:           raw,
	}, nil
}

// NodeTypes returns every node type in the plan tree, depth first.
//
// A flat list is what an assertion wants: "does this plan contain a Seq Scan anywhere" is the
// question, and walking the tree at every call site would be noise.
func (a Analysis) NodeTypes() []string {
	var out []string

	var walk func(Plan)
	walk = func(p Plan) {
		out = append(out, p.NodeType)
		for _, child := range p.Plans {
			walk(child)
		}
	}
	walk(a.Root)

	return out
}

// Uses reports whether the plan contains a node of the given type.
func (a Analysis) Uses(nodeType string) bool {
	for _, t := range a.NodeTypes() {
		if t == nodeType {
			return true
		}
	}
	return false
}

// UsesIndex reports whether the plan reads the named index anywhere.
//
// Checking the index NAME rather than just "is it an index scan" is what makes a composite-index
// test meaningful: a query can use an index scan on the wrong index and look fine.
func (a Analysis) UsesIndex(name string) bool {
	found := false

	var walk func(Plan)
	walk = func(p Plan) {
		if p.IndexName == name {
			found = true
		}
		for _, child := range p.Plans {
			walk(child)
		}
	}
	walk(a.Root)

	return found
}

// IndexesUsed returns every index the plan reads.
func (a Analysis) IndexesUsed() []string {
	var out []string

	var walk func(Plan)
	walk = func(p Plan) {
		if p.IndexName != "" {
			out = append(out, p.IndexName)
		}
		for _, child := range p.Plans {
			walk(child)
		}
	}
	walk(a.Root)

	return out
}

// RowsRemovedByFilter totals the rows the plan read and then threw away.
//
// The number that says an index is missing or the wrong shape. A plan reading 10,000 rows to return
// 12 is doing 9,988 rows of pointless work, and that shows up here rather than in the node type.
func (a Analysis) RowsRemovedByFilter() float64 {
	total := 0.0

	var walk func(Plan)
	walk = func(p Plan) {
		total += p.RowsRemoved
		for _, child := range p.Plans {
			walk(child)
		}
	}
	walk(a.Root)

	return total
}

// Summary is a one-line description for a test log.
func (a Analysis) Summary() string {
	var sb strings.Builder

	sb.WriteString(strings.Join(a.NodeTypes(), " <- "))

	if used := a.IndexesUsed(); len(used) > 0 {
		sb.WriteString(" via " + strings.Join(used, ", "))
	}

	fmt.Fprintf(&sb, " [%.2fms exec, %.2fms plan, %.0f rows",
		a.ExecutionTime, a.PlanningTime, a.Root.ActualRows)

	if removed := a.RowsRemovedByFilter(); removed > 0 {
		fmt.Fprintf(&sb, ", %.0f discarded by filter", removed)
	}
	sb.WriteString("]")

	return sb.String()
}

func truncate(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= 60 {
		return s
	}
	return s[:57] + "..."
}
