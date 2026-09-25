package main

import (
	"slices"
	"testing"
)

func TestCommaOkDistinguishesAbsentFromZero(t *testing.T) {
	stored, storedOK, missing, missingOK := commaOkDistinguishesAbsentFromZero()

	if stored != 0 || missing != 0 {
		t.Errorf("both lookups should yield 0, got %d and %d", stored, missing)
	}
	if storedOK != 1 {
		t.Error("a key holding zero must report ok == true")
	}
	if missingOK {
		t.Error("an absent key must report ok == false")
	}
}

func TestCountWords(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want map[string]int
	}{
		{"repeated word", "go go gopher go", map[string]int{"go": 3, "gopher": 1}},
		{"empty input", "", map[string]int{}},
		{"collapses whitespace", "  a \t b\n a ", map[string]int{"a": 2, "b": 1}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := countWords(tt.in)
			if len(got) != len(tt.want) {
				t.Fatalf("got %d keys, want %d (%v)", len(got), len(tt.want), got)
			}
			for k, want := range tt.want {
				if got[k] != want {
					t.Errorf("count[%q] = %d, want %d", k, got[k], want)
				}
			}
		})
	}
}

func TestDeletingIsSafe(t *testing.T) {
	before, after := deletingIsSafe()

	if before != 3 {
		t.Errorf("started with %d keys, want 3", before)
	}
	if after != 2 {
		t.Errorf("ended with %d keys, want 2 — only the real key should go", after)
	}
}

func TestMapElementsAreNotAddressable(t *testing.T) {
	byValue, byPointer := mapElementsAreNotAddressable()

	if byValue["ana"] != 10 {
		t.Errorf("read-modify-write gave %d, want 10", byValue["ana"])
	}
	if byPointer["ana"] != 10 {
		t.Errorf("pointer value gave %d, want 10", byPointer["ana"])
	}
}

// TestMapIterationOrderIsRandomised proves the order varies between passes.
//
// Worth knowing what it does NOT prove: the runtime randomises the starting
// bucket and the offset within it, not the whole sequence. For a small map that
// lives in one bucket the observed orders are rotations of each other, so 200
// passes over 5 keys typically yields around 5 distinct orders, not the 120
// permutations a true shuffle would reach. The test logs the real count.
//
// The assertion is therefore ">= 2 distinct orders", which is the actual
// guarantee: you cannot depend on map order. Asserting a higher number would
// encode an implementation detail and flake on a bucket-layout change.
func TestMapIterationOrderIsRandomised(t *testing.T) {
	m := map[string]int{"a": 1, "b": 2, "c": 3, "d": 4, "e": 5}

	seen := make(map[string]struct{})
	for i := 0; i < 200; i++ {
		keys := iterationOrderIsRandomised(m)
		if len(keys) != len(m) {
			t.Fatalf("pass %d produced %d keys, want %d", i, len(keys), len(m))
		}
		seen[joinKeys(keys)] = struct{}{}
	}

	if len(seen) < 2 {
		t.Errorf("200 range passes produced %d distinct order(s); Go must randomise map iteration", len(seen))
	}
	t.Logf("200 passes produced %d distinct orders (rotations of one bucket walk)", len(seen))
}

func joinKeys(keys []string) string {
	out := ""
	for _, k := range keys {
		out += k
	}
	return out
}

// TestDeterministicOrderIsStable is the counterpart: the sorted version must
// produce the same answer every single time, which is why it is what goes into
// logs, golden files and API responses.
func TestDeterministicOrder(t *testing.T) {
	m := map[string]int{"e": 5, "a": 1, "c": 3, "b": 2, "d": 4}
	want := []string{"a", "b", "c", "d", "e"}

	for i := 0; i < 50; i++ {
		if got := deterministicOrder(m); !slices.Equal(got, want) {
			t.Fatalf("pass %d: got %v, want %v", i, got, want)
		}
	}
}

func TestSet(t *testing.T) {
	s := Set{}

	s.Add("go")
	s.Add("rust")
	s.Add("go") // adding twice must not double-count

	if len(s) != 2 {
		t.Errorf("len = %d, want 2 — Add must be idempotent", len(s))
	}
	if !s.Has("go") {
		t.Error("Has(\"go\") = false, want true")
	}
	if s.Has("zig") {
		t.Error("Has(\"zig\") = true, want false")
	}
	if got, want := s.Sorted(), []string{"go", "rust"}; !slices.Equal(got, want) {
		t.Errorf("Sorted() = %v, want %v", got, want)
	}
}
