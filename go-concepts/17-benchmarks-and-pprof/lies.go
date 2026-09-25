package main

import (
	"fmt"
	"strings"
)

// The three ways a benchmark lies
// ===============================
//
// Every one of these has happened while building this repository, and the
// write-ups are in LESSONS.md. They are not hypothetical failure modes.

// Lie 1: the two sides do different work
// --------------------------------------
//
// From LESSONS.md: an `appendGrowing` vs `preallocated` pair where the growing
// side also appended to a SECOND slice recording capacity after each step. It
// was doing roughly twice the work, and the comparison overstated the penalty.
//
// The same shape appeared again with a channel counter whose buffered channel
// made Inc asynchronous: the benchmark was timing an ENQUEUE against an
// INCREMENT, and removing the buffer moved the number from 290ns to 507ns.
//
// The defence is a test asserting the two produce identical results, sitting
// next to the benchmark.

// growUnbounded appends without pre-sizing.
func growUnbounded(n int) []int {
	var out []int
	for i := 0; i < n; i++ {
		out = append(out, i)
	}
	return out
}

// growPresized does the same work with the size known.
func growPresized(n int) []int {
	out := make([]int, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, i)
	}
	return out
}

// growUnboundedWithExtraWork is the MISTAKE: identical apart from the second
// slice, which makes the pair incomparable. It exists so the test can show the
// two produce different work while producing the same primary output, which is
// exactly why the bug survives a casual check.
func growUnboundedWithExtraWork(n int) (values []int, caps []int) {
	var out []int
	for i := 0; i < n; i++ {
		out = append(out, i)
		caps = append(caps, cap(out)) // extra work the other side does not do
	}
	return out, caps
}

// Lie 2: the compiler deletes the work
// ------------------------------------
//
// A result nobody uses can be eliminated entirely, giving an impossible number.
// The classic symptom is a sub-nanosecond ns/op, which is faster than a single
// memory access and therefore not real.
//
// Three defences:
//
//	assign to a package-level variable (the sink below)
//	use b.Loop, which the compiler treats as opaque
//	return the value from a function the benchmark calls
//
// sink prevents elimination. Package-level, so the compiler cannot prove
// nobody reads it.
// These three are used by the demos in this package as well as by benchmarks.
// The test-only sinks live in sinks_test.go, because a variable written only
// from _test.go files is genuinely unused in the package and staticcheck's
// `unused` check says so, correctly.
var (
	sinkInt    int
	sinkString string
	sinkSlice  []int
)

// expensiveComputation is pure and its result is easy to eliminate.
func expensiveComputation(n int) int {
	total := 0
	for i := 0; i < n; i++ {
		total += i * i % 7
	}
	return total
}

// Lie 3: one sample is not a measurement
// --------------------------------------
//
// Machines are noisy: another process, thermal throttling, a garbage collection
// landing differently. A single run reporting "8% faster" is usually reporting
// nothing.
//
//	go test -bench . -count=10 > old.txt
//	# change the code
//	go test -bench . -count=10 > new.txt
//	benchstat old.txt new.txt
//
// benchstat reports a median and a confidence interval, and says "~" when the
// difference is not statistically significant. That word is what turns a
// number into a result.

// benchstatWorkflow is the procedure, because it is worth having in your
// fingers rather than looking up.
func benchstatWorkflow() []string {
	return []string{
		"go install golang.org/x/perf/cmd/benchstat@latest",
		"go test -bench . -count=10 > old.txt",
		"# make the change",
		"go test -bench . -count=10 > new.txt",
		"benchstat old.txt new.txt",
		"",
		"it prints a median, a confidence interval, and \"~\" when the change is noise",
	}
}

// theThreeLies is the summary.
func theThreeLies() []string {
	return []string{
		"1. the two sides do different work -> assert they produce identical output, in a test",
		"2. the compiler deleted the work   -> a sub-nanosecond result is not real; use b.Loop or a sink",
		"3. one sample is not a measurement -> -count=10 and benchstat, or it is noise",
	}
}

// warningSigns are the numbers that should make you suspicious.
func warningSigns() map[string]string {
	return map[string]string{
		"under 1 ns/op":                     "faster than a memory access: the work was optimised away",
		"0 allocs/op unexpectedly":          "escape analysis kept it on the stack, OR the work was eliminated",
		"a 100x improvement":                "usually a measurement change rather than a code change",
		"results that vary 2x between runs": "the machine is too noisy to conclude anything",
		"faster after adding a buffer":      "the operation probably became asynchronous; check it still completes",
	}
}

// demoLies prints each failure mode.
func demoLies() {
	const n = 1000

	unbounded := growUnbounded(n)
	presized := growPresized(n)
	withExtra, caps := growUnboundedWithExtraWork(n)

	fmt.Printf("  lie 1, unequal work:\n")
	fmt.Printf("    growUnbounded and growPresized produce identical output: %t\n",
		len(unbounded) == len(presized))
	fmt.Printf("    growUnboundedWithExtraWork produces the SAME primary output: %t\n",
		len(withExtra) == len(unbounded))
	fmt.Printf("    ...and %d extra appends the other side never does\n", len(caps))
	fmt.Println("    the defence: a test asserting identical results, next to the benchmark")

	fmt.Printf("\n  lie 2, eliminated work:\n")
	sinkInt = expensiveComputation(1000)
	fmt.Printf("    a pure function whose result is discarded can be removed entirely\n")
	fmt.Printf("    assigned to a package-level sink instead: %d\n", sinkInt)
	fmt.Println("    the tell: a result under 1 ns/op, which is faster than a memory access")

	fmt.Printf("\n  lie 3, one sample:\n")
	for _, line := range benchstatWorkflow() {
		fmt.Printf("    %s\n", line)
	}

	fmt.Println("\n  the three lies:")
	for _, s := range theThreeLies() {
		fmt.Printf("    %s\n", s)
	}

	fmt.Println("\n  warning signs in the output:")
	for _, k := range []string{
		"under 1 ns/op", "0 allocs/op unexpectedly", "a 100x improvement",
		"results that vary 2x between runs", "faster after adding a buffer",
	} {
		fmt.Printf("    %-34s %s\n", k, warningSigns()[k])
	}

	// Keep the sinks alive so the compiler cannot decide they are unused.
	sinkString = strings.Repeat("x", 1)
	sinkSlice = presized
	_ = sinkString
	_ = sinkSlice
}
