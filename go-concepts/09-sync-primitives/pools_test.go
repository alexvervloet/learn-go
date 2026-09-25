package main

import (
	"bytes"
	"strings"
	"sync"
	"testing"
)

func TestRenderWithAndWithoutPoolAgree(t *testing.T) {
	tests := [][]string{
		nil,
		{"alpha"},
		{"alpha", "beta", "gamma"},
		{"", "empty first"},
	}

	for _, rows := range tests {
		withPool := renderWithPool(rows)
		withoutPool := renderWithoutPool(rows)

		if withPool != withoutPool {
			t.Errorf("rows %v: pooled %q, unpooled %q", rows, withPool, withoutPool)
		}
	}
}

// TestPooledBufferIsResetBetweenUses is the property that matters for
// correctness rather than speed. Without the Reset, one caller's content
// appears in another caller's output.
func TestPooledBufferIsResetBetweenUses(t *testing.T) {
	first := renderWithPool([]string{"user-1"})

	// Many subsequent calls, any of which might receive the same buffer.
	for i := 0; i < 100; i++ {
		got := renderWithPool([]string{"user-2"})

		if strings.Contains(got, "user-1") {
			t.Fatalf("call %d returned %q, which contains the previous caller's data", i, got)
		}
	}

	if !strings.Contains(first, "user-1") {
		t.Errorf("first call = %q, want it to contain user-1", first)
	}
}

// TestPoolIsSafeUnderConcurrency: a Pool is safe for concurrent use, and each
// goroutine must get a buffer nobody else is writing to.
func TestPoolIsSafeUnderConcurrency(t *testing.T) {
	const goroutines = 200

	results := make([]string, goroutines)

	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Go(func() {
			results[i] = renderWithPool([]string{strings.Repeat("x", i%10+1)})
		})
	}
	wg.Wait()

	for i, got := range results {
		want := renderWithoutPool([]string{strings.Repeat("x", i%10+1)})
		if got != want {
			t.Errorf("goroutine %d: got %q, want %q", i, got, want)
		}
	}
}

// TestForgettingResetLeaksData asserts the CONSEQUENCE of the bug, not the
// scheduling that exposes it.
//
// The first version of this test called forgettingResetLeaksData once and
// asserted the leak. It passed locally and on seven of eight CI jobs, and
// failed on the race job, because sync.Pool.Get is not guaranteed to return
// what you just Put: the pool is a per-P cache and the goroutine can move.
//
// leakIsObservable retries until reuse actually happens, so the assertion is
// about what a reused buffer contains rather than about whether reuse occurs.
func TestForgettingResetLeaksData(t *testing.T) {
	leaked, observed := leakIsObservable(200)

	if !observed {
		t.Skip("the pool did not hand the buffer back in 200 attempts; the bug is still real")
	}

	if !strings.Contains(leaked, "user-1-secret") {
		t.Errorf("a reused buffer gave %q, want it to still contain the first caller's data", leaked)
	}
	if !strings.Contains(leaked, "user-2-data") {
		t.Errorf("a reused buffer gave %q, want it to also contain its own data", leaked)
	}
}

// TestForgettingResetReportsReuseHonestly: whatever the scheduling, the first
// caller's own read must be correct.
func TestForgettingResetFirstCallerIsUnaffected(t *testing.T) {
	first, _, _ := forgettingResetLeaksData()

	if first != "user-1-secret" {
		t.Errorf("first = %q, want %q", first, "user-1-secret")
	}
}

func TestGCClearsThePool(t *testing.T) {
	beforeGC, afterGC := gcClearsThePool()

	if !beforeGC {
		t.Error("an item Put into a pool should be retrievable straight away")
	}
	if afterGC {
		t.Error("pool contents should not survive two GC cycles — this is why a Pool is not a resource pool")
	}
}

func TestPoolGuidanceIsDocumented(t *testing.T) {
	if got := whatAPoolMustNeverHold(); len(got) < 4 {
		t.Errorf("expected at least 4 documented rules, got %d", len(got))
	}
}

// The benchmark behind the whole feature. A Pool only pays for itself when the
// allocation is a meaningful share of the work.
//
//	go test -bench BenchmarkRender -benchmem -run '^$' ./09-sync-primitives

func BenchmarkRenderWithPool(b *testing.B) {
	rows := []string{"alpha", "beta", "gamma", "delta", "epsilon"}

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = renderWithPool(rows)
		}
	})
}

func BenchmarkRenderWithoutPool(b *testing.B) {
	rows := []string{"alpha", "beta", "gamma", "delta", "epsilon"}

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = renderWithoutPool(rows)
		}
	})
}

// And the case where a Pool is a clear win: a large buffer, where the
// allocation dominates.
func BenchmarkLargeBufferWithPool(b *testing.B) {
	pool := sync.Pool{New: func() any { return new(bytes.Buffer) }}

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			buf := pool.Get().(*bytes.Buffer)
			buf.Grow(64 * 1024)
			for i := 0; i < 1000; i++ {
				buf.WriteString("some payload data")
			}
			buf.Reset()
			pool.Put(buf)
		}
	})
}

func BenchmarkLargeBufferWithoutPool(b *testing.B) {
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			var buf bytes.Buffer
			buf.Grow(64 * 1024)
			for i := 0; i < 1000; i++ {
				buf.WriteString("some payload data")
			}
		}
	})
}
