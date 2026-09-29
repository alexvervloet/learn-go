// Package concurrency is the code under test for testing concurrent code, which is where Go's
// testing story diverges most from Python's.
//
// # There is no pytest-asyncio, because there is nothing to hook
//
// pytest-asyncio exists because an async test needs an event loop, and something has to create it,
// choose its policy, and tear it down. Go has no event loop: a goroutine is a goroutine and a test
// function can start one. So the plugin has no job.
//
// What Go has instead, and Python does not:
//
//	-race          a runtime data-race detector, in the toolchain
//	synctest       a fake clock and deterministic scheduling, in the standard library since 1.24
//	goroutine dumps on deadlock, so a hung test says what it was waiting for
//
// # The two failure modes a concurrency test has to catch
//
//	A DATA RACE, which -race finds and which no amount of ordinary testing will. A racy test
//	  passes; the same test under -race fails deterministically.
//
//	A LEAKED GOROUTINE, which nothing finds unless you look. A function that starts a goroutine
//	  and returns before it finishes has no failure at all until the process runs out of memory
//	  in production.
package concurrency

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Counter is a shared counter, with two implementations so the race detector has something to say.
type Counter interface {
	Add(n int)
	Value() int
}

// UnsafeCounter has a data race on purpose.
//
// It is here so the race detector has a target, and it is unexported from the module's point of
// view: nothing outside the tests uses it. See concurrency_race_test.go, which is behind a build
// tag because a deliberate race should not fail the suite.
type UnsafeCounter struct {
	n int
}

// Add increments without synchronisation, which is the race.
func (c *UnsafeCounter) Add(n int) { c.n += n }

// Value reads without synchronisation.
func (c *UnsafeCounter) Value() int { return c.n }

// MutexCounter is the fix, and the cheapest one to reason about.
type MutexCounter struct {
	mu sync.Mutex
	n  int
}

// Add increments under the lock.
func (c *MutexCounter) Add(n int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.n += n
}

// Value reads under the lock.
//
// The lock on the READ is the part people leave out, and it is required: a read racing with a write
// is a data race even though the read changes nothing, and on a 32-bit platform it can observe a
// half-written 64-bit value.
func (c *MutexCounter) Value() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

// Worker pools
// ============

// ErrPoolClosed means work was submitted after Close.
var ErrPoolClosed = errors.New("concurrency: pool closed")

// Pool runs jobs on a fixed number of goroutines.
//
// # The contract, which the first version of this got wrong
//
// Results arrive on a BOUNDED channel, so a caller that submits more jobs than the buffer holds
// without reading any of them deadlocks: the workers block sending, so nothing reads from jobs, so
// Submit blocks forever.
//
// That is not a hypothetical. The first test written against this pool submitted twenty jobs to a
// pool of four and then started draining, and it hung until the test timeout.
// TestPoolDeadlocksIfResultsAreNotDrained pins the behaviour down so it cannot be rediscovered.
//
// So the contract is: START DRAINING Results() BEFORE SUBMITTING. If that is awkward, use Collect,
// which does it for you and is what most callers actually want.
//
// The alternative design is an unbounded results channel, which cannot deadlock and can instead
// grow without limit. Neither is free; a bounded channel with a stated contract is the honest
// version.
type Pool struct {
	jobs    chan func() error
	results chan error
	wg      sync.WaitGroup

	closeOnce sync.Once
	closed    chan struct{}
}

// NewPool starts n workers.
func NewPool(n int) *Pool {
	p := &Pool{
		jobs:    make(chan func() error),
		results: make(chan error, n),
		closed:  make(chan struct{}),
	}

	for range n {
		// wg.Go rather than wg.Add(1) plus a goroutine with a deferred Done. Added in
		// Go 1.25, and it removes the Add/Done mismatch that go vet's waitgroup check
		// exists to catch.
		p.wg.Go(func() {
			for {
				select {
				case job := <-p.jobs:
					p.results <- job()
				case <-p.closed:
					return
				}
			}
		})
	}

	return p
}

// Submit queues a job, or reports that the pool is closed.
//
// The jobs channel is never closed, and that is what makes Submit safe to race with Close. The
// first version closed it in Close and relied on the select on p.closed to keep Submit from
// sending on it. That cannot work: a Submit already waiting in the second select, when Close
// runs, has TWO ready cases, and select picks at random, so about once in two thousand it chose
// the send and panicked. TestSubmitRacingCloseNeverPanics found it. Now the workers stop on
// p.closed instead, a Submit that wins the race hands its job to a worker that runs it before
// Close returns, and one that loses gets ErrPoolClosed.
func (p *Pool) Submit(ctx context.Context, job func() error) error {
	select {
	case <-p.closed:
		return ErrPoolClosed
	default:
	}

	select {
	case p.jobs <- job:
		return nil
	case <-p.closed:
		return ErrPoolClosed
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close stops accepting work and waits for the workers to finish.
//
// sync.Once, because Close being called twice would close a closed channel and panic. A Close that
// is not idempotent is a landmine in a defer.
func (p *Pool) Close() {
	p.closeOnce.Do(func() {
		close(p.closed)
		p.wg.Wait()
		close(p.results)
	})
}

// Results returns the channel errors arrive on. It closes when Close has finished.
//
// Must be drained concurrently with Submit; see the type's documentation.
func (p *Pool) Results() <-chan error { return p.results }

// Collect runs every job on n workers and returns the errors, in completion order.
//
// This is the API most callers want, and it exists because Pool's streaming interface has a
// deadlock the caller has to know about. Collect starts the drain before submitting, so there is
// nothing to get wrong.
//
// Completion order, not submission order: the results are whatever finished first. A caller needing
// results paired with their jobs has to carry the pairing itself, which is why this returns errors
// rather than pretending to.
func Collect(ctx context.Context, workers int, jobs []func() error) ([]error, error) {
	if workers < 1 {
		workers = 1
	}

	p := NewPool(workers)

	// The drain starts FIRST, which is the whole difference from the version that deadlocks.
	collected := make([]error, 0, len(jobs))
	done := make(chan struct{})

	go func() {
		defer close(done)

		for err := range p.Results() {
			collected = append(collected, err)
		}
	}()

	var submitErr error
	for _, job := range jobs {
		if err := p.Submit(ctx, job); err != nil {
			submitErr = err
			break
		}
	}

	p.Close()
	<-done

	if submitErr != nil {
		return collected, fmt.Errorf("submitting: %w", submitErr)
	}

	return collected, nil
}

// Leaky goroutines
// ================

// LeakyFetch starts a goroutine and never waits for it, which is the leak.
//
// The bug is the unbuffered channel plus the early return: when the context is cancelled, the
// goroutine is still blocked sending on results and nothing will ever receive, so it lives until the
// process ends. One per request is enough to take a service down over a day.
func LeakyFetch(ctx context.Context, d time.Duration) (string, error) {
	results := make(chan string) // unbuffered: the send blocks until someone receives

	go func() {
		time.Sleep(d)
		results <- "done" // blocks forever if the caller has already returned
	}()

	select {
	case r := <-results:
		return r, nil
	case <-ctx.Done():
		return "", ctx.Err() // and the goroutine above is now stuck
	}
}

// Fetch is the same function without the leak.
//
// Two ways to fix it and this uses both, because they protect against different things:
//
//	a BUFFERED channel, so the send completes even with nobody receiving. One slot is enough
//	  because exactly one value is ever sent.
//	a select on ctx.Done() in the GOROUTINE too, so it stops early rather than sleeping out
//	  its delay after the caller has gone.
//
// The buffer alone fixes the leak. The select also stops the work, which matters when the work is
// expensive rather than a sleep.
func Fetch(ctx context.Context, d time.Duration) (string, error) {
	results := make(chan string, 1)

	go func() {
		select {
		case <-time.After(d):
		case <-ctx.Done():
			return
		}

		// Cannot block: the channel has a slot and only one value is ever sent.
		results <- "done"
	}()

	select {
	case r := <-results:
		return r, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// Rate limiting, for synctest
// ===========================

// Limiter allows n events per interval, using a token bucket.
//
// A real clock makes this untestable without sleeping. synctest makes it exact, and the tests assert
// timings to the nanosecond.
type Limiter struct {
	mu       sync.Mutex
	interval time.Duration
	capacity int
	tokens   int
	lastFill time.Time
}

// NewLimiter returns a limiter that starts full.
func NewLimiter(capacity int, interval time.Duration) *Limiter {
	return &Limiter{
		interval: interval,
		capacity: capacity,
		tokens:   capacity,
		lastFill: time.Now(),
	}
}

// Allow reports whether an event may proceed now.
func (l *Limiter) Allow() bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.refill()

	if l.tokens == 0 {
		return false
	}

	l.tokens--
	return true
}

// Wait blocks until an event may proceed, or the context is done.
func (l *Limiter) Wait(ctx context.Context) error {
	for {
		if l.Allow() {
			return nil
		}

		// Poll rather than computing the exact wait, because the exact version is where
		// the off-by-one lives and this is easier to get right. The cost is a wakeup per
		// tenth of an interval, which synctest makes free to test.
		select {
		case <-time.After(l.interval / 10):
		case <-ctx.Done():
			return fmt.Errorf("waiting for a token: %w", ctx.Err())
		}
	}
}

// refill adds tokens for the time that has passed. Caller holds the lock.
func (l *Limiter) refill() {
	now := time.Now()

	elapsed := now.Sub(l.lastFill)
	if elapsed < l.interval {
		return
	}

	intervals := int(elapsed / l.interval)

	l.tokens = min(l.capacity, l.tokens+intervals*l.capacity)
	l.lastFill = l.lastFill.Add(time.Duration(intervals) * l.interval)
}

// Tokens reports how many are available, for tests.
func (l *Limiter) Tokens() int {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.refill()
	return l.tokens
}
