package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// TestForgettingCancelRetainsHeap measures the leak with the right instrument.
// It is retained heap held by a live parent, not goroutines: a derived context
// registers in its parent's children map and cancelling removes it.
func TestForgettingCancelRetainsHeap(t *testing.T) {
	if testing.Short() {
		t.Skip("allocation measurement")
	}

	const n = 20_000

	leaked := retainedBytes(func() any {
		parent, cancel := leakingContexts(n)
		_ = cancel
		return parent
	})
	clean := retainedBytes(func() any {
		notLeakingContexts(n)
		return nil
	})

	t.Logf("%d contexts: leaked %d KB, cancelled %d KB", n, leaked/1024, clean/1024)

	if leaked <= clean {
		t.Errorf("uncancelled contexts retained %d bytes vs %d for cancelled — expected far more",
			leaked, clean)
	}
	// A generous floor: the gap in practice is two orders of magnitude.
	if leaked < clean*5 {
		t.Errorf("expected the leak to be at least 5x: %d vs %d", leaked, clean)
	}
}

func TestStoredContextPoisonsLaterCalls(t *testing.T) {
	badFirst, badSecond, goodSecond := storedContextPoisonsLaterCalls()

	if badFirst != nil {
		t.Errorf("the first call on the stored context should succeed, got %v", badFirst)
	}
	if !errors.Is(badSecond, context.Canceled) {
		t.Errorf("the second call = %v, want context.Canceled — the stored context is dead", badSecond)
	}
	if goodSecond != nil {
		t.Errorf("the per-call client should be unaffected, got %v", goodSecond)
	}
}

// TestGoodClientIsReusable: the whole point of taking ctx per call.
func TestGoodClientIsReusable(t *testing.T) {
	c := newGoodClient()

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	if err := c.Fetch(cancelled, 1); !errors.Is(err, context.Canceled) {
		t.Errorf("a cancelled context should fail the call, got %v", err)
	}

	// The SAME client still works with a live context.
	if err := c.Fetch(context.Background(), 2); err != nil {
		t.Errorf("the client should still work with a live context, got %v", err)
	}
}

func TestNilContextPanics(t *testing.T) {
	msg := nilContextPanics()

	if msg == "" {
		t.Fatal("a nil context should panic")
	}
	if !strings.Contains(msg, "nil parent") {
		t.Errorf("panic = %q, want it to mention a nil parent", msg)
	}
}

func TestTodoIsThePlaceholder(t *testing.T) {
	if !todoIsThePlaceholder() {
		t.Error("context.TODO() should work as a parent")
	}
}

// TestPollingVsSelecting: both notice, but one burns CPU doing it.
func TestPollingVsSelecting(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	checks := pollingWastesCPU(ctx, 5*time.Millisecond)
	if checks < 1 {
		t.Errorf("polling made %d checks, want at least 1", checks)
	}

	if !selectingParksTheGoroutine(ctx, time.Second) {
		t.Error("selecting on Done() should notice an already-cancelled context immediately")
	}
}

func TestSelectingReturnsPromptly(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	start := time.Now()
	cancelled := selectingParksTheGoroutine(ctx, 10*time.Second)
	elapsed := time.Since(start)

	if !cancelled {
		t.Error("should have been cancelled by the deadline")
	}
	if elapsed > time.Second {
		t.Errorf("took %v to notice a 20ms deadline", elapsed)
	}
}

func TestRequiredValueBelongsInTheSignature(t *testing.T) {
	t.Run("a missing context value is a runtime failure", func(t *testing.T) {
		_, err := badCharge(context.Background())
		if err == nil {
			t.Error("expected a failure when the amount is absent")
		}
	})

	t.Run("present in the context, it works", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), amountKey{}, 250)
		charged, err := badCharge(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if charged != 250 {
			t.Errorf("charged %d, want 250", charged)
		}
	})

	t.Run("as a parameter it cannot be forgotten", func(t *testing.T) {
		charged, err := goodCharge(context.Background(), 100)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if charged != 100 {
			t.Errorf("charged %d, want 100", charged)
		}
	})

	t.Run("as a parameter it still honours cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		if _, err := goodCharge(ctx, 100); !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want context.Canceled", err)
		}
	})
}

func TestDerivingFromTheWrongParent(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	if !derivedFromBackground(cancelled, 10*time.Millisecond) {
		t.Error("deriving from Background ignores the caller, so the work completes")
	}
	if derivedFromTheRequest(cancelled, 10*time.Millisecond) {
		t.Error("deriving from the request context should stop the work")
	}
}
