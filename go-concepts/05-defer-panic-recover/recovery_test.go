package main

import (
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
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

// childEnv is set when a test re-runs its own binary to do something that ends the process.
const childEnv = "LEARN_GO_CHILD"

// runChild re-runs this test binary with only the named test, as a child process, with childEnv set to mode.
// It returns everything the child wrote and whether it exited cleanly.
//
// This is how a test checks something that kills the process: a panic that no recover can catch, a crash
// trace. Doing it in the test's own process would end the test run.
func runChild(t *testing.T, test, mode string) (output string, exitedCleanly bool) {
	t.Helper()

	cmd := exec.Command(os.Args[0], "-test.run=^"+test+"$", "-test.count=1")
	cmd.Env = append(os.Environ(), childEnv+"="+mode)

	out, err := cmd.CombinedOutput()

	return string(out), err == nil
}

func TestPanicInGoroutineIsUnrecoverable(t *testing.T) {
	if os.Getenv(childEnv) == "goroutine-panic" {
		// In the child: the panic kills the process.
		panicInGoroutineIsUnrecoverable(true)

		// This line sometimes runs, and that is worth knowing. The goroutine's deferred wg.Done() runs
		// while its panic unwinds, which releases wg.Wait() above, so the parent can wake and get this
		// far before the runtime finishes killing the process. It gets no further: the test below
		// requires the child to have died, and "finished" never prints.
		fmt.Println("the parent woke up")
		time.Sleep(time.Second)
		fmt.Println("the parent finished")

		return
	}

	t.Run("without a panic, the parent's recover never fires", func(t *testing.T) {
		parentRecovered, done := panicInGoroutineIsUnrecoverable(false)

		if parentRecovered {
			t.Error("the parent's recover should not have fired")
		}
		if !done {
			t.Error("the goroutine should have completed")
		}
	})

	t.Run("with a panic, the whole process dies", func(t *testing.T) {
		out, clean := runChild(t, "TestPanicInGoroutineIsUnrecoverable", "goroutine-panic")

		if clean {
			t.Fatalf("the child exited cleanly; a panic in a goroutine should kill the process:\n%s", out)
		}
		if !strings.Contains(out, "panic: boom") {
			t.Errorf("the child did not die of the goroutine's panic:\n%s", out)
		}
		if strings.Contains(out, "the parent finished") {
			t.Error("the parent ran to completion after the goroutine panicked")
		}

		t.Logf("parent woke before the process died: %t", strings.Contains(out, "the parent woke up"))
	})
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

	if os.Getenv(childEnv) == "repanic" {
		// In the child: nothing catches the re-panic, so this prints a crash trace and exits.
		repanicPreservesTheStack("not mine", mine)

		return
	}

	t.Run("the crash trace keeps the original panic site", func(t *testing.T) {
		out, clean := runChild(t, "TestRepanicPreservesTheStack", "repanic")

		if clean {
			t.Fatalf("the child exited cleanly:\n%s", out)
		}
		if !strings.Contains(out, "panic: not mine [recovered, repanicked]") {
			t.Errorf("the header does not say the panic was re-raised:\n%s", out)
		}

		// The deferred closure that re-panicked is repanicPreservesTheStack.func1. The frame of
		// repanicPreservesTheStack itself is the ORIGINAL panic(value), and it must still be there.
		if !strings.Contains(out, ".repanicPreservesTheStack(") {
			t.Errorf("the original panic site is missing from the trace:\n%s", out)
		}
	})

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
