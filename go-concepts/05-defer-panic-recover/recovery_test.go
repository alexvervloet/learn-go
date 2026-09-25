package main

import (
	"slices"
	"strings"
	"sync"
	"testing"
)

// TestRecoverOnlyWorksInADeferredClosure is the placement table. Three of the
// four shapes compile, run, and do nothing, which is why this is worth a test
// rather than a paragraph.
func TestRecoverOnlyWorksInADeferredClosure(t *testing.T) {
	t.Run("directly inside a deferred closure: works", func(t *testing.T) {
		if !recoverInDeferredClosure() {
			t.Error("recover should have caught the panic")
		}
	})

	t.Run("inside a helper the closure calls: does not work", func(t *testing.T) {
		msg := capturePanic(func() { _ = recoverViaHelperDoesNotWork() })
		if msg != "boom" {
			t.Errorf("panic escaped as %q, want %q — recover is one frame too deep", msg, "boom")
		}
	})

	t.Run("not deferred at all: does not work", func(t *testing.T) {
		if recoverNotDeferredDoesNothing() {
			t.Error("recover outside a defer must return nil")
		}
	})

	t.Run("defer recover() directly: does not work", func(t *testing.T) {
		msg := capturePanic(deferRecoverDirectlyDoesNotWork)
		if msg != "boom" {
			t.Errorf("panic escaped as %q, want %q — recover ran in its own frame", msg, "boom")
		}
	})
}

func TestPanicInGoroutineIsUnrecoverable(t *testing.T) {
	// The non-panicking path, which is all that can be run without ending the
	// test process. The assertion is that the parent's recover never fires,
	// because nothing panicked in the parent's frame.
	parentRecovered, done := panicInGoroutineIsUnrecoverable(false)

	if parentRecovered {
		t.Error("the parent's recover should not have fired")
	}
	if !done {
		t.Error("the goroutine should have completed")
	}
}

// TestSafeGoContainsAPanic is the fix: every goroutine gets its own recover.
func TestSafeGoContainsAPanic(t *testing.T) {
	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		recovered []any
		completed int
	)

	onPanic := func(r any) {
		mu.Lock()
		defer mu.Unlock()
		recovered = append(recovered, r)
	}

	safeGo(&wg, onPanic, func() { panic("boom") })
	safeGo(&wg, onPanic, func() {
		mu.Lock()
		defer mu.Unlock()
		completed++
	})

	wg.Wait()

	if len(recovered) != 1 {
		t.Fatalf("recovered %d panics, want 1", len(recovered))
	}
	if recovered[0] != "boom" {
		t.Errorf("recovered %v, want \"boom\"", recovered[0])
	}
	if completed != 1 {
		t.Errorf("the non-panicking goroutine ran %d times, want 1", completed)
	}
}

// TestWorkerPoolSurvivesOneBadJob: two of four jobs panic, and the other two
// still produce results.
func TestWorkerPoolSurvivesOneBadJob(t *testing.T) {
	jobs := []func() int{
		func() int { return 1 },
		func() int { panic("bad input") },
		func() int { return 3 },
		func() int { var xs []int; return xs[5] },
	}

	results, panics := workerPoolSurvivesOneBadJob(jobs)

	if len(results) != 2 {
		t.Errorf("got %d results, want 2", len(results))
	}
	slices.Sort(results)
	if !slices.Equal(results, []int{1, 3}) {
		t.Errorf("results = %v, want [1 3]", results)
	}

	if len(panics) != 2 {
		t.Fatalf("contained %d panics, want 2", len(panics))
	}
	// Goroutines finish in any order, so assert on content rather than position.
	joined := strings.Join(panics, "\n")
	for _, want := range []string{"job 1:", "bad input", "job 3:", "index out of range"} {
		if !strings.Contains(joined, want) {
			t.Errorf("contained panics %q should mention %q", joined, want)
		}
	}
}

func TestRepanicPreservesTheStack(t *testing.T) {
	mine := func(r any) bool { return r == "mine" }

	t.Run("a recognised value is handled", func(t *testing.T) {
		if !repanicPreservesTheStack("mine", mine) {
			t.Error("expected the handler to claim the panic")
		}
	})

	t.Run("an unrecognised value keeps unwinding", func(t *testing.T) {
		msg := capturePanic(func() { _ = repanicPreservesTheStack("not mine", mine) })
		if msg != "not mine" {
			t.Errorf("escaped panic = %q, want %q", msg, "not mine")
		}
	})
}
