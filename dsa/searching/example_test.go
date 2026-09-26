package searching_test

import (
	"fmt"

	"github.com/alexvervloet/learn-go/dsa/searching"
)

// Partition is the whole package. Everything else is a wrapper that supplies a
// predicate.
func ExamplePartition() {
	// The first index at which the value is at least 7.
	s := []int{2, 3, 5, 7, 11, 13, 17}

	i := searching.Partition(len(s), func(i int) bool { return s[i] >= 7 })
	fmt.Println(i, s[i])

	// A predicate that is never true returns n, which is where the value would go.
	fmt.Println(searching.Partition(len(s), func(i int) bool { return s[i] > 100 }))

	// Output:
	// 3 7
	// 7
}

// With duplicates there are four different questions, and they have four different
// answers.
func ExampleLowerBound() {
	s := []int{1, 3, 3, 3, 5, 7, 7, 9}

	fmt.Println("LowerBound(3):", searching.LowerBound(s, 3))
	fmt.Println("UpperBound(3):", searching.UpperBound(s, 3))
	fmt.Println("Count(3):     ", searching.Count(s, 3))

	lo, hi := searching.Range(s, 3)
	fmt.Println("the run:      ", s[lo:hi])

	// Output:
	// LowerBound(3): 1
	// UpperBound(3): 4
	// Count(3):      3
	// the run:       [3 3 3]
}

// The returned index is useful even when nothing was found: it is where the value
// belongs. A search that returns -1 for absent throws that away.
func ExampleBinarySearch() {
	s := []int{10, 20, 30, 40}

	i, found := searching.BinarySearch(s, 30)
	fmt.Println(i, found)

	i, found = searching.BinarySearch(s, 35)
	fmt.Println(i, found, "-> insert here to stay sorted")

	i, found = searching.BinarySearch(s, 99)
	fmt.Println(i, found, "-> past the end")

	// Output:
	// 2 true
	// 3 false -> insert here to stay sorted
	// 4 false -> past the end
}

// A rotated slice is not sorted, and it is still binary searchable, because at any
// midpoint at least one half is sorted and a sorted half can be range-checked.
func ExampleSearchRotated() {
	s := []int{4, 5, 6, 7, 1, 2, 3}

	fmt.Println(searching.SearchRotated(s, 1))
	fmt.Println(searching.SearchRotated(s, 6))
	fmt.Println(searching.SearchRotated(s, 99))

	fmt.Println("rotation point:", searching.RotationPoint(s), "holding", s[searching.RotationPoint(s)])

	// Output:
	// 4
	// 2
	// -1
	// rotation point: 4 holding 1
}

// No order at all, and still O(log n). The predicate "am I descending yet" is false
// while climbing and true afterwards, which is all bisection needs.
func ExampleFindPeak() {
	fmt.Println(searching.FindPeak([]int{1, 3, 5, 4, 2}))
	fmt.Println(searching.FindPeak([]int{1, 2, 3, 4, 5}))
	fmt.Println(searching.FindPeak([]int{5, 4, 3, 2, 1}))

	// Output:
	// 2
	// 4
	// 0
}

// Binary search with no array. The question is monotonic, so it bisects.
func ExampleSearchAnswer() {
	// The smallest number of boats needed to move 1,000 passengers when each boat
	// holds 37 and must make at most 4 trips.
	needed := searching.SearchAnswer(1, 1000, func(boats int) bool {
		return boats*37*4 >= 1000
	})
	fmt.Println("boats:", needed)

	// Integer square root, which is the same shape.
	fmt.Println("isqrt(200):", searching.SearchAnswer(0, 200, func(x int) bool {
		return x*x >= 200
	}))

	// Output:
	// boats: 7
	// isqrt(200): 15
}

// Exponential search doubles a bound before bisecting, so its cost depends on where
// the target is rather than how long the slice is.
func ExampleExponential() {
	s := make([]int, 1_000_000)
	for i := range s {
		s[i] = i * 2
	}

	i, found := searching.Exponential(s, 20)
	fmt.Println(i, found)

	i, found = searching.Exponential(s, 21)
	fmt.Println(i, found)

	// Output:
	// 10 true
	// 11 false
}
