package ratelimit

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/alexvervloet/learn-go/backends/learning/backend-concepts/internal/redistest"
)

// TestPerProcessLimitersMultiply is the bug the Redis limiter exists to fix, and it cannot be found in a
// test that only ever builds one limiter.
//
// Five replicas, each with a limit of 10, enforce a limit of 50. Nothing in the code is wrong. Every unit
// test passes. The only way to see it is to construct the replicas.
func TestPerProcessLimitersMultiply(t *testing.T) {
	ctx := context.Background()

	const (
		replicas = 5
		limit    = 10
	)

	// Five processes, modelled as five limiters. A load balancer spreads requests across them, so
	// each sees a fifth of the traffic and allows its own full budget.
	replicaLimiters := make([]Limiter, replicas)
	for i := range replicaLimiters {
		replicaLimiters[i] = NewFixedWindow(limit, time.Minute)
	}

	allowed := 0
	for i := range replicas * limit * 2 {
		// Round robin, which is what a load balancer does.
		d, err := replicaLimiters[i%replicas].Allow(ctx, "one-client")
		if err != nil {
			t.Fatal(err)
		}
		if d.Allowed {
			allowed++
		}
	}

	t.Logf("%d replicas with a per-process limit of %d allowed %d requests for ONE client",
		replicas, limit, allowed)

	if allowed != replicas*limit {
		t.Errorf("allowed %d, expected %d (%d replicas times %d)",
			allowed, replicas*limit, replicas, limit)
	}

	t.Logf("the configured limit was %d per minute and the enforced limit is %d", limit, allowed)

	// Now the same five replicas sharing one Redis limiter.
	client := redistest.Client(t)
	redistest.Flush(t)

	t.Logf("using Redis database %d", redistest.Database())

	shared := make([]Limiter, replicas)
	for i := range shared {
		// Five separate limiter objects, exactly as five processes would have, pointing at one
		// Redis.
		shared[i] = NewRedisLimiter(client, limit, time.Minute, "rl-test-multiply")
	}

	sharedAllowed := 0
	for i := range replicas * limit * 2 {
		d, err := shared[i%replicas].Allow(ctx, "one-client")
		if err != nil {
			t.Fatal(err)
		}
		if d.Allowed {
			sharedAllowed++
		}
	}

	t.Logf("the same five replicas sharing one Redis allowed %d", sharedAllowed)

	// The sliding counter's estimate is not exact, so the assertion has slack. It must be nowhere
	// near 5x.
	if sharedAllowed > limit+2 {
		t.Errorf("the shared limiter allowed %d, which is more than the limit of %d plus the "+
			"few requests its estimate can be out by", sharedAllowed, limit)
	}
	if sharedAllowed < limit-2 {
		t.Errorf("the shared limiter allowed only %d of %d", sharedAllowed, limit)
	}
}

// TestRedisLimiterIsAtomic is the lost update from the transactions package, at a different layer.
//
// Without the Lua script, two replicas both GET 9, both allow, both SET 10, and a limit of 10 allowed 11
// requests. With it, the read and the write cannot interleave.
//
// 200 concurrent requests is enough to find a non-atomic implementation reliably and fast.
func TestRedisLimiterIsAtomic(t *testing.T) {
	client := redistest.Client(t)
	redistest.Flush(t)

	ctx := context.Background()

	const limit = 50

	l := NewRedisLimiter(client, limit, time.Minute, "rl-test-atomic")

	var (
		mu      sync.Mutex
		allowed int
		wg      sync.WaitGroup
	)

	for range 200 {
		wg.Add(1)
		go func() {
			defer wg.Done()

			d, err := l.Allow(ctx, "client")
			if err != nil {
				t.Errorf("allow: %v", err)
				return
			}

			if d.Allowed {
				mu.Lock()
				allowed++
				mu.Unlock()
			}
		}()
	}

	wg.Wait()

	t.Logf("200 concurrent requests against a limit of %d: %d allowed", limit, allowed)

	// Exactly the limit. The script is atomic, so there is no slack to allow for here: a count
	// that is off by even one means the read and the write interleaved.
	if allowed != limit {
		t.Errorf("allowed %d, want exactly %d; the script is not atomic", allowed, limit)
	}
}

// TestRedisKeysExpireOnTheirOwn, because a rate limiter that leaks keys fills Redis.
func TestRedisKeysExpireOnTheirOwn(t *testing.T) {
	client := redistest.Client(t)
	redistest.Flush(t)

	ctx := context.Background()

	// A short window, so the TTL is observable without a long test.
	l := NewRedisLimiter(client, 5, 2*time.Second, "rl-test-ttl")

	if _, err := l.Allow(ctx, "client"); err != nil {
		t.Fatal(err)
	}

	keys, err := client.Keys(ctx, "rl-test-ttl:*").Result()
	if err != nil {
		t.Fatal(err)
	}

	if len(keys) != 1 {
		t.Fatalf("expected one key, got %v", keys)
	}

	ttl, err := client.PTTL(ctx, keys[0]).Result()
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("%s has a TTL of %v", keys[0], ttl)

	// Two windows of TTL, so the previous window survives long enough to be weighted.
	if ttl <= 2*time.Second || ttl > 4*time.Second {
		t.Errorf("TTL is %v, expected between one and two windows", ttl)
	}

	// And it is NOT extended by later requests in the same window, which is the INCR-then-EXPIRE
	// bug: refreshing the TTL on every request turns a fixed window into one that never ends for a
	// busy client.
	time.Sleep(300 * time.Millisecond)

	if _, err := l.Allow(ctx, "client"); err != nil {
		t.Fatal(err)
	}

	after, err := client.PTTL(ctx, keys[0]).Result()
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("after a second request 300ms later, the TTL is %v", after)

	if after >= ttl {
		t.Errorf("the TTL went from %v to %v; a later request must not extend it", ttl, after)
	}
}

// TestRedisAndInMemoryAgree, so the Redis version can be swapped in without changing behaviour.
func TestRedisAndInMemoryAgree(t *testing.T) {
	client := redistest.Client(t)
	redistest.Flush(t)

	ctx := context.Background()

	const limit = 20

	redisLimiter := NewRedisLimiter(client, limit, time.Minute, "rl-test-agree")
	memory := NewSlidingCounter(limit, time.Minute)

	var redisAllowed, memoryAllowed int

	for range limit * 2 {
		if d, err := redisLimiter.Allow(ctx, "client"); err != nil {
			t.Fatal(err)
		} else if d.Allowed {
			redisAllowed++
		}

		if d, err := memory.Allow(ctx, "client"); err != nil {
			t.Fatal(err)
		} else if d.Allowed {
			memoryAllowed++
		}
	}

	t.Logf("Redis allowed %d, the in-memory sliding counter allowed %d, limit %d",
		redisAllowed, memoryAllowed, limit)

	if redisAllowed != memoryAllowed {
		t.Errorf("the two implementations disagree: %d against %d", redisAllowed, memoryAllowed)
	}
	if redisAllowed != limit {
		t.Errorf("allowed %d, want %d", redisAllowed, limit)
	}
}

// TestRedisFailureIsAPolicyDecision, with a client pointed at nothing.
func TestRedisFailureIsAPolicyDecision(t *testing.T) {
	// A deliberately broken client: a port nothing listens on, and a short dial timeout so the test
	// is fast. No redistest here, because the point is that Redis is DOWN.
	broken := brokenRedis(t)

	l := NewRedisLimiter(broken, 10, time.Minute, "rl-test-down")

	ctx := context.Background()

	// The bare limiter reports the failure, which is correct: it does not know the policy.
	if _, err := l.Allow(ctx, "client"); err == nil {
		t.Error("a limiter pointed at a dead Redis should return an error")
	} else {
		t.Logf("the bare limiter returns: %v", err)
	}

	var logged []error

	open := FailOpen{Limiter: l, Open: true, OnError: func(e error) { logged = append(logged, e) }}
	closed := FailOpen{Limiter: l, Open: false, OnError: func(e error) { logged = append(logged, e) }}

	d, err := open.Allow(ctx, "client")
	if err != nil {
		t.Fatalf("fail-open returned an error: %v", err)
	}
	if !d.Allowed {
		t.Error("fail-open rejected a request while Redis was down")
	}

	d, err = closed.Allow(ctx, "client")
	if err != nil {
		t.Fatalf("fail-closed returned an error: %v", err)
	}
	if d.Allowed {
		t.Error("fail-closed allowed a request while Redis was down")
	}

	if len(logged) != 2 {
		t.Errorf("%d errors reached OnError, want 2", len(logged))
	}

	t.Log("fail open on a read endpoint so a cache outage is not a site outage; fail closed on " +
		"a login endpoint so it is not an open door. There is no correct default.")
}

// brokenRedis returns a client pointed at a port nothing listens on.
//
// Port 1 rather than a high random port, because a high port might be in use by something, and a connection
// that SUCCEEDS here would make the test assert the opposite of what it means to.
func brokenRedis(t *testing.T) *redis.Client {
	t.Helper()

	// go-redis logs pool dial failures through a package-level logger, so a deliberately broken
	// client prints "failed to dial after 5 attempts" three times into the test output regardless of
	// MaxRetries, which controls COMMAND retries rather than the pool's dialling.
	//
	// SetLogger returns nothing, so the previous logger cannot be saved and restored. It is global
	// and this sets it for the rest of the process, which is acceptable in a test binary and would
	// not be in a library. Swapping in a logger that writes to t.Logf rather than nil, so the
	// messages are still there if a failure needs them.
	redis.SetLogger(testLogger{t})
	t.Cleanup(func() { redis.SetLogger(silentLogger{}) })

	c := redis.NewClient(&redis.Options{
		Addr:         "127.0.0.1:1",
		DialTimeout:  200 * time.Millisecond,
		ReadTimeout:  200 * time.Millisecond,
		WriteTimeout: 200 * time.Millisecond,

		// No retries, so the test does not spend three dial timeouts finding out what one told it.
		MaxRetries: -1,
	})

	t.Cleanup(func() { _ = c.Close() })

	return c
}

// testLogger routes go-redis's internal logging to the test, so it is attached to the failing test rather
// than interleaved into the run's output.
type testLogger struct{ t *testing.T }

func (l testLogger) Printf(_ context.Context, format string, v ...any) {
	l.t.Logf("go-redis: "+format, v...)
}

// silentLogger drops it, for after the test that wanted a broken client has finished.
type silentLogger struct{}

func (silentLogger) Printf(context.Context, string, ...any) {}

// TestWindowKeysShareAClusterSlot: the script reads two keys, and Redis Cluster only runs a script whose keys are
// in one slot. A hash tag (the part in braces) is what Cluster hashes, so both keys must carry the same one.
func TestWindowKeysShareAClusterSlot(t *testing.T) {
	tag := func(k string) string {
		open, closing := strings.Index(k, "{"), strings.Index(k, "}")
		if open < 0 || closing < open {
			return ""
		}
		return k[open+1 : closing]
	}

	current, previous := windowKeys("rl", "203.0.113.7", 42)

	if tag(current) == "" || tag(current) != tag(previous) {
		t.Errorf("keys %q and %q do not share a hash tag, so Cluster can put them in different slots",
			current, previous)
	}
}
