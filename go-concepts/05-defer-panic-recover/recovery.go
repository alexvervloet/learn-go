package main

import (
	"fmt"
	"sync"
	"time"
)

// Where recover works, and where it silently does not
// ===================================================
//
// recover() returns the panic value ONLY when called directly inside a function
// that was deferred by the frame currently unwinding. Every other placement
// compiles fine and does nothing.
//
//	defer func() { recover() }()          WORKS
//	defer func() { helper() }()           NO  (recover is inside helper's frame)
//	defer recover()                       NO  (recover's own frame, not the defer's)
//	if r := recover(); ...                NO  (not deferred at all)
//	go func() { ... }()                   NO  for panics in OTHER goroutines
//
// The compiler warns about exactly none of these.

// recoverInDeferredClosure is the correct placement.
func recoverInDeferredClosure() (recovered bool) {
	defer func() {
		if r := recover(); r != nil {
			recovered = true
		}
	}()

	panic("boom")
}

// recoverViaHelperDoesNotWork is placement mistake 1. The deferred closure
// calls a helper, and the helper calls recover. By then recover is running in
// the helper's frame, not the deferred one, so it returns nil and the panic
// continues unwinding.
//
// The outer capturePanic is what keeps this demo alive.
func recoverViaHelperDoesNotWork() (recovered bool) {
	defer func() {
		// tryRecover calls recover(), which from here returns nil.
		if tryRecover() {
			recovered = true
		}
	}()

	panic("boom")
}

// tryRecover looks reasonable and is useless. recover only works one frame
// deep, and this is two.
func tryRecover() bool {
	return recover() != nil
}

// recoverNotDeferredDoesNothing is placement mistake 2. Calling recover outside
// a deferred function always returns nil, because nothing is unwinding.
func recoverNotDeferredDoesNothing() (sawPanic bool) {
	if r := recover(); r != nil {
		sawPanic = true // unreachable
	}
	return sawPanic
}

// deferRecoverDirectlyDoesNotWork is placement mistake 3, and the subtlest.
// `defer recover()` defers a call to recover, so recover runs in its own frame
// rather than inside a deferred FUNCTION of the panicking frame.
//
// The spec is explicit about this, and it reads like it should work.
func deferRecoverDirectlyDoesNotWork() {
	defer recover() //nolint:staticcheck,errcheck // SA5001/deferred recover is the mistake being shown

	panic("boom")
}

// The goroutine rule
// ==================
//
// A recover in one goroutine cannot see a panic in another. Each goroutine has
// its own stack, and an unrecovered panic anywhere terminates the WHOLE
// process, taking every other goroutine with it.
//
// This is the most common way a Go service dies in production: a handler
// spawns a goroutine for background work, that goroutine panics on a nil
// pointer six months later, and the entire server exits.

// panicInGoroutineIsUnrecoverable shows the shape that does not work. The
// recover in the parent never fires, so if the goroutine actually panicked the
// process would die.
//
// It is written to NOT panic, because running it otherwise would end the demo.
// The test alongside it exercises the working version instead.
func panicInGoroutineIsUnrecoverable(shouldPanic bool) (parentRecovered bool, done bool) {
	var wg sync.WaitGroup

	defer func() {
		// This recover protects THIS goroutine only. It will never see a panic
		// raised inside the goroutine started below.
		if r := recover(); r != nil {
			parentRecovered = true
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		if shouldPanic {
			// Uncommenting a real panic here would kill the process, parent
			// recover or not. That is the point of the function.
			_ = "this would be panic(\"boom\")"
		}
		done = true
	}()

	wg.Wait()
	return parentRecovered, done
}

// safeGo is the fix, and the thing to put in every codebase that starts
// goroutines. Every goroutine gets its own deferred recover, and the panic is
// reported rather than swallowed.
//
// Swallowing is the failure mode to avoid here: a recover that discards the
// value turns a crash into a silent no-op, which is harder to debug than the
// crash was. onPanic is required, not optional, for that reason.
func safeGo(wg *sync.WaitGroup, onPanic func(any), fn func()) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer func() {
			if r := recover(); r != nil {
				onPanic(r)
			}
		}()
		fn()
	}()
}

// workerPoolSurvivesOneBadJob is the realistic use: a pool where one poisonous
// job must not take down the others or the process.
func workerPoolSurvivesOneBadJob(jobs []func() int) (results []int, panics []string) {
	var (
		mu sync.Mutex
		wg sync.WaitGroup
	)

	for i, job := range jobs {
		safeGo(&wg,
			func(r any) {
				mu.Lock()
				defer mu.Unlock()
				panics = append(panics, fmt.Sprintf("job %d: %v", i, r))
			},
			func() {
				v := job()
				mu.Lock()
				defer mu.Unlock()
				results = append(results, v)
			},
		)
	}

	wg.Wait()
	return results, panics
}

// repanicPreservesTheStack is the pattern for a recover that inspects a panic
// and decides it should not have been caught. Re-panicking with the original
// value keeps the program's behaviour; the stack trace will point here rather
// than at the original site, which is the cost.
func repanicPreservesTheStack(value any, handle func(any) bool) (handled bool) {
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		if handle(r) {
			handled = true
			return
		}
		// Not ours. Let it keep going.
		panic(r)
	}()

	panic(value)
}

// demoRecovery prints each placement and the goroutine rule.
func demoRecovery() {
	fmt.Printf("  recover in a deferred closure:      recovered=%t\n", recoverInDeferredClosure())

	msg := capturePanic(func() { _ = recoverViaHelperDoesNotWork() })
	fmt.Printf("  recover inside a called helper:     escaped with %q\n", msg)

	fmt.Printf("  recover outside any defer:          sawPanic=%t\n", recoverNotDeferredDoesNothing())

	msg = capturePanic(deferRecoverDirectlyDoesNotWork)
	fmt.Printf("  `defer recover()` directly:         escaped with %q\n", msg)

	_, done := panicInGoroutineIsUnrecoverable(false)
	fmt.Printf("  goroutine finished normally:        done=%t\n", done)
	fmt.Println("  a panic in a goroutine kills the process, whatever the parent defers")

	jobs := []func() int{
		func() int { return 1 },
		func() int { panic("bad input") },
		func() int { return 3 },
		func() int { var xs []int; return xs[5] }, // index out of range
	}
	results, panicked := workerPoolSurvivesOneBadJob(jobs)
	fmt.Printf("\n  worker pool, 4 jobs, 2 of them panicking:\n")
	fmt.Printf("    results: %d succeeded\n", len(results))
	for _, p := range panicked {
		fmt.Printf("    contained: %s\n", p)
	}

	handled := repanicPreservesTheStack("mine", func(r any) bool { return r == "mine" })
	fmt.Printf("\n  re-panic: handled a value it recognised: %t\n", handled)
	msg = capturePanic(func() {
		_ = repanicPreservesTheStack("not mine", func(r any) bool { return r == "mine" })
	})
	fmt.Printf("  re-panic: passed on a value it did not: %q\n", msg)

	// A brief pause so any stray goroutine output lands before the next section.
	time.Sleep(time.Millisecond)
}
