package main

import (
	"fmt"
	"runtime"
	"sync"
	"time"
)

// What the race detector cannot see
// =================================
//
// `-race` clean does NOT mean correct. Three whole categories of concurrency
// bug are invisible to it, and two of them are more common in practice than
// data races.

// Not caught 1: a goroutine leak
// ------------------------------
//
// Nothing is racing. One goroutine is blocked forever on a channel nobody will
// ever use, holding its stack and everything the stack references. The detector
// has nothing to report because there is no unsynchronised access: there is no
// access at all.
//
// The tool for this is runtime.NumGoroutine, or uber-go/goleak (lesson 06).
func leakedGoroutine() (leaked int) {
	before := settledGoroutineCount()

	ch := make(chan int) // nobody will ever send
	go func() {
		<-ch // blocked forever, perfectly synchronised
	}()

	return settledGoroutineCount() - before
}

// settledGoroutineCount gives the runtime a moment before counting, because a
// goroutine that has returned is not deducted instantly.
func settledGoroutineCount() int {
	for i := 0; i < 20; i++ {
		runtime.Gosched()
		time.Sleep(time.Millisecond)
	}
	return runtime.NumGoroutine()
}

// Not caught 2: a deadlock
// ------------------------
//
// Two goroutines each holding what the other needs. Every access is correctly
// locked, so the detector is satisfied. The runtime's own deadlock detector
// only fires when EVERY goroutine is blocked, which never happens in a server
// with a listener open.
//
// The tools: `go test -timeout`, GOTRACEBACK=all, and /debug/pprof/goroutine.
func lockOrderingDeadlock(timeout time.Duration) (deadlocked bool) {
	var a, b sync.Mutex

	// Both goroutines take real, BLOCKING locks, in opposite orders. Neither
	// gives up, because a deadlock that gives up is not a deadlock: an earlier
	// version used TryLock with a timeout, reported false, and demonstrated
	// nothing at all.
	//
	// The two goroutines below are LEAKED permanently, holding a mutex each.
	// That is what a deadlock is, and cleaning it up would misrepresent it.
	// The count is bounded at two and the process exits shortly after.
	first := make(chan struct{})
	go func() {
		defer close(first)

		a.Lock()
		defer a.Unlock()

		time.Sleep(5 * time.Millisecond) // let the other goroutine take b

		b.Lock() // blocks forever: the other holds b and wants a
		defer b.Unlock()
	}()

	second := make(chan struct{})
	go func() {
		defer close(second)

		b.Lock()
		defer b.Unlock()

		time.Sleep(5 * time.Millisecond)

		a.Lock() // blocks forever
		defer a.Unlock()
	}()

	select {
	case <-first:
		<-second
		return false
	case <-time.After(timeout):
		// Neither finished. Every access was correctly locked throughout, and
		// -race has nothing to say about any of it.
		return true
	}
}

// theRuntimeDeadlockDetectorIsAllOrNothing explains why the runtime does not
// help here either.
//
// `fatal error: all goroutines are asleep - deadlock!` fires only when EVERY
// goroutine is blocked. The two above are stuck while the caller keeps
// running, so the runtime stays quiet. A server with an open listener is never
// in the all-blocked state, which is why real services hang rather than abort.
func theRuntimeDeadlockDetectorIsAllOrNothing() []string {
	return []string{
		"the runtime aborts only when EVERY goroutine is blocked",
		"two deadlocked goroutines plus one live one: it stays silent",
		"a server with an open listener is never in the all-blocked state",
		"so: go test -timeout, GOTRACEBACK=all, and /debug/pprof/goroutine",
	}
}

// Not caught 3: a logical race
// ----------------------------
//
// The one that matters most. Every access is correctly locked and the LOGIC is
// still wrong, because the two locked sections are not atomic together.
//
// The detector sees two properly synchronised operations and says nothing. This
// is lesson 09's check-then-act, and it is the concurrency bug most likely to
// reach production.

// checkThenActCounter locks each half and loses updates anyway.
type checkThenActCounter struct {
	mu    sync.Mutex
	value int
}

// IncBroken is correctly synchronised and wrong.
func (c *checkThenActCounter) IncBroken() {
	c.mu.Lock()
	current := c.value
	c.mu.Unlock()

	// Another goroutine can run this whole method right here.

	c.mu.Lock()
	c.value = current + 1
	c.mu.Unlock()
}

// IncFixed holds the lock across the whole read-modify-write.
func (c *checkThenActCounter) IncFixed() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.value++
}

func (c *checkThenActCounter) Value() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.value
}

// logicalRaceLosesUpdates runs the broken version and returns how many of n
// increments survived. -race reports nothing about any of it.
func logicalRaceLosesUpdates(n int) (broken, fixed int) {
	b := &checkThenActCounter{}
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Go(b.IncBroken)
	}
	wg.Wait()

	f := &checkThenActCounter{}
	for i := 0; i < n; i++ {
		wg.Go(f.IncFixed)
	}
	wg.Wait()

	return b.Value(), f.Value()
}

// whatItMisses is the summary worth remembering.
func whatItMisses() []string {
	return []string{
		"goroutine leaks: nothing is racing, so there is nothing to report",
		"deadlocks: every access is correctly locked",
		"logical races: check-then-act with a lock round each half is synchronised and wrong",
		"races on code paths the tests never execute: it observes, it does not analyse",
		"races that need an interleaving your test never produced",
	}
}

// itObservesRatherThanAnalyses is the property that decides how to use it.
func itObservesRatherThanAnalyses() []string {
	return []string{
		"-race instruments accesses at RUNTIME; it is not static analysis",
		"a green run means \"no race in what executed\", not \"no race\"",
		"so its value is bounded by your coverage of concurrent paths",
		"run the WHOLE suite under -race, not one package",
		"drive concurrent tests with many goroutines and -count=10, to vary the interleaving",
	}
}

// costs is the reason it is a testing tool.
func costs() map[string]string {
	return map[string]string{
		"CPU":        "2-20x slower",
		"memory":     "5-10x more",
		"goroutines": "limited to 8192 simultaneously",
		"production": "usually no; one canary instance is a real technique for an unreproducible race",
	}
}

// demoNotCaught prints what escapes the detector.
func demoNotCaught() {
	fmt.Printf("  a leaked goroutine: %+d goroutine(s), and -race reports nothing\n", leakedGoroutine())

	fmt.Printf("  a lock-order deadlock: still stuck after 100ms=%t, and -race reports nothing\n",
		lockOrderingDeadlock(100*time.Millisecond))
	for _, s := range theRuntimeDeadlockDetectorIsAllOrNothing() {
		fmt.Printf("    %s\n", s)
	}

	broken, fixed := logicalRaceLosesUpdates(2000)
	fmt.Printf("  a logical race: check-then-act kept %d of 2000, one locked section kept %d\n",
		broken, fixed)
	fmt.Println("    ...both are correctly synchronised. -race is silent about both.")

	fmt.Println("\n  what it misses:")
	for _, s := range whatItMisses() {
		fmt.Printf("    %s\n", s)
	}

	fmt.Println("\n  it observes rather than analyses:")
	for _, s := range itObservesRatherThanAnalyses() {
		fmt.Printf("    %s\n", s)
	}

	fmt.Println("\n  what it costs:")
	for _, k := range []string{"CPU", "memory", "goroutines", "production"} {
		fmt.Printf("    %-12s %s\n", k, costs()[k])
	}
}
