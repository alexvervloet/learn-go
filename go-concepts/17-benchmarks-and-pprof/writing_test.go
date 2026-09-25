package main

import (
	"slices"
	"strconv"
	"strings"
	"testing"
)

// TestJoinImplementationsAgree is the guard that makes the benchmark pair
// below meaningful. Lesson 17's first lie is "the two sides do different
// work", and this is the defence: a test asserting identical output, sitting
// next to the benchmark it protects.
func TestJoinImplementationsAgree(t *testing.T) {
	inputs := [][]string{
		nil,
		{"one"},
		{"alpha", "beta", "gamma", "delta"},
		{"", "x", ""},
		make([]string, 100),
	}

	for i, parts := range inputs {
		want := joinWithStdlib(parts)

		got := []struct {
			name  string
			value string
		}{
			{"joinWithPlus", joinWithPlus(parts)},
			{"joinWithBuilder", joinWithBuilder(parts)},
			{"joinWithBuilderPresized", joinWithBuilderPresized(parts)},
			{"joinWithSeparator", joinWithSeparator(parts, "")},
		}

		for _, g := range got {
			if g.value != want {
				t.Errorf("input %d: %s = %q, strings.Join = %q", i, g.name, g.value, want)
			}
		}
	}
}

func TestSearchImplementationsAgree(t *testing.T) {
	values := make([]int, 1000)
	for i := range values {
		values[i] = i
	}
	index := buildIndex(values)

	for _, target := range []int{0, 1, 500, 999, -1, 1000} {
		linear := searchLinear(values, target)
		binary := searchBinary(values, target)
		byMap := searchMap(index, target)

		if linear != binary || linear != byMap {
			t.Errorf("target %d: linear=%d binary=%d map=%d", target, linear, binary, byMap)
		}
	}
}

func TestSumSlice(t *testing.T) {
	tests := []struct {
		name string
		in   []int
		want int
	}{
		{"empty", nil, 0},
		{"one", []int{5}, 5},
		{"several", []int{1, 2, 3, 4}, 10},
		{"negatives", []int{-1, 1}, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sumSlice(tt.in); got != tt.want {
				t.Errorf("got %d, want %d", got, tt.want)
			}
		})
	}
}

func TestBuildIndex(t *testing.T) {
	values := []int{10, 20, 30}
	index := buildIndex(values)

	if len(index) != 3 {
		t.Errorf("index has %d entries, want 3", len(index))
	}
	for i, v := range values {
		if index[v] != i {
			t.Errorf("index[%d] = %d, want %d", v, index[v], i)
		}
	}
}

func TestBenchmarkDocsArePresent(t *testing.T) {
	if got := benchmarkShapes(); len(got) < 6 {
		t.Errorf("expected at least 6 documented shapes, got %d", len(got))
	}
	if got := flagsWorthKnowing(); len(got) < 5 {
		t.Errorf("expected at least 5 documented flags, got %d", len(got))
	}
}

// The benchmarks. Run:
//
//	go test -bench . -benchmem -run '^$' ./17-benchmarks-and-pprof

var benchParts = func() []string {
	parts := make([]string, 100)
	for i := range parts {
		parts[i] = strconv.Itoa(i)
	}
	return parts
}()

func BenchmarkJoinWithPlus(b *testing.B) {
	for b.Loop() {
		sinkString = joinWithPlus(benchParts)
	}
}

func BenchmarkJoinWithBuilder(b *testing.B) {
	for b.Loop() {
		sinkString = joinWithBuilder(benchParts)
	}
}

func BenchmarkJoinWithBuilderPresized(b *testing.B) {
	for b.Loop() {
		sinkString = joinWithBuilderPresized(benchParts)
	}
}

func BenchmarkJoinWithStdlib(b *testing.B) {
	for b.Loop() {
		sinkString = joinWithStdlib(benchParts)
	}
}

// BenchmarkJoinLikeForLike is the FAIR comparison, and the reason it exists is
// worth reading in writing.go.
//
// The four benchmarks above compare functions that do not handle a separator
// against strings.Join, which does. They produce identical output for an empty
// separator, so the agreement test passes, and they do measurably different
// work. That is lie number one, in the lesson about lie number one.
//
// These two do the same job, and the gap shrinks from 1.9x to about 1.2x.
func BenchmarkJoinLikeForLikeStdlib(b *testing.B) {
	for b.Loop() {
		sinkString = strings.Join(benchParts, ",")
	}
}

func BenchmarkJoinLikeForLikeHandWritten(b *testing.B) {
	for b.Loop() {
		sinkString = joinWithSeparator(benchParts, ",")
	}
}

// BenchmarkSearch is a sub-benchmark table over input sizes, which is how you
// see an algorithmic difference rather than a constant factor. Linear and
// binary cross over somewhere, and the table shows where.
func BenchmarkSearch(b *testing.B) {
	for _, size := range []int{10, 100, 1000, 100_000} {
		values := make([]int, size)
		for i := range values {
			values[i] = i
		}
		index := buildIndex(values)
		target := size - 1 // the worst case for a linear scan

		b.Run("linear/"+strconv.Itoa(size), func(b *testing.B) {
			for b.Loop() {
				sinkInt = searchLinear(values, target)
			}
		})

		b.Run("binary/"+strconv.Itoa(size), func(b *testing.B) {
			for b.Loop() {
				sinkInt = searchBinary(values, target)
			}
		})

		b.Run("map/"+strconv.Itoa(size), func(b *testing.B) {
			for b.Loop() {
				sinkInt = searchMap(index, target)
			}
		})
	}
}

// BenchmarkSearchMapIncludingBuild is the honest version of the map
// comparison: it counts the index build, which the pair above amortises away.
//
// For a single lookup the map is far SLOWER than a linear scan, because
// building the index is O(n) and the scan is one pass. The map wins only when
// the index is reused, and a benchmark that hides the build makes it look like
// a free win.
func BenchmarkSearchMapIncludingBuild(b *testing.B) {
	values := make([]int, 1000)
	for i := range values {
		values[i] = i
	}

	for b.Loop() {
		index := buildIndex(values)
		sinkInt = searchMap(index, 999)
	}
}

func BenchmarkSearchLinearOnce(b *testing.B) {
	values := make([]int, 1000)
	for i := range values {
		values[i] = i
	}

	for b.Loop() {
		sinkInt = searchLinear(values, 999)
	}
}

// BenchmarkParallelJoin measures under contention, which is a different
// question from single-threaded speed and the one that matters for a server.
func BenchmarkParallelJoin(b *testing.B) {
	b.RunParallel(func(pb *testing.PB) {
		local := ""
		for pb.Next() {
			local = joinWithBuilderPresized(benchParts)
		}
		sinkString = local
	})
}

// BenchmarkWithCustomMetric shows b.ReportMetric, for when ns/op is not the
// number you care about.
func BenchmarkWithCustomMetric(b *testing.B) {
	parts := benchParts

	var joined int
	for b.Loop() {
		sinkString = strings.Join(parts, "")
		joined += len(parts)
	}

	b.ReportMetric(float64(joined)/b.Elapsed().Seconds(), "parts/sec")
}

func TestSlicesEqualHelperIsAvailable(t *testing.T) {
	// A trivial guard that the test file's imports are all used, and a
	// reminder that slices.Equal is the right comparison for benchmark
	// agreement checks.
	if !slices.Equal([]string{"a"}, []string{"a"}) {
		t.Error("slices.Equal is broken, which would be remarkable")
	}
}
