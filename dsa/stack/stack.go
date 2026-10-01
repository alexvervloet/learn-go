// Package stack implements a last-in-first-out stack over a slice.
//
// A stack touches one end only, and a slice's one end is its cheap end: append
// and reslice are both O(1) amortised, contiguous, and allocation-free per
// element. A linked-list stack allocates a node per push for no benefit, so
// this is a slice with three methods and a name.
package stack

import (
	"fmt"
	"slices"
	"strings"
)

// Stack is a LIFO stack. The zero value is an empty stack ready to use.
type Stack[T any] struct {
	items []T
}

// New returns a stack with room for n elements reserved, which avoids the
// regrowth of the first log2(n) pushes when the size is known.
//
// The zero value works too; this is for when you know the size.
func New[T any](capacity int) *Stack[T] {
	return &Stack[T]{items: make([]T, 0, max(0, capacity))}
}

// Len reports the number of elements.
func (s *Stack[T]) Len() int { return len(s.items) }

// Push adds v to the top.
func (s *Stack[T]) Push(v T) { s.items = append(s.items, v) }

// Pop removes and returns the top element, reporting whether there was one.
//
// The comma-ok result is the only correct signature: a Stack[int] cannot use 0
// to mean empty. Panicking would be defensible for a stack that is non-empty by
// construction, and this one makes no such promise.
func (s *Stack[T]) Pop() (T, bool) {
	if len(s.items) == 0 {
		var zero T
		return zero, false
	}

	last := len(s.items) - 1
	v := s.items[last]

	// Zero the vacated slot before shrinking. Nothing will read past len and
	// nothing will clear it, so a Stack[*Request] would otherwise pin a
	// request for the stack's whole lifetime. Eight wasted bytes for an int;
	// a leak for anything holding pointers.
	var zero T
	s.items[last] = zero
	s.items = s.items[:last]

	return v, true
}

// Peek returns the top element without removing it.
func (s *Stack[T]) Peek() (T, bool) {
	if len(s.items) == 0 {
		var zero T
		return zero, false
	}
	return s.items[len(s.items)-1], true
}

// Slice returns the elements bottom to top, as a copy.
//
// A copy rather than s.items: handing out the backing array would let a caller
// mutate the stack through it, which is lesson 02's aliasing trap wearing a
// container.
func (s *Stack[T]) Slice() []T {
	return slices.Clone(s.items)
}

// String renders the stack bottom to top as "[10 20 30] <- top".
func (s *Stack[T]) String() string {
	if len(s.items) == 0 {
		return "(empty)"
	}

	parts := make([]string, 0, len(s.items))
	for _, v := range s.items {
		parts = append(parts, fmt.Sprint(v))
	}
	return "[" + strings.Join(parts, " ") + "] <- top"
}
