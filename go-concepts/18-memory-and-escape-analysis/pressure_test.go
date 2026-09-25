package main

import (
	"testing"
	"time"
)

func TestBuildersProduceWhatTheyClaim(t *testing.T) {
	const n = 1000

	values := buildValueSlice(n)
	pointers := buildPointerSlice(n)
	heavy := buildPointerHeavySlice(n)

	if len(values) != n || len(pointers) != n || len(heavy) != n {
		t.Fatalf("lengths: %d, %d, %d; want %d each", len(values), len(pointers), len(heavy), n)
	}

	// They must hold the same IDs, or the comparison is measuring different
	// amounts of data rather than different pointer densities.
	for i := 0; i < n; i++ {
		if values[i].ID != int64(i) || pointers[i].ID != int64(i) || heavy[i].ID != int64(i) {
			t.Fatalf("index %d: ids differ", i)
		}
	}
}

// TestPointerDensityCostsTheCollector is the claim, measured.
//
// Best-of-N and a million items, both learned the hard way: a single forced GC
// at 200,000 items reported 1.87ms against 1.98ms, which is noise, and would
// have made this look false.
func TestPointerDensityCostsTheCollector(t *testing.T) {
	if testing.Short() {
		t.Skip("holds a million items alive and forces collections")
	}

	const n = 1_000_000

	valueTime := measureGCImpact(func() any { return buildValueSlice(n) }, 5)
	pointerTime := measureGCImpact(func() any { return buildPointerSlice(n) }, 5)

	ratio := float64(pointerTime) / float64(valueTime)
	t.Logf("[]Item %v, []*Item %v, ratio %.1fx", valueTime, pointerTime, ratio)

	if pointerTime <= valueTime {
		t.Errorf("[]*Item marked in %v and []Item in %v — pointers should cost more",
			pointerTime, valueTime)
	}
	// A loose floor: measured at 2.8x in-process and 24x in isolation.
	if ratio < 1.5 {
		t.Errorf("ratio %.1fx, want at least 1.5x", ratio)
	}
}

func TestMeasureGCImpactTakesTheBest(t *testing.T) {
	got := measureGCImpact(func() any { return buildValueSlice(100) }, 3)

	t.Logf("a collection with 100 items held: %v", got)

	// NOT "> 0". CI caught this on Windows, where the clock resolution is
	// coarser than a collection of 100 items and time.Since returns exactly
	// zero. A zero duration is a legitimate measurement of something faster
	// than the clock can see, not a bug.
	//
	// The same mistake as lesson 06's parallel-speedup test and lesson 07's
	// handshake ordering: asserting more than the platform guarantees. A
	// monotonic clock promises non-decreasing, not a minimum granularity.
	if got < 0 {
		t.Errorf("measured %v, which a monotonic clock cannot produce", got)
	}
	if got > 5*time.Second {
		t.Errorf("measured %v, which is implausible for 100 items", got)
	}
}

// TestMeasureGCImpactIsMeasurableAtScale is the positive half: with enough
// items the duration exceeds any platform's clock resolution.
func TestMeasureGCImpactIsMeasurableAtScale(t *testing.T) {
	if testing.Short() {
		t.Skip("holds a million items alive")
	}

	got := measureGCImpact(func() any { return buildPointerSlice(1_000_000) }, 3)

	if got <= 0 {
		t.Errorf("measured %v for a million pointers, want something the clock can see", got)
	}
}

func TestInternedEventsMatchTheirStrings(t *testing.T) {
	const n = 1000

	events := buildEvents(n)
	interned := buildInternedEvents(n)

	if len(events) != len(interned) {
		t.Fatalf("lengths differ: %d and %d", len(events), len(interned))
	}

	for i := range events {
		if events[i].Kind != interned[i].Kind() {
			t.Fatalf("index %d: %q vs %q", i, events[i].Kind, interned[i].Kind())
		}
		if events[i].Timestamp != interned[i].Timestamp {
			t.Fatalf("index %d: timestamps differ", i)
		}
	}
}

// TestInternedEventIsSmaller: the whole point is removing a pointer from every
// element, which also makes the struct smaller.
func TestInternedEventIsSmaller(t *testing.T) {
	var e Event
	var ie InternedEvent

	eventSize := int(unsafeSizeof(e))
	internedSize := int(unsafeSizeof(ie))

	t.Logf("Event: %d bytes, InternedEvent: %d bytes", eventSize, internedSize)

	if internedSize >= eventSize {
		t.Errorf("InternedEvent is %d bytes and Event is %d — interning should shrink it",
			internedSize, eventSize)
	}
}

func TestPressureDocsArePresent(t *testing.T) {
	if got := reducingPressure(); len(got) < 4 {
		t.Errorf("expected at least 4 steps, got %d", len(got))
	}
	if got := whenPointersAreStillRight(); len(got) < 4 {
		t.Errorf("expected at least 4 cases, got %d", len(got))
	}
}

func BenchmarkBuildValueSlice(b *testing.B) {
	for b.Loop() {
		sinkItems = buildValueSlice(10_000)
	}
}

func BenchmarkBuildPointerSlice(b *testing.B) {
	for b.Loop() {
		sinkItemPtrs = buildPointerSlice(10_000)
	}
}

func BenchmarkBuildEvents(b *testing.B) {
	for b.Loop() {
		sinkEvents = buildEvents(10_000)
	}
}

func BenchmarkBuildInternedEvents(b *testing.B) {
	for b.Loop() {
		sinkInterned = buildInternedEvents(10_000)
	}
}
