package golden

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// update is the flag that regenerates the golden files.
//
// Declared at package level with flag.Bool, which works because `go test` parses flags it does
// not recognise into the test binary's flag set. The convention is exactly this name, so
// `go test ./... -update` works across a repository.
//
// The risk is real: a flag that rewrites the test's own expectations makes accepting a regression
// a single command. The discipline is that its output goes in a diff and gets read.
var update = flag.Bool("update", false, "rewrite the .golden files in testdata/")

// assertGolden compares got against testdata/<name>.golden, or rewrites it under -update.
//
// The helper is fifteen lines and every Go repository has one. Four details matter:
//
//	t.Helper(), so a mismatch points at the test rather than at this function
//	os.MkdirAll, so the first run on a new test does not fail on a missing directory
//	a byte comparison, not a string one, so a trailing-newline difference is visible
//	the failure prints a DIFF rather than both documents, because a 40-line expected and a
//	  40-line actual side by side is unreadable
func assertGolden(t *testing.T, name, got string) {
	t.Helper()

	path := filepath.Join("testdata", name+".golden")

	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatalf("creating testdata: %v", err)
		}
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatalf("writing %s: %v", path, err)
		}

		t.Logf("updated %s", path)
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			t.Fatalf("%s does not exist; run `go test ./golden -update` to create it", path)
		}
		t.Fatalf("reading %s: %v", path, err)
	}

	if got == string(want) {
		return
	}

	t.Errorf("output does not match %s\n%s", path, diff(string(want), got))
}

// diff renders a line-by-line comparison, which is what a failure needs.
//
// Deliberately not a real diff algorithm: a first-difference report is enough to find the problem
// and is twenty lines rather than a dependency. When the output is long enough that this is not
// enough, the answer is `go test -update` and `git diff`, which is a real diff.
func diff(want, got string) string {
	wantLines := strings.Split(want, "\n")
	gotLines := strings.Split(got, "\n")

	var sb strings.Builder

	for i := 0; i < max(len(wantLines), len(gotLines)); i++ {
		var w, g string
		if i < len(wantLines) {
			w = wantLines[i]
		}
		if i < len(gotLines) {
			g = gotLines[i]
		}

		if w == g {
			continue
		}

		sb.WriteString("first difference at line ")
		sb.WriteString(itoa(i + 1))
		sb.WriteString(":\n")
		sb.WriteString("  want: " + quote(w) + "\n")
		sb.WriteString("  got:  " + quote(g) + "\n")

		if len(wantLines) != len(gotLines) {
			sb.WriteString("  (want has " + itoa(len(wantLines)) +
				" lines, got has " + itoa(len(gotLines)) + ")\n")
		}

		return sb.String()
	}

	return "the documents differ but no line does, which means trailing whitespace"
}

func quote(s string) string { return `"` + s + `"` }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var out []byte
	for n > 0 {
		out = append([]byte{byte('0' + n%10)}, out...)
		n /= 10
	}
	return string(out)
}

// A fixed time, so the render is deterministic. Using time.Now() here is the other classic way to
// make a golden test flaky.
var issued = time.Date(2026, 3, 14, 9, 30, 0, 0, time.UTC)

func TestRenderGolden(t *testing.T) {
	tests := []struct {
		name string
		inv  Invoice
	}{
		{
			name: "simple",
			inv: Invoice{
				Number:   "INV-001",
				Customer: "Acme Ltd",
				IssuedAt: issued,
				Lines: []Line{
					{Description: "Widget", Quantity: 2, UnitCents: 1250},
					{Description: "Gadget", Quantity: 1, UnitCents: 9999},
				},
			},
		},
		{
			name: "with notes",
			inv: Invoice{
				Number:   "INV-002",
				Customer: "Globex",
				IssuedAt: issued,
				Lines: []Line{
					{Description: "Consulting", Quantity: 10, UnitCents: 15000},
				},
				// Deliberately out of alphabetical order, to exercise the sort.
				Notes: map[string]string{
					"terms":     "net 30",
					"reference": "PO-4471",
					"contact":   "ada@globex.example",
				},
			},
		},
		{
			name: "long descriptions widen the table",
			inv: Invoice{
				Number:   "INV-003",
				Customer: "Initech",
				IssuedAt: issued,
				Lines: []Line{
					{Description: "A description long enough to widen the column", Quantity: 1, UnitCents: 100},
					{Description: "Short", Quantity: 3, UnitCents: 50},
				},
			},
		},
		{
			name: "empty",
			inv: Invoice{
				Number:   "INV-004",
				Customer: "Nobody",
				IssuedAt: issued,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// The subtest name becomes the file name, so the mapping is obvious and
			// spaces become underscores.
			assertGolden(t, strings.ReplaceAll(tt.name, " ", "_"), Render(tt.inv))
		})
	}
}

// TestRenderIsDeterministic is the test that keeps the golden files from going flaky, and it is
// the one people forget to write.
//
// Rendering the same invoice a hundred times must produce identical output. With an unsorted map
// this fails most runs, and the failure is far more informative than a golden mismatch: it names
// determinism as the problem rather than looking like a content change.
func TestRenderIsDeterministic(t *testing.T) {
	inv := Invoice{
		Number:   "INV-100",
		Customer: "Test",
		IssuedAt: issued,
		Lines:    []Line{{Description: "Item", Quantity: 1, UnitCents: 100}},
		Notes: map[string]string{
			"a": "1", "b": "2", "c": "3", "d": "4", "e": "5",
			"f": "6", "g": "7", "h": "8", "i": "9", "j": "10",
		},
	}

	first := Render(inv)

	for i := range 100 {
		if got := Render(inv); got != first {
			t.Fatalf("render %d differs from the first; the output is not deterministic\n%s",
				i, diff(first, got))
		}
	}
}

// TestRenderNormalisesTheTimezone: the same instant in two zones must render identically, because
// a golden file generated on a developer's laptop has to match one generated in CI.
func TestRenderNormalisesTheTimezone(t *testing.T) {
	utc := issued
	tokyo := issued.In(time.FixedZone("JST", 9*3600))

	inv := func(at time.Time) Invoice {
		return Invoice{Number: "INV-1", Customer: "X", IssuedAt: at}
	}

	if Render(inv(utc)) != Render(inv(tokyo)) {
		t.Error("the same instant rendered differently in two zones; Format must use UTC")
	}
}

// TestGoldenFilesExist is a guard against the failure mode where someone commits a test and
// forgets the testdata, so CI fails with "run -update" and nobody can tell whether that is the
// right answer.
func TestGoldenFilesExist(t *testing.T) {
	entries, err := os.ReadDir("testdata")
	if err != nil {
		t.Fatalf("testdata is missing: %v", err)
	}

	count := 0
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".golden") {
			count++
		}
	}

	if count == 0 {
		t.Error("no .golden files in testdata; run `go test ./golden -update`")
	}
	t.Logf("%d golden files", count)
}
