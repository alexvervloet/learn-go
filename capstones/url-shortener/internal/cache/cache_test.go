package cache_test

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alexvervloet/learn-go/capstones/url-shortener/internal/apitest"
	"github.com/alexvervloet/learn-go/capstones/url-shortener/internal/cache"
	"github.com/stretchr/testify/require"
)

func newCache(t *testing.T, opts cache.Options) *cache.Cache {
	t.Helper()

	return cache.New(apitest.RedisClient(t), opts)
}

// TestPutAndGet is the round trip.
func TestPutAndGet(t *testing.T) {
	c := newCache(t, cache.Options{TTL: time.Minute})
	ctx := context.Background()

	_, err := c.Get(ctx, "absent")
	require.ErrorIs(t, err, cache.ErrMiss)

	require.NoError(t, c.Put(ctx, "abc", cache.Entry{URLID: 7, Target: "https://example.com/"}))

	entry, err := c.Get(ctx, "abc")
	require.NoError(t, err)
	require.Equal(t, int64(7), entry.URLID)
	require.Equal(t, "https://example.com/", entry.Target)
	require.False(t, entry.Missing)

	stats := c.Stats()
	require.Equal(t, int64(1), stats.Hits)
	require.Equal(t, int64(1), stats.Misses)
}

// TestANegativeEntryIsCachedToo is the probe defence.
func TestANegativeEntryIsCachedToo(t *testing.T) {
	c := newCache(t, cache.Options{TTL: time.Minute, NegativeTTL: time.Minute})
	ctx := context.Background()

	require.NoError(t, c.Put(ctx, "never-existed", cache.Entry{Missing: true}))

	entry, err := c.Get(ctx, "never-existed")
	require.NoError(t, err, "an absence is a cached RESULT, not a cache miss")
	require.True(t, entry.Missing)

	stats := c.Stats()
	require.Equal(t, int64(1), stats.Negatives)
	require.Zero(t, stats.Hits, "a negative is counted separately, so the hit rate is not flattered by probes")
	require.Zero(t, stats.Misses)
}

// TestTheNegativeTTLIsShorter is the asymmetry, asserted.
func TestTheNegativeTTLIsShorter(t *testing.T) {
	client := apitest.RedisClient(t)
	c := cache.New(client, cache.Options{TTL: time.Hour, NegativeTTL: 30 * time.Second})

	ctx := context.Background()

	require.NoError(t, c.Put(ctx, "present", cache.Entry{URLID: 1, Target: "https://example.com/"}))
	require.NoError(t, c.Put(ctx, "absent", cache.Entry{Missing: true}))

	positiveTTL, err := client.TTL(ctx, "slug:present").Result()
	require.NoError(t, err)

	negativeTTL, err := client.TTL(ctx, "slug:absent").Result()
	require.NoError(t, err)

	require.Greater(t, positiveTTL, 50*time.Minute)
	require.LessOrEqual(t, negativeTTL, 30*time.Second)

	// A wrong "it exists" is a redirect to the wrong place; a wrong "it does not exist" is a 404 that fixes
	// itself. The asymmetry in consequence is the asymmetry in TTL.
	require.Greater(t, positiveTTL, negativeTTL)
}

// TestAnEntryNeverOutlivesItsURLsExpiry is the correctness bound on the TTL.
func TestAnEntryNeverOutlivesItsURLsExpiry(t *testing.T) {
	client := apitest.RedisClient(t)
	c := cache.New(client, cache.Options{TTL: time.Hour})

	ctx := context.Background()

	soon := time.Now().Add(20 * time.Second)

	require.NoError(t, c.Put(ctx, "expiring", cache.Entry{
		URLID: 1, Target: "https://example.com/", ExpiresAt: &soon,
	}))

	ttl, err := client.TTL(ctx, "slug:expiring").Result()
	require.NoError(t, err)
	require.LessOrEqual(t, ttl, 20*time.Second,
		"without this the expiry is advisory for up to one cache TTL, which is an hour here")
}

// TestAnAlreadyExpiredEntryIsNotCached is the boundary of that rule.
func TestAnAlreadyExpiredEntryIsNotCached(t *testing.T) {
	client := apitest.RedisClient(t)
	c := cache.New(client, cache.Options{TTL: time.Hour})

	ctx := context.Background()
	past := time.Now().Add(-time.Minute)

	require.NoError(t, c.Put(ctx, "gone", cache.Entry{URLID: 1, Target: "x", ExpiresAt: &past}))

	n, err := client.Exists(ctx, "slug:gone").Result()
	require.NoError(t, err)
	require.Zero(t, n, "a TTL of zero or less would be a key with no expiry at all in Redis")
}

// TestDeleteIsWhatAWriteDoes is the cache-aside invalidation.
func TestDeleteIsWhatAWriteDoes(t *testing.T) {
	c := newCache(t, cache.Options{TTL: time.Minute})
	ctx := context.Background()

	require.NoError(t, c.Put(ctx, "abc", cache.Entry{URLID: 1, Target: "https://old.example/"}))
	require.NoError(t, c.Delete(ctx, "abc"))

	_, err := c.Get(ctx, "abc")
	require.ErrorIs(t, err, cache.ErrMiss,
		"delete rather than update: an update can be applied in the opposite order to the database and leave "+
			"the cache permanently stale")
}

// TestLookupCollapsesAStampede is the singleflight claim.
func TestLookupCollapsesAStampede(t *testing.T) {
	c := newCache(t, cache.Options{TTL: time.Minute})
	ctx := context.Background()

	var fetches atomic.Int64

	fetch := func(context.Context) (cache.Entry, error) {
		fetches.Add(1)

		// Long enough that the other callers are in flight while this one runs. Without a delay, the first
		// fetch returns before the rest arrive and there is nothing to collapse.
		time.Sleep(50 * time.Millisecond)

		return cache.Entry{URLID: 1, Target: "https://example.com/"}, nil
	}

	const callers = 200

	var (
		wg    sync.WaitGroup
		ready sync.WaitGroup
	)

	start := make(chan struct{})

	ready.Add(callers)

	for range callers {
		wg.Add(1)

		go func() {
			defer wg.Done()

			ready.Done()
			<-start

			entry, err := c.Lookup(ctx, "hot", fetch)
			require.NoError(t, err)
			require.Equal(t, "https://example.com/", entry.Target)
		}()
	}

	ready.Wait()
	close(start)
	wg.Wait()

	// A BOUND, not an exact 1.
	//
	// singleflight collapses the callers that arrive while a call is IN FLIGHT. A caller reaching Lookup just
	// after the first fetch returns starts a new one, and on a loaded machine that happens. This repository
	// has the same lesson written down from the caching module, where asserting 1 produced a CI failure on
	// working code.
	const tolerated = 3

	require.LessOrEqual(t, fetches.Load(), int64(tolerated),
		"%d callers produced %d fetches, want at most %d", callers, fetches.Load(), tolerated)
	require.Positive(t, c.Stats().Collapsed, "singleflight reported sharing a result")

	t.Logf("%d concurrent misses became %d fetch(es)", callers, fetches.Load())
}

// TestLookupCachesWhatItFetched is the write-back half of cache-aside.
func TestLookupCachesWhatItFetched(t *testing.T) {
	c := newCache(t, cache.Options{TTL: time.Minute})
	ctx := context.Background()

	var fetches int

	fetch := func(context.Context) (cache.Entry, error) {
		fetches++

		return cache.Entry{URLID: 9, Target: "https://example.com/"}, nil
	}

	for range 5 {
		entry, err := c.Lookup(ctx, "warm", fetch)
		require.NoError(t, err)
		require.Equal(t, int64(9), entry.URLID)
	}

	require.Equal(t, 1, fetches, "the first call populated the cache and the rest were served from it")
	require.Equal(t, int64(4), c.Stats().Hits)
}

// TestAFetchErrorIsNotCached is the rule that stops a blip becoming an outage.
func TestAFetchErrorIsNotCached(t *testing.T) {
	c := newCache(t, cache.Options{TTL: time.Minute})
	ctx := context.Background()

	boom := errors.New("the database is down")

	_, err := c.Lookup(ctx, "flaky", func(context.Context) (cache.Entry, error) {
		return cache.Entry{}, boom
	})
	require.ErrorIs(t, err, boom)

	// The next call fetches again rather than being served a cached failure. Caching an error means one blip
	// keeps returning for a full TTL after the database recovered.
	entry, err := c.Lookup(ctx, "flaky", func(context.Context) (cache.Entry, error) {
		return cache.Entry{URLID: 1, Target: "https://example.com/"}, nil
	})
	require.NoError(t, err)
	require.Equal(t, int64(1), entry.URLID)
}

// TestACorruptValueIsTreatedAsAMiss covers the deploy that changed the struct.
func TestACorruptValueIsTreatedAsAMiss(t *testing.T) {
	client := apitest.RedisClient(t)
	c := cache.New(client, cache.Options{TTL: time.Minute})

	ctx := context.Background()

	require.NoError(t, client.Set(ctx, "slug:broken", "not json at all", time.Minute).Err())

	_, err := c.Get(ctx, "broken")
	require.ErrorIs(t, err, cache.ErrMiss)

	// And it was deleted, so the next reader does not pay the same decode failure.
	n, err := client.Exists(ctx, "slug:broken").Result()
	require.NoError(t, err)
	require.Zero(t, n)
}

// TestTheCacheServesTheRedirect is the integration: API plus Redis plus Postgres.
func TestTheCacheServesTheRedirect(t *testing.T) {
	h := apitest.New(t, apitest.HarnessOptions{WithCache: true, CacheTTL: time.Minute})

	token := h.Register(t, "alex@example.com", "correct horse battery staple")

	resp := h.Do(t, http.MethodPost, "/api/v1/urls", token, map[string]any{"target": "https://example.com/"})

	var created map[string]any

	resp.JSON(t, &created)
	require.Equal(t, http.StatusCreated, resp.Status)

	slug, _ := created["slug"].(string)

	for range 10 {
		r := h.Do(t, http.MethodGet, "/"+slug, "", nil)
		require.Equal(t, http.StatusFound, r.Status)
	}

	stats := h.Cache.Stats()
	require.Equal(t, int64(1), stats.Misses, "one miss: the first redirect")
	require.Equal(t, int64(9), stats.Hits, "nine hits: everything after it")
}

// TestCreatingASlugClearsItsNegativeEntry is the interaction people forget.
func TestCreatingASlugClearsItsNegativeEntry(t *testing.T) {
	h := apitest.New(t, apitest.HarnessOptions{
		WithCache: true, CacheTTL: time.Minute, NegativeTTL: time.Minute,
	})

	token := h.Register(t, "alex@example.com", "correct horse battery staple")

	// Somebody probes for the slug before it exists, which caches the absence for a minute.
	probe := h.Do(t, http.MethodGet, "/my-link", "", nil)

	var problem map[string]any

	probe.JSON(t, &problem)
	require.Equal(t, http.StatusNotFound, probe.Status)

	// Now it is created with that exact slug.
	resp := h.Do(t, http.MethodPost, "/api/v1/urls", token, map[string]any{
		"target": "https://example.com/", "slug": "my-link",
	})

	var created map[string]any

	resp.JSON(t, &created)
	require.Equal(t, http.StatusCreated, resp.Status, "%v", created)

	// Without the cache delete in the create handler, this is a 404 for the rest of the negative TTL.
	redirect := h.Do(t, http.MethodGet, "/my-link", "", nil)

	require.Equal(t, http.StatusFound, redirect.Status)
}

// TestDeletingALinkClearsTheCache is the other direction.
func TestDeletingALinkClearsTheCache(t *testing.T) {
	h := apitest.New(t, apitest.HarnessOptions{WithCache: true, CacheTTL: time.Hour})

	token := h.Register(t, "alex@example.com", "correct horse battery staple")

	resp := h.Do(t, http.MethodPost, "/api/v1/urls", token, map[string]any{"target": "https://example.com/"})

	var created map[string]any

	resp.JSON(t, &created)

	slug, _ := created["slug"].(string)

	// Warm the cache.
	warm := h.Do(t, http.MethodGet, "/"+slug, "", nil)

	require.Equal(t, http.StatusFound, warm.Status)

	del := h.Do(t, http.MethodDelete, "/api/v1/urls/"+slug, token, nil)

	require.Equal(t, http.StatusNoContent, del.Status)

	// Without the invalidation this serves a redirect to a deleted link for an hour.
	gone := h.Do(t, http.MethodGet, "/"+slug, "", nil)

	var problem map[string]any

	gone.JSON(t, &problem)
	require.Equal(t, http.StatusNotFound, gone.Status)
}

// TestOneCallerLeavingDoesNotFailTheOthers is the shared fetch's context. The first caller starts the fetch and
// then hangs up; the second, collapsed onto the same fetch, still gets the entry.
func TestOneCallerLeavingDoesNotFailTheOthers(t *testing.T) {
	c := newCache(t, cache.Options{TTL: time.Minute})

	var (
		fetches atomic.Int64
		once    sync.Once
	)

	started := make(chan struct{})

	fetch := func(ctx context.Context) (cache.Entry, error) {
		fetches.Add(1)
		once.Do(func() { close(started) })

		select {
		case <-ctx.Done():
			return cache.Entry{}, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}

		return cache.Entry{URLID: 1, Target: "https://example.com/"}, nil
	}

	first, cancel := context.WithCancel(context.Background())

	firstErr := make(chan error, 1)

	go func() {
		_, err := c.Lookup(first, "leaver", fetch)
		firstErr <- err
	}()

	<-started

	second := make(chan error, 1)

	go func() {
		entry, err := c.Lookup(context.Background(), "leaver", fetch)
		if err == nil && entry.URLID != 1 {
			err = errors.New("wrong entry")
		}
		second <- err
	}()

	cancel()

	require.ErrorIs(t, <-firstErr, context.Canceled, "the caller that left returns at once")
	require.NoError(t, <-second, "the caller that stayed gets the entry")

	// One fetch, whichever way the second caller arrived: collapsed onto the running one, or after it, from the
	// cache it filled. A fetch cancelled by the first caller would have made the second start its own.
	require.Equal(t, int64(1), fetches.Load())
}
