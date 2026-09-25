package main

import (
	"cmp"
	"fmt"
)

// Generic types
// =============
//
//	type Stack[T any] struct { items []T }
//
// A TYPE can have type parameters. A METHOD cannot add its own:
//
//	func (s *Stack[T]) Map[U any](f func(T) U) *Stack[U]   // DOES NOT COMPILE
//	  -> method must have no type parameters
//
// The reason is interface satisfaction: a method with its own type parameter
// would mean an interface had to match an infinite family of methods, which is
// undecidable. The workaround is a plain function, which is why the generic
// standard library is mostly functions.

// Stack is a generic LIFO container. The zero value is usable, following the
// rule from lesson 01.
type Stack[T any] struct {
	items []T
}

// NewStack returns an empty stack with room reserved. The type parameter is
// explicit at the call site because there are no arguments to infer from:
// NewStack[int](10).
func NewStack[T any](capacity int) *Stack[T] {
	return &Stack[T]{items: make([]T, 0, capacity)}
}

// Push adds to the top. Note the receiver: *Stack[T], with the parameter
// repeated. Every method on a generic type names its parameters this way.
func (s *Stack[T]) Push(v T) { s.items = append(s.items, v) }

// Pop removes and returns the top. The comma-ok result is how a generic
// container reports "empty" without knowing whether T has a useful zero value.
func (s *Stack[T]) Pop() (T, bool) {
	if len(s.items) == 0 {
		return Zero[T](), false
	}

	last := len(s.items) - 1
	v := s.items[last]

	// Zero the vacated slot before shortening, so a stack of pointers does not
	// pin what it popped. Same reasoning as lesson 02's delete.
	s.items[last] = Zero[T]()
	s.items = s.items[:last]

	return v, true
}

// Peek returns the top without removing it.
func (s *Stack[T]) Peek() (T, bool) {
	if len(s.items) == 0 {
		return Zero[T](), false
	}
	return s.items[len(s.items)-1], true
}

// Len reports the element count.
func (s *Stack[T]) Len() int { return len(s.items) }

// Items returns a copy, so a caller cannot reach into the stack's backing
// array. Returning s.items directly would hand out an aliased slice, which is
// lesson 02's trap wearing a container.
func (s *Stack[T]) Items() []T {
	out := make([]T, len(s.items))
	copy(out, s.items)
	return out
}

// MapStack is the workaround for the method that cannot exist. A plain
// function can introduce U, so the transform lives here rather than on Stack.
func MapStack[T, U any](s *Stack[T], f func(T) U) *Stack[U] {
	out := NewStack[U](s.Len())
	for _, item := range s.items {
		out.Push(f(item))
	}
	return out
}

// Pair is a generic struct with two parameters, which is how you build a tuple
// in a language with no tuples.
type Pair[A, B any] struct {
	First  A
	Second B
}

// MakePair infers both parameters from its arguments.
func MakePair[A, B any](a A, b B) Pair[A, B] {
	return Pair[A, B]{First: a, Second: b}
}

// Swap returns the pair reversed. It can be a method because it introduces no
// NEW type parameter: it reuses A and B from the receiver.
func (p Pair[A, B]) Swap() Pair[B, A] {
	return Pair[B, A]{First: p.Second, Second: p.First}
}

// String makes Pair printable.
func (p Pair[A, B]) String() string {
	return fmt.Sprintf("(%v, %v)", p.First, p.Second)
}

// Tree is a generic binary search tree, which needs cmp.Ordered on the TYPE
// rather than on each method, because the ordering is a property of the
// container's whole lifetime.
type Tree[T cmp.Ordered] struct {
	root *node[T]
	size int
}

type node[T cmp.Ordered] struct {
	value       T
	left, right *node[T]
}

// Insert adds a value, ignoring duplicates.
func (t *Tree[T]) Insert(v T) {
	root, inserted := insert(t.root, v)

	t.root = root
	if inserted {
		t.size++
	}
}

func insert[T cmp.Ordered](n *node[T], v T) (*node[T], bool) {
	if n == nil {
		return &node[T]{value: v}, true
	}

	switch {
	case v < n.value:
		var ok bool
		n.left, ok = insert(n.left, v)
		return n, ok
	case v > n.value:
		var ok bool
		n.right, ok = insert(n.right, v)
		return n, ok
	default:
		return n, false // already present
	}
}

// Contains reports membership.
func (t *Tree[T]) Contains(v T) bool {
	n := t.root
	for n != nil {
		switch {
		case v < n.value:
			n = n.left
		case v > n.value:
			n = n.right
		default:
			return true
		}
	}
	return false
}

// InOrder returns every value in sorted order.
func (t *Tree[T]) InOrder() []T {
	out := make([]T, 0, t.size)

	var walk func(*node[T])
	walk = func(n *node[T]) {
		if n == nil {
			return
		}
		walk(n.left)
		out = append(out, n.value)
		walk(n.right)
	}
	walk(t.root)

	return out
}

// Len reports the value count.
func (t *Tree[T]) Len() int { return t.size }

// genericTypesInInterfaces: an INSTANTIATED generic type can satisfy an
// interface, and the generic type itself cannot. Stack[int] has methods;
// Stack does not exist as a type at all without its argument.
type Sized interface{ Len() int }

var (
	_ Sized = (*Stack[int])(nil)
	_ Sized = (*Tree[string])(nil)
	// var _ Sized = (*Stack)(nil)   -> cannot use generic type Stack[T any] without instantiation
)

// demoTypes prints generic containers.
func demoTypes() {
	s := NewStack[string](4)
	s.Push("first")
	s.Push("second")
	s.Push("third")

	top, _ := s.Peek()
	fmt.Printf("  Stack[string]: len=%d peek=%q\n", s.Len(), top)

	popped, _ := s.Pop()
	fmt.Printf("  Pop() -> %q, len now %d\n", popped, s.Len())

	_, ok := NewStack[int](0).Pop()
	fmt.Printf("  Pop() on an empty stack: ok=%t\n", ok)

	lengths := MapStack(s, func(v string) int { return len(v) })
	fmt.Printf("  MapStack(stack, len) -> %v   <- a function, because a method cannot add U\n",
		lengths.Items())

	p := MakePair("answer", 42)
	fmt.Printf("\n  Pair[string, int]: %v, swapped: %v\n", p, p.Swap())

	var tree Tree[int]
	for _, v := range []int{5, 3, 8, 1, 4, 8, 7} {
		tree.Insert(v)
	}
	fmt.Printf("\n  Tree[int] after inserting 5,3,8,1,4,8,7: %v (len %d, duplicate ignored)\n",
		tree.InOrder(), tree.Len())
	fmt.Printf("  Contains(4)=%t Contains(6)=%t\n", tree.Contains(4), tree.Contains(6))

	var words Tree[string]
	for _, w := range []string{"go", "rust", "c", "zig"} {
		words.Insert(w)
	}
	fmt.Printf("  Tree[string]: %v\n", words.InOrder())

	fmt.Println("\n  a method cannot add a type parameter:")
	fmt.Println("    func (s *Stack[T]) Map[U any](...)  -> method must have no type parameters")
	fmt.Println("    func MapStack[T, U any](s *Stack[T], ...)  -> the workaround")
}
