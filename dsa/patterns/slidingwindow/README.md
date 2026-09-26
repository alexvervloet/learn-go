# Sliding window

## The tell

The word is **contiguous**.

"The longest substring with no repeated characters" is a window. "The longest
*subsequence* with no repeats" is not, because a subsequence may skip elements and a
window may not. Anything asking for the best, longest, shortest, or count of contiguous
runs is this pattern.

## Two flavours

| | |
|---|---|
| **fixed size** | add the element entering on the right, subtract the one leaving on the left |
| **variable size** | grow right greedily, shrink from the left only while the window is invalid |

The variable version is the one people get wrong, and the mistake is shrinking with an
`if` instead of a `for`. One element entering can invalidate the window by more than one
element's worth.

## Why it is O(n)

The inner shrink loop looks like it makes the whole thing quadratic. It does not: `left`
only ever moves **forward**, so across the entire run it advances at most n times in
total. Each index enters once and leaves once.

That amortised argument *is* the pattern, and it is worth being able to state out loud.
`TestLeftOnlyMovesForward` instruments the loop over 100,000 elements and checks the
total movement stays under n.

## The functions

| | |
|---|---|
| `MaxSumOfSize` | largest sum of exactly k consecutive elements |
| `AverageOfSize` | the average of every window of size k |
| `MaxOfEachWindow` | the maximum of every window of size k, in O(n) |
| `LongestUniqueSubstring` | longest substring with no repeated character |
| `LongestOnesWithFlips` | longest run of 1s after flipping at most k zeros |
| `MinSubarrayAtLeast` | shortest run summing to at least target |
| `MinWindowContaining` | shortest substring containing every character of a pattern |
| `CountSubarraysWithSum` | **not** a window, and here to mark the boundary |

## The parts worth reading twice

**`MaxOfEachWindow` is two patterns at once.** A monotonic deque of *indices*, kept in
decreasing order of their values. Two invariants do everything: the front index is the
window's maximum, and any index whose value a later arrival beats is useless forever,
because the later one is both larger and stays in the window longer. That second one is
why elements can be dropped from the back with no further checks, and it is what makes
each index enter and leave exactly once. See [monotonicstack/](../monotonicstack/) for
the family.

**`LongestUniqueSubstring` needs the `>= left` check.** A character seen *before* the
window started is not a repeat. Without the check, `left` moves backwards and the answer
comes out too large. It also ranges over runes, so `naïve` is 5 and not 6.

**`MinSubarrayAtLeast` requires non-negative numbers,** and that is not a formality. With
a negative present, shrinking from the left can *increase* the sum, so "the window is too
big" stops being monotonic and the pattern does not apply. It also returns 0 for a target
of zero or less, because the empty run already satisfies it. Without that guard the
shrink loop walks `left` past `right` and panics, which is how the bug was found.

**`MinWindowContaining` keeps a `missing` counter,** so the validity check is `missing ==
0` rather than comparing two maps on every step. The subtlety: `missing` changes only when
a character's count crosses from insufficient to sufficient, not on every occurrence. A
window with three copies of a character that needs one must not satisfy it three times.

**`CountSubarraysWithSum` is deliberately not a window.** With negatives allowed there is
nothing monotonic to exploit, so the answer is prefix sums in a map: a run (i, j] sums to
target exactly when `prefix[j] - prefix[i] == target`. If a problem looks like a window
and the numbers can be negative, this is the shape to reach for instead.

## Testing

Every function is checked against a brute-force version on thousands of random inputs,
because the invariants in this pattern are the kind that sound right and are off by one.
The brute-force versions are in the test file and are deliberately the obvious O(n²) or
O(n³) implementations.

`MinWindowContaining` is tested over a three-letter alphabet, so duplicates are constant
and the `missing` bookkeeping gets exercised. Several windows can tie on length, so the
test asserts the **length** matches and that the returned string both contains the pattern
and is a substring of the input.

## How to run

```bash
go test ./patterns/slidingwindow
go test -v -run LeftOnlyMovesForward ./patterns/slidingwindow
```
