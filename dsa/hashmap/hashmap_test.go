package hashmap

import (
	"fmt"
	"slices"
	"testing"
)

func TestZeroValueIsUsable(t *testing.T) {
	var m HashMap[string, int] // no New

	if _, ok := m.Get("absent"); ok {
		t.Error("an empty map should not find anything")
	}

	m.Put("one", 1)

	if got, ok := m.Get("one"); !ok || got != 1 {
		t.Errorf(`Get("one") = %d, %v; want 1, true`, got, ok)
	}
}

func TestPutGetDelete(t *testing.T) {
	m := New[string, int](8)

	m.Put("a", 1)
	m.Put("b", 2)
	m.Put("c", 3)

	if m.Len() != 3 {
		t.Errorf("Len() = %d, want 3", m.Len())
	}

	m.Put("b", 20) // overwrite
	if got, _ := m.Get("b"); got != 20 {
		t.Errorf(`Get("b") = %d, want 20`, got)
	}
	if m.Len() != 3 {
		t.Errorf("an overwrite changed Len() to %d, want 3", m.Len())
	}

	if !m.Delete("b") {
		t.Error(`Delete("b") reported false`)
	}
	if m.Delete("b") {
		t.Error("deleting twice reported true the second time")
	}
	if _, ok := m.Get("b"); ok {
		t.Error("found a deleted key")
	}
	if m.Len() != 2 {
		t.Errorf("Len() = %d, want 2", m.Len())
	}
}

// TestDeleteKeepsTheProbeChain is the test that justifies tombstones, and it is
// built to fail if Delete sets a slot back to empty.
//
// A hash that returns 0 for everything puts every key in slot 0's chain, so the
// keys land in slots 0, 1 and 2. Deleting the middle one has to leave something
// behind, or the lookup for the third key stops at slot 1 and reports absent.
func TestDeleteKeepsTheProbeChain(t *testing.T) {
	allInOneChain := func(string) uint64 { return 0 }
	m := NewWithHash[string, int](8, allInOneChain)

	m.Put("first", 1)
	m.Put("second", 2)
	m.Put("third", 3)

	if m.slots[0].key != "first" || m.slots[1].key != "second" || m.slots[2].key != "third" {
		t.Fatalf("expected a chain in slots 0..2, got %q %q %q",
			m.slots[0].key, m.slots[1].key, m.slots[2].key)
	}

	m.Delete("second")

	if m.slots[1].state != deleted {
		t.Errorf("slot 1 state = %d, want deleted (%d)", m.slots[1].state, deleted)
	}
	if got, ok := m.Get("third"); !ok || got != 3 {
		t.Error("the probe stopped at the deleted slot and lost a key")
	}
}

// TestDeleteClearsKeyAndValue: the state marker is what the probe loop reads, so
// the key and value can and must be cleared, or a map of pointers pins them.
func TestDeleteClearsKeyAndValue(t *testing.T) {
	m := New[string, *int](8)
	v := 1
	m.Put("a", &v)

	idx, found := m.probe("a")
	if !found {
		t.Fatal("the key went missing")
	}

	m.Delete("a")

	if m.slots[idx].key != "" || m.slots[idx].value != nil {
		t.Error("Delete left the key or value in the slot, pinning what it points at")
	}
}

// TestTombstoneIsReused keeps the table from filling up with dead slots when the
// same key is added and removed repeatedly.
func TestTombstoneIsReused(t *testing.T) {
	m := New[string, int](8)
	capBefore := m.Cap()

	for i := range 1000 {
		m.Put("churn", i)
		m.Delete("churn")
	}

	if m.Cap() != capBefore {
		t.Errorf("Cap() grew from %d to %d over 1000 add-remove cycles", capBefore, m.Cap())
	}
	if m.dead > 1 {
		t.Errorf("accumulated %d tombstones, want at most 1", m.dead)
	}
}

func TestResizeKeepsEverything(t *testing.T) {
	m := New[int, string](0)

	const n = 500
	for i := range n {
		m.Put(i, fmt.Sprint(i))
	}

	if m.Len() != n {
		t.Fatalf("Len() = %d, want %d", m.Len(), n)
	}
	for i := range n {
		got, ok := m.Get(i)
		if !ok || got != fmt.Sprint(i) {
			t.Fatalf("Get(%d) = %q, %v after resizing", i, got, ok)
		}
	}
	if m.Load() > maxLoad {
		t.Errorf("Load() = %.2f, want at most %.2f", m.Load(), maxLoad)
	}
}

// TestResizeSizesFromCount checks the one line in resize that is a decision
// rather than mechanics: the new table is sized from the key count, not from the
// old capacity.
//
// A table churned hard fills with tombstones without gaining keys. Sizing from
// capacity would double the memory on every rebuild while the key count stood
// still; sizing from count rebuilds it small and reclaims the dead slots.
//
// resize is called directly because engineering a Put that trips the load factor
// on tombstones alone is fiddly and would test the trigger rather than the
// behaviour.
func TestResizeSizesFromCount(t *testing.T) {
	m := New[int, int](0)

	for i := range 100 {
		m.Put(i, i)
	}
	for i := range 90 {
		m.Delete(i)
	}

	capBefore := m.Cap()
	if m.dead == 0 {
		t.Fatal("expected tombstones")
	}

	m.resize()

	if m.dead != 0 {
		t.Errorf("after a rebuild dead = %d, want 0", m.dead)
	}
	if m.Len() != 10 {
		t.Errorf("Len() = %d, want 10", m.Len())
	}
	if m.Cap() >= capBefore {
		t.Errorf("Cap() went from %d to %d; a table holding 10 keys should shrink",
			capBefore, m.Cap())
	}

	// The survivors have to survive.
	for i := 90; i < 100; i++ {
		if got, ok := m.Get(i); !ok || got != i {
			t.Errorf("Get(%d) = %d, %v after the rebuild", i, got, ok)
		}
	}
}

func TestCapIsAPowerOfTwo(t *testing.T) {
	for _, capacity := range []int{0, 1, 5, 6, 100, 1000} {
		m := New[int, int](capacity)
		for i := range capacity {
			m.Put(i, i)
		}

		got := m.Cap()
		if got&(got-1) != 0 {
			t.Errorf("New(%d).Cap() = %d, not a power of two", capacity, got)
		}
		if float64(capacity)/float64(max(got, 1)) > maxLoad {
			t.Errorf("New(%d) gave %d slots, over the %.2f load factor", capacity, got, maxLoad)
		}
	}
}

func TestAllVisitsEveryKeyOnce(t *testing.T) {
	m := New[string, int](8)
	want := map[string]int{"a": 1, "b": 2, "c": 3, "d": 4}
	for k, v := range want {
		m.Put(k, v)
	}
	m.Put("gone", 9)
	m.Delete("gone")

	seen := map[string]int{}
	for k, v := range m.All() {
		if _, dup := seen[k]; dup {
			t.Errorf("All() yielded %q twice", k)
		}
		seen[k] = v
	}

	if len(seen) != len(want) {
		t.Errorf("All() yielded %d pairs, want %d", len(seen), len(want))
	}
	for k, v := range want {
		if seen[k] != v {
			t.Errorf("All() gave %q = %d, want %d", k, seen[k], v)
		}
	}
}

// TestAllStopsEarly holds up the range-over-func contract: a break in the caller
// becomes a false return from yield, and the iterator has to stop.
func TestAllStopsEarly(t *testing.T) {
	m := New[int, int](16)
	for i := range 10 {
		m.Put(i, i)
	}

	count := 0
	for range m.All() {
		count++
		if count == 3 {
			break
		}
	}

	if count != 3 {
		t.Errorf("visited %d pairs after breaking at 3", count)
	}
}

func TestKeysAndString(t *testing.T) {
	m := New[string, int](8)
	m.Put("b", 2)
	m.Put("a", 1)

	keys := m.Keys()
	slices.Sort(keys)
	if !slices.Equal(keys, []string{"a", "b"}) {
		t.Errorf("Keys() = %v", keys)
	}

	if got, want := m.String(), "{a:1 b:2}"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

// TestSeedDiffersBetweenMaps is the anti-flooding property. Two maps with the
// same keys must not agree on slot order, or an attacker who works out one map's
// layout knows every map's.
func TestSeedDiffersBetweenMaps(t *testing.T) {
	keys := make([]string, 200)
	for i := range keys {
		keys[i] = fmt.Sprint("key", i)
	}

	build := func() []string {
		m := New[string, int](len(keys))
		for i, k := range keys {
			m.Put(k, i)
		}
		return m.Keys()
	}

	// Slot order is not a promise, so this asserts only that the two orders are
	// not identical. Two independent random seeds producing the same order for
	// 200 keys is not something that happens.
	if slices.Equal(build(), build()) {
		t.Error("two maps produced identical slot order, so the seed is not doing its job")
	}
}

func TestProbeCountStaysLowWithAGoodHash(t *testing.T) {
	m := New[string, int](1000)
	for i := range 1000 {
		m.Put(fmt.Sprint("key", i), i)
	}

	perOp := float64(m.Probes()) / 1000
	if perOp > 2 {
		t.Errorf("%.2f probes per insertion, want under 2", perOp)
	}
	t.Logf("%.2f probes per insertion at load %.2f", perOp, m.Load())
}
