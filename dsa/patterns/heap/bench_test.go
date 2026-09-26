package heap

import (
	"cmp"
	stdheap "container/heap"
	"math/rand/v2"
	"slices"
	"testing"
)

// stdIntHeap is container/heap's idea of a min-heap of ints: five methods, two of them
// dealing in `any`. Kept here so the two APIs can be compared on identical work.
type stdIntHeap []int

func (h stdIntHeap) Len() int           { return len(h) }
func (h stdIntHeap) Less(i, j int) bool { return h[i] < h[j] }
func (h stdIntHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *stdIntHeap) Push(x any)        { *h = append(*h, x.(int)) }
func (h *stdIntHeap) Pop() any {
	old := *h
	last := len(old) - 1
	v := old[last]
	*h = old[:last]
	return v
}

func randomInts(n int) []int {
	r := rand.New(rand.NewPCG(1, 2))
	s := make([]int, n)
	for i := range s {
		s[i] = r.IntN(n * 10)
	}
	return s
}

// This package's generic heap against container/heap, on the same n pushes and n pops.
func BenchmarkAgainstContainerHeap(b *testing.B) {
	const n = 10_000
	input := randomInts(n)

	b.Run("generic", func(b *testing.B) {
		for b.Loop() {
			h := New[int](cmp.Compare)
			for _, v := range input {
				h.Push(v)
			}
			for h.Len() > 0 {
				sinkInt, _ = h.Pop()
			}
		}
	})

	b.Run("container/heap", func(b *testing.B) {
		for b.Loop() {
			h := &stdIntHeap{}
			for _, v := range input {
				stdheap.Push(h, v)
			}
			for h.Len() > 0 {
				sinkInt = stdheap.Pop(h).(int)
			}
		}
	})
}

// Heapify against pushing one at a time: O(n) against O(n log n).
func BenchmarkBuild(b *testing.B) {
	for _, n := range []int{100, 10_000, 1_000_000} {
		input := randomInts(n)

		b.Run(itoa(n)+"/From (heapify)", func(b *testing.B) {
			for b.Loop() {
				sinkInt = From(slices.Clone(input), cmp.Compare[int]).Len()
			}
		})

		b.Run(itoa(n)+"/Push each", func(b *testing.B) {
			for b.Loop() {
				h := New[int](cmp.Compare)
				for _, v := range input {
					h.Push(v)
				}
				sinkInt = h.Len()
			}
		})
	}
}

// The pattern's whole justification: O(n log k) against O(n log n). The crossover is
// further out than the complexity suggests, because sorting has a much better constant
// and slices.Sort has no comparison-function indirection at all.
func BenchmarkKLargest(b *testing.B) {
	const n = 1_000_000
	input := randomInts(n)

	for _, k := range []int{1, 10, 100, 1000, 10_000, 100_000} {
		b.Run("k="+itoa(k)+"/heap", func(b *testing.B) {
			for b.Loop() {
				sinkSlice = KLargest(input, k)
			}
		})

		b.Run("k="+itoa(k)+"/sort and slice", func(b *testing.B) {
			for b.Loop() {
				sorted := slices.Clone(input)
				slices.Sort(sorted)
				slices.Reverse(sorted)
				sinkSlice = sorted[:k]
			}
		})
	}
}

// Merging k sorted lists three ways.
func BenchmarkMergeSorted(b *testing.B) {
	const total = 1_000_000

	for _, k := range []int{2, 10, 100} {
		perList := total / k

		lists := make([][]int, k)
		for i := range lists {
			lists[i] = make([]int, perList)
			for j := range lists[i] {
				lists[i][j] = j*k + i
			}
		}

		b.Run("k="+itoa(k)+"/heap", func(b *testing.B) {
			for b.Loop() {
				sinkSlice = MergeSorted(lists)
			}
		})

		b.Run("k="+itoa(k)+"/concatenate and sort", func(b *testing.B) {
			for b.Loop() {
				all := make([]int, 0, total)
				for _, l := range lists {
					all = append(all, l...)
				}
				slices.Sort(all)
				sinkSlice = all
			}
		})
	}
}

// A running median three ways.
func BenchmarkMedianStream(b *testing.B) {
	const n = 20_000
	input := randomInts(n)

	b.Run("two heaps", func(b *testing.B) {
		for b.Loop() {
			m := NewMedianStream[int]()
			for _, v := range input {
				m.Add(v)
				sinkInt, _ = m.Median()
			}
		}
	})

	b.Run("sorted insert", func(b *testing.B) {
		for b.Loop() {
			var sorted []int
			for _, v := range input {
				i, _ := slices.BinarySearch(sorted, v)
				sorted = slices.Insert(sorted, i, v)
				sinkInt = sorted[(len(sorted)-1)/2]
			}
		}
	})

	b.Run("re-sort every time", func(b *testing.B) {
		if testing.Short() {
			b.Skip("O(n^2 log n)")
		}
		for b.Loop() {
			var seen []int
			for _, v := range input[:2000] { // a tenth of the input, and still the slowest
				seen = append(seen, v)
				sorted := slices.Clone(seen)
				slices.Sort(sorted)
				sinkInt = sorted[(len(sorted)-1)/2]
			}
		}
	})
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

var (
	sinkInt   int
	sinkSlice []int
)
