package backtracking

import (
	"cmp"
	"math/rand/v2"
	"slices"
	"testing"
)

// sortedCopy returns results in a canonical order so two runs can be compared.
func canonical[T cmp.Ordered](results [][]T) [][]T {
	out := make([][]T, len(results))
	for i, r := range results {
		out[i] = slices.Clone(r)
	}
	slices.SortFunc(out, func(a, b []T) int { return slices.Compare(a, b) })
	return out
}

// TestResultsAreNotAliased is the test for the single most common bug in this pattern.
// Appending the live path instead of a clone compiles, runs, and gives a result where every
// entry is the same slice.
func TestResultsAreNotAliased(t *testing.T) {
	check := func(name string, results [][]int) {
		t.Helper()

		if len(results) < 2 {
			return
		}

		// Mutating one result must not change another.
		for i := range results {
			if len(results[i]) == 0 {
				continue
			}

			before := slices.Clone(results[i])
			results[i][0] = -999

			for j := range results {
				if i == j {
					continue
				}
				if len(results[j]) > 0 && results[j][0] == -999 {
					t.Fatalf("%s: results %d and %d share a backing array", name, i, j)
				}
			}

			copy(results[i], before)
		}
	}

	check("Subsets", Subsets([]int{1, 2, 3}))
	check("SubsetsDistinct", SubsetsDistinct([]int{1, 2, 2}, cmp.Compare[int]))
	check("Combinations", Combinations(5, 3))
	check("CombinationSum", CombinationSum([]int{2, 3, 5}, 8))
	check("Permutations", Permutations([]int{1, 2, 3}))
	check("PermutationsDistinct", PermutationsDistinct([]int{1, 1, 2}, cmp.Compare[int]))
	check("NQueens", NQueens(6))
}

func TestSubsets(t *testing.T) {
	got := canonical(Subsets([]int{1, 2, 3}))

	want := [][]int{
		nil, {1}, {1, 2}, {1, 2, 3}, {1, 3}, {2}, {2, 3}, {3},
	}

	if len(got) != len(want) {
		t.Fatalf("got %d subsets, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if !slices.Equal(got[i], want[i]) {
			t.Errorf("subset %d = %v, want %v", i, got[i], want[i])
		}
	}

	// The count is 2^n, which is the defining property.
	for n := range 12 {
		s := make([]int, n)
		for i := range s {
			s[i] = i
		}
		if got := len(Subsets(s)); got != 1<<n {
			t.Errorf("Subsets of %d elements gave %d results, want %d", n, got, 1<<n)
		}
	}
}

func TestSubsetsDistinct(t *testing.T) {
	tests := []struct {
		in   []int
		want int // number of distinct subsets
	}{
		{[]int{1, 2, 2}, 6},
		{[]int{1, 2, 3}, 8},
		{[]int{2, 2, 2}, 4}, // zero, one, two or three copies
		{nil, 1},
		{[]int{1}, 2},
		{[]int{1, 1, 2, 2}, 9}, // 3 choices for the 1s times 3 for the 2s
	}

	for _, tt := range tests {
		got := SubsetsDistinct(tt.in, cmp.Compare[int])

		if len(got) != tt.want {
			t.Errorf("SubsetsDistinct(%v) gave %d results, want %d: %v", tt.in, len(got), tt.want, got)
		}

		// Every result must be distinct.
		seen := map[string]bool{}
		for _, sub := range got {
			key := keyOf(sub)
			if seen[key] {
				t.Errorf("SubsetsDistinct(%v) returned %v twice", tt.in, sub)
			}
			seen[key] = true
		}
	}
}

// TestSubsetsDistinctMatchesDeduplicatedSubsets: the skip rule is subtle, so the oracle is
// Subsets with the duplicates removed afterwards.
func TestSubsetsDistinctMatchesDeduplicatedSubsets(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))

	for range 2000 {
		n := r.IntN(9)
		s := make([]int, n)
		for i := range s {
			s[i] = r.IntN(3) // a tiny alphabet, so duplicates are everywhere
		}

		// The oracle: every subset, sorted, deduplicated.
		seen := map[string]bool{}
		for _, sub := range Subsets(s) {
			sorted := slices.Clone(sub)
			slices.Sort(sorted)
			seen[keyOf(sorted)] = true
		}

		got := SubsetsDistinct(s, cmp.Compare[int])

		if len(got) != len(seen) {
			t.Fatalf("SubsetsDistinct(%v) gave %d results, want %d", s, len(got), len(seen))
		}
		for _, sub := range got {
			if !slices.IsSorted(sub) {
				t.Fatalf("SubsetsDistinct(%v) returned %v, which is not sorted", s, sub)
			}
			if !seen[keyOf(sub)] {
				t.Fatalf("SubsetsDistinct(%v) returned %v, which is not a subset", s, sub)
			}
		}
	}
}

func keyOf[T cmp.Ordered](s []T) string {
	var sb []byte
	for _, v := range s {
		sb = append(sb, []byte(sprint(v))...)
		sb = append(sb, ',')
	}
	return string(sb)
}

func sprint[T any](v T) string {
	switch x := any(v).(type) {
	case int:
		if x == 0 {
			return "0"
		}
		neg := x < 0
		if neg {
			x = -x
		}
		var out []byte
		for x > 0 {
			out = append([]byte{byte('0' + x%10)}, out...)
			x /= 10
		}
		if neg {
			return "-" + string(out)
		}
		return string(out)
	case string:
		return x
	}
	return "?"
}

func TestCombinations(t *testing.T) {
	got := canonical(Combinations(4, 2))

	want := [][]int{{1, 2}, {1, 3}, {1, 4}, {2, 3}, {2, 4}, {3, 4}}
	if len(got) != len(want) {
		t.Fatalf("got %d combinations, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if !slices.Equal(got[i], want[i]) {
			t.Errorf("combination %d = %v, want %v", i, got[i], want[i])
		}
	}

	// The count is C(n, k).
	binomial := func(n, k int) int {
		if k < 0 || k > n {
			return 0
		}
		result := 1
		for i := range k {
			result = result * (n - i) / (i + 1)
		}
		return result
	}

	for n := range 11 {
		for k := -1; k <= n+1; k++ {
			got := len(Combinations(n, k))
			want := binomial(n, k)

			// k out of range gives no results, and C(n,0) is one empty combination.
			if k < 0 || k > n {
				want = 0
			}
			if got != want {
				t.Errorf("Combinations(%d, %d) gave %d results, want %d", n, k, got, want)
			}
		}
	}
}

func TestCombinationSum(t *testing.T) {
	tests := []struct {
		candidates []int
		target     int
		want       [][]int
	}{
		{[]int{2, 3, 6, 7}, 7, [][]int{{2, 2, 3}, {7}}},
		{[]int{2, 3, 5}, 8, [][]int{{2, 2, 2, 2}, {2, 3, 3}, {3, 5}}},
		{[]int{2}, 1, nil},
		{[]int{1}, 0, [][]int{nil}}, // one way: take nothing
		{nil, 5, nil},
		{[]int{0, 3}, 6, [][]int{{3, 3}}}, // a zero candidate must not loop forever
		{[]int{-1, 2}, 4, [][]int{{2, 2}}},
	}

	for _, tt := range tests {
		got := canonical(CombinationSum(tt.candidates, tt.target))
		want := canonical(tt.want)

		if len(got) != len(want) {
			t.Errorf("CombinationSum(%v, %d) gave %v, want %v", tt.candidates, tt.target, got, want)
			continue
		}
		for i := range want {
			if !slices.Equal(got[i], want[i]) {
				t.Errorf("CombinationSum(%v, %d)[%d] = %v, want %v",
					tt.candidates, tt.target, i, got[i], want[i])
			}
		}
	}
}

func TestCombinationSumResultsAreValid(t *testing.T) {
	r := rand.New(rand.NewPCG(3, 4))

	for range 2000 {
		n := 1 + r.IntN(4)
		candidates := make([]int, n)
		for i := range candidates {
			candidates[i] = 1 + r.IntN(8)
		}
		target := r.IntN(20)

		got := CombinationSum(candidates, target)

		seen := map[string]bool{}
		for _, combo := range got {
			sum := 0
			for _, v := range combo {
				sum += v
				if !slices.Contains(candidates, v) {
					t.Fatalf("combination %v uses %d, which is not a candidate", combo, v)
				}
			}
			if sum != target {
				t.Fatalf("combination %v sums to %d, want %d", combo, sum, target)
			}
			if !slices.IsSorted(combo) {
				t.Fatalf("combination %v is not sorted", combo)
			}
			if key := keyOf(combo); seen[key] {
				t.Fatalf("combination %v returned twice", combo)
			} else {
				seen[key] = true
			}
		}
	}
}

func TestPermutations(t *testing.T) {
	got := canonical(Permutations([]int{1, 2, 3}))

	want := [][]int{
		{1, 2, 3}, {1, 3, 2}, {2, 1, 3}, {2, 3, 1}, {3, 1, 2}, {3, 2, 1},
	}
	for i := range want {
		if !slices.Equal(got[i], want[i]) {
			t.Errorf("permutation %d = %v, want %v", i, got[i], want[i])
		}
	}

	// n! results, all distinct.
	factorial := 1
	for n := range 8 {
		if n > 0 {
			factorial *= n
		}

		s := make([]int, n)
		for i := range s {
			s[i] = i
		}

		results := Permutations(s)
		if len(results) != factorial {
			t.Errorf("Permutations of %d elements gave %d results, want %d", n, len(results), factorial)
		}

		seen := map[string]bool{}
		for _, p := range results {
			if key := keyOf(p); seen[key] {
				t.Errorf("n=%d: permutation %v returned twice", n, p)
			} else {
				seen[key] = true
			}
		}
	}
}

func TestPermutationsDoesNotMutateItsInput(t *testing.T) {
	s := []int{3, 1, 2}
	before := slices.Clone(s)

	Permutations(s)

	if !slices.Equal(s, before) {
		t.Errorf("Permutations mutated its input: %v then %v", before, s)
	}
}

func TestPermutationsDistinct(t *testing.T) {
	tests := []struct {
		in   []int
		want int
	}{
		{[]int{1, 1, 2}, 3},
		{[]int{1, 2, 3}, 6},
		{[]int{1, 1, 1}, 1},
		{[]int{1, 1, 2, 2}, 6},
		{nil, 1},
		{[]int{5}, 1},
	}

	for _, tt := range tests {
		got := PermutationsDistinct(tt.in, cmp.Compare[int])

		if len(got) != tt.want {
			t.Errorf("PermutationsDistinct(%v) gave %d results, want %d: %v",
				tt.in, len(got), tt.want, got)
		}

		seen := map[string]bool{}
		for _, p := range got {
			if key := keyOf(p); seen[key] {
				t.Errorf("PermutationsDistinct(%v) returned %v twice", tt.in, p)
			} else {
				seen[key] = true
			}
		}
	}
}

// TestPermutationsDistinctMatchesDeduplication: the `!used[i-1]` condition is the classic
// place to get this backwards, so the oracle is Permutations with duplicates removed.
func TestPermutationsDistinctMatchesDeduplication(t *testing.T) {
	r := rand.New(rand.NewPCG(5, 7))

	for range 1000 {
		n := r.IntN(7)
		s := make([]int, n)
		for i := range s {
			s[i] = r.IntN(3)
		}

		seen := map[string]bool{}
		for _, p := range Permutations(s) {
			seen[keyOf(p)] = true
		}

		got := PermutationsDistinct(s, cmp.Compare[int])

		if len(got) != len(seen) {
			t.Fatalf("PermutationsDistinct(%v) gave %d results, want %d", s, len(got), len(seen))
		}
		for _, p := range got {
			if !seen[keyOf(p)] {
				t.Fatalf("PermutationsDistinct(%v) returned %v, which is not a permutation", s, p)
			}
		}
	}
}

// TestNQueensCounts is the standard sequence, and getting all of it right is strong
// evidence the three attack directions are all handled.
func TestNQueensCounts(t *testing.T) {
	want := []int{0, 1, 0, 0, 2, 10, 4, 40, 92, 352, 724}

	for n, expected := range want {
		if got := CountNQueens(n); got != expected {
			t.Errorf("CountNQueens(%d) = %d, want %d", n, got, expected)
		}
		if got := len(NQueens(n)); got != expected {
			t.Errorf("len(NQueens(%d)) = %d, want %d", n, got, expected)
		}
	}
}

// TestNQueensSolutionsAreValid checks the solutions rather than the count, because a solver
// with a broken diagonal check can still produce the right NUMBER of wrong answers.
func TestNQueensSolutionsAreValid(t *testing.T) {
	for n := 1; n <= 9; n++ {
		for _, position := range NQueens(n) {
			if len(position) != n {
				t.Fatalf("n=%d: solution %v has the wrong length", n, position)
			}

			for row := range n {
				if position[row] < 0 || position[row] >= n {
					t.Fatalf("n=%d: solution %v has a column out of range", n, position)
				}

				for other := row + 1; other < n; other++ {
					if position[row] == position[other] {
						t.Fatalf("n=%d: %v has two queens in column %d", n, position, position[row])
					}
					if abs(position[row]-position[other]) == other-row {
						t.Fatalf("n=%d: %v has two queens on a diagonal", n, position)
					}
				}
			}
		}
	}
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// TestNQueensPruning measures what checking before placing is worth, by counting placements
// with and without it.
func TestNQueensPruning(t *testing.T) {
	// Without pruning: place a queen in every column of every row, and validate at the
	// end. That is n^n placements.
	countUnpruned := func(n int) (solutions, placements int) {
		position := make([]int, n)

		var explore func(row int)
		explore = func(row int) {
			if row == n {
				// Validate only now.
				for i := range n {
					for j := i + 1; j < n; j++ {
						if position[i] == position[j] || abs(position[i]-position[j]) == j-i {
							return
						}
					}
				}
				solutions++
				return
			}

			for col := range n {
				position[row] = col
				placements++
				explore(row + 1)
			}
		}

		explore(0)
		return solutions, placements
	}

	countPruned := func(n int) (solutions, placements int) {
		columns := make([]bool, n)
		backslash := make([]bool, 2*n-1)
		forwardslash := make([]bool, 2*n-1)

		var explore func(row int)
		explore = func(row int) {
			if row == n {
				solutions++
				return
			}

			for col := range n {
				back, forward := row-col+n-1, row+col
				if columns[col] || backslash[back] || forwardslash[forward] {
					continue
				}

				placements++
				columns[col], backslash[back], forwardslash[forward] = true, true, true
				explore(row + 1)
				columns[col], backslash[back], forwardslash[forward] = false, false, false
			}
		}

		explore(0)
		return solutions, placements
	}

	for _, n := range []int{4, 6, 8} {
		unprunedSolutions, unprunedPlacements := countUnpruned(n)
		prunedSolutions, prunedPlacements := countPruned(n)

		if unprunedSolutions != prunedSolutions {
			t.Fatalf("n=%d: %d solutions pruned, %d unpruned", n, prunedSolutions, unprunedSolutions)
		}
		if prunedSolutions != CountNQueens(n) {
			t.Fatalf("n=%d: the instrumented copy disagrees with CountNQueens", n)
		}

		t.Logf("n=%d: %d solutions. placements: %d unpruned, %d pruned (%.0fx fewer)",
			n, prunedSolutions, unprunedPlacements, prunedPlacements,
			float64(unprunedPlacements)/float64(prunedPlacements))

		if prunedPlacements >= unprunedPlacements {
			t.Errorf("n=%d: pruning did not reduce the search", n)
		}
	}
}

func TestWordSearch(t *testing.T) {
	grid := func() [][]rune {
		return [][]rune{
			[]rune("ABCE"),
			[]rune("SFCS"),
			[]rune("ADEE"),
		}
	}

	tests := []struct {
		word string
		want bool
	}{
		{"ABCCED", true},
		{"SEE", true},
		{"ABCB", false}, // would need to reuse the B
		{"", true},
		{"A", true},
		{"Z", false},
		// Traced by hand and both wrong first time; the grid is small enough to check.
		// A(0,0) S(1,0) A(2,0) D(2,1) E(2,2) E(2,3) S(1,3) C(1,2) F(1,1) B(0,1).
		{"ASADEESCFB", true},
		// ...ABCE then S(1,3) E(2,3) E(2,2), and (2,2) has no unused E beside it.
		{"ABCESEEEFS", false},
	}

	for _, tt := range tests {
		g := grid()
		if got := WordSearch(g, tt.word); got != tt.want {
			t.Errorf("WordSearch(%q) = %v, want %v", tt.word, got, tt.want)
		}
	}

	// Empty grids.
	if WordSearch(nil, "A") {
		t.Error("WordSearch on a nil grid found something")
	}
	if WordSearch([][]rune{{}}, "A") {
		t.Error("WordSearch on an empty row found something")
	}
}

// TestWordSearchRestoresTheGrid: the grid doubles as the visited set, so the undo has to
// put every cell back, including on the paths that failed.
func TestWordSearchRestoresTheGrid(t *testing.T) {
	original := [][]rune{
		[]rune("ABCE"),
		[]rune("SFCS"),
		[]rune("ADEE"),
	}

	grid := make([][]rune, len(original))
	for i := range original {
		grid[i] = slices.Clone(original[i])
	}

	for _, word := range []string{"ABCCED", "SEE", "ABCB", "ZZZZ", "ASADEESCFB"} {
		WordSearch(grid, word)

		for r := range grid {
			if !slices.Equal(grid[r], original[r]) {
				t.Fatalf("after searching for %q, row %d is %q, want %q",
					word, r, string(grid[r]), string(original[r]))
			}
		}
	}
}

// A puzzle with a unique solution, and a famously hard one for naive solvers.
var (
	easyPuzzle = [9][9]int{
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

	// "Anti-brute-force" by Dobrichev: designed so a reading-order solver takes a very
	// long time, and a most-constrained one does not.
	hardPuzzle = [9][9]int{
		{0, 0, 0, 0, 0, 0, 0, 0, 0},
		{0, 0, 0, 0, 0, 3, 0, 8, 5},
		{0, 0, 1, 0, 2, 0, 0, 0, 0},
		{0, 0, 0, 5, 0, 7, 0, 0, 0},
		{0, 0, 4, 0, 0, 0, 1, 0, 0},
		{0, 9, 0, 0, 0, 0, 0, 0, 0},
		{5, 0, 0, 0, 0, 0, 0, 7, 3},
		{0, 0, 2, 0, 1, 0, 0, 0, 0},
		{0, 0, 0, 0, 4, 0, 0, 0, 9},
	}
)

func TestSolveSudoku(t *testing.T) {
	for name, puzzle := range map[string][9][9]int{"easy": easyPuzzle, "hard": hardPuzzle} {
		t.Run(name, func(t *testing.T) {
			grid := puzzle

			if !SolveSudoku(&grid) {
				t.Fatal("could not solve it")
			}
			if !IsValidSudoku(&grid) {
				t.Errorf("the solution is not valid:\n%s", RenderSudoku(&grid))
			}

			// Every given must be unchanged.
			for r := range 9 {
				for c := range 9 {
					if puzzle[r][c] != 0 && grid[r][c] != puzzle[r][c] {
						t.Errorf("the given at (%d,%d) changed from %d to %d",
							r, c, puzzle[r][c], grid[r][c])
					}
				}
			}
		})
	}
}

// TestSolveSudokuRejectsImpossible is why SolveSudoku validates the givens first.
//
// Without that check the solver only ever validates digits it places ITSELF, so a grid that
// is already contradictory looks fine to it and it explores the entire remaining space
// before failing. Two givens and 79 empty cells took longer than a 110-second timeout.
func TestSolveSudokuRejectsImpossible(t *testing.T) {
	cases := map[string][9][9]int{
		"two 5s in a row":      {{5, 5}},
		"two 5s in a column":   {{5}, {5}},
		"two 5s in a box":      {{5, 0, 0}, {0, 5}},
		"a digit out of range": {{99}},
	}

	for name, puzzle := range cases {
		t.Run(name, func(t *testing.T) {
			grid := puzzle

			if GivensAreConsistent(&grid) {
				t.Fatal("the givens were reported consistent")
			}
			if SolveSudoku(&grid) {
				t.Error("solved an impossible puzzle")
			}
			if SolveSudokuInOrder(&grid) {
				t.Error("the reading-order solver solved an impossible puzzle")
			}

			// And the grid comes back untouched.
			if grid != puzzle {
				t.Error("the solver modified a grid it rejected")
			}
		})
	}

	// The positive case: a real puzzle's givens are consistent, and a solved grid's are
	// too. Without this the check could be `return false` and every test above would
	// still pass.
	easy := easyPuzzle
	if !GivensAreConsistent(&easy) {
		t.Error("a real puzzle's givens were reported inconsistent")
	}

	solved := easyPuzzle
	if !SolveSudoku(&solved) {
		t.Fatal("could not solve the puzzle")
	}
	if !GivensAreConsistent(&solved) {
		t.Error("a solved grid was reported inconsistent")
	}
}

// TestSudokuSolversAgree: the two cell-ordering strategies must reach the same solution on
// a puzzle with a unique one.
func TestSudokuSolversAgree(t *testing.T) {
	a, b := easyPuzzle, easyPuzzle

	if !SolveSudoku(&a) {
		t.Fatal("the most-constrained solver failed")
	}
	if !SolveSudokuInOrder(&b) {
		t.Fatal("the reading-order solver failed")
	}

	if a != b {
		t.Errorf("the two solvers disagree:\n%s\n%s", RenderSudoku(&a), RenderSudoku(&b))
	}
}

func TestIsValidSudoku(t *testing.T) {
	solved := easyPuzzle
	if !SolveSudoku(&solved) {
		t.Fatal("could not solve the puzzle")
	}
	if !IsValidSudoku(&solved) {
		t.Error("a solved grid was rejected")
	}

	// An unfilled grid is not valid.
	if IsValidSudoku(&easyPuzzle) {
		t.Error("a grid with empty cells was accepted")
	}

	// A duplicate in a row.
	broken := solved
	broken[0][0], broken[0][1] = 1, 1
	if IsValidSudoku(&broken) {
		t.Error("a grid with a repeated digit in a row was accepted")
	}
}
