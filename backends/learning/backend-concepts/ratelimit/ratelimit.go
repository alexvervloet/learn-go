// Package ratelimit is four algorithms, the one Go ships, and the one thing they all get wrong at a
// boundary.
//
// # The four shapes
//
//	fixed window     count requests per calendar minute, reset at :00. Simplest, and allows 2x the
//	                 limit across a boundary: 100 at 11:59:59 and 100 at 12:00:00 is 200 in one
//	                 second. TestFixedWindowAllowsDoubleAtTheBoundary measures it.
//	sliding log      keep a timestamp per request, count the ones inside the window. Exact, and
//	                 costs memory proportional to the limit times the number of clients.
//	sliding counter  weight the previous window by how much of it is still in view. Almost exact,
//	                 constant memory, and what most production limiters use.
//	token bucket     refill at a steady rate, spend one token per request. Allows a BURST up to the
//	                 bucket size, which is usually what you want, and is what golang.org/x/time/rate
//	                 implements.
//
// # Why x/time/rate is the default answer in Go
//
// It is in the extended standard library, it is a token bucket, and `Wait(ctx)` blocks instead of failing,
// which is the right behaviour for a client calling someone else's API. `Allow()` is the right behaviour for
// a server deciding whether to answer.
//
// What it does not do: share state. A rate.Limiter is per process, so five replicas with a limit of 100 each
// enforce a limit of 500. That is the single most common rate limiting bug in a deployed service and it
// cannot be found in a test that runs one process. The Redis limiter here is the fix, and the test measures
// the difference.
//
// # The decisions that matter more than the algorithm
//
// WHAT to key on. Per IP punishes offices and mobile carriers behind one NAT, per user cannot limit a
// signup endpoint because there is no user yet, and per API key is right when there is one. Usually it is
// several limits at once: a loose one per IP, a tight one per account, a very tight one per endpoint.
//
// WHAT to return. 429 with `Retry-After`, and `RateLimit-Limit`, `RateLimit-Remaining` and `RateLimit-Reset`
// headers so a well-behaved client can pace itself rather than discovering the limit by hitting it.
//
// WHETHER to fail open. If Redis is down, does every request fail or does every request pass? Failing closed
// turns a cache outage into a total outage. Failing open means an attacker who can break Redis has no limit
// at all. The answer is per endpoint, and the code has to make it explicit, which is what FailOpen below is.
package ratelimit

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// Decision is what a limiter returns.
//
// A struct rather than a bool, because a 429 needs the numbers for its headers and computing them twice is
// how the headers end up disagreeing with the decision.
type Decision struct {
	Allowed bool

	// Limit is the ceiling, Remaining what is left after this request.
	Limit     int
	Remaining int

	// RetryAfter is how long until the next request would be allowed. Zero when allowed.
	RetryAfter time.Duration

	// ResetIn is how long until the window or bucket is fully available again.
	//
	// A DURATION, not a deadline, and that was a bug before it was a design decision. The first
	// version had `ResetAt time.Time` and WriteHeaders called time.Until on it. Under a test with
	// an injected clock that mixes two clocks in one calculation, and the RateLimit-Reset header
	// came out as -41721007 seconds. A header helper that reads the wall clock cannot be tested
	// and cannot be correct in a limiter whose time is a parameter.
	//
	// So the limiter, which knows what time it is, does the subtraction. WriteHeaders only formats.
	ResetIn time.Duration

	// ResetAt is when the window or bucket is fully available again, kept for a caller that wants
	// to log or cache against an absolute instant.
	ResetAt time.Time
}

// WriteHeaders sets the standard rate limit headers.
//
// The draft IETF names (RateLimit-Limit and so on) rather than the X- prefixed ones, which are what most APIs
// still send. Both are in the wild; sending the unprefixed set is the direction the standard went and
// clients that only know the X- names are not broken by it, they just ignore these.
//
// Retry-After is the one that matters, because it is the only one with an RFC behind it and the only one a
// generic HTTP client library knows how to obey. Seconds rather than a date, and rounded UP: rounding down
// tells the client to retry before the limit has cleared, which produces a second 429 and, for a client with
// exponential backoff, a longer wait than telling the truth.
func (d Decision) WriteHeaders(h http.Header) {
	h.Set("RateLimit-Limit", strconv.Itoa(d.Limit))
	h.Set("RateLimit-Remaining", strconv.Itoa(max(d.Remaining, 0)))

	if d.ResetIn > 0 {
		h.Set("RateLimit-Reset", strconv.Itoa(ceilSeconds(d.ResetIn)))
	}

	if !d.Allowed && d.RetryAfter > 0 {
		h.Set("Retry-After", strconv.Itoa(ceilSeconds(d.RetryAfter)))
	}
}

// ceilSeconds rounds a duration UP to whole seconds, with a floor of one.
//
// Up, because rounding down tells the client to retry before the limit has cleared, which produces a second
// 429 and, for a client with exponential backoff, a longer total wait than telling the truth. A floor of one,
// because `Retry-After: 0` means "retry immediately" and that is never what a limiter means.
func ceilSeconds(d time.Duration) int {
	seconds := int(d / time.Second)
	if time.Duration(seconds)*time.Second < d {
		seconds++
	}
	if seconds < 1 {
		seconds = 1
	}
	return seconds
}

// Limiter is what every algorithm here implements.
//
// It takes a key, so one Limiter serves every client, and a context, so the Redis implementation can respect
// a deadline. The in-memory ones ignore it, and taking it anyway is what lets the two be swapped.
type Limiter interface {
	Allow(ctx context.Context, key string) (Decision, error)
}

// Clock is the seam that makes these testable.
//
// Every limiter here reads the time through this rather than calling time.Now, because a rate limiter is a
// function of time and a test that cannot control time has to sleep. A test that sleeps for the window is
// slow; a test that sleeps for slightly less than the window is flaky.
//
// Go 1.25's testing/synctest solves this differently, by making the whole goroutine tree see a fake clock,
// and the tests here use it where it fits. A Clock field is still worth having for the cases synctest cannot
// reach, and for production code that wants to inject a monotonic source.
type Clock func() time.Time

func (c Clock) now() time.Time {
	if c == nil {
		return time.Now()
	}
	return c()
}

// FixedWindow counts requests per aligned window.
//
// The simplest thing that works, and the reason to know it is that its failure is not subtle: it allows up to
// 2x the limit in a span of one window length, straddling the boundary. For a limit of 100 per minute that is
// 200 requests in two seconds, which for an expensive endpoint is the difference between fine and an
// incident.
type FixedWindow struct {
	Limit  int
	Window time.Duration
	Clock  Clock

	mu      sync.Mutex
	windows map[string]*fixedCounter
}

type fixedCounter struct {
	start time.Time
	count int
}

// NewFixedWindow builds one.
func NewFixedWindow(limit int, window time.Duration) *FixedWindow {
	return &FixedWindow{
		Limit:   limit,
		Window:  window,
		windows: make(map[string]*fixedCounter),
	}
}

// Allow implements Limiter.
func (f *FixedWindow) Allow(_ context.Context, key string) (Decision, error) {
	if f.Limit <= 0 || f.Window <= 0 {
		return Decision{}, errors.New("ratelimit: FixedWindow needs a positive limit and window")
	}

	now := f.Clock.now()

	// Truncate aligns windows to the wall clock, which is what "100 per minute" usually means and is
	// also what makes the boundary problem worse: every client's window resets at the same instant,
	// so the burst is synchronised across all of them.
	start := now.Truncate(f.Window)

	f.mu.Lock()
	defer f.mu.Unlock()

	c, ok := f.windows[key]
	if !ok || !c.start.Equal(start) {
		c = &fixedCounter{start: start}
		f.windows[key] = c
	}

	resetAt := start.Add(f.Window)

	if c.count >= f.Limit {
		return Decision{
			Limit:      f.Limit,
			Remaining:  0,
			RetryAfter: resetAt.Sub(now),
			ResetIn:    resetAt.Sub(now),
			ResetAt:    resetAt,
		}, nil
	}

	c.count++

	return Decision{
		Allowed:   true,
		Limit:     f.Limit,
		Remaining: f.Limit - c.count,
		ResetIn:   resetAt.Sub(now),
		ResetAt:   resetAt,
	}, nil
}

// SlidingLog keeps a timestamp per request and counts the ones still inside the window.
//
// Exact: it can never allow more than Limit requests in any span of Window, which no other algorithm here
// can say. The cost is memory, Limit timestamps per key, which for 100,000 keys at a limit of 1,000 is
// 100 million timestamps and not a rate limiter any more.
//
// Worth knowing because it is the correctness baseline the others are measured against, and because at small
// limits (10 per minute on a login endpoint) it is cheap and exactly right.
type SlidingLog struct {
	Limit  int
	Window time.Duration
	Clock  Clock

	mu   sync.Mutex
	hits map[string][]time.Time
}

// NewSlidingLog builds one.
func NewSlidingLog(limit int, window time.Duration) *SlidingLog {
	return &SlidingLog{
		Limit:  limit,
		Window: window,
		hits:   make(map[string][]time.Time),
	}
}

// Allow implements Limiter.
func (s *SlidingLog) Allow(_ context.Context, key string) (Decision, error) {
	if s.Limit <= 0 || s.Window <= 0 {
		return Decision{}, errors.New("ratelimit: SlidingLog needs a positive limit and window")
	}

	now := s.Clock.now()
	cutoff := now.Add(-s.Window)

	s.mu.Lock()
	defer s.mu.Unlock()

	times := s.hits[key]

	// Drop everything older than the window. The slice is sorted, so this is a prefix, and reslicing
	// rather than filtering keeps it allocation-free in the steady state.
	drop := 0
	for drop < len(times) && !times[drop].After(cutoff) {
		drop++
	}
	times = times[drop:]

	if len(times) >= s.Limit {
		// The oldest hit leaving the window is when a slot opens.
		resetAt := times[0].Add(s.Window)

		s.hits[key] = times

		return Decision{
			Limit:      s.Limit,
			Remaining:  0,
			RetryAfter: resetAt.Sub(now),
			ResetIn:    resetAt.Sub(now),
			ResetAt:    resetAt,
		}, nil
	}

	times = append(times, now)
	s.hits[key] = times

	return Decision{
		Allowed:   true,
		Limit:     s.Limit,
		Remaining: s.Limit - len(times),
		ResetIn:   s.Window,
		ResetAt:   now.Add(s.Window),
	}, nil
}

// Len reports how many timestamps are being held, so a test can measure the memory claim rather than assert
// it.
func (s *SlidingLog) Len(key string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.hits[key])
}

// SlidingCounter is the production compromise: two counters per key, weighted by how much of the previous
// window is still in view.
//
// The estimate is `previous * (1 - elapsed/window) + current`. It is not exact, and the direction of the
// error is worth knowing: it assumes the previous window's requests were spread EVENLY, so a client that
// sent all of them in the last second of it is under-counted and a client that sent them in the first second
// is over-counted. Errors of a few percent, constant memory, and no boundary doubling.
//
// This is what Cloudflare described in 2017 and what most limiters implement now.
type SlidingCounter struct {
	Limit  int
	Window time.Duration
	Clock  Clock

	mu      sync.Mutex
	windows map[string]*slidingPair
}

type slidingPair struct {
	start    time.Time
	current  int
	previous int
}

// NewSlidingCounter builds one.
func NewSlidingCounter(limit int, window time.Duration) *SlidingCounter {
	return &SlidingCounter{
		Limit:   limit,
		Window:  window,
		windows: make(map[string]*slidingPair),
	}
}

// Allow implements Limiter.
func (s *SlidingCounter) Allow(_ context.Context, key string) (Decision, error) {
	if s.Limit <= 0 || s.Window <= 0 {
		return Decision{}, errors.New("ratelimit: SlidingCounter needs a positive limit and window")
	}

	now := s.Clock.now()
	start := now.Truncate(s.Window)

	s.mu.Lock()
	defer s.mu.Unlock()

	p, ok := s.windows[key]
	if !ok {
		p = &slidingPair{start: start}
		s.windows[key] = p
	}

	switch {
	case p.start.Equal(start):
		// Same window, nothing to roll.
	case start.Sub(p.start) == s.Window:
		// The next window: what was current becomes previous.
		p.previous, p.current, p.start = p.current, 0, start
	default:
		// A gap of more than one window, so there is nothing to carry.
		p.previous, p.current, p.start = 0, 0, start
	}

	elapsed := now.Sub(start)
	weight := 1 - float64(elapsed)/float64(s.Window)

	estimate := float64(p.previous)*weight + float64(p.current)

	resetAt := start.Add(s.Window)

	if estimate >= float64(s.Limit) {
		return Decision{
			Limit:      s.Limit,
			Remaining:  0,
			RetryAfter: resetAt.Sub(now),
			ResetIn:    resetAt.Sub(now),
			ResetAt:    resetAt,
		}, nil
	}

	p.current++

	return Decision{
		Allowed:   true,
		Limit:     s.Limit,
		Remaining: s.Limit - int(estimate) - 1,
		ResetIn:   resetAt.Sub(now),
		ResetAt:   resetAt,
	}, nil
}

// Estimate exposes the weighted count, so a test can check the arithmetic rather than infer it from
// decisions.
func (s *SlidingCounter) Estimate(key string) float64 {
	now := s.Clock.now()
	start := now.Truncate(s.Window)

	s.mu.Lock()
	defer s.mu.Unlock()

	p, ok := s.windows[key]
	if !ok {
		return 0
	}

	previous := p.previous
	current := p.current

	if !p.start.Equal(start) {
		if start.Sub(p.start) == s.Window {
			previous, current = current, 0
		} else {
			previous, current = 0, 0
		}
	}

	weight := 1 - float64(now.Sub(start))/float64(s.Window)

	return float64(previous)*weight + float64(current)
}

// FailOpen wraps a Limiter and decides what happens when it errors.
//
// The decision has to be explicit and per endpoint, which is the whole point of making it a type. Failing
// closed on a login endpoint is correct: a Redis outage should not open the door to credential stuffing.
// Failing closed on a read endpoint turns a cache outage into a site outage.
//
// The field is named for what it DOES rather than for a policy, because `FailOpen: true` at a call site reads
// as the decision it is and `Policy: PolicyLenient` does not.
type FailOpen struct {
	Limiter Limiter
	Open    bool

	// OnError is called with the error so it reaches the logs. A limiter silently failing open is
	// indistinguishable from a limiter working, which is the worst of the options.
	OnError func(error)
}

// Allow implements Limiter.
func (f FailOpen) Allow(ctx context.Context, key string) (Decision, error) {
	d, err := f.Limiter.Allow(ctx, key)
	if err == nil {
		return d, nil
	}

	if f.OnError != nil {
		f.OnError(err)
	}

	if f.Open {
		// Allowed, and Limit/Remaining/ResetIn are left at zero so the headers do not claim a
		// budget the limiter could not compute.
		return Decision{Allowed: true}, nil
	}

	return Decision{
		Allowed:    false,
		RetryAfter: time.Second,
	}, nil
}

// Middleware turns a Limiter into HTTP middleware.
//
// KeyFunc is a parameter because the key is the design decision, not the algorithm. A single implementation
// serves per-IP, per-user and per-endpoint limiting; hard-coding the IP inside would not.
func Middleware(l Limiter, keyFunc func(*http.Request) string) func(http.Handler) http.Handler {
	if keyFunc == nil {
		keyFunc = func(r *http.Request) string { return r.RemoteAddr }
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := keyFunc(r)

			d, err := l.Allow(r.Context(), key)
			if err != nil {
				// A limiter that errors without a FailOpen wrapper is a programming
				// mistake, not a runtime condition, so this is a 500 rather than a
				// guess at the right policy.
				http.Error(w, "rate limiter unavailable", http.StatusInternalServerError)
				return
			}

			d.WriteHeaders(w.Header())

			if !d.Allowed {
				// 429, and the body is problem+json to match the response package in
				// http-tutorial. A plain-text 429 is fine; being consistent across a
				// service is what lets a client handle errors in one place.
				w.Header().Set("Content-Type", "application/problem+json")
				w.WriteHeader(http.StatusTooManyRequests)

				// The same rounding as the Retry-After header, because a body and a
				// header that disagree about the wait is the kind of detail a client
				// author notices and nobody fixes.
				// The error is captured rather than ignored, and there is nothing to do
				// with it: the status is already written, so the response cannot change.
				// Discarding it explicitly says that was a decision.
				if _, err := fmt.Fprintf(w,
					`{"type":"about:blank","title":"Too Many Requests",`+
						`"status":429,"detail":"retry in %ds"}`+"\n",
					ceilSeconds(d.RetryAfter)); err != nil {
					_ = err
				}
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
