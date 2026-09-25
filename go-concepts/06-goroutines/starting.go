// Package main is lesson 06 of go-concepts: goroutines.
//
//	go f()   runs f concurrently and returns immediately
//
// A goroutine starts on an 8KB stack that grows by copying. An OS thread starts
// at 1MB or more, fixed. Creating a goroutine costs a few hundred nanoseconds;
// creating a thread costs a syscall. That ratio is why Go programs start one
// goroutine per unit of work instead of pooling them.
//
// The runtime multiplexes goroutines (G) onto OS threads (M) through scheduling
// contexts (P). GOMAXPROCS sets the number of Ps, and therefore how many
// goroutines run Go code at the same instant.
package main

import (
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

// goroutinesStartUnordered shows the guarantee you get, which is none. The
// runtime may run these in any order, on any thread, and a caller that assumes
// otherwise has a bug that will surface under load.
//
// The results are collected through a mutex, because appending to a slice from
// several goroutines is a data race.
func goroutinesStartUnordered(n int) []int {
	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		out []int
	)

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			mu.Lock()
			defer mu.Unlock()
			out = append(out, i)
		}()
	}

	wg.Wait()
	return out
}

// mainDoesNotWait is the first thing everyone gets wrong. When main returns,
// the process exits and every other goroutine is killed mid-flight, with no
// deferred calls run and no warning printed.
//
// It returns how many goroutines happened to finish before the read. The number
// is ARBITRARY, and that is the entire lesson: on an idle 12-core machine it is
// often 90-something out of 100, under load it can be 0, and on the next run it
// will be something else. Code that works because the goroutines usually win
// the race is code that fails in production and not in testing.
//
// The test asserts only that the count is in range, because asserting any
// particular value would be asserting a race.
//
// Note the return type is NOT a named return. An earlier version of this
// function used `(finished int32)`, and `go test -race` caught it: with a named
// return, `return atomic.LoadInt32(&finished)` assigns to `finished` itself,
// and that plain write races with the goroutines still calling AddInt32 on it.
// Atomics on a variable protect only the accesses that actually go through the
// atomic operations. See LESSONS.md.
//
// atomic.Int32 rather than the free functions: the method form makes the
// non-atomic access impossible to write by accident, because the underlying
// integer is unexported.
func mainDoesNotWait(n int) int32 {
	var finished atomic.Int32

	for i := 0; i < n; i++ {
		go func() {
			finished.Add(1)
		}()
	}

	// No wait. Whatever has been scheduled by now is what gets counted.
	return finished.Load()
}

// waitForThem is the fix, and the one you will write most often. A WaitGroup is
// a counter: Add before starting, Done when finished, Wait until zero.
//
// Two rules that matter:
//
//	wg.Add(1) goes BEFORE the go statement, never inside the goroutine. Inside,
//	it races with Wait, which may see a zero counter and return early.
//
//	wg.Done() goes in a defer, so a panic or an early return still decrements.
//	Otherwise Wait blocks forever and the program deadlocks.
func waitForThem(n int) int32 {
	var (
		wg       sync.WaitGroup
		finished atomic.Int32
	)

	for i := 0; i < n; i++ {
		wg.Add(1) // before the go statement
		go func() {
			defer wg.Done() // deferred, so every exit path counts
			finished.Add(1)
		}()
	}

	wg.Wait()
	return finished.Load()
}

// goroutineCost measures what starting one actually costs, by counting
// allocations. The benchmark in starting_test.go reports it properly; this
// version exists so the demo can print a number.
func goroutineCost(n int) (elapsed time.Duration, perGoroutine time.Duration) {
	var wg sync.WaitGroup

	start := time.Now()
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() { wg.Done() }()
	}
	wg.Wait()
	elapsed = time.Since(start)

	return elapsed, elapsed / time.Duration(n)
}

// manyGoroutines starts a large number simultaneously and reports the peak
// count, to make the scale concrete. 100,000 OS threads would exhaust memory;
// 100,000 goroutines is roughly 800MB of stack at worst and usually far less,
// because most never grow past their initial 8KB.
func manyGoroutines(n int) (peak int) {
	var (
		wg    sync.WaitGroup
		start = make(chan struct{})
	)

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start // park here until released, so they all exist at once
		}()
	}

	// Let them all get created and parked before counting.
	runtime.Gosched()
	peak = runtime.NumGoroutine()

	close(start) // release every one of them at once
	wg.Wait()

	return peak
}

// deferredCallsDoNotRunOnExit documents what happens when main returns while
// goroutines are still going: nothing. No defer, no cleanup, no flush.
//
// This is why a program that writes to a buffered log from a goroutine can lose
// its last lines, and why graceful shutdown is a real design problem rather
// than a formality.
func deferredCallsDoNotRunOnExit() string {
	return "when main returns, live goroutines are killed without running defers"
}

// demoStarting prints goroutine creation and its cost.
func demoStarting() {
	unordered := goroutinesStartUnordered(8)
	fmt.Printf("  8 goroutines appending in completion order: %v\n", unordered)
	fmt.Println("  ...run it again and the order changes. There is no ordering guarantee.")

	fmt.Printf("\n  without waiting: %d of 100 goroutines had finished (arbitrary, changes per run)\n",
		mainDoesNotWait(100))
	fmt.Printf("  with a WaitGroup: %d of 100 (guaranteed)\n", waitForThem(100))

	elapsed, each := goroutineCost(100_000)
	fmt.Printf("\n  100,000 goroutines created and joined in %v (%v each)\n", elapsed.Round(time.Millisecond), each)
	fmt.Printf("  peak live goroutines when 100,000 are parked: %d\n", manyGoroutines(100_000))

	fmt.Printf("\n  %s\n", deferredCallsDoNotRunOnExit())
}
