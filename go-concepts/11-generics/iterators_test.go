package main

import (
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestCount(t *testing.T) {
	tests := []struct {
		n    int
		want []int
	}{
		{0, nil},
		{1, []int{0}},
		{5, []int{0, 1, 2, 3, 4}},
		{-1, nil},
	}

	for _, tt := range tests {
		got := collectSeq(Count(tt.n))
		if !slices.Equal(got, tt.want) {
			t.Errorf("Count(%d) = %v, want %v", tt.n, got, tt.want)
		}
		// slices.Collect must agree with the hand-written drain.
		if stdlib := slices.Collect(Count(tt.n)); !slices.Equal(got, stdlib) {
			t.Errorf("Count(%d): collectSeq gave %v, slices.Collect gave %v", tt.n, got, stdlib)
		}
	}
}

func TestFilterAndMap(t *testing.T) {
	t.Run("filter", func(t *testing.T) {
		got := collectSeq(Filter(Count(10), func(v int) bool { return v%2 == 0 }))
		if want := []int{0, 2, 4, 6, 8}; !slices.Equal(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("filter that matches nothing", func(t *testing.T) {
		got := collectSeq(Filter(Count(10), func(int) bool { return false }))
		if len(got) != 0 {
			t.Errorf("got %v, want empty", got)
		}
	})

	t.Run("map", func(t *testing.T) {
		got := collectSeq(MapSeq(Count(4), func(v int) string {
			return strings.Repeat("x", v)
		}))
		if want := []string{"", "x", "xx", "xxx"}; !slices.Equal(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})
}

// TestTakeStopsAnInfiniteSequence is the property that makes the yield
// contract worth enforcing: without it this test would never return.
func TestTakeStopsAnInfiniteSequence(t *testing.T) {
	got := collectSeq(Take(Naturals(), 5))

	if want := []int{1, 2, 3, 4, 5}; !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestTakeEdgeCases(t *testing.T) {
	tests := []struct {
		name string
		n    int
		want []int
	}{
		{"zero takes nothing", 0, nil},
		{"negative takes nothing", -1, nil},
		{"more than available", 100, []int{0, 1, 2}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := collectSeq(Take(Count(3), tt.n))
			if !slices.Equal(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

// TestBreakPropagatesThroughTheChain: a break in the consumer must stop every
// stage, not just the outermost one.
func TestBreakPropagatesThroughTheChain(t *testing.T) {
	produced := 0

	counting := func(yield func(int) bool) {
		for i := 0; ; i++ {
			produced++
			if !yield(i) {
				return
			}
		}
	}

	consumed := 0
	for range Filter(MapSeq(counting, func(v int) int { return v * 2 }),
		func(v int) bool { return true }) {
		consumed++
		if consumed == 3 {
			break
		}
	}

	if consumed != 3 {
		t.Errorf("consumed %d, want 3", consumed)
	}
	// The producer must have stopped almost immediately, not run away.
	if produced > 10 {
		t.Errorf("producer made %d values for 3 consumed — break did not propagate", produced)
	}
}

// TestIgnoringYieldPanics is the enforcement, asserted. A producer that
// continues after yield returns false is a runtime error, not a silent waste.
func TestIgnoringYieldPanics(t *testing.T) {
	work := 0

	msg := capturePanic(func() {
		for range CountBroken(100, func(int) { work++ }) {
			break
		}
	})

	if msg == "" {
		t.Fatal("expected a panic from a producer that ignores yield's result")
	}
	if !strings.Contains(msg, "range function continued iteration") {
		t.Errorf("panic = %q, want it to name the contract violation", msg)
	}
	// It stops almost at once, rather than computing all 100.
	if work > 10 {
		t.Errorf("computed %d elements before panicking, want a handful", work)
	}
}

func TestEnumerate(t *testing.T) {
	var indexes []int
	var values []string

	for i, v := range Enumerate(MapSeq(Count(3), func(v int) string {
		return strings.Repeat("a", v+1)
	})) {
		indexes = append(indexes, i)
		values = append(values, v)
	}

	if want := []int{0, 1, 2}; !slices.Equal(indexes, want) {
		t.Errorf("indexes = %v, want %v", indexes, want)
	}
	if want := []string{"a", "aa", "aaa"}; !slices.Equal(values, want) {
		t.Errorf("values = %v, want %v", values, want)
	}
}

func TestEnumerateStopsEarly(t *testing.T) {
	count := 0
	for i := range Enumerate(Naturals()) {
		count++
		if i == 2 {
			break
		}
	}

	if count != 3 {
		t.Errorf("consumed %d, want 3", count)
	}
}

func TestSortedByValue(t *testing.T) {
	scores := map[string]int{"ana": 92, "bo": 78, "cy": 95, "di": 78}

	var names []string
	var values []int
	for name, score := range SortedByValue(scores) {
		names = append(names, name)
		values = append(values, score)
	}

	// Descending by value.
	if !slices.IsSortedFunc(values, func(a, b int) int { return b - a }) {
		t.Errorf("values = %v, want descending", values)
	}
	if names[0] != "cy" {
		t.Errorf("highest scorer = %q, want cy", names[0])
	}
	if len(names) != 4 {
		t.Errorf("got %d entries, want 4", len(names))
	}
}

func TestSortedByValueOnAnEmptyMap(t *testing.T) {
	count := 0
	for range SortedByValue(map[string]int{}) {
		count++
	}
	if count != 0 {
		t.Errorf("got %d entries from an empty map", count)
	}
}

func TestZip(t *testing.T) {
	t.Run("pairs up two sequences", func(t *testing.T) {
		names := slices.Values([]string{"go", "rust", "zig"})

		var got []string
		for name, n := range Zip(names, Naturals()) {
			got = append(got, name)
			if n > 10 {
				t.Fatal("Naturals should have been stopped by the shorter sequence")
			}
		}

		if want := []string{"go", "rust", "zig"}; !slices.Equal(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("stops at the shorter sequence", func(t *testing.T) {
		count := 0
		for range Zip(Count(3), Count(100)) {
			count++
		}
		if count != 3 {
			t.Errorf("got %d pairs, want 3", count)
		}
	})

	t.Run("an empty sequence yields nothing", func(t *testing.T) {
		count := 0
		for range Zip(Count(0), Naturals()) {
			count++
		}
		if count != 0 {
			t.Errorf("got %d pairs, want 0", count)
		}
	})
}

// TestZipDoesNotLeakItsPullGoroutines: iter.Pull runs the producer on a
// goroutine, and the stop function must be called. Zip defers both.
func TestZipStopsCleanly(t *testing.T) {
	if testing.Short() {
		t.Skip("goroutine check")
	}

	before := countGoroutinesForTest()

	for range 100 {
		for range Zip(Count(3), Naturals()) {
			break // early exit, so the pull goroutines must be stopped by the defer
		}
	}

	after := countGoroutinesForTest()
	if after > before+5 {
		t.Errorf("leaked goroutines: %d before, %d after", before, after)
	}
}

// countGoroutinesForTest settles the scheduler and reports the count.
func countGoroutinesForTest() int {
	for range 30 {
		runtime.Gosched()
		time.Sleep(time.Millisecond)
	}
	return runtime.NumGoroutine()
}
