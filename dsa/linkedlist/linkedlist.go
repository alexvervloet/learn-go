// Package linkedlist implements a singly linked list with head and tail
// pointers.
//
// The tail pointer is what makes AddToTail O(1). Without it, appending means
// walking the chain to find the end, which is the difference between a usable
// queue and an accidentally quadratic one.
//
// In Go a slice beats a linked list for almost everything: contiguous memory,
// a working prefetcher, and amortised O(1) append. The list wins only on
// prepend and remove-from-front, both O(1) here against O(n) for a slice.
package linkedlist

import (
	"fmt"
	"iter"
	"strings"
)

// node is one element and its link. Unexported: callers deal in values, and
// handing out nodes would let them corrupt the chain.
type node[T any] struct {
	value T
	next  *node[T]
}

// LinkedList is a singly linked list. The zero value is an empty list ready to
// use, so no constructor is needed.
type LinkedList[T any] struct {
	head *node[T]
	tail *node[T]
	size int
}

// Len reports the number of elements. Maintained incrementally rather than
// counted, so it is O(1).
func (l *LinkedList[T]) Len() int { return l.size }

// IsEmpty reports whether the list has no elements.
func (l *LinkedList[T]) IsEmpty() bool { return l.size == 0 }

// AddToHead inserts v at the front, in O(1).
func (l *LinkedList[T]) AddToHead(v T) {
	n := &node[T]{value: v, next: l.head}

	l.head = n
	if l.tail == nil {
		// The first element is both head and tail.
		l.tail = n
	}
	l.size++
}

// AddToTail appends v, in O(1) thanks to the tail pointer.
func (l *LinkedList[T]) AddToTail(v T) {
	n := &node[T]{value: v}

	if l.tail == nil {
		l.head, l.tail = n, n
		l.size++
		return
	}

	l.tail.next = n
	l.tail = n
	l.size++
}

// RemoveFromHead removes and returns the front element, reporting whether
// there was one. O(1).
//
// The comma-ok result rather than a zero value alone: T may have a meaningful
// zero, so "empty" and "held the zero value" must be distinguishable. Same
// reasoning as a map lookup.
func (l *LinkedList[T]) RemoveFromHead() (T, bool) {
	if l.head == nil {
		var zero T
		return zero, false
	}

	removed := l.head
	l.head = removed.next

	if l.head == nil {
		l.tail = nil // the list is now empty
	}
	l.size--

	// Clear the removed node's link. Without this, a caller holding the node
	// (or a profiler walking the heap) keeps the whole rest of the chain
	// reachable, so removing one element frees nothing. The same trap as
	// deleting from a slice without zeroing the vacated slot.
	removed.next = nil

	return removed.value, true
}

// RemoveFromTail removes and returns the back element, reporting whether there
// was one.
//
// O(n), and deliberately so. Removing the last node needs the one before it,
// and a singly linked list cannot walk backwards. Making the list doubly linked
// fixes it at the cost of a second pointer per node; the asymmetry between this
// and AddToTail is worth feeling rather than reading about.
func (l *LinkedList[T]) RemoveFromTail() (T, bool) {
	if l.head == nil {
		var zero T
		return zero, false
	}

	// One element: head and tail are the same node.
	if l.head == l.tail {
		removed := l.head
		l.head, l.tail = nil, nil
		l.size--
		return removed.value, true
	}

	// Walk to the second-to-last node. This is the O(n) part.
	prev := l.head
	for prev.next != l.tail {
		prev = prev.next
	}

	removed := l.tail
	prev.next = nil
	l.tail = prev
	l.size--

	return removed.value, true
}

// Head returns the front value without removing it.
func (l *LinkedList[T]) Head() (T, bool) {
	if l.head == nil {
		var zero T
		return zero, false
	}
	return l.head.value, true
}

// Tail returns the back value without removing it. O(1), because of the tail
// pointer: reading the last element is cheap even though removing it is not.
func (l *LinkedList[T]) Tail() (T, bool) {
	if l.tail == nil {
		var zero T
		return zero, false
	}
	return l.tail.value, true
}

// All returns an iterator over the values, front to back.
//
// iter.Seq (Go 1.23) rather than returning a slice: a caller that only wants
// the first match should not pay for a copy of the whole list. The yield
// contract is honoured, so `break` stops the walk. See
// go-concepts/11-generics for what happens when it is not.
func (l *LinkedList[T]) All() iter.Seq[T] {
	return func(yield func(T) bool) {
		for n := l.head; n != nil; n = n.next {
			if !yield(n.value) {
				return
			}
		}
	}
}

// Slice returns the values as a slice, for when a caller genuinely wants one.
func (l *LinkedList[T]) Slice() []T {
	out := make([]T, 0, l.size)
	for n := l.head; n != nil; n = n.next {
		out = append(out, n.value)
	}
	return out
}

// String renders the list as "10 -> 20 -> 30", making LinkedList a
// fmt.Stringer so it prints usefully from %v and a log line.
func (l *LinkedList[T]) String() string {
	if l.head == nil {
		return "(empty)"
	}

	parts := make([]string, 0, l.size)
	for n := l.head; n != nil; n = n.next {
		parts = append(parts, fmt.Sprint(n.value))
	}
	return strings.Join(parts, " -> ")
}

// Reverse reverses the list in place, in O(n) with O(1) extra space.
//
// The classic interview question, and the reason it is classic: it needs three
// pointers moving in lockstep and there is nowhere to hide a mistake.
func (l *LinkedList[T]) Reverse() {
	var prev *node[T]
	current := l.head

	l.tail = l.head // the old head becomes the new tail

	for current != nil {
		next := current.next // save it before overwriting
		current.next = prev  // flip the link
		prev = current       // advance prev
		current = next       // advance current
	}

	l.head = prev
}
