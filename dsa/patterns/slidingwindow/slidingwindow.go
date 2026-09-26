// Package slidingwindow implements the sliding-window pattern.
//
// # The tell
//
// The word is CONTIGUOUS. "The longest substring with no repeated characters" is a
// window. "The longest subsequence with no repeats" is not, because a subsequence may
// skip elements and a window may not. Anything asking for the best, longest, shortest
// or count of contiguous runs is this pattern.
//
// # The shape
//
// A window [left, right] moves forward over the sequence carrying running state, so
// every subarray question collapses into one O(n) pass instead of O(n^2) or O(n^3).
// Two flavours:
//
//	FIXED size     add the element entering on the right, subtract the one leaving
//	               on the left, once per step
//	VARIABLE size  grow right greedily, and shrink from the left only while the
//	               window is invalid
//
// The variable version is the one people get wrong, and the mistake is shrinking with
// an `if` instead of a `for`. One element entering can invalidate the window by more
// than one element's worth.
//
// # Why it is O(n) and not O(n^2)
//
// The inner shrink loop looks like it makes the whole thing quadratic, and it does not:
// `left` only ever moves forward, so across the entire run it advances at most n times
// in total. Each index is added once and removed once. That amortised argument is the
// pattern, and it is worth being able to state.
package slidingwindow

import "errors"

// ErrWindowTooLarge means k exceeds the input length.
var ErrWindowTooLarge = errors.New("slidingwindow: window larger than the input")

// Fixed-size windows
// ==================

// MaxSumOfSize returns the largest sum of any contiguous run of exactly k elements.
//
// Compute the first window once, then slide: add the element entering on the right and
// subtract the one leaving on the left. Recomputing each window from scratch is
// O(n*k); this is O(n) with two additions per step.
func MaxSumOfSize(nums []int, k int) (int, error) {
	if k <= 0 {
		return 0, errors.New("slidingwindow: k must be positive")
	}
	if k > len(nums) {
		return 0, ErrWindowTooLarge
	}

	window := 0
	for _, v := range nums[:k] {
		window += v
	}

	best := window

	for right := k; right < len(nums); right++ {
		window += nums[right] - nums[right-k]
		best = max(best, window)
	}

	return best, nil
}

// AverageOfSize returns the average of every contiguous run of exactly k elements.
//
// The output has len(nums)-k+1 entries, which is the count people get wrong by one in
// both directions.
func AverageOfSize(nums []int, k int) ([]float64, error) {
	if k <= 0 {
		return nil, errors.New("slidingwindow: k must be positive")
	}
	if k > len(nums) {
		return nil, ErrWindowTooLarge
	}

	out := make([]float64, 0, len(nums)-k+1)

	window := 0
	for i, v := range nums {
		window += v

		if i < k-1 {
			continue
		}
		out = append(out, float64(window)/float64(k))
		window -= nums[i-k+1]
	}

	return out, nil
}

// MaxOfEachWindow returns the maximum of every contiguous run of exactly k elements.
//
// The naive version rescans each window for its maximum, at O(n*k). This is O(n), using
// a deque of INDICES kept in decreasing order of their values: a monotonic deque.
//
// Two invariants do all the work:
//
//	the front index is always the maximum of the current window
//	any index whose value is smaller than a later arrival is useless forever, because
//	  the later one is both larger and stays in the window longer
//
// The second is why elements can be dropped from the back without checking anything
// else, and it is what makes each index enter and leave exactly once.
//
// This is the sliding window and the monotonic stack patterns in one function. See
// patterns/monotonicstack for the family it belongs to.
func MaxOfEachWindow(nums []int, k int) ([]int, error) {
	if k <= 0 {
		return nil, errors.New("slidingwindow: k must be positive")
	}
	if k > len(nums) {
		return nil, ErrWindowTooLarge
	}

	out := make([]int, 0, len(nums)-k+1)
	deque := make([]int, 0, k) // indices, values decreasing front to back

	for right, v := range nums {
		// Drop indices whose values this one beats: they can never be the maximum
		// again.
		for len(deque) > 0 && nums[deque[len(deque)-1]] <= v {
			deque = deque[:len(deque)-1]
		}
		deque = append(deque, right)

		// Drop the front if it has fallen out of the window.
		//
		// deque[1:] walks the slice header forward, which is the shape queue/README.md
		// is about. It is fine here because the deque never holds more than k indices
		// and append reallocates a bounded number of times over the whole run, but an
		// explicit head index would avoid the reallocation entirely.
		if deque[0] <= right-k {
			deque = deque[1:]
		}

		if right >= k-1 {
			out = append(out, nums[deque[0]])
		}
	}

	return out, nil
}

// Variable-size windows
// =====================

// LongestUniqueSubstring returns the length of the longest substring of s with no
// repeated characters.
//
// lastSeen maps each character to the index of its most recent occurrence. On a repeat
// inside the window, jump left PAST that occurrence rather than creeping one step at a
// time. Creeping is still O(n) amortised and the jump is clearer about why.
//
// The `>= left` check is the part that matters: a character seen before the window
// started is not a repeat, and forgetting the check makes left move backwards.
func LongestUniqueSubstring(s string) int {
	lastSeen := make(map[rune]int)
	left, best := 0, 0

	// Ranging over a string yields runes and byte offsets, so `right` is a byte index
	// and cannot be used for the length. The count is tracked separately.
	position := 0

	for _, c := range s {
		if at, seen := lastSeen[c]; seen && at >= left {
			left = at + 1 // evict everything up to and including the earlier copy
		}
		lastSeen[c] = position

		best = max(best, position-left+1)
		position++
	}

	return best
}

// LongestOnesWithFlips returns the length of the longest run of 1s obtainable by
// flipping at most k zeros. nums must contain only 0 and 1.
//
// The window is valid while it holds at most k zeros. Grow right always; shrink left
// only while invalid.
//
// The `for` on the shrink is load-bearing in general even though an `if` happens to
// work here, because each step adds at most one zero. Using `if` teaches the wrong
// shape, so this uses `for`.
func LongestOnesWithFlips(nums []int, k int) int {
	left, zeros, best := 0, 0, 0

	for right, bit := range nums {
		if bit == 0 {
			zeros++
		}

		for zeros > k {
			if nums[left] == 0 {
				zeros--
			}
			left++
		}

		best = max(best, right-left+1)
	}

	return best
}

// MinSubarrayAtLeast returns the length of the shortest contiguous run summing to at
// least target, or 0 if none does. Requires non-negative numbers.
//
// The mirror image of the problems above: here the window is valid when it is big
// enough, so it shrinks while VALID rather than while invalid, recording the answer on
// the way.
//
// Non-negative is required, and it is not a formality. With a negative number present,
// shrinking from the left can INCREASE the sum, so "the window is too big" stops being
// a monotonic property and the pattern does not apply at all. That case needs prefix
// sums and a different algorithm.
//
// A target of zero or less is met by the empty run, so the answer is 0. That is a
// degenerate answer and it needs stating, because without the guard the shrink loop
// walks left past right and indexes out of range. The `left <= right` condition on the
// loop is a second line of defence for the same thing.
func MinSubarrayAtLeast(nums []int, target int) int {
	if target <= 0 {
		return 0 // the empty run already sums to at least target
	}

	left, sum := 0, 0
	best := 0

	for right, v := range nums {
		sum += v

		for sum >= target && left <= right {
			length := right - left + 1
			if best == 0 || length < best {
				best = length
			}

			sum -= nums[left]
			left++
		}
	}

	return best
}

// MinWindowContaining returns the shortest substring of s containing every character of
// pattern, counting duplicates, or "" if there is none.
//
// The hardest of the classic window problems, and the difficulty is the validity check.
// Comparing two maps on every step would be O(k) per step. Instead `missing` counts how
// many required characters are still outstanding, so the check is `missing == 0`, which
// is O(1).
//
// The subtlety in maintaining it: `missing` decreases only when a character's count
// goes from insufficient to sufficient, not on every occurrence. A window with three
// copies of a character that needs one must not count as satisfying it three times.
func MinWindowContaining(s, pattern string) string {
	if pattern == "" || len(s) < len(pattern) {
		return ""
	}

	need := make(map[rune]int)
	for _, c := range pattern {
		need[c]++
	}

	have := make(map[rune]int)
	missing := len(need) // distinct characters still short of their quota

	runes := []rune(s)
	left := 0
	bestStart, bestLen := 0, -1

	for right, c := range runes {
		if _, required := need[c]; required {
			have[c]++
			if have[c] == need[c] {
				missing--
			}
		}

		// Shrink while the window is still valid, to find the smallest one ending
		// here.
		for missing == 0 {
			if bestLen < 0 || right-left+1 < bestLen {
				bestStart, bestLen = left, right-left+1
			}

			out := runes[left]
			if _, required := need[out]; required {
				if have[out] == need[out] {
					missing++ // dropping this one breaks the window
				}
				have[out]--
			}
			left++
		}
	}

	if bestLen < 0 {
		return ""
	}
	return string(runes[bestStart : bestStart+bestLen])
}

// CountSubarraysWithSum returns how many contiguous runs sum to exactly target.
//
// Not a sliding window, and it is here to mark the boundary. With negative numbers
// allowed, no window can work: growing the window does not monotonically increase the
// sum, so there is nothing to shrink towards. The answer is prefix sums in a map.
//
// The identity: a run (i, j] sums to target exactly when prefix[j] - prefix[i] ==
// target, so for each j the question is how many earlier prefixes equal
// prefix[j] - target. One pass, O(n).
//
// If a problem looks like a window and the numbers can be negative, this is the shape
// to reach for instead.
func CountSubarraysWithSum(nums []int, target int) int {
	seen := map[int]int{0: 1} // the empty prefix, so runs starting at index 0 count
	prefix, count := 0, 0

	for _, v := range nums {
		prefix += v
		count += seen[prefix-target]
		seen[prefix]++
	}

	return count
}
