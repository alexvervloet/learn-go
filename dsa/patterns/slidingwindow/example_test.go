package slidingwindow_test

import (
	"fmt"

	"github.com/alexvervloet/learn-go/dsa/patterns/slidingwindow"
)

// A fixed window: add the element entering, subtract the one leaving. Two additions per
// step instead of k.
func ExampleMaxSumOfSize() {
	best, _ := slidingwindow.MaxSumOfSize([]int{2, 1, 5, 1, 3, 2}, 3)
	fmt.Println(best) // 5+1+3

	_, err := slidingwindow.MaxSumOfSize([]int{1, 2}, 5)
	fmt.Println(err)

	// Output:
	// 9
	// slidingwindow: window larger than the input
}

// The window count is len(nums)-k+1, which is the off-by-one to remember.
func ExampleAverageOfSize() {
	avgs, _ := slidingwindow.AverageOfSize([]int{1, 3, 2, 6, -1, 4, 1, 8, 2}, 5)
	fmt.Println(avgs)
	fmt.Println(len(avgs), "windows over", 9, "elements")

	// Output:
	// [2.2 2.8 2.4 3.6 2.8]
	// 5 windows over 9 elements
}

// The maximum of every window in O(n), using a deque of indices in decreasing order of
// value. An index whose value a later arrival beats is useless forever, because the later
// one is both larger and stays in the window longer.
func ExampleMaxOfEachWindow() {
	maxes, _ := slidingwindow.MaxOfEachWindow([]int{1, 3, -1, -3, 5, 3, 6, 7}, 3)
	fmt.Println(maxes)

	// Output:
	// [3 3 5 5 6 7]
}

// A variable window. On a repeat, jump left past the earlier copy rather than creeping.
// The `>= left` check is what stops a character seen before the window from moving left
// backwards.
func ExampleLongestUniqueSubstring() {
	for _, s := range []string{"abcabcbb", "bbbbb", "pwwkew", "abba", "naïve"} {
		fmt.Printf("%-9s %d\n", s, slidingwindow.LongestUniqueSubstring(s))
	}

	// Output:
	// abcabcbb  3
	// bbbbb     1
	// pwwkew    3
	// abba      2
	// naïve     5
}

// Grow right always, shrink left only while the window is invalid. Here "invalid" means
// more than k zeros inside.
func ExampleLongestOnesWithFlips() {
	nums := []int{1, 1, 1, 0, 0, 0, 1, 1, 1, 1, 0}

	for k := range 4 {
		fmt.Printf("k=%d -> %d\n", k, slidingwindow.LongestOnesWithFlips(nums, k))
	}

	// Output:
	// k=0 -> 4
	// k=1 -> 5
	// k=2 -> 6
	// k=3 -> 10
}

// The mirror image: the window is valid when it is big enough, so it shrinks while VALID
// and records the answer on the way.
func ExampleMinSubarrayAtLeast() {
	fmt.Println(slidingwindow.MinSubarrayAtLeast([]int{2, 1, 5, 2, 3, 2}, 7)) // 5+2
	fmt.Println(slidingwindow.MinSubarrayAtLeast([]int{2, 1, 5, 2, 8}, 7))    // 8 alone
	fmt.Println(slidingwindow.MinSubarrayAtLeast([]int{1, 1, 1}, 10))         // impossible

	// Output:
	// 2
	// 1
	// 0
}

// The hardest of the classic window problems. `missing` counts how many required
// characters are still short of their quota, so the validity check is O(1) rather than a
// map comparison per step.
func ExampleMinWindowContaining() {
	fmt.Printf("%q\n", slidingwindow.MinWindowContaining("ADOBECODEBANC", "ABC"))

	// A character needed twice must appear twice.
	fmt.Printf("%q\n", slidingwindow.MinWindowContaining("abbc", "bb"))
	fmt.Printf("%q\n", slidingwindow.MinWindowContaining("abc", "bb"))

	// Output:
	// "BANC"
	// "bb"
	// ""
}

// Where the pattern stops applying. With negatives allowed, growing the window does not
// monotonically increase the sum, so there is nothing to shrink towards. Prefix sums in a
// map are the answer instead.
func ExampleCountSubarraysWithSum() {
	fmt.Println(slidingwindow.CountSubarraysWithSum([]int{1, 1, 1}, 2))
	fmt.Println(slidingwindow.CountSubarraysWithSum([]int{1, -1, 0}, 0))
	fmt.Println(slidingwindow.CountSubarraysWithSum([]int{3, 4, 7, 2, -3, 1, 4, 2}, 7))

	// Output:
	// 2
	// 3
	// 4
}
