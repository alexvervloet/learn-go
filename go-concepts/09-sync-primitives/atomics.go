package main

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// sync/atomic
// ===========
//
// Lock-free operations on a single word, implemented as CPU instructions rather
// than by parking the goroutine. Roughly an order of magnitude faster than a
// mutex for a single counter, and strictly less capable: atomics protect ONE
// word, not an invariant across several fields.
//
// Prefer the TYPES (atomic.Int64, atomic.Bool, atomic.Pointer[T]) over the free
// functions (atomic.AddInt64(&x, 1)). The types keep the value unexported, so
// there is no way to touch it non-atomically by accident. Lesson 06's LESSONS
// entry is about exactly that mistake.

// atomicCounter is the simplest correct shared counter.
type atomicCounter struct {
	value atomic.Int64
}

func (c *atomicCounter) Inc()       { c.value.Add(1) }
func (c *atomicCounter) Value() int { return int(c.value.Load()) }

// mutexCounter is the same thing with a lock, for the benchmark to compare.
type mutexCounter struct {
	mu    sync.Mutex
	value int
}

func (c *mutexCounter) Inc() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.value++
}

func (c *mutexCounter) Value() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.value
}

// channelCounter routes every increment through a goroutine that owns the
// count. It is the "share memory by communicating" version, included to show
// that for this shape it is the wrong tool: correct, much slower, and more
// code.
//
// incs is UNBUFFERED, deliberately. An earlier version gave it a buffer of 64
// and that made Inc asynchronous: it returned as soon as the value was queued,
// so a Value() straight after a WaitGroup.Wait could read a count that was
// still missing increments already "done". The test caught it.
//
// The buffer also made the benchmark dishonest, measuring the cost of enqueuing
// rather than the cost of counting. An unbuffered channel makes Inc a handshake
// that does not return until the owning goroutine has applied it, which is what
// the atomic and the mutex versions also guarantee. Comparing anything else is
// comparing different operations.
type channelCounter struct {
	incs  chan struct{}
	reads chan chan int
	done  chan struct{}
	wg    sync.WaitGroup
}

func newChannelCounter() *channelCounter {
	c := &channelCounter{
		incs:  make(chan struct{}), // unbuffered: Inc completes the increment
		reads: make(chan chan int),
		done:  make(chan struct{}),
	}

	c.wg.Go(func() {
		var value int
		for {
			select {
			case <-c.incs:
				value++
			case reply := <-c.reads:
				reply <- value
			case <-c.done:
				return
			}
		}
	})

	return c
}

func (c *channelCounter) Inc() { c.incs <- struct{}{} }

func (c *channelCounter) Value() int {
	reply := make(chan int, 1)
	c.reads <- reply
	return <-reply
}

func (c *channelCounter) Close() { close(c.done); c.wg.Wait() }

// compareAndSwap is the primitive everything lock-free is built on: "set this
// to new, but only if it is still old". It returns whether the swap happened.
//
// The retry loop is the standard shape. Another goroutine changing the value
// between the Load and the CAS makes the CAS fail, and the loop reloads and
// tries again. Under low contention this almost always succeeds first time.
func compareAndSwapMax(current *atomic.Int64, candidate int64) (updated bool, attempts int) {
	for {
		attempts++
		old := current.Load()

		if candidate <= old {
			return false, attempts // already at least this big
		}
		if current.CompareAndSwap(old, candidate) {
			return true, attempts
		}
		// Someone else moved it. Reload and reconsider.
	}
}

// atomicBoolAsAFlag is the common case for atomic.Bool: a shutdown or
// ready flag that many goroutines read and one writes.
type service struct {
	running atomic.Bool
	handled atomic.Int64
}

func (s *service) Start() { s.running.Store(true) }
func (s *service) Stop()  { s.running.Store(false) }

// Handle does work only while running. Reading the flag without atomics would
// be a data race even though a bool looks indivisible.
func (s *service) Handle() bool {
	if !s.running.Load() {
		return false
	}
	s.handled.Add(1)
	return true
}

// atomicPointerForConfigReload is the pattern for hot-swapping a whole value.
// Readers Load a pointer and use it; a writer Stores a brand new one. No lock,
// and readers holding the old pointer keep a consistent snapshot rather than
// seeing a half-updated struct.
//
// This is what mutexes cannot give you cheaply: a consistent read of a
// multi-field value with no reader-side locking at all.
type config struct {
	Timeout time.Duration
	Retries int
	Version int
}

type configHolder struct {
	current atomic.Pointer[config]
}

func newConfigHolder(initial *config) *configHolder {
	h := &configHolder{}
	h.current.Store(initial)
	return h
}

// Load returns the current config. Readers never block.
func (h *configHolder) Load() *config { return h.current.Load() }

// Store replaces it wholesale. A reader either sees the old config entirely or
// the new one entirely; there is no state in which Timeout is new and Retries
// is old.
func (h *configHolder) Store(c *config) { h.current.Store(c) }

// consistentSnapshots checks the guarantee: every reader sees a config whose
// fields all come from the same version.
func consistentSnapshots(readers, writes int) (torn int) {
	holder := newConfigHolder(&config{Timeout: time.Second, Retries: 1, Version: 1})

	var (
		wg   sync.WaitGroup
		stop atomic.Bool
		mu   sync.Mutex
	)

	for i := 0; i < readers; i++ {
		wg.Go(func() {
			for !stop.Load() {
				c := holder.Load()
				// Every field of a given version is derived from Version, so
				// any mismatch would mean a torn read.
				if c.Retries != c.Version || c.Timeout != time.Duration(c.Version)*time.Second {
					mu.Lock()
					torn++
					mu.Unlock()
				}
			}
		})
	}

	for v := 2; v <= writes+1; v++ {
		holder.Store(&config{
			Timeout: time.Duration(v) * time.Second,
			Retries: v,
			Version: v,
		})
	}

	stop.Store(true)
	wg.Wait()

	return torn
}

// demoAtomics prints atomic behaviour and a rough timing comparison.
func demoAtomics() {
	const goroutines = 10_000

	timeIt := func(inc func(), value func() int) (int, time.Duration) {
		var wg sync.WaitGroup
		start := time.Now()
		for i := 0; i < goroutines; i++ {
			wg.Go(inc)
		}
		wg.Wait()
		return value(), time.Since(start)
	}

	ac := &atomicCounter{}
	atomicValue, atomicElapsed := timeIt(ac.Inc, ac.Value)

	mc := &mutexCounter{}
	mutexValue, mutexElapsed := timeIt(mc.Inc, mc.Value)

	cc := newChannelCounter()
	channelValue, channelElapsed := timeIt(cc.Inc, cc.Value)
	cc.Close()

	fmt.Printf("  %d concurrent increments:\n", goroutines)
	fmt.Printf("    atomic.Int64: %5d in %v\n", atomicValue, atomicElapsed.Round(time.Microsecond))
	fmt.Printf("    sync.Mutex:   %5d in %v\n", mutexValue, mutexElapsed.Round(time.Microsecond))
	fmt.Printf("    channel:      %5d in %v\n", channelValue, channelElapsed.Round(time.Microsecond))
	fmt.Println("    ...all three correct. `go test -bench` has the honest numbers.")

	var maxVal atomic.Int64
	maxVal.Store(10)
	updated, attempts := compareAndSwapMax(&maxVal, 20)
	fmt.Printf("\n  CAS max: 10 -> 20 updated=%t attempts=%d\n", updated, attempts)
	updated, attempts = compareAndSwapMax(&maxVal, 5)
	fmt.Printf("  CAS max: 20 -> 5  updated=%t attempts=%d\n", updated, attempts)

	svc := &service{}
	fmt.Printf("\n  service stopped, Handle() -> %t\n", svc.Handle())
	svc.Start()
	fmt.Printf("  service started, Handle() -> %t\n", svc.Handle())

	torn := consistentSnapshots(8, 1000)
	fmt.Printf("\n  atomic.Pointer config reload: 8 readers, 1000 writes, %d torn reads\n", torn)
}
