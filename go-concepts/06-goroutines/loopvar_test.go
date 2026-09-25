package main

import (
	"slices"
	"testing"
)

// TestLoopVariableIsPerIteration is the Go 1.22 semantics, asserted. Before
// 1.22 this test would have failed, usually with every goroutine reporting the
// final value.
func TestLoopVariableIsPerIteration(t *testing.T) {
	const n = 100
	want := make([]int, n)
	for i := range want {
		want[i] = i
	}

	// Run it repeatedly: a scheduling-dependent bug that shows up one time in
	// twenty is still a bug, and a single run could miss it.
	for run := 0; run < 20; run++ {
		if got := eachIterationHasItsOwnVariable(n); !slices.Equal(got, want) {
			t.Fatalf("run %d: got %v, want %v", run, got, want)
		}
	}
}

// TestOldWorkaroundIsEquivalent: `i := i` still works, it just adds nothing.
func TestOldWorkaroundIsEquivalent(t *testing.T) {
	const n = 50

	modern := eachIterationHasItsOwnVariable(n)
	legacy := theOldWorkaroundIsNowRedundant(n)

	if !slices.Equal(modern, legacy) {
		t.Errorf("the two forms differ:\n  modern %v\n  legacy %v", modern, legacy)
	}
}

// TestSimulateOldBehaviour pins what the pre-1.22 bug produced: every goroutine
// reading one shared variable sees the last value written to it.
func TestSimulateOldBehaviour(t *testing.T) {
	const n = 5

	got := simulateOldBehaviour(n)

	if len(got) != n {
		t.Fatalf("got %d values, want %d", len(got), n)
	}
	// All n goroutines read the same final value, n-1.
	for i, v := range got {
		if v != n-1 {
			t.Errorf("value %d = %d, want %d — every goroutine should read the last write", i, v, n-1)
		}
	}
}

func TestRangeOverSliceToo(t *testing.T) {
	got := rangeOverSliceToo([]string{"go", "rust", "zig"})

	if want := []string{"go", "rust", "zig"}; !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
