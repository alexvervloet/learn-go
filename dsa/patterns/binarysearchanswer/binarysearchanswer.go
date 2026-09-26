// Package binarysearchanswer implements binary search over the ANSWER rather than over an
// array.
//
// # The tell
//
// "The minimum X such that…" or "the maximum X such that…". Two phrasings in particular
// almost always mean this pattern:
//
//	"minimise the maximum"   split an array so the largest part is as small as possible
//	"maximise the minimum"   place cows so the closest pair is as far apart as possible
//
// There is often no array to search at all. What is being bisected is the space of possible
// answers.
//
// # Why it works
//
// The same requirement as any binary search: a MONOTONIC PREDICATE. Here it takes the form
// "is X enough?", and the question qualifies when
//
//	if X works, every larger X works too      (for a minimum)
//	if X works, every smaller X works too     (for a maximum)
//
// Checking that property is the whole job of recognising the pattern. Once it holds, the
// answer is the boundary where the predicate flips, and dsa/searching.Partition finds it.
//
// # The three steps
//
//  1. Decide what the answer IS: a speed, a capacity, a distance, a number of days.
//  2. Write `feasible(x) bool`, which is usually a simple O(n) simulation.
//  3. Bisect the range of possible answers.
//
// Step 2 is where the work is, and it is deliberately dull: a loop that adds things up. The
// mistake is trying to be clever there, because the binary search is what supplies the
// cleverness.
//
// # Picking the bounds
//
// Getting these wrong is the most common bug in the pattern, and the rule is to make them
// obviously safe rather than tight:
//
//	lo   the smallest value that could conceivably work, often 1 or max(element)
//	hi   a value that certainly works, usually sum(elements) or max(elements)
//
// A `lo` that is too high silently returns a wrong answer, because the true boundary is
// outside the searched range and nothing detects that. `hi` too low does the same. Both
// bounds should be justifiable in one sentence each, and every function below says what
// its are and why.
package binarysearchanswer

import (
	"errors"

	"github.com/alexvervloet/learn-go/dsa/searching"
)

// ErrImpossible means no value in the searched range satisfies the predicate.
var ErrImpossible = errors.New("binarysearchanswer: no feasible value")

// Search returns the smallest x in [lo, hi] for which feasible is true.
//
// A thin wrapper over dsa/searching.SearchAnswer, kept so the pattern has a name in this
// package and so the error case is explicit rather than a sentinel index. feasible must be
// monotonic: false up to the boundary and true above it.
func Search(lo, hi int, feasible func(int) bool) (int, error) {
	if lo > hi {
		return 0, ErrImpossible
	}

	got, found := searching.SearchAnswer(lo, hi, feasible)
	if !found {
		return 0, ErrImpossible
	}

	return got, nil
}

// SearchMax returns the largest x in [lo, hi] for which feasible is true.
//
// The mirror image, and the way to get it is to bisect on the NEGATION and step back one,
// rather than writing a second search loop with the comparisons flipped. Flipping the
// comparisons is where the off-by-one lives.
func SearchMax(lo, hi int, feasible func(int) bool) (int, error) {
	if lo > hi {
		return 0, ErrImpossible
	}

	// The first x that does NOT work. Everything below it does.
	firstBad, found := searching.SearchAnswer(lo, hi, func(x int) bool { return !feasible(x) })

	if !found {
		return hi, nil // nothing fails, so the largest value in range is the answer
	}
	if firstBad == lo {
		return 0, ErrImpossible // not even the smallest value works
	}

	return firstBad - 1, nil
}

// MinEatingSpeed returns the smallest number of bananas per hour that finishes every pile
// within hours. Each hour is spent on one pile, and a partly eaten pile still costs a whole
// hour.
//
// The answer is a speed, and the predicate is "can I finish in time at this speed", which is
// monotonic because eating faster never takes longer.
//
// Bounds: 1 is the slowest speed that makes progress at all, and max(piles) certainly works
// whenever hours >= len(piles), because it finishes one pile per hour.
func MinEatingSpeed(piles []int, hours int) (int, error) {
	if len(piles) == 0 || hours < len(piles) {
		return 0, ErrImpossible // fewer hours than piles cannot work at any speed
	}

	largest := 0
	for _, p := range piles {
		largest = max(largest, p)
	}

	return Search(1, largest, func(speed int) bool {
		spent := 0
		for _, p := range piles {
			// Ceiling division: a pile of 7 at speed 3 takes 3 hours, not 2.
			spent += (p + speed - 1) / speed

			if spent > hours {
				return false // early exit, and it matters on large inputs
			}
		}
		return spent <= hours
	})
}

// MinShipCapacity returns the smallest ship capacity that ships every package within days.
// Packages must go in the given order and cannot be split.
//
// Bounds are the interesting part. lo is max(weights), not 1: a ship smaller than the
// heaviest package can never carry it, so every smaller capacity is infeasible and
// including them would be harmless but pointless. hi is sum(weights), which finishes in one
// day.
//
// Using lo = 1 would still be correct here, because the predicate is false for every
// capacity below max(weights). Using lo = max(weights) is the version that states why.
func MinShipCapacity(weights []int, days int) (int, error) {
	if len(weights) == 0 || days <= 0 {
		return 0, ErrImpossible
	}

	heaviest, total := 0, 0
	for _, w := range weights {
		heaviest = max(heaviest, w)
		total += w
	}

	return Search(heaviest, total, func(capacity int) bool {
		used, load := 1, 0

		for _, w := range weights {
			if load+w > capacity {
				used++
				load = 0
			}
			load += w

			if used > days {
				return false
			}
		}

		return used <= days
	})
}

// SplitArrayLargestSum splits nums into k contiguous parts and returns the smallest possible
// value of the largest part's sum.
//
// The textbook "minimise the maximum". It is the same problem as MinShipCapacity with the
// words changed, which is worth noticing: packages-per-day and array-parts are the same
// constraint.
func SplitArrayLargestSum(nums []int, k int) (int, error) {
	if k <= 0 || k > len(nums) {
		return 0, ErrImpossible
	}

	largest, total := 0, 0
	for _, v := range nums {
		largest = max(largest, v)
		total += v
	}

	return Search(largest, total, func(limit int) bool {
		parts, sum := 1, 0

		for _, v := range nums {
			if sum+v > limit {
				parts++
				sum = 0
			}
			sum += v

			if parts > k {
				return false
			}
		}

		return parts <= k
	})
}

// MaxMinDistance places count items among the given sorted positions so that the closest
// pair is as far apart as possible, and returns that distance.
//
// "Aggressive cows". The textbook "maximise the minimum", and the one that needs SearchMax:
// the predicate "can I place them all at least d apart" is true for small d and false for
// large d, which is the opposite direction from every other function here.
//
// Bounds: 0 always works when count <= len(positions), and the full span is the largest
// distance that could conceivably work.
func MaxMinDistance(sortedPositions []int, count int) (int, error) {
	if count < 2 || count > len(sortedPositions) {
		return 0, ErrImpossible
	}

	span := sortedPositions[len(sortedPositions)-1] - sortedPositions[0]

	return SearchMax(0, span, func(gap int) bool {
		placed, last := 1, sortedPositions[0]

		for _, p := range sortedPositions[1:] {
			if p-last < gap {
				continue
			}
			placed++
			last = p

			if placed >= count {
				return true
			}
		}

		return placed >= count
	})
}

// MinDaysForBouquets returns the earliest day on which m bouquets can be made, each using k
// ADJACENT flowers that have bloomed. bloom[i] is the day flower i blooms.
//
// The predicate is "can I make m bouquets by day d", monotonic because a flower that has
// bloomed stays bloomed.
//
// The overflow guard is not decoration: m*k can exceed the flower count by a lot, and
// without the check the search runs over a range where nothing is feasible and returns the
// wrong kind of answer.
func MinDaysForBouquets(bloom []int, bouquets, perBouquet int) (int, error) {
	if bouquets <= 0 || perBouquet <= 0 {
		return 0, ErrImpossible
	}
	if bouquets > len(bloom)/perBouquet {
		return 0, ErrImpossible // not enough flowers exist, whatever the day
	}

	earliest, latest := bloom[0], bloom[0]
	for _, d := range bloom {
		earliest = min(earliest, d)
		latest = max(latest, d)
	}

	return Search(earliest, latest, func(day int) bool {
		made, run := 0, 0

		for _, d := range bloom {
			if d > day {
				run = 0 // not bloomed, so the adjacent run breaks
				continue
			}

			run++
			if run == perBouquet {
				made++
				run = 0 // the flowers are used up

				if made >= bouquets {
					return true
				}
			}
		}

		return made >= bouquets
	})
}

// IntegerSquareRoot returns the largest x with x*x <= n.
//
// The simplest instance of the pattern, and worth having because it makes the shape obvious
// with no simulation to distract from it. The predicate is x*x <= n, true for small x and
// false for large, so it needs SearchMax.
//
// The bound is n rather than something tighter, because a bound that is obviously safe
// beats a clever one: the extra log2 iterations cost nothing.
func IntegerSquareRoot(n int) (int, error) {
	if n < 0 {
		return 0, ErrImpossible
	}
	if n < 2 {
		return n, nil
	}

	// The range starts at 1, not 0: the predicate divides by x, and n >= 2 here so the
	// answer is at least 1 anyway. Starting at 0 panics.
	return SearchMax(1, n, func(x int) bool {
		// x <= n/x rather than x*x <= n, because x*x overflows for large n and the
		// division does not. The same class of care as lo+(hi-lo)/2.
		return x <= n/x
	})
}

// MinMaxWorkload assigns the given jobs, in order, to workers so that no worker's total
// exceeds a limit, and returns the smallest limit that needs at most workers people.
//
// Yet another disguise for the same problem. Collected here because recognising that
// MinShipCapacity, SplitArrayLargestSum and this one are identical is more useful than
// knowing any one of them.
func MinMaxWorkload(jobs []int, workers int) (int, error) {
	return SplitArrayLargestSum(jobs, workers)
}
