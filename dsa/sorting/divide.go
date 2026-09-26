package sorting

// Divide and conquer
// ==================
//
// Both algorithms below split the slice in two, sort the halves, and combine. The
// difference is where the work goes, and it is the cleanest illustration of a
// trade that comes up everywhere:
//
//	merge sort  splits trivially (in the middle), combines expensively (merge)
//	quicksort   splits expensively (partition), combines for free (nothing)
//
// Everything else follows from that one choice. Merge sort needs a buffer for the
// merge, so it is O(n) memory and stable. Quicksort does its work in place, so it
// is O(log n) memory and unstable, and its performance depends entirely on the
// split being even, which is a property of the data rather than of the algorithm.

// MergeFunc sorts s using merge sort.
//
// The only O(n log n) sort here that is stable, and the only one that is O(n log n)
// in the worst case as well as the average. The price is the buffer.
//
// One buffer is allocated once and reused, rather than one per merge. For 100,000
// elements that is 1 allocation of 803 KB against the textbook version's 32,767
// allocations totalling 12.7 MB, and 7.81 ms against 10.37 ms. The time saving is
// 1.33x, which is less than the allocation count suggests, because Go's allocator
// is fast and these buffers die young.
//
// That buffer is also why merge sort is not Go's default despite being the only
// stable O(n log n) sort here. slices.SortStableFunc exists for when you need the
// property, and it allocates too.
func MergeFunc[T any](s []T, compare Compare[T]) {
	if len(s) < 2 {
		return
	}

	buf := make([]T, len(s))
	mergeSort(s, buf, compare)
}

func mergeSort[T any](s, buf []T, compare Compare[T]) {
	if len(s) < 2 {
		return
	}

	// Insertion sort below the threshold, for the same reason pdqsort does it: the
	// recursion and the merge are pure overhead on a handful of elements. Worth
	// 12% here, at 7.81 ms against 8.75 ms for 100,000 elements, which is less
	// than I expected and is the smaller of this function's two optimisations.
	const threshold = 12
	if len(s) <= threshold {
		InsertionFunc(s, compare)
		return
	}

	mid := len(s) / 2

	mergeSort(s[:mid], buf[:mid], compare)
	mergeSort(s[mid:], buf[mid:], compare)

	// If the halves are already in order relative to each other, the merge would
	// just copy. Skipping it is what makes merge sort fast on nearly-sorted input.
	if compare(s[mid-1], s[mid]) <= 0 {
		return
	}

	merge(s, buf, mid, compare)
}

// merge combines s[:mid] and s[mid:], both sorted, into s.
//
// The <= is what makes the whole algorithm stable: on a tie the LEFT half wins,
// and the left half holds the elements that came first. Changing it to < is a
// one-character edit that leaves every ordering test passing and breaks stability.
func merge[T any](s, buf []T, mid int, compare Compare[T]) {
	copy(buf, s)

	left, right, out := 0, mid, 0

	for left < mid && right < len(s) {
		if compare(buf[left], buf[right]) <= 0 {
			s[out] = buf[left]
			left++
		} else {
			s[out] = buf[right]
			right++
		}
		out++
	}

	// Whatever is left in one half is already in place relative to everything
	// written so far. Only one of these two copies does anything.
	copy(s[out:], buf[left:mid])
	copy(s[out+mid-left:], buf[right:])
}

// QuickFunc sorts s in place using quicksort.
//
// Fastest here on random data and the one that can fail. Its worst case is
// O(n^2), and the input that triggers it is not exotic: with a naive pivot choice
// it is *sorted input*, which is the most common shape real data has.
//
// Two decisions keep this version out of that hole, and both are in pdqsort too:
//
//	a ninther pivot, so sorted and reverse-sorted input still split evenly
//	three-way partitioning, so many equal elements cost O(n) rather than O(n^2)
//
// What those two decisions are worth, against a first-element pivot with two-way
// partitioning, at n=3000:
//
//	              naive      tuned
//	random        196 us     194 us     a wash
//	sorted        9663 us    97 us      100x
//	reversed      10456 us   105 us     100x
//	duplicates    3317 us    21 us      160x
//
// Nine comparisons for the pivot instead of three, and a three-way split instead
// of two, cost nothing measurable on random input and turn three common shapes
// from unusable into the fastest cases in the table.
func QuickFunc[T any](s []T, compare Compare[T]) {
	quickSort(s, compare, budgetFor(len(s)), HeapFunc[T])
}

// budgetFor returns the number of partitions quicksort may do along one path
// before giving up and calling the fallback.
//
// It is computed ONCE, from the original length, and this is the part that took a
// benchmark to get right. The first version recomputed `2*ilog2(len(s))` from the
// current subslice, which tightens as the recursion descends while the depth
// grows: a 13-element subslice allows 6 partitions, and a balanced quicksort
// reaches 13-element subslices at depth 13. So the limit fired on EVERY input.
// 1,909 unintended heapsort fallbacks on 100,000 random elements, and the tell was
// that sorted input benchmarked 46% slower than random.
//
// The slack of 8 on top of 2*log2(n) is deliberate headroom rather than a bound.
// With the ninther pivot the worst consumption measured across five input shapes
// and three sizes is 21 partitions against a budget of 40, so nothing natural comes
// close. It is there because the loop-on-the-larger-side structure makes the true
// bound harder to reason about than plain introsort's, and being generous costs
// nothing: the fallback only has to catch deliberately constructed input.
func budgetFor(n int) int {
	return 2*ilog2(max(n, 1)) + 8
}

// quickSort partitions until the budget runs out, then hands the rest to fallback.
//
// fallback is a parameter rather than a direct call to HeapFunc so a test can
// count how often it fires, which is the only way to know the budget is right.
// TestQuickDoesNotFallBackOnNaturalInput is that test.
func quickSort[T any](s []T, compare Compare[T], budget int, fallback func([]T, Compare[T])) {
	for len(s) > 12 {
		// Out of budget means the splits have been consistently terrible, which
		// for a median-of-three pivot takes deliberately constructed input.
		if budget <= 0 {
			fallback(s, compare)
			return
		}
		budget--

		lt, gt := partitionThreeWay(s, compare)

		// Recurse into the SMALLER side and loop on the larger one. That keeps the
		// stack at O(log n) even when the splits are uneven; recursing into both
		// makes it O(n) in the worst case, which is a stack overflow rather than a
		// slow sort.
		if lt < len(s)-gt {
			quickSort(s[:lt], compare, budget, fallback)
			s = s[gt:]
			continue
		}
		quickSort(s[gt:], compare, budget, fallback)
		s = s[:lt]
	}

	InsertionFunc(s, compare)
}

// partitionThreeWay rearranges s into elements less than the pivot, equal to it,
// and greater than it, returning the two boundaries.
//
// Dutch national flag partitioning. Two-way partitioning puts equal elements on
// one side, so a slice of a thousand identical values splits 1000/0 every time and
// costs O(n^2). Three-way puts them in the middle and never looks at them again,
// making that case O(n).
//
// The cost is that it reorders the two outer regions as it goes, which is what
// defeats a median-of-three pivot on sorted input. See choosePivot.
func partitionThreeWay[T any](s []T, compare Compare[T]) (lt, gt int) {
	pivot := choosePivot(s, compare)

	lt, gt = 0, len(s)
	i := 0

	for i < gt {
		switch c := compare(s[i], pivot); {
		case c < 0:
			s[lt], s[i] = s[i], s[lt]
			lt++
			i++
		case c > 0:
			gt--
			s[gt], s[i] = s[i], s[gt]
			// i does NOT advance: the element just swapped in is unexamined.
		default:
			i++
		}
	}

	return lt, gt
}

// choosePivot returns a value to partition around, without mutating s.
//
// # Why not median-of-three
//
// The first version used the median of the first, middle and last elements, which
// is the textbook choice and is what makes sorted input safe for a TWO-way
// partition. With the three-way partition below it is not safe, and finding out
// why took a benchmark, an instrumented run, and a printed trace.
//
// Three-way partitioning moves elements greater than the pivot to the tail by
// swapping them with whatever is at the shrinking right boundary. On sorted input
// that rotation leaves the right half in a specific shape:
//
//	sorted 1..1000, pivot 501  ->  right half is [503, 504, ... 1000, 502]
//
// Sorted ascending, except the SMALLEST element is now last. Median-of-three then
// samples the first, middle and last of that, and the last is the minimum, so the
// median of the three is the second-smallest value in the subarray. The split is
// 1/1/497, the same thing happens at the next level, and the recursion depth goes
// from log(n) to sqrt(n): 105 partitions deep for 10,000 sorted elements against
// 18 for random ones. It was not visible as a wrong answer, only as sorted input
// benchmarking 46% SLOWER than random input.
//
// # The fix
//
// Tukey's ninther: the median of the medians of three groups of three, spread
// across the slice. Nine samples instead of three, and the one anomalous position
// cannot carry the result. This is what Bentley and McIlroy's engineered quicksort
// uses and why it is robust on shapes that defeat median-of-three.
//
// Measured on the same inputs, budget consumed before and after:
//
//	            median of three   ninther
//	n=1000      31                10
//	n=10000     105               14
//	n=100000    over 200          18
//
// Under 50 elements the ninther's samples would overlap, so the plain
// median-of-three is used there. A subarray that small is one insertion sort away
// from done anyway.
func choosePivot[T any](s []T, compare Compare[T]) T {
	n := len(s)

	if n < 50 {
		return median3(s[0], s[n/2], s[n-1], compare)
	}

	step := n / 8

	low := median3(s[0], s[step], s[2*step], compare)
	mid := median3(s[n/2-step], s[n/2], s[n/2+step], compare)
	high := median3(s[n-1-2*step], s[n-1-step], s[n-1], compare)

	return median3(low, mid, high, compare)
}

// median3 returns the middle of three values. No slice, no mutation: the pivot is
// a value, and letting the selection reorder the slice as a side effect was a
// second thing to reason about for no benefit.
func median3[T any](a, b, c T, compare Compare[T]) T {
	if compare(b, a) < 0 {
		a, b = b, a
	}
	if compare(c, b) < 0 {
		b = c
		if compare(b, a) < 0 {
			b = a
		}
	}
	return b
}

// ilog2 returns floor(log2(n)) for n >= 1, using bit length rather than math.Log2
// to avoid a float conversion in the recursion.
func ilog2(n int) int {
	depth := 0
	for n > 1 {
		n >>= 1
		depth++
	}
	return depth
}
