// Package main is lesson 10 of go-concepts: context.
//
// A Context carries three things down a call tree:
//
//	cancellation  a channel that closes when the work should stop
//	a deadline    an absolute time after which it stops itself
//	values        request-scoped data, sparingly
//
// The interface is four methods, and you will implement none of them:
//
//	Done() <-chan struct{}
//	Err() error
//	Deadline() (time.Time, bool)
//	Value(key any) any
//
// Done returning a CHANNEL is the design. Cancellation composes with select,
// so it works alongside every other thing a goroutine can wait on, with no
// special support anywhere.
package main

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// backgroundAndTODO are the two roots. They are identical at runtime: never
// cancelled, no deadline, no values.
//
//	Background()  the root of a real tree: main, init, a request handler
//	TODO()        a placeholder meaning "a context belongs here and I have not
//	              wired it through yet"
//
// Use TODO deliberately. It is a marker for a reader and for static analysis,
// and choosing Background instead loses that signal for no benefit.
func backgroundAndTODO() (bgDone, todoDone bool, bgDeadline, todoDeadline bool) {
	bg := context.Background()
	todo := context.TODO()

	_, bgHasDeadline := bg.Deadline()
	_, todoHasDeadline := todo.Deadline()

	return bg.Done() == nil, todo.Done() == nil, bgHasDeadline, todoHasDeadline
}

// cancelClosesDone shows the mechanism. Done() is a channel; cancel closes it;
// every receiver is released at once. This is the close-as-broadcast pattern
// from lesson 07, wrapped in an interface.
func cancelClosesDone() (doneBefore, doneAfter bool, err error) {
	ctx, cancel := context.WithCancel(context.Background())

	select {
	case <-ctx.Done():
		doneBefore = true
	default:
	}

	cancel()

	select {
	case <-ctx.Done():
		doneAfter = true
	default:
	}

	return doneBefore, doneAfter, ctx.Err()
}

// errIsNilUntilCancelled: Err returns nil while the context is live, and a
// non-nil error forever afterwards. It never changes back.
func errIsNilUntilCancelled() (before, after error) {
	ctx, cancel := context.WithCancel(context.Background())

	before = ctx.Err()
	cancel()
	after = ctx.Err()

	return before, after
}

// cancelIsIdempotent: calling it twice, or a hundred times, is explicitly safe.
// That is what makes `defer cancel()` correct even when a branch already
// cancelled explicitly.
func cancelIsIdempotent() (panicked bool) {
	defer func() {
		if r := recover(); r != nil {
			panicked = true
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cancel()
	cancel()
	_ = ctx

	return false
}

// timeoutFiresOnItsOwn: WithTimeout cancels itself when the duration elapses,
// with Err reporting DeadlineExceeded rather than Canceled.
func timeoutFiresOnItsOwn(d time.Duration) (elapsed time.Duration, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()

	start := time.Now()
	<-ctx.Done()

	return time.Since(start), ctx.Err()
}

// deadlineIsAbsolute is the same thing expressed as a moment rather than a
// duration. Use it when several sequential steps must finish by one wall-clock
// time: giving each step its own timeout means the total is the sum.
func deadlineIsAbsolute(at time.Time) (deadline time.Time, hasDeadline bool, err error) {
	ctx, cancel := context.WithDeadline(context.Background(), at)
	defer cancel()

	deadline, hasDeadline = ctx.Deadline()
	<-ctx.Done()

	return deadline, hasDeadline, ctx.Err()
}

// aPastDeadlineIsAlreadyExpired: passing a time that has gone produces a
// context that is done immediately. No error, no panic, just an expired
// context, which is the correct behaviour and a surprise the first time.
func aPastDeadlineIsAlreadyExpired() (doneImmediately bool, err error) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Hour))
	defer cancel()

	select {
	case <-ctx.Done():
		doneImmediately = true
	default:
	}

	return doneImmediately, ctx.Err()
}

// respectCancellation is the shape every cancellable function has: select on
// ctx.Done() alongside the real work.
//
// Note the order of the returns. On cancellation it returns what it managed to
// do PLUS the error, because partial progress is usually worth reporting.
func respectCancellation(ctx context.Context, steps int, each time.Duration) (completed int, err error) {
	for i := 0; i < steps; i++ {
		select {
		case <-ctx.Done():
			return completed, fmt.Errorf("after %d of %d steps: %w", completed, steps, ctx.Err())
		case <-time.After(each):
			completed++
		}
	}

	return completed, nil
}

// checkBeforeStarting is worth doing at the top of any expensive function. If
// the context is already done, there is no point beginning.
//
// The idiom is a select with a default, not a call to Err(), because Err is
// documented to be consistent with Done and the select form is what composes
// with everything else.
func checkBeforeStarting(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return fmt.Errorf("not starting: %w", ctx.Err())
	default:
		return nil
	}
}

// demoBasics prints context mechanics.
func demoBasics() {
	bgDone, todoDone, bgDeadline, todoDeadline := backgroundAndTODO()
	fmt.Printf("  Background(): Done() is nil=%t, has deadline=%t\n", bgDone, bgDeadline)
	fmt.Printf("  TODO():       Done() is nil=%t, has deadline=%t   (identical at runtime)\n", todoDone, todoDeadline)

	before, after, err := cancelClosesDone()
	fmt.Printf("\n  Done() closed before cancel=%t, after cancel=%t, Err()=%v\n", before, after, err)

	errBefore, errAfter := errIsNilUntilCancelled()
	fmt.Printf("  Err() before cancel=%v, after=%v\n", errBefore, errAfter)
	fmt.Printf("  calling cancel three times panicked: %t\n", cancelIsIdempotent())

	elapsed, timeoutErr := timeoutFiresOnItsOwn(30 * time.Millisecond)
	fmt.Printf("\n  WithTimeout(30ms) fired after %v with %v\n", elapsed.Round(time.Millisecond), timeoutErr)
	fmt.Printf("  errors.Is(err, context.DeadlineExceeded): %t\n", errors.Is(timeoutErr, context.DeadlineExceeded))

	at := time.Now().Add(20 * time.Millisecond)
	deadline, has, derr := deadlineIsAbsolute(at)
	fmt.Printf("  WithDeadline: has deadline=%t at %s, fired with %v\n",
		has, deadline.Format("15:04:05.000"), derr)

	immediate, ierr := aPastDeadlineIsAlreadyExpired()
	fmt.Printf("  a deadline in the past is done immediately: %t (%v)\n", immediate, ierr)

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	completed, rerr := respectCancellation(ctx, 100, 5*time.Millisecond)
	fmt.Printf("\n  respectCancellation: %d of 100 steps, %v\n", completed, rerr)

	cancelled, cancelFn := context.WithCancel(context.Background())
	cancelFn()
	fmt.Printf("  checkBeforeStarting on a cancelled context: %v\n", checkBeforeStarting(cancelled))
}
