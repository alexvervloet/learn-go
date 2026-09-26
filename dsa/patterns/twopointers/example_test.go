package twopointers_test

import (
	"fmt"
	"slices"

	"github.com/alexvervloet/learn-go/dsa/patterns/twopointers"
)

// Converging pointers on a sorted slice. A sum that is too small rules out the current
// left with every remaining right, so one comparison eliminates a whole row.
func ExampleTwoSumInts() {
	sorted := []int{1, 3, 4, 6, 8, 11}

	i, j, ok := twopointers.TwoSumInts(sorted, 7)
	fmt.Println(i, j, ok, "->", sorted[i], "+", sorted[j])

	_, _, ok = twopointers.TwoSumInts(sorted, 100)
	fmt.Println(ok)

	// Output:
	// 0 3 true -> 1 + 6
	// false
}

// For UNSORTED input a hash map is O(n) and two pointers would need a sort first. The map
// also keeps the original indices, which a sort would destroy.
func ExampleTwoSumUnsorted() {
	nums := []int{3, 9, 1, 5}

	i, j, _ := twopointers.TwoSumUnsorted(nums, 6)
	fmt.Println(i, j, "->", nums[i], "+", nums[j])

	// A single element must not pair with itself, which is why the lookup happens
	// before the insert.
	_, _, ok := twopointers.TwoSumUnsorted([]int{4}, 8)
	fmt.Println(ok)

	// Output:
	// 2 3 -> 1 + 5
	// false
}

// Fix one element, then two pointers over the rest: O(n²) instead of O(n³). The duplicate
// skipping in three places is what keeps the triples distinct.
func ExampleThreeSum() {
	for _, triple := range twopointers.ThreeSum([]int{-1, 0, 1, 2, -1, -4}) {
		fmt.Println(triple)
	}

	// Output:
	// [-1 -1 2]
	// [-1 0 1]
}

// Always move the shorter wall. Moving the taller one can never help, because the area is
// capped by the shorter one, so a narrower window with the same cap is never better.
func ExampleMaxWaterContainer() {
	fmt.Println(twopointers.MaxWaterContainer([]int{1, 8, 6, 2, 5, 4, 8, 3, 7}))
	fmt.Println(twopointers.MaxWaterContainer([]int{4, 3, 2, 1, 4}))

	// Output:
	// 49
	// 16
}

func ExampleIsPalindrome() {
	for _, s := range []string{
		"A man, a plan, a canal: Panama",
		"race a car",
		"No 'x' in Nixon",
		"0P",
	} {
		fmt.Printf("%-31q %v\n", s, twopointers.IsPalindrome(s))
	}

	// Output:
	// "A man, a plan, a canal: Panama" true
	// "race a car"                    false
	// "No 'x' in Nixon"               true
	// "0P"                            false
}

// A read pointer and a write pointer moving the same way. This is what every in-place
// filter looks like, and why they need no extra memory.
func ExampleDedupe() {
	s := []int{0, 0, 1, 1, 1, 2, 2, 3, 3, 4}

	n := twopointers.Dedupe(s)
	fmt.Println(s[:n], "kept", n)

	// Only CONSECUTIVE duplicates go.
	t := []int{1, 2, 1, 2}
	fmt.Println(t[:twopointers.Dedupe(t)])

	// Output:
	// [0 1 2 3 4] kept 5
	// [1 2 1 2]
}

// The generalisation collapses to one comparison: an element may be written if it differs
// from the one `limit` positions back in the OUTPUT.
func ExampleDedupeAllowing() {
	s := []int{0, 0, 1, 1, 1, 1, 2, 3, 3}

	n := twopointers.DedupeAllowing(s, 2)
	fmt.Println(s[:n])

	// Output:
	// [0 0 1 1 2 3 3]
}

// The tail is filled rather than swapped as it goes: a million zeros followed by one
// non-zero costs one write here and a million swaps the other way.
func ExampleMoveZerosToEnd() {
	s := []int{0, 1, 0, 3, 12}
	twopointers.MoveZerosToEnd(s)
	fmt.Println(s)

	// Output:
	// [1 3 12 0 0]
}

// The kept group stays in order. The rejected group does not, because each swap throws a
// rejected element to wherever the write pointer was.
func ExamplePartitionByPredicate() {
	s := []int{1, 2, 3, 4, 5, 6, 7, 8}

	n := twopointers.PartitionByPredicate(s, func(v int) bool { return v%2 == 0 })
	fmt.Println("even:", s[:n], "(in order)")
	fmt.Println("odd: ", s[n:], "(scrambled)")

	// Output:
	// even: [2 4 6 8] (in order)
	// odd:  [5 3 7 1] (scrambled)
}

// The trap: the input may hold negatives, so the largest square is at one END, not in the
// middle. Filling the output from the BACK is what makes it one pass.
func ExampleSquaresOfSorted() {
	fmt.Println(twopointers.SquaresOfSorted([]int{-4, -1, 0, 3, 10}))

	// Squaring and re-sorting gives the same answer at O(n log n).
	naive := []int{16, 1, 0, 9, 100}
	slices.Sort(naive)
	fmt.Println(naive)

	// Output:
	// [0 1 9 16 100]
	// [0 1 9 16 100]
}
