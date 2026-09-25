package main

import (
	"slices"
	"testing"
	"time"
)

// TestBusyClosedChannelLoop is the most valuable test in this lesson. It
// asserts the BUG still happens, by iteration count.
//
// The setup matters: one channel closed immediately, another trickling values.
// If both closed promptly the broken loop would terminate anyway and the test
// would pass while proving nothing.
func TestBusyClosedChannelLoop(t *testing.T) {
	if testing.Short() {
		t.Skip("timing-dependent spin measurement")
	}

	const (
		values = 3
		gap    = 10 * time.Millisecond
	)

	withTimeout(t, 10*time.Second, func() {
		broken, fixed, expected := spinDetector(values, gap)

		t.Logf("broken %d iterations, fixed %d, expected %d", broken, fixed, expected)

		if fixed != expected {
			t.Errorf("the fixed version ran %d iterations, want exactly %d", fixed, expected)
		}

		// The broken version should spin orders of magnitude more. 100x is a
		// floor with enormous slack: in practice it is tens of thousands.
		if broken < fixed*100 {
			t.Errorf("broken ran %d iterations vs %d fixed — the busy loop should spin far more",
				broken, fixed)
		}
	})
}

func TestMergeNilFixProducesEveryValue(t *testing.T) {
	tests := []struct {
		name    string
		aValues []int
		bValues []int
	}{
		{"both have values", []int{1, 2, 3}, []int{10, 11, 12}},
		{"one empty", nil, []int{1, 2}},
		{"both empty", nil, nil},
		{"uneven", []int{1}, []int{2, 3, 4, 5}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withTimeout(t, 2*time.Second, func() {
				a := make(chan int, len(tt.aValues)+1)
				b := make(chan int, len(tt.bValues)+1)
				for _, v := range tt.aValues {
					a <- v
				}
				for _, v := range tt.bValues {
					b <- v
				}
				close(a)
				close(b)

				got, iterations := mergeNilFix(a, b)

				want := slices.Concat(tt.aValues, tt.bValues)
				slices.Sort(want)
				slices.Sort(got)

				if !slices.Equal(got, want) {
					t.Errorf("got %v, want %v", got, want)
				}

				// One iteration per value plus one per close. No spinning.
				wantIterations := len(want) + 2
				if iterations != wantIterations {
					t.Errorf("ran %d iterations, want %d", iterations, wantIterations)
				}
			})
		})
	}
}

// TestMergeNilFixNeverAppendsZeroValues: the broken version appends a zero
// every time it hits a closed channel. The fixed one must not.
func TestMergeNilFixNeverAppendsZeroValues(t *testing.T) {
	withTimeout(t, 2*time.Second, func() {
		a := make(chan int, 2)
		b := make(chan int, 2)
		a <- 1
		a <- 2
		b <- 3
		close(a)
		close(b)

		got, _ := mergeNilFix(a, b)

		if slices.Contains(got, 0) {
			t.Errorf("output %v contains a zero value — a closed channel's zero leaked through", got)
		}
	})
}

func TestToggleACaseOnAndOff(t *testing.T) {
	tests := []struct {
		name string
		in   []int
		want []int
	}{
		{"several values", []int{1, 2, 3, 4}, []int{2, 4, 6, 8}},
		{"single value", []int{5}, []int{10}},
		{"empty", nil, nil},
		{"zero is not skipped", []int{0, 1}, []int{0, 2}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withTimeout(t, 2*time.Second, func() {
				got := toggleACaseOnAndOff(tt.in)
				if !slices.Equal(got, tt.want) {
					t.Errorf("got %v, want %v", got, tt.want)
				}
			})
		})
	}
}

func TestCPUBurnComparison(t *testing.T) {
	if testing.Short() {
		t.Skip("timing measurement")
	}

	withTimeout(t, 10*time.Second, func() {
		brokenElapsed, fixedElapsed, spins := cpuBurnComparison(3, 10*time.Millisecond)

		t.Logf("broken %v (%d iterations), fixed %v", brokenElapsed, spins, fixedElapsed)

		// Both finish in about the same wall time, bounded by the slow
		// producer. The difference is entirely in CPU spent waiting, which is
		// what the iteration count captures.
		if spins < 1000 {
			t.Errorf("broken version spun only %d times — expected it to saturate a core", spins)
		}
	})
}
