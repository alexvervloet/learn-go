package monotonicstack

import (
	"math/rand/v2"
	"slices"
	"strconv"
	"testing"
)

// The obvious O(n²) versions, which are the oracles for everything below.
func bruteNext[T interface{ ~int }](s []T, beats func(candidate, base T) bool) []int {
	out := make([]int, len(s))
	for i := range s {
		out[i] = Absent
		for j := i + 1; j < len(s); j++ {
			if beats(s[j], s[i]) {
				out[i] = j
				break
			}
		}
	}
	return out
}

func brutePrevious[T interface{ ~int }](s []T, beats func(candidate, base T) bool) []int {
	out := make([]int, len(s))
	for i := range s {
		out[i] = Absent
		for j := i - 1; j >= 0; j-- {
			if beats(s[j], s[i]) {
				out[i] = j
				break
			}
		}
	}
	return out
}

func TestNextAndPrevious(t *testing.T) {
	s := []int{2, 1, 2, 4, 3}

	tests := []struct {
		name string
		got  []int
		want []int
	}{
		{"NextGreater", NextGreater(s), []int{3, 2, 3, Absent, Absent}},
		{"NextGreaterOrEqual", NextGreaterOrEqual(s), []int{2, 2, 3, Absent, Absent}},
		{"NextSmaller", NextSmaller(s), []int{1, Absent, Absent, 4, Absent}},
		{"PreviousGreater", PreviousGreater(s), []int{Absent, 0, Absent, Absent, 3}},
		{"PreviousSmaller", PreviousSmaller(s), []int{Absent, Absent, 1, 2, 2}},
		{"PreviousSmallerOrEqual", PreviousSmallerOrEqual(s), []int{Absent, Absent, 1, 2, 2}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !slices.Equal(tt.got, tt.want) {
				t.Errorf("%s(%v) = %v, want %v", tt.name, s, tt.got, tt.want)
			}
		})
	}
}

// TestAllVariantsMatchBruteForce is the real test. Six near-identical functions generated
// from one implementation are exactly the kind of thing where a comparison gets flipped and
// nobody notices.
func TestAllVariantsMatchBruteForce(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))

	variants := []struct {
		name string
		fast func([]int) []int
		slow func([]int) []int
	}{
		{
			"NextGreater", NextGreater[int],
			func(s []int) []int {
				return bruteNext(s, func(c, b int) bool { return c > b })
			},
		},
		{
			"NextGreaterOrEqual", NextGreaterOrEqual[int],
			func(s []int) []int {
				return bruteNext(s, func(c, b int) bool { return c >= b })
			},
		},
		{
			"NextSmaller", NextSmaller[int],
			func(s []int) []int {
				return bruteNext(s, func(c, b int) bool { return c < b })
			},
		},
		{
			"PreviousGreater", PreviousGreater[int],
			func(s []int) []int {
				return brutePrevious(s, func(c, b int) bool { return c > b })
			},
		},
		{
			"PreviousSmaller", PreviousSmaller[int],
			func(s []int) []int {
				return brutePrevious(s, func(c, b int) bool { return c < b })
			},
		},
		{
			"PreviousSmallerOrEqual", PreviousSmallerOrEqual[int],
			func(s []int) []int {
				return brutePrevious(s, func(c, b int) bool { return c <= b })
			},
		},
	}

	for _, v := range variants {
		t.Run(v.name, func(t *testing.T) {
			for range 3000 {
				n := r.IntN(20)
				s := make([]int, n)
				for i := range s {
					s[i] = r.IntN(6) // a small range, so ties are constant
				}

				got, want := v.fast(s), v.slow(s)
				if !slices.Equal(got, want) {
					t.Fatalf("%s(%v) = %v, want %v", v.name, s, got, want)
				}
			}
		})
	}
}

func TestNextAndPreviousDegenerate(t *testing.T) {
	for _, s := range [][]int{nil, {}, {5}} {
		for name, f := range map[string]func([]int) []int{
			"NextGreater":     NextGreater[int],
			"NextSmaller":     NextSmaller[int],
			"PreviousGreater": PreviousGreater[int],
			"PreviousSmaller": PreviousSmaller[int],
		} {
			got := f(s)
			if len(got) != len(s) {
				t.Errorf("%s(%v) returned %d entries, want %d", name, s, len(got), len(s))
			}
			for _, v := range got {
				if v != Absent {
					t.Errorf("%s(%v) = %v, want all Absent", name, s, got)
				}
			}
		}
	}

	// All equal is where the strict and OrEqual variants come apart, and on the sample
	// above they happen to agree, so this is the case that distinguishes them.
	equal := []int{7, 7, 7}

	if got := NextGreater(equal); !slices.Equal(got, []int{Absent, Absent, Absent}) {
		t.Errorf("NextGreater(%v) = %v, want all Absent", equal, got)
	}
	if got := NextGreaterOrEqual(equal); !slices.Equal(got, []int{1, 2, Absent}) {
		t.Errorf("NextGreaterOrEqual(%v) = %v, want [1 2 -1]", equal, got)
	}
	if got := PreviousSmaller(equal); !slices.Equal(got, []int{Absent, Absent, Absent}) {
		t.Errorf("PreviousSmaller(%v) = %v, want all Absent", equal, got)
	}
	if got := PreviousSmallerOrEqual(equal); !slices.Equal(got, []int{Absent, 0, 1}) {
		t.Errorf("PreviousSmallerOrEqual(%v) = %v, want [-1 0 1]", equal, got)
	}
}

// TestEachIndexIsPushedAndPoppedOnce is the amortised argument, measured. The inner pop loop
// looks quadratic and is not.
func TestEachIndexIsPushedAndPoppedOnce(t *testing.T) {
	r := rand.New(rand.NewPCG(3, 4))

	const n = 200_000

	// Descending input is the worst case for the push side and the best for popping;
	// ascending is the reverse. Random is neither.
	for _, shape := range []string{"random", "ascending", "descending"} {
		s := make([]int, n)
		for i := range s {
			switch shape {
			case "ascending":
				s[i] = i
			case "descending":
				s[i] = n - i
			default:
				s[i] = r.IntN(n)
			}
		}

		// An instrumented copy of scan, counting pushes and pops.
		pushes, pops := 0, 0
		stack := make([]int, 0, n)
		out := make([]int, n)
		for i := range out {
			out[i] = Absent
		}

		for at := range s {
			for len(stack) > 0 && s[at] > s[stack[len(stack)-1]] {
				out[stack[len(stack)-1]] = at
				stack = stack[:len(stack)-1]
				pops++
			}
			stack = append(stack, at)
			pushes++
		}

		if pushes != n {
			t.Errorf("%s: %d pushes for %d elements", shape, pushes, n)
		}
		if pops > n {
			t.Errorf("%s: %d pops for %d elements, want at most %d", shape, pops, n, n)
		}

		t.Logf("%-11s %d pushes, %d pops over %d elements", shape, pushes, pops, n)

		if !slices.Equal(out, NextGreater(s)) {
			t.Errorf("%s: the instrumented copy disagrees with NextGreater", shape)
		}
	}
}

func TestDaysUntilWarmer(t *testing.T) {
	tests := []struct {
		in   []int
		want []int
	}{
		{[]int{73, 74, 75, 71, 69, 72, 76, 73}, []int{1, 1, 4, 2, 1, 1, 0, 0}},
		{[]int{30, 40, 50, 60}, []int{1, 1, 1, 0}},
		{[]int{30, 60, 90}, []int{1, 1, 0}},
		{[]int{90, 60, 30}, []int{0, 0, 0}},
		{[]int{50, 50, 50}, []int{0, 0, 0}}, // equal is not warmer
		{[]int{5}, []int{0}},
		{nil, nil},
	}

	for _, tt := range tests {
		got := DaysUntilWarmer(tt.in)
		if len(got) == 0 {
			got = nil
		}
		if !slices.Equal(got, tt.want) {
			t.Errorf("DaysUntilWarmer(%v) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestStockSpan(t *testing.T) {
	tests := []struct {
		in   []int
		want []int
	}{
		{[]int{100, 80, 60, 70, 60, 75, 85}, []int{1, 1, 1, 2, 1, 4, 6}},
		{[]int{10, 20, 30}, []int{1, 2, 3}},
		{[]int{30, 20, 10}, []int{1, 1, 1}},
		{[]int{5, 5, 5}, []int{1, 2, 3}}, // equal counts towards the span
		{[]int{7}, []int{1}},
		{nil, nil},
	}

	for _, tt := range tests {
		got := StockSpan(tt.in)
		if len(got) == 0 {
			got = nil
		}
		if !slices.Equal(got, tt.want) {
			t.Errorf("StockSpan(%v) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

// TestSpannerMatchesBatch: the online version keeps its stack between calls, and it has to
// give the same answers as the batch version that can see everything at once.
func TestSpannerMatchesBatch(t *testing.T) {
	r := rand.New(rand.NewPCG(5, 7))

	for range 3000 {
		n := r.IntN(25)
		prices := make([]int, n)
		for i := range prices {
			prices[i] = r.IntN(20)
		}

		want := StockSpan(prices)

		var s Spanner
		for i, p := range prices {
			got := s.Next(p)
			if got != want[i] {
				t.Fatalf("prices=%v: Spanner gave %d at index %d, batch says %d",
					prices, got, i, want[i])
			}
			if s.Len() != i+1 {
				t.Fatalf("Len() = %d after %d prices", s.Len(), i+1)
			}
		}
	}
}

func TestLargestRectangle(t *testing.T) {
	tests := []struct {
		name string
		in   []int
		want int
	}{
		{"classic", []int{2, 1, 5, 6, 2, 3}, 10}, // 5 and 6, width 2
		{"all equal", []int{3, 3, 3, 3}, 12},
		{"ascending", []int{1, 2, 3, 4, 5}, 9}, // 3,4,5 at height 3
		{"descending", []int{5, 4, 3, 2, 1}, 9},
		{"single", []int{7}, 7},
		{"two", []int{2, 4}, 4},
		{"a valley", []int{6, 2, 5, 4, 5, 1, 6}, 12},
		{"zeros", []int{0, 0, 0}, 0},
		{"empty", nil, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			area, _, _ := LargestRectangle(tt.in)
			if area != tt.want {
				t.Errorf("LargestRectangle(%v) = %d, want %d", tt.in, area, tt.want)
			}
		})
	}
}

// TestLargestRectangleMatchesBruteForce: the tie-breaking with equal heights is the subtle
// part, so the test uses a tiny value range where ties are everywhere.
func TestLargestRectangleMatchesBruteForce(t *testing.T) {
	r := rand.New(rand.NewPCG(9, 11))

	for range 5000 {
		n := r.IntN(15)
		heights := make([]int, n)
		for i := range heights {
			heights[i] = r.IntN(5)
		}

		// Every pair of bounds, limited by the shortest bar between them.
		want := 0
		for i := range heights {
			shortest := heights[i]
			for j := i; j < n; j++ {
				shortest = min(shortest, heights[j])
				want = max(want, shortest*(j-i+1))
			}
		}

		area, at, width := LargestRectangle(heights)
		if area != want {
			t.Fatalf("LargestRectangle(%v) = %d, want %d", heights, area, want)
		}

		// The reported bar and width have to describe a real rectangle of that area.
		if area > 0 {
			if at < 0 || at >= n {
				t.Fatalf("LargestRectangle(%v) reported bar %d", heights, at)
			}
			if heights[at]*width != area {
				t.Fatalf("LargestRectangle(%v) = %d at bar %d width %d, which is %d",
					heights, area, at, width, heights[at]*width)
			}
		}
	}
}

func TestTrapWater(t *testing.T) {
	tests := []struct {
		in   []int
		want int
	}{
		{[]int{0, 1, 0, 2, 1, 0, 1, 3, 2, 1, 2, 1}, 6},
		{[]int{4, 2, 0, 3, 2, 5}, 9},
		{[]int{3, 0, 3}, 3},
		{[]int{1, 2, 3}, 0}, // ascending traps nothing
		{[]int{3, 2, 1}, 0},
		{[]int{5, 5}, 0},
		{[]int{5}, 0},
		{nil, 0},
		{[]int{2, 0, 2}, 2},
	}

	for _, tt := range tests {
		if got := TrapWater(tt.in); got != tt.want {
			t.Errorf("TrapWater(%v) = %d, want %d", tt.in, got, tt.want)
		}
		if got := TrapWaterTwoPointers(tt.in); got != tt.want {
			t.Errorf("TrapWaterTwoPointers(%v) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

// TestTrapWaterImplementationsAgree: the stack version and the two-pointer version share no
// code and must never disagree, and both are checked against the definition.
func TestTrapWaterImplementationsAgree(t *testing.T) {
	r := rand.New(rand.NewPCG(13, 17))

	for range 5000 {
		n := r.IntN(20)
		heights := make([]int, n)
		for i := range heights {
			heights[i] = r.IntN(8)
		}

		// The definition: water above bar i is min(tallest left, tallest right) - height.
		want := 0
		for i := range heights {
			leftMax, rightMax := 0, 0
			for j := 0; j <= i; j++ {
				leftMax = max(leftMax, heights[j])
			}
			for j := i; j < n; j++ {
				rightMax = max(rightMax, heights[j])
			}
			want += min(leftMax, rightMax) - heights[i]
		}

		stack := TrapWater(heights)
		pointers := TrapWaterTwoPointers(heights)

		if stack != want {
			t.Fatalf("TrapWater(%v) = %d, want %d", heights, stack, want)
		}
		if pointers != want {
			t.Fatalf("TrapWaterTwoPointers(%v) = %d, want %d", heights, pointers, want)
		}
	}
}

func TestRemoveDigits(t *testing.T) {
	tests := []struct {
		digits string
		k      int
		want   string
	}{
		{"1432219", 3, "1219"},
		{"10200", 1, "200"}, // leading zero stripped
		{"10", 2, "0"},      // everything removed
		{"10", 1, "0"},
		{"112", 1, "11"},   // nothing to improve, so the end comes off
		{"1234", 1, "123"}, // increasing, so the last digit goes
		{"9876", 1, "876"},
		{"112", 0, "112"},
		{"0", 0, "0"},
		{"00", 0, "0"},
		{"100", 1, "0"},
		{"12345", 5, "0"},
		{"12345", 9, "0"}, // k larger than the input
	}

	for _, tt := range tests {
		if got := RemoveDigits(tt.digits, tt.k); got != tt.want {
			t.Errorf("RemoveDigits(%q, %d) = %q, want %q", tt.digits, tt.k, got, tt.want)
		}
	}
}

// TestRemoveDigitsMatchesBruteForce enumerates every way to remove k digits and takes the
// numerically smallest result, which is the definition of the answer.
func TestRemoveDigitsMatchesBruteForce(t *testing.T) {
	r := rand.New(rand.NewPCG(19, 23))

	for range 3000 {
		n := 1 + r.IntN(9)

		b := make([]byte, n)
		for i := range b {
			b[i] = byte('0' + r.IntN(4)) // a small alphabet, so ties and zeros abound
		}
		digits := string(b)

		k := r.IntN(n + 2)

		// Every subset of positions to keep, of the right size.
		best := ""
		keep := n - k
		if keep <= 0 {
			if got := RemoveDigits(digits, k); got != "0" {
				t.Fatalf("RemoveDigits(%q, %d) = %q, want \"0\"", digits, k, got)
			}
			continue
		}

		for mask := 0; mask < 1<<n; mask++ {
			var kept []byte
			for i := range n {
				if mask&(1<<i) != 0 {
					kept = append(kept, digits[i])
				}
			}
			if len(kept) != keep {
				continue
			}

			value, err := strconv.Atoi(string(kept))
			if err != nil {
				t.Fatalf("could not parse %q", string(kept))
			}

			if best == "" {
				best = strconv.Itoa(value)
				continue
			}
			bestValue, _ := strconv.Atoi(best)
			if value < bestValue {
				best = strconv.Itoa(value)
			}
		}

		if got := RemoveDigits(digits, k); got != best {
			t.Fatalf("RemoveDigits(%q, %d) = %q, brute force says %q", digits, k, got, best)
		}
	}
}
