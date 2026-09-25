package main

import (
	"strings"
	"sync"
	"testing"
)

func TestRWMutexMap(t *testing.T) {
	m := newRWMutexMap[string, int]()

	if _, ok := m.Load("missing"); ok {
		t.Error("an empty map should report a miss")
	}

	m.Store("a", 1)
	if v, ok := m.Load("a"); !ok || v != 1 {
		t.Errorf("Load = %d, %t; want 1, true", v, ok)
	}

	actual, loaded := m.LoadOrStore("a", 99)
	if !loaded || actual != 1 {
		t.Errorf("LoadOrStore on an existing key = %d, %t; want 1, true", actual, loaded)
	}

	actual, loaded = m.LoadOrStore("b", 2)
	if loaded || actual != 2 {
		t.Errorf("LoadOrStore on a new key = %d, %t; want 2, false", actual, loaded)
	}

	if m.Len() != 2 {
		t.Errorf("Len = %d, want 2", m.Len())
	}

	m.Delete("a")
	if _, ok := m.Load("a"); ok {
		t.Error("Delete should remove the key")
	}
	if m.Len() != 1 {
		t.Errorf("Len = %d after delete, want 1", m.Len())
	}
}

// TestLoadOrStoreIsAtomic is the property that makes it worth having: 500
// goroutines racing to create the same key must all receive the same value,
// and exactly one must be told it stored.
func TestLoadOrStoreIsAtomic(t *testing.T) {
	m := newRWMutexMap[string, int]()

	var (
		mu      sync.Mutex
		stored  int
		wg      sync.WaitGroup
		results = make([]int, 500)
	)

	for i := 0; i < 500; i++ {
		wg.Go(func() {
			actual, loaded := m.LoadOrStore("key", i)
			results[i] = actual
			if !loaded {
				mu.Lock()
				stored++
				mu.Unlock()
			}
		})
	}
	wg.Wait()

	if stored != 1 {
		t.Errorf("%d goroutines reported storing, want exactly 1", stored)
	}

	first := results[0]
	for i, v := range results {
		if v != first {
			t.Errorf("goroutine %d got %d, others got %d — all must see the same value", i, v, first)
			break
		}
	}
}

func TestSyncMapIsUntyped(t *testing.T) {
	value, needsAssertion, panicMsg := syncMapIsUntyped()

	if value != 42 {
		t.Errorf("value = %d, want 42", value)
	}
	if !needsAssertion {
		t.Error("reading from a sync.Map should require a type assertion")
	}
	if panicMsg == "" {
		t.Error("asserting the wrong type should panic")
	}
	if !strings.Contains(panicMsg, "interface conversion") {
		t.Errorf("panic = %q, want an interface conversion error", panicMsg)
	}
}

func TestTypedSyncMap(t *testing.T) {
	m := &typedSyncMap[string, int]{}

	if _, ok := m.Load("missing"); ok {
		t.Error("empty map should report a miss")
	}

	m.Store("a", 1)
	m.Store("b", 2)

	if v, ok := m.Load("a"); !ok || v != 1 {
		t.Errorf("Load = %d, %t; want 1, true", v, ok)
	}

	actual, loaded := m.LoadOrStore("a", 99)
	if !loaded || actual != 1 {
		t.Errorf("LoadOrStore = %d, %t; want 1, true", actual, loaded)
	}

	if m.Len() != 2 {
		t.Errorf("Len = %d, want 2", m.Len())
	}

	seen := map[string]int{}
	m.Range(func(k string, v int) bool {
		seen[k] = v
		return true
	})
	if len(seen) != 2 || seen["a"] != 1 || seen["b"] != 2 {
		t.Errorf("Range saw %v, want map[a:1 b:2]", seen)
	}
}

// TestRangeCanStopEarly: returning false halts the walk, which is how you
// implement a find without visiting everything.
func TestRangeCanStopEarly(t *testing.T) {
	m := &typedSyncMap[int, int]{}
	for i := 0; i < 100; i++ {
		m.Store(i, i)
	}

	visited := 0
	m.Range(func(int, int) bool {
		visited++
		return visited < 5
	})

	if visited != 5 {
		t.Errorf("visited %d entries, want 5 — returning false should stop Range", visited)
	}
}

func TestBothMapsBehaveIdentically(t *testing.T) {
	sm := &typedSyncMap[string, int]{}
	rw := newRWMutexMap[string, int]()

	ops := []struct {
		key   string
		value int
	}{
		{"a", 1}, {"b", 2}, {"a", 3}, {"c", 4},
	}

	for _, op := range ops {
		smActual, smLoaded := sm.LoadOrStore(op.key, op.value)
		rwActual, rwLoaded := rw.LoadOrStore(op.key, op.value)

		if smActual != rwActual || smLoaded != rwLoaded {
			t.Errorf("LoadOrStore(%q, %d): sync.Map gave %d/%t, RWMutex gave %d/%t",
				op.key, op.value, smActual, smLoaded, rwActual, rwLoaded)
		}
	}

	if sm.Len() != rw.Len() {
		t.Errorf("lengths differ: sync.Map %d, RWMutex %d", sm.Len(), rw.Len())
	}
}

func TestWhenToUseSyncMapIsDocumented(t *testing.T) {
	if got := whenToUseSyncMap(); len(got) < 4 {
		t.Errorf("expected at least 4 documented points, got %d", len(got))
	}
}

// The benchmark pair behind the README's advice. Case 1 from the standard
// library docs (write once, read many) is where sync.Map is meant to win; the
// mixed workload is where it is meant to lose.
//
//	go test -bench BenchmarkMap -benchmem -run '^$' ./09-sync-primitives

func BenchmarkMapReadHeavySyncMap(b *testing.B) {
	m := &typedSyncMap[int, int]{}
	for i := 0; i < 1000; i++ {
		m.Store(i, i*i)
	}

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			_, _ = m.Load(i % 1000)
			i++
		}
	})
}

func BenchmarkMapReadHeavyRWMutex(b *testing.B) {
	m := newRWMutexMap[int, int]()
	for i := 0; i < 1000; i++ {
		m.Store(i, i*i)
	}

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			_, _ = m.Load(i % 1000)
			i++
		}
	})
}

func BenchmarkMapMixedSyncMap(b *testing.B) {
	m := &typedSyncMap[int, int]{}

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			if i%2 == 0 {
				m.Store(i%1000, i)
			} else {
				_, _ = m.Load(i % 1000)
			}
			i++
		}
	})
}

func BenchmarkMapMixedRWMutex(b *testing.B) {
	m := newRWMutexMap[int, int]()

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			if i%2 == 0 {
				m.Store(i%1000, i)
			} else {
				_, _ = m.Load(i % 1000)
			}
			i++
		}
	})
}

// The four benchmarks above all use b.RunParallel, which is sync.Map's best
// case: maximum contention on the RWMutex's single cacheline, against
// sync.Map's per-P lock-free read path. Reporting only those would be
// misleading.
//
// The pair below runs the same work from ONE goroutine, where there is no
// contention for the mutex to lose on and sync.Map's atomic loads and interface
// boxing are pure overhead. The gap between the two pairs is the actual advice.

func BenchmarkMapSequentialSyncMap(b *testing.B) {
	m := &typedSyncMap[int, int]{}
	for i := 0; i < 1000; i++ {
		m.Store(i, i*i)
	}

	i := 0
	for b.Loop() {
		_, _ = m.Load(i % 1000)
		i++
	}
}

func BenchmarkMapSequentialRWMutex(b *testing.B) {
	m := newRWMutexMap[int, int]()
	for i := 0; i < 1000; i++ {
		m.Store(i, i*i)
	}

	i := 0
	for b.Loop() {
		_, _ = m.Load(i % 1000)
		i++
	}
}

// And the write-heavy case, where sync.Map's design works against it: every
// Store on a new key takes its slow path and allocates to box the value.
func BenchmarkMapWriteHeavySyncMap(b *testing.B) {
	m := &typedSyncMap[int, int]{}

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			m.Store(i%10_000, i)
			i++
		}
	})
}

func BenchmarkMapWriteHeavyRWMutex(b *testing.B) {
	m := newRWMutexMap[int, int]()

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			m.Store(i%10_000, i)
			i++
		}
	})
}
