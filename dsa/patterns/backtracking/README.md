# Backtracking

## The tell

"All subsets", "every permutation", "all combinations", "every way to…". The output is a
**list of answers** rather than a single number, and the answers are built one decision at a
time.

If the question asks for a count or a best, try dynamic programming first. Counting paths
through a grid is DP; **listing** them is backtracking. The difference matters because there
can be exponentially many answers, so listing them is exponential however clever you are,
while counting them often is not.

## The shape

Three steps, always in this order:

```
choose     make a decision and record it
explore    recurse
undo       unmake the decision
```

The **undo** is what makes it backtracking rather than brute force. It is also the step that
gets forgotten, and forgetting it does not crash: it produces answers that are quietly
wrong, because state leaks from one branch into its siblings.

## Pruning is the whole game

Without pruning, backtracking is exhaustive search with extra steps. The interesting line in
every problem here is the one that cuts a branch off early.

`TestNQueensPruning` counts placements with and without it:

| n | solutions | unpruned placements | pruned | fewer by |
|---|---|---|---|---|
| 4 | 2 | 340 | 16 | 21x |
| 6 | 4 | 55,986 | 152 | 368x |
| 8 | 92 | 19,173,960 | **2,056** | **9,326x** |

Same answers, same algorithm, one difference: checking the three attack directions
**before** placing a queen rather than validating the board after. That is the entire
distinction between "exhaustive search" and something that finishes.

## N-queens

Two decisions carry it.

**The representation.** A solution is a slice where index i holds the column of the queen in
row i. One queen per row is built into the shape of the answer, so row conflicts are
impossible by construction and never need checking.

**Three sets instead of a board scan.** A queen at (row, col) attacks:

| | |
|---|---|
| its column | `col` |
| its `\` diagonal | `row - col`, constant along it |
| its `/` diagonal | `row + col`, constant along it |

so each check is O(1). `row - col` ranges over −(n−1) to n−1, hence the `+n-1` offset to
index a slice.

| n | count | build |
|---|---|---|
| 6 | 2.60 µs | 2.36 µs |
| 8 | 52.0 µs | 48.2 µs |
| 10 | 1.21 ms | 1.25 ms |
| 11 | 6.19 ms | 6.46 ms |

Counting and building cost almost the same, because the search dominates and the solutions
are small. `CountNQueens` exists anyway, because at n=12 there are 14,200 of them and the
count is usually all anyone wants.

## Sudoku: most-constrained-first

The pruning that matters is not the constraint check, it is the **cell order**. Always fill
the empty cell with the fewest candidates: a cell with one possible digit is forced, so
filling it first costs nothing and removes a whole level of branching.

| puzzle | most constrained | reading order | ratio |
|---|---|---|---|
| a newspaper puzzle | 123 µs | 369 µs | 3x |
| Dobrichev's anti-brute-force | **139 ms** | **6.34 s** | **46x** |

The hard puzzle is built specifically to punish a reading-order solver. Same code, same
constraint check, one heuristic. This is the "most constrained variable" rule, and it is the
single most valuable idea in constraint solving.

The reading-order benchmark is skipped under `-short`, because 6.3 seconds per iteration is
the point and also too slow to run casually.

## Validating the givens is not optional

`SolveSudoku` checks `GivensAreConsistent` before it starts, and that check was added after
a test hung.

The solver only ever validates digits **it places itself**. Hand it a grid with two 5s
already in the same row and it cannot tell: it explores the entire remaining space before
failing. A grid with two givens and 79 empty cells took longer than a 110-second timeout.

`TestSolveSudokuRejectsImpossible` covers the four ways givens can conflict, and checks that
a rejected grid comes back untouched.

## The Go trap

Every function here appends a **clone** of the current path to the results:

```go
out = append(out, slices.Clone(path))
```

Appending `path` itself compiles, runs, and produces a result where every entry is identical
to the last one, because they all alias the same backing array. It is the single most common
bug in this pattern.

`TestResultsAreNotAliased` runs every function, mutates one result, and checks no other
result changed. The same trap appears in `pvsnp`'s `permute`, where the callback receives the
live slice on purpose and the caller has to clone.

## Duplicates

Three functions handle them, three different ways, and the rules are easy to get backwards.

**`SubsetsDistinct`**: sort, then skip an element equal to the previous one unless the
previous one was taken at this level. Among k copies of a value, the only thing that
distinguishes the subsets is *how many* were taken, so forcing them to be taken as a prefix
of the run collapses every equivalent choice into one.

**`PermutationsDistinct`**: the condition is `!used[i-1]`, and getting it backwards is a
classic. `used[i-1]` would mean "skip unless the previous copy is in use", which permits the
copies to be chosen in any order and reproduces the duplicates.

The swap-and-recurse formulation used by `Permutations` cannot be de-duplicated easily,
because swapping destroys the sorted order the skip rule depends on. So the distinct version
uses a `used[]` flag instead, and the two formulations sit next to each other.

**`CombinationSum`** compacts its candidates. Reuse is unlimited, so a second copy of a value
adds nothing but a second path to the same multiset: without the compaction, `[4,4,7]` with
target 19 returns `[4 4 4 7]` twice. A randomised test found that. The hand-written table of
examples used distinct candidates, as the textbook statement of the problem does, and did
not.

## Testing

Where the answer is a set of answers, the tests check three things rather than one fixed
list: the **count** (2ⁿ, n!, C(n,k)), that every result is **distinct**, and that every
result is **valid**.

For the de-duplicating variants the oracle is the non-de-duplicating one with the duplicates
removed afterwards, which is slow and obviously correct.

`TestNQueensSolutionsAreValid` checks the solutions rather than the count, because a solver
with a broken diagonal check can still produce the right *number* of wrong answers.

`TestWordSearchRestoresTheGrid` checks that the grid comes back unchanged after failed
searches too, since the grid doubles as the visited set.

Two of the fixed expectations in that file were wrong when written, both hand-traced paths
through a 3×4 grid, and the corrected traces are in the test file as comments.

## How to run

```bash
go test ./patterns/backtracking
go test -v -run NQueensPruning ./patterns/backtracking
go test -run XXX -bench BenchmarkNQueens -benchtime 10x ./patterns/backtracking
go test -run XXX -bench BenchmarkSudoku -benchtime 10x ./patterns/backtracking   # 65s
go test -short -run XXX -bench BenchmarkSudoku ./patterns/backtracking           # skips the 6s one
```
