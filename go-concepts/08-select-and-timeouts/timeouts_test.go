package main

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
)

func TestReceiveWithTimeout(t *testing.T) {
	t.Run("value arrives in time", func(t *testing.T) {
		withTimeout(t, 2*time.Second, func() {
			ch := make(chan int, 1)
			ch <- 7

			v, err := receiveWithTimeout(ch, time.Second)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if v != 7 {
				t.Errorf("v = %d, want 7", v)
			}
		})
	})

	t.Run("nothing arrives", func(t *testing.T) {
		withTimeout(t, 2*time.Second, func() {
			_, err := receiveWithTimeout(make(chan int), 20*time.Millisecond)

			if !errors.Is(err, ErrTimeout) {
				t.Errorf("err = %v, want ErrTimeout", err)
			}
		})
	})

	t.Run("the timeout is roughly honoured", func(t *testing.T) {
		withTimeout(t, 2*time.Second, func() {
			start := time.Now()
			_, _ = receiveWithTimeout(make(chan int), 50*time.Millisecond)
			elapsed := time.Since(start)

			if elapsed < 50*time.Millisecond {
				t.Errorf("returned after %v, before the 50ms timeout", elapsed)
			}
			// Generous upper bound: CI runners are shared and timers are not
			// precise. The assertion is "did not hang", not "was punctual".
			if elapsed > time.Second {
				t.Errorf("took %v for a 50ms timeout", elapsed)
			}
		})
	})
}

func TestReceiveWithTimerInLoop(t *testing.T) {
	t.Run("all values available", func(t *testing.T) {
		withTimeout(t, 2*time.Second, func() {
			ch := make(chan int, 3)
			ch <- 1
			ch <- 2
			ch <- 3

			got, err := receiveWithTimerInLoop(ch, time.Second, 3)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if want := []int{1, 2, 3}; !slices.Equal(got, want) {
				t.Errorf("got %v, want %v", got, want)
			}
		})
	})

	t.Run("times out partway", func(t *testing.T) {
		withTimeout(t, 2*time.Second, func() {
			ch := make(chan int, 1)
			ch <- 1

			got, err := receiveWithTimerInLoop(ch, 20*time.Millisecond, 3)

			if !errors.Is(err, ErrTimeout) {
				t.Fatalf("err = %v, want ErrTimeout", err)
			}
			if want := []int{1}; !slices.Equal(got, want) {
				t.Errorf("got %v, want %v — partial results should be returned", got, want)
			}
		})
	})

	t.Run("a closed channel ends the loop without error", func(t *testing.T) {
		withTimeout(t, 2*time.Second, func() {
			ch := make(chan int, 2)
			ch <- 1
			ch <- 2
			close(ch)

			got, err := receiveWithTimerInLoop(ch, time.Second, 5)
			if err != nil {
				t.Errorf("a closed stream is not a timeout: %v", err)
			}
			if want := []int{1, 2}; !slices.Equal(got, want) {
				t.Errorf("got %v, want %v", got, want)
			}
		})
	})

	// The timer must be reset per iteration, not cumulative. With a 30ms
	// timeout and values 10ms apart, five values take 50ms in total and must
	// all arrive: each individual wait is well under the timeout.
	t.Run("the timeout is per value, not for the whole loop", func(t *testing.T) {
		withTimeout(t, 5*time.Second, func() {
			ch := make(chan int)
			go func() {
				defer close(ch)
				for i := 0; i < 5; i++ {
					time.Sleep(10 * time.Millisecond)
					ch <- i
				}
			}()

			got, err := receiveWithTimerInLoop(ch, 100*time.Millisecond, 5)
			if err != nil {
				t.Fatalf("unexpected timeout: %v", err)
			}
			if len(got) != 5 {
				t.Errorf("got %d values, want 5 — Reset should refresh the window each iteration", len(got))
			}
		})
	})
}

// TestTimerAllocationsPerIteration is the measurement behind the README's
// advice. The assertion is a ratio, not an absolute number, because allocation
// sizes change between Go releases.
func TestTimerAllocationsPerIteration(t *testing.T) {
	if testing.Short() {
		t.Skip("allocation measurement")
	}

	const n = 20_000

	withAfter, withTimer := timerAllocationsPerIteration(n)

	t.Logf("%d iterations: time.After %d KB, one timer + Reset %d KB",
		n, withAfter/1024, withTimer/1024)

	if withTimer >= withAfter {
		t.Errorf("reusing one timer allocated %d bytes vs %d for time.After — expected far less",
			withTimer, withAfter)
	}
	// time.After allocates a timer and a channel per iteration, so the gap
	// should be large. 10x is a floor with plenty of slack.
	if withAfter < withTimer*10 {
		t.Errorf("expected time.After to allocate at least 10x more: %d vs %d", withAfter, withTimer)
	}
}

func TestContextIsUsuallyTheAnswer(t *testing.T) {
	t.Run("deadline fires", func(t *testing.T) {
		withTimeout(t, 2*time.Second, func() {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()

			_, err := contextIsUsuallyTheAnswer(ctx, make(chan int))

			if !errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("err = %v, want DeadlineExceeded", err)
			}
		})
	})

	t.Run("explicit cancellation", func(t *testing.T) {
		withTimeout(t, 2*time.Second, func() {
			ctx, cancel := context.WithCancel(context.Background())
			go func() {
				time.Sleep(10 * time.Millisecond)
				cancel()
			}()

			_, err := contextIsUsuallyTheAnswer(ctx, make(chan int))

			if !errors.Is(err, context.Canceled) {
				t.Errorf("err = %v, want Canceled", err)
			}
		})
	})

	t.Run("value beats the deadline", func(t *testing.T) {
		withTimeout(t, 2*time.Second, func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()

			ch := make(chan int, 1)
			ch <- 99

			v, err := contextIsUsuallyTheAnswer(ctx, ch)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if v != 99 {
				t.Errorf("v = %d, want 99", v)
			}
		})
	})
}

// TestDeadlineVsTimeout: a deadline bounds the TOTAL, which is the whole point.
// Ten 10ms steps cannot all fit in 40ms.
func TestDeadlineVsTimeout(t *testing.T) {
	withTimeout(t, 5*time.Second, func() {
		completed, err := deadlineVsTimeout(time.Now().Add(40*time.Millisecond), 10, 10*time.Millisecond)

		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("err = %v, want DeadlineExceeded", err)
		}
		if completed >= 10 {
			t.Errorf("completed %d of 10 steps — the deadline should have cut it short", completed)
		}
		if completed < 1 {
			t.Errorf("completed %d steps, want at least 1 before the deadline", completed)
		}
		t.Logf("completed %d of 10 steps before the 40ms deadline", completed)
	})
}

func TestTimeoutGuidanceIsDocumented(t *testing.T) {
	if got := timeoutGuidance(); len(got) < 4 {
		t.Errorf("expected at least 4 pieces of guidance, got %d", len(got))
	}
}
