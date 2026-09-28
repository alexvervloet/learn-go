package ratelimit_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alexvervloet/learn-go/capstones/bookmark-manager/internal/apitest"
	"github.com/alexvervloet/learn-go/capstones/bookmark-manager/internal/ratelimit"
	"github.com/stretchr/testify/require"
)

func limiter(t *testing.T, limit int, window time.Duration) *ratelimit.Limiter {
	t.Helper()

	l, err := ratelimit.New(apitest.RedisClient(t), ratelimit.Options{
		Limit: limit, Window: window, Prefix: "test",
	})
	require.NoError(t, err)

	return l
}

// TestTheLimitIsExact is the basic guarantee.
func TestTheLimitIsExact(t *testing.T) {
	l := limiter(t, 5, time.Minute)
	ctx := context.Background()

	for i := 1; i <= 5; i++ {
		d, err := l.Allow(ctx, "alex")
		require.NoError(t, err)
		require.True(t, d.Allowed, "request %d", i)
		require.Equal(t, 5-i, d.Remaining)
	}

	d, err := l.Allow(ctx, "alex")
	require.NoError(t, err)
	require.False(t, d.Allowed, "the sixth is refused")
	require.Zero(t, d.Remaining)
	require.Positive(t, d.ResetIn)
}

// TestKeysAreIndependent is what makes a per-user limit possible.
func TestKeysAreIndependent(t *testing.T) {
	l := limiter(t, 2, time.Minute)
	ctx := context.Background()

	for range 2 {
		d, err := l.Allow(ctx, "alex")
		require.NoError(t, err)
		require.True(t, d.Allowed)
	}

	refused, err := l.Allow(ctx, "alex")
	require.NoError(t, err)
	require.False(t, refused.Allowed)

	other, err := l.Allow(ctx, "sam")
	require.NoError(t, err)
	require.True(t, other.Allowed, "one key being full says nothing about another")
}

// TestTheWindowSlides is the whole reason not to use a fixed window.
//
// A fixed window counts per calendar minute and allows twice the limit across a boundary. This test fills the
// window, waits for the oldest entries to age out, and shows exactly that many slots free up, at a position
// that has nothing to do with a calendar boundary.
func TestTheWindowSlides(t *testing.T) {
	const window = 600 * time.Millisecond

	l := limiter(t, 3, window)
	ctx := context.Background()

	// Two now.
	for range 2 {
		d, err := l.Allow(ctx, "alex")
		require.NoError(t, err)
		require.True(t, d.Allowed)
	}

	// One after a pause, so the three are not contemporaneous.
	time.Sleep(window / 2)

	d, err := l.Allow(ctx, "alex")
	require.NoError(t, err)
	require.True(t, d.Allowed)

	refused, err := l.Allow(ctx, "alex")
	require.NoError(t, err)
	require.False(t, refused.Allowed, "full")

	// Wait for the first two to fall out of the window, but not the third.
	time.Sleep(window/2 + 100*time.Millisecond)

	for i := range 2 {
		d, err := l.Allow(ctx, "alex")
		require.NoError(t, err)
		require.True(t, d.Allowed, "slot %d freed up as the oldest entries aged out", i)
	}

	// The third is still inside the window, so there is no third slot.
	stillFull, err := l.Allow(ctx, "alex")
	require.NoError(t, err)
	require.False(t, stillFull.Allowed)
}

// TestResetInPointsAtTheOldestEntry is what makes Retry-After useful.
//
// Returning the whole window would tell a client to wait far longer than it has to. The oldest surviving entry
// decides when a slot frees up, and that is what the script returns.
func TestResetInPointsAtTheOldestEntry(t *testing.T) {
	const window = time.Second

	l := limiter(t, 1, window)
	ctx := context.Background()

	first, err := l.Allow(ctx, "alex")
	require.NoError(t, err)
	require.True(t, first.Allowed)

	time.Sleep(400 * time.Millisecond)

	refused, err := l.Allow(ctx, "alex")
	require.NoError(t, err)
	require.False(t, refused.Allowed)

	// About 600ms left, not a full second. The bounds are wide because this is wall-clock time on a shared
	// machine, and the CLAIM is "less than the window", which is the part that matters.
	require.Less(t, refused.ResetIn, window, "a full window would over-tell the client to wait")
	require.Positive(t, refused.ResetIn)

	t.Logf("400ms into a %v window, reset in %v", window, refused.ResetIn.Round(10*time.Millisecond))
}

// TestConcurrentRequestsCannotExceedTheLimit is why the script is Lua and not four commands.
//
// Done as separate ZREMRANGEBYSCORE, ZCARD, compare, ZADD, two concurrent requests both read a count below the
// limit and both add, so the limit is exceeded by however many are in flight. A MULTI/EXEC does not help either,
// because the decision depends on the count and a transaction cannot branch.
//
// Redis runs a script as one operation, so the read and the write cannot interleave. The assertion is exact.
func TestConcurrentRequestsCannotExceedTheLimit(t *testing.T) {
	const (
		limit   = 50
		callers = 500
	)

	l := limiter(t, limit, time.Minute)
	ctx := context.Background()

	var (
		allowed atomic.Int64
		wg      sync.WaitGroup
		ready   sync.WaitGroup
	)

	start := make(chan struct{})

	ready.Add(callers)

	for range callers {
		wg.Add(1)

		go func() {
			defer wg.Done()

			ready.Done()
			<-start

			d, err := l.Allow(ctx, "hot")
			if err != nil {
				t.Errorf("allow: %v", err)

				return
			}

			if d.Allowed {
				allowed.Add(1)
			}
		}()
	}

	ready.Wait()
	close(start)
	wg.Wait()

	require.Equal(t, int64(limit), allowed.Load(),
		"exactly the limit, not one more: %d callers raced and the script is atomic", callers)
}

// TestTheMemberIsUniquePerRequest is the sorted-set trap.
//
// A sorted set is a SET: two ZADDs with the same member is one member. A member derived only from the clock
// merges concurrent requests, the count stays low, and the limit is never reached. This asserts the count is
// what it should be after concurrent calls within one clock tick.
func TestTheMemberIsUniquePerRequest(t *testing.T) {
	client := apitest.RedisClient(t)

	l, err := ratelimit.New(client, ratelimit.Options{Limit: 1000, Window: time.Minute, Prefix: "test"})
	require.NoError(t, err)

	ctx := context.Background()

	const n = 200

	var wg sync.WaitGroup

	for range n {
		wg.Add(1)

		go func() {
			defer wg.Done()

			_, _ = l.Allow(ctx, "same")
		}()
	}

	wg.Wait()

	count, err := client.ZCard(ctx, "test:same").Result()
	require.NoError(t, err)
	require.Equal(t, int64(n), count,
		"every request is its own member; with a clock-only member this would be far fewer")
}

// TestTheKeyExpires is what stops Redis filling up with one-time visitors.
func TestTheKeyExpires(t *testing.T) {
	client := apitest.RedisClient(t)

	l, err := ratelimit.New(client, ratelimit.Options{Limit: 5, Window: 2 * time.Second, Prefix: "test"})
	require.NoError(t, err)

	ctx := context.Background()

	_, err = l.Allow(ctx, "visitor")
	require.NoError(t, err)

	ttl, err := client.TTL(ctx, "test:visitor").Result()
	require.NoError(t, err)

	require.Positive(t, ttl, "without an EXPIRE, every key ever created lives forever")
	require.LessOrEqual(t, ttl, 4*time.Second, "and the TTL is about the window, not indefinite")
}

// TestResetClearsAKey is what a successful login does to its own counter.
func TestResetClearsAKey(t *testing.T) {
	l := limiter(t, 1, time.Minute)
	ctx := context.Background()

	_, err := l.Allow(ctx, "alex")
	require.NoError(t, err)

	refused, err := l.Allow(ctx, "alex")
	require.NoError(t, err)
	require.False(t, refused.Allowed)

	require.NoError(t, l.Reset(ctx, "alex"))

	after, err := l.Allow(ctx, "alex")
	require.NoError(t, err)
	require.True(t, after.Allowed)
}

// TestABadConfigurationIsRefused catches the two values that cannot work.
func TestABadConfigurationIsRefused(t *testing.T) {
	client := apitest.RedisClient(t)

	_, err := ratelimit.New(client, ratelimit.Options{Limit: 0, Window: time.Minute})
	require.ErrorContains(t, err, "limit must be positive")

	_, err = ratelimit.New(client, ratelimit.Options{Limit: 10, Window: 0})
	require.ErrorContains(t, err, "window must be positive")
}
