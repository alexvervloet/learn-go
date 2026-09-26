package searching

import "cmp"

// Searching things that are not sorted
// ====================================
//
// The predicate framing pays off here. None of the slices below is sorted, and all
// three are binary searchable, because each one still has a monotonic property to
// bisect on.

// SearchRotated returns the index of target in a sorted slice that has been
// rotated, or -1.
//
//	original  [1 2 3 4 5 6 7]
//	rotated   [4 5 6 7 1 2 3]     rotated left by 3
//
// The slice is not sorted, so LowerBound is useless on it. What is still true is
// that at any midpoint, at least ONE of the two halves is properly sorted, and a
// sorted half can be tested for containment in constant time. So each step
// discards half the slice, exactly as binary search does.
//
// Duplicates break this: for [2 2 2 2 2 0 2], the midpoint, first and last
// elements are all equal and there is no way to tell which half is sorted. The
// worst case becomes O(n), and that is why this takes cmp.Ordered and not a
// promise of distinctness.
func SearchRotated[T cmp.Ordered](s []T, target T) int {
	lo, hi := 0, len(s)-1

	for lo <= hi {
		mid := lo + (hi-lo)/2

		if s[mid] == target {
			return mid
		}

		// The ambiguous case has to be checked FIRST. When all three of lo, mid and
		// hi hold the same value there is no way to tell which half is sorted, so
		// shrink by one from each end. This is the O(n) path, and it only happens
		// with duplicates.
		if s[lo] == s[mid] && s[mid] == s[hi] {
			lo++
			hi--
			continue
		}

		// `<=`, not `<`. When lo == mid, which happens on every two-element window,
		// s[lo] and s[mid] are the same element and `<` is false for both branches.
		// The first version used `<` and fell through to the shrink-by-one path,
		// which silently returned -1 for the present target in [3, 0].
		if s[lo] <= s[mid] {
			// The left half is sorted, so containment is one range check.
			if target >= s[lo] && target < s[mid] {
				hi = mid - 1
				continue
			}
			lo = mid + 1
			continue
		}

		// Otherwise the right half is sorted.
		if target > s[mid] && target <= s[hi] {
			lo = mid + 1
			continue
		}
		hi = mid - 1
	}

	return -1
}

// RotationPoint returns the index of the smallest element in a rotated sorted
// slice, and 0 for a slice that was not rotated.
//
// For a slice rotated LEFT by k that index is len(s)-k, not k, which is easy to get
// backwards when writing the test rather than the function.
//
// The monotonic predicate here is "is s[i] <= the last element", which is false
// across the first, larger run and true across the second:
//
//	values     4  5  6  7  1  2  3
//	<= s[n-1]  F  F  F  F  T  T  T
//	                       ^
//
// Comparing against the LAST element rather than the first is what makes it
// monotonic, and swapping the two is a real bug that passes on every rotated input
// and fails on an unrotated one.
func RotationPoint[T cmp.Ordered](s []T) int {
	if len(s) == 0 {
		return 0
	}

	last := s[len(s)-1]
	return Partition(len(s), func(i int) bool { return s[i] <= last })
}

// FindPeak returns the index of any element greater than both its neighbours,
// treating out-of-range neighbours as negative infinity.
//
// There is no sorted order here at all, and it is still O(log n). The predicate is
// "is s[i] > s[i+1]", which is false while climbing and true once descending. A
// peak has to exist between the two: the slice starts out climbing (index 0 beats
// its imaginary left neighbour) and ends descending.
//
// This is the clearest demonstration that binary search is about the predicate. A
// linear scan is the obvious solution and the array offers no ordering to exploit,
// yet bisecting works.
func FindPeak[T cmp.Ordered](s []T) int {
	if len(s) == 0 {
		return -1
	}

	return Partition(len(s)-1, func(i int) bool { return s[i] > s[i+1] })
}

// SearchAnswer returns the smallest value in [lo, hi] for which ok is true, or hi+1
// if none is.
//
// The same search with no array involved. ok must be monotonic in the same sense:
// false up to some threshold and true above it. "The smallest number of ships that
// can carry this cargo in five days" and "the lowest rate limit that keeps the
// queue from growing" are both this function.
//
// See patterns/binarysearchanswer for the worked problems; this is the primitive.
func SearchAnswer(lo, hi int, ok func(int) bool) int {
	// Shift into Partition's [0, n) index space rather than duplicating the loop.
	// Getting the two inclusive bounds right is the only fiddly part, and doing it
	// once here is better than once per problem.
	n := hi - lo + 1
	if n <= 0 {
		return hi + 1
	}

	i := Partition(n, func(i int) bool { return ok(lo + i) })

	return lo + i
}
