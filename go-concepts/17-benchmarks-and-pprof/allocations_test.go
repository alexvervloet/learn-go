package main

import (
	"slices"
	"strconv"
	"testing"
)

// Every pair below is guarded by an agreement test, because that is the
// defence this lesson documents.

func TestCollectImplementationsAgree(t *testing.T) {
	for _, n := range []int{0, 1, 100} {
		if !slices.Equal(collectGrowing(n), collectPresized(n)) {
			t.Errorf("n=%d: the collect pair disagrees", n)
		}
	}
}

func TestIndexImplementationsAgree(t *testing.T) {
	const n = 100

	growing, presized := indexGrowing(n), indexPresized(n)

	if len(growing) != len(presized) {
		t.Fatalf("lengths differ: %d and %d", len(growing), len(presized))
	}
	for k, v := range growing {
		if presized[k] != v {
			t.Errorf("key %d: %q vs %q", k, v, presized[k])
		}
	}
}

func TestBuildImplementationsAgree(t *testing.T) {
	inputs := [][]string{nil, {"a"}, {"alpha", "beta", "gamma"}}

	for _, parts := range inputs {
		if buildWithConcat(parts) != buildWithBuilder(parts) {
			t.Errorf("%v: the build pair disagrees", parts)
		}
	}
}

func TestFormatImplementationsAgree(t *testing.T) {
	values := []int{0, 1, -1, 1000, -1000}

	if !slices.Equal(formatWithFmt(values), formatWithStrconv(values)) {
		t.Error("the format pair disagrees")
	}
}

func TestCountImplementationsAgree(t *testing.T) {
	tests := []struct {
		data, target string
	}{
		{"abcabcabc", "abc"},
		{"aaaa", "aa"},
		{"none", "xyz"},
		{"", "x"},
	}

	for _, tt := range tests {
		withConv := countWithConversion([]byte(tt.data), tt.target)
		without := countWithoutConversion([]byte(tt.data), []byte(tt.target))

		if withConv != without {
			t.Errorf("%q in %q: %d vs %d", tt.target, tt.data, withConv, without)
		}
	}
}

func TestRecordConstructorsAgree(t *testing.T) {
	ptr := newRecordPointer(7)
	val := newRecordValue(7)

	if *ptr != val {
		t.Errorf("%+v != %+v", *ptr, val)
	}
}

func TestSumViaAny(t *testing.T) {
	if got := sumViaAny(boxValues([]int{1, 2, 3})); got != 6 {
		t.Errorf("got %d, want 6", got)
	}
	if got := sumViaAny([]any{1, "not an int", 2}); got != 3 {
		t.Errorf("got %d, want 3 — non-ints should be skipped", got)
	}
}

func TestAllocationDocsArePresent(t *testing.T) {
	if got := allocationSources(); len(got) < 6 {
		t.Errorf("expected at least 6 sources, got %d", len(got))
	}
	if got := theOptimisationOrder(); len(got) != 4 {
		t.Errorf("expected exactly 4 steps, got %d", len(got))
	}
}

// The benchmark pairs. Run:
//
//	go test -bench BenchmarkAlloc -benchmem -run '^$' ./17-benchmarks-and-pprof

const allocN = 1000

var benchValues = func() []int {
	v := make([]int, allocN)
	for i := range v {
		v[i] = i
	}
	return v
}()

var benchStrings = func() []string {
	s := make([]string, 100)
	for i := range s {
		s[i] = strconv.Itoa(i)
	}
	return s
}()

func BenchmarkAllocSliceGrowing(b *testing.B) {
	for b.Loop() {
		sinkStrings = collectGrowing(allocN)
	}
}

func BenchmarkAllocSlicePresized(b *testing.B) {
	for b.Loop() {
		sinkStrings = collectPresized(allocN)
	}
}

func BenchmarkAllocMapGrowing(b *testing.B) {
	for b.Loop() {
		sinkMap = indexGrowing(allocN)
	}
}

func BenchmarkAllocMapPresized(b *testing.B) {
	for b.Loop() {
		sinkMap = indexPresized(allocN)
	}
}

func BenchmarkAllocStringConcat(b *testing.B) {
	for b.Loop() {
		sinkString = buildWithConcat(benchStrings)
	}
}

func BenchmarkAllocStringBuilder(b *testing.B) {
	for b.Loop() {
		sinkString = buildWithBuilder(benchStrings)
	}
}

func BenchmarkAllocFormatWithFmt(b *testing.B) {
	for b.Loop() {
		sinkStrings = formatWithFmt(benchValues)
	}
}

func BenchmarkAllocFormatWithStrconv(b *testing.B) {
	for b.Loop() {
		sinkStrings = formatWithStrconv(benchValues)
	}
}

// The boxing pair has to compare the SAME operation. An earlier version
// benchmarked boxValues (which boxes) against sumSlice (which sums), and the
// resulting "23x" was mostly the difference between boxing and adding.
//
// Both of these sum 1000 integers. One reaches them through `any`.
var benchBoxed = boxValues(benchValues)

func BenchmarkAllocSumViaAny(b *testing.B) {
	for b.Loop() {
		sinkInt = sumViaAny(benchBoxed)
	}
}

func BenchmarkAllocSumViaInt(b *testing.B) {
	for b.Loop() {
		sinkInt = sumSlice(benchValues)
	}
}

// And the boxing itself, measured on its own rather than smuggled into a
// comparison with something else.
func BenchmarkAllocBoxingItself(b *testing.B) {
	for b.Loop() {
		sinkAny = boxValues(benchValues)
	}
}

func BenchmarkAllocRecordPointer(b *testing.B) {
	for b.Loop() {
		sinkRecordPtr = newRecordPointer(1)
	}
}

func BenchmarkAllocRecordValue(b *testing.B) {
	for b.Loop() {
		sinkRecord = newRecordValue(1)
	}
}
