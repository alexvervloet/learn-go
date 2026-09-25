package main

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"sync"
)

// Generic containers
// ==================
//
// The clearest win for type parameters. Before 1.18 every project had a Set of
// strings, a Set of ints, and a Set of whatever else, all copy-pasted.

// Set is a set with no duplicates, backed by a map to an empty struct.
// struct{} occupies zero bytes, so membership costs only the key.
//
// comparable rather than any, because a map key must support ==.
type Set[T comparable] map[T]struct{}

// NewSet builds a set from values.
func NewSet[T comparable](values ...T) Set[T] {
	s := make(Set[T], len(values))
	for _, v := range values {
		s.Add(v)
	}
	return s
}

// Add inserts. Idempotent.
func (s Set[T]) Add(v T) { s[v] = struct{}{} }

// Remove deletes. A no-op when absent.
func (s Set[T]) Remove(v T) { delete(s, v) }

// Has reports membership.
func (s Set[T]) Has(v T) bool {
	_, ok := s[v]
	return ok
}

// Len reports the size.
func (s Set[T]) Len() int { return len(s) }

// Union returns the values in either set.
func (s Set[T]) Union(other Set[T]) Set[T] {
	out := make(Set[T], len(s)+len(other))
	for v := range s {
		out.Add(v)
	}
	for v := range other {
		out.Add(v)
	}
	return out
}

// Intersection returns the values in both. It iterates the SMALLER set, which
// makes the cost proportional to the smaller side rather than to s.
func (s Set[T]) Intersection(other Set[T]) Set[T] {
	small, large := s, other
	if len(other) < len(s) {
		small, large = other, s
	}

	out := make(Set[T], len(small))
	for v := range small {
		if large.Has(v) {
			out.Add(v)
		}
	}
	return out
}

// Difference returns the values in s but not in other.
func (s Set[T]) Difference(other Set[T]) Set[T] {
	out := make(Set[T], len(s))
	for v := range s {
		if !other.Has(v) {
			out.Add(v)
		}
	}
	return out
}

// Values returns the members in an arbitrary order, because a set has none.
func (s Set[T]) Values() []T {
	out := make([]T, 0, len(s))
	for v := range s {
		out = append(out, v)
	}
	return out
}

// SortedValues is a plain function rather than a method, because it needs
// cmp.Ordered and Set only requires comparable. A method cannot add a
// constraint the type does not have.
func SortedValues[T cmp.Ordered](s Set[T]) []T {
	out := s.Values()
	slices.Sort(out)
	return out
}

// Result is the "either a value or an error" type. Go does not need it, because
// multiple returns already do the job, and it appears here because people
// arriving from Rust reach for it and should see what it costs.
//
// The honest assessment: `(T, error)` is more idiomatic, composes with
// errors.Is and errors.As, and does not need a method call to inspect. Result
// is worth having only where you must carry outcomes in a slice or a channel.
type Result[T any] struct {
	value T
	err   error
}

// Ok wraps a success.
func Ok[T any](v T) Result[T] { return Result[T]{value: v} }

// Err wraps a failure.
func Err[T any](err error) Result[T] { return Result[T]{err: err} }

// Unwrap returns the pair, which is where it turns back into ordinary Go.
func (r Result[T]) Unwrap() (T, error) { return r.value, r.err }

// IsOk reports success.
func (r Result[T]) IsOk() bool { return r.err == nil }

// ValueOr returns the value, or a fallback on failure.
func (r Result[T]) ValueOr(fallback T) T {
	if r.err != nil {
		return fallback
	}
	return r.value
}

// MapResult transforms a success and passes a failure through. It is a
// function rather than a method for the usual reason: U is new.
func MapResult[T, U any](r Result[T], f func(T) U) Result[U] {
	if r.err != nil {
		return Err[U](r.err)
	}
	return Ok(f(r.value))
}

// CollectResults turns a slice of Results into a slice plus the first error,
// which is the shape that actually gets used: gather concurrently, then fold.
func CollectResults[T any](results []Result[T]) ([]T, error) {
	values := make([]T, 0, len(results))
	var errs []error

	for _, r := range results {
		if r.err != nil {
			errs = append(errs, r.err)
			continue
		}
		values = append(values, r.value)
	}

	return values, errors.Join(errs...)
}

// Optional distinguishes "absent" from "the zero value", which matters for a
// JSON field that may be missing, null, or genuinely 0.
//
// Go's usual answer is a pointer or a comma-ok pair. Optional is worth it when
// the absence must survive being stored in a struct field.
type Optional[T any] struct {
	value   T
	present bool
}

// Some wraps a present value.
func Some[T any](v T) Optional[T] { return Optional[T]{value: v, present: true} }

// None returns an absent value.
func None[T any]() Optional[T] { return Optional[T]{} }

// Get returns the value and whether it was present.
func (o Optional[T]) Get() (T, bool) { return o.value, o.present }

// OrElse returns the value or a fallback.
func (o Optional[T]) OrElse(fallback T) T {
	if !o.present {
		return fallback
	}
	return o.value
}

// String makes Optional printable.
func (o Optional[T]) String() string {
	if !o.present {
		return "None"
	}
	return fmt.Sprintf("Some(%v)", o.value)
}

// Cache is a generic concurrent map, which is the combination lesson 09 wanted
// and could not express before 1.18: a RWMutex map that keeps its types.
type Cache[K comparable, V any] struct {
	mu    sync.RWMutex
	items map[K]V
}

// NewCache returns a ready cache.
func NewCache[K comparable, V any]() *Cache[K, V] {
	return &Cache[K, V]{items: make(map[K]V)}
}

// Get reads under the read lock.
func (c *Cache[K, V]) Get(key K) (V, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	v, ok := c.items[key]
	return v, ok
}

// Set writes under the write lock.
func (c *Cache[K, V]) Set(key K, value V) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.items[key] = value
}

// GetOrCompute is the double-checked pattern from lesson 09, now generic and
// reusable instead of rewritten per type.
func (c *Cache[K, V]) GetOrCompute(key K, compute func() V) (V, bool) {
	c.mu.RLock()
	if v, ok := c.items[key]; ok {
		c.mu.RUnlock()
		return v, false
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()

	if v, ok := c.items[key]; ok { // the recheck, still necessary
		return v, false
	}

	v := compute()
	c.items[key] = v
	return v, true
}

// Len reports the entry count.
func (c *Cache[K, V]) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return len(c.items)
}

// demoContainers prints each container.
func demoContainers() {
	a := NewSet(1, 2, 3, 4)
	b := NewSet(3, 4, 5, 6)

	fmt.Printf("  Set[int] a=%v b=%v\n", SortedValues(a), SortedValues(b))
	fmt.Printf("    union:        %v\n", SortedValues(a.Union(b)))
	fmt.Printf("    intersection: %v\n", SortedValues(a.Intersection(b)))
	fmt.Printf("    a minus b:    %v\n", SortedValues(a.Difference(b)))

	words := NewSet("go", "rust", "go")
	fmt.Printf("  Set[string]: %v (len %d, duplicate collapsed)\n", SortedValues(words), words.Len())

	ok := Ok(42)
	bad := Err[int](errors.New("not found"))
	fmt.Printf("\n  Result[int]: Ok -> %v, Err -> %v\n", ok.ValueOr(-1), bad.ValueOr(-1))

	doubled := MapResult(ok, func(v int) string { return fmt.Sprintf("value-%d", v) })
	fmt.Printf("  MapResult(Ok(42), format) -> %v\n", doubled.ValueOr("none"))

	values, err := CollectResults([]Result[int]{Ok(1), bad, Ok(3)})
	fmt.Printf("  CollectResults: values=%v err=%v\n", values, err)

	fmt.Printf("\n  Optional[int]: %v and %v\n", Some(0), None[int]())
	fmt.Printf("    Some(0).OrElse(-1) = %d   <- present and zero, not absent\n", Some(0).OrElse(-1))
	fmt.Printf("    None[int]().OrElse(-1) = %d\n", None[int]().OrElse(-1))

	cache := NewCache[string, int]()
	computed := 0
	for i := 0; i < 3; i++ {
		cache.GetOrCompute("key", func() int { computed++; return 99 })
	}
	v, _ := cache.Get("key")
	fmt.Printf("\n  Cache[string,int]: value=%d, computed %d time(s), len=%d\n", v, computed, cache.Len())
}
