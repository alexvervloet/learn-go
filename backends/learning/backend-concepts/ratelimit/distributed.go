package ratelimit

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"golang.org/x/time/rate"
)

// TokenBucket wraps golang.org/x/time/rate, which is what a Go service should reach for first when one
// process is enough.
//
// Two things about rate.Limiter are worth stating because they are not obvious from the name.
//
// The rate is in events per SECOND as a float64, so "100 per minute" is rate.Limit(100.0/60). Writing
// rate.Limit(100) there is a limit of 100 per second and the mistake compiles.
//
// The burst is the bucket size, and it is not optional: a burst of 0 with any rate rejects everything, and a
// burst of 1 means strict pacing with no allowance at all. For an HTTP endpoint the burst is what lets a page
// load its six assets at once, so setting it to 1 breaks the thing it was meant to protect.
type TokenBucket struct {
	limit rate.Limit
	burst int

	// clock is the time source for Allow's decisions; nil means time.Now. Set it with SetClock. A
	// pointer rather than a Clock field like the other limiters have, because a func-typed field
	// makes the struct non-comparable, and TokenBucket was comparable before it had a clock.
	clock *Clock

	// One limiter per key. A sync.Map is tempting and wrong here: the zero value of a rate.Limiter
	// does not work, so every lookup needs a construct-if-missing, and LoadOrStore constructs
	// eagerly on every call. A plain map behind a mutex is clearer and the mutex is not the
	// bottleneck at HTTP rates.
	keyed *keyedLimiters
}

// NewTokenBucket builds one from a limit expressed per window, which is how limits are written down.
//
// perWindow and window rather than a rate.Limit, because "100 per minute" is the thing in the requirements
// document and converting it at every call site is where the factor-of-60 bug comes from.
func NewTokenBucket(perWindow int, window time.Duration, burst int) *TokenBucket {
	return &TokenBucket{
		limit: rate.Limit(float64(perWindow) / window.Seconds()),
		burst: burst,
		keyed: newKeyedLimiters(),
	}
}

// SetClock sets the time source for Allow's decisions, so a test can move time instead of sleeping. The
// first version called time.Now directly, so its behaviour over time could only be tested by waiting.
func (t *TokenBucket) SetClock(c Clock) { t.clock = &c }

// Allow implements Limiter.
func (t *TokenBucket) Allow(_ context.Context, key string) (Decision, error) {
	if t.burst <= 0 {
		return Decision{}, errors.New("ratelimit: TokenBucket needs a positive burst")
	}

	l := t.keyed.get(key, t.limit, t.burst)

	// AllowN with an explicit time, rather than Allow, so the decision is a function of the Clock and
	// a test can move time instead of waiting for it.
	var now time.Time
	if t.clock != nil {
		now = t.clock.now()
	} else {
		now = time.Now()
	}

	if !l.AllowN(now, 1) {
		// Reserve tells us when a token will be available, and CancelAt gives it back so asking
		// does not consume one. Getting that wrong makes a rejected request cost a token, so a
		// client that retries can starve itself forever.
		r := l.ReserveN(now, 1)
		delay := r.DelayFrom(now)
		r.CancelAt(now)

		return Decision{
			Limit:      t.burst,
			Remaining:  0,
			RetryAfter: delay,
			ResetIn:    delay,
			ResetAt:    now.Add(delay),
		}, nil
	}

	// The bucket refills continuously, so "reset" is when one more token arrives rather than when a
	// window ends. That is a real difference between a bucket and a window and the header cannot
	// express it, which is one reason RateLimit-Reset is the least useful of the three.
	perToken := time.Duration(float64(time.Second) / float64(t.limit))

	return Decision{
		Allowed:   true,
		Limit:     t.burst,
		Remaining: int(l.TokensAt(now)),
		ResetIn:   perToken,
		ResetAt:   now.Add(perToken),
	}, nil
}

// Wait blocks until a token is available or the context is done.
//
// This is the half of x/time/rate that has no equivalent in the other algorithms here, and it is the right
// call when this process is the CLIENT of a rate-limited API. Failing a request because someone else's API
// allows 10 per second is pointless when waiting 100ms would succeed.
//
// For a server deciding whether to answer a request, Allow is right and Wait is a way to build a queue of
// blocked goroutines that all time out together.
func (t *TokenBucket) Wait(ctx context.Context, key string) error {
	l := t.keyed.get(key, t.limit, t.burst)

	if err := l.Wait(ctx); err != nil {
		wrapped := fmt.Errorf("waiting for a token for %q: %w", key, err)

		// rate.Limiter's error for a deadline it cannot meet is
		// "rate: Wait(n=1) would exceed context deadline". It is a plain error: it does not wrap
		// context.DeadlineExceeded, so errors.Is(err, context.DeadlineExceeded) is false and a
		// caller checking for a deadline the normal way misses it entirely.
		//
		// And ctx.Err() is nil here, which took a failing test to notice. rate.Limiter does not
		// wait and then give up; it works out up front that the token cannot arrive in time and
		// refuses immediately, so the context has not expired yet. That is better behaviour than
		// waiting, and it means the obvious fix (join ctx.Err()) joins nothing.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return errors.Join(wrapped, ctxErr)
		}

		// So: a context WITH a deadline plus a refusal from rate means the refusal was about the
		// deadline, because that is the only reason Wait returns an error for a limiter with a
		// positive burst. Joining DeadlineExceeded makes it matchable without hiding rate's own
		// message.
		if _, hasDeadline := ctx.Deadline(); hasDeadline {
			return errors.Join(wrapped, context.DeadlineExceeded)
		}

		return wrapped
	}

	return nil
}

// RedisLimiter is a sliding window counter in Redis, which is the answer to "five replicas enforce five times
// the limit".
//
// # Why a Lua script
//
// The read-then-write between GET and SET is the lost update from the transactions package, at a different
// layer. Two replicas both read 99, both allow, both write 100, and the limit of 100 allowed 101 requests.
//
// Redis runs a Lua script atomically against the whole keyspace, so the read and the write cannot interleave.
// The alternatives are worse: MULTI/EXEC does not allow a decision based on a value read inside it, and
// WATCH/retry works and needs a retry loop for something the script does in one round trip.
//
// # Why not INCR with EXPIRE
//
// The obvious version is INCR then EXPIRE, and it has a real bug: if the process dies between them the key
// never expires and that client is limited forever. SET with NX and EX is atomic, but then the TTL is reset
// on every request, which turns a fixed window into a rolling one that never ends. The script sets the TTL
// only when it creates the counter.
type RedisLimiter struct {
	Client *redis.Client
	Limit  int
	Window time.Duration

	// Prefix keeps this limiter's keys away from everything else in the database, which matters
	// because the tests and a real service share a Redis.
	Prefix string
}

// The script.
//
// KEYS[1] is the current window's counter, KEYS[2] the previous window's.
// ARGV: 1 the limit, 2 the window in milliseconds, 3 the weight for the previous window.
//
// Returns: allowed (0/1), the weighted count, and the milliseconds until the window resets.
//
// Note that it does NOT call redis.call('TIME'). A script that reads the server clock cannot be replicated
// by the effects it produces, and Redis used to refuse to run such scripts as writes. Passing the weight in
// means the client computes the time, which is also what makes the whole thing testable.
var slidingScript = redis.NewScript(`
local limit    = tonumber(ARGV[1])
local windowMS = tonumber(ARGV[2])
local weight   = tonumber(ARGV[3])

local current  = tonumber(redis.call('GET', KEYS[1])) or 0
local previous = tonumber(redis.call('GET', KEYS[2])) or 0

local estimate = previous * weight + current

if estimate >= limit then
  return {0, tostring(estimate), redis.call('PTTL', KEYS[1])}
end

local new = redis.call('INCR', KEYS[1])

-- The TTL is set only when the key was just created, so a busy client does not keep extending its own
-- window. Two windows of TTL, because the previous window has to survive long enough to be weighted.
if new == 1 then
  redis.call('PEXPIRE', KEYS[1], windowMS * 2)
end

return {1, tostring(estimate), redis.call('PTTL', KEYS[1])}
`)

// NewRedisLimiter builds one.
func NewRedisLimiter(client *redis.Client, limit int, window time.Duration, prefix string) *RedisLimiter {
	return &RedisLimiter{Client: client, Limit: limit, Window: window, Prefix: prefix}
}

// windowKeys names the two counters the script reads, the current window's and the previous one's.
//
// The client key is wrapped in braces, a Redis Cluster HASH TAG: Cluster hashes only the part between the braces
// to pick a slot, so both keys land on the same node. A Lua script may only touch keys in one slot, and the
// first version named them "prefix:key:index", which hash to different slots and fail on Cluster with CROSSSLOT.
// A single Redis never checks, which is why nothing here noticed.
func windowKeys(prefix, key string, index int64) (current, previous string) {
	return fmt.Sprintf("%s:{%s}:%d", prefix, key, index), fmt.Sprintf("%s:{%s}:%d", prefix, key, index-1)
}

// Allow implements Limiter.
func (r *RedisLimiter) Allow(ctx context.Context, key string) (Decision, error) {
	if r.Limit <= 0 || r.Window <= 0 {
		return Decision{}, errors.New("ratelimit: RedisLimiter needs a positive limit and window")
	}

	now := time.Now()
	start := now.Truncate(r.Window)

	// The window index, so the key changes when the window rolls and the old key expires on its own.
	// Time-based key names rather than a stored timestamp: there is nothing to clean up.
	index := start.UnixMilli() / r.Window.Milliseconds()

	currentKey, previousKey := windowKeys(r.Prefix, key, index)

	weight := 1 - float64(now.Sub(start))/float64(r.Window)

	result, err := slidingScript.Run(ctx, r.Client,
		[]string{currentKey, previousKey},
		r.Limit, r.Window.Milliseconds(), weight).Slice()
	if err != nil {
		return Decision{}, fmt.Errorf("running the rate limit script for %q: %w", key, err)
	}

	if len(result) != 3 {
		return Decision{}, fmt.Errorf("the script returned %d values, want 3", len(result))
	}

	allowed, _ := result[0].(int64)

	var estimate float64
	if s, ok := result[1].(string); ok {
		if _, err := fmt.Sscanf(s, "%g", &estimate); err != nil {
			return Decision{}, fmt.Errorf("parsing the estimate %q: %w", s, err)
		}
	}

	resetAt := start.Add(r.Window)

	d := Decision{
		Allowed:   allowed == 1,
		Limit:     r.Limit,
		Remaining: max(r.Limit-int(estimate)-1, 0),
		ResetIn:   resetAt.Sub(now),
		ResetAt:   resetAt,
	}

	if !d.Allowed {
		d.Remaining = 0
		d.RetryAfter = resetAt.Sub(now)
	}

	return d, nil
}

// Reset clears a key's counters, for tests.
func (r *RedisLimiter) Reset(ctx context.Context, key string) error {
	iter := r.Client.Scan(ctx, 0, fmt.Sprintf("%s:%s:*", r.Prefix, key), 100).Iterator()

	for iter.Next(ctx) {
		if err := r.Client.Del(ctx, iter.Val()).Err(); err != nil {
			return fmt.Errorf("deleting %s: %w", iter.Val(), err)
		}
	}

	if err := iter.Err(); err != nil {
		return fmt.Errorf("scanning for %s keys: %w", key, err)
	}

	return nil
}
