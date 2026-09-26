package sorting

// HeapFunc sorts s in place using heapsort.
//
// The algorithm nobody's favourite and everybody's fallback. O(n log n)
// guaranteed, in O(1) memory, with no input that makes it slow. Quicksort beats
// it on average and can fail; merge sort matches its guarantee and needs a
// buffer. Heapsort gives up the average case to be the one with no bad case,
// which is exactly why pdqsort keeps it as the bail-out.
//
// It is measurably slower than quicksort on random data, and the reason is cache
// behaviour rather than comparison count. Sifting jumps from index i to 2i+1,
// which for a large slice is a different cache line every level. Quicksort's
// partition walks two pointers linearly through memory, which is the access
// pattern hardware is built for.
//
// # How it works
//
// A binary heap stored in the slice itself, with no pointers: the children of
// index i are at 2i+1 and 2i+2. The whole algorithm is two phases:
//
//	build a MAX-heap, so the largest element is at index 0
//	repeatedly swap index 0 with the last unsorted position and re-sift
//
// The second phase grows a sorted region at the end of the slice, which is why
// the heap has to be a max-heap for an ascending sort. It is not stable, for the
// same reason selection sort is not: elements move across the whole slice.
func HeapFunc[T any](s []T, compare Compare[T]) {
	// Build the heap from the last parent backwards. Starting from the leaves is
	// pointless, since a leaf is already a valid heap, and that is why this loop
	// starts at len/2-1 rather than at the end.
	//
	// Building this way is O(n), not O(n log n): most nodes are near the leaves
	// and sift down only a level or two. Inserting elements one at a time into a
	// growing heap would be the O(n log n) version.
	for parent := len(s)/2 - 1; parent >= 0; parent-- {
		siftDown(s, parent, len(s), compare)
	}

	for end := len(s) - 1; end > 0; end-- {
		// The largest remaining element goes to its final position.
		s[0], s[end] = s[end], s[0]

		// Re-heapify what is left, which now excludes the sorted tail.
		siftDown(s, 0, end, compare)
	}
}

// siftDown restores the heap property at root, considering only s[:size].
//
// The size parameter is what lets the sorted region and the heap share one slice.
func siftDown[T any](s []T, root, size int, compare Compare[T]) {
	for {
		largest := root
		left, right := 2*root+1, 2*root+2

		if left < size && compare(s[left], s[largest]) > 0 {
			largest = left
		}
		if right < size && compare(s[right], s[largest]) > 0 {
			largest = right
		}

		if largest == root {
			return // the heap property holds from here down
		}

		s[root], s[largest] = s[largest], s[root]
		root = largest
	}
}
