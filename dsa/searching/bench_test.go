package searching

import (
	"math/rand/v2"
	"slices"
	"sort"
	"testing"
)

func sortedInts(n int) []int {
	s := make([]int, n)
	for i := range s {
		s[i] = i * 2
	}
	return s
}

// The crossover. Binary search is O(log n) and linear search is O(n), and for small
// n the linear one still wins, because it reads memory in the order the prefetcher
// expects and has no branch that depends on a comparison result.
func BenchmarkCrossover(b *testing.B) {
	for _, n := range []int{4, 8, 16, 32, 64, 128, 256, 1024} {
		s := sortedInts(n)

		// Search for every element in turn, so the average position is the middle.
		b.Run(itoa(n)+"/linear", func(b *testing.B) {
			for i := 0; b.Loop(); i++ {
				sinkInt = Linear(s, (i%n)*2)
			}
		})

		b.Run(itoa(n)+"/binary", func(b *testing.B) {
			for i := 0; b.Loop(); i++ {
				sinkInt, sinkBool = BinarySearch(s, (i%n)*2)
			}
		})

		b.Run(itoa(n)+"/slices.BinarySearch", func(b *testing.B) {
			for i := 0; b.Loop(); i++ {
				sinkInt, sinkBool = slices.BinarySearch(s, (i%n)*2)
			}
		})
	}
}

// Against the standard library at a real size. slices.BinarySearch is this package's
// LowerBound; sort.SearchInts is Partition with a closure.
func BenchmarkAgainstStdlib(b *testing.B) {
	const n = 1 << 20
	s := sortedInts(n)

	r := rand.New(rand.NewPCG(1, 2))
	targets := make([]int, 1024)
	for i := range targets {
		targets[i] = r.IntN(n) * 2
	}

	b.Run("this package", func(b *testing.B) {
		for i := 0; b.Loop(); i++ {
			sinkInt, sinkBool = BinarySearch(s, targets[i%len(targets)])
		}
	})

	b.Run("slices.BinarySearch", func(b *testing.B) {
		for i := 0; b.Loop(); i++ {
			sinkInt, sinkBool = slices.BinarySearch(s, targets[i%len(targets)])
		}
	})

	b.Run("slices.BinarySearchFunc", func(b *testing.B) {
		for i := 0; b.Loop(); i++ {
			sinkInt, sinkBool = slices.BinarySearchFunc(s, targets[i%len(targets)],
				func(a, t int) int {
					switch {
					case a < t:
						return -1
					case a > t:
						return 1
					}
					return 0
				})
		}
	})

	b.Run("sort.SearchInts", func(b *testing.B) {
		for i := 0; b.Loop(); i++ {
			sinkInt = sort.SearchInts(s, targets[i%len(targets)])
		}
	})
}

// Exponential search against plain binary search, as a function of where the target
// is. The doubling wins near the front and costs a little at the end.
func BenchmarkExponential(b *testing.B) {
	const n = 1 << 20
	s := sortedInts(n)

	for _, position := range []int{0, 10, 1000, n / 2, n - 1} {
		target := position * 2

		b.Run("at "+itoa(position)+"/exponential", func(b *testing.B) {
			for b.Loop() {
				sinkInt, sinkBool = Exponential(s, target)
			}
		})

		b.Run("at "+itoa(position)+"/binary", func(b *testing.B) {
			for b.Loop() {
				sinkInt, sinkBool = BinarySearch(s, target)
			}
		})
	}
}

// Counting occurrences two ways: two bounds, or find one match and walk outwards.
// The walk is O(k) in the number of matches, which on a heavily duplicated slice is
// the whole slice.
func BenchmarkCount(b *testing.B) {
	const n = 1 << 20

	for _, distinct := range []int{n, 1024, 1} {
		s := make([]int, n)
		for i := range s {
			s[i] = i % distinct
		}
		slices.Sort(s)

		label := "all distinct"
		switch distinct {
		case 1024:
			label = "1024 distinct"
		case 1:
			label = "all identical"
		}

		b.Run(label+"/two bounds", func(b *testing.B) {
			for b.Loop() {
				sinkInt = Count(s, 0)
			}
		})

		b.Run(label+"/walk outwards", func(b *testing.B) {
			for b.Loop() {
				sinkInt = countByWalking(s, 0)
			}
		})
	}
}

// countByWalking is the version people write first: find a match, then expand.
func countByWalking[T int](s []T, target T) int {
	i, ok := BinarySearch(s, target)
	if !ok {
		return 0
	}

	lo := i
	for lo > 0 && s[lo-1] == target {
		lo--
	}

	hi := i
	for hi < len(s)-1 && s[hi+1] == target {
		hi++
	}

	return hi - lo + 1
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
	sinkInt  int
	sinkBool bool
)
