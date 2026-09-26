// Package sorting implements the six sorting algorithms worth knowing, in one
// package rather than six, and benchmarks them against each other and against
// the standard library.
//
// # Use slices.Sort
//
// That is the real answer, and the benchmarks in this package say so in numbers:
// slices.Sort beats the best hand-written algorithm here by 2.5x on random input
// and by far more on the shapes real data has. It is pdqsort, which is quicksort
// with three additions that matter, and the reason to write the six first is that
// all three are visible in what the six get wrong:
//
//	insertion sort below a size threshold    because quicksort's recursion is
//	                                         pure overhead on 12 elements
//	a median-of-medians pivot on bad input   because a bad pivot is O(n^2)
//	a switch to heapsort if recursion is     because that is the guarantee
//	deep                                     quicksort cannot give
//
// # The table
//
//	algorithm   best        average     worst       memory    stable
//	bubble      O(n)        O(n^2)      O(n^2)      O(1)      yes
//	insertion   O(n)        O(n^2)      O(n^2)      O(1)      yes
//	selection   O(n^2)      O(n^2)      O(n^2)      O(1)      no
//	merge       O(n log n)  O(n log n)  O(n log n)  O(n)      yes
//	quick       O(n log n)  O(n log n)  O(n^2)      O(log n)  no
//	heap        O(n log n)  O(n log n)  O(n log n)  O(1)      no
//
// Three of those entries are the whole story. Insertion sort's O(n) best case is
// why it is inside every production sort. Quicksort's O(n^2) worst case is why
// pdqsort has a fallback. Merge sort's O(n) memory is why it is not the default
// despite being the only one of the three fast algorithms that is stable.
//
// # Stability
//
// A stable sort keeps equal elements in their original order. It matters whenever
// you sort twice: sort by name, then stably by department, and you have
// departments alphabetical with names alphabetical inside each. An unstable sort
// throws the first pass away.
//
// Every function here takes a compare function with the same signature as
// slices.SortFunc, so a call site can be swapped between them without any other
// change, and so stability is observable. The Ordered wrappers are the convenience
// form.
package sorting

import "cmp"

// Compare is the comparison function every algorithm here takes: negative if a
// sorts before b, zero if they are equivalent, positive otherwise.
//
// The same signature as slices.SortFunc, deliberately. cmp.Compare satisfies it
// for any ordered type.
type Compare[T any] func(a, b T) int

// Bubble sorts s in place using an ordered comparison.
func Bubble[T cmp.Ordered](s []T) { BubbleFunc(s, cmp.Compare) }

// Insertion sorts s in place using an ordered comparison.
func Insertion[T cmp.Ordered](s []T) { InsertionFunc(s, cmp.Compare) }

// Selection sorts s in place using an ordered comparison.
func Selection[T cmp.Ordered](s []T) { SelectionFunc(s, cmp.Compare) }

// Merge sorts s using an ordered comparison. It allocates O(n).
func Merge[T cmp.Ordered](s []T) { MergeFunc(s, cmp.Compare) }

// Quick sorts s in place using an ordered comparison.
func Quick[T cmp.Ordered](s []T) { QuickFunc(s, cmp.Compare) }

// Heap sorts s in place using an ordered comparison.
func Heap[T cmp.Ordered](s []T) { HeapFunc(s, cmp.Compare) }

// IsSorted reports whether s is in non-decreasing order under compare.
//
// slices.IsSorted exists for ordered types. This is the compare-function form,
// which slices.IsSortedFunc also provides; it is written out because every test
// in this package uses it and seeing the one-line definition makes clear what
// "sorted" is being taken to mean, including that equal elements are fine.
func IsSorted[T any](s []T, compare Compare[T]) bool {
	for i := 1; i < len(s); i++ {
		if compare(s[i-1], s[i]) > 0 {
			return false
		}
	}
	return true
}
