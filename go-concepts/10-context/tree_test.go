package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCancellingAChildLeavesTheParent(t *testing.T) {
	parentDone, childDone := cancellingAChildLeavesTheParent()

	if parentDone {
		t.Error("cancelling a child must not cancel its parent")
	}
	if !childDone {
		t.Error("the child should be cancelled")
	}
}

func TestCancellingAParentCancelsEveryDescendant(t *testing.T) {
	for _, depth := range []int{1, 5, 20} {
		allCancelled, total := cancellingAParentCancelsEveryDescendant(depth)

		if !allCancelled {
			t.Errorf("depth %d: not every context in the chain was cancelled", depth)
		}
		if total != depth+1 {
			t.Errorf("depth %d: built %d contexts, want %d", depth, total, depth+1)
		}
	}
}

// TestAChildCanBeStricterNotLooser is the rule people try to route around.
func TestAChildCanBeStricterNotLooser(t *testing.T) {
	parent, stricter, looser := aChildCanBeStricterNotLooser()

	if stricter > parent {
		t.Errorf("stricter child has %v, parent has %v — it should be shorter", stricter, parent)
	}
	if looser > parent {
		t.Errorf("child asked for an hour and got %v, parent has %v — it must be capped", looser, parent)
	}
	// The looser child should land on the parent's deadline, not its own.
	if looser != parent {
		t.Errorf("looser child = %v, want the parent's %v", looser, parent)
	}
}

func TestRunRequest(t *testing.T) {
	t.Run("everything fits in the budget", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()

		results := runRequest(ctx, []operation{
			{Name: "a", Duration: time.Millisecond, Timeout: 100 * time.Millisecond},
			{Name: "b", Duration: time.Millisecond, Timeout: 100 * time.Millisecond},
		})

		for _, r := range results {
			if !r.Completed {
				t.Errorf("%s did not complete: %v", r.Name, r.Err)
			}
		}
	})

	t.Run("an operation exceeding its own timeout fails", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()

		results := runRequest(ctx, []operation{
			{Name: "slow", Duration: 100 * time.Millisecond, Timeout: 10 * time.Millisecond},
		})

		if results[0].Completed {
			t.Error("the slow operation should have timed out")
		}
		if !errors.Is(results[0].Err, context.DeadlineExceeded) {
			t.Errorf("err = %v, want DeadlineExceeded", results[0].Err)
		}
	})

	t.Run("the request budget stops later operations starting", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
		defer cancel()

		results := runRequest(ctx, []operation{
			{Name: "a", Duration: 10 * time.Millisecond, Timeout: time.Second},
			{Name: "b", Duration: 60 * time.Millisecond, Timeout: time.Second},
			{Name: "c", Duration: time.Millisecond, Timeout: time.Second},
		})

		if !results[0].Completed {
			t.Error("the first operation should have completed")
		}
		if results[2].Completed {
			t.Error("the last operation should not have started — the budget was gone")
		}
		if results[2].Err == nil {
			t.Error("the skipped operation should report why")
		}
	})
}

func TestConcurrentChildrenShareOneDeadline(t *testing.T) {
	t.Run("all cancelled when the work outlasts the deadline", func(t *testing.T) {
		completed, cancelled := concurrentChildrenShareOneDeadline(20, 25*time.Millisecond, 200*time.Millisecond)

		if completed != 0 {
			t.Errorf("%d goroutines completed, want 0", completed)
		}
		if cancelled != 20 {
			t.Errorf("%d goroutines cancelled, want 20", cancelled)
		}
	})

	t.Run("all complete when they fit", func(t *testing.T) {
		completed, cancelled := concurrentChildrenShareOneDeadline(20, time.Second, time.Millisecond)

		if completed != 20 {
			t.Errorf("%d completed, want 20", completed)
		}
		if cancelled != 0 {
			t.Errorf("%d cancelled, want 0", cancelled)
		}
	})
}

func TestIsDone(t *testing.T) {
	if isDone(context.Background()) {
		t.Error("Background should never be done")
	}

	ctx, cancel := context.WithCancel(context.Background())
	if isDone(ctx) {
		t.Error("a fresh context should not be done")
	}
	cancel()
	if !isDone(ctx) {
		t.Error("a cancelled context should be done")
	}
}
