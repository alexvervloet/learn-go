package main

import (
	"fmt"
	"strconv"
	"strings"
)

// Where allocations come from
// ===========================
//
// In Go, reducing allocations is usually the largest remaining win after
// choosing the right algorithm. The garbage collector is fast, and the cheapest
// garbage is the object never created.
//
// The common sources, each with its fix, all measured in allocations_test.go.

// Source 1: a slice that grows
// ----------------------------
//
// append without a capacity reallocates roughly log2(n) times, copying
// everything each time. Lesson 02 measured 12 allocations for 1000 appends
// against 0 when pre-sized.

func collectGrowing(n int) []string {
	var out []string
	for i := 0; i < n; i++ {
		out = append(out, strconv.Itoa(i))
	}
	return out
}

func collectPresized(n int) []string {
	out := make([]string, 0, n) // one allocation
	for i := 0; i < n; i++ {
		out = append(out, strconv.Itoa(i))
	}
	return out
}

// Source 2: a map that grows
// --------------------------
//
// The same problem with worse constants: a map rehashes every key when it
// grows. make(map[K]V, n) sizes it once.

func indexGrowing(n int) map[int]string {
	m := make(map[int]string)
	for i := 0; i < n; i++ {
		m[i] = strconv.Itoa(i)
	}
	return m
}

func indexPresized(n int) map[int]string {
	m := make(map[int]string, n)
	for i := 0; i < n; i++ {
		m[i] = strconv.Itoa(i)
	}
	return m
}

// Source 3: string concatenation
// ------------------------------
//
// Strings are immutable, so `a + b` allocates a new one. In a loop that is
// quadratic in the total length.

func buildWithConcat(parts []string) string {
	out := ""
	for _, p := range parts {
		out += p
	}
	return out
}

func buildWithBuilder(parts []string) string {
	var sb strings.Builder

	total := 0
	for _, p := range parts {
		total += len(p)
	}
	sb.Grow(total) // one allocation

	for _, p := range parts {
		sb.WriteString(p)
	}
	return sb.String()
}

// Source 4: fmt
// -------------
//
// fmt is reflective and allocates. Lesson 12 measured a hand-written JSON
// encoder LOSING to encoding/json because it used fmt.Sprint, then winning by
// 1.3x after one substitution to strconv.
//
// In a hot path this is often the single biggest win available.

func formatWithFmt(values []int) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		out = append(out, fmt.Sprintf("%d", v))
	}
	return out
}

func formatWithStrconv(values []int) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		out = append(out, strconv.Itoa(v))
	}
	return out
}

// Source 5: boxing into an interface
// ----------------------------------
//
// Putting a value into an `any` allocates, because the interface needs a
// pointer to the data. This is why lesson 09's sync.Map write benchmark showed
// 2 allocs/op where the typed map showed 0, and why a generic function over a
// type set is faster than the `any` version.
//
// Small integers (0-255) are the exception: the runtime keeps a static array
// of them, so boxing one allocates nothing.

func sumViaAny(values []any) int {
	total := 0
	for _, v := range values {
		if n, ok := v.(int); ok {
			total += n
		}
	}
	return total
}

func boxValues(values []int) []any {
	out := make([]any, 0, len(values))
	for _, v := range values {
		out = append(out, v) // allocates, unless v is a small integer
	}
	return out
}

// Source 6: returning a pointer to a local
// ----------------------------------------
//
// A local whose address escapes must live on the heap. Lesson 18 is about
// nothing else; the summary is that returning &x moves x to the heap, and
// returning x by value usually does not.

type record struct {
	ID    int
	Name  string
	Score float64
}

// newRecordPointer escapes: the caller outlives the function.
func newRecordPointer(id int) *record {
	r := record{ID: id, Name: "record", Score: float64(id)}
	return &r // r must be heap-allocated
}

// newRecordValue does not escape for small structs: the copy goes in the
// caller's frame.
func newRecordValue(id int) record {
	return record{ID: id, Name: "record", Score: float64(id)}
}

// Source 7: a byte-slice / string round trip
// -------------------------------------------
//
// []byte(s) and string(b) both COPY, because one is mutable and the other is
// not. In a loop that is an allocation per conversion.
//
// The compiler elides some of these: string(b) used as a map key, or a
// comparison, does not allocate. Relying on that is fragile; measuring is not.

func countWithConversion(data []byte, target string) int {
	count := 0
	for i := 0; i+len(target) <= len(data); i++ {
		if string(data[i:i+len(target)]) == target { // the compiler elides this one
			count++
		}
	}
	return count
}

func countWithoutConversion(data []byte, target []byte) int {
	count := 0
	for i := 0; i+len(target) <= len(data); i++ {
		match := true
		for j := range target {
			if data[i+j] != target[j] {
				match = false
				break
			}
		}
		if match {
			count++
		}
	}
	return count
}

// allocationSources is the checklist.
func allocationSources() []string {
	return []string{
		"a slice that grows: make([]T, 0, n) when n is known",
		"a map that grows: make(map[K]V, n)",
		"string concatenation in a loop: strings.Builder with Grow",
		"fmt in a hot path: strconv.Format* instead",
		"boxing into `any`: generics or a concrete type",
		"returning a pointer to a local: return the value when it is small",
		"[]byte / string conversions: work in one representation",
	}
}

// theOptimisationOrder is the part that matters more than the list.
func theOptimisationOrder() []string {
	return []string{
		"1. MEASURE: a profile, not a hunch",
		"2. a better algorithm: O(n^2) -> O(n log n) beats any constant factor",
		"3. fewer allocations: usually the largest remaining win in Go",
		"4. micro-optimisations, and measure again, because they often lose",
	}
}

// demoAllocations prints the pairs producing identical output.
func demoAllocations() {
	const n = 1000

	fmt.Printf("  each pair below produces identical output, so the comparison is honest:\n")

	growing, presized := collectGrowing(n), collectPresized(n)
	fmt.Printf("    slice:  %d == %d elements: %t\n", len(growing), len(presized), len(growing) == len(presized))

	mapGrowing, mapPresized := indexGrowing(n), indexPresized(n)
	fmt.Printf("    map:    %d == %d entries:  %t\n", len(mapGrowing), len(mapPresized),
		len(mapGrowing) == len(mapPresized))

	parts := []string{"alpha", "beta", "gamma"}
	fmt.Printf("    string: %q == %q: %t\n", buildWithConcat(parts), buildWithBuilder(parts),
		buildWithConcat(parts) == buildWithBuilder(parts))

	values := []int{1, 22, 333}
	withFmt, withStrconv := formatWithFmt(values), formatWithStrconv(values)
	fmt.Printf("    format: %v == %v: %t\n", withFmt, withStrconv,
		strings.Join(withFmt, ",") == strings.Join(withStrconv, ","))

	boxed := boxValues([]int{1, 2, 3})
	fmt.Printf("    boxing: sum via any = %d\n", sumViaAny(boxed))

	data := []byte("abcabcabc")
	fmt.Printf("    bytes:  %d == %d matches: %t\n",
		countWithConversion(data, "abc"), countWithoutConversion(data, []byte("abc")),
		countWithConversion(data, "abc") == countWithoutConversion(data, []byte("abc")))

	fmt.Println("\n  where allocations come from:")
	for _, s := range allocationSources() {
		fmt.Printf("    %s\n", s)
	}

	fmt.Println("\n  the optimisation order:")
	for _, s := range theOptimisationOrder() {
		fmt.Printf("    %s\n", s)
	}
	fmt.Println("    run `go test -bench . -benchmem -run '^$' ./17-benchmarks-and-pprof` for the numbers")
}
