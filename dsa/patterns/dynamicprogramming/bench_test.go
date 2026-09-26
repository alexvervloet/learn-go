package dynamicprogramming

import (
	"math/rand/v2"
	"testing"
)

// The three Fibonacci implementations, and the gap is the point of the pattern.
func BenchmarkFib(b *testing.B) {
	// The naive version stops here: 35 is about 50 ms and 40 is about half a second.
	for _, n := range []int{10, 20, 30, 35} {
		b.Run("naive/n="+itoa(n), func(b *testing.B) {
			for b.Loop() {
				sinkInt = FibNaive(n)
			}
		})
	}

	// The others reach 90, which is as far as an int64 result goes.
	for _, n := range []int{10, 30, 90} {
		b.Run("memo/n="+itoa(n), func(b *testing.B) {
			for b.Loop() {
				sinkInt = FibMemo(n)
			}
		})
		b.Run("table/n="+itoa(n), func(b *testing.B) {
			for b.Loop() {
				sinkInt = FibTable(n)
			}
		})
		b.Run("rolling/n="+itoa(n), func(b *testing.B) {
			for b.Loop() {
				sinkInt = FibRolling(n)
			}
		})
	}
}

// The two longest-increasing-subsequence implementations: O(n^2) DP against O(n log n)
// patience sorting.
func BenchmarkLIS(b *testing.B) {
	r := rand.New(rand.NewPCG(1, 2))

	for _, n := range []int{100, 1_000, 10_000, 100_000} {
		nums := make([]int, n)
		for i := range nums {
			nums[i] = r.IntN(n)
		}

		b.Run("n="+itoa(n)+"/n log n", func(b *testing.B) {
			for b.Loop() {
				sinkInt, _ = LIS(nums)
			}
		})

		// The quadratic version is skipped above 10,000, where it takes seconds.
		if n <= 10_000 {
			b.Run("n="+itoa(n)+"/quadratic", func(b *testing.B) {
				for b.Loop() {
					sinkInt = LISQuadratic(nums)
				}
			})
		}
	}
}

// Edit distance with two rows against a full table, to price the space optimisation.
func BenchmarkEditDistance(b *testing.B) {
	r := rand.New(rand.NewPCG(3, 4))

	for _, n := range []int{100, 1_000, 4_000} {
		a := randomString(r, n, 4)
		c := randomString(r, n, 4)

		b.Run("n="+itoa(n)+"/two rows", func(b *testing.B) {
			for b.Loop() {
				sinkInt = EditDistance(a, c)
			}
		})

		b.Run("n="+itoa(n)+"/full table", func(b *testing.B) {
			for b.Loop() {
				sinkInt = editDistanceFullTable(a, c)
			}
		})
	}
}

// editDistanceFullTable is the textbook version, keeping the whole m-by-n table. Kept here
// so the space optimisation can be measured rather than assumed.
func editDistanceFullTable(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	m, n := len(ra), len(rb)

	table := make([][]int, m+1)
	for i := range table {
		table[i] = make([]int, n+1)
		table[i][0] = i
	}
	for j := range table[0] {
		table[0][j] = j
	}

	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			substitute := table[i-1][j-1]
			if ra[i-1] != rb[j-1] {
				substitute++
			}
			table[i][j] = min(substitute, table[i-1][j]+1, table[i][j-1]+1)
		}
	}

	return table[m][n]
}

// Knapsack, to show the pseudo-polynomial cost: the capacity's value, not its bit length.
func BenchmarkKnapsack(b *testing.B) {
	r := rand.New(rand.NewPCG(5, 7))

	items := make([]Item, 100)
	for i := range items {
		items[i] = Item{Weight: 1 + r.IntN(50), Value: r.IntN(100)}
	}

	for _, capacity := range []int{100, 1_000, 10_000, 100_000} {
		b.Run("capacity="+itoa(capacity), func(b *testing.B) {
			for b.Loop() {
				sinkInt, _ = Knapsack01(items, capacity)
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

var sinkInt int
