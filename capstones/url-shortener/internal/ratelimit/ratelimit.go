// Package ratelimit is a Redis-backed sliding window.
//
// # Why not a fixed window
//
// A fixed window counts requests per calendar minute. It is one INCR and one EXPIRE, and it allows twice the
// limit across a boundary: 100 requests at 11:59:59 and 100 more at 12:00:00 is 200 in one second, all within
// the rules. For a limit that exists to protect a database, being wrong by 2x at exactly the moment traffic
// spikes is the wrong direction to be wrong in.
//
// # The sliding window log
//
// Keep a sorted set of request timestamps, drop everything older than the window, and count what is left. It
// is exact: the limit holds over every window position, not just the aligned ones.
//
// The cost is memory proportional to the limit per key, which for 100 requests and a few thousand keys is
// nothing and for a limit of a million would not be. The middle option, a sliding window COUNTER that
// interpolates between two fixed windows, is O(1) memory and approximate, and it is what a service at very
// large scale uses.
//
// # Why the whole thing is a Lua script
//
// The four operations have to be ATOMIC. Done as separate commands, two concurrent requests both trim, both
// count 99, and both are allowed past a limit of 100. A MULTI/EXEC transaction does not help, because the
// decision depends on the count and a transaction cannot branch.
//
// Redis runs a script as a single operation, so the read and the write cannot be interleaved. This is the
// canonical reason to reach for Lua in Redis and almost the only one.
package ratelimit

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

// slidingWindow is the script.
//
// KEYS[1]  the sorted set
// ARGV[1]  now, in microseconds
// ARGV[2]  the window, in microseconds
// ARGV[3]  the limit
// ARGV[4]  a unique member for this request
//
// Returns {allowed, remaining, resetInMicroseconds}.
//
// # Two details in here that are easy to get wrong
//
// The member has to be UNIQUE per request. A sorted set is a set: adding two members with the same name is one
// member, so using the timestamp as the member silently drops concurrent requests from the count and the limit
// is never reached.
//
// The EXPIRE is reset on every call, so a key for an idle client disappears on its own. Without it, every key
// ever created lives forever and Redis fills up with clients that visited once.
var slidingWindow = redis.NewScript(`
local key    = KEYS[1]
local now    = tonumber(ARGV[1])
local window = tonumber(ARGV[2])
local limit  = tonumber(ARGV[3])
local member = ARGV[4]

-- Drop everything that has fallen out of the window.
redis.call('ZREMRANGEBYSCORE', key, '-inf', now - window)

local count = redis.call('ZCARD', key)

if count >= limit then
  -- Refused. The oldest surviving entry decides when a slot frees up, which is what the caller needs for a
  -- Retry-After header. Returning a fixed window length instead would tell a client to wait far longer than
  -- necessary.
  local oldest = redis.call('ZRANGE', key, 0, 0, 'WITHSCORES')
  local resetIn = window

  if oldest[2] then
    resetIn = (tonumber(oldest[2]) + window) - now
    if resetIn < 0 then resetIn = 0 end
  end

  return {0, 0, resetIn}
end

redis.call('ZADD', key, now, member)

-- Expire slightly after the window, so a key for a client that stops sending goes away on its own. The
-- rounding up matters: PEXPIRE with a value that rounds to zero deletes the key immediately.
redis.call('PEXPIRE', key, math.ceil(window / 1000) + 1000)

return {1, limit - count - 1, window}
`)

// Decision is the outcome of one check.
type Decision struct {
	Allowed   bool
	Remaining int

	// ResetIn is a DURATION, not a timestamp.
	//
	// A handler turning this into a header needs `time.Now().Add(ResetIn)`, and a test needs to assert on it
	// without a clock. Returning an absolute time would make the whole thing untestable without injecting a
	// clock, which is a lesson this repository learned in its rate-limiting module.
	ResetIn time.Duration

	Limit int
}

// Limiter checks a key against a limit.
type Limiter struct {
	client *redis.Client
	limit  int
	window time.Duration
	prefix string
}

// Options configures a Limiter.
type Options struct {
	Limit  int
	Window time.Duration

	// Prefix namespaces the keys, so two limiters on one Redis do not share a bucket.
	Prefix string
}

// New builds a Limiter.
func New(client *redis.Client, opts Options) (*Limiter, error) {
	if opts.Limit <= 0 {
		return nil, errors.New("ratelimit: limit must be positive")
	}

	if opts.Window <= 0 {
		return nil, errors.New("ratelimit: window must be positive")
	}

	if opts.Prefix == "" {
		opts.Prefix = "rl"
	}

	return &Limiter{client: client, limit: opts.Limit, window: opts.Window, prefix: opts.Prefix}, nil
}

// Allow checks one request.
//
// # What happens when Redis is down
//
// This returns an error and the caller decides. That is the decision worth making explicitly, and both answers
// are defensible:
//
//   - FAIL OPEN, allow the request. The service keeps working and the limit is off, which is right when the
//     limit exists to be polite rather than to protect something.
//   - FAIL CLOSED, refuse. Right when the limit is the only thing standing between a login endpoint and a
//     credential-stuffing run.
//
// This service limits only the credential endpoints, and they fail closed; reads are not limited at all, so
// there is nothing for them to fail open on. The choice lives in the handler either way. A limiter that picks
// for you has picked wrong for half its callers.
func (l *Limiter) Allow(ctx context.Context, key string) (Decision, error) {
	now := time.Now()

	// A unique member per request.
	//
	// A sorted set is a SET: two ZADDs with the same member is one member, so a member derived only from the
	// timestamp silently merges concurrent requests and the limit is never reached. The nanosecond clock is
	// not enough on its own, because two goroutines can read the same nanosecond.
	//
	// So: the clock, plus a process-wide atomic counter. The counter makes it unique within this process and
	// the clock makes a collision across processes vanishingly unlikely. An atomic rather than a plain
	// variable because Allow is called from every request goroutine.
	member := strconv.FormatInt(now.UnixNano(), 36) + ":" + strconv.FormatUint(counter.Add(1), 36)

	res, err := slidingWindow.Run(ctx, l.client,
		[]string{l.prefix + ":" + key},
		now.UnixMicro(),
		l.window.Microseconds(),
		l.limit,
		member,
	).Slice()
	if err != nil {
		return Decision{}, fmt.Errorf("ratelimit: %w", err)
	}

	if len(res) != 3 {
		return Decision{}, fmt.Errorf("ratelimit: script returned %d values, want 3", len(res))
	}

	allowed, _ := res[0].(int64)
	remaining, _ := res[1].(int64)
	resetMicros, _ := res[2].(int64)

	return Decision{
		Allowed:   allowed == 1,
		Remaining: int(remaining),
		ResetIn:   time.Duration(resetMicros) * time.Microsecond,
		Limit:     l.limit,
	}, nil
}

// Reset clears a key, which is what a successful login does to the per-account bucket.
func (l *Limiter) Reset(ctx context.Context, key string) error {
	return l.client.Del(ctx, l.prefix+":"+key).Err()
}

// counter makes each sorted-set member unique within this process.
var counter atomic.Uint64
