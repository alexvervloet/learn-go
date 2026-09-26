// Package dynamicprogramming implements the dynamic-programming pattern.
//
// # The tell
//
// "The number of ways to…", "the minimum cost to reach…", "the longest common…". The
// signal underneath all of them is OVERLAPPING SUBPROBLEMS: a recursive solution that
// solves the same smaller problem many times.
//
// If the subproblems do not overlap, it is divide and conquer, not dynamic programming.
// Merge sort splits into halves that share nothing, so memoising it buys nothing.
//
// # The two questions
//
// Every problem here is answered by getting two things right, in this order:
//
//  1. What is the STATE? The smallest set of values that identifies a subproblem.
//  2. What is the RECURRENCE? How a state's answer follows from smaller states.
//
// Everything else, memoisation against tabulation and the space optimisations, is
// mechanical once those two are settled. When a DP problem feels impossible it is almost
// always question one that is wrong: the state is missing a dimension.
//
// # Top-down or bottom-up
//
//	MEMOISATION (top-down)   write the recursion, add a cache. Closest to how you
//	                         think about the problem, and it only computes the states
//	                         it actually needs.
//	TABULATION (bottom-up)   fill a table in dependency order. No recursion, no
//	                         hashing, and it is what lets the table be shrunk to one
//	                         or two rows.
//
// Both are here for Fibonacci so the difference is visible in one place, and the
// benchmarks price all three approaches against each other.
package dynamicprogramming

import (
	"slices"
	"strings"
)

// Fibonacci, four ways
// ====================
//
// The smallest problem where every technique is visible at once.

// FibNaive computes the nth Fibonacci number by plain recursion.
//
// O(phi^n), which is about O(1.618^n). Not a mistake to make, and worth running once to
// see: FibNaive(40) takes about 200 ms and FibNaive(50) takes about 25 seconds, for an
// answer the next function produces in nanoseconds.
//
// The reason is the shape of the call tree. Fib(5) calls Fib(3) twice, Fib(2) three
// times, Fib(1) five times. Those repeated calls are the overlapping subproblems, and
// spotting them is the whole pattern.
func FibNaive(n int) int {
	if n < 2 {
		return n
	}
	return FibNaive(n-1) + FibNaive(n-2)
}

// FibMemo computes the nth Fibonacci number with memoisation.
//
// O(n) time and O(n) space. The same function as FibNaive with a cache in front, which is
// what makes memoisation the easy conversion: the recursion is unchanged.
func FibMemo(n int) int {
	memo := make(map[int]int, n)

	var fib func(int) int
	fib = func(n int) int {
		if n < 2 {
			return n
		}
		if cached, ok := memo[n]; ok {
			return cached
		}

		result := fib(n-1) + fib(n-2)
		memo[n] = result

		return result
	}

	return fib(n)
}

// FibTable computes the nth Fibonacci number by filling a table bottom-up.
//
// O(n) time and O(n) space, with no recursion and no hashing. The dependency order is
// obvious here (each entry needs the two before it), and finding that order is the only
// work tabulation adds over memoisation.
func FibTable(n int) int {
	if n < 2 {
		return n
	}

	table := make([]int, n+1)
	table[1] = 1

	for i := 2; i <= n; i++ {
		table[i] = table[i-1] + table[i-2]
	}

	return table[n]
}

// FibRolling computes the nth Fibonacci number in O(1) space.
//
// The optimisation that tabulation enables and memoisation does not: the recurrence only
// reaches back two entries, so only two need keeping. The same trick shrinks the edit
// distance table from m*n to 2*n and the knapsack table from n*W to W.
//
// Worth noticing what it costs: the table is gone, so the intermediate values are gone,
// and anything that needs to reconstruct a path rather than just a total cannot do this.
func FibRolling(n int) int {
	if n < 2 {
		return n
	}

	previous, current := 0, 1
	for range n - 1 {
		previous, current = current, previous+current
	}

	return current
}

// Counting problems
// =================

// ClimbStairs returns the number of distinct ways to climb n steps taking 1 or 2 at a
// time.
//
// It is Fibonacci wearing a hat, and recognising that is the point: ways(n) =
// ways(n-1) + ways(n-2), because the last move was either one step or two.
func ClimbStairs(n int) int {
	if n < 0 {
		return 0
	}
	return FibRolling(n + 1)
}

// CoinChangeWays returns the number of distinct combinations of coins summing to amount.
// Order does not matter, so {1,2} and {2,1} count once.
//
// The loop order is the entire difference between this and counting PERMUTATIONS. Coins
// outside, amounts inside, means each coin is considered once for all amounts, so a
// combination is only ever built in one order. Swapping the loops counts permutations
// instead, which is a different and commonly conflated problem.
//
// See CoinChangePermutations for the swapped version, so the two can be compared.
func CoinChangeWays(coins []int, amount int) int {
	if amount < 0 {
		return 0
	}

	ways := make([]int, amount+1)
	ways[0] = 1 // one way to make nothing: take no coins

	for _, coin := range coins {
		if coin <= 0 {
			continue
		}
		for sum := coin; sum <= amount; sum++ {
			ways[sum] += ways[sum-coin]
		}
	}

	return ways[amount]
}

// CoinChangePermutations returns the number of ORDERED sequences of coins summing to
// amount, so {1,2} and {2,1} count twice.
//
// The loops are swapped relative to CoinChangeWays, and that is the only difference.
func CoinChangePermutations(coins []int, amount int) int {
	if amount < 0 {
		return 0
	}

	ways := make([]int, amount+1)
	ways[0] = 1

	for sum := 1; sum <= amount; sum++ {
		for _, coin := range coins {
			if coin > 0 && coin <= sum {
				ways[sum] += ways[sum-coin]
			}
		}
	}

	return ways[amount]
}

// MinCoins returns the fewest coins summing to amount, and false if it cannot be done.
//
// The greedy approach, always taking the largest coin that fits, is wrong and it is worth
// knowing why: for coins {1, 3, 4} and amount 6, greedy takes 4 then 1 then 1, for three
// coins, where 3+3 is two. Greedy works for some coin systems, including every real
// currency, and not in general.
func MinCoins(coins []int, amount int) (int, bool) {
	if amount < 0 {
		return 0, false
	}

	const unreachable = -1

	best := make([]int, amount+1)
	for i := 1; i <= amount; i++ {
		best[i] = unreachable
	}

	for sum := 1; sum <= amount; sum++ {
		for _, coin := range coins {
			if coin <= 0 || coin > sum || best[sum-coin] == unreachable {
				continue
			}
			if candidate := best[sum-coin] + 1; best[sum] == unreachable || candidate < best[sum] {
				best[sum] = candidate
			}
		}
	}

	if best[amount] == unreachable {
		return 0, false
	}
	return best[amount], true
}

// Two-sequence problems
// =====================
//
// The family where the state has TWO dimensions: a position in each input. The table is
// (m+1) by (n+1) and the extra row and column are the empty-prefix base cases, which is
// the off-by-one everyone fights.

// LongestCommonSubsequence returns the length of the longest subsequence common to a and
// b, and the subsequence itself.
//
// State: (i, j), the lengths of the prefixes considered. Recurrence:
//
//	a[i-1] == b[j-1]   ->  1 + LCS(i-1, j-1)
//	otherwise          ->  max(LCS(i-1, j), LCS(i, j-1))
//
// O(m*n) time and space. The full table is kept because the subsequence has to be
// reconstructed, which is the case where the rolling-rows optimisation is unavailable.
func LongestCommonSubsequence(a, b string) (int, string) {
	ra, rb := []rune(a), []rune(b)
	m, n := len(ra), len(rb)

	// One extra row and column for the empty prefixes, so table[i][j] is about
	// ra[:i] and rb[:j] and there is no special case for i or j being zero.
	table := make([][]int, m+1)
	for i := range table {
		table[i] = make([]int, n+1)
	}

	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			if ra[i-1] == rb[j-1] {
				table[i][j] = table[i-1][j-1] + 1
				continue
			}
			table[i][j] = max(table[i-1][j], table[i][j-1])
		}
	}

	// Walk the table backwards to rebuild one longest subsequence. Where the two
	// characters matched, that character is in the answer; otherwise follow whichever
	// neighbour the maximum came from.
	var sb []rune
	for i, j := m, n; i > 0 && j > 0; {
		switch {
		case ra[i-1] == rb[j-1]:
			sb = append(sb, ra[i-1])
			i--
			j--
		case table[i-1][j] >= table[i][j-1]:
			i--
		default:
			j--
		}
	}

	slices.Reverse(sb)

	return table[m][n], string(sb)
}

// EditDistance returns the Levenshtein distance between a and b: the fewest single-rune
// insertions, deletions or substitutions that turn one into the other.
//
// O(m*n) time and O(min(m,n)) space, keeping two rows instead of the whole table. That is
// available here because only the total is wanted; reconstructing the edit script needs
// the full table, exactly as LCS does.
//
// The three candidates in the recurrence correspond to the three operations, and naming
// them is the difference between remembering the formula and deriving it:
//
//	previous[j]    + 1  delete a[i-1]
//	current[j-1]   + 1  insert b[j-1]
//	previous[j-1]  + 1  substitute, or + 0 if the runes already match
func EditDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)

	// Iterate over the longer string and keep rows of the shorter, so the memory is
	// O(min(m,n)) rather than O(n).
	if len(ra) < len(rb) {
		ra, rb = rb, ra
	}

	previous := make([]int, len(rb)+1)
	current := make([]int, len(rb)+1)

	// Turning an empty prefix into rb[:j] takes j insertions.
	for j := range previous {
		previous[j] = j
	}

	for i := 1; i <= len(ra); i++ {
		current[0] = i // turning ra[:i] into an empty string takes i deletions

		for j := 1; j <= len(rb); j++ {
			substitute := previous[j-1]
			if ra[i-1] != rb[j-1] {
				substitute++
			}

			current[j] = min(substitute, previous[j]+1, current[j-1]+1)
		}

		previous, current = current, previous
	}

	return previous[len(rb)]
}

// Knapsack
// ========

// Item is a thing with a weight and a value.
type Item struct {
	Name   string
	Weight int
	Value  int
}

// Knapsack01 returns the highest total value of a subset of items whose weights fit in
// capacity, and which items those are. Each item may be taken at most once, which is what
// the "0/1" means.
//
// O(n*capacity) time and space. Pseudo-polynomial, for exactly the reason
// dsa/pvsnp explains: the cost is the capacity's VALUE, not the bits needed to write it.
// This is the optimisation version of subset sum.
//
// The full table is kept so the chosen items can be recovered. The value alone needs only
// one row, iterated backwards, and the "backwards" there is the same subtlety as in
// SubsetSumDP: forwards lets an item be taken twice.
func Knapsack01(items []Item, capacity int) (int, []Item) {
	if capacity < 0 {
		return 0, nil
	}

	n := len(items)

	table := make([][]int, n+1)
	for i := range table {
		table[i] = make([]int, capacity+1)
	}

	for i := 1; i <= n; i++ {
		item := items[i-1]

		for w := range capacity + 1 {
			// Not taking it: whatever the previous items managed with this capacity.
			table[i][w] = table[i-1][w]

			// Taking it, if it fits.
			if item.Weight <= w && item.Weight > 0 {
				if taken := table[i-1][w-item.Weight] + item.Value; taken > table[i][w] {
					table[i][w] = taken
				}
			}
		}
	}

	// Walk back to find which items were taken: item i was taken exactly when the row
	// above disagrees with this one.
	var chosen []Item
	w := capacity
	for i := n; i > 0; i-- {
		if table[i][w] == table[i-1][w] {
			continue
		}
		chosen = append(chosen, items[i-1])
		w -= items[i-1].Weight
	}

	slices.Reverse(chosen)

	return table[n][capacity], chosen
}

// Longest increasing subsequence
// ==============================

// LISQuadratic returns the length of the longest strictly increasing subsequence, in
// O(n^2).
//
// State: best[i] is the length of the longest increasing subsequence ENDING at i. That
// choice of state is the whole problem; "the longest so far" does not work, because it
// does not say what can be appended to.
func LISQuadratic(nums []int) int {
	if len(nums) == 0 {
		return 0
	}

	best := make([]int, len(nums))
	longest := 1

	for i := range nums {
		best[i] = 1

		for j := range i {
			if nums[j] < nums[i] && best[j]+1 > best[i] {
				best[i] = best[j] + 1
			}
		}

		longest = max(longest, best[i])
	}

	return longest
}

// LIS returns the length of the longest strictly increasing subsequence, in O(n log n),
// and one such subsequence.
//
// The trick is not dynamic programming at all, which is why it is worth seeing next to the
// quadratic version. `tails[k]` holds the smallest possible tail of an increasing
// subsequence of length k+1. That slice is always sorted, so the position for each new
// element is a binary search.
//
// Keeping the SMALLEST possible tail is the insight: a smaller tail can be extended by
// strictly more future elements, and it never costs anything, because the length is
// unchanged.
//
// Reconstructing the subsequence needs a parent pointer per element, since `tails` holds
// values rather than positions and gets overwritten as it goes.
func LIS(nums []int) (int, []int) {
	if len(nums) == 0 {
		return 0, nil
	}

	tails := make([]int, 0, len(nums))     // tails[k] = smallest tail of a length-(k+1) LIS
	tailIndex := make([]int, 0, len(nums)) // where each of those tails lives in nums
	parent := make([]int, len(nums))       // the previous element of the LIS ending here

	for i, v := range nums {
		// The first tail that is not smaller than v: that subsequence can be extended
		// by v, replacing its tail with a smaller one.
		at, _ := slices.BinarySearch(tails, v)

		if at == len(tails) {
			tails = append(tails, v)
			tailIndex = append(tailIndex, i)
		} else {
			tails[at] = v
			tailIndex[at] = i
		}

		parent[i] = -1
		if at > 0 {
			parent[i] = tailIndex[at-1]
		}
	}

	// Walk the parent chain back from the last tail.
	out := make([]int, 0, len(tails))
	for at := tailIndex[len(tailIndex)-1]; at >= 0; at = parent[at] {
		out = append(out, nums[at])
	}
	slices.Reverse(out)

	return len(tails), out
}

// Grid paths
// ==========

// UniquePaths returns the number of distinct paths from the top-left to the bottom-right
// of a rows-by-cols grid, moving only right or down.
//
// One row of state is enough, because the recurrence only reaches up and left. Overwriting
// in place is what makes that work: by the time cell j is read, it still holds the value
// from the row above, and after the assignment it holds this row's.
func UniquePaths(rows, cols int) int {
	if rows <= 0 || cols <= 0 {
		return 0
	}

	row := make([]int, cols)
	for j := range row {
		row[j] = 1 // the top row has exactly one path to each cell
	}

	for range rows - 1 {
		for j := 1; j < cols; j++ {
			// row[j] is still the cell above; row[j-1] is already this row's left.
			row[j] += row[j-1]
		}
	}

	return row[cols-1]
}

// MinPathSum returns the smallest sum of any path from the top-left to the bottom-right of
// a grid, moving only right or down.
func MinPathSum(grid [][]int) int {
	if len(grid) == 0 || len(grid[0]) == 0 {
		return 0
	}

	cols := len(grid[0])
	row := make([]int, cols)

	row[0] = grid[0][0]
	for j := 1; j < cols; j++ {
		row[j] = row[j-1] + grid[0][j]
	}

	for i := 1; i < len(grid); i++ {
		row[0] += grid[i][0]

		for j := 1; j < cols; j++ {
			row[j] = min(row[j], row[j-1]) + grid[i][j]
		}
	}

	return row[cols-1]
}

// Palindromes
// ===========

// LongestPalindromicSubstring returns the longest substring of s that reads the same both
// ways.
//
// Not the DP solution, deliberately. The DP is O(n^2) time and O(n^2) space; expanding
// around every centre is O(n^2) time and O(1) space, and it is shorter. When the obvious
// DP is beaten by something simpler, the something simpler belongs here with a note saying
// so. (Manacher's algorithm does it in O(n), and is a different subject.)
//
// The 2n-1 centres are the part to get right: n single-rune centres and n-1 gaps between
// runes, because a palindrome can have even or odd length.
func LongestPalindromicSubstring(s string) string {
	runes := []rune(s)
	if len(runes) == 0 {
		return ""
	}

	bestStart, bestLen := 0, 1

	expand := func(left, right int) {
		for left >= 0 && right < len(runes) && runes[left] == runes[right] {
			left--
			right++
		}

		// The loop overshoots by one on each side.
		if length := right - left - 1; length > bestLen {
			bestStart, bestLen = left+1, length
		}
	}

	for i := range runes {
		expand(i, i)   // odd length, centred on a rune
		expand(i, i+1) // even length, centred between two runes
	}

	return string(runes[bestStart : bestStart+bestLen])
}

// WordBreak reports whether s can be segmented into a sequence of words from the
// dictionary, and returns one such segmentation.
//
// State: can[i] is whether s[:i] is segmentable. O(n^2) checks against a set, which is why
// the dictionary goes into a map first.
func WordBreak(s string, dictionary []string) (bool, []string) {
	words := make(map[string]bool, len(dictionary))
	longest := 0
	for _, w := range dictionary {
		if w == "" {
			continue
		}
		words[w] = true
		longest = max(longest, len(w))
	}

	can := make([]bool, len(s)+1)
	can[0] = true
	from := make([]int, len(s)+1) // where the word ending at i started

	for i := 1; i <= len(s); i++ {
		// Only look back as far as the longest dictionary word, which turns O(n^2)
		// into O(n * longest) for a dictionary of short words.
		start := max(0, i-longest)

		for j := start; j < i; j++ {
			if can[j] && words[s[j:i]] {
				can[i] = true
				from[i] = j
				break
			}
		}
	}

	if !can[len(s)] {
		return false, nil
	}

	var out []string
	for i := len(s); i > 0; i = from[i] {
		out = append(out, s[from[i]:i])
	}
	slices.Reverse(out)

	return true, out
}

// Describe renders a segmentation, for the examples.
func Describe(words []string) string { return strings.Join(words, " ") }
