// Package searching implements binary search and its variants.
//
// # Binary search is not about sorted arrays
//
// That is the one idea in this package. Binary search needs a MONOTONIC
// PREDICATE: some question about an index whose answer is false for a while and
// then true forever, with no going back.
//
//	index      0      1      2      3      4      5      6
//	value      2      3      5      7     11     13     17
//	>= 7?    false  false  false  true   true   true   true
//	                               ^
//	                               the boundary, which is what we are finding
//
// A sorted array is one way to get such a predicate, and not the only one. Once
// you see it this way every variant below collapses into one function, Partition,
// and the rest are two-line wrappers. So does "find the smallest capacity that
// finishes the job in time", which involves no array at all.
//
// # The variants exist because of ties
//
// If every element is distinct there is one binary search and nothing to discuss.
// With duplicates there are four different questions, and conflating them is where
// the off-by-one bugs live:
//
//	LowerBound   first index with s[i] >= target      insertion point, ties first
//	UpperBound   first index with s[i] >  target      insertion point, ties last
//	First        index of the first equal element     LowerBound, then check
//	Last         index of the last equal element      UpperBound-1, then check
//
// UpperBound - LowerBound is the number of occurrences, which is why those two
// are the primitives and the other two are wrappers.
//
// # Use the standard library
//
// slices.BinarySearch and slices.BinarySearchFunc are LowerBound with a found
// flag, and sort.Search is Partition. This package exists to make the boundary
// explicit; in real code, call those.
package searching

import "cmp"

// Partition returns the smallest i in [0, n] for which pred(i) is true, or n if
// pred is never true.
//
// pred must be monotonic: false for every index below the boundary and true for
// every index at or above it. Passing a predicate that is not monotonic does not
// error, it returns a meaningless index, and that is the single most common way to
// misuse binary search.
//
// This is exactly sort.Search, written out because everything else in the package
// is one line on top of it and because the loop is where the off-by-one bugs are.
func Partition(n int, pred func(int) bool) int {
	// The invariant, which is the only way to keep this correct:
	//
	//	pred is false for every index < lo
	//	pred is true  for every index >= hi
	//
	// Both halves are vacuously true at the start, and the loop shrinks the gap
	// without ever breaking them. When lo == hi the answer is lo.
	lo, hi := 0, n

	for lo < hi {
		// lo + (hi-lo)/2, not (lo+hi)/2. The difference is overflow, and in Go
		// that is not the theoretical concern it is usually described as: see
		// TestMidpointOverflowIsReachable.
		mid := lo + (hi-lo)/2

		if pred(mid) {
			hi = mid // mid may be the boundary, so it stays in range
			continue
		}
		lo = mid + 1 // mid is not the boundary, so exclude it
	}

	return lo
}

// LowerBound returns the first index at which target could be inserted to keep s
// sorted, which is the index of the first element >= target.
//
// For a slice with no matching element it returns where one would go, including
// len(s) if target is larger than everything. That total-ness is what makes it the
// right primitive: a search that returns -1 for "absent" throws away the
// information the search already found.
func LowerBound[T cmp.Ordered](s []T, target T) int {
	return Partition(len(s), func(i int) bool { return s[i] >= target })
}

// UpperBound returns the first index of an element strictly greater than target.
func UpperBound[T cmp.Ordered](s []T, target T) int {
	return Partition(len(s), func(i int) bool { return s[i] > target })
}

// BinarySearch returns the index of target and whether it was found.
//
// Identical to slices.BinarySearch, including that the returned index is the
// insertion point when found is false.
func BinarySearch[T cmp.Ordered](s []T, target T) (int, bool) {
	i := LowerBound(s, target)
	return i, i < len(s) && s[i] == target
}

// First returns the index of the first element equal to target.
func First[T cmp.Ordered](s []T, target T) (int, bool) {
	return BinarySearch(s, target)
}

// Last returns the index of the last element equal to target.
func Last[T cmp.Ordered](s []T, target T) (int, bool) {
	i := UpperBound(s, target) - 1
	return i, i >= 0 && s[i] == target
}

// Count returns how many times target appears.
//
// Two binary searches, so O(log n) rather than the O(log n + k) a walk outwards
// from any match would cost. On a slice of a million identical values that is 40
// comparisons against a million.
func Count[T cmp.Ordered](s []T, target T) int {
	return UpperBound(s, target) - LowerBound(s, target)
}

// Range returns the half-open index range of the elements equal to target.
func Range[T cmp.Ordered](s []T, target T) (lo, hi int) {
	return LowerBound(s, target), UpperBound(s, target)
}

// Linear returns the index of the first element equal to target, or -1.
//
// Here as the baseline, and it is not always the loser. It has no precondition,
// it reads memory in the order the prefetcher expects, and for small slices it
// beats binary search outright. The crossover is in the benchmarks, and it is
// larger than most people guess.
func Linear[T comparable](s []T, target T) int {
	for i, v := range s {
		if v == target {
			return i
		}
	}
	return -1
}

// Exponential finds target by doubling a bound until it passes the target, then
// binary searching inside that bound.
//
// O(log i) where i is the target's position, rather than O(log n). Worth it in two
// cases: the target is usually near the front, or n is unknown, as with a sorted
// stream or a paginated API where you can ask for item k but not for the length.
//
// It is never much worse than plain binary search: the doubling costs at most
// log2(i) extra comparisons, and log2(i) <= log2(n).
func Exponential[T cmp.Ordered](s []T, target T) (int, bool) {
	if len(s) == 0 {
		return 0, false
	}
	if s[0] >= target {
		return 0, s[0] == target
	}

	// Double until the bound is at or past the target.
	bound := 1
	for bound < len(s) && s[bound] < target {
		bound *= 2
	}

	// The target, if present, is in (bound/2, min(bound, len-1)]. Searching the
	// whole prefix would work and would throw away everything the doubling
	// learned.
	lo := bound / 2
	hi := min(bound+1, len(s))

	window := s[lo:hi]
	i := LowerBound(window, target)

	return lo + i, i < len(window) && window[i] == target
}
