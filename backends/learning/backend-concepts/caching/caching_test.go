package caching

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/alexvervloet/learn-go/backends/learning/backend-concepts/internal/redistest"
)

// User is the cached value, chosen to have a field that JSON round-trips awkwardly so the encoding is actually
// exercised.
type User struct {
	ID       int64     `json:"id"`
	Name     string    `json:"name"`
	JoinedAt time.Time `json:"joined_at"`
}

// source is a fake source of truth that counts calls and can be made slow or broken.
type source struct {
	calls atomic.Int64

	// delay simulates a slow query, which is what makes a stampede a stampede: a fast source has no
	// stampede problem because the window is too small for 500 callers to arrive in.
	delay time.Duration

	missing map[int64]bool
	broken  error
}

func (s *source) load(_ context.Context, key string) (User, error) {
	s.calls.Add(1)

	if s.delay > 0 {
		time.Sleep(s.delay)
	}

	if s.broken != nil {
		return User{}, s.broken
	}

	id := parseID(key)

	if s.missing[id] {
		return User{}, ErrNotFound
	}

	return User{
		ID:       id,
		Name:     "user " + key,
		JoinedAt: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
	}, nil
}

func newCache(t *testing.T, src *source, opts Options) (*Cache[User], *redis.Client) {
	t.Helper()

	client := redistest.Client(t)
	redistest.Flush(t)

	if opts.Prefix == "" {
		opts.Prefix = "test"
	}

	return New(client, src.load, opts), client
}

func TestCacheAsideHitsAndMisses(t *testing.T) {
	src := &source{}
	c, _ := newCache(t, src, Options{TTL: time.Minute})

	ctx := context.Background()

	// First read: a miss and a load.
	u, err := c.Get(ctx, "1")
	if err != nil {
		t.Fatal(err)
	}
	if u.ID != 1 || u.Name != "user 1" {
		t.Errorf("got %+v", u)
	}

	// Second read: a hit, and the source is not touched.
	if _, err := c.Get(ctx, "1"); err != nil {
		t.Fatal(err)
	}

	// Nine more.
	for range 9 {
		if _, err := c.Get(ctx, "1"); err != nil {
			t.Fatal(err)
		}
	}

	s := c.Stats()

	t.Logf("11 reads of one key: %s", s)
	t.Logf("the source was called %d time(s)", src.calls.Load())

	if src.calls.Load() != 1 {
		t.Errorf("the source was called %d times, want 1", src.calls.Load())
	}
	if s.Hits != 10 || s.Misses != 1 {
		t.Errorf("hits=%d misses=%d, want 10 and 1", s.Hits, s.Misses)
	}
	if s.HitRate() < 0.9 {
		t.Errorf("hit rate is %.2f", s.HitRate())
	}

	// The time round-trips through JSON, which is the thing that quietly breaks when a struct gains
	// a field with no tag.
	cached, err := c.Get(ctx, "1")
	if err != nil {
		t.Fatal(err)
	}
	if !cached.JoinedAt.Equal(u.JoinedAt) {
		t.Errorf("JoinedAt came back as %v, want %v", cached.JoinedAt, u.JoinedAt)
	}
}

// TestStampedeIsCollapsed is the measurement the package exists for.
func TestStampedeIsCollapsed(t *testing.T) {
	// A slow source, because a stampede needs a window for the callers to pile up in. 50ms is
	// roughly a slow-but-real query.
	src := &source{delay: 50 * time.Millisecond}

	c, _ := newCache(t, src, Options{TTL: time.Minute})

	ctx := context.Background()

	const callers = 500

	var (
		wg   sync.WaitGroup
		errs atomic.Int64
	)

	start := make(chan struct{})

	// A readiness barrier as well as a start signal. Without it, close(start) fires while some goroutines
	// have not run their first instruction, and those arrive at Get after the first flight has already
	// finished, which starts a second one.
	var ready sync.WaitGroup

	ready.Add(callers)

	for range callers {
		wg.Add(1)

		go func() {
			defer wg.Done()

			ready.Done()
			<-start

			if _, err := c.Get(ctx, "hot"); err != nil {
				errs.Add(1)
			}
		}()
	}

	ready.Wait()

	began := time.Now()
	close(start)
	wg.Wait()
	elapsed := time.Since(began)

	s := c.Stats()

	t.Logf("%d concurrent readers of one expired key", callers)
	t.Logf("the source was called %d time(s)", src.calls.Load())
	t.Logf("%s", s)
	t.Logf("all %d served in %v, which is about one source call", callers,
		elapsed.Round(time.Millisecond))

	if errs.Load() != 0 {
		t.Errorf("%d callers got an error", errs.Load())
	}

	// The assertion is a BOUND, not an exact 1, and the distinction is the whole subtlety of singleflight.
	//
	// It collapses the callers that arrive while a call is IN FLIGHT. A caller that reaches Get one
	// microsecond after the first flight returns is not late to a flight, it is the start of a new one. With
	// 500 goroutines, a 50ms source and a loaded machine, a straggler is possible, and CI produced exactly
	// that: 2 calls, and a test that failed while the code was working.
	//
	// So: a handful rather than one, and nowhere near 500. That is the property worth having. Asserting 1
	// asserts the scheduler.
	const tolerated = 3

	if calls := src.calls.Load(); calls > tolerated {
		t.Errorf("the source was called %d times for %d concurrent misses, want at most %d",
			calls, callers, tolerated)
	}

	if calls := src.calls.Load(); calls > 1 {
		t.Logf("%d flights rather than 1: some callers arrived after the first finished", calls)
	}

	// And it took about one source call's time, not 500.
	if elapsed > 10*src.delay {
		t.Errorf("took %v for a %v source call, so the loads were not collapsed",
			elapsed, src.delay)
	}

	if s.Collapsed == 0 {
		t.Error("nothing was recorded as collapsed, so the measurement is not working")
	}

	t.Logf("without singleflight this is %d queries and %v of source load; with it, 1 and %v",
		callers, time.Duration(callers)*src.delay, src.delay)
}

// TestWithoutSingleflightTheSourceIsHammered is the control, so the number above means something.
func TestWithoutSingleflightTheSourceIsHammered(t *testing.T) {
	client := redistest.Client(t)
	redistest.Flush(t)

	src := &source{delay: 50 * time.Millisecond}

	ctx := context.Background()

	// Cache-aside by hand, with no collapsing. This is the code the Cache type replaces.
	get := func(key string) error {
		if _, err := client.Get(ctx, "naive:"+key).Result(); err == nil {
			return nil
		}

		u, err := src.load(ctx, key)
		if err != nil {
			return err
		}

		return client.Set(ctx, "naive:"+key, u.Name, time.Minute).Err()
	}

	const callers = 500

	var wg sync.WaitGroup

	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := get("hot"); err != nil {
				t.Errorf("get: %v", err)
			}
		}()
	}

	wg.Wait()

	t.Logf("the same %d concurrent readers without singleflight called the source %d times",
		callers, src.calls.Load())

	// Not 500 exactly: some callers arrive after the first Set lands. The point is that it is far
	// more than one, and on a real service the number scales with how slow the source is.
	if src.calls.Load() < 10 {
		t.Errorf("the source was called only %d times, so this control is not showing the "+
			"problem", src.calls.Load())
	}

	t.Logf("%d source calls against 1, which is what the singleflight.Group buys", src.calls.Load())
}

// TestJitterSpreadsExpiry measures the spread rather than asserting the line of code exists.
func TestJitterSpreadsExpiry(t *testing.T) {
	ctx := context.Background()

	measure := func(jitter float64) (min, max time.Duration) {
		src := &source{}
		c, _ := newCache(t, src, Options{TTL: 10 * time.Minute, Jitter: jitter})

		min, max = time.Hour, 0

		for i := range 50 {
			key := itoa(i)

			if _, err := c.Get(ctx, key); err != nil {
				t.Fatal(err)
			}

			ttl, err := c.TTLOf(ctx, key)
			if err != nil {
				t.Fatal(err)
			}
			if ttl < 0 {
				t.Fatalf("key %s has TTL %v, so it was not stored with one", key, ttl)
			}

			min = minDuration(min, ttl)
			max = maxDuration(max, ttl)
		}

		return min, max
	}

	noJitterMin, noJitterMax := measure(0)
	jitterMin, jitterMax := measure(0.1)

	t.Logf("no jitter:  TTLs span %v to %v (spread %v)",
		noJitterMin.Round(time.Millisecond), noJitterMax.Round(time.Millisecond),
		(noJitterMax - noJitterMin).Round(time.Millisecond))
	t.Logf("10%% jitter: TTLs span %v to %v (spread %v)",
		jitterMin.Round(time.Millisecond), jitterMax.Round(time.Millisecond),
		(jitterMax - jitterMin).Round(time.Millisecond))

	// Without jitter, 50 keys written in a few milliseconds expire within a few milliseconds of each
	// other, forever.
	if noJitterMax-noJitterMin > time.Second {
		t.Errorf("the unjittered TTLs span %v, which they should not",
			noJitterMax-noJitterMin)
	}

	// With 10% jitter on 10 minutes, the spread should be most of 2 minutes.
	if jitterMax-jitterMin < time.Minute {
		t.Errorf("the jittered TTLs span only %v, want more than a minute",
			jitterMax-jitterMin)
	}

	// And the jitter stays inside its bounds, so "a 10 minute cache" is not secretly a 20 minute one.
	if jitterMin < 9*time.Minute || jitterMax > 11*time.Minute {
		t.Errorf("jittered TTLs range %v to %v, outside 10 minutes plus or minus 10%%",
			jitterMin, jitterMax)
	}

	t.Logf("50 keys written together now expire over %v rather than within %v, so the source "+
		"sees a trickle instead of a spike every TTL",
		(jitterMax - jitterMin).Round(time.Second),
		(noJitterMax - noJitterMin).Round(time.Millisecond))
}

// TestNegativeCachingStopsAScan is the failure that turns a 404 sweep into a database problem.
func TestNegativeCachingStopsAScan(t *testing.T) {
	ctx := context.Background()

	missing := map[int64]bool{}
	for i := int64(1); i <= 20; i++ {
		missing[i] = true
	}

	// Without negative caching: every request for a missing key reaches the source.
	withoutSrc := &source{missing: missing}
	without, _ := newCache(t, withoutSrc, Options{TTL: time.Minute, Prefix: "neg-off"})

	for range 5 {
		for i := 1; i <= 20; i++ {
			_, err := without.Get(ctx, itoa(i))
			if !errors.Is(err, ErrNotFound) {
				t.Fatalf("expected ErrNotFound, got %v", err)
			}
		}
	}

	t.Logf("100 requests for 20 missing keys, no negative caching: %d source calls",
		withoutSrc.calls.Load())

	if withoutSrc.calls.Load() != 100 {
		t.Errorf("the source was called %d times, want 100", withoutSrc.calls.Load())
	}

	// With it: 20 source calls, then the absence is remembered.
	withSrc := &source{missing: missing}
	with, _ := newCache(t, withSrc, Options{
		TTL:         time.Minute,
		NegativeTTL: 30 * time.Second,
		Prefix:      "neg-on",
	})

	for range 5 {
		for i := 1; i <= 20; i++ {
			_, err := with.Get(ctx, itoa(i))
			if !errors.Is(err, ErrNotFound) {
				t.Fatalf("expected ErrNotFound, got %v", err)
			}
		}
	}

	s := with.Stats()

	t.Logf("the same 100 requests with a 30s negative TTL: %d source calls", withSrc.calls.Load())
	t.Logf("%s", s)

	if withSrc.calls.Load() != 20 {
		t.Errorf("the source was called %d times, want 20", withSrc.calls.Load())
	}

	// A remembered absence counts as a hit, because the cache answered and answered correctly.
	if s.NegativeHits != 80 {
		t.Errorf("%d negative hits, want 80", s.NegativeHits)
	}
	if s.Hits != 80 {
		t.Errorf("%d hits, want 80", s.Hits)
	}

	t.Logf("%d source calls against %d, which is the difference between an id scan being "+
		"noise and being an incident", withSrc.calls.Load(), withoutSrc.calls.Load())

	// And the negative TTL is shorter than the positive one, which is the point: a row about to be
	// created must not be remembered as absent for the full cache lifetime.
	ttl, err := with.TTLOf(ctx, "1")
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("the remembered absence expires in %v, not the %v a value would get",
		ttl.Round(time.Second), time.Minute)

	if ttl > 30*time.Second {
		t.Errorf("the negative entry has a TTL of %v, longer than the configured 30s", ttl)
	}
}

// TestNegativeMarkerIsNotAValue, because an empty value is a legitimate thing to cache.
func TestNegativeMarkerIsNotAValue(t *testing.T) {
	ctx := context.Background()

	client := redistest.Client(t)
	redistest.Flush(t)

	// A cache of strings, where "" is a real value.
	calls := 0
	empty := New(client, func(_ context.Context, key string) (string, error) {
		calls++
		if key == "gone" {
			return "", ErrNotFound
		}
		return "", nil
	}, Options{TTL: time.Minute, NegativeTTL: time.Minute, Prefix: "marker"})

	// An empty string that EXISTS.
	v, err := empty.Get(ctx, "present")
	if err != nil {
		t.Fatalf("an empty string is a value, not an absence: %v", err)
	}
	if v != "" {
		t.Errorf("got %q", v)
	}

	// Read it again: it comes back as a value, not as ErrNotFound.
	v, err = empty.Get(ctx, "present")
	if err != nil {
		t.Errorf("the cached empty string came back as %v", err)
	}
	if v != "" {
		t.Errorf("got %q", v)
	}

	// And a real absence is still an absence.
	if _, err := empty.Get(ctx, "gone"); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
	if _, err := empty.Get(ctx, "gone"); !errors.Is(err, ErrNotFound) {
		t.Errorf("the cached absence came back as %v", err)
	}

	t.Logf("the source was called %d times for 4 requests: once for each distinct key", calls)

	if calls != 2 {
		t.Errorf("the source was called %d times, want 2", calls)
	}
}

// TestInvalidateIsOneRoundTrip, because a loop of DELs is the N+1 problem in a third place.
func TestInvalidateIsOneRoundTrip(t *testing.T) {
	src := &source{}
	c, client := newCache(t, src, Options{TTL: time.Minute})

	ctx := context.Background()

	keys := make([]string, 50)
	for i := range keys {
		keys[i] = itoa(i)
		if _, err := c.Get(ctx, keys[i]); err != nil {
			t.Fatal(err)
		}
	}

	before, err := client.DBSize(ctx).Result()
	if err != nil {
		t.Fatal(err)
	}

	if err := c.Invalidate(ctx, keys...); err != nil {
		t.Fatal(err)
	}

	after, err := client.DBSize(ctx).Result()
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("%d keys before, %d after one Invalidate call", before, after)

	// The difference, not after == 0: the database also holds the harness's marker key, and a test
	// that assumes it owns every key breaks the moment anything else shares the database.
	if removed := before - after; removed != int64(len(keys)) {
		t.Errorf("one Invalidate call removed %d of %d keys", removed, len(keys))
	}

	// And the next read reloads from the source.
	callsBefore := src.calls.Load()

	if _, err := c.Get(ctx, "0"); err != nil {
		t.Fatal(err)
	}

	if src.calls.Load() != callsBefore+1 {
		t.Error("the invalidated key was served from the cache")
	}

	// Invalidating nothing is not an error and does not make a round trip.
	if err := c.Invalidate(ctx); err != nil {
		t.Errorf("invalidating nothing: %v", err)
	}
}

// TestUndecodableValueIsDeletedAndReloaded is what makes a struct change survivable.
func TestUndecodableValueIsDeletedAndReloaded(t *testing.T) {
	src := &source{}
	c, client := newCache(t, src, Options{TTL: time.Minute})

	ctx := context.Background()

	// Something a previous version of the code wrote, which this version cannot decode.
	if err := client.Set(ctx, "test:1", `{"id":"not a number"}`, time.Minute).Err(); err != nil {
		t.Fatal(err)
	}

	u, err := c.Get(ctx, "1")
	if err != nil {
		t.Fatalf("a bad cached value should fall back to the source: %v", err)
	}
	if u.ID != 1 {
		t.Errorf("got %+v", u)
	}

	s := c.Stats()

	t.Logf("after reading an undecodable value: %s", s)

	if s.Errors == 0 {
		t.Error("the decode failure was not counted")
	}
	if src.calls.Load() != 1 {
		t.Errorf("the source was called %d times, want 1", src.calls.Load())
	}

	// And it was replaced, so the next read is a hit rather than another decode failure.
	if _, err := c.Get(ctx, "1"); err != nil {
		t.Fatal(err)
	}

	if src.calls.Load() != 1 {
		t.Error("the bad value was not replaced; the source was called again")
	}
}

// TestBrokenCacheStillServesRequests: a cache outage must not be a service outage.
func TestBrokenCacheStillServesRequests(t *testing.T) {
	redis.SetLogger(testLogger{t})
	t.Cleanup(func() { redis.SetLogger(silentLogger{}) })

	broken := redis.NewClient(&redis.Options{
		Addr:         "127.0.0.1:1",
		DialTimeout:  200 * time.Millisecond,
		ReadTimeout:  200 * time.Millisecond,
		WriteTimeout: 200 * time.Millisecond,
		MaxRetries:   -1,
	})
	t.Cleanup(func() { _ = broken.Close() })

	src := &source{}

	c := New(broken, src.load, Options{TTL: time.Minute})

	ctx := context.Background()

	for i := range 3 {
		u, err := c.Get(ctx, "1")
		if err != nil {
			t.Fatalf("read %d failed even though the source is fine: %v", i, err)
		}
		if u.ID != 1 {
			t.Errorf("got %+v", u)
		}
	}

	s := c.Stats()

	t.Logf("with Redis unreachable: %s", s)
	t.Logf("the source was called %d times for 3 reads, because nothing could be cached",
		src.calls.Load())

	if src.calls.Load() != 3 {
		t.Errorf("the source was called %d times, want 3", src.calls.Load())
	}
	if s.Errors == 0 {
		t.Error("the Redis failures were not counted, so nothing would alert")
	}

	t.Log("every request succeeded and every one was slow. That is the right failure: a cache " +
		"outage becomes a latency problem rather than an availability one, and the error " +
		"count is what tells you which.")
}

// TestSingleflightDoesNotShareErrors, which is a property worth knowing.
func TestSingleflightDoesNotShareErrors(t *testing.T) {
	src := &source{delay: 20 * time.Millisecond, broken: errors.New("database is down")}

	c, _ := newCache(t, src, Options{TTL: time.Minute})

	ctx := context.Background()

	var (
		wg     sync.WaitGroup
		failed atomic.Int64
	)

	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.Get(ctx, "hot"); err != nil {
				failed.Add(1)
			}
		}()
	}

	wg.Wait()

	t.Logf("100 callers, a broken source: %d failed, %d source calls",
		failed.Load(), src.calls.Load())

	// Every caller gets the error, and the source was called once. So an outage does not become a
	// stampede either, which is the property that matters: the failure is shared, not repeated.
	if failed.Load() != 100 {
		t.Errorf("%d callers failed, want 100", failed.Load())
	}
	if src.calls.Load() > 2 {
		t.Errorf("the source was called %d times while broken; the error should be shared "+
			"too", src.calls.Load())
	}

	// And nothing was cached, so a fixed source works immediately rather than after a TTL.
	src.broken = nil

	u, err := c.Get(ctx, "hot")
	if err != nil {
		t.Fatalf("after the source recovered: %v", err)
	}
	if u.ID != 0 {
		t.Logf("recovered and loaded %+v", u)
	}
}

type testLogger struct{ t *testing.T }

func (l testLogger) Printf(_ context.Context, format string, v ...any) {
	l.t.Logf("go-redis: "+format, v...)
}

type silentLogger struct{}

func (silentLogger) Printf(context.Context, string, ...any) {}

func parseID(s string) int64 {
	var n int64
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int64(r-'0')
	}
	return n
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}

	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

func maxDuration(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}

// TestOneCallerLeavingDoesNotFailTheOthers: singleflight runs the load once, with the context of whichever caller
// arrived first. The first version passed that context straight to the loader, so when that one client
// disconnected, the load was cancelled and every caller collapsed onto it got context.Canceled, including callers
// whose own requests were fine.
func TestOneCallerLeavingDoesNotFailTheOthers(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once

	load := func(ctx context.Context, key string) (User, error) {
		once.Do(func() { close(started) })
		select {
		case <-release:
			return User{ID: parseID(key), Name: "loaded"}, nil
		case <-ctx.Done():
			return User{}, ctx.Err()
		}
	}

	client := redistest.Client(t)
	redistest.Flush(t)
	c := New(client, load, Options{TTL: time.Minute, Prefix: "leave"})

	leaderCtx, leaderLeaves := context.WithCancel(context.Background())
	leaderErr := make(chan error, 1)
	go func() {
		_, err := c.Get(leaderCtx, "7")
		leaderErr <- err
	}()
	<-started

	follower := make(chan error, 1)
	go func() {
		_, err := c.Get(context.Background(), "7")
		follower <- err
	}()
	time.Sleep(50 * time.Millisecond) // let the follower join the load in flight

	leaderLeaves()
	if err := <-leaderErr; !errors.Is(err, context.Canceled) {
		t.Errorf("the caller that left got %v, want context.Canceled", err)
	}

	close(release)
	if err := <-follower; err != nil {
		t.Errorf("a caller whose request was fine got %v because another caller left", err)
	}
}
