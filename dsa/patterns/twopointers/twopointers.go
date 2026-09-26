// Package twopointers implements the two-pointer pattern.
//
// # The tell
//
// Two signals, and they point at two different variants.
//
// "A pair, triple, or subarray in a SORTED array" means two pointers converging from
// the ends. Sortedness is what makes it work: if the sum of the two ends is too small,
// the only way to increase it is to move the left pointer right, so one comparison
// eliminates a whole row of the n-by-n search space.
//
// "Compare or partition in place" means two pointers moving in the same direction, one
// reading and one writing. That variant is how every in-place filter, dedupe and
// partition is written, and it is the reason those operations need no extra memory.
//
// # Against a hash map
//
// For "two numbers summing to k" on UNSORTED input, a hash map is O(n) and two pointers
// need a sort first, at O(n log n). The map wins. Two pointers win when
//
//	the input is already sorted, or
//	O(1) extra space is required, or
//	the answer needs the elements in order anyway, as ThreeSum does
//
// Reaching for two pointers on unsorted input and sorting to enable it is a common
// reflex and usually the wrong call. TwoSumUnsorted below is the map version, for the
// comparison.
package twopointers

import (
	"cmp"
	"slices"
)

// Converging pointers
// ===================

// TwoSumSorted returns the indices of the two elements of a SORTED slice summing to
// target, or false.
//
// O(n) time, O(1) space, one pass. The invariant: everything outside [left, right] has
// been ruled out. A sum that is too small rules out the current left with every
// remaining right, and a sum that is too large rules out the current right with every
// remaining left. One comparison per step, each eliminating a row or a column.
func TwoSumSorted[T cmp.Ordered](sorted []T, target T, add func(a, b T) T) (int, int, bool) {
	left, right := 0, len(sorted)-1

	for left < right {
		sum := add(sorted[left], sorted[right])

		switch {
		case sum == target:
			return left, right, true
		case sum < target:
			left++ // the only way to increase the sum
		default:
			right--
		}
	}

	return 0, 0, false
}

// TwoSumInts is TwoSumSorted for ints, without the addition function.
//
// The generic version needs `add` because Go has no constraint for "things that can be
// added": cmp.Ordered covers <, and there is no arithmetic constraint in the standard
// library. constraints.Integer lives in x/exp and this module has no dependencies, so
// the choice is a func parameter or a per-type wrapper. Both are here so the trade is
// visible.
func TwoSumInts(sorted []int, target int) (int, int, bool) {
	return TwoSumSorted(sorted, target, func(a, b int) int { return a + b })
}

// TwoSumUnsorted returns the indices of two elements summing to target, for input in any
// order.
//
// O(n) time and O(n) space with a map, and it is the right answer for unsorted input.
// Two pointers would need a sort first, at O(n log n), and the sort would destroy the
// original indices that the question asks for.
//
// The check happens BEFORE the insert, which is what stops a single element pairing with
// itself when target is twice its value.
func TwoSumUnsorted(nums []int, target int) (int, int, bool) {
	seen := make(map[int]int, len(nums))

	for i, v := range nums {
		if j, ok := seen[target-v]; ok {
			return j, i, true
		}
		seen[v] = i
	}

	return 0, 0, false
}

// ThreeSum returns every distinct triple summing to zero, each sorted ascending, and the
// triples themselves in ascending order.
//
// O(n^2): fix the first element, then two pointers over the rest. The alternative,
// three nested loops, is O(n^3).
//
// The duplicate handling is the whole difficulty, and it happens in three places. After
// fixing an element, skip any identical next one, or the same triple is emitted twice.
// After a successful match, advance BOTH pointers past their duplicates. Missing any of
// the three gives repeated triples that a small test will not reveal.
func ThreeSum(nums []int) [][3]int {
	sorted := slices.Clone(nums)
	slices.Sort(sorted)

	var out [][3]int

	for i := 0; i < len(sorted)-2; i++ {
		// Skip a repeated first element.
		if i > 0 && sorted[i] == sorted[i-1] {
			continue
		}

		// Everything from here is positive, so no triple can reach zero.
		if sorted[i] > 0 {
			break
		}

		left, right := i+1, len(sorted)-1

		for left < right {
			sum := sorted[i] + sorted[left] + sorted[right]

			switch {
			case sum < 0:
				left++
			case sum > 0:
				right--
			default:
				out = append(out, [3]int{sorted[i], sorted[left], sorted[right]})

				// Advance past duplicates on BOTH sides. Advancing one would emit
				// the same triple again from the other.
				for left < right && sorted[left] == sorted[left+1] {
					left++
				}
				for left < right && sorted[right] == sorted[right-1] {
					right--
				}
				left++
				right--
			}
		}
	}

	return out
}

// MaxWaterContainer returns the largest area between two of the given heights, where the
// area is the distance between them times the shorter one.
//
// O(n), and the greedy step is the interesting part: always move the pointer at the
// SHORTER wall. Moving the taller one can never help, because the area is capped by the
// shorter wall, so a narrower window with the same cap is never better. That argument is
// what turns an O(n^2) scan into one pass.
func MaxWaterContainer(heights []int) int {
	left, right := 0, len(heights)-1
	best := 0

	for left < right {
		width := right - left
		height := min(heights[left], heights[right])

		best = max(best, width*height)

		// Move the shorter wall. Moving the taller one cannot increase the minimum.
		if heights[left] < heights[right] {
			left++
			continue
		}
		right--
	}

	return best
}

// IsPalindrome reports whether s reads the same forwards and backwards, ignoring
// everything that is not a letter or digit, and ignoring case.
//
// Two pointers from the ends, each skipping what it should ignore. The alternative,
// building a cleaned copy and comparing it with its reverse, is clearer and allocates
// twice; this is the O(1)-space version.
func IsPalindrome(s string) bool {
	runes := []rune(s)
	left, right := 0, len(runes)-1

	for left < right {
		for left < right && !isAlphanumeric(runes[left]) {
			left++
		}
		for left < right && !isAlphanumeric(runes[right]) {
			right--
		}

		if lower(runes[left]) != lower(runes[right]) {
			return false
		}

		left++
		right--
	}

	return true
}

// isAlphanumeric reports whether c is an ASCII letter or digit.
//
// Deliberately ASCII-only, and the limitation is stated rather than hidden: unicode.
// IsLetter would accept "é" and then lower() would have to handle its case mapping,
// which for some scripts is not a one-rune-to-one-rune operation. The stdlib has
// strings.ToLower and unicode.IsLetter for the real job.
func isAlphanumeric(c rune) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

func lower(c rune) rune {
	if c >= 'A' && c <= 'Z' {
		return c + ('a' - 'A')
	}
	return c
}

// Same-direction pointers
// =======================

// Dedupe removes consecutive duplicates in place and returns the number kept.
//
// A read pointer and a write pointer moving in the same direction. The caller uses
// s[:n], and everything past n is unspecified, which is exactly the contract of
// slices.Compact.
//
// This is what every in-place filter looks like, and the shape is worth recognising:
// read every element, write only the ones that survive, and the write pointer trails
// the read pointer by however many were dropped.
func Dedupe[T comparable](s []T) int {
	if len(s) == 0 {
		return 0
	}

	write := 1
	for read := 1; read < len(s); read++ {
		if s[read] == s[write-1] {
			continue
		}
		s[write] = s[read]
		write++
	}

	return write
}

// DedupeAllowing removes consecutive duplicates in place, keeping at most limit copies of
// each, and returns the number kept.
//
// The generalisation, and it collapses to one comparison: an element may be written if
// it differs from the one `limit` positions back in the OUTPUT. That works because the
// output is already deduplicated to the limit, so if it differs from that one, fewer
// than `limit` copies precede it.
func DedupeAllowing[T comparable](s []T, limit int) int {
	if limit <= 0 {
		return 0
	}

	write := 0
	for _, v := range s {
		if write >= limit && s[write-limit] == v {
			continue
		}
		s[write] = v
		write++
	}

	return write
}

// MoveZerosToEnd moves every zero to the end in place, keeping the order of the rest.
//
// The same read/write shape. The final loop fills the tail rather than swapping as it
// goes, because swapping does more writes for the same result: a slice of a million
// zeros followed by one non-zero costs one write here and a million swaps there.
func MoveZerosToEnd(nums []int) {
	write := 0
	for _, v := range nums {
		if v == 0 {
			continue
		}
		nums[write] = v
		write++
	}

	for ; write < len(nums); write++ {
		nums[write] = 0
	}
}

// PartitionByPredicate moves every element satisfying keep to the front, in place, and
// returns how many there are.
//
// The KEPT group stays in its original order. The rejected group does not, and that is
// worth stating rather than glossing: each swap throws a rejected element to wherever the
// write pointer was, so [1 2 3 4 5 6 7 8] partitioned on "even" gives [2 4 6 8] followed
// by [5 3 7 1].
//
// A fully stable partition needs O(n) extra space, or a rotation-based algorithm at
// O(n log n). This is the O(1)-space version, and for most uses "the survivors are in
// order" is the half that matters.
func PartitionByPredicate[T any](s []T, keep func(T) bool) int {
	write := 0
	for read := range s {
		if !keep(s[read]) {
			continue
		}
		s[write], s[read] = s[read], s[write]
		write++
	}
	return write
}

// SquaresOfSorted returns the squares of a sorted slice of ints, sorted.
//
// The trap: the input may contain negatives, so the largest square is at one END of the
// slice, not the middle. Squaring and re-sorting is O(n log n); comparing the two ends
// and filling the output from the BACK is O(n).
//
// Filling backwards is the trick. Filling forwards would need the smallest square,
// which is somewhere in the middle and takes a search to find.
func SquaresOfSorted(sorted []int) []int {
	out := make([]int, len(sorted))

	left, right := 0, len(sorted)-1

	for i := len(sorted) - 1; i >= 0; i-- {
		leftSquare := sorted[left] * sorted[left]
		rightSquare := sorted[right] * sorted[right]

		if leftSquare > rightSquare {
			out[i] = leftSquare
			left++
			continue
		}
		out[i] = rightSquare
		right--
	}

	return out
}
