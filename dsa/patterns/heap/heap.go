// Package heap implements a binary heap and the "top k" problems it exists for.
//
// # The pattern
//
// Any question of the form "the k largest", "the k most frequent" or "the k closest"
// is a heap. The instinct is to sort and take the first k, which is O(n log n). A
// heap of size k is O(n log k), and when k is small that is effectively O(n).
//
// The trick that makes it work is counter-intuitive: to find the k LARGEST elements
// you keep a MIN-heap of size k. The smallest of your k best sits at the top, so
// deciding whether a new element belongs is one comparison, and evicting the loser is
// one pop. A max-heap would put the wrong element where you can see it.
//
// # Why this package has its own heap
//
// container/heap predates generics. Using it means defining a type with five methods,
// two of them taking and returning `any`, and it has three traps that compile
// silently:
//
//	heap.Push and yourType.Push are different functions, and calling the second
//	  directly leaves the invariant broken
//	your Pop must return the LAST element, because heap.Pop swaps the minimum there
//	  first
//	Push and Pop need pointer receivers, or they append to a copy
//
// The generic version below needs no interface and no type assertion, and it is
// SLOWER: 2.04 ms against container/heap's 1.39 ms for 10,000 pushes and pops, a
// factor of 1.46.
//
// That was not what I expected, and the reason is the same one the sorting package
// measured at 1.48x. container/heap calls `Less` on a concrete type, which the
// compiler devirtualises and inlines down to `h[i] < h[j]`. This heap calls
// `h.compare(a, b)` through a func field, which it cannot. The ergonomics are better
// and the comparison is an indirect call on every sift step.
//
// So the honest recommendation is: use this for the ergonomics, and if a profile
// blames the heap, write the five methods. The standard library still has no generic
// heap, and when it gets one it will presumably be specialised per type and beat
// both.
package heap

import (
	"cmp"
	"iter"
	"slices"
)

// Compare returns negative if a comes out first, zero if the two are equivalent, and
// positive otherwise. The same shape as slices.SortFunc's argument, so cmp.Compare
// gives a min-heap and reversing it gives a max-heap.
type Compare[T any] func(a, b T) int

// Heap is a binary heap. Use New, NewMin or NewMax: the zero value has no comparison
// function and cannot order anything.
//
// The tree lives in the slice itself, with no pointers: the children of index i are
// at 2i+1 and 2i+2, and the parent of i is at (i-1)/2. That is the entire data
// structure, and it is why a heap allocates once and a tree allocates per node.
type Heap[T any] struct {
	items   []T
	compare Compare[T]
}

// New returns an empty heap ordered by compare.
func New[T any](compare Compare[T]) *Heap[T] {
	return &Heap[T]{compare: compare}
}

// NewMin returns a min-heap: Pop returns the smallest element.
func NewMin[T cmp.Ordered]() *Heap[T] { return New[T](cmp.Compare) }

// NewMax returns a max-heap: Pop returns the largest element.
//
// The arguments are swapped rather than the result negated. Negating is the obvious
// alternative and it is wrong for the extremes: -math.MinInt overflows back to
// itself, so a max-heap built that way mis-orders math.MinInt.
func NewMax[T cmp.Ordered]() *Heap[T] {
	return New[T](func(a, b T) int { return cmp.Compare(b, a) })
}

// From builds a heap from existing items in O(n), taking ownership of the slice.
//
// O(n), not O(n log n), and the reason is worth knowing: siftDown from the last
// parent backwards does almost no work for most nodes, because most nodes are near
// the leaves. Pushing the items one at a time would be O(n log n), since each push
// can travel the full height.
func From[T any](items []T, compare Compare[T]) *Heap[T] {
	h := &Heap[T]{items: items, compare: compare}

	// Start at the last parent. Leaves are already valid heaps of one element, so
	// the second half of the slice needs no work at all.
	for i := len(items)/2 - 1; i >= 0; i-- {
		h.siftDown(i)
	}

	return h
}

// Len reports the number of items.
func (h *Heap[T]) Len() int { return len(h.items) }

// Push adds v.
func (h *Heap[T]) Push(v T) {
	h.items = append(h.items, v)
	h.siftUp(len(h.items) - 1)
}

// Pop removes and returns the item at the top, reporting whether there was one.
func (h *Heap[T]) Pop() (T, bool) {
	if len(h.items) == 0 {
		var zero T
		return zero, false
	}

	top := h.items[0]
	last := len(h.items) - 1

	h.items[0] = h.items[last]

	// Zero the vacated slot before shrinking, so a Heap[*Job] does not pin a job.
	// The same problem as the stack and the queue, and just as invisible.
	var zero T
	h.items[last] = zero
	h.items = h.items[:last]

	if len(h.items) > 0 {
		h.siftDown(0)
	}

	return top, true
}

// Peek returns the item at the top without removing it.
func (h *Heap[T]) Peek() (T, bool) {
	if len(h.items) == 0 {
		var zero T
		return zero, false
	}
	return h.items[0], true
}

// PushPop pushes v and pops the top in one operation.
//
// One sift instead of two, which halves the cost of the inner loop of every top-k
// problem below. When v would come straight back out it does not touch the heap at
// all.
func (h *Heap[T]) PushPop(v T) (T, bool) {
	if len(h.items) == 0 {
		return v, false
	}

	top := h.items[0]

	if h.compare(v, top) <= 0 {
		return v, true // v belongs at the top, so it is its own answer
	}

	h.items[0] = v
	h.siftDown(0)

	return top, true
}

// All iterates the items in heap order, which is not sorted order.
//
// Only the top is ordered relative to everything else. A heap is not a sorted
// container, and code that ranges over one expecting sorted output is a bug that
// tests with three elements will not catch.
func (h *Heap[T]) All() iter.Seq[T] {
	return func(yield func(T) bool) {
		for _, v := range h.items {
			if !yield(v) {
				return
			}
		}
	}
}

// Sorted drains the heap and returns the items in order, emptying it.
//
// This is heapsort. n pops, each O(log n).
func (h *Heap[T]) Sorted() []T {
	out := make([]T, 0, len(h.items))

	for {
		v, ok := h.Pop()
		if !ok {
			return out
		}
		out = append(out, v)
	}
}

// siftUp moves item i towards the root until its parent is not worse than it.
func (h *Heap[T]) siftUp(i int) {
	for i > 0 {
		parent := (i - 1) / 2

		if h.compare(h.items[i], h.items[parent]) >= 0 {
			return
		}

		h.items[i], h.items[parent] = h.items[parent], h.items[i]
		i = parent
	}
}

// siftDown moves item i away from the root until both children are not better.
func (h *Heap[T]) siftDown(i int) {
	n := len(h.items)

	for {
		best := i
		left, right := 2*i+1, 2*i+2

		if left < n && h.compare(h.items[left], h.items[best]) < 0 {
			best = left
		}
		if right < n && h.compare(h.items[right], h.items[best]) < 0 {
			best = right
		}

		if best == i {
			return
		}

		h.items[i], h.items[best] = h.items[best], h.items[i]
		i = best
	}
}

// IsHeap reports whether s satisfies the heap property under compare. For tests.
func IsHeap[T any](s []T, compare Compare[T]) bool {
	for i := range s {
		for _, child := range []int{2*i + 1, 2*i + 2} {
			if child < len(s) && compare(s[child], s[i]) < 0 {
				return false
			}
		}
	}
	return true
}

// Top k
// =====

// KLargest returns the k largest elements of s, largest first.
//
// O(n log k) time and O(k) space, using a MIN-heap of size k. The smallest of the k
// best is at the top, so each new element costs one comparison to reject.
//
// Sorting and slicing is O(n log n) and O(n) extra if the input must not be mutated.
// For k much smaller than n the difference is large; the benchmarks say where the
// crossover falls, and it is further out than the complexity alone suggests.
func KLargest[T cmp.Ordered](s []T, k int) []T {
	if k <= 0 || len(s) == 0 {
		return nil
	}
	k = min(k, len(s))

	// The counter-intuitive part: a min-heap, to find the largest.
	h := From(slices.Clone(s[:k]), cmp.Compare[T])

	for _, v := range s[k:] {
		// One comparison rejects most elements, and only the survivors sift.
		if top, _ := h.Peek(); cmp.Compare(v, top) <= 0 {
			continue
		}
		h.PushPop(v)
	}

	out := h.Sorted()
	slices.Reverse(out) // Sorted gives ascending; largest first reads better
	return out
}

// KSmallest returns the k smallest elements of s, smallest first.
//
// The mirror image, so a MAX-heap of size k.
func KSmallest[T cmp.Ordered](s []T, k int) []T {
	if k <= 0 || len(s) == 0 {
		return nil
	}
	k = min(k, len(s))

	descending := func(a, b T) int { return cmp.Compare(b, a) }
	h := From(slices.Clone(s[:k]), descending)

	for _, v := range s[k:] {
		if top, _ := h.Peek(); cmp.Compare(v, top) >= 0 {
			continue
		}
		h.PushPop(v)
	}

	out := h.Sorted()
	slices.Reverse(out)
	return out
}

// Counted pairs a value with how often it appeared.
type Counted[T comparable] struct {
	Value T
	Count int
}

// KMostFrequent returns the k most frequently occurring values, most frequent first.
//
// Two phases, and the first one dominates: count everything in a map, O(n), then take
// the top k of the DISTINCT values, O(d log k). When almost every value is distinct
// this is barely better than sorting; when a few values dominate it is much better.
//
// Ties are broken by the value itself, so the output is deterministic. Without that
// the result depends on map iteration order and the test cannot be written.
func KMostFrequent[T cmp.Ordered](s []T, k int) []Counted[T] {
	if k <= 0 || len(s) == 0 {
		return nil
	}

	counts := make(map[T]int, len(s))
	for _, v := range s {
		counts[v]++
	}

	// Least frequent at the top, so it is the one to evict. The tie-break is
	// REVERSED relative to the final order, because the heap holds the survivors and
	// evicts from the top: to keep the smallest value on a tie, the largest value
	// must be the one nearest the exit.
	byCount := func(a, b Counted[T]) int {
		if a.Count != b.Count {
			return cmp.Compare(a.Count, b.Count)
		}
		return cmp.Compare(b.Value, a.Value)
	}

	h := New(byCount)

	for value, count := range counts {
		item := Counted[T]{Value: value, Count: count}

		if h.Len() < k {
			h.Push(item)
			continue
		}
		if top, _ := h.Peek(); byCount(item, top) <= 0 {
			continue
		}
		h.PushPop(item)
	}

	out := h.Sorted()
	slices.Reverse(out)
	return out
}

// MergeSorted merges any number of sorted slices into one sorted slice.
//
// O(N log k) for N elements across k slices, using a heap of one cursor per slice.
// Concatenating and sorting is O(N log N). On paper the heap wins for any k < N, and
// in practice it wins only for very small k. A million elements:
//
//	k      heap      concatenate and sort
//	2      9.1 ms    17.8 ms      heap 1.95x faster
//	10     32.2 ms   27.8 ms      heap 1.16x slower
//	100    77.3 ms   37.6 ms      heap 2.06x slower
//
// The reason is the cost of one comparison. The heap does log2(k) comparisons per
// element, each an indirect call through a func field that then indexes twice
// (lists[c.list][c.at]). slices.Sort does about log2(N) comparisons, each an inlined
// `<`. That is roughly a 7x difference per comparison, so the break-even is near
// log2(k) = log2(N)/7, which for N=1,000,000 is k around 6. The measurement agrees.
//
// My first guess was cache thrashing across k memory streams, and that is wrong: at a
// total of 1,024 ints, where everything fits in L1, the heap is 8.5x slower at k=100,
// which is worse than its ratio at four million. Shrinking the working set made the
// heap relatively WORSE, so cache is not the mechanism.
//
// None of which retires the algorithm. Its real justification is that it holds only k
// cursors, so it merges inputs that do not fit in memory at all. That is what an
// external sort, a log aggregator, and an LSM-tree compaction need, and concatenating
// is not an option for any of them.
func MergeSorted[T cmp.Ordered](lists [][]T) []T {
	type cursor struct {
		list int
		at   int
	}

	total := 0
	for _, l := range lists {
		total += len(l)
	}
	if total == 0 {
		return nil
	}

	// Compare by the value each cursor points at. The cursor is in the heap, not the
	// value, which is what keeps the memory O(k) rather than O(N).
	h := New(func(a, b cursor) int {
		return cmp.Compare(lists[a.list][a.at], lists[b.list][b.at])
	})

	for i, l := range lists {
		if len(l) > 0 {
			h.Push(cursor{list: i, at: 0})
		}
	}

	out := make([]T, 0, total)

	for h.Len() > 0 {
		c, _ := h.Pop()
		out = append(out, lists[c.list][c.at])

		if c.at+1 < len(lists[c.list]) {
			h.Push(cursor{list: c.list, at: c.at + 1})
		}
	}

	return out
}
