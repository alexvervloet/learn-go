package main

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// WaitGroup
// =========
//
// A counter with a Wait. Add(n) raises it, Done lowers it by one, Wait blocks
// until it reaches zero.
//
//	wg.Add(1)          BEFORE the go statement, never inside the goroutine
//	defer wg.Done()    first line of the goroutine, so every exit path counts
//	wg.Wait()          after starting them all
//
// The zero value is ready. Never copy one: pass *sync.WaitGroup, or capture it
// in a closure. go vet's copylocks catches the copy.

// correctWaitGroup is the shape to memorise.
func correctWaitGroup(n int) int {
	var (
		wg      sync.WaitGroup
		counter atomic.Int64
	)

	for i := 0; i < n; i++ {
		wg.Add(1) // before the go statement
		go func() {
			defer wg.Done() // deferred, so a panic still decrements
			counter.Add(1)
		}()
	}

	wg.Wait()
	return int(counter.Load())
}

// addInsideTheGoroutineRaces is the classic bug. Wait can run before any
// goroutine has been scheduled, see a counter of zero, and return immediately.
//
// GO VET CATCHES THIS, which is the most useful thing on this page. Writing it
// the obvious way:
//
//	go func() {
//	    wg.Add(1)
//	    defer wg.Done()
//	}()
//
// fails the build with:
//
//	WaitGroup.Add called from inside new goroutine
//
// So this function has to launder the call through a method value to stay
// runnable, which is the only reason `add` exists. Do not copy this shape; the
// point is that you cannot easily write the bug any more.
//
// The race detector also reports it, as a race on the WaitGroup itself.
func addInsideTheGoroutineRaces(n int) int {
	var (
		wg      sync.WaitGroup
		counter atomic.Int64
	)

	// A method value bound to &wg. Identical at runtime to calling wg.Add(1)
	// directly; opaque enough that vet's syntactic check does not see it.
	add := wg.Add

	for i := 0; i < n; i++ {
		go func() {
			add(1) // WRONG: may not have run before Wait is called
			defer wg.Done()
			counter.Add(1)
		}()
	}

	wg.Wait() // may see 0 and return before anything has started
	return int(counter.Load())
}

// missingDoneDeadlocks: without the Done, Wait never returns. This version
// runs Wait in a goroutine and gives up, so the demo survives.
func missingDoneDeadlocks() (waitReturned bool) {
	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		// No Done. In real code the usual cause is an early return between
		// Add and Done, which is exactly what `defer wg.Done()` prevents.
		_ = 1
	}()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return true
	case <-time.After(30 * time.Millisecond):
		return false
	}
}

// negativeCounterPanics: more Done calls than Add is a panic, not a silent
// wrap. It usually means a Done ran twice, often because it was both deferred
// and called explicitly.
func negativeCounterPanics() (message string) {
	defer func() {
		if r := recover(); r != nil {
			message = fmt.Sprint(r)
		}
	}()

	var wg sync.WaitGroup
	wg.Add(1)
	wg.Done()
	wg.Done() // one too many

	return ""
}

// waitGroupGo uses Go 1.25's WaitGroup.Go, which does Add(1), runs the
// function in a new goroutine, and Done()s when it returns. It removes both
// the "Add in the wrong place" bug and the "forgot Done" bug by construction.
//
// This is the form to prefer in new code on Go 1.25 and later.
func waitGroupGo(n int) int {
	var (
		wg      sync.WaitGroup
		counter atomic.Int64
	)

	for i := 0; i < n; i++ {
		wg.Go(func() { // Add, go, and Done, in one call
			counter.Add(1)
		})
	}

	wg.Wait()
	return int(counter.Load())
}

// reusingAWaitGroup is legal, but only after Wait has returned. Calling Add
// concurrently with Wait is a race, which is the same bug as
// addInsideTheGoroutineRaces in a different costume.
func reusingAWaitGroup(rounds, perRound int) int {
	var (
		wg      sync.WaitGroup
		counter atomic.Int64
	)

	for r := 0; r < rounds; r++ {
		for i := 0; i < perRound; i++ {
			wg.Go(func() { counter.Add(1) })
		}
		wg.Wait() // fully drained before the next round adds to it
	}

	return int(counter.Load())
}

// collectingResults is the pattern people reach for a WaitGroup to do: run n
// things, gather n results. Indexing a pre-sized slice needs no lock at all,
// because each goroutine writes a distinct element.
//
// This is a genuine exception to "shared memory needs a mutex": distinct
// elements of a slice are distinct memory, and the WaitGroup provides the
// happens-before edge that makes the writes visible after Wait.
func collectingResults(inputs []int, work func(int) int) []int {
	results := make([]int, len(inputs)) // pre-sized: no append, no lock

	var wg sync.WaitGroup
	for i, in := range inputs {
		wg.Go(func() {
			results[i] = work(in) // distinct index per goroutine
		})
	}
	wg.Wait()

	return results
}

// demoWaitGroups prints correct and incorrect WaitGroup use.
func demoWaitGroups() {
	const n = 1000

	fmt.Printf("  correct Add-before-go:        %d of %d\n", correctWaitGroup(n), n)

	racy := addInsideTheGoroutineRaces(n)
	fmt.Printf("  Add inside the goroutine:     %d of %d  (Wait may return early)\n", racy, n)
	fmt.Println("  ...-race reports this as a race on the WaitGroup itself")

	fmt.Printf("\n  missing Done, Wait returned:  %t\n", missingDoneDeadlocks())
	fmt.Printf("  one Done too many:            panic: %s\n", negativeCounterPanics())

	fmt.Printf("\n  WaitGroup.Go (Go 1.25+):      %d of %d\n", waitGroupGo(n), n)
	fmt.Printf("  reused across 3 rounds of 10: %d\n", reusingAWaitGroup(3, 10))

	results := collectingResults([]int{1, 2, 3, 4, 5}, func(v int) int { return v * v })
	fmt.Printf("  collecting into a pre-sized slice, no lock: %v\n", results)
}
