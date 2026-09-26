package stack

import (
	"slices"
	"testing"
)

func TestZeroValueIsUsable(t *testing.T) {
	var s Stack[int]

	if !s.IsEmpty() {
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

	if !s.IsEmpty() {
		t.Errorf("Len = %d after draining, want 0", s.Len())
	}
}

func TestPeekDoesNotRemove(t *testing.T) {
	var s Stack[string]
	s.Push("bottom")
	s.Push("top")

	for i := 0; i < 3; i++ {
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

func TestString(t *testing.T) {
	var s Stack[int]

	if got := s.String(); got != "(empty)" {
		t.Errorf("empty stack = %q", got)
	}

	s.Push(10)
	s.Push(20)
	if got, want := s.String(), "[10 20] <- top"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestSearchAndRemove(t *testing.T) {
	equal := func(a, b int) bool { return a == b }

	tests := []struct {
		name    string
		push    []int
		remove  int
		wantOK  bool
		wantAll []int
	}{
		{"from the middle", []int{1, 2, 3}, 2, true, []int{1, 3}},
		{"from the top", []int{1, 2, 3}, 3, true, []int{1, 2}},
		{"from the bottom", []int{1, 2, 3}, 1, true, []int{2, 3}},
		{"absent", []int{1, 2, 3}, 9, false, []int{1, 2, 3}},
		{"empty stack", nil, 1, false, []int{}},
		{"single element", []int{5}, 5, true, []int{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var s Stack[int]
			for _, v := range tt.push {
				s.Push(v)
			}

			got, ok := s.SearchAndRemove(tt.remove, equal)

			if ok != tt.wantOK {
				t.Fatalf("ok = %t, want %t", ok, tt.wantOK)
			}
			if ok && got != tt.remove {
				t.Errorf("returned %d, want %d", got, tt.remove)
			}
			if remaining := s.Slice(); !slices.Equal(remaining, tt.wantAll) {
				t.Errorf("remaining = %v, want %v", remaining, tt.wantAll)
			}
		})
	}
}

// TestSearchAndRemoveTakesTheTopmost: with duplicates, it must remove the one a
// sequence of Pops would reach first.
func TestSearchAndRemoveTakesTheTopmost(t *testing.T) {
	var s Stack[string]
	for _, v := range []string{"a", "x", "b", "x", "c"} {
		s.Push(v)
	}

	equal := func(a, b string) bool { return a == b }
	if _, ok := s.SearchAndRemove("x", equal); !ok {
		t.Fatal("expected to find x")
	}

	// The x at index 3 goes, not the one at index 1.
	if got, want := s.Slice(), []string{"a", "x", "b", "c"}; !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestSearchAndRemoveZeroesTheTail(t *testing.T) {
	s := New[*int](4)

	a, b := 1, 2
	s.Push(&a)
	s.Push(&b)

	if _, ok := s.SearchAndRemove(&a, func(x, y *int) bool { return x == y }); !ok {
		t.Fatal("expected to find the pointer")
	}

	full := s.items[:2:2]
	if full[1] != nil {
		t.Error("the vacated tail slot still holds a pointer")
	}
}

func BenchmarkPushPop(b *testing.B) {
	for b.Loop() {
		var s Stack[int]
		for i := 0; i < 1000; i++ {
			s.Push(i)
		}
		for i := 0; i < 1000; i++ {
			s.Pop()
		}
	}
}

func BenchmarkPushPopPresized(b *testing.B) {
	for b.Loop() {
		s := New[int](1000)
		for i := 0; i < 1000; i++ {
			s.Push(i)
		}
		for i := 0; i < 1000; i++ {
			s.Pop()
		}
	}
}
