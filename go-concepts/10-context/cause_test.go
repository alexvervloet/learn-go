package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCancelWithCause(t *testing.T) {
	err, cause := cancelWithCause(ErrUpstreamFailed)

	if !errors.Is(err, context.Canceled) {
		t.Errorf("Err() = %v, want context.Canceled", err)
	}
	if !errors.Is(cause, ErrUpstreamFailed) {
		t.Errorf("Cause() = %v, want ErrUpstreamFailed", cause)
	}
	// The two are deliberately different: one is the state, one is the reason.
	if errors.Is(err, ErrUpstreamFailed) {
		t.Error("Err() should report the state, not the cause")
	}
}

// TestCauseIsNeverLessInformativeThanErr: with no cause supplied, Cause falls
// back to Err, so calling Cause is always safe.
func TestCauseIsNeverLessInformativeThanErr(t *testing.T) {
	plainErr, plainCause, nilErr, nilCause := causeWithoutACause()

	if !errors.Is(plainCause, plainErr) {
		t.Errorf("plain WithCancel: Cause = %v, want it to match Err = %v", plainCause, plainErr)
	}
	if !errors.Is(nilCause, nilErr) {
		t.Errorf("cancel(nil): Cause = %v, want it to match Err = %v", nilCause, nilErr)
	}
}

// TestCausePropagatesDownTheTree is what makes it worth using: a goroutine
// several levels down learns the real reason.
func TestCausePropagatesDownTheTree(t *testing.T) {
	childErr, childCause := causePropagatesDownTheTree()

	if !errors.Is(childErr, context.Canceled) {
		t.Errorf("Err() = %v, want context.Canceled", childErr)
	}
	if !errors.Is(childCause, ErrUpstreamFailed) {
		t.Errorf("Cause() = %v, want ErrUpstreamFailed to reach the grandchild", childCause)
	}
}

func TestDeadlineCause(t *testing.T) {
	err, cause := deadlineCause(20*time.Millisecond, ErrBudgetExceeded)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Err() = %v, want DeadlineExceeded", err)
	}
	if !errors.Is(cause, ErrBudgetExceeded) {
		t.Errorf("Cause() = %v, want ErrBudgetExceeded", cause)
	}
}

func TestWithoutCancel(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	t.Run("the request context cancels the audit write", func(t *testing.T) {
		audited, err := handlerWithoutDetaching(cancelled, 5*time.Millisecond)

		if audited {
			t.Error("the audit write should have been cancelled")
		}
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want context.Canceled", err)
		}
	})

	t.Run("detaching lets it finish", func(t *testing.T) {
		audited, err := handlerWithDetaching(cancelled, 5*time.Millisecond)

		if !audited {
			t.Errorf("the detached write should have completed, got err %v", err)
		}
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

// TestWithoutCancelKeepsValues: detaching drops the cancellation and keeps the
// values, which is what makes the trace ID survive into the detached work.
func TestWithoutCancelKeepsValues(t *testing.T) {
	idBefore, idAfter, cancelledBefore, cancelledAfter := valuesSurviveWithoutCancel()

	if idBefore != idAfter {
		t.Errorf("request id %q became %q — values should survive detaching", idBefore, idAfter)
	}
	if !cancelledBefore {
		t.Error("the original context should have been cancelled")
	}
	if cancelledAfter {
		t.Error("the detached context should NOT be cancelled")
	}
}

// TestWithoutCancelStillHonoursItsOwnDeadline: detached does not mean
// unbounded, and handlerWithDetaching adds a fresh timeout for that reason.
func TestWithoutCancelStillHonoursItsOwnDeadline(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	// An audit write that takes longer than the detached context's 1s budget.
	audited, err := handlerWithDetaching(cancelled, 2*time.Second)

	if audited {
		t.Error("the detached write should still have hit its own deadline")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want DeadlineExceeded", err)
	}
}

func TestAfterFunc(t *testing.T) {
	t.Run("runs on cancel", func(t *testing.T) {
		if !afterFuncRunsOnCancel() {
			t.Error("AfterFunc should have run")
		}
	})

	t.Run("stop deregisters it", func(t *testing.T) {
		stopped, ranAnyway := stopPreventsIt()

		if !stopped {
			t.Error("stop() should report that it prevented the call")
		}
		if ranAnyway {
			t.Error("the function ran despite being stopped")
		}
	})

	t.Run("an already-done context still runs it", func(t *testing.T) {
		if !afterFuncOnAnAlreadyDoneContext() {
			t.Error("AfterFunc on a cancelled context should run immediately, not skip")
		}
	})
}

func TestCleanupOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	stop := cleanupOnCancel(ctx, func() { close(done) })

	if stop == nil {
		t.Fatal("cleanupOnCancel should return a stop function")
	}

	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Error("cleanup did not run within a second of cancellation")
	}

	// Stopping after it has already run reports false.
	if stop() {
		t.Error("stop() after the function ran should report false")
	}
}
