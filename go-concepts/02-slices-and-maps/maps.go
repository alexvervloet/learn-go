package main

import (
	"fmt"
	"sort"
	"strings"
)

// User is a struct stored by value in a map, which is what makes the
// unaddressability rule below concrete.
type User struct {
	Name  string
	Score int
}

// commaOkDistinguishesAbsentFromZero is the only way to tell "key not present"
// from "key present, holding the zero value". Both return 0 from m[k].
func commaOkDistinguishesAbsentFromZero() (storedZero, storedZeroOK, missing int, missingOK bool) {
	m := map[string]int{
		"explicit-zero": 0,
		"nonzero":       7,
	}

	v1, ok1 := m["explicit-zero"]
	v2, ok2 := m["not-there"]

	// Both values are 0. Only ok tells them apart.
	return v1, boolToInt(ok1), v2, ok2
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// readingAMissingKeyIsSafe shows that Go maps never raise on a missing key, in
// contrast to Python's dict, which raises KeyError. The zero value comes back
// instead, which makes counting loops pleasantly short.
func countWords(text string) map[string]int {
	counts := make(map[string]int)
	for _, w := range strings.Fields(text) {
		// No need to check for presence first. counts[w] is 0 the first time.
		counts[w]++
	}
	return counts
}

// deletingIsSafeEvenIfAbsent: delete on a missing key is a no-op, and delete
// during a range is explicitly allowed by the spec. Entries deleted before they
// are reached will not be produced; entries added during a range may or may not
// be, which is why adding while ranging is a bug.
func deletingIsSafe() (before, after int) {
	m := map[string]int{"a": 1, "b": 2, "c": 3}
	before = len(m)

	delete(m, "b")
	delete(m, "never-existed") // no panic, no error, no effect

	return before, len(m)
}

// mapElementsAreNotAddressable documents a compile error, because the whole
// point is that it does not compile:
//
//	m := map[string]User{"ana": {Name: "Ana"}}
//	m["ana"].Score = 10
//	   -> cannot assign to struct field m["ana"].Score in map
//
//	&m["ana"]
//	   -> invalid operation: cannot take address of m["ana"]
//
// A map may relocate its entries when it grows, so a pointer into one would
// dangle. The three ways around it are below.
func mapElementsAreNotAddressable() (readModifyWrite, pointerValues map[string]int) {
	// 1. Read out, modify the copy, write back. Fine for small structs.
	byValue := map[string]User{"ana": {Name: "Ana", Score: 1}}
	u := byValue["ana"]
	u.Score = 10
	byValue["ana"] = u

	// 2. Store pointers. Now the map holds an address that does not move when
	//    the map grows, and in-place mutation works.
	byPointer := map[string]*User{"ana": {Name: "Ana", Score: 1}}
	byPointer["ana"].Score = 10

	// 3. Store a slice or map value. Both are already headers pointing at
	//    memory the map does not own, so mutating through them is fine.

	return map[string]int{"ana": byValue["ana"].Score},
		map[string]int{"ana": byPointer["ana"].Score}
}

// iterationOrderIsRandomised collects one full pass over the same map. Calling
// it repeatedly produces different orders: the runtime picks a random starting
// bucket and offset for every range statement.
//
// This is a feature. Go randomises so that code cannot come to depend on an
// order the spec never promised, which is what happened in other languages
// where map order was incidentally stable until a release changed it.
func iterationOrderIsRandomised(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// deterministicOrder is what to write whenever output is compared, logged, or
// serialised: collect keys, sort, then index. Never range a map into a golden
// file or a test assertion.
func deterministicOrder(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Set is Go's missing set type, spelled the idiomatic way: a map to an empty
// struct. struct{} occupies zero bytes, so a Set of a million items costs the
// same as a map with a million keys and no values at all.
//
// map[string]bool works too and reads slightly better, at 1 byte per entry and
// the risk of storing false, which means "present and false" rather than absent.
type Set map[string]struct{}

// Add inserts v. The zero-size value carries no information; membership is the
// presence of the key.
func (s Set) Add(v string) { s[v] = struct{}{} }

// Has reports membership.
func (s Set) Has(v string) bool {
	_, ok := s[v]
	return ok
}

// Sorted returns the members in a stable order, because a Set has none.
func (s Set) Sorted() []string {
	out := make([]string, 0, len(s))
	for k := range s {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// demoMaps prints each map behaviour.
func demoMaps() {
	v1, ok1, v2, ok2 := commaOkDistinguishesAbsentFromZero()
	fmt.Printf("  m[\"explicit-zero\"] = %d, ok=%d   m[\"not-there\"] = %d, ok=%t\n", v1, ok1, v2, ok2)

	counts := countWords("go go gopher go")
	fmt.Printf("  countWords: go=%d gopher=%d   (no presence check needed)\n", counts["go"], counts["gopher"])

	before, after := deletingIsSafe()
	fmt.Printf("  delete: %d keys -> %d keys, deleting a missing key is a no-op\n", before, after)

	byValue, byPointer := mapElementsAreNotAddressable()
	fmt.Printf("  read-modify-write: score %d   map of pointers: score %d\n", byValue["ana"], byPointer["ana"])

	m := map[string]int{"a": 1, "b": 2, "c": 3, "d": 4, "e": 5}
	fmt.Println("  five range passes over the same map:")
	for i := 0; i < 5; i++ {
		fmt.Printf("    %v\n", iterationOrderIsRandomised(m))
	}
	fmt.Printf("  deterministicOrder: %v\n", deterministicOrder(m))

	s := Set{}
	s.Add("go")
	s.Add("rust")
	s.Add("go") // idempotent
	fmt.Printf("  Set: %v, Has(\"go\")=%t, Has(\"zig\")=%t\n", s.Sorted(), s.Has("go"), s.Has("zig"))
}
