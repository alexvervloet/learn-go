package dynamicprogramming_test

import (
	"fmt"

	dp "github.com/alexvervloet/learn-go/dsa/patterns/dynamicprogramming"
)

// Four implementations of one function, so the techniques sit side by side.
func ExampleFibRolling() {
	for _, n := range []int{10, 20, 30} {
		fmt.Println(n, dp.FibNaive(n), dp.FibMemo(n), dp.FibTable(n), dp.FibRolling(n))
	}

	// Output:
	// 10 55 55 55 55
	// 20 6765 6765 6765 6765
	// 30 832040 832040 832040 832040
}

// The loop order is the whole difference between combinations and permutations, and it is
// the most commonly conflated pair in the pattern.
func ExampleCoinChangeWays() {
	coins := []int{1, 2}

	fmt.Println("combinations of 3:", dp.CoinChangeWays(coins, 3))         // {1,1,1} {1,2}
	fmt.Println("permutations of 3:", dp.CoinChangePermutations(coins, 3)) // and {2,1}

	// Output:
	// combinations of 3: 2
	// permutations of 3: 3
}

// Greedy is wrong here, and the counterexample is small: for {1,3,4} and 6, greedy takes
// 4+1+1 where 3+3 is two coins.
func ExampleMinCoins() {
	n, ok := dp.MinCoins([]int{1, 3, 4}, 6)
	fmt.Println(n, ok, "-> 3+3, where greedy takes 4+1+1")

	_, ok = dp.MinCoins([]int{2}, 3)
	fmt.Println("odd amount from even coins:", ok)

	// Output:
	// 2 true -> 3+3, where greedy takes 4+1+1
	// odd amount from even coins: false
}

// Two-dimensional state: a position in each input. The full table is kept because the
// subsequence has to be reconstructed.
func ExampleLongestCommonSubsequence() {
	length, sub := dp.LongestCommonSubsequence("ABCBDAB", "BDCABA")
	fmt.Println(length, sub)

	length, sub = dp.LongestCommonSubsequence("AGGTAB", "GXTXAYB")
	fmt.Println(length, sub)

	// Output:
	// 4 BCBA
	// 4 GTAB
}

// The three candidates in the recurrence are the three edit operations, and naming them is
// the difference between remembering the formula and deriving it.
func ExampleEditDistance() {
	pairs := [][2]string{
		{"kitten", "sitting"},
		{"flaw", "lawn"},
		{"intention", "execution"},
		{"naïve", "naive"},
	}

	for _, p := range pairs {
		fmt.Printf("%-10s %-10s %d\n", p[0], p[1], dp.EditDistance(p[0], p[1]))
	}

	// Output:
	// kitten     sitting    3
	// flaw       lawn       2
	// intention  execution  5
	// naïve      naive      1
}

// The optimisation version of subset sum, and pseudo-polynomial for the same reason: the
// cost is the capacity's value, not the bits needed to write it.
func ExampleKnapsack01() {
	items := []dp.Item{
		{Name: "map", Weight: 9, Value: 150},
		{Name: "compass", Weight: 13, Value: 35},
		{Name: "water", Weight: 153, Value: 200},
		{Name: "sandwich", Weight: 50, Value: 160},
		{Name: "glucose", Weight: 15, Value: 60},
	}

	value, chosen := dp.Knapsack01(items, 100)

	weight := 0
	for _, it := range chosen {
		weight += it.Weight
		fmt.Printf("  %-9s %3d %3d\n", it.Name, it.Weight, it.Value)
	}
	fmt.Printf("total weight %d, value %d\n", weight, value)

	// Output:
	//   map         9 150
	//   compass    13  35
	//   sandwich   50 160
	//   glucose    15  60
	// total weight 87, value 405
}

// The O(n log n) version is not dynamic programming at all, which is why both are here.
// tails[k] holds the smallest possible tail of an increasing subsequence of length k+1.
func ExampleLIS() {
	nums := []int{10, 9, 2, 5, 3, 7, 101, 18}

	length, sub := dp.LIS(nums)
	fmt.Println(length, sub)
	fmt.Println("the quadratic DP agrees:", dp.LISQuadratic(nums) == length)

	// Output:
	// 4 [2 3 7 18]
	// the quadratic DP agrees: true
}

// One row of state is enough when the recurrence only reaches up and left. Overwriting in
// place is what makes that work.
func ExampleUniquePaths() {
	fmt.Println(dp.UniquePaths(3, 7))
	fmt.Println(dp.UniquePaths(3, 2))
	fmt.Println(dp.UniquePaths(1, 10))

	// Output:
	// 28
	// 3
	// 1
}

func ExampleMinPathSum() {
	grid := [][]int{
		{1, 3, 1},
		{1, 5, 1},
		{4, 2, 1},
	}

	fmt.Println(dp.MinPathSum(grid)) // 1 -> 3 -> 1 -> 1 -> 1

	// Output:
	// 7
}

// Here the obvious DP is beaten by something simpler: expanding around 2n-1 centres is the
// same O(n²) time in O(1) space.
func ExampleLongestPalindromicSubstring() {
	for _, s := range []string{"babad", "cbbd", "forgeeksskeegfor", "racecar"} {
		fmt.Printf("%-17s %s\n", s, dp.LongestPalindromicSubstring(s))
	}

	// Output:
	// babad             bab
	// cbbd              bb
	// forgeeksskeegfor  geeksskeeg
	// racecar           racecar
}

func ExampleWordBreak() {
	dict := []string{"apple", "pen"}

	ok, words := dp.WordBreak("applepenapple", dict)
	fmt.Println(ok, dp.Describe(words))

	ok, _ = dp.WordBreak("applepenapplf", dict)
	fmt.Println(ok)

	// Output:
	// true apple pen apple
	// false
}
