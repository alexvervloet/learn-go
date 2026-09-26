package heap_test

import (
	"cmp"
	"fmt"

	"github.com/alexvervloet/learn-go/dsa/patterns/heap"
)

func ExampleHeap() {
	h := heap.NewMin[int]()
	for _, v := range []int{5, 2, 9, 1, 7} {
		h.Push(v)
	}

	top, _ := h.Peek()
	fmt.Println("smallest:", top)
	fmt.Println("drained: ", h.Sorted())

	// Output:
	// smallest: 1
	// drained:  [1 2 5 7 9]
}

// A heap orders only its top. Ranging over one is not ranging over a sorted
// container, and code that assumes otherwise passes every three-element test.
func ExampleHeap_All() {
	h := heap.NewMin[int]()
	for _, v := range []int{9, 8, 7, 6, 5} {
		h.Push(v)
	}

	fmt.Println("heap order:  ", slicesOf(h))
	fmt.Println("sorted order:", h.Sorted())

	// Output:
	// heap order:   [5 6 8 9 7]
	// sorted order: [5 6 7 8 9]
}

func slicesOf(h *heap.Heap[int]) []int {
	var out []int
	for v := range h.All() {
		out = append(out, v)
	}
	return out
}

// From heapifies in place in O(n), where pushing one at a time is O(n log n). It takes
// ownership of the slice.
func ExampleFrom() {
	items := []int{5, 2, 9, 1, 7}

	h := heap.From(items, cmp.Compare[int])
	fmt.Println("the slice was rearranged:", items[0] == 1)
	fmt.Println(h.Sorted())

	// Output:
	// the slice was rearranged: true
	// [1 2 5 7 9]
}

// Any comparison works, so a heap of structs needs no wrapper type.
func ExampleNew() {
	type job struct {
		name     string
		priority int
	}

	// Highest priority first, ties broken by name so the output is deterministic.
	h := heap.New(func(a, b job) int {
		if a.priority != b.priority {
			return cmp.Compare(b.priority, a.priority)
		}
		return cmp.Compare(a.name, b.name)
	})

	for _, j := range []job{{"vacuum", 1}, {"fire", 9}, {"email", 3}, {"flood", 9}} {
		h.Push(j)
	}

	for _, j := range h.Sorted() {
		fmt.Printf("%d %s\n", j.priority, j.name)
	}

	// Output:
	// 9 fire
	// 9 flood
	// 3 email
	// 1 vacuum
}

// The counter-intuitive part of the pattern: to find the k LARGEST, keep a MIN-heap of
// size k, so the weakest of your current best sits where you can see it.
func ExampleKLargest() {
	s := []int{3, 1, 4, 1, 5, 9, 2, 6, 5, 3, 5}

	fmt.Println(heap.KLargest(s, 3))
	fmt.Println(heap.KSmallest(s, 3))

	// k larger than the input is not an error.
	fmt.Println(heap.KLargest(s, 100))

	// Output:
	// [9 6 5]
	// [1 1 2]
	// [9 6 5 5 5 4 3 3 2 1 1]
}

// Ties are broken by the value, so the result does not depend on map iteration order.
func ExampleKMostFrequent() {
	words := []string{"go", "rust", "go", "zig", "go", "rust"}

	for _, c := range heap.KMostFrequent(words, 2) {
		fmt.Printf("%s appears %d times\n", c.Value, c.Count)
	}

	// Output:
	// go appears 3 times
	// rust appears 2 times
}

// The heap holds one cursor per list, not the values, so merging inputs far larger
// than memory costs O(k) space. That, rather than speed, is why the algorithm exists.
func ExampleMergeSorted() {
	fmt.Println(heap.MergeSorted([][]int{
		{1, 4, 7},
		{2, 5, 8},
		{3, 6, 9},
	}))

	// Output:
	// [1 2 3 4 5 6 7 8 9]
}

// Two heaps pointing at each other: a max-heap of the small half and a min-heap of the
// large half, so the median is whatever is on top.
func ExampleMedianStream() {
	m := heap.NewMedianStream[int]()

	for _, v := range []int{5, 15, 1, 3} {
		m.Add(v)

		median, _ := m.Median()
		low, high, _ := m.MedianPair()
		fmt.Printf("added %2d -> median %2d (between %d and %d)\n", v, median, low, high)
	}

	// Output:
	// added  5 -> median  5 (between 5 and 5)
	// added 15 -> median  5 (between 5 and 15)
	// added  1 -> median  5 (between 5 and 5)
	// added  3 -> median  3 (between 3 and 5)
}

// MedianFloat averages the two middle values when the count is even. Median itself
// cannot, because T is only cmp.Ordered and averaging strings is not a thing.
func ExampleMedianFloat() {
	m := heap.NewMedianStream[int]()

	for _, v := range []int{1, 2, 3, 4} {
		m.Add(v)
		got, _ := heap.MedianFloat(m)
		fmt.Printf("%v ", got)
	}
	fmt.Println()

	// Output:
	// 1 1.5 2 2.5
}
