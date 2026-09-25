package main

import (
	"sync"
	"testing"
)

func TestCounterIsCorrectUnderConcurrency(t *testing.T) {
	const goroutines = 5000

	c := &Counter{}
	got := countConcurrently(goroutines, c.Inc, c.Value)

	if got != goroutines {
		t.Errorf("counter = %d, want %d — a mutex must not lose updates", got, goroutines)
	}
}

// TestRacyCounterLosesUpdates asserts the bug. If this ever passes with the
// full count, either the test got lucky or something changed, and either way
// the README's claim needs rechecking.
//
// It is not run under -race in CI as a failure: the race detector reports the
// race, which is the point. Here we only check the arithmetic outcome.
func TestRacyCounterLosesUpdates(t *testing.T) {
	if testing.Short() {
		t.Skip("racy by design")
	}
	if raceDetectorEnabled {
		// The detector is right, and reporting it would fail the package. The
		// race is the point of racyCounter, so this test steps aside and
		// lesson 16 shows the report in full.
		t.Skip("skipped under -race: this test exercises a deliberate data race")
	}

	const goroutines = 10_000

	// Repeat: a single run can get lucky on a quiet machine.
	lostAtLeastOnce := false
	for run := 0; run < 5; run++ {
		c := &racyCounter{}
		got := countConcurrently(goroutines, c.Inc, func() int { return c.value })
		if got < goroutines {
			lostAtLeastOnce = true
			t.Logf("run %d: %d of %d increments survived", run, got, goroutines)
			break
		}
	}

	if !lostAtLeastOnce {
		t.Skip("no lost updates observed; value++ happened to be atomic enough on this machine")
	}
}

func TestCounterAdd(t *testing.T) {
	c := &Counter{}

	var wg sync.WaitGroup
	for i := 1; i <= 100; i++ {
		wg.Go(func() { c.Add(i) })
	}
	wg.Wait()

	if want := 5050; c.Value() != want {
		t.Errorf("sum = %d, want %d", c.Value(), want)
	}
}

// TestCheckThenActLosesUpdates is the bug that survives having a mutex.
func TestCheckThenActLosesUpdates(t *testing.T) {
	if testing.Short() {
		t.Skip("contention-dependent")
	}

	const goroutines = 5000

	t.Run("two locked sections lose updates", func(t *testing.T) {
		b := newBrokenMap()

		var wg sync.WaitGroup
		for i := 0; i < goroutines; i++ {
			wg.Go(func() { b.IncBroken("k") })
		}
		wg.Wait()

		got := b.Get("k")
		t.Logf("%d of %d increments survived", got, goroutines)

		if got == goroutines {
			t.Skip("no lost updates observed this run")
		}
	})

	t.Run("one locked section loses none", func(t *testing.T) {
		b := newBrokenMap()

		var wg sync.WaitGroup
		for i := 0; i < goroutines; i++ {
			wg.Go(func() { b.IncFixed("k") })
		}
		wg.Wait()

		if got := b.Get("k"); got != goroutines {
			t.Errorf("got %d, want %d", got, goroutines)
		}
	})
}

func TestCache(t *testing.T) {
	c := NewCache()

	if _, ok := c.Get("missing"); ok {
		t.Error("an empty cache should report a miss")
	}

	c.Set("key", "value")
	v, ok := c.Get("key")
	if !ok || v != "value" {
		t.Errorf("Get = %q, %t; want \"value\", true", v, ok)
	}
	if c.Len() != 1 {
		t.Errorf("Len = %d, want 1", c.Len())
	}

	c.Set("key", "updated")
	if v, _ := c.Get("key"); v != "updated" {
		t.Errorf("after overwrite Get = %q, want \"updated\"", v)
	}
	if c.Len() != 1 {
		t.Errorf("Len = %d after overwriting, want 1", c.Len())
	}
}

// TestGetOrComputeRunsOnce is the double-check pattern's whole purpose: 100
// goroutines racing for a missing key must produce exactly one computation.
//
// Without the recheck after upgrading the lock, several would compute and all
// but one result would be discarded, which for an expensive computation is the
// entire cost the cache was meant to avoid.
func TestGetOrComputeRunsOnce(t *testing.T) {
	c := NewCache()

	var (
		computations int
		mu           sync.Mutex
		wg           sync.WaitGroup
	)

	for i := 0; i < 200; i++ {
		wg.Go(func() {
			v, _ := c.GetOrCompute("key", func() string {
				mu.Lock()
				computations++
				mu.Unlock()
				return "computed"
			})
			if v != "computed" {
				t.Errorf("got %q, want \"computed\"", v)
			}
		})
	}
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()

	if computations != 1 {
		t.Errorf("computed %d times, want exactly 1 — the double-check should prevent duplicates", computations)
	}
	if c.Len() != 1 {
		t.Errorf("cache holds %d entries, want 1", c.Len())
	}
}

func TestGetOrComputeReportsWhetherItComputed(t *testing.T) {
	c := NewCache()

	v, computed := c.GetOrCompute("k", func() string { return "first" })
	if v != "first" || !computed {
		t.Errorf("first call: %q, computed=%t; want \"first\", true", v, computed)
	}

	v, computed = c.GetOrCompute("k", func() string { return "second" })
	if v != "first" || computed {
		t.Errorf("second call: %q, computed=%t; want \"first\", false", v, computed)
	}
}

func TestCopylocksGuidanceIsDocumented(t *testing.T) {
	if got := copyingAMutexBreaksIt(); len(got) < 3 {
		t.Errorf("expected at least 3 documented points, got %d", len(got))
	}
}

// Benchmarks: Mutex against RWMutex at different read/write mixes. The whole
// point of this pair is that RWMutex is NOT a free upgrade, and the crossover
// depends on the workload and the machine.
//
//	go test -bench 'BenchmarkCache' -benchmem -run '^$' ./09-sync-primitives

func BenchmarkCacheReadHeavyRWMutex(b *testing.B) {
	c := NewCache()
	c.Set("key", "value")

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = c.Get("key")
		}
	})
}

// mutexCache is the same cache with a plain Mutex, to compare against.
type mutexCache struct {
	mu    sync.Mutex
	items map[string]string
}

func (c *mutexCache) Get(key string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.items[key]
	return v, ok
}

func (c *mutexCache) Set(key, value string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items[key] = value
}

func BenchmarkCacheReadHeavyMutex(b *testing.B) {
	c := &mutexCache{items: map[string]string{"key": "value"}}

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = c.Get("key")
		}
	})
}

// The pair above uses a critical section of one map lookup, which is the
// case where RWMutex loses: the RLock bookkeeping costs more than the
// parallelism it buys.
//
// The pair below makes the critical section long enough to matter. This is
// where RWMutex earns its keep, and finding the crossover on your own hardware
// is more useful than any rule of thumb.

// slowRead simulates a read whose work is non-trivial: a hash lookup plus some
// computation over the value, a JSON decode, a template render.
func slowRead(v string) int {
	sum := 0
	for i := 0; i < 200; i++ {
		for _, c := range v {
			sum += int(c) * i
		}
	}
	return sum
}

func BenchmarkSlowReadRWMutex(b *testing.B) {
	c := NewCache()
	c.Set("key", "a reasonably long cached value to work over")

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			v, _ := c.Get("key")
			_ = slowRead(v)
		}
	})
}

func BenchmarkSlowReadMutex(b *testing.B) {
	c := &mutexCache{items: map[string]string{"key": "a reasonably long cached value to work over"}}

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			v, _ := c.Get("key")
			_ = slowRead(v)
		}
	})
}

// The pair above still does the slow part OUTSIDE the lock, which is correct
// code and therefore not a fair test of the lock itself. These two hold the
// lock for the whole computation, which is what a real long critical section
// looks like.

func (c *Cache) SlowGet(key string) int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return slowRead(c.items[key])
}

func (c *mutexCache) SlowGet(key string) int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return slowRead(c.items[key])
}

func BenchmarkLongCriticalSectionRWMutex(b *testing.B) {
	c := NewCache()
	c.Set("key", "a reasonably long cached value to work over")

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = c.SlowGet("key")
		}
	})
}

func BenchmarkLongCriticalSectionMutex(b *testing.B) {
	c := &mutexCache{items: map[string]string{"key": "a reasonably long cached value to work over"}}

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = c.SlowGet("key")
		}
	})
}
