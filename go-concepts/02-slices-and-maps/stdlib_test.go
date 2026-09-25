package main

import (
	"slices"
	"testing"
)

func TestSortingAndSearching(t *testing.T) {
	sorted, idx, found, maxV, minV := sortingAndSearching()

	if want := []int{1, 2, 5, 7, 9}; !slices.Equal(sorted, want) {
		t.Errorf("sorted = %v, want %v", sorted, want)
	}
	if !found || idx != 3 {
		t.Errorf("BinarySearch(7) = %d, %t; want 3, true", idx, found)
	}
	if maxV != 9 || minV != 1 {
		t.Errorf("Max/Min = %d/%d, want 9/1", maxV, minV)
	}
}

// TestSortingStructsBreaksTiesDeterministically is the reason the comparator
// has a second clause. Without it, Ana and Cy (both 7) could come out in either
// order, and the test would flake instead of failing.
func TestSortingStructs(t *testing.T) {
	users := []User{{"Cy", 7}, {"Bo", 9}, {"Ana", 7}}

	want := []User{{"Bo", 9}, {"Ana", 7}, {"Cy", 7}}
	for i := 0; i < 20; i++ {
		got := sortingStructs(users)
		if !slices.Equal(got, want) {
			t.Fatalf("pass %d: got %v, want %v", i, got, want)
		}
	}

	// Clone means the input is untouched, which SortFunc alone would not give.
	if users[0].Name != "Cy" {
		t.Error("sortingStructs must not reorder its argument")
	}
}

func TestContainsIndexReverse(t *testing.T) {
	has, at, reversed := containsIndexReverse()

	if !has {
		t.Error("Contains(\"rust\") = false, want true")
	}
	if at != 2 {
		t.Errorf("Index(\"zig\") = %d, want 2", at)
	}
	if want := []string{"zig", "rust", "go"}; !slices.Equal(reversed, want) {
		t.Errorf("Reverse = %v, want %v", reversed, want)
	}
}

// TestCompactNeedsASortFirst pins the behaviour people get wrong: Compact only
// removes ADJACENT duplicates, so on unsorted input it is not a dedupe.
func TestCompactNeedsASortFirst(t *testing.T) {
	t.Run("sorted first, fully deduped", func(t *testing.T) {
		deduped, equal := compactAndEqual()
		if want := []int{1, 2, 3}; !slices.Equal(deduped, want) {
			t.Errorf("got %v, want %v", deduped, want)
		}
		if !equal {
			t.Error("slices.Equal should report the result equal to [1 2 3]")
		}
	})

	t.Run("unsorted, duplicates survive", func(t *testing.T) {
		got := slices.Compact([]int{3, 1, 3, 1, 2})
		if want := []int{3, 1, 3, 1, 2}; !slices.Equal(got, want) {
			t.Errorf("Compact on unsorted input = %v, want %v unchanged", got, want)
		}
	})
}

func TestMapsPackage(t *testing.T) {
	keys, values, cloned, equal := mapsPackage()

	if want := []string{"a", "b", "c"}; !slices.Equal(keys, want) {
		t.Errorf("keys = %v, want %v", keys, want)
	}
	if want := []int{1, 2, 3}; !slices.Equal(values, want) {
		t.Errorf("values = %v, want %v", values, want)
	}
	if len(cloned) != 4 {
		t.Errorf("clone has %d keys after adding one, want 4", len(cloned))
	}
	if equal {
		t.Error("maps.Equal should be false once the clone diverges")
	}
}

// TestChunkAliasesTheInput is the trap slices.Chunk hides: each yielded chunk
// is a view into the original array, so a chunk with spare capacity can be
// appended into the next chunk's territory.
func TestChunkAliasesTheInput(t *testing.T) {
	src := []int{1, 2, 3, 4, 5}
	got := chunkingAndJoining(src, 2)

	want := [][]int{{1, 2}, {3, 4}, {5}}
	if len(got) != len(want) {
		t.Fatalf("got %d chunks, want %d", len(got), len(want))
	}
	for i := range want {
		if !slices.Equal(got[i], want[i]) {
			t.Errorf("chunk %d = %v, want %v", i, got[i], want[i])
		}
	}

	// chunkingAndJoining clones, so mutating a chunk must not reach src.
	got[0][0] = 99
	if src[0] != 1 {
		t.Error("chunks were not cloned — mutating one reached the input slice")
	}
}
