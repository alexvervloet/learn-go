package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Reading escape analysis
// =======================
//
//	go build -gcflags=-m ./...        one level
//	go build -gcflags='-m -m' ./...   why, with the flow
//	go build -gcflags=-l ./...        inlining OFF, to see its effect
//
// Two messages that read alike and mean different things:
//
//	"X escapes to heap"   the VALUE at that expression is heap-allocated
//	"moved to heap: x"    the VARIABLE x would have been on the stack, and
//	                      something took its address in a way that outlives
//	                      the frame
//
// "does not escape" is the one you are looking for, and it appears for
// parameters rather than for locals, which trips people up: a local that stays
// on the stack is simply not mentioned.

// EscapeDecision is one line of the compiler's output, parsed.
type EscapeDecision struct {
	File    string
	Line    int
	Message string
	Kind    string // "escapes", "moved", "does-not-escape", "inline"
}

// escapeLine matches the compiler's format: file:line:col: message.
var escapeLine = regexp.MustCompile(`^(.+?):(\d+):\d+: (.+)$`)

// AnalyseEscapes runs the compiler on a package and parses its decisions.
//
// Shelling out to the toolchain is the honest way to do this: there is no API,
// and the output format is not guaranteed stable, which is worth knowing
// before building anything on top of it.
func AnalyseEscapes(packageDir string) (decisions []EscapeDecision, err error) {
	cmd := exec.Command("go", "build", "-o", os.DevNull, "-gcflags=-m", ".")
	cmd.Dir = packageDir

	// The decisions go to STDERR, not stdout, which is the first thing to get
	// wrong when scripting this.
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("go build -gcflags=-m in %s: %w\n%s", packageDir, err, out)
	}

	for _, line := range strings.Split(string(out), "\n") {
		m := escapeLine.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}

		message := m[3]

		var kind string
		switch {
		case strings.Contains(message, "escapes to heap"):
			kind = "escapes"
		case strings.HasPrefix(message, "moved to heap:"):
			kind = "moved"
		case strings.Contains(message, "does not escape"):
			kind = "does-not-escape"
		case strings.HasPrefix(message, "can inline"), strings.HasPrefix(message, "inlining call"):
			kind = "inline"
		default:
			continue
		}

		decisions = append(decisions, EscapeDecision{
			File:    filepath.Base(m[1]),
			Line:    atoi(m[2]),
			Message: message,
			Kind:    kind,
		})
	}

	return decisions, nil
}

// atoi is a small helper; the regexp guarantees digits, so an error is
// impossible and ignoring it is honest rather than lazy.
func atoi(s string) int {
	n := 0
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return n
}

// countByKind summarises a set of decisions.
func countByKind(decisions []EscapeDecision) map[string]int {
	out := make(map[string]int)
	for _, d := range decisions {
		out[d.Kind]++
	}
	return out
}

// decisionsFor returns the decisions in one file, sorted by line.
func decisionsFor(decisions []EscapeDecision, file string) []EscapeDecision {
	var out []EscapeDecision
	for _, d := range decisions {
		if d.File == file {
			out = append(out, d)
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Line < out[j].Line })
	return out
}

// theTwoMessages explains the distinction, because it is the one that makes
// the output readable.
func theTwoMessages() []string {
	return []string{
		"\"X escapes to heap\":  the VALUE at that expression is heap-allocated",
		"\"moved to heap: x\":   the VARIABLE x was going to be on the stack and something",
		"                      took its address in a way that outlives the frame",
		"\"does not escape\":    reported for PARAMETERS; a local that stays put is not mentioned",
		"so the absence of a message about a local is the good outcome",
	}
}

// inliningChangesEverything is the point that took this lesson a correction to
// learn, and the reason escape analysis cannot be reasoned about per-function.
func inliningChangesEverything() []string {
	return []string{
		"escape analysis runs AFTER inlining, so it sees the caller's actual arguments",
		"make([]byte, n) in an inlined function stays on the stack when n is provably small",
		"the same function called with a large n escapes, at that call site only",
		"so \"does this function allocate?\" has no answer; \"does this CALL allocate?\" does",
		"go build -gcflags=-l turns inlining off and makes both cases escape",
		"testing.AllocsPerRun measures the call site, which is the question that matters",
	}
}

// flagsForAnalysis is the reference.
func flagsForAnalysis() map[string]string {
	return map[string]string{
		"-gcflags=-m":                       "escape and inlining decisions, one level",
		"-gcflags='-m -m'":                  "the same with the flow: why a value escaped",
		"-gcflags=-l":                       "disable inlining, to see what it was buying",
		"-gcflags='-m -l'":                  "escape decisions without inlining muddying them",
		"-gcflags=-S":                       "the generated assembly",
		"-gcflags=-d=ssa/check_bce/debug=1": "report bounds-check eliminations",
	}
}

// demoAnalysis runs the compiler on this very package and reports.
func demoAnalysis() {
	fmt.Println("  the two messages:")
	for _, s := range theTwoMessages() {
		fmt.Printf("    %s\n", s)
	}

	dir, err := os.Getwd()
	if err != nil {
		fmt.Printf("\n  could not determine the working directory: %v\n", err)
		return
	}

	// When run via `go run ./18-...`, the working directory is the module
	// root, so point at this package explicitly.
	pkgDir := dir
	if filepath.Base(dir) != "18-memory-and-escape-analysis" {
		pkgDir = filepath.Join(dir, "18-memory-and-escape-analysis")
	}

	decisions, err := AnalyseEscapes(pkgDir)
	if err != nil {
		fmt.Printf("\n  analysis unavailable: %v\n", err)
		return
	}

	counts := countByKind(decisions)
	fmt.Printf("\n  this package, analysed by the compiler just now:\n")
	for _, kind := range []string{"escapes", "moved", "does-not-escape", "inline"} {
		fmt.Printf("    %-16s %d\n", kind, counts[kind])
	}

	fmt.Printf("\n  the decisions in escapes.go, in order:\n")
	shown := 0
	for _, d := range decisionsFor(decisions, "escapes.go") {
		if d.Kind == "inline" {
			continue
		}
		fmt.Printf("    line %-4d %s\n", d.Line, d.Message)
		shown++
		if shown >= 12 {
			fmt.Printf("    ... and more\n")
			break
		}
	}

	fmt.Println("\n  inlining changes everything:")
	for _, s := range inliningChangesEverything() {
		fmt.Printf("    %s\n", s)
	}

	fmt.Println("\n  the flags:")
	for _, k := range []string{
		"-gcflags=-m", "-gcflags='-m -m'", "-gcflags=-l", "-gcflags='-m -l'",
		"-gcflags=-S", "-gcflags=-d=ssa/check_bce/debug=1",
	} {
		fmt.Printf("    %-36s %s\n", k, flagsForAnalysis()[k])
	}
}
