// Package queue implements a first-in-first-out queue over a ring buffer.
//
// The interesting part is which of the three obvious implementations to pick,
// and the benchmarks in this package say something different from the textbook
// answer. `q = q[1:]` on pop is not quadratic and does not grow memory without
// bound: reslicing shrinks cap too, so append reallocates and the old array is
// collected. It measures the same nanosecond count as this ring buffer.
// `copy(q, q[1:])` is the genuinely bad one, at 20x slower in steady state and
// 83x on a drain.
//
// The ring buffer is here for the allocation column, not the time column: zero
// bytes of garbage per operation once it reaches its steady-state size, against
// a permanent 14 to 39 bytes for the reslice version. See README.md for the
// numbers.
package queue

import (
	"fmt"
	"strings"
)

// Queue is a FIFO queue. The zero value is an empty queue ready to use.
type Queue[T any] struct {
	items []T
	head  int // index of the front element
	tail  int // index where the next push goes
	count int
}

// New returns a queue with room for n elements reserved.
func New[T any](capacity int) *Queue[T] {
	if capacity < 1 {
		capacity = 1
	}
	return &Queue[T]{items: make([]T, capacity)}
}

// Len reports the number of elements.
func (q *Queue[T]) Len() int { return q.count }

// IsEmpty reports whether the queue has no elements.
func (q *Queue[T]) IsEmpty() bool { return q.count == 0 }

// Push adds v to the back.
func (q *Queue[T]) Push(v T) {
	if q.count == len(q.items) {
		q.grow()
	}

	q.items[q.tail] = v
	q.tail = (q.tail + 1) % len(q.items) // wrap around
	q.count++
}

// grow doubles the capacity and copies the elements into order, so head
// becomes 0 again. Called only when full, so the cost is amortised O(1) per
// push, exactly as with append.
func (q *Queue[T]) grow() {
	capacity := max(1, len(q.items)*2)
	bigger := make([]T, capacity)

	// Copy in logical order rather than memory order, which also un-wraps the
	// buffer. Two copies because the live elements may straddle the end.
	n := copy(bigger, q.items[q.head:])
	copy(bigger[n:], q.items[:q.tail])

	q.items = bigger
	q.head = 0
	q.tail = q.count
}

// Pop removes and returns the front element, reporting whether there was one.
func (q *Queue[T]) Pop() (T, bool) {
	if q.count == 0 {
		var zero T
		return zero, false
	}

	v := q.items[q.head]

	// Zero the vacated slot so a Queue[*Request] does not pin a request. Easy
	// to forget in a ring buffer, because the slot is not past len: it is in
	// the middle of a live slice and looks like it still belongs to someone.
	var zero T
	q.items[q.head] = zero

	q.head = (q.head + 1) % len(q.items)
	q.count--

	return v, true
}

// Peek returns the front element without removing it.
func (q *Queue[T]) Peek() (T, bool) {
	if q.count == 0 {
		var zero T
		return zero, false
	}
	return q.items[q.head], true
}

// Slice returns the elements front to back, as a copy.
func (q *Queue[T]) Slice() []T {
	out := make([]T, 0, q.count)
	for i := 0; i < q.count; i++ {
		out = append(out, q.items[(q.head+i)%len(q.items)])
	}
	return out
}

// String renders the queue front to back as "front -> [10 20 30] <- back".
func (q *Queue[T]) String() string {
	if q.count == 0 {
		return "(empty)"
	}

	parts := make([]string, 0, q.count)
	for _, v := range q.Slice() {
		parts = append(parts, fmt.Sprint(v))
	}
	return "front -> [" + strings.Join(parts, " ") + "] <- back"
}

// SearchAndRemove removes the first element equal to v, preserving the order of
// the rest, and reports whether it was found. O(n).
//
// equal is a parameter rather than a `comparable` constraint on T, because
// constraining the queue would forbid a Queue[[]byte] or a Queue[func()].
// Comparable below is the convenience wrapper for comparable types.
//
// This is not a queue operation, and it is what makes Matchmake possible: a
// player at the front with no compatible partner must be able to wait while a
// later pair is matched around them.
func (q *Queue[T]) SearchAndRemove(v T, equal func(a, b T) bool) (T, bool) {
	for i := 0; i < q.count; i++ {
		idx := (q.head + i) % len(q.items)

		if !equal(q.items[idx], v) {
			continue
		}
		found := q.items[idx]

		// Shift the elements after it forward by one, in logical order.
		for j := i; j < q.count-1; j++ {
			from := (q.head + j + 1) % len(q.items)
			to := (q.head + j) % len(q.items)
			q.items[to] = q.items[from]
		}

		// Zero the now-unused last slot and retreat the tail.
		last := (q.head + q.count - 1) % len(q.items)
		var zero T
		q.items[last] = zero

		q.tail = last
		q.count--

		return found, true
	}

	var zero T
	return zero, false
}

// Comparable is a Queue for comparable element types, so callers do not have to
// pass an equality function.
//
// Embedding rather than a type alias, so Comparable gets every Queue method for
// free and only Remove needs writing. See go-concepts/03 for how promotion
// works.
//
// Named Comparable rather than QueueOf because revive rejects queue.QueueOf as
// stuttering, and queue.Comparable[string] says what the constraint buys you.
type Comparable[T comparable] struct {
	Queue[T]
}

// Remove removes the first element equal to v.
func (q *Comparable[T]) Remove(v T) (T, bool) {
	return q.SearchAndRemove(v, func(a, b T) bool { return a == b })
}
