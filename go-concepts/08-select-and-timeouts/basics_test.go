package main

import (
	"testing"
	"time"
)

// withTimeout fails the test if fn has not returned within d. Every test here
// that touches a channel uses it: a select bug means "blocks forever", and a
// test that blocks forever reports nothing useful.
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
		t.Fatalf("timed out after %v — a select blocked", d)
	}
}

// TestRandomChoiceAmongReady checks the distribution is roughly even. The
// bounds are wide on purpose: this is a statistical property, and a tight
// assertion would produce a test that fails for no reason a few runs in ten
// thousand.
//
// With 10,000 rounds, a fair coin lands outside 45-55% with probability far
// below anything worth worrying about, while a select that always picked the
// first case would score 100% and fail loudly.
func TestRandomChoiceAmongReady(t *testing.T) {
	const rounds = 10_000

	first, second := randomChoiceAmongReady(rounds)

	if first+second != rounds {
		t.Fatalf("counts sum to %d, want %d", first+second, rounds)
	}

	pct := float64(first) / float64(rounds) * 100
	t.Logf("first case chosen %.1f%% of the time", pct)

	if pct < 45 || pct > 55 {
		t.Errorf("first case chosen %.1f%% of the time, want roughly 50%% — select must choose uniformly", pct)
	}
}

func TestDefaultMakesItNonBlocking(t *testing.T) {
	withTimeout(t, time.Second, func() {
		if !defaultMakesItNonBlocking() {
			t.Error("select with default on an empty channel should take the default")
		}
	})
}

func TestTryReceive(t *testing.T) {
	tests := []struct {
		name      string
		setup     func() chan int
		wantValue int
		wantOK    bool
	}{
		{"empty buffered", func() chan int { return make(chan int, 1) }, 0, false},
		{"unbuffered", func() chan int { return make(chan int) }, 0, false},
		{"has a value", func() chan int { ch := make(chan int, 1); ch <- 42; return ch }, 42, true},
		{"closed", func() chan int { ch := make(chan int); close(ch); return ch }, 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withTimeout(t, time.Second, func() {
				v, ok := tryReceive(tt.setup())
				if v != tt.wantValue || ok != tt.wantOK {
					t.Errorf("got %d, %t; want %d, %t", v, ok, tt.wantValue, tt.wantOK)
				}
			})
		})
	}
}

func TestTrySend(t *testing.T) {
	withTimeout(t, time.Second, func() {
		ch := make(chan int, 1)

		if !trySend(ch, 1) {
			t.Error("first send into an empty buffer should succeed")
		}
		if trySend(ch, 2) {
			t.Error("second send into a full buffer should fail rather than block")
		}

		<-ch
		if !trySend(ch, 3) {
			t.Error("send should succeed again once there is room")
		}
	})
}

func TestDropOnFullChannel(t *testing.T) {
	tests := []struct {
		capacity, attempts   int
		wantAccept, wantDrop int
	}{
		{10, 100, 10, 90},
		{0, 5, 0, 5},
		{100, 50, 50, 0},
		{1, 1, 1, 0},
	}

	for _, tt := range tests {
		withTimeout(t, time.Second, func() {
			accepted, dropped := dropOnFullChannel(tt.capacity, tt.attempts)

			if accepted != tt.wantAccept || dropped != tt.wantDrop {
				t.Errorf("cap %d, %d attempts: accepted %d dropped %d; want %d and %d",
					tt.capacity, tt.attempts, accepted, dropped, tt.wantAccept, tt.wantDrop)
			}
			if accepted+dropped != tt.attempts {
				t.Errorf("accepted+dropped = %d, want %d — every attempt must be accounted for",
					accepted+dropped, tt.attempts)
			}
		})
	}
}

func TestLatestValueWins(t *testing.T) {
	withTimeout(t, time.Second, func() {
		ch := make(chan int, 1)

		for i := 1; i <= 100; i++ {
			latestValueWins(ch, i)
		}

		if len(ch) != 1 {
			t.Fatalf("channel holds %d values, want exactly 1", len(ch))
		}
		if got := <-ch; got != 100 {
			t.Errorf("held value = %d, want 100 — the latest should win", got)
		}
	})
}

// TestSelectOnSendsToo is the fix for goroutine leak shape 1: a producer
// blocked on a send is released by a cancellation arm in the same select.
func TestSelectOnSendsToo(t *testing.T) {
	withTimeout(t, 2*time.Second, func() {
		sent, cancelled := selectOnSendsToo(1000)

		if !cancelled {
			t.Error("the producer should have exited via the cancellation case")
		}
		if sent >= 1000 {
			t.Errorf("sent %d values, want fewer than 1000 — nobody was receiving", sent)
		}
	})
}

func TestEmptySelectIsDocumented(t *testing.T) {
	if emptySelectBlocksForever() == "" {
		t.Error("select{} should carry an explanation")
	}
}
