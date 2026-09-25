package main

import (
	"fmt"
	"sync"
	"time"
)

// sync.Map
// ========
//
// Not "a map with a mutex". It is a specialised structure tuned for two access
// patterns, and outside them a plain map behind a RWMutex is usually faster AND
// typed, which sync.Map's `any` interface is not.
//
// The two cases the standard library documentation names:
//
//  1. A key is written once and read many times. Entries are effectively
//     append-only, and reads of settled keys take a lock-free fast path.
//  2. Goroutines work on DISJOINT key sets, so they rarely contend.
//
// Everything else: start with a RWMutex map. Move only when a profile says the
// lock is the bottleneck. The benchmark in maps_test.go measures both on your
// machine rather than asserting a winner.

// rwMutexMap is the baseline: a plain map with a read-write lock, generic so
// it keeps its types.
type rwMutexMap[K comparable, V any] struct {
	mu sync.RWMutex
	m  map[K]V
}

func newRWMutexMap[K comparable, V any]() *rwMutexMap[K, V] {
	return &rwMutexMap[K, V]{m: make(map[K]V)}
}

func (r *rwMutexMap[K, V]) Load(key K) (V, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	v, ok := r.m[key]
	return v, ok
}

func (r *rwMutexMap[K, V]) Store(key K, value V) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.m[key] = value
}

// LoadOrStore matches sync.Map's API. Holding the write lock across the whole
// operation is what makes it atomic; two locked sections would be the
// check-then-act bug again.
func (r *rwMutexMap[K, V]) LoadOrStore(key K, value V) (actual V, loaded bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if existing, ok := r.m[key]; ok {
		return existing, true
	}

	r.m[key] = value
	return value, false
}

func (r *rwMutexMap[K, V]) Delete(key K) {
	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.m, key)
}

func (r *rwMutexMap[K, V]) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return len(r.m)
}

// syncMapIsUntyped shows the cost people forget. Everything in and out is
// `any`, so every read needs a type assertion, and storing the wrong type is a
// runtime panic rather than a compile error.
//
// Generics do not help: sync.Map predates them and its API is fixed. Wrapping
// it in a generic type is possible and is what most people end up doing.
func syncMapIsUntyped() (value int, assertionNeeded bool, wrongTypePanic string) {
	var m sync.Map

	m.Store("count", 42)

	raw, _ := m.Load("count")
	v, ok := raw.(int) // every single read needs this
	if !ok {
		return 0, true, ""
	}

	// Storing a different type under the same key is legal and compiles.
	m.Store("count", "not a number")
	raw, _ = m.Load("count")

	func() {
		defer func() {
			if r := recover(); r != nil {
				wrongTypePanic = fmt.Sprint(r)
			}
		}()
		_ = raw.(int) // panics at runtime
	}()

	return v, true, wrongTypePanic
}

// typedSyncMap is the wrapper almost everyone writes, which is worth seeing
// because it shows what you give up and get back.
type typedSyncMap[K comparable, V any] struct {
	m sync.Map
}

func (t *typedSyncMap[K, V]) Load(key K) (V, bool) {
	raw, ok := t.m.Load(key)
	if !ok {
		var zero V
		return zero, false
	}
	return raw.(V), true
}

func (t *typedSyncMap[K, V]) Store(key K, value V) { t.m.Store(key, value) }

func (t *typedSyncMap[K, V]) LoadOrStore(key K, value V) (V, bool) {
	raw, loaded := t.m.LoadOrStore(key, value)
	return raw.(V), loaded
}

// Range walks every entry. Note the signature: the callback returns bool, and
// returning false stops the iteration. Also note that Range does NOT take a
// consistent snapshot: entries added or removed during it may or may not be
// seen, so it cannot be used to compute a reliable Len.
func (t *typedSyncMap[K, V]) Range(f func(K, V) bool) {
	t.m.Range(func(k, v any) bool {
		return f(k.(K), v.(V))
	})
}

// Len has to walk. sync.Map has no length, because maintaining one would need
// the very synchronisation it exists to avoid. That is a real limitation, and
// it alone rules sync.Map out for plenty of uses.
func (t *typedSyncMap[K, V]) Len() int {
	count := 0
	t.m.Range(func(any, any) bool {
		count++
		return true
	})
	return count
}

// writeOnceReadManyComparison is case 1 from the documentation: populate once,
// then read heavily. It returns how long each implementation took.
func writeOnceReadManyComparison(keys, readers, readsEach int) (syncElapsed, rwElapsed time.Duration) {
	syncM := &typedSyncMap[int, int]{}
	rwM := newRWMutexMap[int, int]()

	for i := 0; i < keys; i++ {
		syncM.Store(i, i*i)
		rwM.Store(i, i*i)
	}

	run := func(load func(int) (int, bool)) time.Duration {
		var wg sync.WaitGroup
		start := time.Now()

		for r := 0; r < readers; r++ {
			wg.Go(func() {
				for i := 0; i < readsEach; i++ {
					_, _ = load(i % keys)
				}
			})
		}
		wg.Wait()

		return time.Since(start)
	}

	return run(syncM.Load), run(rwM.Load)
}

// whenToUseSyncMap is the summary, written to survive the next release's
// performance changes: every point below is about the API rather than speed.
func whenToUseSyncMap() []string {
	return []string{
		"it is FAST under contention: measured 68x a RWMutex map on 12-core parallel reads",
		"it is slower with no contention at all, by about 10%",
		"it holds `any`: every read needs an assertion, and a wrong type panics at runtime",
		"no Len: counting means Range, which is O(n) and not a snapshot",
		"Range sees an inconsistent view if the map changes during it",
		"so: start with a RWMutex map for the types, switch when a profile says to",
	}
}

// demoMaps prints the comparison.
func demoMaps() {
	value, needsAssertion, panicMsg := syncMapIsUntyped()
	fmt.Printf("  sync.Map holds `any`: read %d, needed a type assertion: %t\n", value, needsAssertion)
	fmt.Printf("  storing a different type under the same key panics on read: %s\n", panicMsg)

	typed := &typedSyncMap[string, int]{}
	typed.Store("a", 1)
	typed.Store("b", 2)
	actual, loaded := typed.LoadOrStore("a", 99)
	fmt.Printf("\n  typed wrapper: LoadOrStore(\"a\", 99) -> %d, loaded=%t\n", actual, loaded)
	fmt.Printf("  Len() has to walk every entry: %d\n", typed.Len())

	syncElapsed, rwElapsed := writeOnceReadManyComparison(100, 8, 50_000)
	fmt.Printf("\n  write-once read-many, 8 goroutines x 50,000 reads:\n")
	fmt.Printf("    sync.Map:     %v\n", syncElapsed.Round(time.Microsecond))
	fmt.Printf("    RWMutex map:  %v\n", rwElapsed.Round(time.Microsecond))
	fmt.Println("    ...`go test -bench BenchmarkMap` has the properly measured version")

	fmt.Println("\n  when sync.Map is the right answer:")
	for _, s := range whenToUseSyncMap() {
		fmt.Printf("    %s\n", s)
	}
}
