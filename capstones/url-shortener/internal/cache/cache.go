// Package cache is the redirect path's read cache.
//
// # What is being cached and why
//
// One row, looked up by a unique index. That is already fast, and the cache is not about the query's cost: it is
// about the DATABASE'S capacity. A popular link takes every redirect to Postgres, and a shortener's traffic is
// almost entirely redirects, so the database becomes the ceiling on a workload that has nothing to compute.
//
// # Cache-aside, not write-through
//
// Read: look in Redis, fall back to Postgres, write what you found. Write: change Postgres, DELETE the key.
//
// Delete rather than update, because an update has a race that a delete does not: two writers can update the
// cache in the opposite order to the database and leave the cache holding the older value, permanently. A delete
// makes the next reader fetch, which cannot be stale.
//
// # Negative caching, and why it is here
//
// A missing slug is cached too, for a shorter time. Without it, a scanner probing random slugs sends every probe
// to Postgres, and that is a denial of service that costs the attacker nothing. With it, the same probe hits
// Redis.
//
// The cost is that a slug created moments after someone asked for it stays 404 until the negative entry
// expires, which is why the negative TTL is short and why creation deletes the key.
package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"
)

// Entry is what is stored against a slug.
type Entry struct {
	// Missing marks a negative entry. A nil Entry could not be told from a cache miss, and a separate key
	// prefix for absences means two lookups.
	Missing bool `json:"missing,omitempty"`

	URLID     int64      `json:"url_id,omitempty"`
	Target    string     `json:"target,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// Cache wraps Redis.
type Cache struct {
	client *redis.Client

	ttl         time.Duration
	negativeTTL time.Duration

	// group collapses concurrent misses for one key into a single database call.
	//
	// Without it, a popular link expiring sends every in-flight reader to Postgres at once, which is the
	// cache stampede. singleflight is per-process, so a fleet of ten replicas makes ten calls rather than one,
	// and ten is the number this is trying to get down from ten thousand.
	group singleflight.Group

	hits      atomic.Int64
	misses    atomic.Int64
	negatives atomic.Int64
	collapsed atomic.Int64
	errors    atomic.Int64
}

// Stats counts what happened, so a test can assert on behaviour rather than on timing.
//
// The counters are atomics rather than plain ints because they are incremented from request goroutines. The
// first draft used plain ints with a comment saying the method was "for tests, which read it after the work is
// done", which is exactly the reasoning that produces a race the detector finds a week later. An atomic
// increment is one instruction and the argument for skipping it is not worth having.
type Stats struct {
	Hits      int64
	Misses    int64
	Negatives int64
	Collapsed int64
	Errors    int64
}

// Options configures a Cache.
type Options struct {
	TTL         time.Duration
	NegativeTTL time.Duration
}

// New builds a Cache.
func New(client *redis.Client, opts Options) *Cache {
	if opts.TTL == 0 {
		opts.TTL = 5 * time.Minute
	}

	if opts.NegativeTTL == 0 {
		// Much shorter than the positive TTL. A wrong "it exists" is a redirect to the wrong place; a wrong
		// "it does not exist" is a 404 that fixes itself. The asymmetry in consequence is the asymmetry in TTL.
		opts.NegativeTTL = 30 * time.Second
	}

	return &Cache{client: client, ttl: opts.TTL, negativeTTL: opts.NegativeTTL}
}

// ErrMiss is returned by Get when the key is not cached.
var ErrMiss = errors.New("cache: miss")

func key(slug string) string { return "slug:" + slug }

// Get reads an entry.
func (c *Cache) Get(ctx context.Context, slug string) (*Entry, error) {
	raw, err := c.client.Get(ctx, key(slug)).Bytes()

	switch {
	case errors.Is(err, redis.Nil):
		c.misses.Add(1)

		return nil, ErrMiss

	case err != nil:
		// A cache error is not a request error.
		//
		// The caller falls through to the database, so Redis being down makes the service slower and not
		// broken. That is the whole point of a cache being optional, and it is a decision that has to be made
		// deliberately: the naive version propagates the error and takes the service down with Redis.
		c.errors.Add(1)

		return nil, fmt.Errorf("cache: get %q: %w", slug, err)
	}

	var entry Entry
	if err := json.Unmarshal(raw, &entry); err != nil {
		// A value that does not decode is treated as a miss and deleted. This happens on a deploy that
		// changes the struct, and the alternative is every reader failing until the TTL runs out.
		c.errors.Add(1)
		_ = c.client.Del(ctx, key(slug)).Err()

		return nil, ErrMiss
	}

	if entry.Missing {
		c.negatives.Add(1)
	} else {
		c.hits.Add(1)
	}

	return &entry, nil
}

// Put stores an entry.
func (c *Cache) Put(ctx context.Context, slug string, entry Entry) error {
	ttl := c.ttl
	if entry.Missing {
		ttl = c.negativeTTL
	}

	// A URL that expires sooner than the cache TTL gets the shorter one, so the cache cannot serve a redirect
	// to a link that has expired. Without this the expiry is advisory for up to one TTL.
	if entry.ExpiresAt != nil {
		if remaining := time.Until(*entry.ExpiresAt); remaining < ttl {
			ttl = remaining
		}
	}

	if ttl <= 0 {
		// Already expired, so there is nothing worth caching.
		return nil
	}

	encoded, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("cache: encode %q: %w", slug, err)
	}

	return c.client.Set(ctx, key(slug), encoded, ttl).Err()
}

// Delete removes an entry. This is what a write does.
func (c *Cache) Delete(ctx context.Context, slug string) error {
	return c.client.Del(ctx, key(slug)).Err()
}

// Lookup is the whole cache-aside read, with the stampede collapsed.
//
// The fetch function returns the entry to cache. A not-found is expressed as an Entry with Missing set, not as
// an error, because a negative result is a result worth caching.
func (c *Cache) Lookup(ctx context.Context, slug string, fetch func(context.Context) (Entry, error)) (Entry, error) {
	if entry, err := c.Get(ctx, slug); err == nil {
		return *entry, nil
	}

	// singleflight.Do returns `shared` telling you whether this call's result was given to more than one
	// caller. That is the measurement: it is how a test proves the collapse happened rather than inferring it
	// from a call count that could be one by luck.
	value, err, shared := c.group.Do(slug, func() (any, error) {
		entry, err := fetch(ctx)
		if err != nil {
			return Entry{}, err
		}

		// The cache write's error is deliberately dropped.
		//
		// The store succeeded, so the caller has a correct answer. A failed Put is a degraded cache, and
		// failing the request because the cache is unavailable is exactly what a cache must not do.
		//
		// Written as a bare `_ =` rather than an `if err != nil { return entry, nil }`, because the second
		// form is indistinguishable from the bug where someone checks an error and forgets to return it. A
		// linter cannot tell them apart either, and it told me so.
		_ = c.Put(ctx, slug, entry)

		return entry, nil
	})

	if shared {
		c.collapsed.Add(1)
	}

	if err != nil {
		return Entry{}, err
	}

	entry, ok := value.(Entry)
	if !ok {
		return Entry{}, fmt.Errorf("cache: fetch for %q returned %T", slug, value)
	}

	return entry, nil
}

// Stats returns a snapshot of the counters.
//
// A snapshot, not a consistent view: the five loads are not one atomic operation, so under concurrent traffic
// the numbers can be from slightly different instants. That is fine for what they are for and it is worth
// saying, because "Hits + Misses == requests" is not guaranteed while requests are in flight.
func (c *Cache) Stats() Stats {
	return Stats{
		Hits:      c.hits.Load(),
		Misses:    c.misses.Load(),
		Negatives: c.negatives.Load(),
		Collapsed: c.collapsed.Load(),
		Errors:    c.errors.Load(),
	}
}
