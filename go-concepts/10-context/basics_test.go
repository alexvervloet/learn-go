package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestBackgroundAndTODO(t *testing.T) {
	bgDone, todoDone, bgDeadline, todoDeadline := backgroundAndTODO()

	if !bgDone || !todoDone {
		t.Error("Background and TODO should have a nil Done channel — they are never cancelled")
	}
	if bgDeadline || todoDeadline {
		t.Error("Background and TODO should have no deadline")
	}

	if context.Background().Err() != nil {
		t.Error("Background().Err() should be nil")
	}
}

func TestCancelClosesDone(t *testing.T) {
	before, after, err := cancelClosesDone()

	if before {
		t.Error("Done() should not be closed before cancel")
	}
	if !after {
		t.Error("Done() should be closed after cancel")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Err() = %v, want context.Canceled", err)
	}
}

func TestErrIsNilUntilCancelled(t *testing.T) {
	before, after := errIsNilUntilCancelled()

	if before != nil {
		t.Errorf("Err() before cancel = %v, want nil", before)
	}
	if !errors.Is(after, context.Canceled) {
		t.Errorf("Err() after cancel = %v, want context.Canceled", after)
	}
}

func TestCancelIsIdempotent(t *testing.T) {
	if cancelIsIdempotent() {
		t.Error("calling cancel repeatedly must be safe")
	}
}

func TestTimeoutFiresOnItsOwn(t *testing.T) {
	elapsed, err := timeoutFiresOnItsOwn(30 * time.Millisecond)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Err() = %v, want DeadlineExceeded", err)
	}
	if elapsed < 30*time.Millisecond {
		t.Errorf("fired after %v, before the 30ms timeout", elapsed)
	}
	// Loose upper bound: timers are not precise and CI runners are shared.
	if elapsed > 2*time.Second {
		t.Errorf("took %v for a 30ms timeout", elapsed)
	}
}

// TestDeadlineExceededIsNotCanceled: the two sentinels are distinct, and code
// that branches on which one it got must be able to tell them apart.
func TestDeadlineExceededIsNotCanceled(t *testing.T) {
	_, timeoutErr := timeoutFiresOnItsOwn(10 * time.Millisecond)

	if errors.Is(timeoutErr, context.Canceled) {
		t.Error("a deadline should not report as Canceled")
	}

	_, _, cancelErr := cancelClosesDone()
	if errors.Is(cancelErr, context.DeadlineExceeded) {
		t.Error("an explicit cancel should not report as DeadlineExceeded")
	}
}

func TestDeadlineIsAbsolute(t *testing.T) {
	at := time.Now().Add(20 * time.Millisecond)

	deadline, has, err := deadlineIsAbsolute(at)

	if !has {
		t.Fatal("WithDeadline should report a deadline")
	}
	if !deadline.Equal(at) {
		t.Errorf("deadline = %v, want %v", deadline, at)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Err() = %v, want DeadlineExceeded", err)
	}
}

func TestAPastDeadlineIsAlreadyExpired(t *testing.T) {
	immediate, err := aPastDeadlineIsAlreadyExpired()

	if !immediate {
		t.Error("a deadline in the past should produce an already-done context")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Err() = %v, want DeadlineExceeded", err)
	}
}

func TestRespectCancellation(t *testing.T) {
	tests := []struct {
		name          string
		timeout       time.Duration
		steps         int
		each          time.Duration
		wantErr       error
		wantCompleted int // -1: do not assert exactly
	}{
		{"finishes in time", time.Second, 3, time.Millisecond, nil, 3},
		{"cut short", 25 * time.Millisecond, 100, 5 * time.Millisecond, context.DeadlineExceeded, -1},
		{"zero steps", time.Second, 0, time.Millisecond, nil, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), tt.timeout)
			defer cancel()

			completed, err := respectCancellation(ctx, tt.steps, tt.each)

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if tt.wantCompleted >= 0 && completed != tt.wantCompleted {
				t.Errorf("completed = %d, want %d", completed, tt.wantCompleted)
			}
			if tt.wantCompleted < 0 && completed >= tt.steps {
				t.Errorf("completed %d of %d — should have been cut short", completed, tt.steps)
			}
		})
	}
}

// TestRespectCancellationReportsPartialProgress: the count matters, because a
// caller often wants to know how far it got.
func TestRespectCancellationReportsPartialProgress(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	completed, err := respectCancellation(ctx, 100, 5*time.Millisecond)

	if err == nil {
		t.Fatal("expected a deadline error")
	}
	if completed < 1 {
		t.Errorf("completed = %d, want at least 1 before the deadline", completed)
	}
	if completed >= 100 {
		t.Errorf("completed = %d, want fewer than 100", completed)
	}
}

func TestCheckBeforeStarting(t *testing.T) {
	t.Run("live context", func(t *testing.T) {
		if err := checkBeforeStarting(context.Background()); err != nil {
			t.Errorf("got %v, want nil", err)
		}
	})

	t.Run("cancelled context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		err := checkBeforeStarting(ctx)
		if !errors.Is(err, context.Canceled) {
			t.Errorf("got %v, want context.Canceled", err)
		}
	})

	t.Run("expired deadline", func(t *testing.T) {
		ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Hour))
		defer cancel()

		err := checkBeforeStarting(ctx)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("got %v, want DeadlineExceeded", err)
		}
	})
}
