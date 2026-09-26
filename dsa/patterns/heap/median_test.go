package heap

import (
	"math/rand/v2"
	"slices"
	"testing"
)

func TestMedianStream(t *testing.T) {
	m := NewMedianStream[int]()

	if _, ok := m.Median(); ok {
		t.Error("an empty stream reported a median")
	}

	steps := []struct {
		add       int
		median    int
		low, high int
	}{
		{5, 5, 5, 5},
		{15, 5, 5, 15}, // even count: the two middles
		{1, 5, 5, 5},   // 1 5 15
		{3, 3, 3, 5},   // 1 3 5 15
		{8, 5, 5, 5},   // 1 3 5 8 15
		{7, 5, 5, 7},   // 1 3 5 7 8 15
		{9, 7, 7, 7},   // 1 3 5 7 8 9 15
	}

	for i, step := range steps {
		m.Add(step.add)

		got, ok := m.Median()
		if !ok {
			t.Fatalf("step %d: no median after adding %d", i, step.add)
		}
		if got != step.median {
			t.Errorf("step %d: after adding %d, Median() = %d, want %d", i, step.add, got, step.median)
		}

		low, high, _ := m.MedianPair()
		if low != step.low || high != step.high {
			t.Errorf("step %d: MedianPair() = %d, %d; want %d, %d", i, low, high, step.low, step.high)
		}
		if m.Len() != i+1 {
			t.Errorf("step %d: Len() = %d, want %d", i, m.Len(), i+1)
		}
	}
}

// TestMedianStreamMatchesSorting is the real test. A running median has one obvious
// correct implementation, and the heap version must agree with it at every step.
func TestMedianStreamMatchesSorting(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))

	for range 200 {
		m := NewMedianStream[int]()
		var seen []int

		for range 1 + r.IntN(80) {
			v := r.IntN(100)

			m.Add(v)
			seen = append(seen, v)

			sorted := slices.Clone(seen)
			slices.Sort(sorted)

			// The convention: for an even count, Median returns the LOWER middle.
			wantLow := sorted[(len(sorted)-1)/2]
			wantHigh := sorted[len(sorted)/2]

			got, ok := m.Median()
			if !ok || got != wantLow {
				t.Fatalf("after %v, Median() = %d, %v; want %d", seen, got, ok, wantLow)
			}

			low, high, _ := m.MedianPair()
			if low != wantLow || high != wantHigh {
				t.Fatalf("after %v, MedianPair() = %d, %d; want %d, %d", seen, low, high, wantLow, wantHigh)
			}
		}
	}
}

// TestMedianStreamStaysBalanced: the two halves must never differ in size by more than
// one, or the tops stop being the middle values. Checking the invariant directly
// catches a rebalance bug that the value assertions might miss on some inputs.
func TestMedianStreamStaysBalanced(t *testing.T) {
	r := rand.New(rand.NewPCG(3, 4))
	m := NewMedianStream[int]()

	for step := range 5000 {
		m.Add(r.IntN(1000))

		lower, upper := m.lower.Len(), m.upper.Len()

		if lower < upper {
			t.Fatalf("step %d: lower has %d and upper has %d; lower must never be smaller", step, lower, upper)
		}
		if lower > upper+1 {
			t.Fatalf("step %d: lower has %d and upper has %d; the gap must be at most one", step, lower, upper)
		}

		// And every value in lower must be at or below every value in upper.
		if lower > 0 && upper > 0 {
			maxLower, _ := m.lower.Peek()
			minUpper, _ := m.upper.Peek()
			if maxLower > minUpper {
				t.Fatalf("step %d: lower's top %d exceeds upper's top %d", step, maxLower, minUpper)
			}
		}
	}
}

func TestMedianStreamSortedInput(t *testing.T) {
	// Ascending and descending input both push every value onto the same side before
	// rebalancing, which is where a one-sided rebalance shows up.
	for _, name := range []string{"ascending", "descending"} {
		t.Run(name, func(t *testing.T) {
			m := NewMedianStream[int]()

			var seen []int
			for i := range 200 {
				v := i
				if name == "descending" {
					v = 200 - i
				}

				m.Add(v)
				seen = append(seen, v)

				sorted := slices.Clone(seen)
				slices.Sort(sorted)
				want := sorted[(len(sorted)-1)/2]

				if got, _ := m.Median(); got != want {
					t.Fatalf("after %d values, Median() = %d, want %d", len(seen), got, want)
				}
			}
		})
	}
}

func TestMedianFloat(t *testing.T) {
	m := NewMedianStream[int]()

	if _, ok := MedianFloat(m); ok {
		t.Error("an empty stream reported a median")
	}

	m.Add(1)
	if got, _ := MedianFloat(m); got != 1 {
		t.Errorf("MedianFloat = %v, want 1", got)
	}

	m.Add(2)
	if got, _ := MedianFloat(m); got != 1.5 {
		t.Errorf("MedianFloat = %v, want 1.5", got)
	}

	m.Add(3)
	if got, _ := MedianFloat(m); got != 2 {
		t.Errorf("MedianFloat = %v, want 2", got)
	}
}

func TestMedianStreamWithStrings(t *testing.T) {
	// cmp.Ordered, so strings work, and Median returning the lower middle rather than
	// a mean is what makes that possible.
	m := NewMedianStream[string]()
	for _, s := range []string{"delta", "alpha", "charlie", "bravo"} {
		m.Add(s)
	}

	low, high, ok := m.MedianPair()
	if !ok {
		t.Fatal("no median")
	}
	if low != "bravo" || high != "charlie" {
		t.Errorf("MedianPair() = %q, %q; want \"bravo\", \"charlie\"", low, high)
	}
}
