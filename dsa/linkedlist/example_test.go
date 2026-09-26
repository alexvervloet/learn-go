package linkedlist_test

import (
	"fmt"

	"github.com/alexvervloet/learn-go/dsa/linkedlist"
)

// Example functions are compiled, run, and their output compared against the
// "Output:" comment. So they are tests AND documentation, and they cannot rot:
// change the behaviour and the example fails.
//
// They appear in `go doc` and on pkg.go.dev, which is why every package in this
// module has them. The Python repo's docstrings cannot be checked this way.

func ExampleLinkedList() {
	var l linkedlist.LinkedList[int]

	l.AddToTail(20)
	l.AddToTail(30)
	l.AddToHead(10)

	fmt.Println(l.String())
	fmt.Println("length:", l.Len())

	// Output:
	// 10 -> 20 -> 30
	// length: 3
}

func ExampleLinkedList_RemoveFromHead() {
	var l linkedlist.LinkedList[string]
	l.AddToTail("first")
	l.AddToTail("second")

	v, ok := l.RemoveFromHead()
	fmt.Printf("%q, ok=%t\n", v, ok)
	fmt.Println("remaining:", l.String())

	// An empty list reports false rather than panicking.
	var empty linkedlist.LinkedList[string]
	_, ok = empty.RemoveFromHead()
	fmt.Println("empty list ok:", ok)

	// Output:
	// "first", ok=true
	// remaining: second
	// empty list ok: false
}

func ExampleLinkedList_All() {
	var l linkedlist.LinkedList[int]
	for _, v := range []int{1, 2, 3, 4, 5} {
		l.AddToTail(v)
	}

	// range over an iter.Seq, and break works.
	for v := range l.All() {
		if v > 3 {
			break
		}
		fmt.Print(v, " ")
	}
	fmt.Println()

	// Output:
	// 1 2 3
}

func ExampleLinkedList_Reverse() {
	var l linkedlist.LinkedList[int]
	for _, v := range []int{1, 2, 3} {
		l.AddToTail(v)
	}

	l.Reverse()
	fmt.Println(l.String())

	// The tail pointer moved too, so appending still works.
	l.AddToTail(0)
	fmt.Println(l.String())

	// Output:
	// 3 -> 2 -> 1
	// 3 -> 2 -> 1 -> 0
}
