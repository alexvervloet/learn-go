package redblack

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

func TestZeroValueIsUsable(t *testing.T) {
	var tr Tree[string, int]

	if tr.Len() != 0 || tr.Contains("a") || tr.Height() != 0 {
		t.Error("an empty tree should be empty")
	}

	tr.Put("a", 1)
	if got, ok := tr.Get("a"); !ok || got != 1 {
		t.Errorf(`Get("a") = %d, %v`, got, ok)
	}
	if !tr.IsValid() {
		t.Error("a one-node tree is invalid")
	}
}

func TestPutAndGet(t *testing.T) {
	tr := build(50, 30, 70, 20, 40, 60, 80)

	if tr.Len() != 7 {
		t.Errorf("Len() = %d, want 7", tr.Len())
	}
	for _, k := range []int{20, 30, 40, 50, 60, 70, 80} {
		if got, ok := tr.Get(k); !ok || got != fmt.Sprint("v", k) {
			t.Errorf("Get(%d) = %q, %v", k, got, ok)
		}
	}
	if _, ok := tr.Get(99); ok {
		t.Error("Get(99) found something")
	}
	if !tr.IsValid() {
		t.Error("the invariants broke")
	}
}

func TestPutReplaces(t *testing.T) {
	tr := build(1, 2, 3)

	if tr.Put(2, "replaced") {
		t.Error("Put on an existing key reported the key was new")
	}
	if got, _ := tr.Get(2); got != "replaced" {
		t.Errorf("Get(2) = %q", got)
	}
	if tr.Len() != 3 {
		t.Errorf("Len() = %d, want 3", tr.Len())
	}
}

// TestSortedInputStaysBalanced is why this package exists. The bst package gives
// a height of n for this input; a red-black tree gives roughly log2(n).
func TestSortedInputStaysBalanced(t *testing.T) {
	for _, n := range []int{100, 1_000, 10_000, 100_000} {
		tr := New[int, int]()
		for i := range n {
			tr.Put(i, i)
		}

		best := int(math.Log2(float64(n))) + 1
		limit := 2 * best // the bound the invariants guarantee

		if got := tr.Height(); got > limit {
			t.Errorf("n=%d: height %d, want at most %d", n, got, limit)
		}
		if !tr.IsValid() {
			t.Errorf("n=%d: the invariants broke", n)
		}

		t.Logf("n=%-7d height %-3d black height %-3d best possible %-3d unbalanced would be %d",
			n, tr.Height(), tr.BlackHeight(), best, n)
	}
}

// TestDescendingInputStaysBalanced: the mirror image, because a left-leaning tree
// is asymmetric and the two directions exercise different rotations.
func TestDescendingInputStaysBalanced(t *testing.T) {
	const n = 10_000

	tr := New[int, int]()
	for i := n - 1; i >= 0; i-- {
		tr.Put(i, i)
	}

	if limit := 2 * (int(math.Log2(n)) + 1); tr.Height() > limit {
		t.Errorf("height %d, want at most %d", tr.Height(), limit)
	}
	if !tr.IsValid() {
		t.Error("the invariants broke")
	}
	if !slices.IsSorted(tr.Keys()) {
		t.Error("keys came out unsorted")
	}
}

// TestInvariantsHoldAfterEveryInsert checks the three rules after every single
// Put, not just at the end. A tree that is valid at the end of a run may have
// been invalid in the middle, and the next operation is what turns that into a
// wrong answer.
func TestInvariantsHoldAfterEveryInsert(t *testing.T) {
	r := rand.New(rand.NewPCG(13, 17))

	for trial := range 30 {
		tr := New[int, int]()
		for i, k := range r.Perm(150) {
			tr.Put(k, k)

			if !tr.isOrdered() {
				t.Fatalf("trial %d, insert %d: ordering broke", trial, i)
			}
			if !tr.is23() {
				t.Fatalf("trial %d, insert %d: a red link leans right or two reds are in a row", trial, i)
			}
			if !tr.isBalanced() {
				t.Fatalf("trial %d, insert %d: black heights differ", trial, i)
			}
		}
	}
}

func TestInvariantsHoldAfterEveryDelete(t *testing.T) {
	r := rand.New(rand.NewPCG(19, 23))

	for trial := range 30 {
		const n = 120

		tr := New[int, int]()
		for _, k := range r.Perm(n) {
			tr.Put(k, k)
		}

		remaining := make([]int, n)
		for i := range remaining {
			remaining[i] = i
		}

		for i, k := range r.Perm(n) {
			if !tr.Delete(k) {
				t.Fatalf("trial %d: Delete(%d) reported the key was absent", trial, k)
			}

			idx, _ := slices.BinarySearch(remaining, k)
			remaining = slices.Delete(remaining, idx, idx+1)

			if !tr.IsValid() {
				t.Fatalf("trial %d, delete %d of %d (key %d): invariants broke", trial, i+1, n, k)
			}
			if got := tr.Keys(); !slices.Equal(got, remaining) {
				t.Fatalf("trial %d after Delete(%d): %v, want %v", trial, k, got, remaining)
			}
		}

		if tr.root != nil || tr.Len() != 0 {
			t.Fatalf("trial %d: the tree is not empty after deleting everything", trial)
		}
	}
}

// TestDeleteStaysBalanced: deletion can unbalance a tree as easily as insertion,
// and always taking the successor is the classic way to skew one slowly.
func TestDeleteStaysBalanced(t *testing.T) {
	const n = 20_000

	tr := New[int, int]()
	for i := range n {
		tr.Put(i, i)
	}

	// Delete the first half in order, the worst pattern for a tree that always
	// promotes the successor.
	for i := range n / 2 {
		tr.Delete(i)
	}

	best := int(math.Log2(float64(n/2))) + 1
	if got := tr.Height(); got > 2*best {
		t.Errorf("after deleting half the keys, height %d, want at most %d", got, 2*best)
	}
	if !tr.IsValid() {
		t.Error("the invariants broke")
	}
	if tr.Len() != n/2 {
		t.Errorf("Len() = %d, want %d", tr.Len(), n/2)
	}

	t.Logf("height %d after deleting %d of %d keys in order", tr.Height(), n/2, n)
}

func TestDeleteAbsent(t *testing.T) {
	tr := build(1, 2, 3)

	if tr.Delete(99) {
		t.Error("Delete(99) reported it removed something")
	}
	if tr.Len() != 3 {
		t.Errorf("Len() = %d, want 3", tr.Len())
	}
	if !tr.IsValid() {
		t.Error("a failed delete broke the tree")
	}
}

func TestDeleteMinAndMax(t *testing.T) {
	r := rand.New(rand.NewPCG(29, 31))

	tr := New[int, int]()
	for _, k := range r.Perm(500) {
		tr.Put(k, k)
	}

	for i := range 200 {
		if !tr.DeleteMin() {
			t.Fatalf("DeleteMin failed at %d", i)
		}
		if k, _, _ := tr.Min(); k != i+1 {
			t.Fatalf("after %d DeleteMin calls, Min() = %d, want %d", i+1, k, i+1)
		}
		if !tr.IsValid() {
			t.Fatalf("DeleteMin %d broke the invariants", i)
		}
	}

	for i := range 200 {
		if !tr.DeleteMax() {
			t.Fatalf("DeleteMax failed at %d", i)
		}
		if k, _, _ := tr.Max(); k != 499-i-1 {
			t.Fatalf("after %d DeleteMax calls, Max() = %d, want %d", i+1, k, 499-i-1)
		}
		if !tr.IsValid() {
			t.Fatalf("DeleteMax %d broke the invariants", i)
		}
	}

	if tr.Len() != 100 {
		t.Errorf("Len() = %d, want 100", tr.Len())
	}
}

func TestDeleteMinOnEmpty(t *testing.T) {
	var tr Tree[int, int]

	if tr.DeleteMin() || tr.DeleteMax() {
		t.Error("deleting from an empty tree reported success")
	}
	if tr.Len() != 0 {
		t.Errorf("Len() = %d, want 0", tr.Len())
	}
}

func TestFloorAndCeiling(t *testing.T) {
	tr := build(20, 30, 40, 50, 60, 70, 80)

	tests := []struct {
		key                  int
		floor, ceiling       int
		hasFloor, hasCeiling bool
	}{
		{50, 50, 50, true, true},
		{45, 40, 50, true, true},
		{19, 0, 20, false, true},
		{81, 80, 0, true, false},
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

func TestRange(t *testing.T) {
	tr := New[int, int]()
	for i := range 100 {
		tr.Put(i, i*i)
	}

	var got []int
	for k := range tr.Range(10, 14) {
		got = append(got, k)
	}
	if want := []int{10, 11, 12, 13, 14}; !slices.Equal(got, want) {
		t.Errorf("Range(10, 14) = %v, want %v", got, want)
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

// TestIsValidCatchesBrokenColours is the test for the test. IsValid has to reject
// a tree that is correctly ORDERED but whose colours are wrong, because such a
// tree answers every query correctly while being an unbalanced tree wearing the
// name.
func TestIsValidCatchesBrokenColours(t *testing.T) {
	// A valid tree, then break only the colours.
	tr := build(10, 20, 30, 40, 50)
	if !tr.IsValid() {
		t.Fatal("the starting tree is already invalid")
	}

	t.Run("red link leaning right", func(t *testing.T) {
		tr := build(10, 20, 30, 40, 50)
		// Find any node with a right child and paint the link red.
		var target *node[int, string]
		for n := tr.root; n != nil; n = n.left {
			if n.right != nil {
				target = n
				break
			}
		}
		if target == nil {
			t.Skip("no right child in this shape")
		}

		target.right.color = red

		if tr.is23() {
			t.Error("is23 accepted a red link leaning right")
		}
		if tr.isOrdered() != true {
			t.Error("the ordering should be untouched, which is the point")
		}
	})

	t.Run("black heights differ", func(t *testing.T) {
		tr := build(10, 20, 30, 40, 50)

		// Paint one black left link red, which shortens exactly one path's black
		// count while leaving every key where it was.
		for n := tr.root; n != nil; n = n.left {
			if n.left != nil && !isRed(n.left) {
				n.left.color = red
				break
			}
		}

		if tr.isBalanced() && tr.is23() {
			t.Error("the checks accepted a tree with unequal black heights")
		}
		if !tr.isOrdered() {
			t.Error("the ordering should be untouched")
		}
	})
}

// TestNoRedRightLinks is the left-leaning property itself, stated once.
func TestNoRedRightLinks(t *testing.T) {
	r := rand.New(rand.NewPCG(37, 41))

	tr := New[int, int]()
	for _, k := range r.Perm(2000) {
		tr.Put(k, k)
	}

	var check func(n *node[int, int])
	found := 0
	check = func(n *node[int, int]) {
		if n == nil {
			return
		}
		if isRed(n.right) {
			found++
		}
		check(n.left)
		check(n.right)
	}
	check(tr.root)

	if found != 0 {
		t.Errorf("found %d red right links, want 0", found)
	}
}
