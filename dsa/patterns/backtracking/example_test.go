package backtracking_test

import (
	"cmp"
	"fmt"

	"github.com/alexvervloet/learn-go/dsa/patterns/backtracking"
)

// 2^n results, because the decision at each element is binary.
func ExampleSubsets() {
	for _, sub := range backtracking.Subsets([]int{1, 2, 3}) {
		fmt.Println(sub)
	}

	// Output:
	// []
	// [3]
	// [2]
	// [2 3]
	// [1]
	// [1 3]
	// [1 2]
	// [1 2 3]
}

// With duplicates in the input, [1,2,2] has 8 subsets but only 6 distinct ones.
func ExampleSubsetsDistinct() {
	for _, sub := range backtracking.SubsetsDistinct([]int{1, 2, 2}, cmp.Compare[int]) {
		fmt.Println(sub)
	}

	// Output:
	// []
	// [1]
	// [1 2]
	// [1 2 2]
	// [2]
	// [2 2]
}

// The pruning is in the loop bound: never start at a value so high that fewer than k
// elements remain.
func ExampleCombinations() {
	for _, c := range backtracking.Combinations(4, 2) {
		fmt.Println(c)
	}

	// Output:
	// [1 2]
	// [1 3]
	// [1 4]
	// [2 3]
	// [2 4]
	// [3 4]
}

// Candidates may be reused, so the recursion passes i rather than i+1. Sorting first is
// what makes the "too large" check a break rather than a continue.
func ExampleCombinationSum() {
	for _, c := range backtracking.CombinationSum([]int{2, 3, 6, 7}, 7) {
		fmt.Println(c)
	}

	// Output:
	// [2 2 3]
	// [7]
}

// Swap and recurse: swapping element k with each candidate is exactly "choose what goes in
// position k". It needs no visited set.
func ExamplePermutations() {
	for _, p := range backtracking.Permutations([]string{"a", "b", "c"}) {
		fmt.Println(p)
	}

	// Output:
	// [a b c]
	// [a c b]
	// [b a c]
	// [b c a]
	// [c b a]
	// [c a b]
}

// The swap version cannot be de-duplicated easily, because swapping destroys the sorted
// order the skip rule depends on. This one sorts and picks with a used[] flag instead.
func ExamplePermutationsDistinct() {
	for _, p := range backtracking.PermutationsDistinct([]int{1, 1, 2}, cmp.Compare[int]) {
		fmt.Println(p)
	}

	// Output:
	// [1 1 2]
	// [1 2 1]
	// [2 1 1]
}

// One queen per row is built into the representation, so row conflicts are impossible by
// construction and never need checking.
func ExampleNQueens() {
	solutions := backtracking.NQueens(4)

	fmt.Println(len(solutions), "solutions")
	fmt.Print(backtracking.RenderBoard(solutions[0]))

	// Output:
	// 2 solutions
	// .Q..
	// ...Q
	// Q...
	// ..Q.
}

// The standard sequence. n=2 and n=3 have none, which is the first thing to check against.
func ExampleCountNQueens() {
	for n := range 11 {
		fmt.Print(backtracking.CountNQueens(n), " ")
	}
	fmt.Println()

	// Output:
	// 0 1 0 0 2 10 4 40 92 352 724
}

// The grid doubles as the visited set, so the undo is one assignment. "Without reusing a
// cell" is what makes this backtracking rather than a plain traversal.
func ExampleWordSearch() {
	grid := [][]rune{
		[]rune("ABCE"),
		[]rune("SFCS"),
		[]rune("ADEE"),
	}

	for _, word := range []string{"ABCCED", "SEE", "ABCB"} {
		fmt.Printf("%-7s %v\n", word, backtracking.WordSearch(grid, word))
	}

	// Output:
	// ABCCED  true
	// SEE     true
	// ABCB    false
}

// Always fill the empty cell with the fewest candidates. A cell with one possible digit is
// forced, so filling it first costs nothing and removes a level of branching.
func ExampleSolveSudoku() {
	grid := [9][9]int{
		{5, 3, 0, 0, 7, 0, 0, 0, 0},
		{6, 0, 0, 1, 9, 5, 0, 0, 0},
		{0, 9, 8, 0, 0, 0, 0, 6, 0},
		{8, 0, 0, 0, 6, 0, 0, 0, 3},
		{4, 0, 0, 8, 0, 3, 0, 0, 1},
		{7, 0, 0, 0, 2, 0, 0, 0, 6},
		{0, 6, 0, 0, 0, 0, 2, 8, 0},
		{0, 0, 0, 4, 1, 9, 0, 0, 5},
		{0, 0, 0, 0, 8, 0, 0, 7, 9},
	}

	fmt.Println(backtracking.SolveSudoku(&grid))
	fmt.Print(backtracking.RenderSudoku(&grid))

	// Output:
	// true
	// 534678912
	// 672195348
	// 198342567
	// 859761423
	// 426853791
	// 713924856
	// 961537284
	// 287419635
	// 345286179
}

// A grid whose givens already conflict has no solution, and saying so takes one pass. The
// solver only validates digits it places itself, so without this check it would explore the
// whole remaining space before failing.
func ExampleGivensAreConsistent() {
	twoFives := [9][9]int{{5, 5}}

	fmt.Println(backtracking.GivensAreConsistent(&twoFives))
	fmt.Println(backtracking.SolveSudoku(&twoFives))

	// Output:
	// false
	// false
}
