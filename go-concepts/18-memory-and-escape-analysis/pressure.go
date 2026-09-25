package main

import (
	"fmt"
	"runtime"
	"time"
	"unsafe"
)

// Reducing GC pressure
// ====================
//
// The collector's work is proportional to the NUMBER OF POINTERS it must
// follow, not to the number of bytes. That single fact drives most of what
// follows.
//
//	[]int of a million          ONE object, no pointers inside: trivial to scan
//	[]*Item of a million        a million+1 objects, each with pointers to follow
//
// So a struct with no pointer fields is nearly free for the collector, however
// large, and a tree of small pointer-linked nodes is expensive however small.

// Item without pointers. The collector can skip its contents entirely once it
// knows the type has no pointer fields.
type Item struct {
	ID    int64
	Score float64
	Flags uint32
}

// ItemWithPointers has two, so every one of these is two more edges for the
// collector to follow.
type ItemWithPointers struct {
	ID    int64
	Name  *string
	Owner *Item
}

// buildValueSlice: one allocation, no pointers to scan.
func buildValueSlice(n int) []Item {
	items := make([]Item, n)
	for i := range items {
		items[i] = Item{ID: int64(i), Score: float64(i)}
	}
	return items
}

// buildPointerSlice: n+1 allocations, and n pointers for every GC cycle to
// follow for as long as the slice lives.
func buildPointerSlice(n int) []*Item {
	items := make([]*Item, n)
	for i := range items {
		items[i] = &Item{ID: int64(i), Score: float64(i)}
	}
	return items
}

// buildPointerHeavySlice is worse again: each element holds two more pointers.
func buildPointerHeavySlice(n int) []ItemWithPointers {
	names := make([]string, n)
	owners := make([]Item, n)

	items := make([]ItemWithPointers, n)
	for i := range items {
		names[i] = "item"
		owners[i] = Item{ID: int64(i)}
		items[i] = ItemWithPointers{
			ID:    int64(i),
			Name:  &names[i],
			Owner: &owners[i],
		}
	}
	return items
}

// measureGCImpact holds a structure alive and reports the fastest of several
// forced collections, which is the cost that structure imposes on every cycle.
//
// Two details that took a correction to get right:
//
//	BEST-OF-N. A single forced GC at 200,000 items reported 1.87ms against
//	1.98ms, which is noise, and would have made the pointer-density claim look
//	false. Taking the minimum of five removes the scheduling and timer noise,
//	the same fix as lesson 06's parallel-speedup test.
//
//	ENOUGH ITEMS. The effect is proportional to the pointer count, so it is
//	invisible at 200,000 and unmistakable at a million: measured here at 24x
//	for 1M items and 43x for 5M.
func measureGCImpact(build func() any, rounds int) (markTime time.Duration) {
	runtime.GC()

	held := build() // stays reachable for the duration

	best := time.Hour
	for i := 0; i < rounds; i++ {
		start := time.Now()
		runtime.GC()
		if d := time.Since(start); d < best {
			best = d
		}
	}

	runtime.KeepAlive(held)
	return best
}

// The string-interning trade
// --------------------------
//
// A million structs each holding their own copy of one of ten strings is a
// million string headers pointing at ten backing arrays. Interning replaces
// the strings with an index, removing a million pointers from the heap.
//
// Whether it is worth it depends entirely on whether the collector is actually
// the bottleneck, which is a profile question.

// Event with a string field: one pointer per event.
type Event struct {
	Timestamp int64
	Kind      string
}

// InternedEvent with an index instead: no pointers at all.
type InternedEvent struct {
	Timestamp int64
	KindIndex uint8
}

// eventKinds is the interning table.
var eventKinds = []string{"create", "update", "delete", "read", "list"}

func buildEvents(n int) []Event {
	events := make([]Event, n)
	for i := range events {
		events[i] = Event{Timestamp: int64(i), Kind: eventKinds[i%len(eventKinds)]}
	}
	return events
}

func buildInternedEvents(n int) []InternedEvent {
	events := make([]InternedEvent, n)
	for i := range events {
		events[i] = InternedEvent{Timestamp: int64(i), KindIndex: uint8(i % len(eventKinds))}
	}
	return events
}

// Kind resolves an interned event back to its string, which costs one bounds
// check and no allocation.
func (e InternedEvent) Kind() string { return eventKinds[e.KindIndex] }

// reducingPressure is the ordered list.
func reducingPressure() []string {
	return []string{
		"1. allocate less: pre-size, strings.Builder, strconv over fmt (lesson 17's table)",
		"2. reuse buffers with sync.Pool, in a genuinely hot path (lesson 09)",
		"3. fewer POINTERS: []T beats []*T, because the collector follows pointers not bytes",
		"4. raise GOGC, trading memory for CPU, when a profile says the collector is the problem",
		"...and measure after each, because 3 and 4 both have losing cases",
	}
}

// whenPointersAreStillRight, because "avoid pointers" is not the lesson.
func whenPointersAreStillRight() []string {
	return []string{
		"a large struct copied often: the copy costs more than the pointer",
		"anything needing identity, or mutation visible to a caller",
		"a linked structure: a tree is pointers by definition",
		"an optional field: *T distinguishes absent from zero (lesson 12)",
		"the rule is \"know what it costs\", not \"never use them\"",
	}
}

// demoPressure prints the pointer-density effect.
func demoPressure() {
	// A million, not two hundred thousand: the effect is proportional to the
	// pointer count and is simply not visible at the smaller size.
	const n = 1_000_000

	valueTime := measureGCImpact(func() any { return buildValueSlice(n) }, 5)
	pointerTime := measureGCImpact(func() any { return buildPointerSlice(n) }, 5)
	heavyTime := measureGCImpact(func() any { return buildPointerHeavySlice(n) }, 5)

	fmt.Printf("  the fastest of 5 forced collections, %d items held alive:\n", n)
	fmt.Printf("    []Item             (0 pointers each): %v\n", valueTime.Round(time.Microsecond))
	fmt.Printf("    []*Item            (1 pointer each):  %v  (%.0fx)\n",
		pointerTime.Round(time.Microsecond), float64(pointerTime)/float64(valueTime))
	fmt.Printf("    []ItemWithPointers (2 pointers each): %v  (%.0fx)\n",
		heavyTime.Round(time.Microsecond), float64(heavyTime)/float64(valueTime))
	fmt.Println("    ...the collector follows POINTERS, not bytes")

	const eventCount = 200_000
	events := buildEvents(eventCount)
	interned := buildInternedEvents(eventCount)
	fmt.Printf("\n  %d events with a string field vs an interned index:\n", eventCount)
	fmt.Printf("    Event.Kind        = %q\n", events[0].Kind)
	fmt.Printf("    InternedEvent.Kind() = %q, and the struct holds no pointer\n", interned[0].Kind())
	runtime.KeepAlive(events)
	runtime.KeepAlive(interned)

	fmt.Println("\n  reducing pressure, in order:")
	for _, s := range reducingPressure() {
		fmt.Printf("    %s\n", s)
	}

	fmt.Println("\n  when a pointer is still right:")
	for _, s := range whenPointersAreStillRight() {
		fmt.Printf("    %s\n", s)
	}
}

// unsafeSizeof exposes the size of a value so pressure_test.go can assert that
// interning shrinks the struct, without importing unsafe into a test file.
//
// unsafe.Sizeof is a compile-time constant and involves no unsafe operation:
// it reads the type's size and nothing more. It is in package unsafe because
// the size is implementation-defined, not because using it is dangerous.
func unsafeSizeof(v any) uintptr {
	switch v.(type) {
	case Event:
		return unsafe.Sizeof(Event{})
	case InternedEvent:
		return unsafe.Sizeof(InternedEvent{})
	default:
		return 0
	}
}
