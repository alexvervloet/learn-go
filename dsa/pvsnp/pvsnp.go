// Package pvsnp demonstrates what makes a problem hard, using the two standard
// examples: subset sum and the travelling salesman.
//
// # The distinction that actually matters
//
// P and NP are not "easy" and "hard". They are about two different questions:
//
//	P    can you FIND an answer in polynomial time
//	NP   can you CHECK a proposed answer in polynomial time
//
// Every problem here is in NP, and for all of them checking is trivial. Hand me a
// subset and I will add it up in O(n). Hand me a tour and I will measure it in
// O(n). What nobody knows how to do is find them without, in effect, trying
// everything.
//
// That asymmetry is the whole subject, and it is why Verify functions sit next to
// the solvers below. The verifier is the definition of the problem; the solver is
// the part we cannot do quickly.
//
// # What the numbers look like
//
//	n       2^n              n!
//	10      1,024            3,628,800
//	20      1,048,576        2.4 x 10^18
//	30      1,073,741,824    2.7 x 10^32
//	50      1.1 x 10^15      3.0 x 10^64
//
// At a billion operations a second, 2^50 takes a fortnight and 50! takes longer
// than the universe has existed. This is not "slow", it is a different kind of
// thing from slow, and the benchmarks in this package are capped at the sizes
// where each approach stops finishing.
//
// # Pseudo-polynomial is the subtle part
//
// SubsetSumDP runs in O(n * target) and looks like it settles the question. It
// does not, and the reason is worth getting right: the input SIZE is the number of
// bits needed to write it down, and a target of one billion is 30 bits. O(n *
// target) is therefore O(n * 2^bits), which is exponential in the input size. A
// 30-bit target is instant and a 200-bit target is not, even though writing it
// down takes 25 bytes. See TestPseudoPolynomialBlowup.
package pvsnp

import (
	"errors"
	"math/bits"
)

// ErrNoSolution means no answer exists, which is different from an answer existing
// and not having been found.
var ErrNoSolution = errors.New("pvsnp: no solution")

// Subset sum
// ==========
//
// Given a set of numbers and a target, is there a subset that adds up to the
// target exactly? One of Karp's original 21 NP-complete problems, and the easiest
// to state.

// VerifySubsetSum reports whether the given indices select a subset of numbers
// summing to target.
//
// O(n), and this is the definition of the problem. Everything below is an attempt
// to find what this function checks.
func VerifySubsetSum(numbers []int, indices []int, target int) bool {
	seen := make(map[int]bool, len(indices))
	sum := 0

	for _, i := range indices {
		if i < 0 || i >= len(numbers) {
			return false // not a valid selection
		}
		if seen[i] {
			return false // the same element twice is not a subset
		}
		seen[i] = true

		sum += numbers[i]
	}

	return sum == target
}

// SubsetSumBrute tries every subset and returns the indices of one that works.
//
// O(2^n) time, O(1) extra space. Every subset of n elements corresponds to an
// n-bit number, so counting from 0 to 2^n-1 enumerates them all, and bit i of the
// counter says whether element i is in.
//
// That correspondence is the reason this is the natural brute force, and the reason
// n is limited to 62: beyond that the counter does not fit in a uint64. That limit
// is not the real constraint. At n=40 this needs a trillion iterations.
func SubsetSumBrute(numbers []int, target int) ([]int, error) {
	if len(numbers) > 62 {
		return nil, errors.New("pvsnp: too many elements to enumerate")
	}

	for mask := uint64(0); mask < 1<<len(numbers); mask++ {
		sum := 0

		// Walk only the SET bits rather than all n. TrailingZeros finds the next
		// one and mask&(mask-1) clears it, so this loop runs popcount(mask) times
		// instead of n. On average that halves the work, which buys one extra
		// element of n.
		for m := mask; m != 0; m &= m - 1 {
			sum += numbers[bits.TrailingZeros64(m)]
		}

		if sum != target {
			continue
		}

		return indicesOf(mask), nil
	}

	return nil, ErrNoSolution
}

// indicesOf returns the positions of the set bits in mask, ascending.
func indicesOf(mask uint64) []int {
	out := make([]int, 0, bits.OnesCount64(mask))
	for m := mask; m != 0; m &= m - 1 {
		out = append(out, bits.TrailingZeros64(m))
	}
	return out
}

// SubsetSumDP returns the indices of a subset summing to target, using dynamic
// programming.
//
// O(n * target) time and space, which for a small target is fast and for a large
// one is not. It requires non-negative numbers: the table is indexed by
// reachable sum, and a negative number would mean reaching a sum by going
// backwards through indices the table has already finalised.
//
// This does NOT put subset sum in P. See the package doc on pseudo-polynomial, and
// TestPseudoPolynomialBlowup for the measurement.
func SubsetSumDP(numbers []int, target int) ([]int, error) {
	if target < 0 {
		return nil, ErrNoSolution
	}
	for _, v := range numbers {
		if v < 0 {
			return nil, errors.New("pvsnp: SubsetSumDP requires non-negative numbers")
		}
	}

	// from[s] is the index of the element that first reached sum s, plus one, so
	// that zero means unreachable. Storing the element rather than a bool is what
	// makes the subset recoverable instead of only its existence.
	from := make([]int, target+1)
	reachable := make([]bool, target+1)
	reachable[0] = true

	for i, v := range numbers {
		if v == 0 || v > target {
			continue
		}

		// Descending, and this is the one line that matters. Ascending would let
		// element i be used twice: reaching sum s with it, then reaching s+v from
		// the s it just created. Descending means every sum is built from the
		// table as it was before this element was considered.
		for s := target; s >= v; s-- {
			if reachable[s] || !reachable[s-v] {
				continue
			}
			reachable[s] = true
			from[s] = i + 1
		}
	}

	if !reachable[target] {
		return nil, ErrNoSolution
	}

	// Walk the chain backwards to recover the subset.
	var out []int
	for s := target; s > 0; {
		i := from[s] - 1
		out = append(out, i)
		s -= numbers[i]
	}

	// Reverse so the indices come back ascending, which the verifier does not care
	// about and a reader does.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}

	return out, nil
}

// SubsetSumCount returns how many subsets sum to target.
//
// Counting is not easier than finding, and for some problems it is strictly harder:
// counting solutions is #P-complete, a class above NP. Here the same table does it,
// because the sums compose additively.
func SubsetSumCount(numbers []int, target int) int {
	if target < 0 {
		return 0
	}

	ways := make([]int, target+1)
	ways[0] = 1

	for _, v := range numbers {
		if v > target {
			continue
		}
		for s := target; s >= v; s-- {
			ways[s] += ways[s-v]
		}
	}

	return ways[target]
}
