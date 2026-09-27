// Package caching is cache-aside with Redis, and the four ways it goes wrong under load.
//
// # Cache-aside, which is the only pattern most services need
//
//	read:   look in the cache; on a miss, load from the source, store, return
//	write:  write the source, then DELETE the cache entry
//
// Delete rather than update, on write. Updating means computing the new cached value at write time, which is
// work nobody asked for and a second place for the derivation to be wrong. Deleting means the next reader
// recomputes it from the source, which is the same code path that already exists.
//
// The order also matters and is not symmetric. Write-then-delete leaves a window where a reader can see stale
// data; delete-then-write leaves a window where a reader can populate the cache with the OLD value and then
// keep it for the full TTL. The second is much worse, so: write the source first, delete second, always.
//
// # The four failures, in the order they bite
//
//	STAMPEDE       a popular key expires and 500 concurrent readers all miss and all load from the
//	               source at once. The database sees 500 identical queries. singleflight collapses
//	               them into one. TestStampedeIsCollapsed measures it.
//	SYNCHRONISED   every key written in the same deploy gets the same TTL, so they all expire in
//	  EXPIRY       the same second, forever. Jitter on the TTL is the fix and it is one line.
//	CACHING        a miss that is expensive and returns nothing is cached as nothing, or is not
//	  NOTHING      cached at all and every request for a missing key hits the source. Both are real
//	               and the second is how a 404 scan becomes a database outage.
//	UNBOUNDED      no TTL, or a TTL nobody set, so the cache holds everything forever and a value
//	  GROWTH       that changed in 2023 is still being served.
//
// # What this deliberately does not do
//
// No read-through, no write-behind, no two-level cache. Those are real patterns and each one adds a
// consistency question that cache-aside does not have. A service that gets cache-aside right, with
// singleflight and jittered TTLs, has solved the problem it actually had.
package caching

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"
)

// ErrNotFound is what a Loader returns when the source has no such key.
//
// A sentinel, because "missing" and "broken" need different handling and a nil value with a nil error cannot
// express the difference. A cache that treats a failed load as an empty value serves emptiness for the full
// TTL.
var ErrNotFound = errors.New("not found")

// Loader reads a value from the source of truth.
//
// Generic over the value, so the cache does not need an `any` and a type assertion at every call site. The
// assertion version compiles and fails at runtime when someone caches a *User under a key another caller
// reads as a User.
type Loader[T any] func(ctx context.Context, key string) (T, error)

// Stats is what the cache counted, so a test can assert on behaviour rather than timing and a service can
// export it.
type Stats struct {
	Hits         atomic.Int64
	Misses       atomic.Int64
	Loads        atomic.Int64
	Collapsed    atomic.Int64
	Errors       atomic.Int64
	NegativeHits atomic.Int64
}

// Snapshot is a readable copy.
type Snapshot struct {
	Hits, Misses, Loads, Collapsed, Errors, NegativeHits int64
}

// Read takes a snapshot.
func (s *Stats) Read() Snapshot {
	return Snapshot{
		Hits:         s.Hits.Load(),
		Misses:       s.Misses.Load(),
		Loads:        s.Loads.Load(),
		Collapsed:    s.Collapsed.Load(),
		Errors:       s.Errors.Load(),
		NegativeHits: s.NegativeHits.Load(),
	}
}

// HitRate is the number on the dashboard.
func (s Snapshot) HitRate() float64 {
	total := s.Hits + s.Misses
	if total == 0 {
		return 0
	}
	return float64(s.Hits) / float64(total)
}

// String formats a snapshot for a log line.
func (s Snapshot) String() string {
	return fmt.Sprintf("%d hits, %d misses (%.1f%% hit rate), %d loads, %d collapsed, "+
		"%d negative hits, %d errors",
		s.Hits, s.Misses, s.HitRate()*100, s.Loads, s.Collapsed, s.NegativeHits, s.Errors)
}

// Cache is cache-aside over Redis with the four fixes built in.
type Cache[T any] struct {
	client *redis.Client
	prefix string
	load   Loader[T]

	ttl    time.Duration
	jitter float64

	// negativeTTL is how long a "not found" is remembered. Short, because the thing being waited
	// for is usually a row about to be created, and remembering its absence for an hour means a
	// user who just signed up cannot log in.
	//
	// Zero disables negative caching, which is the wrong default and is available because a few
	// endpoints genuinely need it: anything where a miss is cheap and a stale negative is harmful.
	negativeTTL time.Duration

	// group collapses concurrent loads of the same key. This is the whole stampede fix, and it is
	// one field.
	group singleflight.Group

	stats Stats
}

// Options configures a Cache.
type Options struct {
	// TTL is the base lifetime of a cached value.
	TTL time.Duration

	// Jitter spreads expiry, as a fraction of TTL. 0.1 means each key lives for TTL plus or minus
	// up to 10%.
	//
	// Not decoration. Without it, every key written during a deploy expires in the same second for
	// the life of the service, so the source sees a spike every TTL. With 10% jitter on a 5-minute
	// TTL the same keys expire spread over a minute. The cost is that a value can live 10% longer
	// than configured, which for a cache is not a cost.
	Jitter float64

	// NegativeTTL is how long to remember that the source had nothing. Zero disables it.
	NegativeTTL time.Duration

	// Prefix namespaces the keys.
	Prefix string
}

// New builds a Cache.
func New[T any](client *redis.Client, load Loader[T], opts Options) *Cache[T] {
	if opts.TTL <= 0 {
		opts.TTL = 5 * time.Minute
	}
	if opts.Prefix == "" {
		opts.Prefix = "cache"
	}

	return &Cache[T]{
		client:      client,
		prefix:      opts.Prefix,
		load:        load,
		ttl:         opts.TTL,
		jitter:      opts.Jitter,
		negativeTTL: opts.NegativeTTL,
	}
}

// negativeMarker is what a cached "not found" looks like on the wire.
//
// A distinguishable value rather than an empty string, because an empty string is a legitimate cached value
// for a string cache and a zero-length JSON document is a legitimate value for some types. A marker that
// cannot be confused with a real value is the only way to tell "we know there is nothing" from "there is
// something and it is empty".
const negativeMarker = "\x00nil"

// Get is cache-aside, with the stampede fix.
func (c *Cache[T]) Get(ctx context.Context, key string) (T, error) {
	var zero T

	redisKey := c.prefix + ":" + key

	raw, err := c.client.Get(ctx, redisKey).Result()

	switch {
	case err == nil && raw == negativeMarker:
		// A remembered absence, which is a HIT: the cache answered, and it answered correctly.
		// Counting it as a miss makes the hit rate look worse than it is and hides whether
		// negative caching is working.
		c.stats.Hits.Add(1)
		c.stats.NegativeHits.Add(1)

		return zero, ErrNotFound

	case err == nil:
		var v T
		if err := json.Unmarshal([]byte(raw), &v); err != nil {
			// A value that will not decode is worse than a miss, because it will not decode
			// again on the next request either. Delete it and fall through to a load, which
			// is what makes a schema change survivable without flushing the cache by hand.
			c.stats.Errors.Add(1)

			if delErr := c.client.Del(ctx, redisKey).Err(); delErr != nil {
				return zero, fmt.Errorf("decoding %s failed (%w) and so did deleting it: %w",
					redisKey, err, delErr)
			}

			break
		}

		c.stats.Hits.Add(1)
		return v, nil

	case errors.Is(err, redis.Nil):
		// A miss, which is the normal path and not an error.

	default:
		// Redis is broken. The decision is the same one FailOpen makes in the ratelimit
		// package, and here the answer is easy: a cache that cannot be read falls through to
		// the source. A cache outage must not be a service outage.
		c.stats.Errors.Add(1)
	}

	c.stats.Misses.Add(1)

	return c.loadAndStore(ctx, redisKey, key)
}

// loadAndStore does the load through singleflight, so N concurrent misses become one load.
//
// # What singleflight does and does not do
//
// Do calls fn once per key for all callers that arrive while it is running, and gives every caller the same
// result. So 500 concurrent misses on one key produce one database query.
//
// It does NOT deduplicate across processes. Five replicas each collapse their own 100 callers into one query,
// so the source sees five queries rather than 500. Getting from five to one needs a lock in Redis, which adds
// a round trip to every miss and a decision about what to do when the lock holder dies. Five is usually fine
// and one is usually not worth it.
//
// The shared flag says whether this caller's result came from someone else's call, which is how Collapsed is
// counted, and it is the only way to measure the fix from inside.
func (c *Cache[T]) loadAndStore(ctx context.Context, redisKey, key string) (T, error) {
	var zero T

	result, err, shared := c.group.Do(redisKey, func() (any, error) {
		c.stats.Loads.Add(1)

		v, err := c.load(ctx, key)
		if err != nil {
			return nil, err
		}

		encoded, err := json.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("encoding %s: %w", key, err)
		}

		// SET with a TTL, not SET then EXPIRE. Two commands means a window where the key has no
		// TTL, and if the process dies in it the key is cached forever.
		if err := c.client.Set(ctx, redisKey, encoded, c.jitteredTTL(c.ttl)).Err(); err != nil {
			// The value was loaded successfully, so return it. A cache that cannot be
			// WRITTEN is still a service that works, just a slower one.
			c.stats.Errors.Add(1)
		}

		return v, nil
	})

	if shared {
		c.stats.Collapsed.Add(1)
	}

	if err != nil {
		if errors.Is(err, ErrNotFound) && c.negativeTTL > 0 {
			// Remember the absence, briefly. Without this, a scan for ids 1 to 1,000,000
			// puts a million queries through the source and the cache never helps, because
			// nothing was ever cacheable.
			if setErr := c.client.Set(ctx, redisKey, negativeMarker,
				c.jitteredTTL(c.negativeTTL)).Err(); setErr != nil {
				c.stats.Errors.Add(1)
			}
		}

		return zero, err
	}

	v, ok := result.(T)
	if !ok {
		// Unreachable: the closure above only ever returns a T or an error. Checked because a
		// failed assertion here would panic inside a handler, and singleflight's any is the one
		// place the generic type is lost.
		return zero, fmt.Errorf("caching: %s produced %T, want %T", key, result, zero)
	}

	return v, nil
}

// jitteredTTL spreads expiry so keys written together do not expire together.
//
// The randomness is symmetric around the TTL rather than added to it, because an asymmetric jitter changes
// the average lifetime and then "a 5 minute cache" is a 5 minute 15 second cache.
//
// math/rand/v2's global functions are safe for concurrent use and are seeded automatically, so there is no
// source to manage and no lock to contend on. In v1 the global functions took a mutex and a cache under load
// could measurably contend on it, which is why so much code carries its own *rand.Rand.
func (c *Cache[T]) jitteredTTL(base time.Duration) time.Duration {
	if c.jitter <= 0 {
		return base
	}

	spread := float64(base) * c.jitter

	// [-spread, +spread)
	offset := (rand.Float64()*2 - 1) * spread

	ttl := time.Duration(float64(base) + offset)
	if ttl < time.Second {
		ttl = time.Second
	}

	return ttl
}

// Invalidate deletes a key, which is what a write should do.
//
// The name is Invalidate rather than Delete because that is what it means at this layer, and because Delete
// invites the question "delete from where".
func (c *Cache[T]) Invalidate(ctx context.Context, keys ...string) error {
	if len(keys) == 0 {
		return nil
	}

	redisKeys := make([]string, len(keys))
	for i, key := range keys {
		redisKeys[i] = c.prefix + ":" + key
	}

	// One DEL with every key, rather than one per key. DEL is variadic and this is the difference
	// between one round trip and N, which is the N+1 problem again in a third place.
	if err := c.client.Del(ctx, redisKeys...).Err(); err != nil {
		return fmt.Errorf("invalidating %v: %w", keys, err)
	}

	return nil
}

// Stats returns a snapshot.
func (c *Cache[T]) Stats() Snapshot { return c.stats.Read() }

// TTLOf reports the remaining TTL of a key, so a test can measure the jitter rather than trust it.
func (c *Cache[T]) TTLOf(ctx context.Context, key string) (time.Duration, error) {
	ttl, err := c.client.PTTL(ctx, c.prefix+":"+key).Result()
	if err != nil {
		return 0, fmt.Errorf("reading the TTL of %s: %w", key, err)
	}

	// -1 means the key exists with no TTL, -2 that it does not exist. Both are real states that a
	// caller has to distinguish, and returning them as negative durations is what go-redis does.
	return ttl, nil
}
