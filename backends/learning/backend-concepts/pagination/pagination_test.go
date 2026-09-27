package pagination

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/alexvervloet/learn-go/backends/learning/backend-concepts/internal/pgtest"
)

const (
	// 50,000 rows, because OFFSET's cost is linear in the offset and a small table hides it. At
	// 500 rows every page is fast and the wrong conclusion is available.
	rowCount = 50_000

	// Timestamps deliberately COLLIDE: 50,000 rows over 500 distinct seconds means 100 rows share
	// each timestamp. Real feeds do this (a batch import, a burst of webhook deliveries) and it is
	// what makes a non-unique pagination key lose rows.
	distinctSeconds = 500
)

var seedOnce sync.Once

func dataset(t testing.TB) *pgxpool.Pool {
	t.Helper()

	p := pgtest.Pool(t)

	seedOnce.Do(func() {
		ctx := context.Background()

		if _, err := p.Exec(ctx, "TRUNCATE events RESTART IDENTITY"); err != nil {
			t.Fatalf("truncating: %v", err)
		}

		// A fixed seed, so a failing assertion is reproducible.
		r := rand.New(rand.NewPCG(7, 11))
		base := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)

		rows := make([][]any, rowCount)
		actors := []string{"alice", "bob", "carol", "dave"}
		kinds := []string{"created", "updated", "deleted", "viewed"}

		for i := range rows {
			// i/(rowCount/distinctSeconds) gives 100 consecutive rows the same second.
			second := i / (rowCount / distinctSeconds)

			rows[i] = []any{
				actors[r.IntN(len(actors))],
				kinds[r.IntN(len(kinds))],
				base.Add(time.Duration(second) * time.Second),
				"payload " + strings.Repeat("x", r.IntN(40)),
			}
		}

		copied, err := p.CopyFrom(ctx, pgx.Identifier{"events"},
			[]string{"actor", "kind", "created_at", "payload"}, pgx.CopyFromRows(rows))
		if err != nil {
			t.Fatalf("copying rows: %v", err)
		}
		if copied != rowCount {
			t.Fatalf("copied %d rows, want %d", copied, rowCount)
		}

		// ANALYZE, because CountEstimate reads the planner's statistics and an unanalysed table
		// reports -1.
		if _, err := p.Exec(ctx, "ANALYZE events"); err != nil {
			t.Fatalf("analysing: %v", err)
		}
	})

	return p
}

// TestCursorRoundTrips, because everything else depends on it and a cursor that loses precision is the
// non-unique-key bug in disguise.
func TestCursorRoundTrips(t *testing.T) {
	for _, c := range []Cursor{
		{CreatedAt: time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC), ID: 1},
		{CreatedAt: time.Date(2025, 6, 1, 12, 0, 0, 123456789, time.UTC), ID: 999_999_999},
		{CreatedAt: time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC), ID: 0},
		{CreatedAt: time.Date(2025, 6, 1, 12, 0, 0, 1, time.UTC), ID: -1},
	} {
		encoded := EncodeCursor(c)

		// URL-safe: no characters that need escaping in a query string.
		if strings.ContainsAny(encoded, "+/=") {
			t.Errorf("%q is not URL-safe", encoded)
		}

		got, err := DecodeCursor(encoded)
		if err != nil {
			t.Fatalf("decoding %q: %v", encoded, err)
		}

		if !got.CreatedAt.Equal(c.CreatedAt) {
			t.Errorf("timestamp: got %v, want %v", got.CreatedAt, c.CreatedAt)
		}
		if got.ID != c.ID {
			t.Errorf("id: got %d, want %d", got.ID, c.ID)
		}

		// Nanosecond precision survives, which a Unix-second encoding would not.
		if got.CreatedAt.Nanosecond() != c.CreatedAt.Nanosecond() {
			t.Errorf("lost the nanoseconds: %d vs %d",
				got.CreatedAt.Nanosecond(), c.CreatedAt.Nanosecond())
		}
	}
}

// TestBadCursorsAreClientErrors, so a handler can return 400 rather than 500.
func TestBadCursorsAreClientErrors(t *testing.T) {
	for _, s := range []string{
		"not base64 at all!!",
		"",
		"YWJj",             // "abc", no separator
		"MjAyNS0wNi0wMXwx", // a bad timestamp
		"MjAyNS0wNi0wMVQwMDowMDowMFp8bm90YW51bWJlcg", // a bad id
	} {
		_, err := DecodeCursor(s)

		if err == nil {
			t.Errorf("%q decoded without error", s)
			continue
		}
		if !errors.Is(err, ErrBadCursor) {
			t.Errorf("%q gave %v, which does not match ErrBadCursor", s, err)
		}
	}

	// And the empty cursor means "first page" at the Keyset level, not an error.
	db := dataset(t)

	page, err := Keyset(context.Background(), db, 5, "")
	if err != nil {
		t.Fatalf("the empty cursor should mean the first page: %v", err)
	}
	if len(page.Events) != 5 {
		t.Errorf("got %d events, want 5", len(page.Events))
	}
}

// TestOffsetCostGrowsWithTheOffset is the assertion that does not depend on the machine: the plan says how
// many rows were read.
func TestOffsetCostGrowsWithTheOffset(t *testing.T) {
	db := dataset(t)
	ctx := context.Background()

	t.Log("offset   rows read   exec ms   plan")

	var firstRows, lastRows float64

	for i, offset := range []int{0, 100, 5_000, 45_000} {
		var plan struct {
			rows   float64
			execMS float64
			shape  string
		}

		var jsonText string
		if err := db.QueryRow(ctx, `
			EXPLAIN (ANALYZE, FORMAT JSON)
			SELECT id, actor, kind, created_at, payload
			  FROM events ORDER BY created_at DESC, id DESC LIMIT 20 OFFSET `+itoa(offset)).
			Scan(&jsonText); err != nil {
			t.Fatal(err)
		}

		plan.rows, plan.execMS, plan.shape = parseRowsRead(t, jsonText)

		t.Logf("%6d %11.0f %9.2f   %s", offset, plan.rows, plan.execMS, plan.shape)

		if i == 0 {
			firstRows = plan.rows
		}
		lastRows = plan.rows
	}

	// The assertion: reading page 2,251 touches far more rows than reading page 1. The ratio is
	// data, not timing, so it holds on any machine.
	if lastRows <= firstRows {
		t.Errorf("OFFSET 45000 read %.0f rows and OFFSET 0 read %.0f; the cost should grow",
			lastRows, firstRows)
	}

	t.Logf("OFFSET 45000 read %.0fx as many rows as OFFSET 0 to return the same 20",
		lastRows/firstRows)

	// Keyset, for comparison. Same 20 rows, and the number read does not depend on how deep the
	// page is.
	page, err := Keyset(ctx, db, 20, "")
	if err != nil {
		t.Fatal(err)
	}

	// Walk to roughly the same depth with the cursor, then read the plan.
	cursor := page.Next
	for range 2_249 {
		page, err = Keyset(ctx, db, 20, cursor)
		if err != nil {
			t.Fatal(err)
		}
		if page.Next == "" {
			break
		}
		cursor = page.Next
	}

	c, err := DecodeCursor(cursor)
	if err != nil {
		t.Fatal(err)
	}

	var jsonText string
	if err := db.QueryRow(ctx, `
		EXPLAIN (ANALYZE, FORMAT JSON)
		SELECT id, actor, kind, created_at, payload
		  FROM events WHERE (created_at, id) < ($1, $2)
		 ORDER BY created_at DESC, id DESC LIMIT 20`, c.CreatedAt, c.ID).Scan(&jsonText); err != nil {
		t.Fatal(err)
	}

	keysetRows, keysetMS, keysetShape := parseRowsRead(t, jsonText)

	t.Logf("keyset at the same depth: %.0f rows read, %.2fms, %s", keysetRows, keysetMS, keysetShape)

	if keysetRows >= lastRows {
		t.Errorf("keyset read %.0f rows where OFFSET 45000 read %.0f; keyset should read "+
			"only the page", keysetRows, lastRows)
	}

	t.Logf("keyset reads %.0f rows for a page of 20 whatever the depth; OFFSET 45000 reads %.0f",
		keysetRows, lastRows)
}

// TestOffsetSkipsRows is the bug that matters more than the speed.
func TestOffsetSkipsRows(t *testing.T) {
	tx := pgtest.Tx(t)
	ctx := context.Background()

	// A small, controlled set inside the transaction, so the inserted row's effect is exact rather
	// than probabilistic.
	base := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)

	for i := range 10 {
		if _, err := tx.Exec(ctx, `
			INSERT INTO events (actor, kind, created_at, payload)
			VALUES ('seed', 'created', $1, $2)`,
			base.Add(time.Duration(i)*time.Second), "row "+itoa(i)); err != nil {
			t.Fatal(err)
		}
	}

	// Page 1 of 5, newest first: rows 9, 8, 7, 6, 5.
	first, err := offsetIn(ctx, tx, 5, 0, base)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("page 1: %v", payloads(first))

	// A new row arrives, which is what a feed does.
	if _, err := tx.Exec(ctx, `
		INSERT INTO events (actor, kind, created_at, payload)
		VALUES ('seed', 'created', $1, 'row NEW')`,
		base.Add(100*time.Second)); err != nil {
		t.Fatal(err)
	}

	// Page 2 of 5, offset 5.
	second, err := offsetIn(ctx, tx, 5, 5, base)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("page 2: %v", payloads(second))

	// Row 5 was last on page 1 and is now first on page 2. The client sees it twice and never sees
	// what should have been there.
	seen := map[string]int{}
	for _, e := range append(append([]Event{}, first...), second...) {
		seen[e.Payload]++
	}

	duplicates := []string{}
	for payload, n := range seen {
		if n > 1 {
			duplicates = append(duplicates, payload)
		}
	}

	t.Logf("across two pages, these rows appeared twice: %v", duplicates)

	if len(duplicates) == 0 {
		t.Error("expected OFFSET to repeat a row after an insert, and it did not")
	}

	// And the symmetric case: a DELETE skips a row entirely, which is the one nobody notices.
	deleted, err := skipDemo(ctx, tx, base)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("after deleting a row between pages, %q was never returned on either page", deleted)

	if deleted == "" {
		t.Error("expected a row to be skipped after a delete")
	}
}

// TestKeysetDoesNotSkipRows is the same scenario with a cursor.
func TestKeysetDoesNotSkipRows(t *testing.T) {
	tx := pgtest.Tx(t)
	ctx := context.Background()

	base := time.Date(2031, 1, 1, 0, 0, 0, 0, time.UTC)

	if _, err := tx.Exec(ctx, "TRUNCATE events"); err != nil {
		t.Fatal(err)
	}

	for i := range 10 {
		if _, err := tx.Exec(ctx, `
			INSERT INTO events (actor, kind, created_at, payload)
			VALUES ('seed', 'created', $1, $2)`,
			base.Add(time.Duration(i)*time.Second), "row "+itoa(i)); err != nil {
			t.Fatal(err)
		}
	}

	first, err := Keyset(ctx, tx, 5, "")
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("page 1: %v", payloads(first.Events))

	// The same insert.
	if _, err := tx.Exec(ctx, `
		INSERT INTO events (actor, kind, created_at, payload)
		VALUES ('seed', 'created', $1, 'row NEW')`,
		base.Add(100*time.Second)); err != nil {
		t.Fatal(err)
	}

	second, err := Keyset(ctx, tx, 5, first.Next)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("page 2: %v", payloads(second.Events))

	seen := map[string]int{}
	for _, e := range append(append([]Event{}, first.Events...), second.Events...) {
		seen[e.Payload]++
	}

	for payload, n := range seen {
		if n > 1 {
			t.Errorf("keyset returned %q %d times", payload, n)
		}
	}

	if len(seen) != 10 {
		t.Errorf("two pages of 5 returned %d distinct rows, want 10", len(seen))
	}

	t.Log("the cursor names a position in the data, not a position in a result set, so a row " +
		"inserted above it changes nothing. The new row is simply not in this iteration, " +
		"which is the correct answer for a snapshot the client started before it existed.")
}

// TestNonUniqueKeysetLosesRows is the mistake that makes keyset pagination as broken as offset.
func TestNonUniqueKeysetLosesRows(t *testing.T) {
	tx := pgtest.Tx(t)
	ctx := context.Background()

	if _, err := tx.Exec(ctx, "TRUNCATE events"); err != nil {
		t.Fatal(err)
	}

	// Ten rows, all sharing ONE timestamp. Which is what a batch import looks like.
	ts := time.Date(2032, 1, 1, 0, 0, 0, 0, time.UTC)

	for i := range 10 {
		if _, err := tx.Exec(ctx, `
			INSERT INTO events (actor, kind, created_at, payload)
			VALUES ('batch', 'created', $1, $2)`, ts, "row "+itoa(i)); err != nil {
			t.Fatal(err)
		}
	}

	// The correct version walks all ten.
	seen := map[string]int{}
	cursor := ""

	for range 5 {
		page, err := Keyset(ctx, tx, 4, cursor)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range page.Events {
			seen[e.Payload]++
		}
		if !page.HasMore {
			break
		}
		cursor = page.Next
	}

	t.Logf("row comparison on (created_at, id): %d of 10 rows seen", len(seen))

	if len(seen) != 10 {
		t.Errorf("the correct keyset lost rows: saw %d of 10", len(seen))
	}
	for payload, n := range seen {
		if n != 1 {
			t.Errorf("%q seen %d times", payload, n)
		}
	}

	// The single-column version loses everything after the first page, because every remaining row
	// has the SAME timestamp and `created_at < $1` excludes them all.
	firstPage, err := KeysetSingleColumn(ctx, tx, 4, ts.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}

	next, err := KeysetSingleColumn(ctx, tx, 4, firstPage[len(firstPage)-1].CreatedAt)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("cursoring on created_at alone: page 1 returned %d rows, page 2 returned %d",
		len(firstPage), len(next))

	if len(next) != 0 {
		t.Errorf("expected the single-column cursor to return nothing on page 2, got %d", len(next))
	}

	t.Log("six of ten rows are unreachable, silently, and the API looks like it reached the end")

	// And the AND-of-two-columns version, which looks right and drops boundary rows.
	wrong, err := KeysetWrong(ctx, tx, 4, Cursor{
		CreatedAt: firstPage[len(firstPage)-1].CreatedAt,
		ID:        firstPage[len(firstPage)-1].ID,
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("created_at < $1 AND id < $2 returned %d rows where the row comparison returns 4",
		len(wrong))

	if len(wrong) != 0 {
		t.Errorf("expected the AND version to return nothing here, got %d", len(wrong))
	}
}

// TestCountIsTheExpensivePart measures why "page 7 of 2,500" is rare on large sites.
func TestCountIsTheExpensivePart(t *testing.T) {
	db := dataset(t)
	ctx := context.Background()

	exact, err := Count(ctx, db)
	if err != nil {
		t.Fatal(err)
	}

	estimate, err := CountEstimate(ctx, db)
	if err != nil {
		t.Fatal(err)
	}

	var jsonText string
	if err := db.QueryRow(ctx,
		"EXPLAIN (ANALYZE, FORMAT JSON) SELECT count(*) FROM events").Scan(&jsonText); err != nil {
		t.Fatal(err)
	}

	rows, execMS, shape := parseRowsRead(t, jsonText)

	t.Logf("exact count:    %d, reading %.0f rows in %.2fms (%s)", exact, rows, execMS, shape)
	t.Logf("reltuples:      %d, one row from pg_class", estimate)

	if exact != rowCount {
		t.Errorf("counted %d rows, want %d", exact, rowCount)
	}

	// The estimate is accurate to within a few percent on an analysed table, and that is the whole
	// argument: "about 50,000 results" costs nothing and is true.
	off := float64(estimate-exact) / float64(exact) * 100

	t.Logf("the estimate is %.2f%% off", off)

	if off > 5 || off < -5 {
		t.Errorf("the estimate is %.1f%% off, which is more than ANALYZE should leave", off)
	}

	if rows < float64(rowCount) {
		t.Errorf("count(*) read %.0f rows for a %d-row table; it has to read them all",
			rows, rowCount)
	}
}

// parseRowsRead pulls the rows actually read and the execution time out of an EXPLAIN JSON document.
//
// A tiny hand-rolled parse rather than importing the indexes package from database-concepts, for the same
// reason pgtest duplicates dbtest: cross-module dependencies in this repo need a tag.
func parseRowsRead(t *testing.T, jsonText string) (rows, execMS float64, shape string) {
	t.Helper()

	type plan struct {
		NodeType    string  `json:"Node Type"`
		IndexName   string  `json:"Index Name"`
		ActualRows  float64 `json:"Actual Rows"`
		ActualLoops float64 `json:"Actual Loops"`
		Plans       []plan  `json:"Plans"`
	}

	var docs []struct {
		Plan          plan    `json:"Plan"`
		ExecutionTime float64 `json:"Execution Time"`
	}

	if err := json.Unmarshal([]byte(jsonText), &docs); err != nil {
		t.Fatalf("parsing the plan: %v", err)
	}
	if len(docs) == 0 {
		t.Fatal("the plan document is empty")
	}

	// The deepest node's actual rows is what was READ; the root's is what was returned. Rows times
	// loops, because a node inside a nested loop reports per-loop figures.
	var walk func(p plan, depth int)
	var deepest float64
	var names []string

	walk = func(p plan, depth int) {
		label := p.NodeType
		if p.IndexName != "" {
			label += " (" + p.IndexName + ")"
		}
		names = append(names, label)

		loops := p.ActualLoops
		if loops == 0 {
			loops = 1
		}

		if read := p.ActualRows * loops; read > deepest {
			deepest = read
		}

		for _, child := range p.Plans {
			walk(child, depth+1)
		}
	}

	walk(docs[0].Plan, 0)

	return deepest, docs[0].ExecutionTime, strings.Join(names, " <- ")
}

func payloads(events []Event) []string {
	out := make([]string, len(events))
	for i, e := range events {
		out[i] = e.Payload
	}
	return out
}

// offsetIn is Offset scoped to one timestamp range, so a test can work on its own rows inside a
// transaction that shares the table with 50,000 others.
func offsetIn(ctx context.Context, q pgtest.Querier, limit, offset int, after time.Time) ([]Event, error) {
	rows, err := q.Query(ctx, `
		SELECT id, actor, kind, created_at, payload
		  FROM events WHERE created_at >= $3
		 ORDER BY created_at DESC, id DESC
		 LIMIT $1 OFFSET $2`, limit, offset, after)
	if err != nil {
		return nil, err
	}
	return collect(rows)
}

// skipDemo reads page 1, deletes a row from it, reads page 2, and reports the row neither page returned.
func skipDemo(ctx context.Context, q pgtest.Querier, after time.Time) (string, error) {
	first, err := offsetIn(ctx, q, 5, 0, after)
	if err != nil {
		return "", err
	}

	// Delete the newest row, so everything shifts up.
	if _, err := q.Exec(ctx, "DELETE FROM events WHERE id = $1", first[0].ID); err != nil {
		return "", err
	}

	second, err := offsetIn(ctx, q, 5, 5, after)
	if err != nil {
		return "", err
	}

	// Everything still in the table, in order.
	rows, err := q.Query(ctx, `
		SELECT id, actor, kind, created_at, payload
		  FROM events WHERE created_at >= $1
		 ORDER BY created_at DESC, id DESC LIMIT 10`, after)
	if err != nil {
		return "", err
	}

	all, err := collect(rows)
	if err != nil {
		return "", err
	}

	returned := map[string]bool{}
	for _, e := range append(append([]Event{}, first...), second...) {
		returned[e.Payload] = true
	}

	for _, e := range all {
		if !returned[e.Payload] {
			return e.Payload, nil
		}
	}

	return "", nil
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
