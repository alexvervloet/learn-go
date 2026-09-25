// Package main is lesson 17 of go-concepts: benchmarks and pprof.
//
//	func BenchmarkX(b *testing.B) {
//	    setup()            // not measured: b.Loop excludes it
//	    for b.Loop() {
//	        work()
//	    }
//	}
//
// b.Loop (Go 1.24) replaced `for i := 0; i < b.N; i++` and is better in two
// ways that matter: the compiler treats it as opaque so the body cannot be
// optimised away, and setup before the loop is excluded automatically, so
// b.ResetTimer is usually unnecessary.
package main

import (
	"fmt"
	"sort"
	"strings"
)

// The functions below exist to be benchmarked. They are deliberately simple:
// the lesson is the measurement, not the algorithm.

// sumSlice is the baseline: one pass, no allocation.
func sumSlice(values []int) int {
	total := 0
	for _, v := range values {
		total += v
	}
	return total
}

// joinWithPlus builds a string by concatenation, which allocates a new string
// per iteration and is quadratic in the total length.
func joinWithPlus(parts []string) string {
	out := ""
	for _, p := range parts {
		out += p // a new string every time
	}
	return out
}

// joinWithBuilder uses strings.Builder, which grows one buffer.
func joinWithBuilder(parts []string) string {
	var sb strings.Builder
	for _, p := range parts {
		sb.WriteString(p)
	}
	return sb.String()
}

// joinWithBuilderPresized tells the Builder the final size up front, so it
// allocates once rather than growing.
func joinWithBuilderPresized(parts []string) string {
	total := 0
	for _, p := range parts {
		total += len(p)
	}

	var sb strings.Builder
	sb.Grow(total)
	for _, p := range parts {
		sb.WriteString(p)
	}
	return sb.String()
}

// joinWithSeparator does what strings.Join actually does: it handles a
// separator. The four functions above do not, and that turned out to matter.
//
// THE COMPARISON ABOVE IS NOT FAIR, and finding that out is the most useful
// thing in this lesson. Benchmarking joinWithBuilderPresized against
// strings.Join showed 374 ns against 716 ns, a 1.9x win for the hand-written
// version, and the agreement test passed because with an empty separator the
// OUTPUT is identical.
//
// The output was identical and the work was not: strings.Join runs a separator
// branch on every element and the presized builder does not. Comparing like
// with like closes most of the gap, to about 1.1-1.2x.
//
// This is lie number one from lies.go, in the lesson that documents lie number
// one. See LESSONS.md.
func joinWithSeparator(parts []string, sep string) string {
	total := len(sep) * max(0, len(parts)-1)
	for _, p := range parts {
		total += len(p)
	}

	var sb strings.Builder
	sb.Grow(total)

	for i, p := range parts {
		if i > 0 {
			sb.WriteString(sep)
		}
		sb.WriteString(p)
	}
	return sb.String()
}

// joinWithStdlib is what you should actually use. It is not the fastest
// possible implementation, and the margin is small enough that writing your
// own is rarely worth the code.
func joinWithStdlib(parts []string) string {
	return strings.Join(parts, "")
}

// searchLinear is O(n).
func searchLinear(values []int, target int) int {
	for i, v := range values {
		if v == target {
			return i
		}
	}
	return -1
}

// searchBinary is O(log n) and requires sorted input, which is the trade every
// "use a better algorithm" decision actually is.
func searchBinary(sorted []int, target int) int {
	i := sort.SearchInts(sorted, target)
	if i < len(sorted) && sorted[i] == target {
		return i
	}
	return -1
}

// searchMap is O(1) and costs memory plus a build step, which is the other
// common trade.
func searchMap(index map[int]int, target int) int {
	if i, ok := index[target]; ok {
		return i
	}
	return -1
}

// buildIndex is the cost searchMap amortises. A benchmark comparing a map
// lookup against a linear scan WITHOUT counting this is one of the ways a
// benchmark lies.
func buildIndex(values []int) map[int]int {
	index := make(map[int]int, len(values))
	for i, v := range values {
		index[v] = i
	}
	return index
}

// benchmarkShapes documents the forms worth knowing.
func benchmarkShapes() map[string]string {
	return map[string]string{
		"for b.Loop()":             "the modern form: opaque to the optimiser, excludes setup",
		"for i := 0; i < b.N; i++": "the old form; still works, needs b.ResetTimer after setup",
		"b.Run(name, fn)":          "sub-benchmarks, for a table of input sizes",
		"b.RunParallel(fn)":        "measures under contention, across GOMAXPROCS goroutines",
		"b.ReportAllocs()":         "force allocation reporting without the -benchmem flag",
		"b.ReportMetric(v, u)":     "report a custom metric, such as items/sec",
		"b.StopTimer/StartTimer":   "exclude per-iteration setup; expensive, so prefer restructuring",
		"b.Cleanup(fn)":            "tear down after the benchmark, like t.Cleanup",
	}
}

// flagsWorthKnowing for running them.
func flagsWorthKnowing() map[string]string {
	return map[string]string{
		"-bench .":           "run every benchmark; the regexp is required, and . means all",
		"-bench X -run '^$'": "run benchmark X and NO tests, which is usually what you want",
		"-benchmem":          "report B/op and allocs/op; not optional",
		"-benchtime=5s":      "run each for at least 5 seconds",
		"-benchtime=100x":    "run each exactly 100 times",
		"-count=10":          "10 samples per benchmark, for benchstat",
		"-cpu=1,4,12":        "run at several GOMAXPROCS values",
	}
}

// demoWriting prints the benchmark reference and shows the functions agreeing.
func demoWriting() {
	parts := []string{"alpha", "beta", "gamma", "delta"}

	results := []string{
		joinWithPlus(parts),
		joinWithBuilder(parts),
		joinWithBuilderPresized(parts),
		joinWithStdlib(parts),
	}

	allAgree := true
	for _, r := range results[1:] {
		if r != results[0] {
			allAgree = false
		}
	}
	fmt.Printf("  four join implementations, identical output: %t (%q)\n", allAgree, results[0])
	fmt.Println("    ...a benchmark comparing implementations that DISAGREE measures nothing")

	values := make([]int, 1000)
	for i := range values {
		values[i] = i
	}
	index := buildIndex(values)

	fmt.Printf("\n  three searches for 750 in 1000 values: linear=%d binary=%d map=%d\n",
		searchLinear(values, 750), searchBinary(values, 750), searchMap(index, 750))
	fmt.Println("    ...the map is O(1) and the index build is a cost the benchmark must count")

	fmt.Printf("\n  sumSlice over 1000 values: %d\n", sumSlice(values))

	fmt.Println("\n  benchmark shapes:")
	for _, k := range []string{
		"for b.Loop()", "for i := 0; i < b.N; i++", "b.Run(name, fn)", "b.RunParallel(fn)",
		"b.ReportAllocs()", "b.ReportMetric(v, u)", "b.StopTimer/StartTimer", "b.Cleanup(fn)",
	} {
		fmt.Printf("    %-26s %s\n", k, benchmarkShapes()[k])
	}

	fmt.Println("\n  flags:")
	for _, k := range []string{
		"-bench .", "-bench X -run '^$'", "-benchmem", "-benchtime=5s",
		"-benchtime=100x", "-count=10", "-cpu=1,4,12",
	} {
		fmt.Printf("    %-20s %s\n", k, flagsWorthKnowing()[k])
	}
}
