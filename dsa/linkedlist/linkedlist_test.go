package linkedlist

import (
	"slices"
	"testing"
)

// TestZeroValueIsUsable is the rule from go-concepts/01: a container should
// need no constructor.
func TestZeroValueIsUsable(t *testing.T) {
	var l LinkedList[int]

	if l.Len() != 0 {
		t.Errorf("Len = %d, want 0 for a fresh list", l.Len())
	}

	l.PushBack(1)
	if l.Len() != 1 {
		t.Errorf("Len = %d after one append, want 1", l.Len())
	}
}

func TestPushFront(t *testing.T) {
	var l LinkedList[int]

	for _, v := range []int{1, 2, 3} {
		l.PushFront(v)
	}

	// Adding at the head reverses the insertion order.
	if got, want := l.Slice(), []int{3, 2, 1}; !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if l.Len() != 3 {
		t.Errorf("Len = %d, want 3", l.Len())
	}

	// The tail must have been set by the first insertion and never moved.
	if tail, ok := l.Back(); !ok || tail != 1 {
		t.Errorf("Back = %d, %t; want 1, true", tail, ok)
	}
}

func TestPushBack(t *testing.T) {
	var l LinkedList[string]

	for _, v := range []string{"a", "b", "c"} {
		l.PushBack(v)
	}

	if got, want := l.Slice(), []string{"a", "b", "c"}; !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if head, ok := l.Front(); !ok || head != "a" {
		t.Errorf("Front = %q, %t; want a, true", head, ok)
	}
	if tail, ok := l.Back(); !ok || tail != "c" {
		t.Errorf("Back = %q, %t; want c, true", tail, ok)
	}
}

// TestMixedInsertion checks the head and tail pointers stay consistent when
// both ends are used, which is where an off-by-one hides.
func TestMixedInsertion(t *testing.T) {
	var l LinkedList[int]

	l.PushBack(2)  // [2]
	l.PushFront(1) // [1 2]
	l.PushBack(3)  // [1 2 3]
	l.PushFront(0) // [0 1 2 3]

	if got, want := l.Slice(), []int{0, 1, 2, 3}; !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if l.Len() != 4 {
		t.Errorf("Len = %d, want 4", l.Len())
	}
}

func TestPopFront(t *testing.T) {
	var l LinkedList[int]
	for _, v := range []int{1, 2, 3} {
		l.PushBack(v)
	}

	for want := 1; want <= 3; want++ {
		got, ok := l.PopFront()
		if !ok {
			t.Fatalf("PopFront reported empty with %d elements left", l.Len())
		}
		if got != want {
			t.Errorf("got %d, want %d", got, want)
		}
	}

	if l.Len() != 0 {
		t.Errorf("list should be empty, Len = %d", l.Len())
	}

	// And on an empty list.
	if _, ok := l.PopFront(); ok {
		t.Error("PopFront on an empty list should report false")
	}
}

func TestPopBack(t *testing.T) {
	var l LinkedList[int]
	for _, v := range []int{1, 2, 3} {
		l.PushBack(v)
	}

	for want := 3; want >= 1; want-- {
		got, ok := l.PopBack()
		if !ok {
			t.Fatalf("PopBack reported empty with %d elements left", l.Len())
		}
		if got != want {
			t.Errorf("got %d, want %d", got, want)
		}
	}

	if l.Len() != 0 {
		t.Errorf("list should be empty, Len = %d", l.Len())
	}
	if _, ok := l.PopBack(); ok {
		t.Error("PopBack on an empty list should report false")
	}
}

// TestSingleElementRemoval is where head and tail must both be cleared, and
// where a list implementation usually breaks first.
func TestSingleElementRemoval(t *testing.T) {
	t.Run("from the head", func(t *testing.T) {
		var l LinkedList[int]
		l.PushBack(42)

		if got, ok := l.PopFront(); !ok || got != 42 {
			t.Fatalf("got %d, %t", got, ok)
		}
		if _, ok := l.Front(); ok {
			t.Error("Front should report empty")
		}
		if _, ok := l.Back(); ok {
			t.Error("Back should report empty — it was left dangling")
		}
	})

	t.Run("from the tail", func(t *testing.T) {
		var l LinkedList[int]
		l.PushBack(42)

		if got, ok := l.PopBack(); !ok || got != 42 {
			t.Fatalf("got %d, %t", got, ok)
		}
		if _, ok := l.Front(); ok {
			t.Error("Front should report empty — it was left dangling")
		}
		if _, ok := l.Back(); ok {
			t.Error("Back should report empty")
		}
	})
}

// TestTailPointerSurvivesEmptying: adding after emptying must work, which
// catches a tail left pointing at a removed node.
func TestTailPointerSurvivesEmptying(t *testing.T) {
	var l LinkedList[int]

	l.PushBack(1)
	l.PopFront()
	l.PushBack(2)

	if got, want := l.Slice(), []int{2}; !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if tail, ok := l.Back(); !ok || tail != 2 {
		t.Errorf("Back = %d, %t; want 2, true", tail, ok)
	}
}

// TestRemoveDoesNotCorruptTheRest is the memory property: a removed node
// must not keep the rest of the list reachable.
//
// It cannot be observed directly from Go, so this checks the consequence that
// can be: the list is still correct, and the removed value is returned by
// value rather than as part of a structure the caller could walk.
func TestRemoveDoesNotCorruptTheRest(t *testing.T) {
	var l LinkedList[int]
	for i := 1; i <= 100; i++ {
		l.PushBack(i)
	}

	for i := range 50 {
		if _, ok := l.PopFront(); !ok {
			t.Fatalf("removal %d failed", i)
		}
	}

	want := make([]int, 0, 50)
	for i := 51; i <= 100; i++ {
		want = append(want, i)
	}
	if got := l.Slice(); !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestAllIterator(t *testing.T) {
	var l LinkedList[int]
	for _, v := range []int{1, 2, 3, 4, 5} {
		l.PushBack(v)
	}

	var got []int
	for v := range l.All() {
		got = append(got, v)
	}

	if want := []int{1, 2, 3, 4, 5}; !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestAllIteratorStopsOnBreak is the yield contract: the producer must stop
// when the consumer does. go-concepts/11 measured what happens when it does
// not (the runtime panics).
func TestAllIteratorStopsOnBreak(t *testing.T) {
	var l LinkedList[int]
	for i := 1; i <= 1000; i++ {
		l.PushBack(i)
	}

	visited := 0
	for range l.All() {
		visited++
		if visited == 3 {
			break
		}
	}

	if visited != 3 {
		t.Errorf("visited %d elements, want 3", visited)
	}
}

func TestAllIteratorOnEmpty(t *testing.T) {
	var l LinkedList[int]

	for range l.All() {
		t.Error("an empty list should yield nothing")
	}
}

func TestString(t *testing.T) {
	tests := []struct {
		name  string
		build func(*LinkedList[int])
		want  string
	}{
		{"empty", func(*LinkedList[int]) {}, "(empty)"},
		{"one", func(l *LinkedList[int]) { l.PushBack(1) }, "1"},
		{"three", func(l *LinkedList[int]) {
			l.PushBack(10)
			l.PushBack(20)
			l.PushBack(30)
		}, "10 -> 20 -> 30"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var l LinkedList[int]
			tt.build(&l)

			if got := l.String(); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestReverse(t *testing.T) {
	tests := []struct {
		name string
		in   []int
		want []int
	}{
		{"empty", nil, nil},
		{"one", []int{1}, []int{1}},
		{"two", []int{1, 2}, []int{2, 1}},
		{"several", []int{1, 2, 3, 4, 5}, []int{5, 4, 3, 2, 1}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var l LinkedList[int]
			for _, v := range tt.in {
				l.PushBack(v)
			}

			l.Reverse()

			if got := l.Slice(); !slices.Equal(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}

			// The head and tail pointers must have swapped, or a later
			// PushBack appends in the wrong place.
			if len(tt.want) > 0 {
				if head, _ := l.Front(); head != tt.want[0] {
					t.Errorf("Front = %d, want %d", head, tt.want[0])
				}
				if tail, _ := l.Back(); tail != tt.want[len(tt.want)-1] {
					t.Errorf("Back = %d, want %d", tail, tt.want[len(tt.want)-1])
				}
			}
		})
	}
}

// TestReverseThenAppend is the check that catches a Reverse which fixes the
// values and forgets the tail pointer.
func TestReverseThenAppend(t *testing.T) {
	var l LinkedList[int]
	for _, v := range []int{1, 2, 3} {
		l.PushBack(v)
	}

	l.Reverse()
	l.PushBack(0)

	if got, want := l.Slice(), []int{3, 2, 1, 0}; !slices.Equal(got, want) {
		t.Errorf("got %v, want %v — the tail pointer was not updated", got, want)
	}
}

func BenchmarkPushBack(b *testing.B) {
	for b.Loop() {
		var l LinkedList[int]
		for i := range 1000 {
			l.PushBack(i)
		}
	}
}

// BenchmarkSliceAppend is the comparison that matters: a slice does the same
// job faster, which is why a linked list is rarely the answer in Go.
func BenchmarkSliceAppend(b *testing.B) {
	for b.Loop() {
		s := make([]int, 0, 1000)
		for i := range 1000 {
			s = append(s, i)
		}
		_ = s
	}
}

// BenchmarkListPrepend is the case the list wins: O(1) against a slice's O(n).
func BenchmarkListPrepend(b *testing.B) {
	for b.Loop() {
		var l LinkedList[int]
		for i := range 1000 {
			l.PushFront(i)
		}
	}
}

func BenchmarkSlicePrepend(b *testing.B) {
	for b.Loop() {
		var s []int
		for i := range 1000 {
			s = append([]int{i}, s...) // the O(n) prepend
		}
		_ = s
	}
}
