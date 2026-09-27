// Package golden is the code under test for golden-file testing.
//
// # What a golden file is for
//
// A test whose expected output is thirty lines of formatted text does not want those thirty lines
// inside a Go string literal. It wants them in a file, and it wants a way to regenerate the file
// when the format changes on purpose.
//
//	go test ./golden             compares against testdata/*.golden
//	go test ./golden -update     rewrites them
//
// The -update flag is the whole technique, and it is also its risk: a flag that makes the test
// rewrite its own expectations makes it trivially easy to accept a regression. The discipline is
// that -update output goes in a diff and gets read, which is why the files live in the repository.
//
// # When to use one
//
//	yes   generated text: reports, SQL, templates, CLI help, formatted errors
//	yes   anything where the DIFF is what a reviewer needs to see
//	no    a value with three fields; a struct literal is clearer
//	no    anything non-deterministic, unless it is normalised first
//
// That last one is what kills most golden tests: a timestamp, a map iteration order, or an
// absolute path in the output makes the file differ on every run and the test gets deleted. Every
// one of those has to be normalised before comparison, and Render below does it.
package golden

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// Invoice is the thing being rendered.
type Invoice struct {
	Number   string
	Customer string
	IssuedAt time.Time
	Lines    []Line
	Notes    map[string]string
}

// Line is one invoice line.
type Line struct {
	Description string
	Quantity    int
	UnitCents   int
}

// Total returns the line's total in cents.
func (l Line) Total() int { return l.Quantity * l.UnitCents }

// Render formats an invoice as plain text.
//
// Three decisions here exist entirely so the output can be golden-tested:
//
//	the map is rendered in SORTED key order, because Go's map iteration is random and an
//	  unsorted render differs between runs
//	the time is formatted with a fixed layout in UTC, because a local timezone makes the
//	  output machine-dependent
//	the column widths are computed, so adding a long description changes the whole table and
//	  the diff shows it
//
// The first two are the ones people miss, and the symptom is a golden test that fails one run in
// three and gets marked flaky.
func Render(inv Invoice) string {
	var sb strings.Builder

	fmt.Fprintf(&sb, "INVOICE %s\n", inv.Number)
	fmt.Fprintf(&sb, "Customer: %s\n", inv.Customer)

	// UTC and a fixed layout. time.Time's default String() includes a monotonic clock
	// reading, which differs on every run and is the classic golden-file trap.
	fmt.Fprintf(&sb, "Issued:   %s\n", inv.IssuedAt.UTC().Format("2006-01-02"))
	sb.WriteString("\n")

	// Compute the description column width so the table lines up whatever the input.
	width := len("Description")
	for _, l := range inv.Lines {
		width = max(width, len(l.Description))
	}

	fmt.Fprintf(&sb, "%-*s  %5s  %10s  %10s\n", width, "Description", "Qty", "Unit", "Total")
	sb.WriteString(strings.Repeat("-", width+2+5+2+10+2+10) + "\n")

	total := 0
	for _, l := range inv.Lines {
		fmt.Fprintf(&sb, "%-*s  %5d  %10s  %10s\n",
			width, l.Description, l.Quantity, money(l.UnitCents), money(l.Total()))
		total += l.Total()
	}

	sb.WriteString(strings.Repeat("-", width+2+5+2+10+2+10) + "\n")
	fmt.Fprintf(&sb, "%-*s  %5s  %10s  %10s\n", width, "", "", "", money(total))

	if len(inv.Notes) > 0 {
		sb.WriteString("\nNotes:\n")

		// SORTED, because map iteration order is random and this is the single most
		// common reason a golden test is flaky.
		keys := make([]string, 0, len(inv.Notes))
		for k := range inv.Notes {
			keys = append(keys, k)
		}
		slices.Sort(keys)

		for _, k := range keys {
			fmt.Fprintf(&sb, "  %s: %s\n", k, inv.Notes[k])
		}
	}

	return sb.String()
}

// money formats cents as a decimal amount.
func money(cents int) string {
	sign := ""
	if cents < 0 {
		sign, cents = "-", -cents
	}
	return fmt.Sprintf("%s%d.%02d", sign, cents/100, cents%100)
}
