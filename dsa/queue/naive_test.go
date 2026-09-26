package queue

import "testing"

// The two implementations this package exists to beat. They live in a test file
// because nothing should import them, and they are benchmarked against the ring
// buffer in bench_test.go.

// naiveReslice is the version everyone writes first: pop by moving the slice
// header forward.
type naiveReslice[T any] struct {
	items []T
}

func (q *naiveReslice[T]) Push(v T) { q.items = append(q.items, v) }

func (q *naiveReslice[T]) Pop() (T, bool) {
	if len(q.items) == 0 {
		var zero T
		return zero, false
	}
	v := q.items[0]
	q.items = q.items[1:] // O(1), and it also shrinks cap
	return v, true
}

func (q *naiveReslice[T]) Len() int { return len(q.items) }

// shiftDown is the version people write when they notice the first one
// reallocates: keep index 0 as the front and move everything else down.
type shiftDown[T any] struct {
	items []T
}

func (q *shiftDown[T]) Push(v T) { q.items = append(q.items, v) }

func (q *shiftDown[T]) Pop() (T, bool) {
	if len(q.items) == 0 {
		var zero T
		return zero, false
	}
	v := q.items[0]
	copy(q.items, q.items[1:]) // O(n) on every single pop
	q.items = q.items[:len(q.items)-1]
	return v, true
}

func (q *shiftDown[T]) Len() int { return len(q.items) }

// TestResliceRetainsPoppedValues is the one real memory bug in the reslice
// version, and the reason this package has a ring buffer.
//
// A slice keeps its whole backing array alive, not just the part it points at.
// After popping, the vacated slot is unreachable from any slice you hold, so you
// cannot clear it, and it still references whatever it held. Fill a queue to a
// million and drain it to one element and cap reads 1 while the million-element
// allocation is still live.
//
// Deterministic rather than a GC test: the assertion is on the array contents,
// which the spec does guarantee, instead of on whether a collection happened,
// which it does not.
func TestResliceRetainsPoppedValues(t *testing.T) {
	var q naiveReslice[*int]
	for i := range 4 {
		v := i
		q.Push(&v)
	}

	array := q.items // the only way to see the slots Pop leaves behind

	q.Pop()
	q.Pop()

	if len(q.items) != 2 {
		t.Fatalf("len after two pops = %d, want 2", len(q.items))
	}
	if array[0] == nil || array[1] == nil {
		t.Fatal("expected the popped pointers to still be in the backing array")
	}
	if *array[0] != 0 || *array[1] != 1 {
		t.Errorf("array[0:2] = %d, %d, want 0, 1", *array[0], *array[1])
	}

	// The ring buffer clears the slot it vacates, which is the whole difference.
	ring := New[*int](4)
	for i := range 4 {
		v := i
		ring.Push(&v)
	}
	ring.Pop()
	ring.Pop()

	if ring.items[0] != nil || ring.items[1] != nil {
		t.Error("the ring buffer left a popped pointer in the backing array")
	}
}
