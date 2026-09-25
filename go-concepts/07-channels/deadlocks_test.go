package main

import (
	"testing"
	"time"
)

// TestDeadlockShapesAllBlock runs every shape in the catalogue and confirms it
// does what the catalogue says. Each is given a short window; none should
// complete.
//
// These tests deliberately leak a goroutine each. That is what a deadlocked
// goroutine is, and cleaning them up would mean they were not deadlocked. The
// count is bounded and the test binary exits shortly after.
func TestDeadlockShapesAllBlock(t *testing.T) {
	for _, shape := range deadlockShapes() {
		t.Run(shape.name, func(t *testing.T) {
			if wouldBlockForever(shape.try, 50*time.Millisecond) {
				t.Errorf("%s completed, but it should block forever", shape.name)
			}
			if shape.why == "" {
				t.Error("every shape should document why it blocks")
			}
		})
	}
}

func TestDeadlockCatalogueIsComplete(t *testing.T) {
	shapes := deadlockShapes()

	if len(shapes) < 6 {
		t.Errorf("catalogue has %d shapes, want at least 6", len(shapes))
	}

	seen := make(map[string]struct{}, len(shapes))
	for _, s := range shapes {
		if _, dup := seen[s.name]; dup {
			t.Errorf("duplicate shape %q", s.name)
		}
		seen[s.name] = struct{}{}
	}
}

// TestNilChannelBlocksForever is the property lesson 08 depends on: a nil
// channel in a select disables that arm.
func TestNilChannelBlocksForever(t *testing.T) {
	send, receive := nilChannelBlocksForever()

	if send {
		t.Error("a send on a nil channel should never complete")
	}
	if receive {
		t.Error("a receive on a nil channel should never complete")
	}
}

// TestWouldBlockForeverDetectsCompletion is the negative control. Without it,
// a broken wouldBlockForever that always returned false would make every test
// above pass for the wrong reason.
func TestWouldBlockForeverDetectsCompletion(t *testing.T) {
	if !wouldBlockForever(func() {}, 500*time.Millisecond) {
		t.Error("a function that returns immediately should be reported as completed")
	}

	if !wouldBlockForever(func() {
		ch := make(chan int, 1)
		ch <- 1
		<-ch
	}, 500*time.Millisecond) {
		t.Error("a buffered send and receive should complete")
	}
}

func TestDetectionLimitsAreDocumented(t *testing.T) {
	if got := detectionIsAllOrNothing(); len(got) < 3 {
		t.Errorf("expected at least 3 documented limits, got %d", len(got))
	}
}
