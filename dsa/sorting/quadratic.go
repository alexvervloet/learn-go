package sorting

// The quadratic three
// ===================
//
// All O(n^2) average, all in place, and only one of them is ever the right
// answer. They are here because the differences between them are the differences
// that matter in every other algorithm too: where the comparisons happen, where
// the writes happen, and what the best case looks like.

// BubbleFunc sorts s in place by repeatedly swapping adjacent elements that are
// out of order.
//
// The early exit is the only interesting line. A pass with no swaps means the
// slice is sorted, so already-sorted input costs one pass, O(n). Without that
// check bubble sort has no best case at all and is strictly worse than insertion
// sort in every respect.
//
// After pass i the largest i elements have bubbled to the end and are final, so
// the inner loop shortens each time. Forgetting that turns 2 million comparisons
// into 4 million for n=2000, with the same answer.
func BubbleFunc[T any](s []T, compare Compare[T]) {
	for end := len(s); end > 1; end-- {
		swapped := false

		for i := 1; i < end; i++ {
			if compare(s[i-1], s[i]) <= 0 {
				continue
			}
			s[i-1], s[i] = s[i], s[i-1]
			swapped = true
		}

		if !swapped {
			return // already sorted, and this is the whole best case
		}
	}
}

// InsertionFunc sorts s in place by taking each element and sliding it back to
// where it belongs.
//
// The one quadratic sort worth having, and it is inside slices.Sort. Three
// properties earn it that:
//
//	O(n) on sorted input, because each element stops immediately
//	O(n*k) on input where nothing is more than k places out, which is what
//	  "nearly sorted" means and what real data usually is
//	almost no overhead, so it beats every O(n log n) algorithm below roughly
//	  twelve elements, which is why pdqsort switches to it there
//
// It is stable: the loop stops when it meets an element that is not GREATER than
// the one being placed, so equal elements never swap. Changing that > to a >= is
// a one-character edit that keeps every test passing except the stability one.
func InsertionFunc[T any](s []T, compare Compare[T]) {
	for i := 1; i < len(s); i++ {
		current := s[i]

		// Slide everything greater than current one place right.
		j := i - 1
		for ; j >= 0 && compare(s[j], current) > 0; j-- {
			s[j+1] = s[j]
		}

		s[j+1] = current
	}
}

// SelectionFunc sorts s in place by repeatedly finding the smallest remaining
// element and swapping it into position.
//
// Its one distinguishing property: exactly n-1 swaps, whatever the input, which
// is the fewest of any comparison sort. That matters only when a swap is far more
// expensive than a comparison, which for anything Go puts in a slice it is not.
// It has no best case; sorted input costs the same O(n^2) as reversed input,
// because the scan for the minimum cannot be cut short.
//
// It is NOT stable, and the reason is the swap: moving the minimum into place
// throws whatever was there across the slice, past any number of equal elements.
func SelectionFunc[T any](s []T, compare Compare[T]) {
	for i := range len(s) - 1 {
		smallest := i

		for j := i + 1; j < len(s); j++ {
			if compare(s[j], s[smallest]) < 0 {
				smallest = j
			}
		}

		// The long-range swap that costs stability.
		s[i], s[smallest] = s[smallest], s[i]
	}
}
