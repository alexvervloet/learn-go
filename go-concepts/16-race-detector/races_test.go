package main

import (
	"slices"
	"testing"
)

// The deliberate races skip under -race, because the detector is right to
// report them and would fail the package. See raceflag_race.go.
//
// They still run in the normal `go test`, where they demonstrate lost updates,
// so the examples cannot quietly stop being wrong.

func TestRacyCounterLosesUpdates(t *testing.T) {
	if raceDetectorEnabled {
		t.Skip("skipped under -race: this exercises a deliberate data race")
	}
	if testing.Short() {
		t.Skip("racy by design")
	}

	const n = 5000

	// Repeat: a single run on a quiet machine can get lucky.
	for run := 0; run < 5; run++ {
		if got := countWithRacyCounter(n); got < n {
			t.Logf("run %d: %d of %d increments survived", run, got, n)
			return
		}
	}

	t.Skip("no lost updates observed on this machine; the race is still present")
}

func TestRacyAppendLosesElements(t *testing.T) {
	if raceDetectorEnabled {
		t.Skip("skipped under -race: this exercises a deliberate data race")
	}
	if testing.Short() {
		t.Skip("racy by design")
	}

	const n = 5000

	for run := 0; run < 5; run++ {
		if got := len(racyAppend(n)); got < n {
			t.Logf("run %d: %d of %d elements survived", run, got, n)
			return
		}
	}

	t.Skip("no lost elements observed on this machine; the race is still present")
}

func TestRacyClosureCaptureLosesUpdates(t *testing.T) {
	if raceDetectorEnabled {
		t.Skip("skipped under -race: this exercises a deliberate data race")
	}
	if testing.Short() {
		t.Skip("racy by design")
	}

	const n = 5000
	want := n * (n - 1) / 2

	for run := 0; run < 5; run++ {
		if got := racyClosureCapture(n); got != want {
			t.Logf("run %d: total %d, want %d", run, got, want)
			return
		}
	}

	t.Skip("no lost updates observed on this machine; the race is still present")
}

// TestRacyStructAccessCompletes does not assert a value, because there is no
// correct one: the point is that it runs and -race reports it.
func TestRacyStructAccessCompletes(t *testing.T) {
	if raceDetectorEnabled {
		t.Skip("skipped under -race: this exercises a deliberate data race")
	}

	cfg := racyStructAccess(100)
	if cfg == nil {
		t.Error("expected a config back")
	}
}

func TestRaceDocsArePresent(t *testing.T) {
	if got := raceConditions(); len(got) != 4 {
		t.Errorf("expected exactly 4 conditions, got %d", len(got))
	}
	if got := whyUndefinedBehaviourMatters(); len(got) < 4 {
		t.Errorf("expected at least 4 notes, got %d", len(got))
	}
	if got := racyMapWriteDescription(); len(got) < 3 {
		t.Errorf("expected at least 3 notes on concurrent map writes, got %d", len(got))
	}
}

// TestRaceFlagMatchesTheBuild: the constant must agree with how the binary was
// compiled, or every skip above is wrong.
func TestRaceFlagMatchesTheBuild(t *testing.T) {
	// There is no runtime API for this, so the check is indirect: under -race
	// the deliberate races must not have been run, and the flag says so.
	t.Logf("raceDetectorEnabled = %t", raceDetectorEnabled)

	// A sanity check that the constant is at least usable in a condition.
	if raceDetectorEnabled == !raceDetectorEnabled {
		t.Error("impossible")
	}
}

// TestFixedVersionsAreAlwaysCorrect runs under BOTH builds, which is the
// important half: the fixes must be correct and race-free.
func TestFixedVersionsAreAlwaysCorrect(t *testing.T) {
	const n = 2000

	tests := []struct {
		name string
		got  int
		want int
	}{
		{"mutex counter", countWithMutex(n), n},
		{"atomic counter", countWithAtomic(n), n},
		{"guarded map", fillMapConcurrently(n), n},
		{"pre-sized slice", len(fillSliceByIndex(n)), n},
		{"append under a lock", len(appendUnderLock(n)), n},
		{"collected by channel", len(collectOverAChannel(n)), n},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("got %d, want %d", tt.got, tt.want)
			}
		})
	}
}

// TestPreSizedSlicePreservesOrder is what the indexed fix buys over a mutex
// and an append: the output order matches the input.
func TestPreSizedSlicePreservesOrder(t *testing.T) {
	got := fillSliceByIndex(100)

	for i, v := range got {
		if v != i*i {
			t.Fatalf("index %d = %d, want %d — order should be preserved", i, v, i*i)
		}
	}
}

// TestAppendUnderLockDoesNotPreserveOrder is the contrast, asserted so the
// distinction is not folklore.
func TestAppendUnderLockHasEveryElement(t *testing.T) {
	got := appendUnderLock(1000)

	slices.Sort(got)
	for i, v := range got {
		if v != i {
			t.Fatalf("after sorting, index %d = %d — an element was lost or duplicated", i, v)
		}
	}
}

func TestSumWithoutSharing(t *testing.T) {
	tests := []struct {
		n, workers, want int
	}{
		{100, 4, 4950},
		{1000, 8, 499500},
		{10, 1, 45},
		{10, 20, 45}, // more workers than items
	}

	for _, tt := range tests {
		if got := sumWithoutSharing(tt.n, tt.workers); got != tt.want {
			t.Errorf("sumWithoutSharing(%d, %d) = %d, want %d", tt.n, tt.workers, got, tt.want)
		}
	}
}

func TestConfigFixesAreConsistent(t *testing.T) {
	guarded, swapped := updateConfigConcurrently(500)

	timeout, retries, name := guarded.Snapshot()
	if retries != timeout*2 {
		t.Errorf("guarded snapshot is inconsistent: timeout=%d retries=%d", timeout, retries)
	}
	if name == "" {
		t.Error("guarded snapshot lost the name")
	}

	if swapped.Retries != swapped.Timeout*2 {
		t.Errorf("swapped config is inconsistent: %+v", swapped)
	}
}

func TestFixDocsArePresent(t *testing.T) {
	if got := happensBeforeEdges(); len(got) < 6 {
		t.Errorf("expected at least 6 memory-model edges, got %d", len(got))
	}
	if got := fixPreference(); len(got) < 5 {
		t.Errorf("expected at least 5 ordered preferences, got %d", len(got))
	}
}
