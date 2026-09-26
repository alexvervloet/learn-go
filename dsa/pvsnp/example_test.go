package pvsnp_test

import (
	"errors"
	"fmt"

	"github.com/alexvervloet/learn-go/dsa/pvsnp"
)

// The asymmetry the whole package is about: checking an answer is O(n), and finding
// one is not known to be.
func ExampleVerifySubsetSum() {
	numbers := []int{3, 34, 4, 12, 5, 2}

	// Checking a proposed answer: instant, and this is the definition of NP.
	fmt.Println(pvsnp.VerifySubsetSum(numbers, []int{0, 2, 4}, 12)) // 3+4+5
	fmt.Println(pvsnp.VerifySubsetSum(numbers, []int{0, 1}, 12))

	// A subset cannot use the same element twice.
	fmt.Println(pvsnp.VerifySubsetSum(numbers, []int{0, 0, 0, 0}, 12))

	// Output:
	// true
	// false
	// false
}

func ExampleSubsetSumBrute() {
	numbers := []int{3, 34, 4, 12, 5, 2}

	indices, err := pvsnp.SubsetSumBrute(numbers, 9)
	if err != nil {
		fmt.Println(err)
		return
	}

	sum := 0
	for _, i := range indices {
		sum += numbers[i]
	}
	fmt.Println(indices, "sums to", sum)

	// 1 is not reachable from any subset of these numbers.
	_, err = pvsnp.SubsetSumBrute(numbers, 1)
	fmt.Println(errors.Is(err, pvsnp.ErrNoSolution))

	// Output:
	// [2 4] sums to 9
	// true
}

// The DP is O(n * target), which is fast for a small target and is not polynomial:
// the input SIZE is the number of bits in the target, not its value.
func ExampleSubsetSumDP() {
	numbers := []int{3, 34, 4, 12, 5, 2}

	indices, err := pvsnp.SubsetSumDP(numbers, 9)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(indices)

	// It refuses negative numbers, because the table is built in one pass over
	// increasing sums. Brute force has no such restriction.
	_, err = pvsnp.SubsetSumDP([]int{1, -2, 3}, 2)
	fmt.Println(err)

	// Output:
	// [2 4]
	// pvsnp: SubsetSumDP requires non-negative numbers
}

// Counting solutions is a different question from finding one, and in general it is
// harder: counting is #P-complete, a class above NP.
func ExampleSubsetSumCount() {
	fmt.Println(pvsnp.SubsetSumCount([]int{1, 2, 3, 4, 5}, 5)) // {5}, {1,4}, {2,3}
	fmt.Println(pvsnp.SubsetSumCount([]int{1, 1, 1}, 2))       // three ways
	fmt.Println(pvsnp.SubsetSumCount([]int{2, 4, 6}, 7))

	// Output:
	// 3
	// 3
	// 0
}

// What the exponents mean. -1 is returned when the answer no longer fits in an int,
// which for tours happens at n=22.
func ExampleSubsetCount() {
	for _, n := range []int{10, 20, 30, 62, 63} {
		fmt.Printf("n=%-3d subsets=%-21d tours=%d\n", n, pvsnp.SubsetCount(n), pvsnp.TourCount(n, false))
	}

	// Output:
	// n=10  subsets=1024                  tours=362880
	// n=20  subsets=1048576               tours=121645100408832000
	// n=30  subsets=1073741824            tours=-1
	// n=62  subsets=4611686018427387904   tours=-1
	// n=63  subsets=-1                    tours=-1
}

func ExampleDistances_VerifyTour() {
	// A 3-4-5 triangle.
	d := pvsnp.Points([][2]int{{0, 0}, {3, 0}, {3, 4}})

	length, ok := d.VerifyTour([]int{0, 1, 2})
	fmt.Println(length, ok)

	// A tour is a cycle, so rotating and reversing cost the same.
	fmt.Println(d.TourLength([]int{1, 2, 0}), d.TourLength([]int{2, 1, 0}))

	// What VerifyTour does NOT check is whether this is the SHORTEST tour. Nothing
	// can check that in polynomial time, which is the difference between
	// NP-complete and NP-hard.
	_, ok = d.VerifyTour([]int{0, 0, 1})
	fmt.Println("repeat accepted:", ok)

	// Output:
	// 12 true
	// 12 12
	// repeat accepted: false
}

// Two exact solvers, the same answer, wildly different cost. Held-Karp collapses
// n! sequences into 2^n subsets by noticing that the cost of the best path over a
// SET does not depend on the order it visited them in.
func ExampleTSPHeldKarp() {
	d := pvsnp.Points([][2]int{{0, 0}, {4, 0}, {4, 3}, {2, 6}, {0, 3}})

	_, brute, _ := pvsnp.TSPBrute(d)
	tour, heldKarp, _ := pvsnp.TSPHeldKarp(d)

	fmt.Println("brute force:", brute)
	fmt.Println("held-karp:  ", heldKarp)
	fmt.Println("agree:", brute == heldKarp)

	length, ok := d.VerifyTour(tour)
	fmt.Println("tour verifies:", ok, "at length", length)

	// Output:
	// brute force: 18
	// held-karp:   18
	// agree: true
	// tour verifies: true at length 18
}

// Both exact solvers refuse rather than run forever, and where they refuse is the
// interesting part.
func ExampleTSPBrute() {
	_, _, err := pvsnp.TSPBrute(make(pvsnp.Distances, 13))
	fmt.Println(errors.Is(err, pvsnp.ErrTooLarge))

	_, _, err = pvsnp.TSPHeldKarp(make(pvsnp.Distances, 21))
	fmt.Println(errors.Is(err, pvsnp.ErrTooLarge))

	// Output:
	// true
	// true
}

// When exact is impossible the question becomes how wrong you will settle for. On
// this instance nearest neighbour is 47% over optimal and 2-opt recovers all of it.
func ExampleTwoOpt() {
	d := pvsnp.Points([][2]int{
		{4, 35}, {16, 32}, {15, 44}, {43, 8}, {7, 14}, {19, 7}, {23, 25}, {2, 37},
	})

	_, optimal, _ := pvsnp.TSPBrute(d)

	nnTour, nn := pvsnp.TSPNearestNeighbour(d, 0)
	_, improved := pvsnp.TwoOpt(d, nnTour)

	fmt.Println("optimal:          ", optimal)
	fmt.Println("nearest neighbour:", nn)
	fmt.Println("after 2-opt:      ", improved)

	// Output:
	// optimal:           125
	// nearest neighbour: 184
	// after 2-opt:       125
}

// The cheapest possible improvement to a greedy algorithm: run it from every
// starting point and keep the best.
func ExampleTSPNearestNeighbourBest() {
	d := pvsnp.Points([][2]int{
		{4, 35}, {16, 32}, {15, 44}, {43, 8}, {7, 14}, {19, 7}, {23, 25}, {2, 37},
	})

	_, fromZero := pvsnp.TSPNearestNeighbour(d, 0)
	_, best := pvsnp.TSPNearestNeighbourBest(d)

	fmt.Println("from city 0:", fromZero)
	fmt.Println("best start: ", best)

	// Output:
	// from city 0: 184
	// best start:  135
}
