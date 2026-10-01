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
// module has them.

func ExampleLinkedList() {
	var l linkedlist.LinkedList[int]

	l.PushBack(20)
	l.PushBack(30)
	l.PushFront(10)

	fmt.Println(l.String())
	fmt.Println("length:", l.Len())

	// Output:
	// 10 -> 20 -> 30
	// length: 3
}

func ExampleLinkedList_PopFront() {
	var l linkedlist.LinkedList[string]
	l.PushBack("first")
	l.PushBack("second")

	v, ok := l.PopFront()
	fmt.Printf("%q, ok=%t\n", v, ok)
	fmt.Println("remaining:", l.String())

	// An empty list reports false rather than panicking.
	var empty linkedlist.LinkedList[string]
	_, ok = empty.PopFront()
	fmt.Println("empty list ok:", ok)

	// Output:
	// "first", ok=true
	// remaining: second
	// empty list ok: false
}

func ExampleLinkedList_All() {
	var l linkedlist.LinkedList[int]
	for _, v := range []int{1, 2, 3, 4, 5} {
		l.PushBack(v)
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
		l.PushBack(v)
	}

	l.Reverse()
	fmt.Println(l.String())

	// The tail pointer moved too, so appending still works.
	l.PushBack(0)
	fmt.Println(l.String())

	// Output:
	// 3 -> 2 -> 1
	// 3 -> 2 -> 1 -> 0
}
