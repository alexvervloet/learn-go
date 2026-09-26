package searching

import (
	"math/rand/v2"
	"slices"
	"testing"
)

// rotate returns s rotated left by k.
func rotate[T any](s []T, k int) []T {
	if len(s) == 0 {
		return nil
	}
	k %= len(s)
	out := slices.Clone(s)
	return append(out[k:], out[:k]...)
}

func TestSearchRotated(t *testing.T) {
	base := []int{1, 2, 3, 4, 5, 6, 7}

	// Every rotation, every target, plus targets that are absent.
	for k := range len(base) {
		s := rotate(base, k)

		for _, target := range base {
			got := SearchRotated(s, target)
			if got < 0 || s[got] != target {
				t.Errorf("rotation %d %v: SearchRotated(%d) = %d", k, s, target, got)
			}
		}

		for _, absent := range []int{0, 8, 99} {
			if got := SearchRotated(s, absent); got != -1 {
				t.Errorf("rotation %d: SearchRotated(%d) = %d, want -1", k, absent, got)
			}
		}
	}
}

func TestSearchRotatedEdgeCases(t *testing.T) {
	if got := SearchRotated([]int(nil), 1); got != -1 {
		t.Errorf("empty slice = %d, want -1", got)
	}
	if got := SearchRotated([]int{5}, 5); got != 0 {
		t.Errorf("one element, present = %d, want 0", got)
	}
	if got := SearchRotated([]int{5}, 1); got != -1 {
		t.Errorf("one element, absent = %d, want -1", got)
	}
}

// TestSearchRotatedWithDuplicates is the case that makes the algorithm O(n). It
// still has to be correct.
func TestSearchRotatedWithDuplicates(t *testing.T) {
	cases := []struct {
		s      []int
		target int
		found  bool
	}{
		{[]int{2, 2, 2, 2, 2, 0, 2}, 0, true}, // the classic killer
		{[]int{2, 2, 2, 2, 2, 0, 2}, 2, true},
		{[]int{2, 2, 2, 2, 2, 0, 2}, 3, false},
		{[]int{1, 1, 1, 1}, 1, true},
		{[]int{1, 1, 1, 1}, 2, false},
		{[]int{3, 1, 2, 3, 3, 3, 3}, 1, true},
		{[]int{3, 1, 2, 3, 3, 3, 3}, 2, true},
	}

	for _, tc := range cases {
		got := SearchRotated(tc.s, tc.target)

		if tc.found {
			if got < 0 || tc.s[got] != tc.target {
				t.Errorf("SearchRotated(%v, %d) = %d, want an index holding %d",
					tc.s, tc.target, got, tc.target)
			}
			continue
		}
		if got != -1 {
			t.Errorf("SearchRotated(%v, %d) = %d, want -1", tc.s, tc.target, got)
		}
	}
}

func TestSearchRotatedRandomised(t *testing.T) {
	r := rand.New(rand.NewPCG(5, 7))

	for range 3000 {
		n := 1 + r.IntN(40)

		base := make([]int, n)
		for i := range base {
			base[i] = r.IntN(n * 2)
		}
		slices.Sort(base)

		s := rotate(base, r.IntN(n))
		target := r.IntN(n * 2)

		got := SearchRotated(s, target)
		want := slices.Contains(s, target)

		if want && (got < 0 || s[got] != target) {
			t.Fatalf("SearchRotated(%v, %d) = %d, but the target is present", s, target, got)
		}
		if !want && got != -1 {
			t.Fatalf("SearchRotated(%v, %d) = %d, but the target is absent", s, target, got)
		}
	}
}

func TestRotationPoint(t *testing.T) {
	base := []int{1, 2, 3, 4, 5, 6, 7}

	for k := range len(base) {
		s := rotate(base, k)

		// rotate() rotates LEFT by k, so the minimum lands at len-k, not at k.
		want := (len(base) - k) % len(base)

		got := RotationPoint(s)
		if got != want {
			t.Errorf("RotationPoint(%v) = %d, want %d", s, got, want)
		}
		if s[got] != 1 {
			t.Errorf("RotationPoint(%v) = %d, which holds %d rather than the minimum",
				s, got, s[got])
		}
	}
}

// TestRotationPointOnUnrotatedInput is the case that catches comparing against the
// first element instead of the last. That version returns a valid-looking answer for
// every rotated input and gets this one wrong.
func TestRotationPointOnUnrotatedInput(t *testing.T) {
	s := []int{1, 2, 3, 4, 5}

	if got := RotationPoint(s); got != 0 {
		t.Errorf("RotationPoint on unrotated input = %d, want 0", got)
	}
	if got := RotationPoint([]int(nil)); got != 0 {
		t.Errorf("RotationPoint(nil) = %d, want 0", got)
	}
	if got := RotationPoint([]int{7}); got != 0 {
		t.Errorf("RotationPoint of one element = %d, want 0", got)
	}
}

func TestRotationPointRandomised(t *testing.T) {
	r := rand.New(rand.NewPCG(11, 13))

	for range 2000 {
		n := 1 + r.IntN(30)

		base := make([]int, n)
		for i := range base {
			base[i] = i // distinct, since duplicates make the rotation ambiguous
		}

		k := r.IntN(n)
		s := rotate(base, k)

		want := (n - k) % n
		if got := RotationPoint(s); got != want {
			t.Fatalf("RotationPoint(%v) = %d, want %d", s, got, want)
		}
	}
}

func TestFindPeak(t *testing.T) {
	tests := []struct {
		name string
		s    []int
	}{
		{"single", []int{1}},
		{"ascending", []int{1, 2, 3, 4, 5}},
		{"descending", []int{5, 4, 3, 2, 1}},
		{"peak in the middle", []int{1, 3, 5, 4, 2}},
		{"two peaks", []int{1, 5, 2, 6, 3}},
		{"plateau ends", []int{1, 2, 3, 1}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			i := FindPeak(tt.s)
			if i < 0 || i >= len(tt.s) {
				t.Fatalf("FindPeak(%v) = %d, out of range", tt.s, i)
			}

			// A peak beats both neighbours, with out-of-range treated as smaller.
			if i > 0 && tt.s[i] <= tt.s[i-1] {
				t.Errorf("FindPeak(%v) = %d, but s[%d]=%d is not greater than s[%d]=%d",
					tt.s, i, i, tt.s[i], i-1, tt.s[i-1])
			}
			if i < len(tt.s)-1 && tt.s[i] <= tt.s[i+1] {
				t.Errorf("FindPeak(%v) = %d, but s[%d]=%d is not greater than s[%d]=%d",
					tt.s, i, i, tt.s[i], i+1, tt.s[i+1])
			}
		})
	}

	if got := FindPeak[int](nil); got != -1 {
		t.Errorf("FindPeak(nil) = %d, want -1", got)
	}
}

func TestFindPeakRandomised(t *testing.T) {
	r := rand.New(rand.NewPCG(17, 19))

	for range 3000 {
		n := 1 + r.IntN(50)

		// Distinct values, because a plateau has no peak by this definition.
		perm := r.Perm(n)

		i := FindPeak(perm)
		if i < 0 || i >= n {
			t.Fatalf("FindPeak(%v) = %d, out of range", perm, i)
		}
		if i > 0 && perm[i] <= perm[i-1] {
			t.Fatalf("FindPeak(%v) = %d, not a peak on the left", perm, i)
		}
		if i < n-1 && perm[i] <= perm[i+1] {
			t.Fatalf("FindPeak(%v) = %d, not a peak on the right", perm, i)
		}
	}
}

// TestFindPeakIsLogarithmic: the whole surprise of the function is that an
// unsorted array can be bisected, so the cost is worth asserting.
func TestFindPeakIsLogarithmic(t *testing.T) {
	const n = 1 << 20

	// A single peak at the far end, so a linear scan would take n steps.
	s := make([]int, n)
	for i := range s {
		s[i] = i
	}

	calls := 0
	i := Partition(len(s)-1, func(i int) bool {
		calls++
		return s[i] > s[i+1]
	})

	if i != n-1 {
		t.Errorf("peak at %d, want %d", i, n-1)
	}
	if calls > 25 {
		t.Errorf("%d comparisons for n=%d, want at most 25", calls, n)
	}
	t.Logf("%d comparisons to find a peak in %d elements", calls, n)
}
