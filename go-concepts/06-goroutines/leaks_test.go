package main

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"
)

// assertNoLeak runs fn and checks that every goroutine the examples started has
// returned.
//
// # Why this counts the examples' own goroutines and not runtime.NumGoroutine
//
// NumGoroutine counts the whole process, so an unrelated goroutine on its way
// out changes the number this is trying to measure. CI failed a working leak
// test with "4 before, 4 after": one goroutine leaked, one unrelated one
// exited, and the total stayed put. The counter in leaks.go is touched only by
// this file's examples, so nothing can cancel out.
//
// The poll is still needed in THIS direction. A goroutine that has been
// unblocked has not necessarily run its deferred decrement yet, so a single
// immediate check fails a few percent of the time. Polling with a deadline is
// the fix, and it is what uber-go/goleak does too.
func assertNoLeak(t *testing.T, fn func()) {
	t.Helper()

	before := LiveExampleGoroutines()
	fn()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		runtime.Gosched()

		if LiveExampleGoroutines() <= before {
			return
		}

		time.Sleep(5 * time.Millisecond)
	}

	t.Errorf("leaked goroutines: %d live before, %d after", before, LiveExampleGoroutines())
}

// assertLeaks is the inverse, and it is what keeps the broken examples honest.
// If someone "fixes" leakySender, this test fails and the README needs editing.
//
// # No poll, and no sleep
//
// The count rises in the PARENT, before the `go` statement, so by the time fn
// returns the leak is already recorded. There is nothing to wait for, which is
// the difference between a test that is deterministic and one that is merely
// usually right.
func assertLeaks(t *testing.T, fn func()) {
	t.Helper()

	before := LiveExampleGoroutines()
	fn()

	after := LiveExampleGoroutines()
	if after <= before {
		t.Errorf("expected a leak: %d live before, %d after", before, after)
	}

	// And it stays leaked. A goroutine that exits a moment later is not a leak,
	// it is a slow return, and the two are worth telling apart.
	time.Sleep(50 * time.Millisecond)

	if LiveExampleGoroutines() < after {
		t.Errorf("the goroutine returned after all, so this is not a leak: %d then %d",
			after, LiveExampleGoroutines())
	}
}

func TestLeakySenderLeaks(t *testing.T) {
	assertLeaks(t, func() {
		if got := leakySender(); got != 0 {
			t.Errorf("leakySender() = %d, want 0", got)
		}
	})
}

func TestFixedSendersDoNotLeak(t *testing.T) {
	t.Run("cancellation lets the producer exit", func(t *testing.T) {
		assertNoLeak(t, func() {
			if got := fixedSenderWithContext(); got != 0 {
				t.Errorf("got %d, want 0", got)
			}
		})
	})

	t.Run("a buffer large enough completes the send", func(t *testing.T) {
		assertNoLeak(t, func() {
			if got := fixedSenderWithBuffer(); got != 42 {
				t.Errorf("got %d, want 42", got)
			}
		})
	})
}

func TestLeakyReceiverLeaks(t *testing.T) {
	assertLeaks(t, leakyReceiver)
}

func TestFixedReceiverDoesNotLeak(t *testing.T) {
	assertNoLeak(t, func() {
		if got := fixedReceiver(); got != 3 {
			t.Errorf("received %d values, want 3", got)
		}
	})
}

func TestFixedWorkerRespectsCancellation(t *testing.T) {
	tests := []struct {
		name          string
		timeout       time.Duration
		steps         int
		each          time.Duration
		wantErr       error
		wantCompleted int // -1 means "do not assert exactly"
	}{
		{"finishes within the deadline", time.Second, 3, time.Millisecond, nil, 3},
		{"cancelled partway", 25 * time.Millisecond, 100, 5 * time.Millisecond, context.DeadlineExceeded, -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), tt.timeout)
			defer cancel()

			completed, err := fixedWorker(ctx, tt.steps, tt.each)

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if tt.wantCompleted >= 0 && completed != tt.wantCompleted {
				t.Errorf("completed = %d, want %d", completed, tt.wantCompleted)
			}
			if tt.wantCompleted < 0 && completed >= tt.steps {
				t.Errorf("completed = %d, want fewer than %d — it should have been cut short", completed, tt.steps)
			}
		})
	}
}

// TestFixedWorkerRespondsPromptly: cancellation is only useful if it is
// noticed quickly. This checks the worker stops within one step of the
// deadline rather than running to completion.
func TestFixedWorkerRespondsPromptly(t *testing.T) {
	const step = 10 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	start := time.Now()
	completed, err := fixedWorker(ctx, 1000, step)
	elapsed := time.Since(start)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want DeadlineExceeded", err)
	}
	if elapsed > 30*time.Millisecond+2*step {
		t.Errorf("took %v to notice a 30ms deadline", elapsed)
	}
	t.Logf("stopped after %d steps in %v", completed, elapsed.Round(time.Millisecond))
}

func TestCountGoroutinesIsPositive(t *testing.T) {
	if got := countGoroutines(); got < 1 {
		t.Errorf("countGoroutines() = %d, want at least 1", got)
	}
}
