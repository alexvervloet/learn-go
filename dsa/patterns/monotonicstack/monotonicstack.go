// Package monotonicstack implements the monotonic stack pattern.
//
// # The tell
//
// "The next greater element", "the previous smaller element", "how many days until a warmer
// one", "the largest rectangle", "how much water is trapped". Anything about the nearest
// element in one direction that beats the current one.
//
// # The idea
//
// Keep a stack of INDICES whose answer is still unknown, and keep the values at those
// indices monotonic. When a new element arrives, it is the answer for everything on the
// stack that it beats, so pop those and record it.
//
//	values   2  1  2  4  3
//	stack    [0]                      2 is unresolved
//	         [0,1]                    1 is smaller, so 2 is still unresolved
//	         4 arrives: it beats 2 at index 1 and 2 at index 0, so both pop
//
// The invariant that makes it work: an index on the stack has not yet met anything that
// beats it, and the values on the stack are ordered, so the first element that beats the top
// beats a whole run of them.
//
// # Why it is O(n)
//
// The inner pop loop makes it look quadratic. Each index is PUSHED once and POPPED once, so
// across the whole run there are at most n pops however long any individual burst is. That
// amortised argument is the pattern, and TestEachIndexIsPushedAndPoppedOnce measures it.
//
// It is the same argument as the sliding window's "left only moves forward", and the two
// patterns meet in slidingwindow.MaxOfEachWindow, which is a monotonic DEQUE.
//
// # Increasing or decreasing
//
// The direction of the stack is the direction of the question, and getting it backwards
// gives a plausible wrong answer rather than an error:
//
//	next GREATER   keep values DECREASING, pop when the new value is larger
//	next SMALLER   keep values INCREASING, pop when the new value is smaller
//
// All four combinations are here, generated from one implementation, so the relationship is
// visible rather than four near-identical functions to compare by eye.
package monotonicstack

import "cmp"

// Absent is the result for an element that has no answer in the given direction.
const Absent = -1

// NextGreater returns, for each index, the index of the next element to its right that is
// strictly greater, or Absent.
func NextGreater[T cmp.Ordered](s []T) []int {
	return scan(s, forwards, func(candidate, onStack T) bool { return candidate > onStack })
}

// NextGreaterOrEqual returns the index of the next element to the right that is greater than
// or equal to this one.
//
// The difference from NextGreater is one character in the comparison, and it decides how
// ties are treated. For a slice of equal values, NextGreater finds nothing and this finds
// the neighbour. Which one a problem wants is usually stated only by its example.
func NextGreaterOrEqual[T cmp.Ordered](s []T) []int {
	return scan(s, forwards, func(candidate, onStack T) bool { return candidate >= onStack })
}

// NextSmaller returns the index of the next element to the right that is strictly smaller.
func NextSmaller[T cmp.Ordered](s []T) []int {
	return scan(s, forwards, func(candidate, onStack T) bool { return candidate < onStack })
}

// PreviousGreater returns the index of the nearest element to the LEFT that is strictly
// greater.
func PreviousGreater[T cmp.Ordered](s []T) []int {
	return scan(s, backwards, func(candidate, onStack T) bool { return candidate > onStack })
}

// PreviousSmaller returns the index of the nearest element to the left that is strictly
// smaller.
func PreviousSmaller[T cmp.Ordered](s []T) []int {
	return scan(s, backwards, func(candidate, onStack T) bool { return candidate < onStack })
}

// PreviousSmallerOrEqual returns the index of the nearest element to the left that is
// smaller than or equal to this one.
func PreviousSmallerOrEqual[T cmp.Ordered](s []T) []int {
	return scan(s, backwards, func(candidate, onStack T) bool { return candidate <= onStack })
}

type direction bool

const (
	forwards  direction = false
	backwards direction = true
)

// scan is the one implementation the six functions above are made of.
//
// beats says when an arriving value resolves an index still on the stack. Running the slice
// backwards turns "next" into "previous" with no other change, which is the whole reason to
// write it once: the four variants differ by a comparison and a loop direction, and stating
// that is more useful than four functions that look almost the same.
func scan[T cmp.Ordered](s []T, dir direction, beats func(candidate, onStack T) bool) []int {
	out := make([]int, len(s))
	for i := range out {
		out[i] = Absent
	}

	stack := make([]int, 0, len(s)) // indices whose answer is still unknown

	for step := range s {
		at := step
		if dir == backwards {
			at = len(s) - 1 - step
		}

		// Everything on the stack that this element beats is resolved by it. Because
		// the stack is monotonic, this pops a whole run at once and never has to look
		// past the first index it does not beat.
		for len(stack) > 0 && beats(s[at], s[stack[len(stack)-1]]) {
			out[stack[len(stack)-1]] = at
			stack = stack[:len(stack)-1]
		}

		stack = append(stack, at)
	}

	// Whatever is left never met anything that beat it, and out is already Absent there.
	return out
}

// DaysUntilWarmer returns, for each day, how many days until a strictly warmer one, or 0.
//
// "Daily temperatures", and it is NextGreater with the indices turned into distances. Worth
// having as its own function because the conversion is where the off-by-one goes: the answer
// is `next - i`, not `next - i - 1`, and it is 0 rather than Absent when there is none.
func DaysUntilWarmer(temperatures []int) []int {
	next := NextGreater(temperatures)

	out := make([]int, len(temperatures))
	for i, n := range next {
		if n == Absent {
			continue // 0 means "never", which is the problem's convention
		}
		out[i] = n - i
	}

	return out
}

// StockSpan returns, for each day, how many consecutive days up to and including it had a
// price less than or equal to it.
//
// PreviousGreater in disguise: the span is the distance back to the nearest strictly higher
// price. Included because it is the ONLINE version of the pattern, which is the form that
// actually turns up in production: prices arrive one at a time and the answer is needed
// immediately, with no second pass available.
//
// See Spanner for the streaming version.
func StockSpan(prices []int) []int {
	previous := PreviousGreater(prices)

	out := make([]int, len(prices))
	for i, p := range previous {
		out[i] = i - p // Absent is -1, so a price higher than everything gives i+1
	}

	return out
}

// Spanner computes stock spans one price at a time.
//
// The same algorithm with the loop turned inside out. The stack survives between calls,
// which is what makes it online, and the amortised O(1) per call is the same argument as
// before: each price is pushed once and popped once across the lifetime of the Spanner.
//
// The zero value is ready to use.
type Spanner struct {
	stack  []int // indices of prices not yet beaten
	prices []int
}

// Next records a price and returns its span.
func (s *Spanner) Next(price int) int {
	at := len(s.prices)
	s.prices = append(s.prices, price)

	for len(s.stack) > 0 && s.prices[s.stack[len(s.stack)-1]] <= price {
		s.stack = s.stack[:len(s.stack)-1]
	}

	span := at + 1
	if len(s.stack) > 0 {
		span = at - s.stack[len(s.stack)-1]
	}

	s.stack = append(s.stack, at)

	return span
}

// Len reports how many prices have been recorded.
func (s *Spanner) Len() int { return len(s.prices) }

// LargestRectangle returns the area of the largest rectangle that fits under a histogram,
// and the bar index and width that achieve it.
//
// The problem the pattern exists for. Every rectangle is limited by its shortest bar, so for
// each bar the question is how far it can extend left and right before meeting something
// shorter. Those are PreviousSmaller and NextSmaller, so the whole thing is two monotonic
// scans and a multiplication.
//
// The tie-breaking is the subtle part. With equal-height bars, using strict comparisons on
// both sides means neither bar sees the other as a boundary, so both compute the full width
// and the maximum still comes out right. Using `<=` on one side and `<` on the other also
// works. Using `<=` on BOTH is the one that breaks, because then each bar stops at its
// equal neighbour and the widest rectangle is never considered.
func LargestRectangle(heights []int) (area, at, width int) {
	if len(heights) == 0 {
		return 0, Absent, 0
	}

	left := PreviousSmaller(heights)
	right := NextSmaller(heights)

	for i, h := range heights {
		// Absent on the left means "extends to the start", so -1 is already correct.
		// Absent on the right means "extends to the end", so it becomes len(heights).
		end := right[i]
		if end == Absent {
			end = len(heights)
		}

		w := end - left[i] - 1

		if h*w > area {
			area, at, width = h*w, i, w
		}
	}

	return area, at, width
}

// TrapWater returns how much water is trapped between the given bar heights.
//
// The water above bar i is min(tallest to its left, tallest to its right) minus its own
// height. Computing those two prefix maxima takes two passes and O(n) memory.
//
// This version uses a monotonic stack instead, filling water one horizontal layer at a time:
// when a bar arrives that is taller than the top of the stack, the gap between the new bar
// and the one below the top holds water up to the shorter of the two.
//
// TrapWaterTwoPointers is the O(1)-space version, and it is the better answer. Both are here
// because the comparison is the lesson: the stack version generalises to problems the two
// pointers cannot handle, and for this problem it is strictly worse.
func TrapWater(heights []int) int {
	total := 0
	var stack []int // indices, heights decreasing

	for i, h := range heights {
		for len(stack) > 0 && heights[stack[len(stack)-1]] < h {
			// The bottom of a basin.
			floor := stack[len(stack)-1]
			stack = stack[:len(stack)-1]

			if len(stack) == 0 {
				break // no left wall, so nothing is held
			}

			left := stack[len(stack)-1]

			// One horizontal layer: as wide as the gap, as deep as the shorter wall
			// above the floor.
			width := i - left - 1
			depth := min(heights[left], h) - heights[floor]

			total += width * depth
		}

		stack = append(stack, i)
	}

	return total
}

// TrapWaterTwoPointers returns the same answer in O(1) space.
//
// Two pointers converging, tracking the tallest bar seen from each side. The key step: move
// whichever side has the SHORTER wall, because that side's answer is already determined.
// If the left wall is shorter, then whatever is on the right, the water above the left
// pointer is capped by the left maximum, so it can be settled immediately.
//
// That is the same greedy argument as twopointers.MaxWaterContainer, applied to a different
// question, and noticing that is worth more than either solution.
func TrapWaterTwoPointers(heights []int) int {
	if len(heights) < 3 {
		return 0
	}

	left, right := 0, len(heights)-1
	leftMax, rightMax := heights[left], heights[right]
	total := 0

	for left < right {
		if leftMax <= rightMax {
			left++
			leftMax = max(leftMax, heights[left])
			total += leftMax - heights[left]
			continue
		}

		right--
		rightMax = max(rightMax, heights[right])
		total += rightMax - heights[right]
	}

	return total
}

// RemoveDigits removes k digits from a decimal string to leave the smallest possible number,
// with no leading zeros.
//
// A monotonic stack over characters rather than numbers. To make a number small, an earlier
// digit matters more than a later one, so whenever a digit is followed by a smaller one, the
// earlier digit should go. Keeping the stack increasing does exactly that.
//
// Two details that are easy to miss and that the tests cover:
//
//	if k removals are left over, they come off the END, because at that point the
//	  remaining digits are increasing and the last ones are the largest
//	leading zeros have to be stripped afterwards, and an empty result is "0"
func RemoveDigits(digits string, k int) string {
	if k <= 0 {
		return stripLeadingZeros(digits)
	}
	if k >= len(digits) {
		return "0"
	}

	stack := make([]byte, 0, len(digits))

	for i := range len(digits) {
		d := digits[i]

		for k > 0 && len(stack) > 0 && stack[len(stack)-1] > d {
			stack = stack[:len(stack)-1]
			k--
		}

		stack = append(stack, d)
	}

	// Anything left to remove comes off the end: what remains is increasing, so the last
	// digits are the largest.
	stack = stack[:len(stack)-k]

	return stripLeadingZeros(string(stack))
}

func stripLeadingZeros(s string) string {
	at := 0
	for at < len(s)-1 && s[at] == '0' {
		at++
	}

	out := s[at:]
	if out == "" {
		return "0"
	}
	return out
}
