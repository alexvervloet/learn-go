package monotonicstack_test

import (
	"fmt"

	ms "github.com/alexvervloet/learn-go/dsa/patterns/monotonicstack"
)

// All four directions from one implementation. The stack's direction is the question's
// direction, and getting it backwards gives a plausible wrong answer rather than an error.
func ExampleNextGreater() {
	s := []int{2, 1, 2, 4, 3}

	fmt.Println("values:         ", s)
	fmt.Println("next greater:   ", ms.NextGreater(s))
	fmt.Println("next smaller:   ", ms.NextSmaller(s))
	fmt.Println("previous greater:", ms.PreviousGreater(s))
	fmt.Println("previous smaller:", ms.PreviousSmaller(s))

	// Output:
	// values:          [2 1 2 4 3]
	// next greater:    [3 2 3 -1 -1]
	// next smaller:    [1 -1 -1 4 -1]
	// previous greater: [-1 0 -1 -1 3]
	// previous smaller: [-1 -1 1 2 2]
}

// The strict and OrEqual variants differ by one character and only come apart on ties,
// which is why a problem's example is usually the only place the choice is stated.
func ExampleNextGreaterOrEqual() {
	equal := []int{7, 7, 7}

	fmt.Println("strict: ", ms.NextGreater(equal))
	fmt.Println("orEqual:", ms.NextGreaterOrEqual(equal))

	// Output:
	// strict:  [-1 -1 -1]
	// orEqual: [1 2 -1]
}

// NextGreater with the indices turned into distances. The conversion is where the off-by-one
// goes: the answer is next-i, and 0 rather than -1 when there is none.
func ExampleDaysUntilWarmer() {
	fmt.Println(ms.DaysUntilWarmer([]int{73, 74, 75, 71, 69, 72, 76, 73}))

	// Equal is not warmer.
	fmt.Println(ms.DaysUntilWarmer([]int{50, 50, 50}))

	// Output:
	// [1 1 4 2 1 1 0 0]
	// [0 0 0]
}

// The online form of the pattern, which is the one that turns up in production: prices
// arrive one at a time and the answer is needed immediately.
func ExampleSpanner() {
	var s ms.Spanner

	for _, price := range []int{100, 80, 60, 70, 60, 75, 85} {
		fmt.Printf("%d -> span %d\n", price, s.Next(price))
	}

	// Output:
	// 100 -> span 1
	// 80 -> span 1
	// 60 -> span 1
	// 70 -> span 2
	// 60 -> span 1
	// 75 -> span 4
	// 85 -> span 6
}

// The problem the pattern exists for. Every rectangle is limited by its shortest bar, so for
// each bar the question is how far it reaches before meeting something shorter, which is
// PreviousSmaller and NextSmaller.
func ExampleLargestRectangle() {
	area, at, width := ms.LargestRectangle([]int{2, 1, 5, 6, 2, 3})

	fmt.Printf("area %d, from bar %d of height 5, width %d\n", area, at, width)

	// Output:
	// area 10, from bar 2 of height 5, width 2
}

// Two solutions that share no code. The two-pointer version is the better answer here; the
// stack version generalises to problems the two pointers cannot handle.
func ExampleTrapWater() {
	heights := []int{0, 1, 0, 2, 1, 0, 1, 3, 2, 1, 2, 1}

	fmt.Println("monotonic stack:", ms.TrapWater(heights))
	fmt.Println("two pointers:   ", ms.TrapWaterTwoPointers(heights))

	// Output:
	// monotonic stack: 6
	// two pointers:    6
}

// A monotonic stack over characters. To make a number small, an earlier digit matters more
// than a later one, so a digit followed by a smaller one should go.
func ExampleRemoveDigits() {
	fmt.Println(ms.RemoveDigits("1432219", 3))

	// Leading zeros are stripped afterwards.
	fmt.Println(ms.RemoveDigits("10200", 1))

	// Already increasing, so the removals come off the END.
	fmt.Println(ms.RemoveDigits("12345", 2))

	// Everything removed leaves "0", not "".
	fmt.Println(ms.RemoveDigits("10", 2))

	// Output:
	// 1219
	// 200
	// 123
	// 0
}
