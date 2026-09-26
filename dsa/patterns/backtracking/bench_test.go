package backtracking

import "testing"

// N-queens, where the search grows fast enough that the pruning is the only thing making it
// possible at all.
func BenchmarkNQueens(b *testing.B) {
	for _, n := range []int{6, 8, 10, 11} {
		b.Run("count/n="+itoa(n), func(b *testing.B) {
			for b.Loop() {
				sinkInt = CountNQueens(n)
			}
		})

		// Building the solutions rather than counting them, to price the allocation.
		b.Run("build/n="+itoa(n), func(b *testing.B) {
			for b.Loop() {
				sinkInt = len(NQueens(n))
			}
		})
	}
}

// The two cell-ordering strategies. Most-constrained-first is the single most valuable idea
// in constraint solving, and the hard puzzle is where that shows.
func BenchmarkSudoku(b *testing.B) {
	for name, puzzle := range map[string][9][9]int{"easy": easyPuzzle, "hard": hardPuzzle} {
		b.Run(name+"/most constrained", func(b *testing.B) {
			for b.Loop() {
				grid := puzzle
				sinkBool = SolveSudoku(&grid)
			}
		})

		b.Run(name+"/reading order", func(b *testing.B) {
			// 6.3 seconds per solve on the hard puzzle, which is the whole point and
			// also too slow to run casually.
			if name == "hard" && testing.Short() {
				b.Skip("6+ seconds per iteration")
			}

			for b.Loop() {
				grid := puzzle
				sinkBool = SolveSudokuInOrder(&grid)
			}
		})
	}
}

// Generating every answer is exponential by definition: there are that many answers.
func BenchmarkEnumeration(b *testing.B) {
	for _, n := range []int{10, 15, 20} {
		s := make([]int, n)
		for i := range s {
			s[i] = i
		}

		b.Run("subsets/n="+itoa(n), func(b *testing.B) {
			for b.Loop() {
				sinkInt = len(Subsets(s))
			}
		})
	}

	for _, n := range []int{6, 8, 9} {
		s := make([]int, n)
		for i := range s {
			s[i] = i
		}

		b.Run("permutations/n="+itoa(n), func(b *testing.B) {
			for b.Loop() {
				sinkInt = len(Permutations(s))
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
	sinkInt  int
	sinkBool bool
)
