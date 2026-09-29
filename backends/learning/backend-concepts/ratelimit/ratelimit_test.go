package ratelimit

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// fakeClock is a settable clock, for the tests synctest cannot express.
//
// synctest handles anything where the code under test SLEEPS or waits. These limiters do neither: they read
// the time and return. So a test has to move the clock by hand, and a Clock func is the seam for it.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2025, 6, 1, 11, 59, 0, 0, time.UTC)}
}

func (f *fakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *fakeClock) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
}

// TestFixedWindowAllowsDoubleAtTheBoundary is the failure that makes fixed windows the wrong default.
func TestFixedWindowAllowsDoubleAtTheBoundary(t *testing.T) {
	clock := newFakeClock()

	f := NewFixedWindow(10, time.Minute)
	f.Clock = clock.Now

	ctx := context.Background()

	// One second before the boundary, spend the whole budget.
	clock.mu.Lock()
	clock.now = time.Date(2025, 6, 1, 11, 59, 59, 0, time.UTC)
	clock.mu.Unlock()

	allowed := 0
	for range 20 {
		d, err := f.Allow(ctx, "client")
		if err != nil {
			t.Fatal(err)
		}
		if d.Allowed {
			allowed++
		}
	}

	t.Logf("at 11:59:59, %d of 20 requests allowed", allowed)

	if allowed != 10 {
		t.Fatalf("allowed %d in one window, want 10", allowed)
	}

	// One second later the window resets, and the budget is fresh.
	clock.Advance(time.Second)

	allowedAfter := 0
	for range 20 {
		d, err := f.Allow(ctx, "client")
		if err != nil {
			t.Fatal(err)
		}
		if d.Allowed {
			allowedAfter++
		}
	}

	t.Logf("at 12:00:00, %d more allowed", allowedAfter)
	t.Logf("so %d requests in a two-second span, against a limit of 10 per minute",
		allowed+allowedAfter)

	if allowed+allowedAfter != 20 {
		t.Errorf("expected 20 across the boundary, got %d", allowed+allowedAfter)
	}

	// And the sliding counter, on the same sequence, does not.
	clock2 := newFakeClock()
	s := NewSlidingCounter(10, time.Minute)
	s.Clock = clock2.Now

	clock2.mu.Lock()
	clock2.now = time.Date(2025, 6, 1, 11, 59, 59, 0, time.UTC)
	clock2.mu.Unlock()

	before := 0
	for range 20 {
		d, _ := s.Allow(ctx, "client")
		if d.Allowed {
			before++
		}
	}

	clock2.Advance(time.Second)

	after := 0
	for range 20 {
		d, _ := s.Allow(ctx, "client")
		if d.Allowed {
			after++
		}
	}

	t.Logf("sliding counter across the same boundary: %d then %d, %d total",
		before, after, before+after)

	if before+after > 12 {
		t.Errorf("the sliding counter allowed %d across the boundary, which is more than the "+
			"few percent of slack its estimate should give", before+after)
	}
}

// TestSlidingCounterArithmetic checks the weighting rather than inferring it from decisions.
func TestSlidingCounterArithmetic(t *testing.T) {
	clock := newFakeClock()

	s := NewSlidingCounter(100, time.Minute)
	s.Clock = clock.Now

	ctx := context.Background()

	// 60 requests in the first window.
	clock.mu.Lock()
	clock.now = time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)
	clock.mu.Unlock()

	for range 60 {
		if _, err := s.Allow(ctx, "client"); err != nil {
			t.Fatal(err)
		}
	}

	if got := s.Estimate("client"); got != 60 {
		t.Errorf("inside the window the estimate is %v, want 60", got)
	}

	// 15 seconds into the next window: a quarter elapsed, so three quarters of the previous window
	// still counts. 60 * 0.75 = 45.
	clock.Advance(75 * time.Second)

	got := s.Estimate("client")

	t.Logf("60 requests in the previous window, 15s into the next: the estimate is %.1f", got)

	if got < 44.9 || got > 45.1 {
		t.Errorf("the estimate is %v, want 45", got)
	}

	// 45 seconds in: a quarter of the previous window left. 60 * 0.25 = 15.
	clock.Advance(30 * time.Second)

	got = s.Estimate("client")

	t.Logf("45s into the next window: %.1f", got)

	if got < 14.9 || got > 15.1 {
		t.Errorf("the estimate is %v, want 15", got)
	}

	// A full window later, nothing carries.
	clock.Advance(2 * time.Minute)

	if got := s.Estimate("client"); got != 0 {
		t.Errorf("after a gap the estimate is %v, want 0", got)
	}
}

// TestSlidingLogIsExactAndExpensive measures both halves of the trade.
func TestSlidingLogIsExactAndExpensive(t *testing.T) {
	clock := newFakeClock()

	limit := 10

	s := NewSlidingLog(limit, time.Minute)
	s.Clock = clock.Now

	ctx := context.Background()

	// Spread 10 requests over the first 50 seconds.
	for i := range limit {
		clock.Advance(5 * time.Second)

		d, err := s.Allow(ctx, "client")
		if err != nil {
			t.Fatal(err)
		}
		if !d.Allowed {
			t.Fatalf("request %d was rejected inside the limit", i+1)
		}
	}

	// The eleventh is rejected, and it says exactly when a slot opens. The first hit was at
	// 11:59:05 (the loop advances the clock BEFORE each request), so a slot opens at 12:00:05,
	// and now is 11:59:50. 15 seconds, not the 10 I wrote before counting the off-by-one in my
	// own fixture.
	d, err := s.Allow(ctx, "client")
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("the 11th request: allowed=%v, retry after %v", d.Allowed, d.RetryAfter)

	if d.Allowed {
		t.Error("the 11th request was allowed")
	}
	if d.RetryAfter != 15*time.Second {
		t.Errorf("RetryAfter is %v, want 15s", d.RetryAfter)
	}

	// The memory claim, measured: one timestamp per request inside the window.
	if got := s.Len("client"); got != limit {
		t.Errorf("holding %d timestamps, want %d", got, limit)
	}

	t.Logf("holding %d timestamps for one client at a limit of %d; at 100,000 clients and a "+
		"limit of 1,000 that is 100 million", s.Len("client"), limit)

	// After the window passes, the oldest is dropped rather than accumulating.
	clock.Advance(16 * time.Second)

	if _, err := s.Allow(ctx, "client"); err != nil {
		t.Fatal(err)
	}

	if got := s.Len("client"); got > limit {
		t.Errorf("holding %d timestamps after a slot opened, want at most %d", got, limit)
	}
}

// TestSlidingLogIsTheExactBaseline compares all three in-memory algorithms on one adversarial sequence.
func TestSlidingLogIsTheExactBaseline(t *testing.T) {
	ctx := context.Background()

	// The sequence: all the budget at the end of one window, all of it at the start of the next.
	// This is the pattern an attacker uses and the pattern a cron job produces by accident.
	const limit = 10

	run := func(name string, l Limiter, setClock func(*fakeClock)) int {
		clock := newFakeClock()
		setClock(clock)

		clock.mu.Lock()
		clock.now = time.Date(2025, 6, 1, 11, 59, 59, 0, time.UTC)
		clock.mu.Unlock()

		allowed := 0

		// 20 attempts at 11:59:59, then 20 at 12:00:00.
		for range 20 {
			if d, _ := l.Allow(ctx, "k"); d.Allowed {
				allowed++
			}
		}

		clock.Advance(time.Second)

		for range 20 {
			if d, _ := l.Allow(ctx, "k"); d.Allowed {
				allowed++
			}
		}

		t.Logf("%-16s allowed %2d requests in 2 seconds, against a limit of %d per minute",
			name, allowed, limit)

		return allowed
	}

	fixed := NewFixedWindow(limit, time.Minute)
	log := NewSlidingLog(limit, time.Minute)
	counter := NewSlidingCounter(limit, time.Minute)

	fixedAllowed := run("fixed window", fixed, func(c *fakeClock) { fixed.Clock = c.Now })
	logAllowed := run("sliding log", log, func(c *fakeClock) { log.Clock = c.Now })
	counterAllowed := run("sliding counter", counter, func(c *fakeClock) { counter.Clock = c.Now })

	// The sliding log is exact by construction: never more than the limit in any window.
	if logAllowed != limit {
		t.Errorf("the sliding log allowed %d, and exact means %d", logAllowed, limit)
	}

	// The fixed window allows double.
	if fixedAllowed != 2*limit {
		t.Errorf("the fixed window allowed %d, expected %d", fixedAllowed, 2*limit)
	}

	// The sliding counter is between them, much closer to exact.
	if counterAllowed <= limit {
		t.Logf("the sliding counter was exact on this sequence too")
	}
	if counterAllowed >= 2*limit {
		t.Errorf("the sliding counter allowed %d, no better than a fixed window", counterAllowed)
	}

	t.Logf("exact %d, sliding counter %d (%.0f%% over), fixed window %d (%.0f%% over)",
		logAllowed, counterAllowed, float64(counterAllowed-limit)/float64(limit)*100,
		fixedAllowed, float64(fixedAllowed-limit)/float64(limit)*100)
}

// TestTokenBucketRateUnits is the mistake that compiles.
func TestTokenBucketRateUnits(t *testing.T) {
	ctx := context.Background()

	// 60 per minute with a burst of 5.
	b := NewTokenBucket(60, time.Minute, 5)

	// The burst is spendable immediately.
	allowed := 0
	for range 10 {
		d, err := b.Allow(ctx, "client")
		if err != nil {
			t.Fatal(err)
		}
		if d.Allowed {
			allowed++
		}
	}

	t.Logf("a burst of 5 allowed %d of 10 immediate requests", allowed)

	if allowed != 5 {
		t.Errorf("allowed %d, want 5", allowed)
	}

	// And the refill rate is one per second, not one per minute.
	d, err := b.Allow(ctx, "client")
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("the next request must wait %v", d.RetryAfter.Round(time.Millisecond))

	if d.RetryAfter > 1100*time.Millisecond || d.RetryAfter < 900*time.Millisecond {
		t.Errorf("RetryAfter is %v; 60 per minute is one per second", d.RetryAfter)
	}

	// The mistake: rate.Limit(60) is 60 per SECOND, which is 3,600 per minute.
	wrong := NewTokenBucket(60, time.Second, 5)

	allowedWrong := 0
	for range 10 {
		if dd, _ := wrong.Allow(ctx, "other"); dd.Allowed {
			allowedWrong++
		}
	}

	t.Logf("60 per SECOND with the same burst allows %d immediately, and the difference "+
		"between the two constructor calls is one argument", allowedWrong)
}

// TestRejectingDoesNotConsumeAToken is the bug in the obvious implementation of RetryAfter.
func TestRejectingDoesNotConsumeAToken(t *testing.T) {
	ctx := context.Background()

	b := NewTokenBucket(60, time.Minute, 1)

	// Spend the one token.
	if d, _ := b.Allow(ctx, "client"); !d.Allowed {
		t.Fatal("the first request should be allowed")
	}

	// Ask 50 times while rejected. Each ask calls ReserveN to compute RetryAfter, and if the
	// reservation is not cancelled each one books a future token, so the wait grows to 50 seconds
	// and the client can never get in.
	var last time.Duration
	for range 50 {
		d, err := b.Allow(ctx, "client")
		if err != nil {
			t.Fatal(err)
		}
		if d.Allowed {
			t.Fatal("a token appeared from nowhere")
		}
		last = d.RetryAfter
	}

	t.Logf("after 50 rejected attempts the wait is still %v", last.Round(10*time.Millisecond))

	if last > 2*time.Second {
		t.Errorf("the wait grew to %v, so the rejected attempts consumed tokens; "+
			"Reserve needs CancelAt", last)
	}
}

// TestWaitBlocksUntilATokenArrives uses synctest, because this is the one method that actually sleeps.
func TestWaitBlocksUntilATokenArrives(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// 10 per second, burst 1. So the second Wait should take 100ms of fake time.
		b := NewTokenBucket(10, time.Second, 1)

		ctx := context.Background()

		start := time.Now()

		for i := range 5 {
			if err := b.Wait(ctx, "client"); err != nil {
				t.Fatalf("wait %d: %v", i, err)
			}
		}

		elapsed := time.Since(start)

		t.Logf("five Waits at 10 per second took %v of fake time", elapsed)

		// The first is free (the burst), the other four are 100ms apart.
		if elapsed != 400*time.Millisecond {
			t.Errorf("took %v, want exactly 400ms", elapsed)
		}
	})
}

// TestWaitRespectsTheContext, because a blocked Wait with no deadline is a request that never returns.
func TestWaitRespectsTheContext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := NewTokenBucket(1, time.Minute, 1)

		ctx := context.Background()

		// Spend the token.
		if err := b.Wait(ctx, "client"); err != nil {
			t.Fatal(err)
		}

		// The next one would wait a minute.
		short, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		defer cancel()

		start := time.Now()
		err := b.Wait(short, "client")
		elapsed := time.Since(start)

		t.Logf("Wait with a 50ms deadline returned after %v: %v", elapsed, err)

		if err == nil {
			t.Fatal("expected the deadline to fire")
		}

		// rate.Limiter is better than "wait then fail": it notices up front that the token
		// cannot arrive before the deadline and returns immediately.
		if elapsed != 0 {
			t.Errorf("waited %v; rate.Limiter should refuse immediately when the token "+
				"cannot arrive in time", elapsed)
		}
		// This assertion is the reason TokenBucket.Wait joins the context's error.
		// rate.Limiter returns "rate: Wait(n=1) would exceed context deadline", which is a
		// plain error and does not wrap context.DeadlineExceeded, so errors.Is on the raw
		// error is false. A caller checking for a deadline the normal way would miss it.
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("got %v, which does not match context.DeadlineExceeded", err)
		}
	})
}

// TestKeyedLimitersAreSwept is the memory leak, measured under a sustained scan.
//
// The first version evicted at most 1,000 keys per sweep, once a minute, and its test only checked that a
// one-off burst eventually drained. A scan that brings more than 1,000 new keys a minute outruns that forever.
// Here 2,000 new keys arrive every minute for an hour. Evicting everything past its TTL on each sweep holds the
// map at about one TTL's worth of keys; the batched version kept growing and ended past 60,000.
func TestKeyedLimitersAreSwept(t *testing.T) {
	ctx := context.Background()

	b := NewTokenBucket(60, time.Minute, 5)

	clock := newFakeClock()
	b.keyed.now = clock.Now
	b.keyed.lastSweep = clock.Now()

	const perMinute = 2_000

	peak := 0
	for minute := range 60 {
		for i := range perMinute {
			if _, err := b.Allow(ctx, "ip-"+itoa(minute*perMinute+i)); err != nil {
				t.Fatal(err)
			}
		}
		peak = max(peak, b.Len())
		clock.Advance(time.Minute)
	}

	t.Logf("peak after an hour of %d new keys a minute: %d limiters", perMinute, peak)

	// A TTL of 10 minutes and a sweep every minute keep at most 11 minutes of keys, plus the minute in progress.
	if limit := 12 * perMinute; peak > limit {
		t.Errorf("peak %d limiters; a TTL sweep should hold at most about %d", peak, limit)
	}
}

// TestAlgorithmLimitersForgetIdleKeys: the fixed window, sliding log and sliding counter each keep a map entry
// per key, and the first versions never removed one. 1,000 clients that each made one request stayed in memory
// for as long as the limiter lived.
func TestAlgorithmLimitersForgetIdleKeys(t *testing.T) {
	ctx := context.Background()
	clock := newFakeClock()

	fixed := NewFixedWindow(10, time.Minute)
	fixed.Clock = clock.Now
	log := NewSlidingLog(10, time.Minute)
	log.Clock = clock.Now
	counter := NewSlidingCounter(10, time.Minute)
	counter.Clock = clock.Now

	for i := range 1_000 {
		key := "ip-" + itoa(i)
		for _, l := range []Limiter{fixed, log, counter} {
			if _, err := l.Allow(ctx, key); err != nil {
				t.Fatal(err)
			}
		}
	}

	// Past every window, then one request from someone else triggers the sweep.
	clock.Advance(3 * time.Minute)
	for _, l := range []Limiter{fixed, log, counter} {
		if _, err := l.Allow(ctx, "a-real-client"); err != nil {
			t.Fatal(err)
		}
	}

	for name, n := range map[string]int{
		"FixedWindow":    len(fixed.windows),
		"SlidingLog":     len(log.hits),
		"SlidingCounter": len(counter.windows),
	} {
		if n != 1 {
			t.Errorf("%s holds %d keys after every window passed, want 1", name, n)
		}
	}
}

// TestFailOpenIsAnExplicitDecision.
func TestFailOpenIsAnExplicitDecision(t *testing.T) {
	ctx := context.Background()

	broken := brokenLimiter{errors.New("redis: connection refused")}

	var logged []error

	open := FailOpen{Limiter: broken, Open: true, OnError: func(err error) { logged = append(logged, err) }}
	closed := FailOpen{Limiter: broken, Open: false, OnError: func(err error) { logged = append(logged, err) }}

	d, err := open.Allow(ctx, "k")
	if err != nil {
		t.Fatalf("fail-open should not return an error: %v", err)
	}
	if !d.Allowed {
		t.Error("fail-open rejected a request")
	}

	d, err = closed.Allow(ctx, "k")
	if err != nil {
		t.Fatalf("fail-closed should not return an error either: %v", err)
	}
	if d.Allowed {
		t.Error("fail-closed allowed a request")
	}
	if d.RetryAfter == 0 {
		t.Error("fail-closed should tell the client when to come back")
	}

	if len(logged) != 2 {
		t.Errorf("the error reached OnError %d times, want 2", len(logged))
	}

	t.Logf("both policies swallow the error and report it: %v", logged[0])
	t.Log("a limiter that fails silently is indistinguishable from one that works, which is " +
		"why OnError is not optional in practice")
}

type brokenLimiter struct{ err error }

func (b brokenLimiter) Allow(context.Context, string) (Decision, error) { return Decision{}, b.err }

// TestMiddlewareHeadersAndStatus.
func TestMiddlewareHeadersAndStatus(t *testing.T) {
	clock := newFakeClock()

	f := NewFixedWindow(2, time.Minute)
	f.Clock = clock.Now

	handler := Middleware(f, func(r *http.Request) string { return r.Header.Get("X-Api-Key") })(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
		}))

	req := func() *http.Request {
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("X-Api-Key", "key-1")
		return r
	}

	for i := 1; i <= 2; i++ {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req())

		t.Logf("request %d: %d, limit=%s remaining=%s reset=%ss",
			i, rec.Code,
			rec.Header().Get("RateLimit-Limit"),
			rec.Header().Get("RateLimit-Remaining"),
			rec.Header().Get("RateLimit-Reset"))

		if rec.Code != http.StatusOK {
			t.Errorf("request %d got %d, want 200", i, rec.Code)
		}
		if got, want := rec.Header().Get("RateLimit-Remaining"), itoa(2-i); got != want {
			t.Errorf("request %d: remaining=%q, want %q", i, got, want)
		}
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req())

	t.Logf("request 3: %d, Retry-After=%s, body=%s",
		rec.Code, rec.Header().Get("Retry-After"), rec.Body.String())

	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("got %d, want 429", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("a 429 without Retry-After leaves the client guessing")
	}
	if got := rec.Header().Get("Content-Type"); got != "application/problem+json" {
		t.Errorf("Content-Type is %q", got)
	}

	// A different key has its own budget, which is the whole reason KeyFunc is a parameter.
	other := httptest.NewRequest("GET", "/", nil)
	other.Header.Set("X-Api-Key", "key-2")

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, other)

	if rec.Code != http.StatusOK {
		t.Errorf("a different key got %d, want 200", rec.Code)
	}
}

// TestRetryAfterRoundsUp, because rounding down tells a client to retry too early.
func TestRetryAfterRoundsUp(t *testing.T) {
	for _, tc := range []struct {
		retryAfter time.Duration
		want       string
	}{
		{1500 * time.Millisecond, "2"},
		{1000 * time.Millisecond, "1"},
		{100 * time.Millisecond, "1"},
		{1 * time.Nanosecond, "1"},
		{59500 * time.Millisecond, "60"},
	} {
		h := http.Header{}

		Decision{Allowed: false, RetryAfter: tc.retryAfter, Limit: 10}.WriteHeaders(h)

		if got := h.Get("Retry-After"); got != tc.want {
			t.Errorf("%v gave Retry-After: %q, want %q", tc.retryAfter, got, tc.want)
		}
	}

	// An allowed decision sets no Retry-After at all.
	h := http.Header{}
	Decision{Allowed: true, Limit: 10, Remaining: 9}.WriteHeaders(h)

	if got := h.Get("Retry-After"); got != "" {
		t.Errorf("an allowed request has Retry-After: %q", got)
	}

	// And Remaining never goes negative in a header, whatever the arithmetic produced.
	h = http.Header{}
	Decision{Allowed: false, Limit: 10, Remaining: -3}.WriteHeaders(h)

	if got := h.Get("RateLimit-Remaining"); got != "0" {
		t.Errorf("RateLimit-Remaining is %q for a negative remaining, want 0", got)
	}
}

// TestConcurrentAllowIsRaceFree, run under -race in CI.
func TestConcurrentAllowIsRaceFree(t *testing.T) {
	ctx := context.Background()

	limiters := map[string]Limiter{
		"fixed window":    NewFixedWindow(1_000, time.Minute),
		"sliding log":     NewSlidingLog(1_000, time.Minute),
		"sliding counter": NewSlidingCounter(1_000, time.Minute),
		"token bucket":    NewTokenBucket(1_000, time.Minute, 100),
	}

	for name, l := range limiters {
		t.Run(name, func(t *testing.T) {
			var wg sync.WaitGroup

			for g := range 20 {
				wg.Add(1)
				go func() {
					defer wg.Done()

					for range 50 {
						if _, err := l.Allow(ctx, "shared"); err != nil {
							t.Errorf("goroutine %d: %v", g, err)
							return
						}
					}
				}()
			}

			wg.Wait()
		})
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}

	neg := n < 0
	if neg {
		n = -n
	}

	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}

	if neg {
		return "-" + string(digits)
	}
	return string(digits)
}

// TestDefaultKeyIsTheAddressNotTheConnection: RemoteAddr is "ip:port", and a client gets a new source port with
// every new connection. The first version keyed on RemoteAddr itself, so reconnecting was a fresh budget and a
// per-IP limit limited nothing.
func TestDefaultKeyIsTheAddressNotTheConnection(t *testing.T) {
	h := Middleware(NewFixedWindow(1, time.Minute), nil)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	codes := make([]int, 0, 2)
	for _, addr := range []string{"203.0.113.7:50001", "203.0.113.7:50002"} {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = addr

		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		codes = append(codes, w.Code)
	}

	if codes[1] != http.StatusTooManyRequests {
		t.Errorf("the same IP on a second connection got %v; the port must not be part of the key", codes)
	}

	// IPv6, where the address itself contains colons.
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "[2001:db8::1]:443"
	if got := remoteIP(r); got != "2001:db8::1" {
		t.Errorf("remoteIP([2001:db8::1]:443) = %q", got)
	}
}

// TestTokenBucketUsesItsClock refills a bucket by moving a fake clock rather than sleeping.
func TestTokenBucketUsesItsClock(t *testing.T) {
	ctx := context.Background()
	clock := newFakeClock()

	b := NewTokenBucket(60, time.Minute, 1) // one token a second, burst of one
	b.Clock = clock.Now

	if d, _ := b.Allow(ctx, "k"); !d.Allowed {
		t.Fatal("the first request was refused")
	}
	if d, _ := b.Allow(ctx, "k"); d.Allowed {
		t.Fatal("a second request in the same instant was allowed with a burst of one")
	}

	clock.Advance(time.Second)

	if d, _ := b.Allow(ctx, "k"); !d.Allowed {
		t.Error("one second on the Clock did not refill the token; Allow is not reading the Clock")
	}
}
