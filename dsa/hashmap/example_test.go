package hashmap_test

import (
	"fmt"
	"slices"

	"github.com/alexvervloet/learn-go/dsa/hashmap"
)

func ExampleHashMap() {
	var m hashmap.HashMap[string, int] // the zero value is ready to use

	m.Put("ada", 36)
	m.Put("bo", 41)
	m.Put("ada", 37) // an overwrite, not a second entry

	fmt.Println(&m)
	fmt.Println("len:", m.Len())

	age, ok := m.Get("bo")
	fmt.Println("bo:", age, ok)

	_, ok = m.Get("cy")
	fmt.Println("cy:", ok)

	// Output:
	// {ada:37 bo:41}
	// len: 2
	// bo: 41 true
	// cy: false
}

// Delete leaves a tombstone rather than an empty slot, which the caller never
// sees but which is what keeps the rest of the probe chain reachable.
func ExampleHashMap_Delete() {
	m := hashmap.New[string, int](8)
	m.Put("a", 1)
	m.Put("b", 2)

	fmt.Println(m.Delete("a"))
	fmt.Println(m.Delete("a")) // already gone
	fmt.Println(m)

	// Output:
	// true
	// false
	// {b:2}
}

// The table grows when it reaches a load factor of 0.7, and the capacity is
// always a power of two so the modulo can be a bitwise AND.
func ExampleHashMap_Cap() {
	m := hashmap.New[int, int](0)
	fmt.Printf("%d keys: %d slots\n", m.Len(), m.Cap())

	for i := range 100 {
		m.Put(i, i)
	}
	fmt.Printf("%d keys: %d slots, load %.2f\n", m.Len(), m.Cap(), m.Load())

	// Output:
	// 0 keys: 0 slots
	// 100 keys: 256 slots, load 0.39
}

// All yields the pairs in slot order, which is arbitrary. Sorting is the caller's
// job, and the fact that this example has to sort is the demonstration.
func ExampleHashMap_All() {
	m := hashmap.New[string, int](8)
	for i, name := range []string{"cy", "ada", "bo"} {
		m.Put(name, i)
	}

	var lines []string
	for name, i := range m.All() {
		lines = append(lines, fmt.Sprintf("%s=%d", name, i))
	}
	slices.Sort(lines)

	fmt.Println(lines)

	// Output:
	// [ada=1 bo=2 cy=0]
}

// SumBytes is the hash everybody writes first. Every permutation of the same
// bytes gives the same result, which is all an attacker needs.
func ExampleSumBytes() {
	fmt.Println(hashmap.SumBytes("abc") == hashmap.SumBytes("cba"))
	fmt.Println(hashmap.FNV1a("abc") == hashmap.FNV1a("cba"))

	// Output:
	// true
	// false
}

// Measure compares a hash function against a uniformly random one. A ratio near
// 1.0 means the keys are spread as well as chance would spread them.
func ExampleMeasure() {
	keys := make([]string, 4000)
	for i := range keys {
		keys[i] = fmt.Sprintf("user_%d", i)
	}

	bad := hashmap.Measure(hashmap.SumBytes, keys, 4096)
	good := hashmap.Measure(hashmap.FNV1a, keys, 4096)

	fmt.Printf("SumBytes: %d slots used, worst slot %d keys\n", bad.Used, bad.Worst)
	fmt.Printf("FNV1a:    %d slots used, worst slot %d keys\n", good.Used, good.Worst)

	// Output:
	// SumBytes: 85 slots used, worst slot 223 keys
	// FNV1a:    2502 slots used, worst slot 5 keys
}
