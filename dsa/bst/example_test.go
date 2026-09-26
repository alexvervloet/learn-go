package bst_test

import (
	"fmt"

	"github.com/alexvervloet/learn-go/dsa/bst"
)

func ExampleTree() {
	var t bst.Tree[int, string] // the zero value is ready to use

	for _, k := range []int{50, 30, 70, 20, 40, 60, 80} {
		t.Put(k, fmt.Sprint("v", k))
	}

	fmt.Println(t.Keys())
	fmt.Println("len:", t.Len(), "height:", t.Height())

	v, ok := t.Get(40)
	fmt.Println(v, ok)

	// Output:
	// [20 30 40 50 60 70 80]
	// len: 7 height: 3
	// v40 true
}

// Sorted iteration in O(n) with no comparisons: the ordering work was done at
// insertion time. This is what a hash map cannot do.
func ExampleTree_All() {
	t := bst.New[string, int]()
	for i, name := range []string{"delta", "alpha", "charlie", "bravo"} {
		t.Put(name, i)
	}

	for name, i := range t.All() {
		fmt.Println(name, i)
	}

	// Output:
	// alpha 1
	// bravo 3
	// charlie 2
	// delta 0
}

// Floor and Ceiling answer "the nearest key at or below / at or above this one",
// which is the query behind every time-series lookup.
func ExampleTree_Floor() {
	t := bst.New[int, string]()
	for _, minute := range []int{0, 15, 30, 45} {
		t.Put(minute, fmt.Sprintf("reading at %02d", minute))
	}

	for _, at := range []int{30, 37, 59, 0} {
		reading, _, _ := t.Floor(at)
		fmt.Printf("at %02d the latest reading is from %02d\n", at, reading)
	}

	_, _, ok := t.Floor(-1)
	fmt.Println("before the first reading:", ok)

	// Output:
	// at 30 the latest reading is from 30
	// at 37 the latest reading is from 30
	// at 59 the latest reading is from 45
	// at 00 the latest reading is from 00
	// before the first reading: false
}

// Range prunes: a subtree entirely outside [lo, hi] is never entered, so the
// cost is the height plus the number of results.
func ExampleTree_Range() {
	t := bst.New[int, int]()
	for i := range 100 {
		t.Put(i, i*i)
	}

	for k, v := range t.Range(10, 14) {
		fmt.Println(k, v)
	}

	// Output:
	// 10 100
	// 11 121
	// 12 144
	// 13 169
	// 14 196
}

// Deleting a node with two children replaces it with its in-order successor, the
// smallest key in its right subtree. That is the only key that can take its place
// without breaking the invariant.
func ExampleTree_Delete() {
	t := bst.New[int, string]()
	for _, k := range []int{50, 30, 70, 60, 80} {
		t.Put(k, "")
	}

	fmt.Println("before:", t.Keys())

	t.Delete(70) // two children: 60 and 80
	fmt.Println("after: ", t.Keys())
	fmt.Println("valid: ", t.IsValid())

	// Output:
	// before: [30 50 60 70 80]
	// after:  [30 50 60 80]
	// valid:  true
}

// Height is the number that says whether the tree is doing its job, and insertion
// order decides it. Nothing here rebalances, so sorted input gives a linked list
// that is still a perfectly valid search tree.
func ExampleTree_Height() {
	balanced := bst.New[int, int]()
	for _, k := range []int{4, 2, 6, 1, 3, 5, 7} {
		balanced.Put(k, k)
	}

	sorted := bst.New[int, int]()
	for k := 1; k <= 7; k++ {
		sorted.Put(k, k)
	}

	fmt.Println("balanced:", balanced.Height(), balanced.Keys())
	fmt.Println("sorted:  ", sorted.Height(), sorted.Keys())
	fmt.Println("both valid:", balanced.IsValid(), sorted.IsValid())

	// Output:
	// balanced: 3 [1 2 3 4 5 6 7]
	// sorted:   7 [1 2 3 4 5 6 7]
	// both valid: true true
}

// LevelOrder is breadth-first, so it shows the shape rather than the order.
func ExampleTree_LevelOrder() {
	t := bst.New[int, int]()
	for _, k := range []int{50, 30, 70, 20, 40} {
		t.Put(k, k)
	}

	for k := range t.LevelOrder() {
		fmt.Print(k, " ")
	}
	fmt.Println()

	// Output:
	// 50 30 70 20 40
}
