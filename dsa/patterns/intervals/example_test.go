package intervals_test

import (
	"fmt"

	"github.com/alexvervloet/learn-go/dsa/patterns/intervals"
)

func iv(start, end int) intervals.Interval {
	return intervals.Interval{Start: start, End: end}
}

// The decision the whole package rests on. A meeting from 1 to 2 and one from 2 to 3 do not
// conflict; they do abut.
func ExampleInterval_Overlaps() {
	a, b := iv(1, 2), iv(2, 3)

	fmt.Println("overlaps:", a.Overlaps(b))
	fmt.Println("touches: ", a.Touches(b))
	fmt.Println("contains 2:", a.Contains(2))
	fmt.Println("contains 1:", a.Contains(1))

	fmt.Println("genuine overlap:", iv(1, 3).Overlaps(iv(2, 4)))

	// Output:
	// overlaps: false
	// touches:  true
	// contains 2: false
	// contains 1: true
	// genuine overlap: true
}

// Sort by start, then each interval either extends the current one or begins a new one.
// Touching intervals merge, which is why the walk uses Touches and not Overlaps.
func ExampleMerge() {
	fmt.Println(intervals.Merge([]intervals.Interval{
		iv(1, 3), iv(2, 6), iv(8, 10), iv(15, 18),
	}))

	// [1,2) and [2,3) abut, so they merge even though they do not overlap.
	fmt.Println(intervals.Merge([]intervals.Interval{iv(1, 2), iv(2, 3)}))

	// Output:
	// [{1 6} {8 10} {15 18}]
	// [{1 3}]
}

// Into an already-sorted list this is O(n): copy what is before, absorb what touches, copy
// what is after. Appending and re-merging is O(n log n) and the usual first answer.
func ExampleInsert() {
	sorted := []intervals.Interval{
		iv(1, 2), iv(3, 5), iv(6, 7), iv(8, 10), iv(12, 16),
	}

	fmt.Println(intervals.Insert(sorted, iv(4, 8)))

	// Output:
	// [{1 2} {3 10} {12 16}]
}

// After sorting by start, only ADJACENT pairs need checking: if any two overlap, two
// adjacent ones do. That is what turns O(n²) into O(n log n).
func ExampleAnyOverlap() {
	canAttend := []intervals.Interval{iv(7, 10), iv(2, 4)}
	cannot := []intervals.Interval{iv(0, 30), iv(5, 10), iv(15, 20)}
	backToBack := []intervals.Interval{iv(1, 2), iv(2, 3), iv(3, 4)}

	_, _, a := intervals.AnyOverlap(canAttend)
	_, _, b := intervals.AnyOverlap(cannot)
	_, _, c := intervals.AnyOverlap(backToBack)

	fmt.Println("no conflict: ", a)
	fmt.Println("conflict:    ", b)
	fmt.Println("back to back:", c)

	// Output:
	// no conflict:  false
	// conflict:     true
	// back to back: false
}

// A sweep: +1 at each start, -1 at each end, and the peak of the running total is the
// answer. Ends must sort before starts at the same instant, or a room freed at 10 is not
// counted as available at 10.
func ExampleMinRooms() {
	fmt.Println(intervals.MinRooms([]intervals.Interval{
		iv(0, 30), iv(5, 10), iv(15, 20),
	}))

	// Back-to-back meetings need one room, which is the tie-break doing its job.
	fmt.Println(intervals.MinRooms([]intervals.Interval{
		iv(1, 2), iv(2, 3), iv(3, 4),
	}))

	// Output:
	// 2
	// 1
}

// Sort by END and always take the earliest finisher. The two plausible alternatives are
// both wrong, and here is one counterexample for each.
func ExampleMaxNonOverlapping() {
	// Sorting by start keeps [0,10) and blocks both others.
	longEarly := []intervals.Interval{iv(0, 10), iv(1, 2), iv(3, 4)}
	fmt.Println("by end:", intervals.MaxNonOverlapping(longEarly))

	// Taking the shortest first keeps [4,6) and blocks both others.
	shortestBlocks := []intervals.Interval{iv(1, 5), iv(4, 6), iv(5, 9)}
	fmt.Println("by end:", intervals.MaxNonOverlapping(shortestBlocks))

	// "Keep the most" and "remove the fewest" are one question.
	fmt.Println("removals:", intervals.MinRemovals(longEarly))

	// Output:
	// by end: [{1 2} {3 4}]
	// by end: [{1 5} {5 9}]
	// removals: 1
}

// Two pointers over sorted lists. Advance whichever ends first: it has nothing left to
// offer the other list.
func ExampleIntersection() {
	a := []intervals.Interval{iv(0, 2), iv(5, 10), iv(13, 23), iv(24, 25)}
	b := []intervals.Interval{iv(1, 5), iv(8, 12), iv(15, 24), iv(25, 26)}

	fmt.Println(intervals.Intersection(a, b))

	// Output:
	// [{1 2} {8 10} {15 23}]
}

// "Which parts of this window are still free."
func ExampleSubtract() {
	day := iv(9, 17)
	meetings := []intervals.Interval{iv(10, 11), iv(13, 14), iv(13, 15)}

	fmt.Println("free:", intervals.Subtract(day, meetings))
	fmt.Println("busy:", intervals.TotalCovered(meetings), "hours")

	// Output:
	// free: [{9 10} {11 13} {15 17}]
	// busy: 3 hours
}
