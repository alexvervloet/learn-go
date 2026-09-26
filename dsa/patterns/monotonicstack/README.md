# Monotonic stack

## The tell

"The **next greater** element", "the **previous smaller** element", "how many days until a
warmer one", "the largest rectangle", "how much water is trapped". Anything about the
**nearest element in one direction that beats the current one**.

## The idea

Keep a stack of **indices whose answer is still unknown**, and keep the values at those
indices monotonic. When a new element arrives, it is the answer for everything on the stack
that it beats, so pop those and record it.

```
values   2  1  2  4  3
stack    [0]           2 at index 0 is unresolved
         [0,1]         1 is smaller, so index 0 is still unresolved
         4 arrives:    it beats index 1 and index 0, so both pop and both get answer 3
```

The invariant that makes it work: an index on the stack has not yet met anything that beats
it, and the values on the stack are ordered, so the first element that beats the top beats a
**whole run** of them and the scan never has to look past the first one it does not beat.

## Why it is O(n)

The inner pop loop makes it look quadratic. Each index is **pushed once and popped once**, so
across the whole run there are at most n pops however long any individual burst is.

`TestEachIndexIsPushedAndPoppedOnce` measures it over 200,000 elements:

| input | pushes | pops |
|---|---|---|
| random | 200,000 | 199,982 |
| ascending | 200,000 | 199,999 |
| descending | 200,000 | **0** |

Descending input never pops at all, which is the worst case for memory and the best for
time. It is the same amortised argument as the sliding window's "left only moves forward",
and the two patterns meet in `slidingwindow.MaxOfEachWindow`, which is a monotonic **deque**.

## Direction

The stack's direction is the question's direction, and getting it backwards gives a
plausible wrong answer rather than an error:

| question | stack | pop when |
|---|---|---|
| next **greater** | values **decreasing** | the new value is larger |
| next **smaller** | values **increasing** | the new value is smaller |

All six variants here come from **one** implementation, `scan`, parameterised by a comparison
and a loop direction. Running the slice backwards turns "next" into "previous" with no other
change. Writing them out separately would be four near-identical functions to compare by eye,
which is exactly where a flipped comparison hides.

`TestAllVariantsMatchBruteForce` checks every one against the obvious O(n²) version on 3,000
random inputs each, over a six-value range so ties are constant.

## Strict or OrEqual

`NextGreater` and `NextGreaterOrEqual` differ by one character, and they only come apart on
**ties**:

```
[7 7 7]   NextGreater        -> [-1 -1 -1]
          NextGreaterOrEqual -> [ 1  2 -1]
```

Which one a problem wants is usually stated only by its example. The sample slice used
throughout the tests happens to give both variants the same answer, so the degenerate test
is the one that actually distinguishes them.

## Largest rectangle

The problem the pattern exists for. Every rectangle is limited by its shortest bar, so for
each bar the question is how far it reaches left and right before meeting something shorter.
Those are `PreviousSmaller` and `NextSmaller`, so the whole thing is two monotonic scans and
a multiplication.

The tie-breaking is the subtle part. With equal-height bars:

| | |
|---|---|
| strict on both sides | works: neither bar sees the other as a boundary, so both compute the full width |
| `<=` on one side, `<` on the other | works |
| `<=` on **both** | **breaks**: each bar stops at its equal neighbour and the widest rectangle is never considered |

`TestLargestRectangleMatchesBruteForce` uses a five-value range precisely so ties are
everywhere.

## Trapping rain water, two ways

`TrapWater` uses the stack, filling water one horizontal layer at a time.
`TrapWaterTwoPointers` does it in O(1) space, and it is the better answer.

Both are here because the comparison is the lesson. The two-pointer version moves whichever
side has the **shorter** wall, because that side's answer is already determined: if the left
wall is shorter then, whatever is on the right, the water above the left pointer is capped by
the left maximum and can be settled immediately.

That is the same greedy argument as `twopointers.MaxWaterContainer`, applied to a different
question. Noticing that is worth more than either solution.

`TestTrapWaterImplementationsAgree` checks both against the definition (water above bar i is
`min(tallest left, tallest right) - height`) on 5,000 random inputs.

## Two things that are not about numbers

**`Spanner` is the online version.** The same algorithm with the loop turned inside out: the
stack survives between calls, prices arrive one at a time, and the answer is needed
immediately with no second pass available. That is the form that actually turns up in
production, and the amortised O(1) per call is the same push-once-pop-once argument.

**`RemoveDigits` is a monotonic stack over characters.** To make a number small, an earlier
digit matters more than a later one, so whenever a digit is followed by a smaller one the
earlier digit should go. Keeping the stack increasing does exactly that.

Two details the tests cover and that are easy to miss: leftover removals come off the **end**,
because what remains is increasing so the last digits are the largest; and leading zeros have
to be stripped afterwards, with an empty result becoming `"0"`.

## How to run

```bash
go test ./patterns/monotonicstack
go test -v -run EachIndexIsPushedAndPoppedOnce ./patterns/monotonicstack
```
