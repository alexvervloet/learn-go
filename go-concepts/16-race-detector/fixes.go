package main

import (
	"sync"
	"sync/atomic"
)

// Each race from races.go, fixed, with the memory-model rule that makes the
// fix work. "It seems to work now" is not a fix; knowing which construct
// establishes the ordering is.
//
// The Go memory model names these happens-before edges:
//
//	a send on a channel happens before the corresponding receive completes
//	a receive from an unbuffered channel happens before the send completes
//	Unlock happens before a later Lock returns
//	Wait returns after the last Done
//	once.Do returns after f() has returned, for every caller
//	an atomic write happens before a later atomic read that observes it

// Fix 1: a mutex, or an atomic
// ----------------------------

// mutexCounter serialises access.
type mutexCounter struct {
	mu    sync.Mutex
	value int
}

func (c *mutexCounter) Inc() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.value++
}

// Value must lock too. Reading an int without the lock is still a race, even
// though a single int read looks indivisible: the memory model is about
// ordering, not about instruction width.
func (c *mutexCounter) Value() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.value
}

// atomicCounter is the cheaper fix for a single word. Lesson 09 measured it at
// 57ns against the mutex's 131ns.
//
// atomic.Int64 rather than atomic.AddInt64(&x, 1): the method form keeps the
// integer unexported, so a non-atomic access is not expressible. That is not
// pedantry; it is the exact bug the detector found in lesson 06.
type atomicCounter struct {
	value atomic.Int64
}

func (c *atomicCounter) Inc() { c.value.Add(1) }

func (c *atomicCounter) Value() int { return int(c.value.Load()) }

// countWithMutex and countWithAtomic both return exactly n.
func countWithMutex(n int) int {
	c := &mutexCounter{}

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Go(c.Inc)
	}
	wg.Wait()

	return c.Value()
}

func countWithAtomic(n int) int {
	c := &atomicCounter{}

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Go(c.Inc)
	}
	wg.Wait()

	return c.Value()
}

// Fix 2: a guarded map
// --------------------

// safeMap is a map behind a mutex, which is the answer for almost every case
// (lesson 09 measured sync.Map as faster under heavy contention, and this is
// still where to start, for the types).
type safeMap struct {
	mu sync.RWMutex
	m  map[int]int
}

func newSafeMap() *safeMap { return &safeMap{m: make(map[int]int)} }

func (s *safeMap) Set(k, v int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[k] = v
}

func (s *safeMap) Get(k int) (int, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.m[k]
	return v, ok
}

func (s *safeMap) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.m)
}

// fillMapConcurrently writes n entries from n goroutines.
func fillMapConcurrently(n int) int {
	m := newSafeMap()

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Go(func() { m.Set(i, i*i) })
	}
	wg.Wait()

	return m.Len()
}

// Fix 3: pre-size and index, rather than append
// ---------------------------------------------
//
// The best fix is not a lock. A pre-sized slice with one goroutine per index
// needs no synchronisation at all, because distinct elements are distinct
// memory and the WaitGroup provides the ordering for reading them afterwards.
//
// This is faster than a mutex AND preserves input order, which append from
// several goroutines does not.

func fillSliceByIndex(n int) []int {
	results := make([]int, n) // pre-sized: every goroutine owns one element

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Go(func() {
			results[i] = i * i // no lock: distinct index, distinct memory
		})
	}
	wg.Wait() // the happens-before edge that makes the writes visible

	return results
}

// appendUnderLock is the fix when the count is not known ahead of time. Note
// that the ORDER is then nondeterministic, which is usually why the indexed
// version is better when it is available.
func appendUnderLock(n int) []int {
	var (
		mu     sync.Mutex
		shared = make([]int, 0, n)
		wg     sync.WaitGroup
	)

	for i := 0; i < n; i++ {
		wg.Go(func() {
			mu.Lock()
			defer mu.Unlock()
			shared = append(shared, i)
		})
	}
	wg.Wait()

	return shared
}

// collectOverAChannel is the third option: one goroutine owns the slice and
// everyone else sends to it. "Share memory by communicating", and for this
// shape it is more code than the indexed version for no benefit.
func collectOverAChannel(n int) []int {
	ch := make(chan int, n)

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Go(func() { ch <- i })
	}

	go func() {
		wg.Wait()
		close(ch)
	}()

	out := make([]int, 0, n)
	for v := range ch {
		out = append(out, v)
	}
	return out
}

// Fix 4: a guarded struct, or an immutable one
// --------------------------------------------

// safeConfig guards every field with one mutex.
type safeConfig struct {
	mu      sync.RWMutex
	timeout int
	retries int
	name    string
}

func (c *safeConfig) Update(timeout, retries int, name string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.timeout, c.retries, c.name = timeout, retries, name
}

// Snapshot returns a consistent copy. Returning the fields individually would
// let a caller read timeout from one update and retries from the next.
func (c *safeConfig) Snapshot() (timeout, retries int, name string) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.timeout, c.retries, c.name
}

// immutableConfig is the other fix, and usually the better one: never mutate,
// swap the whole value atomically. Readers never block and always see a
// coherent snapshot, which lesson 09 demonstrated with atomic.Pointer.
type immutableConfig struct {
	Timeout int
	Retries int
	Name    string
}

type configHolder struct {
	current atomic.Pointer[immutableConfig]
}

func newConfigHolder(initial immutableConfig) *configHolder {
	h := &configHolder{}
	h.current.Store(&initial)
	return h
}

// Load never blocks.
func (h *configHolder) Load() immutableConfig { return *h.current.Load() }

// Store replaces the whole value. A reader sees the old one entirely or the new
// one entirely, never a mixture.
func (h *configHolder) Store(c immutableConfig) { h.current.Store(&c) }

// updateConfigConcurrently exercises both.
func updateConfigConcurrently(iterations int) (guarded safeConfig, swapped immutableConfig) {
	var (
		guardedCfg safeConfig
		holder     = newConfigHolder(immutableConfig{})
		wg         sync.WaitGroup
	)

	wg.Go(func() {
		for i := 0; i < iterations; i++ {
			guardedCfg.Update(i, i*2, "writer")
			holder.Store(immutableConfig{Timeout: i, Retries: i * 2, Name: "writer"})
		}
	})

	wg.Go(func() {
		for i := 0; i < iterations; i++ {
			_, _, _ = guardedCfg.Snapshot()
			_ = holder.Load()
		}
	})

	wg.Wait()

	timeout, retries, name := guardedCfg.Snapshot()
	return safeConfig{timeout: timeout, retries: retries, name: name}, holder.Load()
}

// Fix 5: give each goroutine its own variable
// --------------------------------------------
//
// The cheapest fix, when it is available: do not share. Each goroutine
// accumulates locally and the results are combined once at the end.

func sumWithoutSharing(n, workers int) int {
	partials := make([]int, workers) // one element per worker: no sharing

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Go(func() {
			local := 0 // entirely this goroutine's
			for i := w; i < n; i += workers {
				local += i
			}
			partials[w] = local
		})
	}
	wg.Wait()

	total := 0
	for _, p := range partials {
		total += p
	}
	return total
}

// happensBeforeEdges lists what actually establishes ordering, because "add a
// lock somewhere" is not a fix and knowing the rule is.
func happensBeforeEdges() []string {
	return []string{
		"a channel send happens before the corresponding receive completes",
		"a receive from an UNBUFFERED channel happens before the send completes",
		"closing a channel happens before a receive that returns the zero value",
		"Unlock happens before a later Lock returns",
		"WaitGroup.Wait returns after the last Done",
		"once.Do returns after f() has returned, for every caller",
		"an atomic write happens before a later atomic read that observes it",
		"a goroutine's creation happens before the goroutine starts",
	}
}

// fixPreference is the order to try them in.
func fixPreference() []string {
	return []string{
		"1. do not share: give each goroutine its own variable, combine at the end",
		"2. share immutably: never mutate, swap the whole value with atomic.Pointer",
		"3. distinct memory: a pre-sized slice, one index per goroutine, no lock",
		"4. an atomic: for a single word, ~2x cheaper than a mutex",
		"5. a mutex: for anything with an invariant across several fields",
		"6. a channel: when you are passing ownership rather than protecting state",
	}
}
