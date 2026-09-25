package main

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestAtomicCounter(t *testing.T) {
	const n = 10_000

	c := &atomicCounter{}

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Go(c.Inc)
	}
	wg.Wait()

	if got := c.Value(); got != n {
		t.Errorf("counter = %d, want %d", got, n)
	}
}

func TestChannelCounter(t *testing.T) {
	const n = 1000

	c := newChannelCounter()
	defer c.Close()

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Go(c.Inc)
	}
	wg.Wait()

	if got := c.Value(); got != n {
		t.Errorf("counter = %d, want %d", got, n)
	}
}

func TestAllThreeCountersAgree(t *testing.T) {
	const n = 2000

	ac := &atomicCounter{}
	mc := &mutexCounter{}
	cc := newChannelCounter()
	defer cc.Close()

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Go(ac.Inc)
		wg.Go(mc.Inc)
		wg.Go(cc.Inc)
	}
	wg.Wait()

	if ac.Value() != n || mc.Value() != n || cc.Value() != n {
		t.Errorf("counters disagree: atomic=%d mutex=%d channel=%d, want %d",
			ac.Value(), mc.Value(), cc.Value(), n)
	}
}

func TestCompareAndSwapMax(t *testing.T) {
	tests := []struct {
		name        string
		start       int64
		candidate   int64
		wantUpdated bool
		wantFinal   int64
	}{
		{"larger wins", 10, 20, true, 20},
		{"smaller loses", 20, 5, false, 20},
		{"equal loses", 10, 10, false, 10},
		{"negative start", -5, 0, true, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var v atomic.Int64
			v.Store(tt.start)

			updated, attempts := compareAndSwapMax(&v, tt.candidate)

			if updated != tt.wantUpdated {
				t.Errorf("updated = %t, want %t", updated, tt.wantUpdated)
			}
			if v.Load() != tt.wantFinal {
				t.Errorf("final = %d, want %d", v.Load(), tt.wantFinal)
			}
			if attempts < 1 {
				t.Errorf("attempts = %d, want at least 1", attempts)
			}
		})
	}
}

// TestCompareAndSwapMaxUnderContention: many goroutines proposing values must
// leave the true maximum, and never a lower one.
func TestCompareAndSwapMaxUnderContention(t *testing.T) {
	var v atomic.Int64

	var wg sync.WaitGroup
	for i := int64(1); i <= 1000; i++ {
		wg.Go(func() { compareAndSwapMax(&v, i) })
	}
	wg.Wait()

	if got := v.Load(); got != 1000 {
		t.Errorf("max = %d, want 1000", got)
	}
}

func TestServiceFlag(t *testing.T) {
	s := &service{}

	if s.Handle() {
		t.Error("a stopped service should not handle")
	}

	s.Start()
	if !s.Handle() {
		t.Error("a started service should handle")
	}

	s.Stop()
	if s.Handle() {
		t.Error("a stopped service should not handle")
	}

	if got := s.handled.Load(); got != 1 {
		t.Errorf("handled = %d, want 1", got)
	}
}

// TestConsistentSnapshots is what atomic.Pointer gives you that a mutex gives
// only at reader-side cost: a reader either sees the whole old value or the
// whole new one, never a mixture.
func TestConsistentSnapshots(t *testing.T) {
	if testing.Short() {
		t.Skip("contention test")
	}

	if torn := consistentSnapshots(8, 5000); torn != 0 {
		t.Errorf("observed %d torn reads, want 0 — atomic.Pointer swaps the whole value", torn)
	}
}

func TestConfigHolder(t *testing.T) {
	initial := &config{Timeout: time.Second, Retries: 1, Version: 1}
	h := newConfigHolder(initial)

	if got := h.Load(); got != initial {
		t.Error("Load should return the pointer that was stored")
	}

	updated := &config{Timeout: 2 * time.Second, Retries: 2, Version: 2}
	h.Store(updated)

	if got := h.Load(); got != updated {
		t.Error("Load should return the new pointer after Store")
	}
	// The old pointer is still valid for anyone holding it: that is the point.
	if initial.Version != 1 {
		t.Error("storing a new config must not mutate the old one")
	}
}

// The counter benchmarks. These are the numbers behind the README's claim that
// matching the tool to the shape matters.
//
//	go test -bench BenchmarkCounter -benchmem -run '^$' ./09-sync-primitives

func BenchmarkCounterAtomic(b *testing.B) {
	c := &atomicCounter{}

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			c.Inc()
		}
	})
}

func BenchmarkCounterMutex(b *testing.B) {
	c := &mutexCounter{}

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			c.Inc()
		}
	})
}

func BenchmarkCounterChannel(b *testing.B) {
	c := newChannelCounter()
	defer c.Close()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			c.Inc()
		}
	})
}
