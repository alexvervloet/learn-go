package main

import (
	"slices"
	"testing"
)

func TestSubslicingShares(t *testing.T) {
	parent, child := subslicingShares()

	if parent[1] != 99 {
		t.Errorf("parent[1] = %d, want 99 — the subslice write should show through", parent[1])
	}
	if child[0] != 99 {
		t.Errorf("child[0] = %d, want 99", child[0])
	}
	if cap(child) != 4 {
		t.Errorf("cap(parent[1:3]) = %d, want 4 — capacity runs to the end of the array", cap(child))
	}
}

// TestAppendAliasing is the pair of tests that matter. Same code shape, one
// character of difference in the slice expression, opposite outcomes.
func TestAppendAliasing(t *testing.T) {
	t.Run("spare capacity lets append clobber the parent", func(t *testing.T) {
		parent, child := appendMayMutateTheParent()

		want := []int{10, 20, 30, 999, 50}
		if !slices.Equal(parent, want) {
			t.Errorf("parent = %v, want %v", parent, want)
		}
		if got := len(child); got != 3 {
			t.Errorf("len(child) = %d, want 3", got)
		}
	})

	t.Run("full slice expression forces a copy", func(t *testing.T) {
		parent, child := appendCannotMutateWithFullSliceExpression()

		want := []int{10, 20, 30, 40, 50}
		if !slices.Equal(parent, want) {
			t.Errorf("parent = %v, want %v — parent[1:3:3] should have protected it", parent, want)
		}
		if child[2] != 999 {
			t.Errorf("child[2] = %d, want 999", child[2])
		}
	})
}

// TestGrowthInvariants deliberately asserts properties rather than the exact
// capacity sequence. Go has changed its growth factor and its small-size
// rounding across releases, and a test pinned to today's numbers would be a
// false alarm on the next toolchain bump.
func TestGrowthInvariants(t *testing.T) {
	caps := growthReallocates(100)

	if len(caps) != 100 {
		t.Fatalf("recorded %d capacities, want 100", len(caps))
	}
	for i, c := range caps {
		length := i + 1
		if c < length {
			t.Errorf("after %d appends cap = %d, which is below len", length, c)
		}
		if i > 0 && c < caps[i-1] {
			t.Errorf("capacity shrank at append %d: %d -> %d", length, caps[i-1], c)
		}
	}

	// Amortised growth means far fewer distinct capacities than appends.
	distinct := 1
	for i := 1; i < len(caps); i++ {
		if caps[i] != caps[i-1] {
			distinct++
		}
	}
	if distinct > 20 {
		t.Errorf("%d reallocations for 100 appends — growth is not amortised", distinct)
	}
}

// TestAppendGrowingMatchesPreallocated keeps the benchmark pair honest: if the
// two functions ever stop producing identical output, the benchmark stops
// comparing the same work.
func TestAppendGrowingMatchesPreallocated(t *testing.T) {
	if got, want := appendGrowing(1000), preallocated(1000); !slices.Equal(got, want) {
		t.Error("appendGrowing and preallocated must produce identical slices")
	}
}

func TestPreallocatedNeverReallocates(t *testing.T) {
	xs := preallocated(1000)

	if len(xs) != 1000 {
		t.Errorf("len = %d, want 1000", len(xs))
	}
	if cap(xs) != 1000 {
		t.Errorf("cap = %d, want 1000 — a correctly sized make should never grow", cap(xs))
	}
}

func TestCopyDoesNotGrow(t *testing.T) {
	n1, empty, n2, sized := copyDoesNotGrow()

	if n1 != 0 {
		t.Errorf("copy into a nil slice copied %d elements, want 0", n1)
	}
	if len(empty) != 0 {
		t.Errorf("destination grew to %d, want 0 — copy never extends", len(empty))
	}
	if n2 != 3 || !slices.Equal(sized, []int{1, 2, 3}) {
		t.Errorf("copy into make([]int, 3) copied %d -> %v, want 3 -> [1 2 3]", n2, sized)
	}
}

func TestDeleteTailHandling(t *testing.T) {
	t.Run("manual delete leaves the old value in the array", func(t *testing.T) {
		got, tail := deleteLeaksTail([]string{"a", "b", "c", "d", "e"}, 1)

		if want := []string{"a", "c", "d", "e"}; !slices.Equal(got, want) {
			t.Errorf("result = %v, want %v", got, want)
		}
		if tail != "e" {
			t.Errorf("array tail = %q, want %q — the duplicate should still be referenced", tail, "e")
		}
	})

	t.Run("zeroing the tail releases the reference", func(t *testing.T) {
		got, cleared := deleteClearsTail([]string{"a", "b", "c", "d", "e"}, 1)

		if want := []string{"a", "c", "d", "e"}; !slices.Equal(got, want) {
			t.Errorf("result = %v, want %v", got, want)
		}
		if !cleared {
			t.Error("tail was not cleared")
		}
	})

	t.Run("slices.Delete matches the manual version", func(t *testing.T) {
		got := deleteWithStdlib([]string{"a", "b", "c", "d", "e"}, 1)

		if want := []string{"a", "c", "d", "e"}; !slices.Equal(got, want) {
			t.Errorf("slices.Delete = %v, want %v", got, want)
		}
	})
}

func TestMutatesElementsButNotLength(t *testing.T) {
	xs := []int{1, 2, 3}
	before := len(xs)

	mutatesElements(xs)

	if xs[0] != -1 {
		t.Errorf("xs[0] = %d, want -1 — the callee shares the backing array", xs[0])
	}
	if len(xs) != before {
		t.Errorf("len = %d, want %d — the callee's append cannot reach the caller's header", len(xs), before)
	}
}

// BenchmarkAppend quantifies the claim that make with a known capacity is
// cheaper. Run:  go test -bench . -benchmem ./02-slices-and-maps
func BenchmarkAppendGrowing(b *testing.B) {
	for b.Loop() {
		appendGrowing(1000)
	}
}

func BenchmarkAppendPreallocated(b *testing.B) {
	for b.Loop() {
		preallocated(1000)
	}
}
