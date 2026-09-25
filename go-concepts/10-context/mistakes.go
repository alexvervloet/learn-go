package main

import (
	"context"
	"fmt"
	"runtime"
	"time"
)

// The six context mistakes
// ========================
//
// Each one below is paired with its fix. Several are caught by tooling, and
// where they are, the tool is named: that is worth more than remembering the
// rule.

// Mistake 1: forgetting cancel
// ----------------------------
//
// Every WithCancel, WithTimeout and WithDeadline returns a cancel function.
// Not calling it leaks the context and, for the timer variants, its timer,
// until the PARENT is cancelled. With a Background parent, that is never.
//
// go vet's lostcancel catches the straightforward cases:
//
//	the cancel function is not used on all paths (possible context leak)
//
// It does not catch a cancel stored in a struct field, or passed to another
// function, so the habit still matters.

// leakingContexts creates n contexts and never cancels them. The goroutine
// count is the visible symptom: each WithTimeout under a cancellable parent
// spawns a propagation goroutine.
//
// GO VET CATCHES THE OBVIOUS FORM. Writing the body as:
//
//	_, _ = context.WithTimeout(parent, time.Hour)
//
// fails the build with:
//
//	the cancel function returned by context.WithTimeout should be called,
//	not discarded, to avoid a context leak
//
// So the cancels below are collected into a slice and dropped on the floor,
// purely so this function can still demonstrate the leak. That is also exactly
// what lostcancel CANNOT see: once the cancel function is stored somewhere, the
// analysis gives up. The habit still matters, because the check only covers the
// shape people rarely write by accident.
func leakingContexts(n int) (parent context.Context, cancelParent context.CancelFunc) {
	parent, cancelParent = context.WithCancel(context.Background())

	cancels := make([]context.CancelFunc, 0, n)
	for i := 0; i < n; i++ {
		_, cancel := context.WithTimeout(parent, time.Hour)
		cancels = append(cancels, cancel) // collected, and never called
	}
	_ = cancels

	return parent, cancelParent
}

// notLeakingContexts does the same work correctly.
func notLeakingContexts(n int) {
	parent, cancelParent := context.WithCancel(context.Background())
	defer cancelParent()

	for i := 0; i < n; i++ {
		_, cancel := context.WithTimeout(parent, time.Hour)
		cancel() // explicitly, inside the loop: a defer here piles up until the function returns
	}
}

// retainedBytes measures what a leaked context actually costs, which is NOT
// goroutines.
//
// An earlier version of this file measured runtime.NumGoroutine and reported
// "+0", which was correct and useless. Modern Go does not spawn a goroutine per
// derived context: a child registers itself in its parent's children map, and a
// timer goes on the runtime's timer heap. Cancelling removes it from the map.
// NOT cancelling leaves it there for as long as the parent lives.
//
// So the leak is retained HEAP, held by a live parent, and the right instrument
// is MemStats after a forced GC.
func retainedBytes(fn func() any) uint64 {
	var before, after runtime.MemStats

	runtime.GC()
	runtime.ReadMemStats(&before)

	keep := fn()

	runtime.GC()
	runtime.ReadMemStats(&after)

	// keep is still reachable here, so anything it holds is still counted.
	runtime.KeepAlive(keep)

	if after.HeapAlloc < before.HeapAlloc {
		return 0
	}
	return after.HeapAlloc - before.HeapAlloc
}

// Mistake 2: storing a Context in a struct
// ----------------------------------------
//
// A context is scoped to ONE call. Stored in a struct it outlives that scope,
// and the next method call uses a context that was cancelled minutes ago.

// badClient stores a context, which means every call shares one lifetime.
type badClient struct {
	ctx context.Context //nolint:containedctx // the anti-pattern is the point
}

func newBadClient(ctx context.Context) *badClient { return &badClient{ctx: ctx} }

// Fetch uses the STORED context, so once it is cancelled the client is dead
// forever, whatever the current caller wanted.
func (c *badClient) Fetch(id int) error {
	select {
	case <-c.ctx.Done():
		return fmt.Errorf("fetch %d: %w", id, c.ctx.Err())
	case <-time.After(time.Millisecond):
		return nil
	}
}

// goodClient takes the context per call, which is the rule.
type goodClient struct{}

func newGoodClient() *goodClient { return &goodClient{} }

// Fetch takes ctx as its first parameter, so each call carries its own
// lifetime and the client is reusable.
func (c *goodClient) Fetch(ctx context.Context, id int) error {
	select {
	case <-ctx.Done():
		return fmt.Errorf("fetch %d: %w", id, ctx.Err())
	case <-time.After(time.Millisecond):
		return nil
	}
}

// storedContextPoisonsLaterCalls demonstrates it.
func storedContextPoisonsLaterCalls() (badFirst, badSecond, goodSecond error) {
	ctx, cancel := context.WithCancel(context.Background())

	bad := newBadClient(ctx)
	good := newGoodClient()

	badFirst = bad.Fetch(1)

	cancel() // the first request finishes and its context is cancelled

	badSecond = bad.Fetch(2)                         // poisoned
	goodSecond = good.Fetch(context.Background(), 2) // fine

	return badFirst, badSecond, goodSecond
}

// Mistake 3: a nil Context
// ------------------------
//
// Passing nil compiles and panics on first use. context.TODO() is the
// placeholder for "a context belongs here and I have not wired it yet".

// nilContextPanics shows the failure. The recover keeps the demo alive.
func nilContextPanics() (message string) {
	defer func() {
		if r := recover(); r != nil {
			message = fmt.Sprint(r)
		}
	}()

	var ctx context.Context // nil interface

	// Routed through a variable for the same reason as above: the direct
	// `_, _ = context.WithCancel(ctx)` form does not get past go vet.
	_, cancel := context.WithCancel(ctx)
	_ = cancel

	return ""
}

// todoIsTheePlaceholder is the fix.
func todoIsThePlaceholder() (works bool) {
	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()

	return ctx != nil
}

// Mistake 4: checking Err() instead of selecting on Done()
// --------------------------------------------------------
//
// Polling Err() in a loop burns CPU and only notices cancellation at the next
// iteration. Selecting on Done() parks the goroutine until there is something
// to do.

// pollingWastesCPU checks Err repeatedly.
func pollingWastesCPU(ctx context.Context, work time.Duration) (checks int) {
	deadline := time.Now().Add(work)

	for time.Now().Before(deadline) {
		checks++
		if ctx.Err() != nil {
			return checks
		}
	}
	return checks
}

// selectingParksTheGoroutine is the fix: zero CPU while waiting, and it
// notices immediately.
func selectingParksTheGoroutine(ctx context.Context, work time.Duration) (cancelled bool) {
	select {
	case <-time.After(work):
		return false
	case <-ctx.Done():
		return true
	}
}

// Mistake 5: using a context value for a required argument
// --------------------------------------------------------
//
// If a function cannot work without a thing, that thing is a parameter. A
// context value is untyped, invisible in the signature, and absent at runtime
// with no compile error.

// badCharge takes its amount from the context, so the signature is a lie and
// a caller that forgets gets a zero charge rather than a build failure.
type amountKey struct{}

func badCharge(ctx context.Context) (charged int, err error) {
	amount, ok := ctx.Value(amountKey{}).(int)
	if !ok {
		return 0, fmt.Errorf("charge: no amount in context")
	}
	return amount, nil
}

// goodCharge takes it as a parameter. Forgetting it does not compile.
func goodCharge(ctx context.Context, amount int) (charged int, err error) {
	if err := checkBeforeStarting(ctx); err != nil {
		return 0, err
	}
	return amount, nil
}

// Mistake 6: deriving from the wrong parent
// -----------------------------------------
//
// Deriving from Background inside a request throws away the caller's deadline
// and cancellation. The work then outlives the request, which is a leak
// dressed up as a feature.

// derivedFromBackground ignores the caller entirely.
func derivedFromBackground(requestCtx context.Context, work time.Duration) (completedAnyway bool) {
	// WRONG: requestCtx is discarded.
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	_ = requestCtx

	select {
	case <-time.After(work):
		return true
	case <-ctx.Done():
		return false
	}
}

// derivedFromTheRequest honours it.
func derivedFromTheRequest(requestCtx context.Context, work time.Duration) (completed bool) {
	ctx, cancel := context.WithTimeout(requestCtx, time.Minute)
	defer cancel()

	select {
	case <-time.After(work):
		return true
	case <-ctx.Done():
		return false
	}
}

// demoMistakes prints each mistake with its fix.
func demoMistakes() {
	const n = 20_000

	leaked := retainedBytes(func() any {
		parent, cancel := leakingContexts(n)
		_ = cancel // deliberately not called
		return parent
	})
	clean := retainedBytes(func() any {
		notLeakingContexts(n)
		return nil
	})

	fmt.Printf("  1. forgetting cancel, %d derived contexts:\n", n)
	fmt.Printf("     never cancelled: %6d KB still reachable from the live parent\n", leaked/1024)
	fmt.Printf("     cancelled:       %6d KB\n", clean/1024)
	fmt.Println("     it is retained HEAP, not goroutines: a child stays in its parent's children map")
	fmt.Println("     go vet's lostcancel catches the straightforward cases")

	badFirst, badSecond, goodSecond := storedContextPoisonsLaterCalls()
	fmt.Printf("\n  2. storing a context in a struct:\n")
	fmt.Printf("     stored, first call:  %v\n", badFirst)
	fmt.Printf("     stored, second call: %v\n", badSecond)
	fmt.Printf("     per-call, second:    %v\n", goodSecond)

	fmt.Printf("\n  3. a nil context: panic: %s\n", nilContextPanics())
	fmt.Printf("     context.TODO() works: %t\n", todoIsThePlaceholder())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	checks := pollingWastesCPU(ctx, 5*time.Millisecond)
	fmt.Printf("\n  4. polling Err() made %d checks before noticing\n", checks)
	fmt.Printf("     selecting on Done() noticed immediately: %t\n",
		selectingParksTheGoroutine(ctx, time.Second))

	_, err := badCharge(context.Background())
	fmt.Printf("\n  5. a required value in the context: %v\n", err)
	amount, _ := goodCharge(context.Background(), 100)
	fmt.Printf("     as a parameter: charged %d, and omitting it would not compile\n", amount)

	cancelled, cancelFn := context.WithCancel(context.Background())
	cancelFn()
	fmt.Printf("\n  6. deriving from Background inside a cancelled request: completed anyway=%t\n",
		derivedFromBackground(cancelled, 20*time.Millisecond))
	fmt.Printf("     deriving from the request context: completed=%t\n",
		derivedFromTheRequest(cancelled, 20*time.Millisecond))
}
