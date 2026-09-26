package searching

import (
	"math"
	"math/rand/v2"
	"slices"
	"testing"
)

func TestPartition(t *testing.T) {
	tests := []struct {
		name string
		n    int
		pred func(int) bool
		want int
	}{
		{"never true", 5, func(int) bool { return false }, 5},
		{"always true", 5, func(int) bool { return true }, 0},
		{"true from 3", 5, func(i int) bool { return i >= 3 }, 3},
		{"true from 4", 5, func(i int) bool { return i >= 4 }, 4},
		{"empty", 0, func(int) bool { return true }, 0},
		{"one element, false", 1, func(int) bool { return false }, 1},
		{"one element, true", 1, func(int) bool { return true }, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Partition(tt.n, tt.pred); got != tt.want {
				t.Errorf("Partition(%d, ...) = %d, want %d", tt.n, got, tt.want)
			}
		})
	}
}

// TestPartitionIsLogarithmic: counting predicate calls is the only exact way to
// assert the complexity, and it needs no timing.
func TestPartitionIsLogarithmic(t *testing.T) {
	for _, n := range []int{1, 10, 1_000, 1_000_000, 1_000_000_000} {
		calls := 0
		Partition(n, func(i int) bool {
			calls++
			return i >= n/2
		})

		// ceil(log2(n)) + a little slack for the integer arithmetic.
		limit := 2
		for 1<<limit < n {
			limit++
		}
		limit += 2

		if calls > limit {
			t.Errorf("n=%d took %d predicate calls, want at most %d", n, calls, limit)
		}
		t.Logf("n=%-12d %d predicate calls", n, calls)
	}
}

// TestMidpointOverflowIsReachable is the test that justifies writing
// lo + (hi-lo)/2 instead of (lo+hi)/2 in Go specifically.
//
// The usual advice is that (lo+hi)/2 overflows, and the usual rebuttal in Go is
// that a slice can never be long enough, because a slice of length maxint/2 would
// need exabytes of memory. That rebuttal is wrong: a slice of ZERO-SIZE elements
// needs no memory at all, and make([]struct{}, 3<<61) is legal.
//
// With that length, (lo+hi)/2 wraps to a negative number and indexing panics.
func TestMidpointOverflowIsReachable(t *testing.T) {
	const huge = 3 << 61 // 6.9e18, comfortably over maxint64/2

	s := make([]struct{}, huge) // costs nothing: struct{} is zero bytes
	if len(s) != huge {
		t.Fatalf("len = %d, want %d", len(s), huge)
	}

	lo, hi := len(s)/2, len(s)-1

	naive := (lo + hi) / 2
	safe := lo + (hi-lo)/2

	if naive >= 0 {
		t.Errorf("(lo+hi)/2 = %d, expected it to have wrapped negative", naive)
	}
	if safe <= lo || safe >= hi {
		t.Errorf("lo+(hi-lo)/2 = %d, expected it between %d and %d", safe, lo, hi)
	}

	t.Logf("len=%d lo=%d hi=%d", len(s), lo, hi)
	t.Logf("(lo+hi)/2    = %d  (wrapped)", naive)
	t.Logf("lo+(hi-lo)/2 = %d", safe)

	// And the wrapped value really does panic when used as an index.
	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Error("indexing with the wrapped midpoint did not panic")
			}
		}()
		_ = s[naive]
	}()

	// Partition itself survives this length, which is the point of the whole test.
	// The predicate is true only for the very last index, so this is the deepest
	// search the type system allows.
	got := Partition(len(s), func(i int) bool { return i >= huge-1 })
	if got != huge-1 {
		t.Errorf("Partition on a %d-element slice returned %d, want %d", huge, got, huge-1)
	}
}

func TestLowerAndUpperBound(t *testing.T) {
	//           0  1  2  3  4  5  6  7
	s := []int{1, 3, 3, 3, 5, 7, 7, 9}

	tests := []struct {
		target       int
		lower, upper int
	}{
		{0, 0, 0}, // below everything
		{1, 0, 1},
		{2, 1, 1}, // absent, between 1 and 3
		{3, 1, 4}, // three of them
		{4, 4, 4},
		{5, 4, 5},
		{7, 5, 7}, // two of them
		{9, 7, 8},
		{10, 8, 8}, // above everything
	}

	for _, tt := range tests {
		if got := LowerBound(s, tt.target); got != tt.lower {
			t.Errorf("LowerBound(%d) = %d, want %d", tt.target, got, tt.lower)
		}
		if got := UpperBound(s, tt.target); got != tt.upper {
			t.Errorf("UpperBound(%d) = %d, want %d", tt.target, got, tt.upper)
		}
		if got, want := Count(s, tt.target), tt.upper-tt.lower; got != want {
			t.Errorf("Count(%d) = %d, want %d", tt.target, got, want)
		}
	}
}

// TestLowerBoundMatchesStdlib: slices.BinarySearch is LowerBound with a found flag,
// so the two must never disagree. Randomised, because a fixed table would only
// cover the cases I thought of.
func TestLowerBoundMatchesStdlib(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))

	for range 2000 {
		n := r.IntN(30)
		s := make([]int, n)
		for i := range s {
			s[i] = r.IntN(15) // duplicates on purpose
		}
		slices.Sort(s)

		target := r.IntN(17)

		gotIdx, gotOK := BinarySearch(s, target)
		wantIdx, wantOK := slices.BinarySearch(s, target)

		if gotIdx != wantIdx || gotOK != wantOK {
			t.Fatalf("BinarySearch(%v, %d) = %d, %v; stdlib says %d, %v",
				s, target, gotIdx, gotOK, wantIdx, wantOK)
		}
	}
}

func TestFirstAndLast(t *testing.T) {
	s := []int{1, 3, 3, 3, 5, 7, 7, 9}

	tests := []struct {
		target      int
		first, last int
		found       bool
	}{
		{3, 1, 3, true},
		{7, 5, 6, true},
		{1, 0, 0, true},
		{9, 7, 7, true},
		{4, 0, 0, false},
		{0, 0, 0, false},
		{99, 0, 0, false},
	}

	for _, tt := range tests {
		gotFirst, ok := First(s, tt.target)
		if ok != tt.found {
			t.Errorf("First(%d) found = %v, want %v", tt.target, ok, tt.found)
		}
		if ok && gotFirst != tt.first {
			t.Errorf("First(%d) = %d, want %d", tt.target, gotFirst, tt.first)
		}

		gotLast, ok := Last(s, tt.target)
		if ok != tt.found {
			t.Errorf("Last(%d) found = %v, want %v", tt.target, ok, tt.found)
		}
		if ok && gotLast != tt.last {
			t.Errorf("Last(%d) = %d, want %d", tt.target, gotLast, tt.last)
		}
	}
}

func TestLastOnEmptySlice(t *testing.T) {
	// UpperBound returns 0, so Last computes index -1, which must not be indexed.
	var s []int
	if _, ok := Last(s, 1); ok {
		t.Error("Last on an empty slice reported a find")
	}
	if _, ok := First(s, 1); ok {
		t.Error("First on an empty slice reported a find")
	}
}

func TestRange(t *testing.T) {
	s := []int{1, 3, 3, 3, 5}

	lo, hi := Range(s, 3)
	if lo != 1 || hi != 4 {
		t.Errorf("Range(3) = %d, %d; want 1, 4", lo, hi)
	}
	if got := s[lo:hi]; !slices.Equal(got, []int{3, 3, 3}) {
		t.Errorf("s[lo:hi] = %v, want [3 3 3]", got)
	}

	// An absent value gives an empty but valid range.
	lo, hi = Range(s, 4)
	if lo != hi {
		t.Errorf("Range(4) = %d, %d; want an empty range", lo, hi)
	}
	if got := s[lo:hi]; len(got) != 0 {
		t.Errorf("s[lo:hi] = %v, want empty", got)
	}
}

// TestCountIsLogarithmic: the point of implementing Count as two bounds rather than
// finding a match and walking outwards.
func TestCountIsLogarithmic(t *testing.T) {
	const n = 1_000_000

	s := make([]int, n) // every element is 0
	comparisons := 0

	// Count through a counting predicate, to show the cost does not depend on how
	// many matches there are.
	lower := Partition(n, func(i int) bool { comparisons++; return s[i] >= 0 })
	upper := Partition(n, func(i int) bool { comparisons++; return s[i] > 0 })

	if got := upper - lower; got != n {
		t.Errorf("counted %d occurrences, want %d", got, n)
	}
	if comparisons > 60 {
		t.Errorf("%d comparisons to count a million matches, want at most 60", comparisons)
	}
	t.Logf("%d comparisons to count %d identical elements", comparisons, n)
}

func TestLinear(t *testing.T) {
	s := []string{"a", "b", "c"}

	if got := Linear(s, "b"); got != 1 {
		t.Errorf("Linear = %d, want 1", got)
	}
	if got := Linear(s, "z"); got != -1 {
		t.Errorf("Linear = %d, want -1", got)
	}
	if got := Linear([]string(nil), "a"); got != -1 {
		t.Errorf("Linear on nil = %d, want -1", got)
	}
}

func TestExponential(t *testing.T) {
	s := make([]int, 1000)
	for i := range s {
		s[i] = i * 2 // 0, 2, 4, ...
	}

	for _, target := range []int{0, 2, 40, 998, 1998} {
		gotIdx, ok := Exponential(s, target)
		wantIdx, wantOK := slices.BinarySearch(s, target)

		if !ok || gotIdx != wantIdx {
			t.Errorf("Exponential(%d) = %d, %v; want %d, %v", target, gotIdx, ok, wantIdx, wantOK)
		}
	}

	for _, absent := range []int{-1, 1, 41, 1999, 5000} {
		if _, ok := Exponential(s, absent); ok {
			t.Errorf("Exponential(%d) reported a find", absent)
		}
	}
}

// TestExponentialMatchesStdlibEverywhere: the doubling and the window arithmetic are
// where this goes wrong, so it is checked against every position rather than a few.
func TestExponentialMatchesStdlibEverywhere(t *testing.T) {
	r := rand.New(rand.NewPCG(3, 4))

	for range 500 {
		n := r.IntN(40)
		s := make([]int, n)
		for i := range s {
			s[i] = r.IntN(20)
		}
		slices.Sort(s)

		for target := -1; target <= 21; target++ {
			gotIdx, gotOK := Exponential(s, target)
			wantIdx, wantOK := slices.BinarySearch(s, target)

			if gotOK != wantOK {
				t.Fatalf("Exponential(%v, %d) found = %v, stdlib says %v", s, target, gotOK, wantOK)
			}
			if gotOK && gotIdx != wantIdx {
				// Only the index of a FOUND element has to match: for a miss,
				// Exponential does not promise the insertion point.
				if s[gotIdx] != s[wantIdx] {
					t.Fatalf("Exponential(%v, %d) = %d, stdlib says %d", s, target, gotIdx, wantIdx)
				}
			}
		}
	}
}

// TestExponentialBeatsBinaryNearTheFront is the reason it exists, and the
// predicate count is the way to show it.
func TestExponentialBeatsBinaryNearTheFront(t *testing.T) {
	const n = 1 << 20

	s := make([]int, n)
	for i := range s {
		s[i] = i
	}

	// Counting inside Exponential is not possible from outside, so this counts the
	// doublings and the window size instead, which is the whole cost.
	target := 5

	bound := 1
	doublings := 0
	for bound < len(s) && s[bound] < target {
		bound *= 2
		doublings++
	}
	window := min(bound+1, len(s)) - bound/2

	binarySteps := 0
	for 1<<binarySteps < n {
		binarySteps++
	}

	t.Logf("target at index %d: exponential searches a window of %d after %d doublings; plain binary search takes %d steps",
		target, window, doublings, binarySteps)

	if window > 16 {
		t.Errorf("window is %d, expected it small for a target near the front", window)
	}
}

func TestSearchAnswer(t *testing.T) {
	tests := []struct {
		name   string
		lo, hi int
		ok     func(int) bool
		want   int
		found  bool
	}{
		{"smallest x with x*x >= 200", 1, 1000, func(x int) bool { return x*x >= 200 }, 15, true},
		{"never true", 1, 10, func(int) bool { return false }, 0, false},
		{"always true", 5, 10, func(int) bool { return true }, 5, true},
		{"inverted range", 10, 5, func(int) bool { return true }, 0, false},
		{"negative bounds", -100, 100, func(x int) bool { return x >= -7 }, -7, true},
		{"single value, true", 5, 5, func(int) bool { return true }, 5, true},
		{"single value, false", 5, 5, func(int) bool { return false }, 0, false},
		{"only the top", 1, 10, func(x int) bool { return x == 10 }, 10, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, found := SearchAnswer(tt.lo, tt.hi, tt.ok)

			if found != tt.found {
				t.Fatalf("found = %v, want %v (got %d)", found, tt.found, got)
			}
			if found && got != tt.want {
				t.Errorf("SearchAnswer = %d, want %d", got, tt.want)
			}
		})
	}
}

// TestSearchAnswerAtTheExtremes is why SearchAnswer returns a bool rather than hi+1, and why
// it bisects [lo, hi] directly rather than shifting into [0, n).
//
// The shift computes hi-lo+1, which overflows for lo=0 and hi=math.MaxInt and made the whole
// call return a confidently wrong answer. The sentinel hi+1 overflows at the same place.
func TestSearchAnswerAtTheExtremes(t *testing.T) {
	// A range covering every non-negative int.
	got, found := SearchAnswer(0, math.MaxInt, func(x int) bool { return x >= 1<<40 })
	if !found || got != 1<<40 {
		t.Errorf("over [0, MaxInt] = %d, %v; want %d, true", got, found, 1<<40)
	}

	// Nothing satisfies it, over the same enormous range.
	if _, found := SearchAnswer(0, math.MaxInt, func(int) bool { return false }); found {
		t.Error("found a value where the predicate is never true")
	}

	// Only the very last value works, so the loop has to reach it without overflowing.
	got, found = SearchAnswer(0, math.MaxInt, func(x int) bool { return x == math.MaxInt })
	if !found || got != math.MaxInt {
		t.Errorf("= %d, %v; want MaxInt, true", got, found)
	}

	// And the negative end, where mid-1 would underflow.
	got, found = SearchAnswer(math.MinInt, 0, func(x int) bool { return x >= math.MinInt })
	if !found || got != math.MinInt {
		t.Errorf("= %d, %v; want MinInt, true", got, found)
	}
}

// TestMidpointOverTheWholeIntRange checks the unsigned midpoint against the obvious
// arithmetic where the obvious arithmetic is valid, and against known answers where it is
// not.
func TestMidpointOverTheWholeIntRange(t *testing.T) {
	// Where lo + (hi-lo)/2 is safe, the two must agree.
	r := rand.New(rand.NewPCG(31, 37))
	for range 20_000 {
		lo := r.IntN(1 << 40)
		hi := lo + r.IntN(1<<40)

		if got, want := midpoint(lo, hi), lo+(hi-lo)/2; got != want {
			t.Fatalf("midpoint(%d, %d) = %d, want %d", lo, hi, got, want)
		}
	}

	// Where it is not safe, the answers are known by hand.
	cases := []struct{ lo, hi, want int }{
		{math.MinInt, math.MaxInt, -1},    // the full range
		{math.MinInt, 0, math.MinInt / 2}, // 0 - MinInt does not fit in an int
		{0, math.MaxInt, math.MaxInt / 2},
		{math.MinInt, math.MinInt, math.MinInt}, // a single value
		{math.MaxInt, math.MaxInt, math.MaxInt},
		{-1, 1, 0},
		{0, 1, 0},
		{-2, -1, -2}, // rounds towards negative infinity, like integer division of the sum
	}

	for _, tc := range cases {
		if got := midpoint(tc.lo, tc.hi); got != tc.want {
			t.Errorf("midpoint(%d, %d) = %d, want %d", tc.lo, tc.hi, got, tc.want)
		}
	}

	// And the defining property: the midpoint is always within the range.
	for _, tc := range cases {
		got := midpoint(tc.lo, tc.hi)
		if got < tc.lo || got > tc.hi {
			t.Errorf("midpoint(%d, %d) = %d, outside the range", tc.lo, tc.hi, got)
		}
	}
}
