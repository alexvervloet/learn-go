package main

import (
	"slices"
	"testing"
	"time"
)

// withTimeout runs fn and fails the test if it has not returned within d.
//
// Every test in this package that touches a channel goes through this. A
// channel bug usually manifests as "blocks forever", and a test that blocks
// forever reports nothing useful: it burns the CI job's wall clock and prints a
// panic from the test binary's own watchdog. Failing at a known point with a
// named message is far more useful.
func withTimeout(t *testing.T, d time.Duration, fn func()) {
	t.Helper()

	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()

	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("timed out after %v — a channel operation blocked", d)
	}
}

// TestUnbufferedIsAHandshake asserts the ordering the MEMORY MODEL guarantees,
// which is narrower than the ordering the demo happens to print.
//
// Guaranteed: the receive completes before the send returns. So
// "receiver: received" always precedes "sender: sent".
//
// NOT guaranteed: which of the two "about to" lines is recorded first. Those
// happen in different goroutines with nothing ordering them, and the 20ms sleep
// in the receiver only makes the sender likely to win, not certain. CI caught
// this: under -race on a loaded runner, main was descheduled past the sleep and
// the receiver logged first.
//
// Asserting the full four-line sequence was asserting a race. This asserts the
// guarantee instead.
func TestUnbufferedIsAHandshake(t *testing.T) {
	withTimeout(t, 2*time.Second, func() {
		got := unbufferedIsAHandshake()

		if len(got) != 4 {
			t.Fatalf("got %d events, want 4: %v", len(got), got)
		}

		indexOf := func(event string) int {
			return slices.Index(got, event)
		}

		received := indexOf("receiver: received")
		sent := indexOf("sender: sent")
		aboutToSend := indexOf("sender: about to send")
		aboutToReceive := indexOf("receiver: about to receive")

		for name, i := range map[string]int{
			"sender: about to send":      aboutToSend,
			"receiver: about to receive": aboutToReceive,
			"receiver: received":         received,
			"sender: sent":               sent,
		} {
			if i < 0 {
				t.Fatalf("event %q missing from %v", name, got)
			}
		}

		// THE guarantee: an unbuffered send does not return until a receiver
		// has taken the value.
		if received > sent {
			t.Errorf("receive logged after send returned, which an unbuffered channel forbids:\n  %v", got)
		}

		// Each goroutine's own events are ordered within that goroutine.
		if aboutToSend > sent {
			t.Errorf("sender's events out of order: %v", got)
		}
		if aboutToReceive > received {
			t.Errorf("receiver's events out of order: %v", got)
		}
	})
}

func TestBufferedDoesNotBlockUntilFull(t *testing.T) {
	tests := []struct {
		name        string
		capacity    int
		sends       int
		wantLengths []int
		wantBlocked bool
	}{
		{"within capacity", 3, 3, []int{1, 2, 3}, false},
		{"one over", 3, 4, []int{1, 2, 3}, true},
		{"unbuffered blocks at once", 0, 1, nil, true},
		{"capacity one", 1, 2, []int{1}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withTimeout(t, 2*time.Second, func() {
				lengths, blocked := bufferedDoesNotBlockUntilFull(tt.capacity, tt.sends)

				if !slices.Equal(lengths, tt.wantLengths) {
					t.Errorf("lengths = %v, want %v", lengths, tt.wantLengths)
				}
				if blocked != tt.wantBlocked {
					t.Errorf("blocked = %t, want %t", blocked, tt.wantBlocked)
				}
			})
		})
	}
}

func TestLenAndCap(t *testing.T) {
	ul, uc, bl, bc := lenAndCap()

	if ul != 0 || uc != 0 {
		t.Errorf("unbuffered len/cap = %d/%d, want 0/0", ul, uc)
	}
	if bl != 2 {
		t.Errorf("buffered len = %d, want 2", bl)
	}
	if bc != 5 {
		t.Errorf("buffered cap = %d, want 5", bc)
	}
}

func TestUnbufferedOperationsBlock(t *testing.T) {
	withTimeout(t, 2*time.Second, func() {
		if sendBlocksWithoutAReceiver() {
			t.Error("an unbuffered send should not complete without a receiver")
		}
		if receiveBlocksWithoutASender() {
			t.Error("an unbuffered receive should not complete without a sender")
		}
	})
}

func TestBufferSizeGuidanceIsDocumented(t *testing.T) {
	guidance := bufferSizeIsADesignDecision()

	for _, size := range []int{0, 1, 8, 100_000} {
		if guidance[size] == "" {
			t.Errorf("no guidance documented for buffer size %d", size)
		}
	}
}

// BenchmarkChannelSendReceive compares the three options for moving one int
// between goroutines. Run:
//
//	go test -bench BenchmarkChannel -benchmem -run '^$' ./07-channels
func BenchmarkUnbufferedRoundTrip(b *testing.B) {
	ch := make(chan int)
	done := make(chan struct{})

	go func() {
		defer close(done)
		for range ch {
		}
	}()

	for b.Loop() {
		ch <- 1
	}

	close(ch)
	<-done
}

func BenchmarkBufferedRoundTrip(b *testing.B) {
	ch := make(chan int, 128)
	done := make(chan struct{})

	go func() {
		defer close(done)
		for range ch {
		}
	}()

	for b.Loop() {
		ch <- 1
	}

	close(ch)
	<-done
}
