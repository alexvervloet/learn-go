package main

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// The context tree
// ================
//
// Every With* call returns a CHILD of the context passed in. Cancelling a node
// cancels its whole subtree, and never its parent.
//
//	Background
//	    └── WithTimeout(30s)          the request
//	          ├── WithCancel          a goroutine
//	          └── WithTimeout(5s)     a database query
//
// Cancelling the 5s node stops the query alone. The 30s deadline firing stops
// everything below it. This is what makes a request deadline mean something:
// one value, passed down, and every layer honours it without coordinating.

// cancellingAChildLeavesTheParent proves cancellation flows one way.
func cancellingAChildLeavesTheParent() (parentDone, childDone bool) {
	parent, cancelParent := context.WithCancel(context.Background())
	defer cancelParent()

	child, cancelChild := context.WithCancel(parent)
	defer cancelChild()

	cancelChild()

	return isDone(parent), isDone(child)
}

// cancellingAParentCancelsEveryDescendant is the other direction, and the
// reason a request deadline works.
func cancellingAParentCancelsEveryDescendant(depth int) (allCancelled bool, depths int) {
	root, cancel := context.WithCancel(context.Background())

	contexts := []context.Context{root}
	current := root
	for i := 0; i < depth; i++ {
		child, childCancel := context.WithCancel(current)
		defer childCancel() //nolint:gocritic // a short-lived demo; each child needs its own cancel
		contexts = append(contexts, child)
		current = child
	}

	cancel() // cancel the root only

	// Cancellation propagates asynchronously, so give it a moment to settle
	// through the whole chain before checking.
	time.Sleep(10 * time.Millisecond)

	allCancelled = true
	for _, ctx := range contexts {
		if !isDone(ctx) {
			allCancelled = false
		}
	}

	return allCancelled, len(contexts)
}

// isDone reports whether a context has been cancelled, without blocking.
func isDone(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return true
	default:
		return false
	}
}

// aChildCanBeStricterNotLooser is the rule people try to work around. Asking
// for an hour under a parent that expires in 50ms gets you 50ms.
//
// There is no way to extend a deadline, by design: a library cannot override
// the caller's decision about how long it is prepared to wait. When you
// genuinely need work to outlive the request, detach it with
// context.WithoutCancel (see cause.go), which is explicit rather than sneaky.
func aChildCanBeStricterNotLooser() (parentDeadline, stricterDeadline, looserDeadline time.Duration) {
	parent, cancelParent := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancelParent()

	stricter, cancelStricter := context.WithTimeout(parent, 20*time.Millisecond)
	defer cancelStricter()

	looser, cancelLooser := context.WithTimeout(parent, time.Hour)
	defer cancelLooser()

	remaining := func(ctx context.Context) time.Duration {
		d, ok := ctx.Deadline()
		if !ok {
			return 0
		}
		return time.Until(d).Round(10 * time.Millisecond)
	}

	return remaining(parent), remaining(stricter), remaining(looser)
}

// requestWithSubOperations is what the tree looks like in a real handler: one
// request deadline, and per-operation deadlines beneath it that are tighter.
type operation struct {
	Name      string
	Duration  time.Duration
	Timeout   time.Duration
	Completed bool
	Err       error
}

// runRequest executes each operation under its own child context, all beneath
// the request's deadline. It stops at the first failure, because a request that
// has already blown its budget should not start the next call.
func runRequest(ctx context.Context, ops []operation) []operation {
	results := make([]operation, len(ops))

	for i, op := range ops {
		results[i] = op

		// Stop early if the REQUEST deadline has gone, regardless of what this
		// operation's own timeout would allow.
		if err := checkBeforeStarting(ctx); err != nil {
			results[i].Err = err
			continue
		}

		opCtx, cancel := context.WithTimeout(ctx, op.Timeout)

		select {
		case <-time.After(op.Duration):
			results[i].Completed = true
		case <-opCtx.Done():
			results[i].Err = fmt.Errorf("%s: %w", op.Name, opCtx.Err())
		}

		cancel() // explicitly, inside the loop: a defer here would pile up
	}

	return results
}

// concurrentChildrenShareOneDeadline: several goroutines under one context all
// stop together when it expires, with no coordination between them.
func concurrentChildrenShareOneDeadline(workers int, timeout, work time.Duration) (completed, cancelled int) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var (
		mu sync.Mutex
		wg sync.WaitGroup
	)

	for i := 0; i < workers; i++ {
		wg.Go(func() {
			select {
			case <-time.After(work):
				mu.Lock()
				completed++
				mu.Unlock()
			case <-ctx.Done():
				mu.Lock()
				cancelled++
				mu.Unlock()
			}
		})
	}
	wg.Wait()

	return completed, cancelled
}

// demoTree prints propagation behaviour.
func demoTree() {
	parentDone, childDone := cancellingAChildLeavesTheParent()
	fmt.Printf("  cancelling a child: parent done=%t, child done=%t\n", parentDone, childDone)

	allCancelled, depth := cancellingAParentCancelsEveryDescendant(10)
	fmt.Printf("  cancelling the root of a %d-deep chain: all cancelled=%t\n", depth, allCancelled)

	parent, stricter, looser := aChildCanBeStricterNotLooser()
	fmt.Printf("\n  parent deadline:            ~%v\n", parent)
	fmt.Printf("  child asking for 20ms:      ~%v  (stricter, honoured)\n", stricter)
	fmt.Printf("  child asking for one hour:  ~%v  (capped by the parent)\n", looser)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()

	results := runRequest(ctx, []operation{
		{Name: "auth", Duration: 10 * time.Millisecond, Timeout: 50 * time.Millisecond},
		{Name: "database", Duration: 20 * time.Millisecond, Timeout: 50 * time.Millisecond},
		{Name: "render", Duration: 80 * time.Millisecond, Timeout: 50 * time.Millisecond},
		{Name: "audit", Duration: 5 * time.Millisecond, Timeout: 50 * time.Millisecond},
	})

	fmt.Printf("\n  request with a 60ms budget:\n")
	for _, r := range results {
		switch {
		case r.Completed:
			fmt.Printf("    %-9s completed in %v\n", r.Name, r.Duration)
		default:
			fmt.Printf("    %-9s %v\n", r.Name, r.Err)
		}
	}

	completed, cancelled := concurrentChildrenShareOneDeadline(20, 30*time.Millisecond, 50*time.Millisecond)
	fmt.Printf("\n  20 goroutines, 50ms of work, 30ms deadline: %d completed, %d cancelled\n",
		completed, cancelled)
}
