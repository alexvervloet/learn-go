package main

import (
	"slices"
	"testing"
)

func TestStack(t *testing.T) {
	s := NewStack[string](2)

	if s.Len() != 0 {
		t.Errorf("new stack Len = %d, want 0", s.Len())
	}
	if _, ok := s.Pop(); ok {
		t.Error("Pop on an empty stack should report false")
	}
	if _, ok := s.Peek(); ok {
		t.Error("Peek on an empty stack should report false")
	}

	s.Push("first")
	s.Push("second")

	if s.Len() != 2 {
		t.Errorf("Len = %d, want 2", s.Len())
	}

	top, ok := s.Peek()
	if !ok || top != "second" {
		t.Errorf("Peek = %q, %t; want second, true", top, ok)
	}
	if s.Len() != 2 {
		t.Errorf("Peek should not remove: Len = %d, want 2", s.Len())
	}

	popped, ok := s.Pop()
	if !ok || popped != "second" {
		t.Errorf("Pop = %q, %t; want second, true", popped, ok)
	}
	if s.Len() != 1 {
		t.Errorf("Len after Pop = %d, want 1", s.Len())
	}
}

// TestStackZeroValueIsUsable is the lesson 01 rule, applied to a generic type.
func TestStackZeroValueIsUsable(t *testing.T) {
	var s Stack[int] // no constructor

	s.Push(1)
	s.Push(2)

	if s.Len() != 2 {
		t.Errorf("Len = %d, want 2", s.Len())
	}
	if v, _ := s.Pop(); v != 2 {
		t.Errorf("Pop = %d, want 2", v)
	}
}

// TestStackItemsIsACopy: handing out the internal slice would let a caller
// scribble on the stack's backing array. Lesson 02's aliasing trap, in a box.
func TestStackItemsIsACopy(t *testing.T) {
	s := NewStack[int](4)
	s.Push(1)
	s.Push(2)

	items := s.Items()
	items[0] = 99

	fresh := s.Items()
	if fresh[0] != 1 {
		t.Errorf("mutating the returned slice reached the stack: %v", fresh)
	}
}

// TestStackPopZeroesTheSlot: a stack of pointers must not pin what it popped.
func TestStackPopZeroesTheSlot(t *testing.T) {
	s := NewStack[*Product](4)
	p := &Product{SKU: "A1", Title: "Keyboard"}

	s.Push(p)
	popped, _ := s.Pop()

	if popped != p {
		t.Fatal("Pop should return what was pushed")
	}
	// The slot past len must have been cleared. Reaching it needs the full
	// slice expression, which is how the test can see what len hides.
	full := s.items[:1:1]
	if full[0] != nil {
		t.Error("the vacated slot still holds a pointer — it should be zeroed")
	}
}

func TestMapStack(t *testing.T) {
	s := NewStack[string](3)
	s.Push("go")
	s.Push("generics")

	lengths := MapStack(s, func(v string) int { return len(v) })

	if want := []int{2, 8}; !slices.Equal(lengths.Items(), want) {
		t.Errorf("got %v, want %v", lengths.Items(), want)
	}
	// The source stack is untouched.
	if s.Len() != 2 {
		t.Errorf("source Len = %d, want 2", s.Len())
	}
}

func TestPair(t *testing.T) {
	p := MakePair("answer", 42)

	if p.First != "answer" || p.Second != 42 {
		t.Errorf("pair = %v", p)
	}
	if got := p.String(); got != "(answer, 42)" {
		t.Errorf("String() = %q", got)
	}

	swapped := p.Swap()
	if swapped.First != 42 || swapped.Second != "answer" {
		t.Errorf("swapped = %v", swapped)
	}
	// Swap returns Pair[B, A], a different type. The explicit type on this
	// declaration IS the assertion: if Swap ever returned Pair[A, B] the line
	// would stop compiling.
	var _ Pair[int, string] = swapped //nolint:staticcheck // the explicit type is the assertion
}

func TestTree(t *testing.T) {
	var tree Tree[int]

	for _, v := range []int{5, 3, 8, 1, 4, 7} {
		tree.Insert(v)
	}

	if want := []int{1, 3, 4, 5, 7, 8}; !slices.Equal(tree.InOrder(), want) {
		t.Errorf("InOrder = %v, want %v", tree.InOrder(), want)
	}
	if tree.Len() != 6 {
		t.Errorf("Len = %d, want 6", tree.Len())
	}

	for _, v := range []int{1, 3, 4, 5, 7, 8} {
		if !tree.Contains(v) {
			t.Errorf("Contains(%d) = false, want true", v)
		}
	}
	for _, v := range []int{0, 2, 6, 9} {
		if tree.Contains(v) {
			t.Errorf("Contains(%d) = true, want false", v)
		}
	}
}

func TestTreeIgnoresDuplicates(t *testing.T) {
	var tree Tree[int]

	for _, v := range []int{5, 5, 5, 3} {
		tree.Insert(v)
	}

	if tree.Len() != 2 {
		t.Errorf("Len = %d, want 2 — duplicates should be ignored", tree.Len())
	}
	if want := []int{3, 5}; !slices.Equal(tree.InOrder(), want) {
		t.Errorf("InOrder = %v, want %v", tree.InOrder(), want)
	}
}

func TestTreeOfStrings(t *testing.T) {
	var tree Tree[string]

	for _, w := range []string{"go", "rust", "c", "zig", "go"} {
		tree.Insert(w)
	}

	if want := []string{"c", "go", "rust", "zig"}; !slices.Equal(tree.InOrder(), want) {
		t.Errorf("InOrder = %v, want %v", tree.InOrder(), want)
	}
}

func TestEmptyTree(t *testing.T) {
	var tree Tree[int]

	if tree.Len() != 0 {
		t.Errorf("Len = %d, want 0", tree.Len())
	}
	if tree.Contains(1) {
		t.Error("an empty tree contains nothing")
	}
	if got := tree.InOrder(); len(got) != 0 {
		t.Errorf("InOrder = %v, want empty", got)
	}
}

// TestInstantiatedTypesSatisfyInterfaces: Stack[int] has methods, Stack does
// not exist without its argument. The var block in types.go asserts this at
// compile time; this checks it at the value level too.
func TestInstantiatedTypesSatisfyInterfaces(t *testing.T) {
	var sized Sized = NewStack[int](1)
	if sized.Len() != 0 {
		t.Errorf("Len = %d, want 0", sized.Len())
	}

	sized = &Tree[string]{}
	if sized.Len() != 0 {
		t.Errorf("Len = %d, want 0", sized.Len())
	}
}
