package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Cause, detaching, and AfterFunc
// ===============================
//
// ctx.Err() tells you WHAT happened: Canceled or DeadlineExceeded. It never
// tells you why, which is the difference between a useful log line and an hour
// of searching.
//
// Go 1.20 and 1.21 added four things worth knowing:
//
//	WithCancelCause     cancel(err), and context.Cause(ctx) returns it
//	WithDeadlineCause   the same for a deadline
//	WithoutCancel       a child that does NOT inherit cancellation
//	AfterFunc           run f when ctx is done, without parking a goroutine

// Errors a request can be abandoned for. Cancellation with a cause turns
// "context canceled" in a log into one of these.
var (
	ErrUpstreamFailed = errors.New("upstream returned 503")
	ErrClientGone     = errors.New("client disconnected")
	ErrBudgetExceeded = errors.New("request cost budget exceeded")
)

// cancelWithCause records why. Err() still reports Canceled, because the
// context's state genuinely is cancelled; Cause carries the reason alongside.
func cancelWithCause(reason error) (err error, cause error) {
	ctx, cancel := context.WithCancelCause(context.Background())

	cancel(reason)

	return ctx.Err(), context.Cause(ctx)
}

// causeWithoutACause: cancelling with nil, or using plain WithCancel, makes
// Cause return the same thing as Err. So Cause is always safe to call and
// never less informative.
func causeWithoutACause() (plainErr, plainCause, nilErr, nilCause error) {
	plain, cancelPlain := context.WithCancel(context.Background())
	cancelPlain()

	withNil, cancelNil := context.WithCancelCause(context.Background())
	cancelNil(nil)

	return plain.Err(), context.Cause(plain), withNil.Err(), context.Cause(withNil)
}

// causePropagatesDownTheTree: a child cancelled by its parent reports the
// parent's cause, so a goroutine four levels down learns the real reason.
func causePropagatesDownTheTree() (childErr, childCause error) {
	parent, cancel := context.WithCancelCause(context.Background())

	child, cancelChild := context.WithCancel(parent)
	defer cancelChild()

	grandchild, cancelGrandchild := context.WithCancel(child)
	defer cancelGrandchild()

	cancel(ErrUpstreamFailed)

	// Propagation is asynchronous; wait for it to arrive.
	<-grandchild.Done()

	return grandchild.Err(), context.Cause(grandchild)
}

// deadlineCause names why a deadline exists, which is more useful than
// "context deadline exceeded" when a service has five different budgets.
func deadlineCause(d time.Duration, reason error) (err, cause error) {
	ctx, cancel := context.WithDeadlineCause(context.Background(), time.Now().Add(d), reason)
	defer cancel()

	<-ctx.Done()

	return ctx.Err(), context.Cause(ctx)
}

// WithoutCancel
// -------------
//
// Some work must finish even though the request is over: writing an audit
// record, releasing a lock, emitting a metric. Doing it with the request's
// context means it is cancelled exactly when it matters most.
//
// WithoutCancel returns a context that keeps the VALUES and drops the
// cancellation and deadline. So the trace ID still flows and the work survives.
//
// Use it deliberately and rarely. Detached work has no deadline of its own
// unless you give it one, which is how a "cleanup" goroutine becomes a leak.

// auditLog is work that must not be cancelled with the request.
func auditLog(ctx context.Context, event string, d time.Duration) (completed bool, err error) {
	select {
	case <-time.After(d):
		_ = event
		return true, nil
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

// handlerWithoutDetaching cancels its own audit write, which is the bug.
func handlerWithoutDetaching(ctx context.Context, auditDuration time.Duration) (audited bool, err error) {
	return auditLog(ctx, "request.completed", auditDuration)
}

// handlerWithDetaching keeps the values and drops the cancellation. Note that
// it adds its OWN timeout: detached does not mean unbounded.
func handlerWithDetaching(ctx context.Context, auditDuration time.Duration) (audited bool, err error) {
	detached := context.WithoutCancel(ctx)

	// A fresh budget, so a detached write cannot hang forever.
	auditCtx, cancel := context.WithTimeout(detached, time.Second)
	defer cancel()

	return auditLog(auditCtx, "request.completed", auditDuration)
}

// AfterFunc
// ---------
//
// Before Go 1.21, reacting to cancellation meant parking a goroutine:
//
//	go func() { <-ctx.Done(); cleanup() }()
//
// which costs a goroutine per context for the whole lifetime. AfterFunc runs f
// in its own goroutine only when the context is actually done, and returns a
// stop function so you can cancel the registration.

// cleanupOnCancel registers work for when ctx ends, and returns a stop function.
func cleanupOnCancel(ctx context.Context, cleanup func()) (stop func() bool) {
	return context.AfterFunc(ctx, cleanup)
}

// afterFuncRunsOnCancel demonstrates it firing.
func afterFuncRunsOnCancel() (ran bool) {
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	context.AfterFunc(ctx, func() { close(done) })

	cancel()
	<-done

	return true
}

// stopPreventsIt: the returned function deregisters. It reports whether it
// stopped the call before it ran, which distinguishes "cancelled in time" from
// "too late, it already fired".
func stopPreventsIt() (stoppedInTime bool, ranAnyway bool) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	ran := false

	stop := context.AfterFunc(ctx, func() {
		mu.Lock()
		defer mu.Unlock()
		ran = true
	})

	stoppedInTime = stop() // before any cancellation

	cancel()
	time.Sleep(10 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	return stoppedInTime, ran
}

// afterFuncOnAnAlreadyDoneContext runs f immediately, in a new goroutine. It
// does not skip it, which is the behaviour you want for cleanup.
func afterFuncOnAnAlreadyDoneContext() (ran bool) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan struct{})
	context.AfterFunc(ctx, func() { close(done) })

	select {
	case <-done:
		return true
	case <-time.After(time.Second):
		return false
	}
}

// demoCause prints cause, detaching and AfterFunc.
func demoCause() {
	err, cause := cancelWithCause(ErrUpstreamFailed)
	fmt.Printf("  cancel(ErrUpstreamFailed):\n")
	fmt.Printf("    ctx.Err()          = %v\n", err)
	fmt.Printf("    context.Cause(ctx) = %v\n", cause)

	plainErr, plainCause, nilErr, nilCause := causeWithoutACause()
	fmt.Printf("  plain WithCancel:   Err=%v Cause=%v\n", plainErr, plainCause)
	fmt.Printf("  cancel(nil):        Err=%v Cause=%v\n", nilErr, nilCause)

	childErr, childCause := causePropagatesDownTheTree()
	fmt.Printf("  a grandchild sees:  Err=%v Cause=%v\n", childErr, childCause)

	dErr, dCause := deadlineCause(20*time.Millisecond, ErrBudgetExceeded)
	fmt.Printf("  WithDeadlineCause:  Err=%v Cause=%v\n", dErr, dCause)

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	audited, aerr := handlerWithoutDetaching(cancelled, 10*time.Millisecond)
	fmt.Printf("\n  audit with the request context: written=%t err=%v\n", audited, aerr)

	audited, aerr = handlerWithDetaching(cancelled, 10*time.Millisecond)
	fmt.Printf("  audit with WithoutCancel:       written=%t err=%v\n", audited, aerr)

	fmt.Printf("\n  AfterFunc ran on cancel: %t\n", afterFuncRunsOnCancel())
	stopped, ranAnyway := stopPreventsIt()
	fmt.Printf("  stop() prevented it: stopped=%t ran anyway=%t\n", stopped, ranAnyway)
	fmt.Printf("  AfterFunc on an already-cancelled context still runs: %t\n",
		afterFuncOnAnAlreadyDoneContext())
}
