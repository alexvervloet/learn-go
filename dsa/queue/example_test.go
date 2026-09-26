package queue_test

import (
	"fmt"

	"github.com/alexvervloet/learn-go/dsa/queue"
)

// Example functions are compiled, run, and checked against their Output
// comment, and they appear in `go doc`. A stale example fails the build, which
// is more than a README can offer.

func ExampleQueue() {
	var q queue.Queue[int] // the zero value is ready to use

	q.Push(10)
	q.Push(20)
	q.Push(30)

	// &q, not q. String has a pointer receiver, and the method set of a VALUE
	// does not include pointer-receiver methods, so fmt would not find it and
	// would print the struct fields instead. Nothing warns you: it compiles,
	// runs, and prints {[10 20 30 0] 0 3 3}.
	fmt.Println(&q)

	served, _ := q.Pop()
	fmt.Println("served:", served)
	fmt.Println(&q)

	// Output:
	// front -> [10 20 30] <- back
	// served: 10
	// front -> [20 30] <- back
}

// The comma-ok return is how an empty queue reports itself. There is no
// exception to catch and no sentinel value to remember.
func ExampleQueue_Pop() {
	var q queue.Queue[string]

	if _, ok := q.Pop(); !ok {
		fmt.Println("empty")
	}

	q.Push("first")
	v, ok := q.Pop()
	fmt.Println(v, ok)

	// Output:
	// empty
	// first true
}

// A queue holding more elements than its capacity keeps working: the buffer
// doubles, and the wrap is invisible from outside.
func ExampleQueue_Slice() {
	q := queue.New[int](2)
	for i := 1; i <= 5; i++ {
		q.Push(i)
	}
	q.Pop()
	q.Push(6)

	fmt.Println(q.Slice())

	// Output:
	// [2 3 4 5 6]
}

// Comparable embeds Queue and adds Remove, so a comparable element type does not
// have to supply an equality function. Every other method comes by promotion.
func ExampleComparable() {
	var q queue.Comparable[string]
	q.Push("ada")
	q.Push("bo")
	q.Push("cy")

	removed, ok := q.Remove("bo")
	fmt.Println(removed, ok)
	fmt.Println(q.Slice())

	// Output:
	// bo true
	// [ada cy]
}

// Matchmaking pairs the front player with the first compatible player behind
// them, which is why Cy is matched with Ada while Bo keeps waiting.
func ExampleMatchAll() {
	q := queue.New[queue.Player](4)
	for _, p := range []queue.Player{
		{Name: "Ada", Rank: "gold"},
		{Name: "Bo", Rank: "bronze"},
		{Name: "Cy", Rank: "gold"},
		{Name: "Di", Rank: "silver"},
	} {
		q.Push(p)
	}

	matches, waiting := queue.MatchAll(q)
	fmt.Println(queue.Describe(matches, waiting))

	// Output:
	// Ada(gold) vs Cy(gold)
	// waiting: Bo(bronze), Di(silver)
}
