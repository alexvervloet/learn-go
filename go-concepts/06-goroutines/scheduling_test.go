package main

import (
	"runtime"
	"testing"
	"time"
)

func TestSchedulerFacts(t *testing.T) {
	numCPU, maxprocs, goroutines, version := schedulerFacts()

	if numCPU < 1 {
		t.Errorf("NumCPU = %d, want at least 1", numCPU)
	}
	if maxprocs < 1 {
		t.Errorf("GOMAXPROCS = %d, want at least 1", maxprocs)
	}
	if goroutines < 1 {
		t.Errorf("NumGoroutine = %d, want at least 1 (this one)", goroutines)
	}
	if version == "" {
		t.Error("runtime.Version() should not be empty")
	}
	t.Logf("%s NumCPU=%d GOMAXPROCS=%d", version, numCPU, maxprocs)
}

func TestWithGOMAXPROCSRestores(t *testing.T) {
	before := runtime.GOMAXPROCS(0)

	withGOMAXPROCS(1, func() time.Duration {
		if got := runtime.GOMAXPROCS(0); got != 1 {
			t.Errorf("inside the call GOMAXPROCS = %d, want 1", got)
		}
		return 0
	})

	if after := runtime.GOMAXPROCS(0); after != before {
		t.Errorf("GOMAXPROCS = %d after the call, want it restored to %d", after, before)
	}
}

// TestParallelIsFasterThanSequential is the no-GIL claim, measured.
//
// It is written to be robust rather than precise: CI runners are shared, noisy,
// and sometimes single-core. The assertion is "meaningfully faster when there
// is more than one core", with generous slack, and a skip when there is not.
// A tight threshold here would produce a test that fails for reasons unrelated
// to the code.
func TestParallelIsFasterThanSequential(t *testing.T) {
	if testing.Short() {
		t.Skip("CPU-bound timing test")
	}
	if runtime.NumCPU() < 2 {
		t.Skip("needs more than one CPU to show parallelism")
	}

	const (
		chunks     = 8
		iterations = 4_000_000
	)

	seq := sequential(chunks, iterations)
	par := parallel(chunks, iterations)

	t.Logf("sequential %v, parallel %v, speedup %.1fx on %d CPUs",
		seq.Round(time.Millisecond), par.Round(time.Millisecond),
		float64(seq)/float64(par), runtime.NumCPU())

	if par >= seq {
		t.Errorf("parallel (%v) was not faster than sequential (%v)", par, seq)
	}
}

// TestConcurrencyIsNotParallelism: with one P, concurrent code does the same
// total work in roughly the same total time. This is the shape Python's
// threading is stuck in permanently.
func TestConcurrencyIsNotParallelism(t *testing.T) {
	if testing.Short() {
		t.Skip("CPU-bound timing test")
	}

	const (
		chunks     = 8
		iterations = 2_000_000
	)

	seq := sequential(chunks, iterations)
	oneP := withGOMAXPROCS(1, func() time.Duration { return parallel(chunks, iterations) })

	t.Logf("sequential %v, concurrent-on-1-P %v", seq.Round(time.Millisecond), oneP.Round(time.Millisecond))

	// Within 3x is a deliberately loose bound. The point is that it is the same
	// order of magnitude, not 8x faster: no parallelism was added.
	if oneP > 3*seq {
		t.Errorf("concurrent on one P took %v vs sequential %v — more than expected overhead", oneP, seq)
	}
}

func TestBlockingSyscallDoesNotStopOthers(t *testing.T) {
	if testing.Short() {
		t.Skip("timing test")
	}

	progressed := blockingSyscallDoesNotStopOthers(8, 30*time.Millisecond)

	if progressed == 0 {
		t.Error("the working goroutine made no progress while others were blocked")
	}
	t.Logf("%d iterations completed while 8 goroutines were parked", progressed)
}

func TestPreemptionFactsAreDocumented(t *testing.T) {
	if got := preemptionFacts(); len(got) < 3 {
		t.Errorf("expected at least 3 documented facts, got %d", len(got))
	}
}

func BenchmarkSequential(b *testing.B) {
	for b.Loop() {
		sequential(8, 500_000)
	}
}

func BenchmarkParallel(b *testing.B) {
	for b.Loop() {
		parallel(8, 500_000)
	}
}
