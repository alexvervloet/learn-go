package stack

import (
	"slices"
	"testing"
)

func TestZeroValueIsUsable(t *testing.T) {
	var s Stack[int]

	if s.Len() != 0 {
		t.Error("a fresh stack should be empty")
	}
	if _, ok := s.Pop(); ok {
		t.Error("Pop on an empty stack should report false")
	}
	if _, ok := s.Peek(); ok {
		t.Error("Peek on an empty stack should report false")
	}

	s.Push(1)
	if s.Len() != 1 {
		t.Errorf("Len = %d, want 1", s.Len())
	}
}

func TestPushPopIsLIFO(t *testing.T) {
	var s Stack[int]

	for _, v := range []int{1, 2, 3} {
		s.Push(v)
	}

	for want := 3; want >= 1; want-- {
		got, ok := s.Pop()
		if !ok {
			t.Fatalf("Pop reported empty with %d left", s.Len())
		}
		if got != want {
			t.Errorf("got %d, want %d — a stack is last in, first out", got, want)
		}
	}

	if s.Len() != 0 {
		t.Errorf("Len = %d after draining, want 0", s.Len())
	}
}

func TestPeekDoesNotRemove(t *testing.T) {
	var s Stack[string]
	s.Push("bottom")
	s.Push("top")

	for range 3 {
		got, ok := s.Peek()
		if !ok || got != "top" {
			t.Errorf("Peek = %q, %t; want top, true", got, ok)
		}
	}

	if s.Len() != 2 {
		t.Errorf("Len = %d after three Peeks, want 2", s.Len())
	}
}

func TestNewReservesCapacity(t *testing.T) {
	s := New[int](100)

	if s.Len() != 0 {
		t.Errorf("Len = %d, want 0", s.Len())
	}
	if got := cap(s.items); got != 100 {
		t.Errorf("cap = %d, want 100", got)
	}

	// A negative capacity must not panic.
	neg := New[int](-5)
	neg.Push(1)
	if neg.Len() != 1 {
		t.Errorf("Len = %d, want 1", neg.Len())
	}
}

// TestPopZeroesTheVacatedSlot is the memory property. A Stack[*int] that keeps
// the popped pointer past len pins whatever it points at.
func TestPopZeroesTheVacatedSlot(t *testing.T) {
	s := New[*int](4)

	value := 42
	s.Push(&value)

	if _, ok := s.Pop(); !ok {
		t.Fatal("Pop failed")
	}

	// Reach past len with a full slice expression to inspect the vacated slot.
	full := s.items[:1:1]
	if full[0] != nil {
		t.Error("the vacated slot still holds a pointer — it should be zeroed")
	}
}

// TestSliceIsACopy: handing out the backing array would let a caller mutate the
// stack through it.
func TestSliceIsACopy(t *testing.T) {
	var s Stack[int]
	s.Push(1)
	s.Push(2)

	got := s.Slice()
	got[0] = 99

	if fresh := s.Slice(); fresh[0] != 1 {
		t.Errorf("mutating the returned slice reached the stack: %v", fresh)
	}
}

func TestSliceOrderIsBottomToTop(t *testing.T) {
	var s Stack[int]
	for _, v := range []int{1, 2, 3} {
		s.Push(v)
	}

	if got, want := s.Slice(), []int{1, 2, 3}; !slices.Equal(got, want) {
		t.Errorf("got %v, want %v (bottom to top)", got, want)
	}
}
func BenchmarkPushPop(b *testing.B) {
	for b.Loop() {
		var s Stack[int]
		for i := range 1000 {
			s.Push(i)
		}
		for range 1000 {
			s.Pop()
		}
	}
}

func BenchmarkPushPopPresized(b *testing.B) {
	for b.Loop() {
		s := New[int](1000)
		for i := range 1000 {
			s.Push(i)
		}
		for range 1000 {
			s.Pop()
		}
	}
}

// TestAllWalksTopDownAndStopsOnBreak: All is Pop order, removes nothing, and honours break.
func TestAllWalksTopDownAndStopsOnBreak(t *testing.T) {
	var s Stack[int]
	for _, v := range []int{1, 2, 3} {
		s.Push(v)
	}

	if got := slices.Collect(s.All()); !slices.Equal(got, []int{3, 2, 1}) {
		t.Errorf("All = %v, want [3 2 1]", got)
	}

	var seen []int
	for v := range s.All() {
		seen = append(seen, v)
		break
	}

	if !slices.Equal(seen, []int{3}) || s.Len() != 3 {
		t.Errorf("break: saw %v and Len is %d; want [3] and 3", seen, s.Len())
	}
}
