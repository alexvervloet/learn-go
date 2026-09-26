// Package hashmap implements a hash table with open addressing and linear
// probing, the same design as Go's own map but written out so every decision is
// visible.
//
// Use the builtin map. This exists so that when a profile shows a map is the
// bottleneck, or an interviewer asks what happens on a collision, the answer is
// something you have written rather than something you have read.
//
// The three decisions that make a hash table:
//
//	where a key goes          hash(key) mod capacity
//	what happens on collision open addressing, linear probing
//	when to grow              load factor 0.7, doubling
package hashmap

import (
	"fmt"
	"hash/maphash"
	"iter"
	"slices"
	"strings"
)

// slotState distinguishes a never-used slot from a deleted one, which is the
// whole difficulty of deletion in an open-addressed table. See Delete.
type slotState uint8

const (
	empty slotState = iota // never held a key: a probe may stop here
	occupied
	deleted // a tombstone: a probe must continue past it
)

type slot[K comparable, V any] struct {
	key   K
	value V
	state slotState
}

// maxLoad is the fraction of slots that may be in use, counting tombstones,
// before the table grows.
//
// 0.7 is the usual compromise. Linear probing degrades sharply as a table fills,
// because the expected probe count for an unsuccessful lookup is roughly
// (1 + 1/(1-load)^2)/2: at 0.5 that is about 2.5 probes, at 0.7 about 6, at 0.9
// about 50. Go's own map grows at 0.8125 (13/16), with buckets of 8 rather than
// single slots, which changes the arithmetic.
const maxLoad = 0.7

// HashMap maps keys to values. The zero value is an empty map ready to use.
type HashMap[K comparable, V any] struct {
	slots []slot[K, V]
	count int // occupied slots
	dead  int // tombstones

	// seed makes this map's hash function different from every other map's,
	// which is what stops an attacker sending keys chosen to collide. See
	// hash.go for what that attack costs.
	seed maphash.Seed

	// hash overrides the default when set, which only the teaching code in
	// hash.go does. A real table would not offer this.
	hash func(K) uint64

	probes int // cumulative probe count, for the clustering measurements
}

// New returns a map with room for at least capacity keys before the first
// resize.
func New[K comparable, V any](capacity int) *HashMap[K, V] {
	m := &HashMap[K, V]{seed: maphash.MakeSeed()}
	if capacity > 0 {
		m.slots = make([]slot[K, V], slotsFor(capacity))
	}
	return m
}

// NewWithHash returns a map using the given hash function instead of the
// default. Only the demonstrations in hash.go use it: a fixed, caller-supplied
// hash is exactly the thing a real table must not have.
func NewWithHash[K comparable, V any](capacity int, hash func(K) uint64) *HashMap[K, V] {
	m := New[K, V](capacity)
	m.hash = hash
	return m
}

// slotsFor returns the smallest power of two that holds n keys under maxLoad.
//
// A power of two so the modulo becomes a bitwise AND, which is what makes the
// probe loop cheap. The cost of that choice is that only the low bits of the
// hash are used, so a hash with poor low bits clusters badly where a prime
// modulus would have hidden it. hash.go measures exactly that.
func slotsFor(n int) int {
	needed := int(float64(n)/maxLoad) + 1

	size := 8
	for size < needed {
		size *= 2
	}
	return size
}

// hashOf returns the slot-independent hash of a key.
func (m *HashMap[K, V]) hashOf(key K) uint64 {
	if m.hash != nil {
		return m.hash(key)
	}

	// maphash.Comparable, new in Go 1.24, hashes ANY comparable type using the
	// runtime's own hash functions. Before it, a generic hash table in Go had
	// to make the caller supply a hash function, or restrict itself to strings
	// and integers, or reach for reflection.
	return maphash.Comparable(m.seed, key)
}

// Len reports the number of keys.
func (m *HashMap[K, V]) Len() int { return m.count }

// Cap reports the number of slots, which is always a power of two.
func (m *HashMap[K, V]) Cap() int { return len(m.slots) }

// Load reports the fraction of slots in use, counting tombstones.
func (m *HashMap[K, V]) Load() float64 {
	if len(m.slots) == 0 {
		return 0
	}
	return float64(m.count+m.dead) / float64(len(m.slots))
}

// Probes reports how many slots have been examined across every operation, which
// is the number that shows a bad hash function for what it is. A perfect hash
// costs one probe per operation.
func (m *HashMap[K, V]) Probes() int { return m.probes }

// probe finds the slot for key.
//
// It returns the index where key lives when found is true, and otherwise the
// index where it should be inserted. The insertion index prefers the first
// tombstone seen, so deleting and re-adding a key reuses its slot instead of
// lengthening the chain.
func (m *HashMap[K, V]) probe(key K) (idx int, found bool) {
	mask := len(m.slots) - 1 // valid because the length is a power of two
	start := int(m.hashOf(key)) & mask

	firstTombstone := -1

	for i := range m.slots {
		idx = (start + i) & mask
		m.probes++

		switch m.slots[idx].state {
		case empty:
			// A never-used slot ends the chain: the key is not in the table.
			if firstTombstone >= 0 {
				return firstTombstone, false
			}
			return idx, false

		case deleted:
			// Keep going. The key may have been inserted past this slot while
			// it was still occupied.
			if firstTombstone < 0 {
				firstTombstone = idx
			}

		case occupied:
			if m.slots[idx].key == key {
				return idx, true
			}
		}
	}

	// Every slot is occupied or a tombstone and the key is absent. maxLoad
	// makes this unreachable through Put; it is here so the loop has an answer.
	return firstTombstone, false
}

// Get returns the value for key and reports whether it was present.
func (m *HashMap[K, V]) Get(key K) (V, bool) {
	if m.count == 0 {
		var zero V
		return zero, false
	}

	idx, found := m.probe(key)
	if !found {
		var zero V
		return zero, false
	}
	return m.slots[idx].value, true
}

// Put stores value under key, replacing any previous value.
func (m *HashMap[K, V]) Put(key K, value V) {
	if len(m.slots) == 0 {
		// The zero value is usable, so allocation is deferred to the first Put.
		// The seed has to be set here too: maphash panics on an uninitialised
		// Seed rather than treating zero as a valid one.
		m.slots = make([]slot[K, V], slotsFor(1))
		if m.seed == (maphash.Seed{}) {
			m.seed = maphash.MakeSeed()
		}
	}

	idx, found := m.probe(key)
	if found {
		m.slots[idx].value = value // overwrite, no growth
		return
	}

	if m.slots[idx].state == deleted {
		m.dead-- // reusing a tombstone retires it
	}

	m.slots[idx] = slot[K, V]{key: key, value: value, state: occupied}
	m.count++

	if m.Load() > maxLoad {
		m.resize()
	}
}

// Delete removes key and reports whether it was present.
//
// The slot becomes a tombstone rather than going back to empty, and that is not
// an optimisation: it is required for correctness. Consider three keys that hash
// to slot 5, landing in 5, 6 and 7. Delete the one in slot 6 and set it empty,
// and the lookup for the key in slot 7 probes 5, then 6, sees an empty slot,
// concludes the key is absent, and the table has silently lost a value.
func (m *HashMap[K, V]) Delete(key K) bool {
	if m.count == 0 {
		return false
	}

	idx, found := m.probe(key)
	if !found {
		return false
	}

	// Clear the key and value so a map of pointers does not pin them, but keep
	// the state marker. Only state carries meaning to the probe loop.
	var zeroK K
	var zeroV V
	m.slots[idx] = slot[K, V]{key: zeroK, value: zeroV, state: deleted}

	m.count--
	m.dead++

	return true
}

// resize rebuilds the table, which is the only way to reclaim tombstones.
//
// The new size comes from the key count, not from the current capacity, and that
// one choice covers both cases. Resizing because the table is full of keys gives
// slotsFor(count), which is the next power of two up, so the table doubles.
// Resizing a table full of TOMBSTONES gives a small number, so it rebuilds
// small instead of doubling memory for keys it does not have.
//
// slotsFor(count*2) was the first version, and it grew by 4x rather than 2x: the
// doubling is already in slotsFor's division by maxLoad, so multiplying first
// applied it twice. A hundred keys landed in 512 slots at a load of 0.20.
func (m *HashMap[K, V]) resize() {
	old := m.slots

	m.slots = make([]slot[K, V], slotsFor(m.count))
	m.count = 0
	m.dead = 0

	// Every key has to be re-probed. Its slot depends on the capacity, so
	// nothing can be copied across, which is why a resize is O(n) and why the
	// load factor exists to make it rare.
	for _, s := range old {
		if s.state != occupied {
			continue
		}
		idx, _ := m.probe(s.key)
		m.slots[idx] = slot[K, V]{key: s.key, value: s.value, state: occupied}
		m.count++
	}
}

// All returns an iterator over the key-value pairs.
//
// The order is slot order, which is arbitrary and changes with the capacity and
// with this map's seed. Two maps holding the same keys will iterate them
// differently. Sort the keys if you need a stable order; the builtin map goes
// further and randomises the starting point on every range, specifically to stop
// code depending on an order that was never promised.
func (m *HashMap[K, V]) All() iter.Seq2[K, V] {
	return func(yield func(K, V) bool) {
		for _, s := range m.slots {
			if s.state != occupied {
				continue
			}
			if !yield(s.key, s.value) {
				return
			}
		}
	}
}

// Keys returns the keys in slot order.
func (m *HashMap[K, V]) Keys() []K {
	out := make([]K, 0, m.count)
	for k := range m.All() {
		out = append(out, k)
	}
	return out
}

// String renders the map for the examples, with keys sorted so the output is
// stable. The sort is the tell: without it there is nothing to print
// deterministically.
func (m *HashMap[K, V]) String() string {
	pairs := make([]string, 0, m.count)
	for k, v := range m.All() {
		pairs = append(pairs, fmt.Sprintf("%v:%v", k, v))
	}
	slices.Sort(pairs)

	return "{" + strings.Join(pairs, " ") + "}"
}
