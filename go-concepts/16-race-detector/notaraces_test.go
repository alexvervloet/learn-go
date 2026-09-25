package main

import (
	"slices"
	"testing"
)

// Every test here runs under BOTH builds, and must be clean under -race. That
// is the point: these look racy and are not, and the detector agrees.

func TestDistinctIndexesAreSafe(t *testing.T) {
	const n = 1000

	got := distinctIndexesAreSafe(n)

	if len(got) != n {
		t.Fatalf("got %d elements, want %d", len(got), n)
	}
	for i, v := range got {
		if v != i {
			t.Fatalf("index %d = %d, want %d", i, v, i)
		}
	}
}

func TestReadOnlySharingIsSafe(t *testing.T) {
	data := []int{1, 2, 3, 4, 5}

	tests := []struct {
		workers int
		want    int
	}{
		{1, 15},
		{8, 120},
		{100, 1500},
	}

	for _, tt := range tests {
		if got := readOnlySharingIsSafe(data, tt.workers); got != tt.want {
			t.Errorf("%d workers: got %d, want %d", tt.workers, got, tt.want)
		}
	}

	// The input must be untouched.
	if !slices.Equal(data, []int{1, 2, 3, 4, 5}) {
		t.Errorf("the shared data was mutated: %v", data)
	}
}

func TestOwnershipTransferIsSafe(t *testing.T) {
	const n = 200

	got := ownershipTransferIsSafe(n)

	if len(got) != n {
		t.Fatalf("got %d batches, want %d", len(got), n)
	}

	// Every id exactly once, whatever the order.
	slices.Sort(got)
	for i, id := range got {
		if id != i {
			t.Fatalf("after sorting, index %d = %d — a batch was lost or duplicated", i, id)
		}
	}
}

// TestCopyingBeforeTheGoStatementIsSafe: the parent mutates its own copy
// between iterations, and no goroutine sees it.
func TestCopyingBeforeTheGoStatementIsSafe(t *testing.T) {
	const n = 50

	got := copyingBeforeTheGoStatementIsSafe(racyConfig{Timeout: 10}, n)

	if len(got) != n {
		t.Fatalf("got %d results, want %d", len(got), n)
	}

	// Each goroutine got the value at the moment its go statement ran, so the
	// results count up from the starting value.
	for i, v := range got {
		if v != 10+i {
			t.Errorf("result %d = %d, want %d — the copy should be taken at the go statement", i, v, 10+i)
		}
	}
}

func TestOnceIsSafe(t *testing.T) {
	const readers = 500

	values, distinct := onceIsSafe(readers)

	if len(values) != readers {
		t.Fatalf("got %d values, want %d", len(values), readers)
	}
	if distinct != 1 {
		t.Errorf("readers saw %d distinct values, want 1", distinct)
	}
	if values[0] != "initialised" {
		t.Errorf("value = %q, want initialised", values[0])
	}
}

func TestSafetyReasonsAreDocumented(t *testing.T) {
	reasons := whyEachIsSafe()

	if len(reasons) < 4 {
		t.Errorf("expected at least 4 documented reasons, got %d", len(reasons))
	}
	for k, v := range reasons {
		if v == "" {
			t.Errorf("%q has no reason", k)
		}
	}
}
