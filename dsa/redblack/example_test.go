package redblack_test

import (
	"fmt"

	"github.com/alexvervloet/learn-go/dsa/bst"
	"github.com/alexvervloet/learn-go/dsa/redblack"
)

func ExampleTree() {
	var t redblack.Tree[int, string] // the zero value is ready to use

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

// The whole point of the package, in one comparison. Both trees get the same keys
// in the same order, and both answer every query correctly.
func ExampleTree_Height() {
	const n = 1000

	balanced := redblack.New[int, int]()
	unbalanced := bst.New[int, int]()

	for i := range n {
		balanced.Put(i, i)
		unbalanced.Put(i, i)
	}

	fmt.Printf("%d sorted keys\n", n)
	fmt.Println("red-black height:", balanced.Height())
	fmt.Println("plain BST height:", unbalanced.Height())

	// Output:
	// 1000 sorted keys
	// red-black height: 10
	// plain BST height: 1000
}

// BlackHeight is the number the invariants actually hold constant. Height counts
// the red links too, and can be up to twice as large.
func ExampleTree_BlackHeight() {
	t := redblack.New[int, int]()
	for i := range 1000 {
		t.Put(i, i)
	}

	fmt.Println("height:      ", t.Height())
	fmt.Println("black height:", t.BlackHeight())

	// Output:
	// height:       10
	// black height: 9
}

// Deletion keeps the invariants, which is the part that takes forty lines rather
// than four. IsValid checks all three rules, not just the ordering.
func ExampleTree_Delete() {
	t := redblack.New[int, int]()
	for i := range 100 {
		t.Put(i, i)
	}

	for i := range 50 {
		t.Delete(i)
	}

	fmt.Println("len:", t.Len(), "height:", t.Height())
	fmt.Println("valid:", t.IsValid())

	min, _, _ := t.Min()
	max, _, _ := t.Max()
	fmt.Println("range:", min, "to", max)

	// Output:
	// len: 50 height: 8
	// valid: true
	// range: 50 to 99
}

// Everything the bst package offers is still here, because a red-black tree is a
// search tree with bookkeeping and nothing else.
func ExampleTree_Range() {
	t := redblack.New[int, string]()
	for _, hour := range []int{0, 6, 12, 18} {
		t.Put(hour, fmt.Sprintf("%02d:00", hour))
	}

	for h, label := range t.Range(6, 12) {
		fmt.Println(h, label)
	}

	before, label, _ := t.Floor(14)
	fmt.Println("floor of 14:", before, label)

	// Output:
	// 6 06:00
	// 12 12:00
	// floor of 14: 12 12:00
}
