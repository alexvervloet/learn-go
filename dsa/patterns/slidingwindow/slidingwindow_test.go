package slidingwindow

import (
	"errors"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
)

func TestMaxSumOfSize(t *testing.T) {
	tests := []struct {
		name string
		nums []int
		k    int
		want int
		err  bool
	}{
		{"classic", []int{2, 1, 5, 1, 3, 2}, 3, 9, false}, // 5+1+3
		{"k of 1", []int{2, 1, 5}, 1, 5, false},
		{"whole slice", []int{1, 2, 3}, 3, 6, false},
		{"negatives", []int{-1, -2, -3, -1}, 2, -3, false},
		{"all equal", []int{4, 4, 4}, 2, 8, false},
		{"k too large", []int{1, 2}, 3, 0, true},
		{"k is zero", []int{1, 2}, 0, 0, true},
		{"k is negative", []int{1, 2}, -1, 0, true},
		{"empty input", nil, 1, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := MaxSumOfSize(tt.nums, tt.k)

			if tt.err {
				if err == nil {
					t.Errorf("expected an error, got %d", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("MaxSumOfSize(%v, %d) = %d, want %d", tt.nums, tt.k, got, tt.want)
			}
		})
	}
}

// TestMaxSumOfSizeMatchesBruteForce: the sliding version has two additions per step and
// an O(n*k) rescan is the obviously correct answer, so they must agree.
func TestMaxSumOfSizeMatchesBruteForce(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))

	brute := func(nums []int, k int) int {
		best := 0
		for i := 0; i+k <= len(nums); i++ {
			sum := 0
			for _, v := range nums[i : i+k] {
				sum += v
			}
			if i == 0 || sum > best {
				best = sum
			}
		}
		return best
	}

	for range 3000 {
		n := 1 + r.IntN(30)
		nums := make([]int, n)
		for i := range nums {
			nums[i] = r.IntN(40) - 20 // negatives included
		}
		k := 1 + r.IntN(n)

		got, err := MaxSumOfSize(nums, k)
		if err != nil {
			t.Fatalf("unexpected error for n=%d k=%d: %v", n, k, err)
		}
		if want := brute(nums, k); got != want {
			t.Fatalf("MaxSumOfSize(%v, %d) = %d, want %d", nums, k, got, want)
		}
	}
}

func TestAverageOfSize(t *testing.T) {
	got, err := AverageOfSize([]int{1, 3, 2, 6, -1, 4, 1, 8, 2}, 5)
	if err != nil {
		t.Fatal(err)
	}

	want := []float64{2.2, 2.8, 2.4, 3.6, 2.8}
	if !slices.Equal(got, want) {
		t.Errorf("AverageOfSize = %v, want %v", got, want)
	}

	// The count is len(nums)-k+1, which is the off-by-one worth pinning.
	for _, tc := range []struct{ n, k, want int }{
		{5, 1, 5}, {5, 5, 1}, {5, 3, 3}, {1, 1, 1},
	} {
		nums := make([]int, tc.n)
		got, err := AverageOfSize(nums, tc.k)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != tc.want {
			t.Errorf("n=%d k=%d gave %d windows, want %d", tc.n, tc.k, len(got), tc.want)
		}
	}

	if _, err := AverageOfSize([]int{1}, 2); !errors.Is(err, ErrWindowTooLarge) {
		t.Errorf("expected ErrWindowTooLarge, got %v", err)
	}
}

func TestMaxOfEachWindow(t *testing.T) {
	tests := []struct {
		name string
		nums []int
		k    int
		want []int
	}{
		{"classic", []int{1, 3, -1, -3, 5, 3, 6, 7}, 3, []int{3, 3, 5, 5, 6, 7}},
		{"k of 1", []int{4, 2, 7}, 1, []int{4, 2, 7}},
		{"whole slice", []int{4, 2, 7}, 3, []int{7}},
		{"descending", []int{5, 4, 3, 2, 1}, 2, []int{5, 4, 3, 2}},
		{"ascending", []int{1, 2, 3, 4, 5}, 2, []int{2, 3, 4, 5}},
		{"all equal", []int{7, 7, 7}, 2, []int{7, 7}},
		{"maximum falls out of the window", []int{9, 1, 1, 1}, 2, []int{9, 1, 1}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := MaxOfEachWindow(tt.nums, tt.k)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("MaxOfEachWindow(%v, %d) = %v, want %v", tt.nums, tt.k, got, tt.want)
			}
		})
	}
}

// TestMaxOfEachWindowMatchesBruteForce: the monotonic deque is the only genuinely
// tricky code in this package, so it gets a randomised check against the obvious O(n*k)
// version.
func TestMaxOfEachWindowMatchesBruteForce(t *testing.T) {
	r := rand.New(rand.NewPCG(3, 4))

	for range 5000 {
		n := 1 + r.IntN(40)
		nums := make([]int, n)
		for i := range nums {
			nums[i] = r.IntN(20) - 10
		}
		k := 1 + r.IntN(n)

		got, err := MaxOfEachWindow(nums, k)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		want := make([]int, 0, n-k+1)
		for i := 0; i+k <= n; i++ {
			want = append(want, slices.Max(nums[i:i+k]))
		}

		if !slices.Equal(got, want) {
			t.Fatalf("MaxOfEachWindow(%v, %d) = %v, want %v", nums, k, got, want)
		}
	}
}

func TestLongestUniqueSubstring(t *testing.T) {
	tests := []struct {
		s    string
		want int
	}{
		{"abcabcbb", 3}, // abc
		{"bbbbb", 1},
		{"pwwkew", 3}, // wke
		{"", 0},
		{"a", 1},
		{"abcdef", 6},
		{"abba", 2},    // the case that catches left moving backwards
		{"tmmzuxt", 5}, // mzuxt
		{"naïve", 5},   // multi-byte, so a byte-indexed version gets 6
		{"日本語日本", 3},
	}

	for _, tt := range tests {
		if got := LongestUniqueSubstring(tt.s); got != tt.want {
			t.Errorf("LongestUniqueSubstring(%q) = %d, want %d", tt.s, got, tt.want)
		}
	}
}

// TestLongestUniqueSubstringMatchesBruteForce, because the `>= left` check is subtle
// enough that a table of examples is not convincing.
func TestLongestUniqueSubstringMatchesBruteForce(t *testing.T) {
	r := rand.New(rand.NewPCG(5, 7))

	brute := func(s string) int {
		runes := []rune(s)
		best := 0
		for i := range runes {
			seen := map[rune]bool{}
			for j := i; j < len(runes); j++ {
				if seen[runes[j]] {
					break
				}
				seen[runes[j]] = true
				best = max(best, j-i+1)
			}
		}
		return best
	}

	alphabet := []rune("abcdé日")

	for range 5000 {
		n := r.IntN(20)
		b := make([]rune, n)
		for i := range b {
			b[i] = alphabet[r.IntN(len(alphabet))]
		}
		s := string(b)

		if got, want := LongestUniqueSubstring(s), brute(s); got != want {
			t.Fatalf("LongestUniqueSubstring(%q) = %d, want %d", s, got, want)
		}
	}
}

func TestLongestOnesWithFlips(t *testing.T) {
	tests := []struct {
		nums []int
		k    int
		want int
	}{
		{[]int{1, 1, 1, 0, 0, 0, 1, 1, 1, 1, 0}, 2, 6},
		{[]int{0, 0, 1, 1, 0, 0, 1, 1, 1, 0, 1, 1, 0, 0, 0, 1, 1, 1, 1}, 3, 10},
		{[]int{1, 1, 1}, 0, 3},
		{[]int{0, 0, 0}, 0, 0},
		{[]int{0, 0, 0}, 3, 3},
		{[]int{0, 0, 0}, 99, 3},
		{nil, 2, 0},
		{[]int{1}, 0, 1},
	}

	for _, tt := range tests {
		if got := LongestOnesWithFlips(tt.nums, tt.k); got != tt.want {
			t.Errorf("LongestOnesWithFlips(%v, %d) = %d, want %d", tt.nums, tt.k, got, tt.want)
		}
	}
}

func TestMinSubarrayAtLeast(t *testing.T) {
	tests := []struct {
		nums   []int
		target int
		want   int
	}{
		{[]int{2, 1, 5, 2, 3, 2}, 7, 2}, // 5+2
		{[]int{2, 1, 5, 2, 8}, 7, 1},    // 8 alone
		{[]int{3, 4, 1, 1, 6}, 8, 3},    // 3+4+1 or 1+1+6
		{[]int{1, 1, 1}, 10, 0},         // impossible
		{nil, 1, 0},
		{[]int{5}, 5, 1},
		{[]int{1, 2, 3, 4, 5}, 15, 5}, // the whole slice
		{[]int{1, 2, 3}, 0, 0},        // a target of 0 is met by the EMPTY run
		{[]int{1, 2, 3}, -5, 0},       // and so is any negative target        // a target of 0 is met by one element
	}

	for _, tt := range tests {
		if got := MinSubarrayAtLeast(tt.nums, tt.target); got != tt.want {
			t.Errorf("MinSubarrayAtLeast(%v, %d) = %d, want %d", tt.nums, tt.target, got, tt.want)
		}
	}
}

func TestMinSubarrayAtLeastMatchesBruteForce(t *testing.T) {
	r := rand.New(rand.NewPCG(9, 11))

	brute := func(nums []int, target int) int {
		best := 0
		for i := range nums {
			sum := 0
			for j := i; j < len(nums); j++ {
				sum += nums[j]
				if sum >= target {
					if length := j - i + 1; best == 0 || length < best {
						best = length
					}
					break
				}
			}
		}
		return best
	}

	for range 5000 {
		n := r.IntN(25)
		nums := make([]int, n)
		for i := range nums {
			nums[i] = r.IntN(10) // non-negative, which the function requires
		}
		target := 1 + r.IntN(30) // positive, which the function requires

		if got, want := MinSubarrayAtLeast(nums, target), brute(nums, target); got != want {
			t.Fatalf("MinSubarrayAtLeast(%v, %d) = %d, want %d", nums, target, got, want)
		}
	}
}

func TestMinWindowContaining(t *testing.T) {
	tests := []struct {
		name    string
		s       string
		pattern string
		want    string
	}{
		{"classic", "ADOBECODEBANC", "ABC", "BANC"},
		{"whole string", "a", "a", "a"},
		{"impossible", "a", "aa", ""},
		{"duplicates in the pattern", "aaflslflsldkalskaaa", "aaa", "aaa"},
		{"empty pattern", "abc", "", ""},
		{"empty string", "", "a", ""},
		{"pattern longer than the string", "ab", "abc", ""},
		{"pattern is the string", "abc", "cba", "abc"},
		{"repeat required twice", "abbc", "bb", "bb"},
		{"one copy is not enough", "abc", "bb", ""},
		{"multi-byte", "aéb éc", "é é", "éb é"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MinWindowContaining(tt.s, tt.pattern); got != tt.want {
				t.Errorf("MinWindowContaining(%q, %q) = %q, want %q", tt.s, tt.pattern, got, tt.want)
			}
		})
	}
}

// TestMinWindowContainingMatchesBruteForce is the one that matters: the `missing`
// bookkeeping is the subtlest code in the package, and a duplicate-heavy alphabet is
// where it breaks.
func TestMinWindowContainingMatchesBruteForce(t *testing.T) {
	r := rand.New(rand.NewPCG(13, 17))

	contains := func(s, pattern string) bool {
		need := map[rune]int{}
		for _, c := range pattern {
			need[c]++
		}
		for _, c := range s {
			need[c]--
		}
		for _, n := range need {
			if n > 0 {
				return false
			}
		}
		return true
	}

	brute := func(s, pattern string) string {
		if pattern == "" {
			return ""
		}
		runes := []rune(s)
		best := ""
		for i := range runes {
			for j := i + 1; j <= len(runes); j++ {
				sub := string(runes[i:j])
				if !contains(sub, pattern) {
					continue
				}
				if best == "" || len([]rune(sub)) < len([]rune(best)) {
					best = sub
				}
				break // longer windows starting here cannot be shorter
			}
		}
		return best
	}

	for range 4000 {
		n := r.IntN(16)
		b := make([]byte, n)
		for i := range b {
			b[i] = byte('a' + r.IntN(3)) // only three letters, so duplicates abound
		}
		s := string(b)

		m := r.IntN(4)
		p := make([]byte, m)
		for i := range p {
			p[i] = byte('a' + r.IntN(3))
		}
		pattern := string(p)

		got := MinWindowContaining(s, pattern)
		want := brute(s, pattern)

		// Several windows can tie on length; only the length is guaranteed.
		if len(got) != len(want) {
			t.Fatalf("MinWindowContaining(%q, %q) = %q (len %d), want length %d (e.g. %q)",
				s, pattern, got, len(got), len(want), want)
		}
		if got != "" && !contains(got, pattern) {
			t.Fatalf("MinWindowContaining(%q, %q) = %q, which does not contain the pattern",
				s, pattern, got)
		}
		if got != "" && !strings.Contains(s, got) {
			t.Fatalf("MinWindowContaining(%q, %q) = %q, which is not a substring", s, pattern, got)
		}
	}
}

func TestCountSubarraysWithSum(t *testing.T) {
	tests := []struct {
		nums   []int
		target int
		want   int
	}{
		{[]int{1, 1, 1}, 2, 2},
		{[]int{1, 2, 3}, 3, 2},  // {3} and {1,2}
		{[]int{1, -1, 0}, 0, 3}, // negatives, which no window can handle
		{[]int{3, 4, 7, 2, -3, 1, 4, 2}, 7, 4},
		{nil, 0, 0},
		{[]int{0, 0, 0}, 0, 6}, // every run of a zero-sum slice
		{[]int{1}, 1, 1},
		{[]int{1}, 2, 0},
	}

	for _, tt := range tests {
		if got := CountSubarraysWithSum(tt.nums, tt.target); got != tt.want {
			t.Errorf("CountSubarraysWithSum(%v, %d) = %d, want %d", tt.nums, tt.target, got, tt.want)
		}
	}
}

func TestCountSubarraysWithSumMatchesBruteForce(t *testing.T) {
	r := rand.New(rand.NewPCG(19, 23))

	for range 5000 {
		n := r.IntN(20)
		nums := make([]int, n)
		for i := range nums {
			nums[i] = r.IntN(9) - 4 // negatives on purpose
		}
		target := r.IntN(11) - 5

		want := 0
		for i := range nums {
			sum := 0
			for j := i; j < n; j++ {
				sum += nums[j]
				if sum == target {
					want++
				}
			}
		}

		if got := CountSubarraysWithSum(nums, target); got != want {
			t.Fatalf("CountSubarraysWithSum(%v, %d) = %d, want %d", nums, target, got, want)
		}
	}
}

// TestLeftOnlyMovesForward is the amortised argument, checked rather than asserted in
// prose. Across a whole run, the shrink loop advances left at most n times in total,
// which is why the nested loop is still O(n).
func TestLeftOnlyMovesForward(t *testing.T) {
	r := rand.New(rand.NewPCG(29, 31))

	const n = 100_000
	nums := make([]int, n)
	for i := range nums {
		nums[i] = r.IntN(2) // a 0/1 sequence, so the window shrinks constantly
	}

	// Instrumented copy of LongestOnesWithFlips, counting left's total movement.
	left, zeros, best, moves := 0, 0, 0, 0
	for right, bit := range nums {
		if bit == 0 {
			zeros++
		}
		for zeros > 5 {
			if nums[left] == 0 {
				zeros--
			}
			left++
			moves++
		}
		best = max(best, right-left+1)
	}

	if moves > n {
		t.Errorf("left moved %d times over %d elements; the amortised bound is n", moves, n)
	}
	t.Logf("left moved %d times over %d elements, and the answer is %d", moves, n, best)

	if got := LongestOnesWithFlips(nums, 5); got != best {
		t.Errorf("the instrumented copy disagrees with the real function: %d and %d", best, got)
	}
}
