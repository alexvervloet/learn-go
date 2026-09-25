package main

import (
	"math"
	"runtime/debug"
	"strings"
	"testing"
)

func TestSnapshotReportsSomethingSensible(t *testing.T) {
	s := Snapshot()

	if s.HeapAllocKB == 0 {
		t.Error("HeapAlloc is 0, which cannot be right for a running program")
	}
	if s.HeapSysKB < s.HeapAllocKB {
		t.Errorf("HeapSys (%d KB) is below HeapAlloc (%d KB)", s.HeapSysKB, s.HeapAllocKB)
	}
	if s.NextGCKB == 0 {
		t.Error("NextGC is 0")
	}
	if s.GCCPUPercent < 0 || s.GCCPUPercent > 100 {
		t.Errorf("GC CPU = %.2f%%, want 0-100", s.GCCPUPercent)
	}

	t.Logf("%s", s)
}

func TestSnapshotString(t *testing.T) {
	s := Snapshot().String()

	for _, want := range []string{"heap", "stack", "GCs", "paused", "CPU", "next at"} {
		if !strings.Contains(s, want) {
			t.Errorf("the snapshot string is missing %q: %s", want, s)
		}
	}
}

// TestAllocatingTriggersCollection: the collector runs when the heap grows
// past NextGC, which is what GOGC controls.
func TestAllocatingTriggersCollection(t *testing.T) {
	if testing.Short() {
		t.Skip("allocates 20MB")
	}

	collections := allocateAndDiscard(20_000, 1024)

	if collections == 0 {
		t.Error("allocating 20MB triggered no collection at all")
	}
	t.Logf("%d collection(s) for 20MB in 1KB pieces", collections)
}

// TestGOGCChangesTheCollectionRate is the trade the knob makes: fewer
// collections, more memory held.
func TestGOGCChangesTheCollectionRate(t *testing.T) {
	if testing.Short() {
		t.Skip("allocation-heavy")
	}

	atDefault, atHigh := gogcControlsWhenItRuns(20_000, 1024)

	t.Logf("GOGC=100: %d collections, GOGC=800: %d collections", atDefault, atHigh)

	if atHigh > atDefault {
		t.Errorf("GOGC=800 caused %d collections and GOGC=100 caused %d — raising it should reduce them",
			atHigh, atDefault)
	}
}

// TestGOGCIsRestored: the helper changes a global setting, so it must put it
// back or every later test runs under the wrong configuration.
func TestGOGCIsRestored(t *testing.T) {
	before := debug.SetGCPercent(-1)
	debug.SetGCPercent(before) // -1 reads, so put it straight back

	_, _ = gogcControlsWhenItRuns(100, 64)

	after := debug.SetGCPercent(-1)
	debug.SetGCPercent(after)

	if after != before {
		t.Errorf("GOGC was left at %d, want it restored to %d", after, before)
	}
}

func TestMemoryLimitIsReadAndRestored(t *testing.T) {
	previous, current := gomemlimitIsTheContainerAnswer()

	if current != 512<<20 {
		t.Errorf("the limit was set to %d, want %d", current, 512<<20)
	}

	// The default is math.MaxInt64, meaning no limit.
	if previous != math.MaxInt64 {
		t.Logf("a memory limit was already set: %d", previous)
	}

	// It must have been put back.
	now := debug.SetMemoryLimit(-1)
	if now != previous {
		t.Errorf("the limit was left at %d, want it restored to %d", now, previous)
		debug.SetMemoryLimit(previous)
	}
}

func TestForcingACycleReclaims(t *testing.T) {
	if testing.Short() {
		t.Skip("allocation-heavy")
	}

	beforeKB, afterKB := forcingACycle()

	t.Logf("heap %d KB -> %d KB after runtime.GC()", beforeKB, afterKB)

	if afterKB >= beforeKB {
		t.Errorf("the heap did not shrink: %d KB -> %d KB", beforeKB, afterKB)
	}
}

func TestReturningMemoryToTheOS(t *testing.T) {
	if testing.Short() {
		t.Skip("calls FreeOSMemory, which stops the world")
	}

	idleKB, releasedKB := returningMemoryToTheOS()

	t.Logf("%d KB idle before, %d KB released after", idleKB, releasedKB)

	if releasedKB == 0 {
		t.Error("FreeOSMemory released nothing")
	}
}

func TestCollectorDocsArePresent(t *testing.T) {
	props := collectorProperties()

	for _, want := range []string{"algorithm", "generational", "compacting", "tuned for"} {
		if props[want] == "" {
			t.Errorf("%q is not documented", want)
		}
	}
	if !strings.Contains(props["generational"], "NO") {
		t.Error("the collector is not generational, and the docs should say so")
	}
	if !strings.Contains(props["compacting"], "NO") {
		t.Error("the collector is not compacting, and the docs should say so")
	}

	if got := knobs(); len(got) < 6 {
		t.Errorf("expected at least 6 knobs, got %d", len(got))
	}
}
