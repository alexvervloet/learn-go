// Package backtracking implements the backtracking pattern.
//
// # The tell
//
// "All subsets", "every permutation", "all combinations", "every way to…". The output is
// a LIST OF ANSWERS rather than a single number, and the answers are built one decision
// at a time.
//
// If the question asks for a count or a best, try dynamic programming first: counting
// paths through a grid is DP, and listing them is backtracking. The difference matters
// because there can be exponentially many answers, so listing them is exponential however
// clever you are, while counting them often is not.
//
// # The shape
//
// Three steps, always in this order:
//
//	choose    make a decision and record it
//	explore   recurse
//	undo      unmake the decision
//
// The undo is what makes it backtracking rather than brute force. It is also the step
// that gets forgotten, and forgetting it does not crash: it produces answers that are
// quietly wrong, because state leaks from one branch into its siblings.
//
// # Pruning is the whole game
//
// Without pruning, backtracking is exhaustive search with extra steps. The interesting
// part of every problem below is the line that cuts a branch off early:
//
//	CombinationSum  stop when the remaining target goes negative
//	NQueens         check three attack directions before placing, not after
//	SolveSudoku     pick the cell with the FEWEST candidates next
//
// The n-queens numbers in README.md show what one of those lines is worth: pruning takes
// n=8 from 16.7 million placements to 2,057.
//
// # The Go trap
//
// Every function here appends a CLONE of the current path to the results. Appending the
// live slice compiles, runs, and produces a result where every entry is identical to the
// last one, because they all alias the same backing array. It is the single most common
// bug in this pattern, and TestResultsAreNotAliased exists for it.
package backtracking

import (
	"slices"
	"strings"
)

// Subsets and combinations
// ========================

// Subsets returns every subset of s, including the empty one.
//
// 2^n results, so this is exponential by definition rather than by inefficiency: there
// really are that many answers.
//
// The decision at each step is binary, take element i or do not, which is why the result
// count is a power of two and why the same problem can be written as a bitmask loop. The
// recursive version is here because it generalises to the problems below and the bitmask
// version does not.
func Subsets[T any](s []T) [][]T {
	var out [][]T
	var path []T

	var explore func(at int)
	explore = func(at int) {
		if at == len(s) {
			// A clone, always. Appending `path` itself would make every entry alias
			// the same array.
			out = append(out, slices.Clone(path))
			return
		}

		// Do not take s[at].
		explore(at + 1)

		// Take it: choose, explore, undo.
		path = append(path, s[at])
		explore(at + 1)
		path = path[:len(path)-1]
	}

	explore(0)

	return out
}

// SubsetsDistinct returns every distinct subset of s, treating equal elements as
// interchangeable, with each subset in sorted order.
//
// Duplicates in the input are what make this different from Subsets. For [1,2,2] there
// are 8 subsets but only 6 distinct ones.
//
// The rule: sort first, then at each level skip an element equal to the previous one
// UNLESS the previous one was taken. That is the standard formulation and the reason it
// works is worth stating: among k copies of a value, the only thing that distinguishes
// the subsets is HOW MANY were taken, so forcing them to be taken as a prefix of the run
// collapses every equivalent choice into one.
func SubsetsDistinct[T interface{ comparable }](s []T, less func(a, b T) int) [][]T {
	sorted := slices.Clone(s)
	slices.SortFunc(sorted, less)

	var out [][]T
	var path []T

	var explore func(at int)
	explore = func(at int) {
		out = append(out, slices.Clone(path))

		for i := at; i < len(sorted); i++ {
			// Skip a duplicate unless it is the first of its run at this level.
			if i > at && sorted[i] == sorted[i-1] {
				continue
			}

			path = append(path, sorted[i])
			explore(i + 1)
			path = path[:len(path)-1]
		}
	}

	explore(0)

	return out
}

// Combinations returns every way of choosing k elements from 1 to n, ascending.
//
// C(n, k) results. The pruning is in the loop bound: there is no point starting at a value
// so high that fewer than k elements remain. Without it the recursion explores branches
// that cannot possibly produce a full combination, which for large n and k is most of them.
func Combinations(n, k int) [][]int {
	if k < 0 || k > n || n < 0 {
		return nil
	}

	var out [][]int
	path := make([]int, 0, k)

	var explore func(start int)
	explore = func(start int) {
		if len(path) == k {
			out = append(out, slices.Clone(path))
			return
		}

		// The bound is the pruning: stopping at n-(k-len(path))+1 means every branch
		// entered still has enough elements left to finish.
		for v := start; v <= n-(k-len(path))+1; v++ {
			path = append(path, v)
			explore(v + 1)
			path = path[:len(path)-1]
		}
	}

	explore(1)

	return out
}

// CombinationSum returns every distinct multiset of candidates summing to target. Each
// candidate may be used any number of times.
//
// The pruning is the line that stops a branch once a candidate exceeds the remaining
// target, and sorting first is what makes it a `break` rather than a `continue`: once one
// candidate is too large, every later one is too.
//
// Duplicate candidates are removed first. Reuse is unlimited, so a second copy of a value
// adds nothing except a second path to the same multiset, and without the compaction
// [4,4,7] returns [4 4 4 7] twice. A randomised test found that; the hand-written table of
// examples used distinct candidates and did not.
func CombinationSum(candidates []int, target int) [][]int {
	sorted := slices.Clone(candidates)
	slices.Sort(sorted)
	sorted = slices.Compact(sorted)

	var out [][]int
	var path []int

	var explore func(start, remaining int)
	explore = func(start, remaining int) {
		if remaining == 0 {
			out = append(out, slices.Clone(path))
			return
		}

		for i := start; i < len(sorted); i++ {
			if sorted[i] <= 0 {
				continue // a zero or negative candidate would never terminate
			}
			if sorted[i] > remaining {
				break // sorted, so every later candidate is too large as well
			}

			path = append(path, sorted[i])
			explore(i, remaining-sorted[i]) // i, not i+1: reuse is allowed
			path = path[:len(path)-1]
		}
	}

	explore(0, target)

	return out
}

// Permutations
// ============

// Permutations returns every ordering of s.
//
// n! results. The swap-and-recurse version, which needs no visited set and no extra
// allocation: swapping element k with each candidate and recursing is exactly "choose what
// goes in position k".
//
// It does not handle duplicates: [1,1] produces two identical orderings. See
// PermutationsDistinct.
func Permutations[T any](s []T) [][]T {
	work := slices.Clone(s) // do not scramble the caller's slice
	var out [][]T

	var explore func(at int)
	explore = func(at int) {
		if at == len(work) {
			out = append(out, slices.Clone(work))
			return
		}

		for i := at; i < len(work); i++ {
			work[at], work[i] = work[i], work[at]
			explore(at + 1)
			work[at], work[i] = work[i], work[at] // undo
		}
	}

	explore(0)

	return out
}

// PermutationsDistinct returns every distinct ordering of s.
//
// The swap version cannot easily be de-duplicated, because swapping destroys the sorted
// order that the "skip a duplicate" rule depends on. So this uses the other formulation:
// sort, then pick from the remaining elements with a used[] flag, skipping a duplicate
// whose identical predecessor has not been used at this level.
//
// The condition is `!used[i-1]`, and getting it backwards is a classic. `used[i-1]` would
// mean "skip unless the previous copy is in use", which permits the copies to be chosen in
// any order and reproduces the duplicates.
func PermutationsDistinct[T comparable](s []T, less func(a, b T) int) [][]T {
	sorted := slices.Clone(s)
	slices.SortFunc(sorted, less)

	used := make([]bool, len(sorted))
	var out [][]T
	path := make([]T, 0, len(sorted))

	var explore func()
	explore = func() {
		if len(path) == len(sorted) {
			out = append(out, slices.Clone(path))
			return
		}

		for i := range sorted {
			if used[i] {
				continue
			}
			// Force the copies of a value to be used left to right, so each distinct
			// arrangement is generated once.
			if i > 0 && sorted[i] == sorted[i-1] && !used[i-1] {
				continue
			}

			used[i] = true
			path = append(path, sorted[i])

			explore()

			path = path[:len(path)-1]
			used[i] = false
		}
	}

	explore()

	return out
}

// N-queens
// ========

// NQueens returns every way to place n non-attacking queens on an n-by-n board. Each
// solution is a slice where index i holds the column of the queen in row i.
//
// The representation is the first optimisation: one queen per row is built into the shape
// of the answer, so row conflicts are impossible by construction and never need checking.
//
// The three sets are the second. A queen at (row, col) attacks:
//
//	its column          col
//	its "\" diagonal    row - col, constant along it
//	its "/" diagonal    row + col, constant along it
//
// so each check is O(1) instead of scanning the board. Checking BEFORE placing rather than
// validating after is what turns exhaustive search into something that finishes: see the
// README for the 8,000x.
func NQueens(n int) [][]int {
	if n <= 0 {
		return nil
	}

	var out [][]int
	position := make([]int, n)

	columns := make([]bool, n)
	// row-col ranges over -(n-1) to n-1, so it is offset by n-1 to index a slice.
	backslash := make([]bool, 2*n-1)
	forwardslash := make([]bool, 2*n-1)

	var explore func(row int)
	explore = func(row int) {
		if row == n {
			out = append(out, slices.Clone(position))
			return
		}

		for col := range n {
			back, forward := row-col+n-1, row+col

			if columns[col] || backslash[back] || forwardslash[forward] {
				continue // pruned before placing, not validated after
			}

			position[row] = col
			columns[col], backslash[back], forwardslash[forward] = true, true, true

			explore(row + 1)

			columns[col], backslash[back], forwardslash[forward] = false, false, false
		}
	}

	explore(0)

	return out
}

// CountNQueens returns how many solutions n-queens has, without building them.
//
// Same search, no allocation for the results. Worth having separately because the count is
// often all that is wanted and the solutions are what make the memory grow: n=12 has
// 14,200 solutions.
func CountNQueens(n int) int {
	if n <= 0 {
		return 0
	}

	columns := make([]bool, n)
	backslash := make([]bool, 2*n-1)
	forwardslash := make([]bool, 2*n-1)

	count := 0

	var explore func(row int)
	explore = func(row int) {
		if row == n {
			count++
			return
		}

		for col := range n {
			back, forward := row-col+n-1, row+col

			if columns[col] || backslash[back] || forwardslash[forward] {
				continue
			}

			columns[col], backslash[back], forwardslash[forward] = true, true, true
			explore(row + 1)
			columns[col], backslash[back], forwardslash[forward] = false, false, false
		}
	}

	explore(0)

	return count
}

// RenderBoard draws a single n-queens solution.
func RenderBoard(position []int) string {
	var sb strings.Builder

	for _, col := range position {
		for c := range len(position) {
			if c == col {
				sb.WriteByte('Q')
				continue
			}
			sb.WriteByte('.')
		}
		sb.WriteByte('\n')
	}

	return sb.String()
}

// Word search
// ===========

// WordSearch reports whether word can be spelled by walking the grid one cell at a time,
// horizontally or vertically, without reusing a cell.
//
// The "without reusing" is what makes it backtracking rather than a plain grid traversal:
// a cell used on one path has to become available again when that path is abandoned. The
// undo here is marking the cell back.
//
// The grid itself is used as the visited set, with a sentinel rune, so no second data
// structure is needed and the undo is one assignment. That mutates the caller's grid
// during the search and restores it before returning, which the test checks.
func WordSearch(grid [][]rune, word string) bool {
	target := []rune(word)
	if len(target) == 0 {
		return true
	}
	if len(grid) == 0 || len(grid[0]) == 0 {
		return false
	}

	const visited = rune(0)

	var explore func(row, col, at int) bool
	explore = func(row, col, at int) bool {
		if row < 0 || row >= len(grid) || col < 0 || col >= len(grid[row]) {
			return false
		}
		if grid[row][col] != target[at] {
			return false
		}
		if at == len(target)-1 {
			return true
		}

		// Choose: mark the cell used.
		was := grid[row][col]
		grid[row][col] = visited

		found := explore(row-1, col, at+1) ||
			explore(row+1, col, at+1) ||
			explore(row, col-1, at+1) ||
			explore(row, col+1, at+1)

		// Undo: this cell is available to other paths again.
		grid[row][col] = was

		return found
	}

	for row := range grid {
		for col := range grid[row] {
			if explore(row, col, 0) {
				return true
			}
		}
	}

	return false
}

// Sudoku
// ======

// SolveSudoku fills a 9x9 grid in place, using 0 for an empty cell, and reports whether it
// could.
//
// The pruning that matters is the cell ORDER: always fill the empty cell with the fewest
// candidates. A cell with one possible digit is forced, so filling it first costs nothing
// and removes a whole level of branching. Scanning in reading order instead is the
// difference between milliseconds and minutes on a hard puzzle, and the benchmark measures
// it.
//
// This is the "most constrained variable" heuristic, and it is the single most valuable
// idea in constraint solving.
//
// The GIVENS are checked first, and that check is not optional. A grid handed in with two
// 5s already in the same row has no solution, and without the check the solver cannot tell:
// it only ever validates digits it places itself, so it explores the entire remaining space
// before failing. A grid with two givens and 79 empty cells takes effectively forever.
// TestSolveSudokuRejectsImpossible is that case, and it hung until this was added.
func SolveSudoku(grid *[9][9]int) bool {
	if !GivensAreConsistent(grid) {
		return false
	}
	return solveMostConstrained(grid)
}

func solveMostConstrained(grid *[9][9]int) bool {
	row, col, candidates, ok := mostConstrained(grid)
	if !ok {
		return true // nothing empty, so it is solved
	}
	if candidates == 0 {
		return false // an empty cell with no legal digit: this branch is dead
	}

	for digit := 1; digit <= 9; digit++ {
		if !sudokuAllows(grid, row, col, digit) {
			continue
		}

		grid[row][col] = digit
		if solveMostConstrained(grid) {
			return true
		}
		grid[row][col] = 0 // undo
	}

	return false
}

// GivensAreConsistent reports whether the non-empty cells of a grid conflict with each
// other. It says nothing about whether a solution exists, only that none is ruled out by
// the givens alone.
func GivensAreConsistent(grid *[9][9]int) bool {
	for r := range 9 {
		for c := range 9 {
			digit := grid[r][c]
			if digit == 0 {
				continue
			}
			if digit < 1 || digit > 9 {
				return false
			}

			// Blank the cell so it does not conflict with itself.
			grid[r][c] = 0
			ok := sudokuAllows(grid, r, c, digit)
			grid[r][c] = digit

			if !ok {
				return false
			}
		}
	}

	return true
}

// mostConstrained returns the empty cell with the fewest legal digits, and how many it has.
// ok is false when there are no empty cells.
func mostConstrained(grid *[9][9]int) (row, col, candidates int, ok bool) {
	best := 10

	for r := range 9 {
		for c := range 9 {
			if grid[r][c] != 0 {
				continue
			}

			n := 0
			for digit := 1; digit <= 9; digit++ {
				if sudokuAllows(grid, r, c, digit) {
					n++
				}
			}

			if n < best {
				best, row, col, ok = n, r, c, true

				if n == 0 {
					return row, col, 0, true // dead already, stop looking
				}
			}
		}
	}

	return row, col, best, ok
}

// sudokuAllows reports whether digit may go in (row, col) under the three constraints.
func sudokuAllows(grid *[9][9]int, row, col, digit int) bool {
	for i := range 9 {
		if grid[row][i] == digit || grid[i][col] == digit {
			return false
		}
	}

	boxRow, boxCol := row/3*3, col/3*3
	for r := boxRow; r < boxRow+3; r++ {
		for c := boxCol; c < boxCol+3; c++ {
			if grid[r][c] == digit {
				return false
			}
		}
	}

	return true
}

// SolveSudokuInOrder is the same solver filling cells in reading order, for the comparison.
func SolveSudokuInOrder(grid *[9][9]int) bool {
	if !GivensAreConsistent(grid) {
		return false
	}
	return solveInOrder(grid)
}

func solveInOrder(grid *[9][9]int) bool {
	for r := range 9 {
		for c := range 9 {
			if grid[r][c] != 0 {
				continue
			}

			for digit := 1; digit <= 9; digit++ {
				if !sudokuAllows(grid, r, c, digit) {
					continue
				}

				grid[r][c] = digit
				if solveInOrder(grid) {
					return true
				}
				grid[r][c] = 0
			}

			return false // no digit fits here, so this branch is dead
		}
	}

	return true
}

// IsValidSudoku reports whether a COMPLETELY FILLED grid satisfies all three constraints.
// A grid with any empty cell is not valid.
func IsValidSudoku(grid *[9][9]int) bool {
	for r := range 9 {
		for c := range 9 {
			if grid[r][c] == 0 {
				return false
			}
		}
	}

	return GivensAreConsistent(grid)
}

// RenderSudoku draws a grid, using "." for empty cells.
func RenderSudoku(grid *[9][9]int) string {
	var sb strings.Builder

	for r := range 9 {
		for c := range 9 {
			if grid[r][c] == 0 {
				sb.WriteByte('.')
				continue
			}
			sb.WriteByte(byte('0' + grid[r][c]))
		}
		sb.WriteByte('\n')
	}

	return sb.String()
}
