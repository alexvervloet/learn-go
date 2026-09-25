package main

import (
	"slices"
	"strings"
	"testing"
)

func TestCorrectWaitGroup(t *testing.T) {
	const n = 1000

	// Repeat, because a synchronisation bug that fires one run in twenty is
	// still a bug and one run could miss it.
	for run := 0; run < 10; run++ {
		if got := correctWaitGroup(n); got != n {
			t.Fatalf("run %d: counted %d, want %d", run, got, n)
		}
	}
}

// TestAddInsideTheGoroutineIsUnreliable asserts that the bug is a bug. It does
// NOT assert a particular count, because the count is the outcome of a race.
//
// It also has to survive a PANIC, which CI taught me. On macOS the race
// produced:
//
//	panic: sync: WaitGroup is reused before previous Wait has returned
//
// rather than a short count. That is the same bug wearing a different hat: Wait
// returned early, the function returned, and a goroutine then called Add on a
// WaitGroup that was already being reused. Both outcomes are evidence, so the
// test accepts either and fails only if the code is reliably correct, which
// would mean the example had stopped demonstrating anything.
func TestAddInsideTheGoroutineIsUnreliable(t *testing.T) {
	if testing.Short() {
		t.Skip("racy by design")
	}
	if raceDetectorEnabled {
		// -race reports this as a race on the WaitGroup itself, which is
		// exactly right and would fail the package. See raceflag_race.go.
		t.Skip("skipped under -race: this test exercises a deliberate data race")
	}

	const n = 1000

	// runOnce returns the count, or -1 if the race panicked.
	runOnce := func() (result int) {
		defer func() {
			if r := recover(); r != nil {
				result = -1
			}
		}()
		return addInsideTheGoroutineRaces(n)
	}

	for run := 0; run < 20; run++ {
		switch got := runOnce(); {
		case got == -1:
			t.Logf("run %d: the race panicked, which is the same bug", run)
			return
		case got < n:
			t.Logf("run %d: Wait returned with %d of %d counted", run, got, n)
			return
		}
	}

	t.Skip("the race did not surface on this machine; it is still present")
}

func TestMissingDoneBlocksWait(t *testing.T) {
	if missingDoneDeadlocks() {
		t.Error("Wait should not return when a Done is missing")
	}
}

func TestNegativeCounterPanics(t *testing.T) {
	msg := negativeCounterPanics()

	if msg == "" {
		t.Fatal("one Done too many should panic")
	}
	if !strings.Contains(msg, "negative WaitGroup counter") {
		t.Errorf("panic = %q, want it to mention a negative counter", msg)
	}
}

func TestWaitGroupGo(t *testing.T) {
	const n = 1000

	for run := 0; run < 10; run++ {
		if got := waitGroupGo(n); got != n {
			t.Fatalf("run %d: counted %d, want %d", run, got, n)
		}
	}
}

func TestReusingAWaitGroup(t *testing.T) {
	tests := []struct {
		rounds, perRound, want int
	}{
		{3, 10, 30},
		{1, 1, 1},
		{5, 20, 100},
	}

	for _, tt := range tests {
		if got := reusingAWaitGroup(tt.rounds, tt.perRound); got != tt.want {
			t.Errorf("%d rounds of %d: got %d, want %d", tt.rounds, tt.perRound, got, tt.want)
		}
	}
}

// TestCollectingResultsNeedsNoLock: distinct slice indices are distinct memory,
// and Wait provides the happens-before edge. -race confirms it.
func TestCollectingResults(t *testing.T) {
	inputs := []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}

	got := collectingResults(inputs, func(v int) int { return v * v })

	want := []int{1, 4, 9, 16, 25, 36, 49, 64, 81, 100}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}

	// Order must match the input, which a channel-based collector would not
	// give without extra work.
	if got := collectingResults(nil, func(v int) int { return v }); len(got) != 0 {
		t.Errorf("empty input produced %v", got)
	}
}
