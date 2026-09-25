// Package main is lesson 09 of go-concepts: sync primitives.
//
// Channels move data between goroutines. sync and sync/atomic protect data that
// several goroutines share. Both are idiomatic; the Go wiki's own guidance is
// "use whichever is most expressive and/or most simple".
//
//	sync.Mutex      one holder at a time
//	sync.RWMutex    many readers or one writer
//	sync.WaitGroup  wait for N goroutines
//	sync.Once       run something exactly once
//	sync/atomic     lock-free single-word operations
package main

import (
	"fmt"
	"sync"
	"time"
)

// Counter is the canonical mutex type. Three things make it correct, and all
// three are easy to get wrong:
//
//	the zero value works          no constructor, no initialisation
//	defer mu.Unlock()             survives panics and early returns
//	pointer receivers everywhere  a copy would copy the lock state
type Counter struct {
	mu    sync.Mutex
	value int
}

// Inc increments under the lock.
func (c *Counter) Inc() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.value++
}

// Value reads under the lock. Reading an int without the lock would be a data
// race even though a single int read looks atomic: the race detector flags it,
// and the compiler is entitled to assume it does not happen.
func (c *Counter) Value() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.value
}

// Add is the operation that shows why check-then-act needs one critical
// section rather than two.
func (c *Counter) Add(n int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.value += n
}

// racyCounter has no lock at all. It exists so the race detector has something
// to find, and so the test can show the lost updates.
type racyCounter struct {
	value int
}

func (c *racyCounter) Inc() { c.value++ } // read, add, write: three steps, not one

// countConcurrently runs inc from n goroutines and returns the final value.
// With a mutex this is always n; without one it is usually less, because
// value++ is a read-modify-write that two goroutines can interleave.
func countConcurrently(n int, inc func(), value func() int) int {
	var wg sync.WaitGroup

	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			inc()
		}()
	}
	wg.Wait()

	return value()
}

// checkThenActIsNotAtomic is the bug that survives having a mutex. Each
// operation is individually locked and the PAIR is not, so another goroutine
// can slip between them.
//
//	mu.Lock(); v := m[k]; mu.Unlock()     <- another goroutine writes here
//	mu.Lock(); m[k] = v + 1; mu.Unlock()
//
// The result is a lost update, from code that looks carefully synchronised.
type brokenMap struct {
	mu sync.Mutex
	m  map[string]int
}

func newBrokenMap() *brokenMap { return &brokenMap{m: make(map[string]int)} }

// IncBroken locks twice. The gap between them is the bug.
func (b *brokenMap) IncBroken(key string) {
	b.mu.Lock()
	v := b.m[key]
	b.mu.Unlock()

	// Another goroutine can run the whole of IncBroken right here.

	b.mu.Lock()
	b.m[key] = v + 1
	b.mu.Unlock()
}

// IncFixed holds the lock across the whole read-modify-write.
func (b *brokenMap) IncFixed(key string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.m[key] = b.m[key] + 1
}

func (b *brokenMap) Get(key string) int {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.m[key]
}

// copyingAMutexBreaksIt documents what go vet's copylocks check catches.
//
//	func (c Counter) Broken() { c.mu.Lock() }   // value receiver: copies the mutex
//	  -> go vet: Broken passes lock by value: Counter contains sync.Mutex
//
//	other := *counter                            // copies the lock state too
//	  -> go vet: assignment copies lock value
//
// The copy locks and unlocks its own mutex, protecting nothing, and the
// original stays unprotected. This is why every method on a type with a mutex
// takes a pointer receiver, and why such types are passed as pointers.
func copyingAMutexBreaksIt() []string {
	return []string{
		"a value receiver on a type with a mutex copies the mutex",
		"the copy's Lock protects the copy, so the original is unguarded",
		"go vet's copylocks catches it: \"passes lock by value\"",
		"the rule: pointer receivers everywhere, and pass the type as a pointer",
	}
}

// Cache is the RWMutex case: reads vastly outnumber writes.
//
// RLock allows many concurrent readers. It is NOT free: each RLock costs more
// than a plain Lock, and a steady stream of readers can starve a writer. It
// wins when reads dominate AND the critical section is long enough for the
// parallelism to pay for the overhead.
type Cache struct {
	mu    sync.RWMutex
	items map[string]string
}

func NewCache() *Cache { return &Cache{items: make(map[string]string)} }

// Get takes the read lock, so any number of Gets run concurrently.
func (c *Cache) Get(key string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	v, ok := c.items[key]
	return v, ok
}

// Set takes the write lock, which excludes every reader and every other writer.
func (c *Cache) Set(key, value string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.items[key] = value
}

// GetOrCompute is the pattern worth studying: take the read lock for the common
// case, upgrade to the write lock only on a miss, and CHECK AGAIN after
// upgrading.
//
// The second check is not redundant. Go's RWMutex has no upgrade operation, so
// RUnlock and Lock are two steps, and another goroutine can populate the key in
// between. Without the recheck, both goroutines compute and one write is lost,
// which for an expensive computation is the whole cost of the cache.
func (c *Cache) GetOrCompute(key string, compute func() string) (value string, computed bool) {
	c.mu.RLock()
	if v, ok := c.items[key]; ok {
		c.mu.RUnlock()
		return v, false
	}
	c.mu.RUnlock()

	// The gap. Another goroutine may fill this key right now.

	c.mu.Lock()
	defer c.mu.Unlock()

	if v, ok := c.items[key]; ok { // the recheck
		return v, false
	}

	v := compute()
	c.items[key] = v
	return v, true
}

// Len reports the entry count under the read lock.
func (c *Cache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return len(c.items)
}

// demoMutexes prints locking behaviour and the check-then-act bug.
func demoMutexes() {
	const goroutines = 1000

	safe := &Counter{}
	got := countConcurrently(goroutines, safe.Inc, safe.Value)
	fmt.Printf("  %d goroutines, mutex-protected counter: %d\n", goroutines, got)

	racy := &racyCounter{}
	racyGot := countConcurrently(goroutines, racy.Inc, func() int { return racy.value })
	fmt.Printf("  %d goroutines, unprotected counter:     %d  (lost %d updates)\n",
		goroutines, racyGot, goroutines-racyGot)
	fmt.Println("  ...run with -race and the second one is reported as a data race")

	broken := newBrokenMap()
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() { defer wg.Done(); broken.IncBroken("k") }()
	}
	wg.Wait()
	fmt.Printf("\n  check-then-act with two locked sections: %d of %d\n", broken.Get("k"), goroutines)

	fixed := newBrokenMap()
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() { defer wg.Done(); fixed.IncFixed("k") }()
	}
	wg.Wait()
	fmt.Printf("  one locked section:                      %d of %d\n", fixed.Get("k"), goroutines)

	fmt.Println("\n  copying a mutex:")
	for _, s := range copyingAMutexBreaksIt() {
		fmt.Printf("    %s\n", s)
	}

	cache := NewCache()
	var computeCount int
	var computeMu sync.Mutex

	wg.Add(50)
	for i := 0; i < 50; i++ {
		go func() {
			defer wg.Done()
			cache.GetOrCompute("expensive", func() string {
				computeMu.Lock()
				computeCount++
				computeMu.Unlock()
				time.Sleep(time.Millisecond)
				return "result"
			})
		}()
	}
	wg.Wait()

	computeMu.Lock()
	count := computeCount
	computeMu.Unlock()
	fmt.Printf("\n  50 goroutines racing GetOrCompute: computed %d time(s), cache has %d entry\n",
		count, cache.Len())
}
