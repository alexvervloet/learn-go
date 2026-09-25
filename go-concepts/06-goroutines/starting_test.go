package main

import (
	"runtime"
	"slices"
	"sync"
	"testing"
)

func TestGoroutinesStartUnordered(t *testing.T) {
	const n = 50

	got := goroutinesStartUnordered(n)

	if len(got) != n {
		t.Fatalf("got %d values, want %d", len(got), n)
	}

	// Every value appears exactly once, whatever the order. Asserting the ORDER
	// would be asserting a scheduling detail that is explicitly not guaranteed.
	sorted := slices.Clone(got)
	slices.Sort(sorted)
	for i, v := range sorted {
		if v != i {
			t.Fatalf("value %d is %d, want %d — each iteration should appear once", i, v, i)
		}
	}
}

// TestMainDoesNotWait asserts a RANGE, not a value. The count is the outcome of
// a race, so pinning it to a number would make the test itself flaky.
func TestMainDoesNotWait(t *testing.T) {
	const n = 100

	got := mainDoesNotWait(n)

	if got < 0 || got > n {
		t.Errorf("finished = %d, want something in [0, %d]", got, n)
	}
	t.Logf("%d of %d goroutines happened to finish before the read", got, n)
}

// TestWaitForThem is the counterpart, and it CAN assert an exact value, because
// WaitGroup.Wait establishes a happens-before edge with every Done.
func TestWaitForThem(t *testing.T) {
	const n = 100

	for i := 0; i < 20; i++ {
		if got := waitForThem(n); got != n {
			t.Fatalf("run %d: finished = %d, want %d", i, got, n)
		}
	}
}

func TestManyGoroutines(t *testing.T) {
	if testing.Short() {
		t.Skip("starts 10,000 goroutines")
	}

	const n = 10_000

	before := runtime.NumGoroutine()
	peak := manyGoroutines(n)

	// Slack, because `before` is a snapshot of a moving number: a goroutine
	// from an earlier test may still be tearing down, and under -race the
	// counting drifts further. The claim is "roughly n of them were live at
	// once", not an exact arithmetic identity, and CI reported 10004 against a
	// hard expectation of 10005.
	const slack = 10
	if peak < n-slack {
		t.Errorf("peak = %d, want at least %d — all %d should be live at once",
			peak, n-slack, n)
	}
	t.Logf("%d goroutines parked simultaneously (baseline %d, peak %d)", n, before, peak)
}

// BenchmarkGoroutineCreation measures what a goroutine actually costs. Run:
//
//	go test -bench BenchmarkGoroutine -benchmem -run '^$' ./06-goroutines
func BenchmarkGoroutineCreation(b *testing.B) {
	var wg sync.WaitGroup

	for b.Loop() {
		wg.Add(1)
		go wg.Done()
		wg.Wait()
	}
}

// BenchmarkGoroutineCreationParallel is the more honest number: creating many
// without joining each one, which is how they are actually used.
func BenchmarkGoroutineCreationBatch(b *testing.B) {
	for b.Loop() {
		var wg sync.WaitGroup
		for i := 0; i < 1000; i++ {
			wg.Add(1)
			go wg.Done()
		}
		wg.Wait()
	}
}
