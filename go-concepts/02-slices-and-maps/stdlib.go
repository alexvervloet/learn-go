package main

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// The slices and maps packages
// ============================
//
// Go 1.21 promoted golang.org/x/exp/slices and .../maps into the standard
// library as `slices` and `maps`. They are generic, so one implementation works
// for every element type, and they replace most of the hand-written loops that
// used to fill Go codebases.
//
// Reach for these first. The hand-rolled versions in slices.go and maps.go are
// there to show the mechanics, not to be copied.

// sortingAndSearching covers the four calls that come up constantly.
func sortingAndSearching() (sorted []int, idx int, found bool, maxV int, minV int) {
	xs := []int{5, 2, 9, 1, 7}

	sorted = slices.Clone(xs) // Sort is in place, so clone if the input matters
	slices.Sort(sorted)

	// BinarySearch needs a sorted slice and returns the insertion point plus
	// whether the value was actually there. The two results together mean you
	// never need a second lookup.
	idx, found = slices.BinarySearch(sorted, 7)

	return sorted, idx, found, slices.Max(xs), slices.Min(xs)
}

// sortingStructs uses SortFunc with cmp.Compare, which is the generic
// three-way comparison Go 1.21 added. Returning cmp.Compare directly is
// clearer and less error-prone than writing `a.Score < b.Score`, which silently
// produces an unstable sort when scores tie.
func sortingStructs(users []User) []User {
	out := slices.Clone(users)

	slices.SortFunc(out, func(a, b User) int {
		// Descending by score, then ascending by name to break ties. Without
		// the tie-break, equal scores come out in whatever order the sort
		// happened to leave them, which differs between runs.
		if c := cmp.Compare(b.Score, a.Score); c != 0 {
			return c
		}
		return cmp.Compare(a.Name, b.Name)
	})

	return out
}

// containsIndexReverse covers the three lookup helpers that replace a
// hand-written loop each. Note that slices.Sort is NOT stable; reach for
// slices.SortStableFunc when the input already carries a meaningful order that
// equal elements should preserve.
func containsIndexReverse() (has bool, at int, reversed []string) {
	langs := []string{"go", "rust", "zig"}

	has = slices.Contains(langs, "rust")
	at = slices.Index(langs, "zig")

	reversed = slices.Clone(langs)
	slices.Reverse(reversed)

	return has, at, reversed
}

// compactAndEqual: Compact removes CONSECUTIVE duplicates, which means it is
// only a dedupe after a sort. Equal compares element by element, which is the
// comparison slices themselves do not support with ==.
func compactAndEqual() (deduped []int, equal bool) {
	xs := []int{3, 1, 3, 1, 2}

	deduped = slices.Clone(xs)
	slices.Sort(deduped)              // [1 1 2 3 3]
	deduped = slices.Compact(deduped) // [1 2 3]

	equal = slices.Equal([]int{1, 2, 3}, deduped)

	return deduped, equal
}

// mapsPackage covers the iterator-returning helpers. maps.Keys and maps.Values
// return iter.Seq values as of Go 1.23, not slices, so slices.Sorted or
// slices.Collect turns them into something indexable.
func mapsPackage() (keys []string, values []int, cloned map[string]int, equal bool) {
	m := map[string]int{"c": 3, "a": 1, "b": 2}

	// slices.Sorted consumes the iterator and returns a sorted slice in one
	// step, which is the deterministic-output pattern from maps.go compressed
	// into a single call.
	keys = slices.Sorted(maps.Keys(m))
	values = slices.Sorted(maps.Values(m))

	cloned = maps.Clone(m) // shallow: a map[string][]int would share its slices
	cloned["d"] = 4

	equal = maps.Equal(m, cloned) // false now, because of the added key

	return keys, values, cloned, equal
}

// chunkingAndJoining shows the iterator-based helpers that landed in 1.23.
func chunkingAndJoining(xs []int, size int) [][]int {
	var chunks [][]int
	for chunk := range slices.Chunk(xs, size) {
		// Chunk yields subslices of the ORIGINAL backing array, so each chunk
		// aliases xs. Clone if the chunks outlive the input or get appended to.
		chunks = append(chunks, slices.Clone(chunk))
	}
	return chunks
}

// demoStdlib prints the stdlib equivalents of the hand-written code above.
func demoStdlib() {
	sorted, idx, found, maxV, minV := sortingAndSearching()
	fmt.Printf("  slices.Sort -> %v   BinarySearch(7) -> idx %d, found %t\n", sorted, idx, found)
	fmt.Printf("  slices.Max=%d slices.Min=%d\n", maxV, minV)

	users := []User{{"Ana", 7}, {"Bo", 9}, {"Cy", 7}}
	fmt.Printf("  SortFunc by score desc, name asc -> %v\n", sortingStructs(users))

	has, at, reversed := containsIndexReverse()
	fmt.Printf("  Contains(\"rust\")=%t  Index(\"zig\")=%d  Reverse -> %v\n", has, at, reversed)

	deduped, equal := compactAndEqual()
	fmt.Printf("  Sort+Compact -> %v   Equal([1 2 3], it) = %t\n", deduped, equal)

	keys, values, cloned, mapsEqual := mapsPackage()
	fmt.Printf("  maps.Keys sorted -> %v   maps.Values sorted -> %v\n", keys, values)
	fmt.Printf("  clone + one key: len %d, maps.Equal with original = %t\n", len(cloned), mapsEqual)

	chunks := chunkingAndJoining([]int{1, 2, 3, 4, 5}, 2)
	fmt.Printf("  slices.Chunk([1..5], 2) -> %v\n", chunks)

	fmt.Printf("  strings.Join for output: %s\n", strings.Join(keys, " | "))
}
