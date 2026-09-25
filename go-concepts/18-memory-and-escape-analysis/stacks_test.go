package main

import (
	"testing"
)

// TestDeepRecursion is the claim lesson 05 relied on: Go grows stacks by
// copying, so a depth that crashes C and raises in Python merely takes a
// little longer here.
func TestDeepRecursion(t *testing.T) {
	tests := []struct {
		depth int
		want  int
	}{
		{0, 0},
		{1, 1},
		{10, 55},
		{100, 5050},
	}

	for _, tt := range tests {
		if got := deepRecursion(tt.depth); got != tt.want {
			t.Errorf("deepRecursion(%d) = %d, want %d", tt.depth, got, tt.want)
		}
	}
}

func TestDeepRecursionAtScale(t *testing.T) {
	if testing.Short() {
		t.Skip("recurses a million frames")
	}

	// Python raises RecursionError at 1000; C segfaults on a fixed 8MB stack.
	// This is the assertion behind that comparison.
	if got := deepRecursion(1_000_000); got == 0 {
		t.Error("a million frames produced 0, which suggests it did not run")
	}
}

func TestMeasureStackGrowth(t *testing.T) {
	if testing.Short() {
		t.Skip("allocates a large stack")
	}

	beforeKB, duringKB := measureStackGrowth(50_000)

	t.Logf("stack in use: %d KB before, %d KB at 50,000 frames", beforeKB, duringKB)

	if duringKB <= beforeKB {
		t.Errorf("stack did not grow: %d KB -> %d KB", beforeKB, duringKB)
	}
}

func TestGoroutineStackCost(t *testing.T) {
	if testing.Short() {
		t.Skip("starts 10,000 goroutines")
	}

	perGoroutine := goroutineStackCost(10_000)

	t.Logf("~%d bytes of stack per parked goroutine", perGoroutine)

	// A goroutine starts at 8KB of ADDRESS SPACE but the runtime does not
	// commit it all, and StackInuse is measured in spans, so the per-goroutine
	// figure is well under 8192. The assertion is a sanity bound rather than a
	// precise claim: this is indicative, not exact.
	if perGoroutine == 0 {
		t.Error("measured 0 bytes per goroutine, which cannot be right")
	}
	if perGoroutine > 16*1024 {
		t.Errorf("measured %d bytes per goroutine, which is implausibly high", perGoroutine)
	}
}

func TestStackDocsArePresent(t *testing.T) {
	facts := stackFacts()

	for _, want := range []string{"initial size", "growth", "maximum", "exceeding it"} {
		if facts[want] == "" {
			t.Errorf("%q is not documented", want)
		}
	}

	if got := whyStacksMatter(); len(got) < 4 {
		t.Errorf("expected at least 4 notes, got %d", len(got))
	}
}

func BenchmarkDeepRecursion(b *testing.B) {
	for b.Loop() {
		sinkIntValue = deepRecursion(1000)
	}
}
