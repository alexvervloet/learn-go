package main

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"time"
)

// Timeouts
// ========
//
// A timeout is a select with a time channel in it:
//
//	select {
//	case v := <-ch:              return v, nil
//	case <-time.After(timeout):  return 0, errTimeout
//	}
//
// Correct for a single wait. Inside a LOOP, time.After allocates a timer per
// iteration that cannot be stopped, and historically was not collected until it
// fired. A loop running 1,000/s with a 30s timeout accumulated 30,000 live
// timers.
//
// Go 1.23 changed this: unreachable timers are now collectable immediately, and
// Reset no longer requires draining the channel first. So the classic leak is
// largely historical. The NewTimer form is still better, still cheaper, and
// still what a reviewer expects to see in a loop.

// ErrTimeout is returned when an operation did not finish in time.
var ErrTimeout = errors.New("operation timed out")

// receiveWithTimeout is the simple, correct, single-use form.
func receiveWithTimeout(ch <-chan int, timeout time.Duration) (int, error) {
	select {
	case v := <-ch:
		return v, nil
	case <-time.After(timeout):
		return 0, ErrTimeout
	}
}

// receiveWithTimerInLoop is the loop form. One timer, reset per iteration,
// stopped on the way out.
//
// Two details:
//
//	defer timer.Stop()   releases it on every exit path, including a panic
//	timer.Reset(d)       before each select, not after, so the window is fresh
func receiveWithTimerInLoop(ch <-chan int, timeout time.Duration, want int) (received []int, err error) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	for len(received) < want {
		timer.Reset(timeout)

		select {
		case v, ok := <-ch:
			if !ok {
				return received, nil // the stream ended early, not an error
			}
			received = append(received, v)
		case <-timer.C:
			return received, fmt.Errorf("after %d of %d values: %w", len(received), want, ErrTimeout)
		}
	}

	return received, nil
}

// timerAllocationsPerIteration measures the difference the loop form makes. It
// returns bytes allocated by each approach over n iterations, which the test
// asserts on and the demo prints.
func timerAllocationsPerIteration(n int) (withAfter, withTimer uint64) {
	ch := make(chan int, 1)

	measure := func(fn func()) uint64 {
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)

		fn()

		runtime.GC()
		runtime.ReadMemStats(&after)
		return after.TotalAlloc - before.TotalAlloc
	}

	withAfter = measure(func() {
		for i := 0; i < n; i++ {
			ch <- i
			select {
			case <-ch:
			case <-time.After(time.Hour): // a new timer every iteration
			}
		}
	})

	withTimer = measure(func() {
		timer := time.NewTimer(time.Hour)
		defer timer.Stop()

		for i := 0; i < n; i++ {
			timer.Reset(time.Hour)
			ch <- i
			select {
			case <-ch:
			case <-timer.C:
			}
		}
	})

	return withAfter, withTimer
}

// contextIsUsuallyTheAnswer: a raw timeout stops at the function that declared
// it. A context deadline propagates, so everything downstream (an HTTP request,
// a database query, another goroutine) learns about it too and stops as well.
//
// This is the difference between "my function returned after 5 seconds" and
// "the work actually stopped after 5 seconds". Lesson 10 is about nothing else.
func contextIsUsuallyTheAnswer(ctx context.Context, ch <-chan int) (int, error) {
	select {
	case v := <-ch:
		return v, nil
	case <-ctx.Done():
		// ctx.Err() is context.Canceled or context.DeadlineExceeded, and
		// wrapping it keeps errors.Is working for the caller.
		return 0, fmt.Errorf("waiting for a value: %w", ctx.Err())
	}
}

// deadlineVsTimeout: a timeout is relative, a deadline absolute. Across several
// sequential calls that must finish by a wall-clock moment, the deadline is the
// right one: giving each call its own 5-second timeout means the total can be
// 5 seconds times the number of calls.
func deadlineVsTimeout(deadline time.Time, steps int, each time.Duration) (completed int, err error) {
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()

	for i := 0; i < steps; i++ {
		select {
		case <-time.After(each):
			completed++
		case <-ctx.Done():
			return completed, fmt.Errorf("step %d: %w", i, ctx.Err())
		}
	}

	return completed, nil
}

// timeoutGuidance is the summary worth keeping.
func timeoutGuidance() []string {
	return []string{
		"single wait:              time.After is fine",
		"inside a loop:            time.NewTimer + Reset + defer Stop",
		"work that calls outward:  context.WithTimeout, so it propagates",
		"a wall-clock deadline:    context.WithDeadline, not N separate timeouts",
		"Go 1.23+:                 unstopped timers are collectable; the old leak is mostly history",
	}
}

// demoTimeouts prints timeout behaviour and the allocation difference.
func demoTimeouts() {
	ch := make(chan int, 1)
	ch <- 7
	v, err := receiveWithTimeout(ch, 50*time.Millisecond)
	fmt.Printf("  receiveWithTimeout, value ready:  v=%d err=%v\n", v, err)

	_, err = receiveWithTimeout(make(chan int), 20*time.Millisecond)
	fmt.Printf("  receiveWithTimeout, nothing sent: err=%v\n", err)

	feed := make(chan int, 3)
	feed <- 1
	feed <- 2
	feed <- 3
	got, err := receiveWithTimerInLoop(feed, 50*time.Millisecond, 3)
	fmt.Printf("  receiveWithTimerInLoop, 3 available: %v err=%v\n", got, err)

	slow := make(chan int, 1)
	slow <- 1
	got, err = receiveWithTimerInLoop(slow, 20*time.Millisecond, 3)
	fmt.Printf("  receiveWithTimerInLoop, only 1 available: %v\n    err=%v\n", got, err)

	withAfter, withTimer := timerAllocationsPerIteration(20_000)
	fmt.Printf("\n  20,000 iterations:\n")
	fmt.Printf("    time.After per iteration: %6d KB allocated\n", withAfter/1024)
	fmt.Printf("    one timer, Reset:         %6d KB allocated\n", withTimer/1024)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err = contextIsUsuallyTheAnswer(ctx, make(chan int))
	fmt.Printf("\n  context deadline: %v (errors.Is DeadlineExceeded: %t)\n",
		err, errors.Is(err, context.DeadlineExceeded))

	completed, err := deadlineVsTimeout(time.Now().Add(40*time.Millisecond), 10, 10*time.Millisecond)
	fmt.Printf("  10 steps of 10ms against a 40ms deadline: %d completed, %v\n", completed, err)

	fmt.Println("\n  which timeout to reach for:")
	for _, g := range timeoutGuidance() {
		fmt.Printf("    %s\n", g)
	}
}
