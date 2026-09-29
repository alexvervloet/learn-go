package main

import (
	"runtime"
	"sync"
	"testing"
	"time"
)

// TestLeakedGoroutineIsInvisibleToRace: nothing is racing, so -race has
// nothing to report. This test runs clean under -race and the leak is real.
func TestLeakedGoroutineIsInvisibleToRace(t *testing.T) {
	if testing.Short() {
		t.Skip("leaks a goroutine deliberately")
	}

	leaked := leakedGoroutine()

	if leaked < 1 {
		t.Errorf("leaked %d goroutines, want at least 1", leaked)
	}
	t.Logf("leaked %+d goroutine(s), and -race reported nothing", leaked)
}

// TestLockOrderingDeadlockIsInvisibleToRace: every access is correctly locked.
//
// This test leaks two goroutines, permanently, each holding a mutex. That is
// what a deadlock is; cleaning it up would mean it was not one.
func TestLockOrderingDeadlockIsInvisibleToRace(t *testing.T) {
	if testing.Short() {
		t.Skip("deadlocks two goroutines deliberately")
	}

	if !lockOrderingDeadlock(200 * time.Millisecond) {
		t.Error("expected the lock-order inversion to deadlock")
	}
}

// TestLogicalRaceIsInvisibleToRace is the most important test here: the broken
// version is perfectly synchronised and loses updates, and -race is silent.
func TestLogicalRaceIsInvisibleToRace(t *testing.T) {
	if testing.Short() {
		t.Skip("contention-dependent")
	}

	const n = 5000

	for run := range 5 {
		broken, fixed := logicalRaceLosesUpdates(n)

		if fixed != n {
			t.Fatalf("the fixed version lost updates: %d of %d", fixed, n)
		}
		if broken < n {
			t.Logf("run %d: check-then-act kept %d of %d, and -race reported nothing", run, broken, n)
			return
		}
	}

	t.Skip("no lost updates observed this run; the logical race is still present")
}

// TestCheckThenActHasNoDataRace is the half that makes the point:
// the broken version has no DATA race, only a logical one. It runs clean under
// -race, which is exactly the problem.
func TestCheckThenActHasNoDataRace(t *testing.T) {
	c := &checkThenActCounter{}

	// If IncBroken had a data race, -race would fail this. It does not.
	for range 100 {
		go c.IncBroken()
	}
	time.Sleep(50 * time.Millisecond)

	// No assertion on the value: there is no correct one. The point is that
	// this is clean under -race.
	_ = c.Value()
}

func TestNotCaughtDocsArePresent(t *testing.T) {
	if got := whatItMisses(); len(got) < 4 {
		t.Errorf("expected at least 4 documented gaps, got %d", len(got))
	}
	if got := itObservesRatherThanAnalyses(); len(got) < 4 {
		t.Errorf("expected at least 4 notes, got %d", len(got))
	}
	if got := costs(); len(got) < 3 {
		t.Errorf("expected at least 3 documented costs, got %d", len(got))
	}
	if got := theRuntimeDeadlockDetectorIsAllOrNothing(); len(got) < 3 {
		t.Errorf("expected at least 3 notes on the runtime detector, got %d", len(got))
	}
}

// TestNoGoroutineLimitUnderRace checks the claim in the costs table by doing it: 10,000 goroutines alive at
// once. Under -race before Go 1.19 this died with "race: limit on 8128 simultaneously alive goroutines is
// exceeded". The test it replaces asserted that the table said "8192", which enforced the old limit as a fact
// long after the limit was gone.
func TestNoGoroutineLimitUnderRace(t *testing.T) {
	const n = 10_000

	release := make(chan struct{})

	var started, done sync.WaitGroup

	started.Add(n)

	for range n {
		done.Go(func() {
			started.Done()
			<-release
		})
	}

	started.Wait()

	t.Logf("%d goroutines alive at once, race detector %t", runtime.NumGoroutine(), raceDetectorEnabled)

	close(release)
	done.Wait()
}
