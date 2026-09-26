package queue

import (
	"fmt"
	"slices"
	"testing"
)

func TestZeroValueIsUsable(t *testing.T) {
	var q Queue[int] // no New, no make

	if !q.IsEmpty() {
		t.Error("zero value should be empty")
	}
	if _, ok := q.Pop(); ok {
		t.Error("Pop on an empty queue should report false")
	}

	q.Push(1)
	if got, _ := q.Peek(); got != 1 {
		t.Errorf("Peek() = %d, want 1", got)
	}
}

func TestFIFOOrder(t *testing.T) {
	q := New[string](2) // deliberately too small, so it has to grow
	for _, s := range []string{"a", "b", "c", "d", "e"} {
		q.Push(s)
	}

	var got []string
	for {
		v, ok := q.Pop()
		if !ok {
			break
		}
		got = append(got, v)
	}

	want := []string{"a", "b", "c", "d", "e"}
	if !slices.Equal(got, want) {
		t.Errorf("drained %v, want %v", got, want)
	}
}

// TestRingBufferWraps is the test that matters for a ring buffer: the live
// elements straddle the end of the backing array, so head > tail. Every index
// calculation has to keep working.
func TestRingBufferWraps(t *testing.T) {
	q := New[int](4)
	for i := 1; i <= 4; i++ {
		q.Push(i) // exactly full: head=0, tail=0
	}
	if _, ok := q.Pop(); !ok { // 1 leaves
		t.Fatal("Pop failed")
	}
	if _, ok := q.Pop(); !ok { // 2 leaves
		t.Fatal("Pop failed")
	}
	q.Push(5) // wraps to index 0
	q.Push(6) // index 1

	// A full ring buffer has head == tail, so the indices alone do not tell you
	// it wrapped. What does: memory order no longer matches logical order.
	if q.head == 0 {
		t.Fatalf("expected head to have moved, got head=%d", q.head)
	}
	if got, want := q.items, []int{5, 6, 3, 4}; !slices.Equal(got, want) {
		t.Fatalf("backing array = %v, want %v (5 and 6 wrapped to the front)", got, want)
	}

	got := q.Slice()
	want := []int{3, 4, 5, 6}
	if !slices.Equal(got, want) {
		t.Errorf("wrapped queue = %v, want %v", got, want)
	}
}

// TestGrowUnwrapsTheBuffer checks the other half: growing a wrapped buffer has
// to copy in logical order, not memory order, or the elements come out rotated.
func TestGrowUnwrapsTheBuffer(t *testing.T) {
	q := New[int](4)
	for i := 1; i <= 4; i++ {
		q.Push(i)
	}
	q.Pop()
	q.Pop()
	q.Push(5)
	q.Push(6) // now full and wrapped

	q.Push(7) // forces grow() on a wrapped buffer

	if q.head != 0 {
		t.Errorf("after grow, head = %d, want 0", q.head)
	}

	got := q.Slice()
	want := []int{3, 4, 5, 6, 7}
	if !slices.Equal(got, want) {
		t.Errorf("after grow = %v, want %v", got, want)
	}
}

// TestPopZeroesTheVacatedSlot guards the leak. Unlike the stack's version the
// slot is not past len, so there is nothing to stop you reading a stale value
// and nothing to remind you to clear it.
func TestPopZeroesTheVacatedSlot(t *testing.T) {
	q := New[*int](4)
	v := 42
	q.Push(&v)
	q.Pop()

	if q.items[0] != nil {
		t.Error("Pop left the pointer in the backing array, pinning what it points at")
	}
}

func TestSearchAndRemovePreservesOrder(t *testing.T) {
	tests := []struct {
		name   string
		push   []int
		remove int
		found  bool
		want   []int
	}{
		{"middle", []int{1, 2, 3, 4}, 3, true, []int{1, 2, 4}},
		{"front", []int{1, 2, 3}, 1, true, []int{2, 3}},
		{"back", []int{1, 2, 3}, 3, true, []int{1, 2}},
		{"absent", []int{1, 2, 3}, 9, false, []int{1, 2, 3}},
		{"only element", []int{7}, 7, true, nil},
		{"empty", nil, 1, false, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var q Comparable[int]
			for _, v := range tt.push {
				q.Push(v)
			}

			_, found := q.Remove(tt.remove)
			if found != tt.found {
				t.Errorf("Remove(%d) found = %v, want %v", tt.remove, found, tt.found)
			}
			if got := q.Slice(); !slices.Equal(got, tt.want) {
				t.Errorf("after Remove(%d) = %v, want %v", tt.remove, got, tt.want)
			}
			if q.Len() != len(tt.want) {
				t.Errorf("Len() = %d, want %d", q.Len(), len(tt.want))
			}
		})
	}
}

// TestSearchAndRemoveOnAWrappedBuffer is the case the straightforward
// implementation gets wrong: the shift has to walk logical positions, not
// array indices.
func TestSearchAndRemoveOnAWrappedBuffer(t *testing.T) {
	var q Comparable[int]
	q.Queue = *New[int](4)
	for i := 1; i <= 4; i++ {
		q.Push(i)
	}
	q.Pop()
	q.Pop()
	q.Push(5)
	q.Push(6) // [3 4 5 6], wrapped

	if _, ok := q.Remove(5); !ok {
		t.Fatal("Remove(5) did not find it")
	}

	got := q.Slice()
	want := []int{3, 4, 6}
	if !slices.Equal(got, want) {
		t.Errorf("= %v, want %v", got, want)
	}

	// And the queue still works afterwards.
	q.Push(7)
	if got, want := q.Slice(), []int{3, 4, 6, 7}; !slices.Equal(got, want) {
		t.Errorf("after a push = %v, want %v", got, want)
	}
}

func TestPushFrontKeepsThePlace(t *testing.T) {
	q := New[int](2)
	q.Push(2)
	q.Push(3)
	q.pushFront(1) // full, so this grows too

	if got, want := q.Slice(), []int{1, 2, 3}; !slices.Equal(got, want) {
		t.Errorf("= %v, want %v", got, want)
	}
}

func TestStringRendersFrontToBack(t *testing.T) {
	var q Queue[int]
	if got, want := q.String(), "(empty)"; got != want {
		t.Errorf("empty String() = %q, want %q", got, want)
	}

	q.Push(10)
	q.Push(20)
	if got, want := q.String(), "front -> [10 20] <- back"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

// TestChurnDoesNotGrowTheBuffer is the point of the whole package. Push and pop
// a hundred thousand times with only a few elements in flight; the backing
// array must stay small. The naive `q = q[1:]` version fails this badly.
func TestChurnDoesNotGrowTheBuffer(t *testing.T) {
	q := New[int](8)

	for i := 0; i < 100_000; i++ {
		q.Push(i)
		if q.Len() > 4 {
			q.Pop()
		}
	}

	if len(q.items) > 8 {
		t.Errorf("backing array grew to %d over 100k operations, want <= 8", len(q.items))
	}
	if q.Len() != 4 {
		t.Errorf("Len() = %d, want 4", q.Len())
	}
}

// TestStringNeedsAPointer pins down a gotcha that costs people an afternoon.
// String has a pointer receiver, so the method set of a Queue VALUE does not
// include it, and fmt falls back to printing the struct fields. It compiles and
// runs; only the output is wrong.
func TestStringNeedsAPointer(t *testing.T) {
	var q Queue[int]
	q.Push(1)

	viaPointer := fmt.Sprint(&q)
	viaValue := fmt.Sprint(q) //nolint:govet // deliberately the wrong form, see above

	if viaPointer != "front -> [1] <- back" {
		t.Errorf("fmt.Sprint(&q) = %q", viaPointer)
	}
	if viaValue == viaPointer {
		t.Error("expected fmt.Sprint(q) to miss the String method and print fields")
	}
}
