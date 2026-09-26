package sorting

import (
	"cmp"
	"slices"
	"sort"
	"testing"
)

// Benchmarking a sort needs care: the input has to be restored between
// iterations, or the second iteration sorts already-sorted data and every one
// after it measures the best case.
//
// The copy is INSIDE the timer. The obvious alternative, b.StopTimer around it,
// costs roughly a microsecond per call, which is fine for a 5 ms sort and swamps a
// 50 ns one: with StopTimer the n=8 benchmarks took so long they had to be killed.
// BenchmarkCopyOnly measures the copy at each size so it can be subtracted, and at
// every size that matters it is under 1%.
func benchSort(b *testing.B, input []int, sortFn func([]int)) {
	work := make([]int, len(input))

	for b.Loop() {
		copy(work, input)
		sortFn(work)
	}
}

// The baseline for the note above: what benchSort costs with no sort in it.
func BenchmarkCopyOnly(b *testing.B) {
	for _, n := range []int{8, 64, 2000, 100_000} {
		input := shapes(n)["random"]

		b.Run(itoa(n), func(b *testing.B) {
			benchSort(b, input, func([]int) {})
		})
	}
}

// All six plus the standard library, at a size the quadratic ones can survive.
func BenchmarkQuadratic(b *testing.B) {
	const n = 2000
	shaped := shapes(n)

	all := append(slices.Clone(algorithms), struct {
		name   string
		sort   func([]int)
		stable bool
	}{"slices.Sort", slices.Sort[[]int], false})

	for _, shape := range []string{"random", "sorted", "reversed", "nearly", "duplicates"} {
		for _, alg := range all {
			b.Run(shape+"/"+alg.name, func(b *testing.B) {
				benchSort(b, shaped[shape], alg.sort)
			})
		}
	}
}

// The three that can handle a real size, against the standard library.
func BenchmarkFast(b *testing.B) {
	const n = 100_000
	shaped := shapes(n)

	fast := []struct {
		name string
		sort func([]int)
	}{
		{"merge", Merge[int]},
		{"quick", Quick[int]},
		{"heap", Heap[int]},
		{"slices.Sort", slices.Sort[[]int]},
		{"slices.SortFunc", func(s []int) { slices.SortFunc(s, cmp.Compare) }},
		{"slices.SortStableFunc", func(s []int) { slices.SortStableFunc(s, cmp.Compare) }},
		{"sort.Ints", func(s []int) { sort.Ints(s) }},
	}

	for _, shape := range []string{"random", "sorted", "reversed", "nearly", "duplicates"} {
		for _, alg := range fast {
			b.Run(shape+"/"+alg.name, func(b *testing.B) {
				benchSort(b, shaped[shape], alg.sort)
			})
		}
	}
}

// Where insertion sort wins, which is the measurement that explains why every
// production sort contains one.
func BenchmarkSmall(b *testing.B) {
	for _, n := range []int{8, 16, 32, 64, 128} {
		input := shapes(n)["random"]

		for _, alg := range []struct {
			name string
			sort func([]int)
		}{
			{"insertion", Insertion[int]},
			{"quick", Quick[int]},
			{"merge", Merge[int]},
			{"heap", Heap[int]},
			{"slices.Sort", slices.Sort[[]int]},
		} {
			b.Run(itoa(n)+"/"+alg.name, func(b *testing.B) {
				benchSort(b, input, alg.sort)
			})
		}
	}
}

// What a comparison function costs. slices.Sort inlines `<`; slices.SortFunc calls
// through a func value on every comparison, and so does everything in this package.
func BenchmarkCompareFunctionCost(b *testing.B) {
	const n = 100_000
	input := shapes(n)["random"]

	b.Run("slices.Sort inlined", func(b *testing.B) {
		benchSort(b, input, slices.Sort[[]int])
	})

	b.Run("slices.SortFunc via cmp.Compare", func(b *testing.B) {
		benchSort(b, input, func(s []int) { slices.SortFunc(s, cmp.Compare) })
	})

	b.Run("sort.Slice via a closure", func(b *testing.B) {
		benchSort(b, input, func(s []int) {
			sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
		})
	})
}

// The two lines inside mergeSort that are not the algorithm, priced separately.

// mergeNaive allocates a buffer per merge and has no insertion-sort threshold,
// which is how merge sort is written in most textbooks.
func mergeNaive[T any](s []T, compare Compare[T]) {
	if len(s) < 2 {
		return
	}

	mid := len(s) / 2
	mergeNaive(s[:mid], compare)
	mergeNaive(s[mid:], compare)

	buf := make([]T, len(s)) // a fresh allocation at every level
	merge(s, buf, mid, compare)
}

// mergeNoThreshold reuses one buffer but recurses all the way to single elements.
func mergeNoThreshold[T any](s []T, compare Compare[T]) {
	if len(s) < 2 {
		return
	}
	buf := make([]T, len(s))
	mergeNoThresholdInner(s, buf, compare)
}

func mergeNoThresholdInner[T any](s, buf []T, compare Compare[T]) {
	if len(s) < 2 {
		return
	}

	mid := len(s) / 2
	mergeNoThresholdInner(s[:mid], buf[:mid], compare)
	mergeNoThresholdInner(s[mid:], buf[mid:], compare)

	if compare(s[mid-1], s[mid]) <= 0 {
		return
	}
	merge(s, buf, mid, compare)
}

func BenchmarkMergeVariants(b *testing.B) {
	const n = 100_000
	input := shapes(n)["random"]

	b.Run("one buffer, threshold 12", func(b *testing.B) {
		benchSort(b, input, Merge[int])
	})

	b.Run("one buffer, no threshold", func(b *testing.B) {
		benchSort(b, input, func(s []int) { mergeNoThreshold(s, cmp.Compare) })
	})

	b.Run("buffer per merge, no threshold", func(b *testing.B) {
		benchSort(b, input, func(s []int) { mergeNaive(s, cmp.Compare) })
	})
}

// quickNaive takes the first element as the pivot and partitions two ways, which
// is the textbook version and the one that is quadratic on sorted input.
func quickNaive[T any](s []T, compare Compare[T]) {
	if len(s) < 2 {
		return
	}

	pivot := s[0]
	i := 0

	for j := 1; j < len(s); j++ {
		if compare(s[j], pivot) < 0 {
			i++
			s[i], s[j] = s[j], s[i]
		}
	}
	s[0], s[i] = s[i], s[0]

	quickNaive(s[:i], compare)
	quickNaive(s[i+1:], compare)
}

// What the pivot choice and the three-way partition are worth. The naive version
// is run at a much smaller n because it is O(n^2) on three of these five shapes
// and would not finish otherwise.
func BenchmarkQuickVariants(b *testing.B) {
	const n = 3000
	shaped := shapes(n)

	for _, shape := range []string{"random", "sorted", "reversed", "duplicates"} {
		b.Run(shape+"/ninther, three-way", func(b *testing.B) {
			benchSort(b, shaped[shape], Quick[int])
		})

		b.Run(shape+"/first element, two-way", func(b *testing.B) {
			benchSort(b, shaped[shape], func(s []int) { quickNaive(s, cmp.Compare) })
		})
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
