package ratelimit

import (
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// keyedLimiters holds one rate.Limiter per key.
//
// # The leak this exists to talk about
//
// The obvious implementation is `map[string]*rate.Limiter` and nothing else, and it grows forever. One entry
// per IP that has ever been seen is a slow memory leak that looks like normal heap growth, and on a public
// endpoint under a scan it is not slow.
//
// Three ways out, and the one here is the third:
//
//	an LRU with a fixed size. Correct, needs a dependency or 80 lines, and evicting a limiter
//	  resets that client's budget, which an attacker can force by cycling keys.
//	a TTL plus a background sweeper goroutine. Works, and the goroutine has to be stopped or it
//	  outlives the limiter, which is the leak again in a different shape.
//	a TTL swept lazily, on write, in bounded batches. No goroutine, no dependency, and the bound
//	  is what keeps one unlucky request from paying for 100,000 evictions.
type keyedLimiters struct {
	mu       sync.Mutex
	limiters map[string]*keyedEntry

	// TTL is how long an unused limiter is kept. Long enough that a client's budget survives a gap
	// in its traffic, short enough that a scan's keys are gone quickly.
	ttl time.Duration

	// sweepEvery bounds how often a sweep runs, and sweepBatch how much it does. Together they make
	// the amortised cost of the map constant.
	sweepEvery time.Duration
	sweepBatch int

	lastSweep time.Time
	now       func() time.Time
}

type keyedEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

func newKeyedLimiters() *keyedLimiters {
	return &keyedLimiters{
		limiters:   make(map[string]*keyedEntry),
		ttl:        10 * time.Minute,
		sweepEvery: time.Minute,
		sweepBatch: 1_000,
		now:        time.Now,
	}
}

// get returns the limiter for a key, creating it if needed.
func (k *keyedLimiters) get(key string, limit rate.Limit, burst int) *rate.Limiter {
	now := k.now()

	k.mu.Lock()
	defer k.mu.Unlock()

	k.maybeSweep(now)

	if e, ok := k.limiters[key]; ok {
		e.lastSeen = now
		return e.limiter
	}

	l := rate.NewLimiter(limit, burst)
	k.limiters[key] = &keyedEntry{limiter: l, lastSeen: now}

	return l
}

// maybeSweep drops expired entries, at most sweepBatch at a time and at most once per sweepEvery.
//
// Called with the mutex held.
//
// Ranging over a map and deleting during the range is safe in Go and is specified to be: a key deleted before
// it is reached will not be produced. That is one of the few map guarantees worth knowing, and it is what
// makes the batch limit work without collecting the keys first.
func (k *keyedLimiters) maybeSweep(now time.Time) {
	if now.Sub(k.lastSweep) < k.sweepEvery {
		return
	}

	k.lastSweep = now
	cutoff := now.Add(-k.ttl)

	swept := 0
	for key, e := range k.limiters {
		if swept >= k.sweepBatch {
			break
		}
		if e.lastSeen.Before(cutoff) {
			delete(k.limiters, key)
			swept++
		}
	}
}

// Len reports how many limiters are held, so a test can measure the leak rather than assert it.
func (k *keyedLimiters) Len() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return len(k.limiters)
}

// Len exposes the same thing on TokenBucket, for tests and for a metrics gauge.
func (t *TokenBucket) Len() int { return t.keyed.Len() }
