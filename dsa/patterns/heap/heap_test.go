package heap

import (
	"cmp"
	"math"
	"math/rand/v2"
	"slices"
	"testing"
)

func TestMinAndMaxHeap(t *testing.T) {
	values := []int{5, 2, 9, 1, 7, 3}

	minHeap := NewMin[int]()
	maxHeap := NewMax[int]()
	for _, v := range values {
		minHeap.Push(v)
		maxHeap.Push(v)
	}

	if got, _ := minHeap.Peek(); got != 1 {
		t.Errorf("min heap top = %d, want 1", got)
	}
	if got, _ := maxHeap.Peek(); got != 9 {
		t.Errorf("max heap top = %d, want 9", got)
	}

	if got := minHeap.Sorted(); !slices.Equal(got, []int{1, 2, 3, 5, 7, 9}) {
		t.Errorf("min heap drained = %v", got)
	}
	if got := maxHeap.Sorted(); !slices.Equal(got, []int{9, 7, 5, 3, 2, 1}) {
		t.Errorf("max heap drained = %v", got)
	}
}

func TestEmptyHeap(t *testing.T) {
	h := NewMin[string]()

	if h.Len() != 0 {
		t.Errorf("Len() = %d, want 0", h.Len())
	}
	if _, ok := h.Peek(); ok {
		t.Error("Peek on an empty heap reported a value")
	}
	if _, ok := h.Pop(); ok {
		t.Error("Pop on an empty heap reported a value")
	}
	if got := h.Sorted(); len(got) != 0 {
		t.Errorf("Sorted() on an empty heap = %v", got)
	}

	// PushPop on an empty heap returns its argument and reports false, meaning
	// nothing came out of the heap.
	v, fromHeap := h.PushPop("a")
	if v != "a" || fromHeap {
		t.Errorf("PushPop on an empty heap = %q, %v; want \"a\", false", v, fromHeap)
	}
}

// TestNewMaxDoesNotNegate is the reason NewMax swaps its arguments rather than
// negating the values.
//
// The tempting one-liner is `cmp.Compare(-a, -b)`. It is wrong for exactly one input:
// -math.MinInt overflows back to math.MinInt, so math.MinInt compares as if it were
// the smallest AND the largest value, and it ends up on top of a max-heap.
func TestNewMaxDoesNotNegate(t *testing.T) {
	values := []int{math.MinInt, 0, math.MaxInt, -1}

	// The version this package uses: swap the arguments.
	correct := NewMax[int]()
	for _, v := range values {
		correct.Push(v)
	}

	want := []int{math.MaxInt, 0, -1, math.MinInt}
	if got := correct.Sorted(); !slices.Equal(got, want) {
		t.Errorf("NewMax drained %v, want %v", got, want)
	}

	// The version that looks equivalent and is not.
	byNegatedValue := New(func(a, b int) int { return cmp.Compare(-a, -b) })
	for _, v := range values {
		byNegatedValue.Push(v)
	}

	top, _ := byNegatedValue.Peek()
	if top != math.MinInt {
		t.Errorf("negating the values put %d on top; expected it to be broken and put math.MinInt there", top)
	}
	t.Logf("negating the values puts %d on top of a max-heap, because -math.MinInt == math.MinInt", top)

	// Negating the RESULT is safe, because cmp.Compare only ever returns -1, 0 or 1.
	byNegatedResult := New(func(a, b int) int { return -cmp.Compare(a, b) })
	for _, v := range values {
		byNegatedResult.Push(v)
	}
	if got := byNegatedResult.Sorted(); !slices.Equal(got, want) {
		t.Errorf("negating the comparison result drained %v, want %v", got, want)
	}
}

// TestFromIsLinearAndCorrect: From must produce a valid heap, and it must produce the
// same drained order as pushing one at a time.
func TestFromIsLinearAndCorrect(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))

	for range 500 {
		n := r.IntN(60)
		items := make([]int, n)
		for i := range items {
			items[i] = r.IntN(100)
		}

		fromHeapify := From(slices.Clone(items), cmp.Compare[int])
		if !IsHeap(fromHeapify.items, cmp.Compare[int]) {
			t.Fatalf("From produced an invalid heap from %v", items)
		}

		pushed := NewMin[int]()
		for _, v := range items {
			pushed.Push(v)
		}

		a, b := fromHeapify.Sorted(), pushed.Sorted()
		if !slices.Equal(a, b) {
			t.Fatalf("heapify gave %v, pushing gave %v", a, b)
		}

		want := slices.Clone(items)
		slices.Sort(want)
		if !slices.Equal(a, want) {
			t.Fatalf("drained %v, want %v", a, want)
		}
	}
}

// TestFromTakesOwnership: From heapifies in place, so the caller's slice is
// rearranged. That is the point (it is what makes it allocation-free) and it is worth
// pinning down so nobody relies on the opposite.
func TestFromTakesOwnership(t *testing.T) {
	items := []int{5, 1, 3}
	From(items, cmp.Compare[int])

	if items[0] != 1 {
		t.Errorf("From did not heapify in place: %v", items)
	}
}

// TestHeapPropertyAfterEveryOperation: a heap that is valid at the end may have been
// invalid in the middle, and the next operation is what turns that into a wrong
// answer.
func TestHeapPropertyAfterEveryOperation(t *testing.T) {
	r := rand.New(rand.NewPCG(3, 4))
	h := NewMin[int]()

	var reference []int

	for step := range 2000 {
		switch r.IntN(3) {
		case 0, 1:
			v := r.IntN(1000)
			h.Push(v)
			reference = append(reference, v)

		case 2:
			got, ok := h.Pop()
			if len(reference) == 0 {
				if ok {
					t.Fatalf("step %d: Pop reported a value from an empty heap", step)
				}
				continue
			}
			if !ok {
				t.Fatalf("step %d: Pop reported empty with %d items", step, len(reference))
			}

			want := slices.Min(reference)
			if got != want {
				t.Fatalf("step %d: Pop = %d, want %d", step, got, want)
			}
			reference = slices.Delete(reference, slices.Index(reference, want), slices.Index(reference, want)+1)
		}

		if !IsHeap(h.items, cmp.Compare[int]) {
			t.Fatalf("step %d: heap property broken", step)
		}
		if h.Len() != len(reference) {
			t.Fatalf("step %d: Len() = %d, want %d", step, h.Len(), len(reference))
		}
	}
}

// TestPopZeroesTheVacatedSlot guards the same leak as the stack and the queue.
func TestPopZeroesTheVacatedSlot(t *testing.T) {
	h := New(func(a, b *int) int { return cmp.Compare(*a, *b) })

	one, two := 1, 2
	h.Push(&one)
	h.Push(&two)
	h.Pop()

	// Reach past len into the capacity the slice still owns.
	full := h.items[:2:2]
	if full[1] != nil {
		t.Error("Pop left the pointer in the backing array, pinning what it points at")
	}
}

func TestPushPopMatchesPushThenPop(t *testing.T) {
	r := rand.New(rand.NewPCG(5, 7))

	for range 2000 {
		n := 1 + r.IntN(20)
		items := make([]int, n)
		for i := range items {
			items[i] = r.IntN(50)
		}
		v := r.IntN(50)

		combined := From(slices.Clone(items), cmp.Compare[int])
		gotCombined, _ := combined.PushPop(v)

		separate := From(slices.Clone(items), cmp.Compare[int])
		separate.Push(v)
		gotSeparate, _ := separate.Pop()

		if gotCombined != gotSeparate {
			t.Fatalf("items=%v v=%d: PushPop = %d, Push+Pop = %d", items, v, gotCombined, gotSeparate)
		}
		if a, b := combined.Sorted(), separate.Sorted(); !slices.Equal(a, b) {
			t.Fatalf("items=%v v=%d: remainders differ, %v and %v", items, v, a, b)
		}
	}
}

// TestAllIsNotSorted: the documented warning, asserted. Only the top of a heap is
// ordered relative to everything else.
func TestAllIsNotSorted(t *testing.T) {
	h := NewMin[int]()
	for _, v := range []int{9, 8, 7, 6, 5, 4, 3, 2, 1} {
		h.Push(v)
	}

	got := slices.Collect(h.All())

	if slices.IsSorted(got) {
		t.Errorf("All() returned %v, which happens to be sorted; the test needs a better input", got)
	}
	if got[0] != 1 {
		t.Errorf("All() started with %d, want the top element 1", got[0])
	}

	// And the values are all there, just in the wrong order.
	sorted := slices.Clone(got)
	slices.Sort(sorted)
	if !slices.Equal(sorted, []int{1, 2, 3, 4, 5, 6, 7, 8, 9}) {
		t.Errorf("All() lost or duplicated items: %v", got)
	}
}

func TestAllStopsEarly(t *testing.T) {
	h := NewMin[int]()
	for i := range 10 {
		h.Push(i)
	}

	count := 0
	for range h.All() {
		count++
		if count == 3 {
			break
		}
	}

	if count != 3 {
		t.Errorf("visited %d items after breaking at 3", count)
	}
}

func TestSortedEmptiesTheHeap(t *testing.T) {
	h := NewMin[int]()
	for _, v := range []int{3, 1, 2} {
		h.Push(v)
	}

	h.Sorted()

	if h.Len() != 0 {
		t.Errorf("Len() = %d after Sorted(), want 0", h.Len())
	}
}
