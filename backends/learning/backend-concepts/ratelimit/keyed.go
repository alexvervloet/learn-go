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
//	a TTL swept lazily, on write, at most once per interval. No goroutine and no dependency.
//
// The sweep evicts EVERY expired entry. The first version capped it at 1,000 per sweep to spare
// the unlucky request that runs it, and that cap is a leak with extra steps: a scan bringing more
// than 1,000 new keys a minute outruns it forever (TestKeyedLimitersAreSwept measured 71,000 held
// after an hour at 2,000 a minute). The honest cost is a walk over every live entry once per
// interval, paid by one request, and in exchange the map never holds much more than one TTL's
// worth of distinct keys.
type keyedLimiters struct {
	mu       sync.Mutex
	limiters map[string]*keyedEntry

	// TTL is how long an unused limiter is kept. Long enough that a client's budget survives a gap
	// in its traffic, short enough that a scan's keys are gone quickly.
	ttl time.Duration

	// sweepEvery bounds how often a sweep runs.
	sweepEvery time.Duration

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

// maybeSweep drops every expired entry, at most once per sweepEvery. Called with the mutex held.
func (k *keyedLimiters) maybeSweep(now time.Time) {
	cutoff := now.Add(-k.ttl)

	sweepExpired(&k.lastSweep, now, k.sweepEvery, k.limiters, func(e *keyedEntry) bool {
		return e.lastSeen.Before(cutoff)
	})
}

// sweepExpired deletes the entries of m for which expired returns true, if at least every has
// passed since *last. Called with the owning lock held.
//
// Ranging over a map and deleting during the range is safe in Go and is specified to be: a key
// deleted before it is reached will not be produced. That is one of the few map guarantees worth
// knowing, and it is what lets this delete as it goes without collecting the keys first.
func sweepExpired[V any](last *time.Time, now time.Time, every time.Duration, m map[string]V, expired func(V) bool) {
	if now.Sub(*last) < every {
		return
	}
	*last = now

	for key, v := range m {
		if expired(v) {
			delete(m, key)
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
