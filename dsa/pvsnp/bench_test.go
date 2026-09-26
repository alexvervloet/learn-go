package pvsnp

import (
	"math/rand/v2"
	"testing"
)

// These benchmarks exist to show the wall, not to compare implementations. Each one
// stops at the size where it stops finishing, and where that is is the whole point.

func BenchmarkSubsetSumBrute(b *testing.B) {
	r := rand.New(rand.NewPCG(1, 2))

	// 2^n, so each step up doubles. 26 takes about a tenth of a second and 30 takes
	// well over a second, which is where the default benchtime gives up.
	for _, n := range []int{10, 15, 20, 24, 26} {
		numbers := make([]int, n)
		for i := range numbers {
			numbers[i] = 1 + r.IntN(1000)
		}
		target := -1 // unreachable, so every subset is tried: the worst case

		b.Run("n="+itoa(n), func(b *testing.B) {
			for b.Loop() {
				_, sinkErr = SubsetSumBrute(numbers, target)
			}
		})
	}
}

func BenchmarkSubsetSumDP(b *testing.B) {
	r := rand.New(rand.NewPCG(1, 2))

	// Same n as the brute force, with a target of 100,000. The DP does not care
	// about n the way brute force does; it cares about the target.
	for _, n := range []int{10, 15, 20, 24, 26} {
		numbers := make([]int, n)
		for i := range numbers {
			numbers[i] = 1 + r.IntN(1000)
		}

		b.Run("n="+itoa(n), func(b *testing.B) {
			for b.Loop() {
				_, sinkErr = SubsetSumDP(numbers, 100_001)
			}
		})
	}
}

// The other axis, and the one that matters: the DP's cost is the target, not n. Each
// step here is 10x the target for the same 20 numbers.
func BenchmarkSubsetSumDPByTarget(b *testing.B) {
	r := rand.New(rand.NewPCG(1, 2))

	numbers := make([]int, 20)
	for i := range numbers {
		numbers[i] = 1 + r.IntN(1000)
	}

	for _, target := range []int{1_000, 10_000, 100_000, 1_000_000, 10_000_000} {
		b.Run("target="+itoa(target), func(b *testing.B) {
			for b.Loop() {
				_, sinkErr = SubsetSumDP(numbers, target+1)
			}
		})
	}
}

func BenchmarkTSPBrute(b *testing.B) {
	r := rand.New(rand.NewPCG(3, 4))

	// (n-1)!, so each step up multiplies by n-1. 11 is 3.6 million tours.
	for _, n := range []int{6, 8, 9, 10, 11} {
		d := randomPoints(r, n, 1000)

		b.Run("n="+itoa(n), func(b *testing.B) {
			for b.Loop() {
				_, sinkInt, sinkErr = TSPBrute(d)
			}
		})
	}
}

func BenchmarkTSPHeldKarp(b *testing.B) {
	r := rand.New(rand.NewPCG(3, 4))

	// n^2 * 2^n, so each step up roughly doubles. Where brute force dies at 11,
	// this reaches 18.
	for _, n := range []int{6, 8, 10, 12, 14, 16, 18} {
		d := randomPoints(r, n, 1000)

		b.Run("n="+itoa(n), func(b *testing.B) {
			for b.Loop() {
				_, sinkInt, sinkErr = TSPHeldKarp(d)
			}
		})
	}
}

// And the heuristics, which are polynomial and run at sizes the exact solvers cannot
// be mentioned in the same sentence as.
func BenchmarkTSPHeuristics(b *testing.B) {
	r := rand.New(rand.NewPCG(5, 7))

	for _, n := range []int{10, 100, 1000} {
		d := randomPoints(r, n, 10_000)
		nnTour, _ := TSPNearestNeighbour(d, 0)

		b.Run("n="+itoa(n)+"/nearest neighbour", func(b *testing.B) {
			for b.Loop() {
				_, sinkInt = TSPNearestNeighbour(d, 0)
			}
		})

		b.Run("n="+itoa(n)+"/best start", func(b *testing.B) {
			for b.Loop() {
				_, sinkInt = TSPNearestNeighbourBest(d)
			}
		})

		b.Run("n="+itoa(n)+"/2-opt", func(b *testing.B) {
			for b.Loop() {
				_, sinkInt = TwoOpt(d, nnTour)
			}
		})
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

var (
	sinkInt int
	sinkErr error
)
