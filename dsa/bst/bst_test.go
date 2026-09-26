package bst

import (
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"testing"
)

func build(keys ...int) *Tree[int, string] {
	t := New[int, string]()
	for _, k := range keys {
		t.Put(k, fmt.Sprint("v", k))
	}
	return t
}

// The tree used throughout, chosen so it is balanced and every case below has
// somewhere to happen.
//
//	       50
//	     /    \
//	   30      70
//	  /  \    /  \
//	20   40  60   80
func sample() *Tree[int, string] { return build(50, 30, 70, 20, 40, 60, 80) }

func TestZeroValueIsUsable(t *testing.T) {
	var tr Tree[string, int]

	if tr.Len() != 0 || tr.Contains("a") || tr.Height() != 0 {
		t.Error("an empty tree should be empty")
	}
	if _, _, ok := tr.Min(); ok {
		t.Error("Min on an empty tree should report false")
	}

	tr.Put("a", 1)
	if got, ok := tr.Get("a"); !ok || got != 1 {
		t.Errorf(`Get("a") = %d, %v`, got, ok)
	}
}

func TestPutAndGet(t *testing.T) {
	tr := sample()

	if tr.Len() != 7 {
		t.Errorf("Len() = %d, want 7", tr.Len())
	}
	for _, k := range []int{20, 30, 40, 50, 60, 70, 80} {
		got, ok := tr.Get(k)
		if !ok || got != fmt.Sprint("v", k) {
			t.Errorf("Get(%d) = %q, %v", k, got, ok)
		}
	}
	for _, k := range []int{0, 25, 55, 99} {
		if _, ok := tr.Get(k); ok {
			t.Errorf("Get(%d) found something", k)
		}
	}
}

func TestPutReplaces(t *testing.T) {
	tr := sample()

	if tr.Put(50, "replaced") {
		t.Error("Put on an existing key reported the key was new")
	}
	if got, _ := tr.Get(50); got != "replaced" {
		t.Errorf("Get(50) = %q, want %q", got, "replaced")
	}
	if tr.Len() != 7 {
		t.Errorf("a replacement changed Len() to %d, want 7", tr.Len())
	}
}

func TestSortedOrder(t *testing.T) {
	tr := build(50, 30, 70, 20, 40, 60, 80, 10, 90)

	want := []int{10, 20, 30, 40, 50, 60, 70, 80, 90}
	if got := tr.Keys(); !slices.Equal(got, want) {
		t.Errorf("Keys() = %v, want %v", got, want)
	}
}

// TestSortedOrderFromRandomInput is the property worth asserting rather than a
// fixed expectation: however the keys arrive, they come out sorted.
func TestSortedOrderFromRandomInput(t *testing.T) {
	r := rand.New(rand.NewPCG(7, 11))

	for range 50 {
		keys := r.Perm(200)
		tr := build(keys...)

		got := tr.Keys()
		if !slices.IsSorted(got) {
			t.Fatalf("Keys() came out unsorted: %v", got[:20])
		}
		if len(got) != 200 {
			t.Fatalf("Keys() has %d entries, want 200", len(got))
		}
		if !tr.IsValid() {
			t.Fatal("the invariant broke")
		}
	}
}

func TestMinMax(t *testing.T) {
	tr := sample()

	if k, _, _ := tr.Min(); k != 20 {
		t.Errorf("Min() = %d, want 20", k)
	}
	if k, _, _ := tr.Max(); k != 80 {
		t.Errorf("Max() = %d, want 80", k)
	}
}

func TestFloorAndCeiling(t *testing.T) {
	tr := sample() // 20 30 40 50 60 70 80

	tests := []struct {
		key                  int
		floor, ceiling       int
		hasFloor, hasCeiling bool
	}{
		{50, 50, 50, true, true}, // exact
		{45, 40, 50, true, true}, // between
		{55, 50, 60, true, true}, // between
		{19, 0, 20, false, true}, // below everything
		{81, 80, 0, true, false}, // above everything
		{20, 20, 20, true, true}, // the minimum
		{80, 80, 80, true, true}, // the maximum
	}

	for _, tt := range tests {
		gotFloor, _, ok := tr.Floor(tt.key)
		if ok != tt.hasFloor || (ok && gotFloor != tt.floor) {
			t.Errorf("Floor(%d) = %d, %v; want %d, %v", tt.key, gotFloor, ok, tt.floor, tt.hasFloor)
		}

		gotCeil, _, ok := tr.Ceiling(tt.key)
		if ok != tt.hasCeiling || (ok && gotCeil != tt.ceiling) {
			t.Errorf("Ceiling(%d) = %d, %v; want %d, %v", tt.key, gotCeil, ok, tt.ceiling, tt.hasCeiling)
		}
	}
}

// TestDelete covers the three cases by name, because the two-child case is the
// only one with any content and it is easy to test the other two by accident.
func TestDelete(t *testing.T) {
	tests := []struct {
		name string
		key  int
		want []int
	}{
		{"a leaf", 20, []int{30, 40, 50, 60, 70, 80}},
		{"one child", 30, []int{40, 50, 60, 70, 80}}, // 20 is deleted first, leaving 30 with only a right child
		{"two children", 50, []int{20, 30, 40, 60, 70, 80}},
		{"the root with two children", 50, []int{20, 30, 40, 60, 70, 80}},
		{"absent", 99, []int{20, 30, 40, 50, 60, 70, 80}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := sample()

			if tt.name == "one child" {
				tr.Delete(20) // leaves 30 with only a right child
			}

			deleted := tr.Delete(tt.key)
			if want := tt.key != 99; deleted != want {
				t.Errorf("Delete(%d) = %v, want %v", tt.key, deleted, want)
			}

			if got := tr.Keys(); !slices.Equal(got, tt.want) {
				t.Errorf("after Delete(%d), Keys() = %v, want %v", tt.key, got, tt.want)
			}
			if !tr.IsValid() {
				t.Error("the invariant broke")
			}
			if tr.Len() != len(tt.want) {
				t.Errorf("Len() = %d, want %d", tr.Len(), len(tt.want))
			}
		})
	}
}

// TestDeleteEverythingInEveryOrder is where the real deletion bugs live. Deleting
// in one order and getting away with it says nothing.
func TestDeleteEverythingInEveryOrder(t *testing.T) {
	r := rand.New(rand.NewPCG(3, 5))

	for range 200 {
		keys := r.Perm(60)
		tr := build(keys...)

		order := r.Perm(60)
		remaining := slices.Clone(keys)
		slices.Sort(remaining)

		for _, k := range order {
			if !tr.Delete(k) {
				t.Fatalf("Delete(%d) reported the key was absent", k)
			}

			idx, _ := slices.BinarySearch(remaining, k)
			remaining = slices.Delete(remaining, idx, idx+1)

			if got := tr.Keys(); !slices.Equal(got, remaining) {
				t.Fatalf("after Delete(%d): %v, want %v", k, got, remaining)
			}
			if !tr.IsValid() {
				t.Fatalf("the invariant broke after Delete(%d)", k)
			}
		}

		if tr.Len() != 0 || tr.root != nil {
			t.Fatalf("the tree is not empty after deleting everything: Len()=%d", tr.Len())
		}
	}
}

func TestRange(t *testing.T) {
	tr := build(50, 30, 70, 20, 40, 60, 80, 10, 90)

	tests := []struct {
		lo, hi int
		want   []int
	}{
		{30, 60, []int{30, 40, 50, 60}},
		{35, 55, []int{40, 50}},
		{0, 100, []int{10, 20, 30, 40, 50, 60, 70, 80, 90}},
		{50, 50, []int{50}},
		{51, 59, nil},
		{100, 200, nil},
		{60, 30, nil}, // an inverted range is empty, not an error
	}

	for _, tt := range tests {
		var got []int
		for k := range tr.Range(tt.lo, tt.hi) {
			got = append(got, k)
		}
		if !slices.Equal(got, tt.want) {
			t.Errorf("Range(%d, %d) = %v, want %v", tt.lo, tt.hi, got, tt.want)
		}
	}
}

func TestAllStopsEarly(t *testing.T) {
	tr := build(50, 30, 70, 20, 40, 60, 80)

	var got []int
	for k := range tr.All() {
		got = append(got, k)
		if len(got) == 3 {
			break
		}
	}

	if want := []int{20, 30, 40}; !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestRangeStopsEarly(t *testing.T) {
	tr := build(50, 30, 70, 20, 40, 60, 80)

	count := 0
	for range tr.Range(20, 80) {
		count++
		if count == 2 {
			break
		}
	}

	if count != 2 {
		t.Errorf("visited %d keys after breaking at 2", count)
	}
}

func TestLevelOrder(t *testing.T) {
	tr := sample()

	var got []int
	for k := range tr.LevelOrder() {
		got = append(got, k)
	}

	// Breadth-first, so this is the tree's actual shape.
	if want := []int{50, 30, 70, 20, 40, 60, 80}; !slices.Equal(got, want) {
		t.Errorf("LevelOrder() = %v, want %v", got, want)
	}
}

// TestDegenerates is the point of the whole package. Sorted input produces a
// linked list, and nothing in the code notices.
func TestDegenerates(t *testing.T) {
	const n = 1000

	sortedInput := New[int, int]()
	for i := range n {
		sortedInput.Put(i, i)
	}

	shuffled := New[int, int]()
	r := rand.New(rand.NewPCG(1, 1))
	for _, k := range r.Perm(n) {
		shuffled.Put(k, k)
	}

	best := int(math.Log2(n)) + 1

	if got := sortedInput.Height(); got != n {
		t.Errorf("sorted input gave height %d, want exactly %d (a chain)", got, n)
	}
	if got := shuffled.Height(); got > 3*best {
		t.Errorf("shuffled input gave height %d, want under %d", got, 3*best)
	}

	t.Logf("n=%d: best possible height %d, shuffled %d, sorted %d",
		n, best, shuffled.Height(), sortedInput.Height())

	// Both are still correct trees. That is what makes the failure mode nasty:
	// nothing is wrong, everything is just slow.
	if !sortedInput.IsValid() || !shuffled.IsValid() {
		t.Error("a degenerate tree is still a valid tree")
	}
	if !slices.IsSorted(sortedInput.Keys()) {
		t.Error("a chain still has to iterate in order")
	}
}

// TestIsValidCatchesTheNaiveCheck: the "left child smaller, right child larger"
// version of IsValid passes this tree, and the tree is broken. 60 is in the
// root's LEFT subtree.
func TestIsValidCatchesTheNaiveCheck(t *testing.T) {
	tr := New[int, string]()
	tr.root = &node[int, string]{
		key: 50,
		left: &node[int, string]{
			key:   30,
			right: &node[int, string]{key: 60}, // larger than the root
		},
	}
	tr.count = 3

	if tr.IsValid() {
		t.Error("IsValid accepted a tree with 60 in the root's left subtree")
	}
}

// TestDeepTreeWorks: Put and All are iterative, so the only bound on depth is
// memory.
//
// n is 20,000 rather than something impressive because inserting sorted keys into
// a chain is O(n^2): each Put walks the whole chain to reach the end. At 20,000
// that is 2x10^8 pointer hops and takes about a third of a second, which is
// already the slowest test in the package. That cost IS the lesson.
func TestDeepTreeWorks(t *testing.T) {
	const n = 20_000

	tr := New[int, int]()
	for i := range n {
		tr.Put(i, i) // sorted, so this is a chain of 200,000 nodes
	}

	if got := tr.Height(); got != n {
		t.Fatalf("Height() = %d, want %d", got, n)
	}

	count := 0
	for range tr.All() {
		count++
	}
	if count != n {
		t.Errorf("All() visited %d of %d keys", count, n)
	}
}
