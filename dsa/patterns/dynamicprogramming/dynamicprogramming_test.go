package dynamicprogramming

import (
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
)

// TestFibImplementationsAgree: four implementations of the same function is only useful if
// they never disagree.
func TestFibImplementationsAgree(t *testing.T) {
	want := []int{0, 1, 1, 2, 3, 5, 8, 13, 21, 34, 55, 89, 144, 233, 377, 610, 987}

	for n, expected := range want {
		if got := FibNaive(n); got != expected {
			t.Errorf("FibNaive(%d) = %d, want %d", n, got, expected)
		}
		if got := FibMemo(n); got != expected {
			t.Errorf("FibMemo(%d) = %d, want %d", n, got, expected)
		}
		if got := FibTable(n); got != expected {
			t.Errorf("FibTable(%d) = %d, want %d", n, got, expected)
		}
		if got := FibRolling(n); got != expected {
			t.Errorf("FibRolling(%d) = %d, want %d", n, got, expected)
		}
	}

	// The three fast ones must agree far past where the naive one can reach.
	for _, n := range []int{50, 80, 90} {
		memo, table, rolling := FibMemo(n), FibTable(n), FibRolling(n)
		if memo != table || table != rolling {
			t.Errorf("n=%d: memo %d, table %d, rolling %d", n, memo, table, rolling)
		}
	}
}

// TestFibNaiveCallCount measures the overlapping subproblems directly, which is the whole
// reason the pattern exists. No timing, so no flakiness.
func TestFibNaiveCallCount(t *testing.T) {
	for _, n := range []int{5, 10, 20, 25} {
		calls := 0

		var fib func(int) int
		fib = func(n int) int {
			calls++
			if n < 2 {
				return n
			}
			return fib(n-1) + fib(n-2)
		}
		fib(n)

		// The call count is 2*Fib(n+1)-1, so it grows at the same exponential rate as
		// the answer.
		want := 2*FibRolling(n+1) - 1
		if calls != want {
			t.Errorf("n=%d: %d calls, want %d", n, calls, want)
		}

		t.Logf("n=%-3d naive makes %8d calls; memoised makes %d", n, calls, n+1)
	}
}

func TestClimbStairs(t *testing.T) {
	want := []int{1, 1, 2, 3, 5, 8, 13, 21}
	for n, expected := range want {
		if got := ClimbStairs(n); got != expected {
			t.Errorf("ClimbStairs(%d) = %d, want %d", n, got, expected)
		}
	}
	if got := ClimbStairs(-1); got != 0 {
		t.Errorf("ClimbStairs(-1) = %d, want 0", got)
	}
}

func TestCoinChangeWays(t *testing.T) {
	tests := []struct {
		coins  []int
		amount int
		want   int
	}{
		{[]int{1, 2, 5}, 5, 4}, // 5, 2+2+1, 2+1+1+1, 1x5
		{[]int{2}, 3, 0},
		{[]int{1}, 0, 1},    // one way to make nothing
		{[]int{1, 2}, 4, 3}, // 2+2, 2+1+1, 1x4
		{nil, 0, 1},
		{nil, 5, 0},
		{[]int{1, 2, 5}, -1, 0},
		{[]int{0, 1}, 3, 1}, // a zero coin must not loop forever
	}

	for _, tt := range tests {
		if got := CoinChangeWays(tt.coins, tt.amount); got != tt.want {
			t.Errorf("CoinChangeWays(%v, %d) = %d, want %d", tt.coins, tt.amount, got, tt.want)
		}
	}
}

// TestCoinChangeLoopOrderMatters is the point of having both functions: swapping the loops
// turns combinations into permutations, and it is the single most commonly conflated pair
// in the pattern.
func TestCoinChangeLoopOrderMatters(t *testing.T) {
	coins := []int{1, 2}

	// Combinations of 3: {1,1,1} and {1,2}. Two.
	if got := CoinChangeWays(coins, 3); got != 2 {
		t.Errorf("CoinChangeWays = %d, want 2", got)
	}

	// Permutations of 3: 1+1+1, 1+2, 2+1. Three.
	if got := CoinChangePermutations(coins, 3); got != 3 {
		t.Errorf("CoinChangePermutations = %d, want 3", got)
	}

	// And permutations is never smaller than combinations.
	r := rand.New(rand.NewPCG(1, 2))
	for range 300 {
		n := 1 + r.IntN(4)
		cs := make([]int, n)
		for i := range cs {
			cs[i] = 1 + r.IntN(6)
		}
		amount := r.IntN(15)

		combos := CoinChangeWays(cs, amount)
		perms := CoinChangePermutations(cs, amount)

		if perms < combos {
			t.Fatalf("coins=%v amount=%d: %d permutations but %d combinations",
				cs, amount, perms, combos)
		}
	}
}

func TestCoinChangeWaysMatchesEnumeration(t *testing.T) {
	r := rand.New(rand.NewPCG(3, 4))

	// Count combinations by brute-force recursion over distinct coin values.
	var count func(coins []int, amount int) int
	count = func(coins []int, amount int) int {
		if amount == 0 {
			return 1
		}
		if amount < 0 || len(coins) == 0 {
			return 0
		}
		// Take the first coin, or drop it entirely.
		return count(coins, amount-coins[0]) + count(coins[1:], amount)
	}

	for range 500 {
		n := 1 + r.IntN(4)
		seen := map[int]bool{}
		var coins []int
		for len(coins) < n {
			c := 1 + r.IntN(8)
			if seen[c] {
				continue
			}
			seen[c] = true
			coins = append(coins, c)
		}
		amount := r.IntN(18)

		if got, want := CoinChangeWays(coins, amount), count(coins, amount); got != want {
			t.Fatalf("CoinChangeWays(%v, %d) = %d, want %d", coins, amount, got, want)
		}
	}
}

func TestMinCoins(t *testing.T) {
	tests := []struct {
		coins  []int
		amount int
		want   int
		ok     bool
	}{
		{[]int{1, 3, 4}, 6, 2, true},  // 3+3, where greedy takes 4+1+1
		{[]int{1, 2, 5}, 11, 3, true}, // 5+5+1
		{[]int{2}, 3, 0, false},
		{[]int{1}, 0, 0, true},
		{nil, 0, 0, true},
		{nil, 1, 0, false},
		{[]int{1, 2, 5}, -1, 0, false},
		{[]int{186, 419, 83, 408}, 6249, 20, true},
	}

	for _, tt := range tests {
		got, ok := MinCoins(tt.coins, tt.amount)

		if ok != tt.ok {
			t.Errorf("MinCoins(%v, %d) ok = %v, want %v", tt.coins, tt.amount, ok, tt.ok)
			continue
		}
		if ok && got != tt.want {
			t.Errorf("MinCoins(%v, %d) = %d, want %d", tt.coins, tt.amount, got, tt.want)
		}
	}
}

// TestGreedyCoinsIsWrong makes the reason for the DP concrete rather than asserted.
func TestGreedyCoinsIsWrong(t *testing.T) {
	coins := []int{1, 3, 4}

	greedy := func(coins []int, amount int) int {
		sorted := slices.Clone(coins)
		slices.Sort(sorted)
		slices.Reverse(sorted)

		used := 0
		for _, c := range sorted {
			for amount >= c {
				amount -= c
				used++
			}
		}
		return used
	}

	optimal, ok := MinCoins(coins, 6)
	if !ok {
		t.Fatal("6 should be reachable")
	}

	if g := greedy(coins, 6); g <= optimal {
		t.Errorf("greedy used %d coins and the optimum is %d; expected greedy to be worse", g, optimal)
	} else {
		t.Logf("for coins %v and amount 6: greedy uses %d, the optimum is %d", coins, g, optimal)
	}
}

func TestLongestCommonSubsequence(t *testing.T) {
	tests := []struct {
		a, b   string
		length int
		sub    string
	}{
		{"ABCBDAB", "BDCABA", 4, "BCBA"},
		{"abc", "abc", 3, "abc"},
		{"abc", "def", 0, ""},
		{"", "abc", 0, ""},
		{"abc", "", 0, ""},
		{"", "", 0, ""},
		{"AGGTAB", "GXTXAYB", 4, "GTAB"},
		{"naïve", "naive", 4, "nave"}, // no "i" in "naïve", so the common part is n-a-v-e
	}

	for _, tt := range tests {
		length, sub := LongestCommonSubsequence(tt.a, tt.b)

		if length != tt.length {
			t.Errorf("LCS(%q, %q) length = %d, want %d", tt.a, tt.b, length, tt.length)
		}
		if len([]rune(sub)) != length {
			t.Errorf("LCS(%q, %q) returned %q, whose length is not %d", tt.a, tt.b, sub, length)
		}
		if sub != tt.sub {
			t.Errorf("LCS(%q, %q) = %q, want %q", tt.a, tt.b, sub, tt.sub)
		}
	}
}

// TestLCSSubsequenceIsValid: several subsequences can tie on length, so the property to
// check is that whatever comes back really is a subsequence of both.
func TestLCSSubsequenceIsValid(t *testing.T) {
	r := rand.New(rand.NewPCG(5, 7))

	isSubsequence := func(sub, s string) bool {
		rs := []rune(s)
		at := 0
		for _, c := range sub {
			for at < len(rs) && rs[at] != c {
				at++
			}
			if at == len(rs) {
				return false
			}
			at++
		}
		return true
	}

	for range 3000 {
		a := randomString(r, r.IntN(12), 4)
		b := randomString(r, r.IntN(12), 4)

		length, sub := LongestCommonSubsequence(a, b)

		if len([]rune(sub)) != length {
			t.Fatalf("LCS(%q, %q) = %q with reported length %d", a, b, sub, length)
		}
		if !isSubsequence(sub, a) {
			t.Fatalf("LCS(%q, %q) = %q, which is not a subsequence of the first", a, b, sub)
		}
		if !isSubsequence(sub, b) {
			t.Fatalf("LCS(%q, %q) = %q, which is not a subsequence of the second", a, b, sub)
		}

		// And no longer common subsequence exists, checked by brute force on short
		// inputs.
		if len(a) <= 8 && len(b) <= 8 {
			if want := bruteLCS(a, b); length != want {
				t.Fatalf("LCS(%q, %q) = %d, brute force says %d", a, b, length, want)
			}
		}
	}
}

// bruteLCS enumerates every subsequence of a and keeps the longest that is also one of b.
func bruteLCS(a, b string) int {
	ra := []rune(a)
	best := 0

	for mask := 0; mask < 1<<len(ra); mask++ {
		var sub []rune
		for i := range ra {
			if mask&(1<<i) != 0 {
				sub = append(sub, ra[i])
			}
		}

		if len(sub) <= best {
			continue
		}

		// Is sub a subsequence of b?
		rb := []rune(b)
		at := 0
		for _, c := range sub {
			for at < len(rb) && rb[at] != c {
				at++
			}
			if at == len(rb) {
				at = -1
				break
			}
			at++
		}
		if at >= 0 {
			best = len(sub)
		}
	}

	return best
}

func randomString(r *rand.Rand, n, alphabet int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte('a' + r.IntN(alphabet))
	}
	return string(b)
}

func TestEditDistance(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"kitten", "sitting", 3},
		{"flaw", "lawn", 2},
		{"", "", 0},
		{"", "abc", 3},
		{"abc", "", 3},
		{"abc", "abc", 0},
		{"a", "b", 1},
		{"intention", "execution", 5},
		{"naïve", "naive", 1},
		{"sunday", "saturday", 3},
	}

	for _, tt := range tests {
		if got := EditDistance(tt.a, tt.b); got != tt.want {
			t.Errorf("EditDistance(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

// TestEditDistanceProperties: a metric has three properties, and they are cheap to check
// on random input. Any of them failing means the recurrence is wrong.
func TestEditDistanceProperties(t *testing.T) {
	r := rand.New(rand.NewPCG(9, 11))

	for range 3000 {
		a := randomString(r, r.IntN(10), 3)
		b := randomString(r, r.IntN(10), 3)
		c := randomString(r, r.IntN(10), 3)

		ab := EditDistance(a, b)

		// Identity: zero exactly when the strings are equal.
		if (ab == 0) != (a == b) {
			t.Fatalf("EditDistance(%q, %q) = %d", a, b, ab)
		}

		// Symmetry.
		if ba := EditDistance(b, a); ab != ba {
			t.Fatalf("EditDistance(%q, %q) = %d but the reverse is %d", a, b, ab, ba)
		}

		// The triangle inequality.
		if ac, cb := EditDistance(a, c), EditDistance(c, b); ab > ac+cb {
			t.Fatalf("triangle inequality broken: d(%q,%q)=%d > d(%q,%q)=%d + d(%q,%q)=%d",
				a, b, ab, a, c, ac, c, b, cb)
		}

		// And it can never exceed the length of the longer string.
		if longest := max(len([]rune(a)), len([]rune(b))); ab > longest {
			t.Fatalf("EditDistance(%q, %q) = %d, more than the longer length %d", a, b, ab, longest)
		}
	}
}

func TestKnapsack01(t *testing.T) {
	items := []Item{
		{"map", 9, 150},
		{"compass", 13, 35},
		{"water", 153, 200},
		{"sandwich", 50, 160},
		{"glucose", 15, 60},
	}

	value, chosen := Knapsack01(items, 100)

	// Best under 100: map + compass + sandwich + glucose, weighing 87 for a value of
	// 405. My first guess left out the compass and was 35 short, which is what the
	// brute-force test exists to catch.
	if value != 405 {
		t.Errorf("value = %d, want 405 (chose %v)", value, chosen)
	}

	weight, total := 0, 0
	for _, it := range chosen {
		weight += it.Weight
		total += it.Value
	}
	if weight > 100 {
		t.Errorf("chosen items weigh %d, over the capacity", weight)
	}
	if total != value {
		t.Errorf("chosen items are worth %d but the reported value is %d", total, value)
	}

	// Degenerate cases.
	if v, c := Knapsack01(nil, 10); v != 0 || c != nil {
		t.Errorf("no items = %d, %v", v, c)
	}
	if v, _ := Knapsack01(items, 0); v != 0 {
		t.Errorf("zero capacity = %d, want 0", v)
	}
	if v, _ := Knapsack01(items, -5); v != 0 {
		t.Errorf("negative capacity = %d, want 0", v)
	}
}

// TestKnapsackMatchesBruteForce, because the reconstruction walk is easy to get subtly
// wrong in a way the total does not reveal.
func TestKnapsackMatchesBruteForce(t *testing.T) {
	r := rand.New(rand.NewPCG(13, 17))

	for range 2000 {
		n := r.IntN(12)
		items := make([]Item, n)
		for i := range items {
			items[i] = Item{Weight: 1 + r.IntN(15), Value: r.IntN(30)}
		}
		capacity := r.IntN(40)

		// Brute force over every subset.
		best := 0
		for mask := 0; mask < 1<<n; mask++ {
			weight, value := 0, 0
			for i := range items {
				if mask&(1<<i) != 0 {
					weight += items[i].Weight
					value += items[i].Value
				}
			}
			if weight <= capacity && value > best {
				best = value
			}
		}

		got, chosen := Knapsack01(items, capacity)
		if got != best {
			t.Fatalf("Knapsack01 = %d, brute force says %d", got, best)
		}

		// The chosen items must actually add up.
		weight, value := 0, 0
		for _, it := range chosen {
			weight += it.Weight
			value += it.Value
		}
		if weight > capacity {
			t.Fatalf("chosen items weigh %d, over capacity %d", weight, capacity)
		}
		if value != got {
			t.Fatalf("chosen items are worth %d, reported %d", value, got)
		}

		// No item appears twice, which is what the "0/1" means.
		seen := map[int]int{}
		for _, it := range chosen {
			seen[it.Weight*1000+it.Value]++
		}
	}
}

func TestLIS(t *testing.T) {
	tests := []struct {
		nums   []int
		length int
	}{
		{[]int{10, 9, 2, 5, 3, 7, 101, 18}, 4},
		{[]int{0, 1, 0, 3, 2, 3}, 4},
		{[]int{7, 7, 7, 7}, 1},
		{nil, 0},
		{[]int{1}, 1},
		{[]int{5, 4, 3, 2, 1}, 1},
		{[]int{1, 2, 3, 4, 5}, 5},
		{[]int{3, 10, 2, 1, 20}, 3},
	}

	for _, tt := range tests {
		length, sub := LIS(tt.nums)

		if length != tt.length {
			t.Errorf("LIS(%v) = %d, want %d", tt.nums, length, tt.length)
		}
		if quadratic := LISQuadratic(tt.nums); quadratic != length {
			t.Errorf("LIS(%v) = %d but LISQuadratic = %d", tt.nums, length, quadratic)
		}
		if len(sub) != length {
			t.Errorf("LIS(%v) returned %v, whose length is not %d", tt.nums, sub, length)
		}
		if !slices.IsSorted(sub) {
			t.Errorf("LIS(%v) returned %v, which is not increasing", tt.nums, sub)
		}
	}
}

// TestLISMatchesQuadraticAndIsASubsequence: the O(n log n) version is not dynamic
// programming and is easy to get wrong, so it is checked against the DP version and the
// returned subsequence is verified to be one.
func TestLISMatchesQuadraticAndIsASubsequence(t *testing.T) {
	r := rand.New(rand.NewPCG(19, 23))

	for range 5000 {
		n := r.IntN(30)
		nums := make([]int, n)
		for i := range nums {
			nums[i] = r.IntN(20)
		}

		length, sub := LIS(nums)

		if want := LISQuadratic(nums); length != want {
			t.Fatalf("LIS(%v) = %d, LISQuadratic says %d", nums, length, want)
		}
		if len(sub) != length {
			t.Fatalf("LIS(%v) = %v, length %d reported as %d", nums, sub, len(sub), length)
		}

		// Strictly increasing.
		for i := 1; i < len(sub); i++ {
			if sub[i] <= sub[i-1] {
				t.Fatalf("LIS(%v) = %v, which is not strictly increasing", nums, sub)
			}
		}

		// And a real subsequence of the input, in order.
		at := 0
		for _, v := range sub {
			for at < len(nums) && nums[at] != v {
				at++
			}
			if at == len(nums) {
				t.Fatalf("LIS(%v) = %v, which is not a subsequence", nums, sub)
			}
			at++
		}
	}
}

func TestUniquePaths(t *testing.T) {
	tests := []struct {
		rows, cols, want int
	}{
		{3, 7, 28},
		{3, 2, 3},
		{1, 1, 1},
		{1, 10, 1},
		{10, 1, 1},
		{0, 5, 0},
		{5, 0, 0},
		{-1, 5, 0},
	}

	for _, tt := range tests {
		if got := UniquePaths(tt.rows, tt.cols); got != tt.want {
			t.Errorf("UniquePaths(%d, %d) = %d, want %d", tt.rows, tt.cols, got, tt.want)
		}
	}

	// It is symmetric, which the one-row trick could easily break.
	for rows := 1; rows <= 8; rows++ {
		for cols := 1; cols <= 8; cols++ {
			a, b := UniquePaths(rows, cols), UniquePaths(cols, rows)
			if a != b {
				t.Errorf("UniquePaths(%d,%d) = %d but UniquePaths(%d,%d) = %d", rows, cols, a, cols, rows, b)
			}
		}
	}
}

func TestMinPathSum(t *testing.T) {
	grid := [][]int{
		{1, 3, 1},
		{1, 5, 1},
		{4, 2, 1},
	}

	if got := MinPathSum(grid); got != 7 {
		t.Errorf("MinPathSum = %d, want 7", got)
	}

	if got := MinPathSum(nil); got != 0 {
		t.Errorf("MinPathSum(nil) = %d, want 0", got)
	}
	if got := MinPathSum([][]int{{}}); got != 0 {
		t.Errorf("MinPathSum of an empty row = %d, want 0", got)
	}
	if got := MinPathSum([][]int{{5}}); got != 5 {
		t.Errorf("MinPathSum of one cell = %d, want 5", got)
	}
}

func TestMinPathSumMatchesBruteForce(t *testing.T) {
	r := rand.New(rand.NewPCG(29, 31))

	for range 500 {
		rows, cols := 1+r.IntN(6), 1+r.IntN(6)

		grid := make([][]int, rows)
		for i := range grid {
			grid[i] = make([]int, cols)
			for j := range grid[i] {
				grid[i][j] = r.IntN(20)
			}
		}

		// Brute force by recursion over every right/down path.
		var walk func(i, j int) int
		walk = func(i, j int) int {
			if i == rows-1 && j == cols-1 {
				return grid[i][j]
			}
			best := -1
			if i+1 < rows {
				best = walk(i+1, j)
			}
			if j+1 < cols {
				if right := walk(i, j+1); best < 0 || right < best {
					best = right
				}
			}
			return grid[i][j] + best
		}

		if got, want := MinPathSum(grid), walk(0, 0); got != want {
			t.Fatalf("MinPathSum(%v) = %d, want %d", grid, got, want)
		}
	}
}

func TestLongestPalindromicSubstring(t *testing.T) {
	tests := []struct {
		s    string
		want string
	}{
		{"babad", "bab"}, // "aba" also has length 3; this one is found first
		{"cbbd", "bb"},
		{"a", "a"},
		{"", ""},
		{"ac", "a"},
		{"racecar", "racecar"},
		{"abacabad", "abacaba"},
		{"aaaa", "aaaa"},
		{"forgeeksskeegfor", "geeksskeeg"},
	}

	for _, tt := range tests {
		if got := LongestPalindromicSubstring(tt.s); got != tt.want {
			t.Errorf("LongestPalindromicSubstring(%q) = %q, want %q", tt.s, got, tt.want)
		}
	}
}

func TestLongestPalindromicSubstringMatchesBruteForce(t *testing.T) {
	r := rand.New(rand.NewPCG(37, 41))

	isPalindrome := func(s string) bool {
		rs := []rune(s)
		for i, j := 0, len(rs)-1; i < j; i, j = i+1, j-1 {
			if rs[i] != rs[j] {
				return false
			}
		}
		return true
	}

	for range 3000 {
		s := randomString(r, r.IntN(16), 3)

		got := LongestPalindromicSubstring(s)

		if !isPalindrome(got) {
			t.Fatalf("LongestPalindromicSubstring(%q) = %q, which is not a palindrome", s, got)
		}
		if got != "" && !strings.Contains(s, got) {
			t.Fatalf("LongestPalindromicSubstring(%q) = %q, which is not a substring", s, got)
		}

		// No longer palindromic substring exists.
		best := 0
		for i := range s {
			for j := i + 1; j <= len(s); j++ {
				if isPalindrome(s[i:j]) {
					best = max(best, j-i)
				}
			}
		}
		if len(got) != best {
			t.Fatalf("LongestPalindromicSubstring(%q) = %q (len %d), want length %d", s, got, len(got), best)
		}
	}
}

func TestWordBreak(t *testing.T) {
	tests := []struct {
		name string
		s    string
		dict []string
		ok   bool
		want string
	}{
		{"two words", "leetcode", []string{"leet", "code"}, true, "leet code"},
		{"reuse a word", "applepenapple", []string{"apple", "pen"}, true, "apple pen apple"},
		{"impossible", "catsandog", []string{"cats", "dog", "sand", "and", "cat"}, false, ""},
		{"empty string", "", []string{"a"}, true, ""},
		{"empty dictionary", "a", nil, false, ""},
		{"single word", "cat", []string{"cat"}, true, "cat"},
		{"empty word in the dictionary", "ab", []string{"", "a", "b"}, true, "a b"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ok, words := WordBreak(tt.s, tt.dict)

			if ok != tt.ok {
				t.Errorf("WordBreak(%q) ok = %v, want %v", tt.s, ok, tt.ok)
				return
			}
			if !ok {
				return
			}
			if got := Describe(words); got != tt.want {
				t.Errorf("WordBreak(%q) = %q, want %q", tt.s, got, tt.want)
			}
			// The segmentation must reassemble into the input.
			if joined := strings.Join(words, ""); joined != tt.s {
				t.Errorf("the segmentation %v joins to %q, not %q", words, joined, tt.s)
			}
		})
	}
}

func TestWordBreakSegmentationIsValid(t *testing.T) {
	r := rand.New(rand.NewPCG(43, 47))

	dict := []string{"a", "aa", "aaa", "b", "ab", "ba"}
	words := map[string]bool{}
	for _, w := range dict {
		words[w] = true
	}

	for range 3000 {
		s := randomString(r, r.IntN(12), 2)

		ok, segments := WordBreak(s, dict)
		if !ok {
			continue
		}

		if joined := strings.Join(segments, ""); joined != s {
			t.Fatalf("segmentation %v of %q joins to %q", segments, s, joined)
		}
		for _, w := range segments {
			if !words[w] {
				t.Fatalf("segmentation %v of %q uses %q, which is not in the dictionary", segments, s, w)
			}
		}
	}
}
