# Binary search on the answer

## The tell

"The **minimum** X such that…" or "the **maximum** X such that…". Two phrasings in
particular almost always mean this pattern:

| | |
|---|---|
| "minimise the maximum" | split an array so the largest part is as small as possible |
| "maximise the minimum" | place cows so the closest pair is as far apart as possible |

There is often **no array to search at all**. What is being bisected is the space of
possible answers.

## Why it works

The same requirement as any binary search: a monotonic predicate. Here it takes the form
"is X enough?", and the question qualifies when

```
if X works, every larger X works too      (for a minimum)
if X works, every smaller X works too     (for a maximum)
```

Checking that property is the whole job of recognising the pattern.

## The three steps

1. Decide what the answer **is**: a speed, a capacity, a distance, a number of days.
2. Write `feasible(x) bool`, which is usually a simple O(n) simulation.
3. Bisect the range of possible answers.

Step 2 is where the work is, and it is deliberately dull: a loop that adds things up. The
mistake is trying to be clever there, because the binary search supplies the cleverness.

## Picking the bounds

The most common bug in the pattern, and the rule is to make them **obviously safe** rather
than tight:

| | |
|---|---|
| `lo` | the smallest value that could conceivably work, often 1 or `max(element)` |
| `hi` | a value that certainly works, usually `sum(elements)` or `max(elements)` |

A `lo` that is too high silently returns a wrong answer, because the true boundary is outside
the searched range and nothing detects that. `hi` too low does the same. Both bounds should
be justifiable in one sentence, and every function here says what its are and why.

The extra `log2` iterations of a generous bound cost nothing. `IntegerSquareRoot` searches
`[1, n]` rather than `[1, √n]` for exactly that reason.

## These are all the same problem

| function | the story |
|---|---|
| `MinShipCapacity` | packages shipped in order within D days |
| `SplitArrayLargestSum` | k contiguous parts, minimise the largest sum |
| `MinMaxWorkload` | jobs assigned in order, minimise the busiest worker |

One question, three costumes. `TestTheseAreAllTheSameProblem` runs all three on the same
random inputs and asserts they agree, which is a more useful thing to know than any one of
them.

## SearchMax by negation

The maximising variant is obtained by bisecting the **negation** and stepping back one:

```go
firstBad, found := searching.SearchAnswer(lo, hi, func(x int) bool { return !feasible(x) })
```

rather than by writing a second loop with the comparisons flipped. Flipping comparisons is
exactly where the off-by-one lives, and doing it once in a wrapper beats doing it per
problem.

`MaxMinDistance` and `IntegerSquareRoot` are the two that need it: their predicates are true
for small values and false for large ones, the opposite direction from everything else.

## Two overflow bugs, found by a test that hung

`IntegerSquareRoot(math.MaxInt)` returned `math.MaxInt`, confidently and with no error. Two
separate problems, both in [searching/](../../searching/)'s `SearchAnswer`:

**The `hi+1` sentinel.** The old signature returned `hi+1` to mean "nothing found", which
overflows when `hi` is `math.MaxInt` and wraps to `math.MinInt`. It now returns
`(int, bool)`.

**The range width.** The old implementation shifted `[lo, hi]` into `[0, n)` by computing
`hi - lo + 1`, which overflows for any range wider than `MaxInt`. It now bisects `[lo, hi]`
directly.

Fixing those exposed a third, which announced itself by hanging the test suite:
`lo + (hi-lo)/2` is the standard safe midpoint and it is **not safe here**. For
`lo = math.MinInt` and `hi = 0` the difference is 2⁶³, which does not fit in an `int`. The
fix is unsigned subtraction, and the searching package's README has the details.

`IntegerSquareRoot` also had to move its lower bound from 0 to 1, because its predicate
divides by `x`.

## Testing

The interesting property is not the value, it is that the value is a **boundary**. So the
tests check two things:

- the answer works, and
- the value one step further in does not.

That is stronger than comparing against a memorised number, and it does not need the right
answer to be known in advance. `TestMinEatingSpeedIsTheBoundary` and
`TestMinShipCapacityIsTheBoundary` are both written that way, with the simulation reimplemented
independently in the test so it is not just re-running the implementation.

Where brute force is available it is used instead: `SplitArrayLargestSum` is checked against
enumerating every way to cut the array, and `MaxMinDistance` against every subset of the
right size.

`TestSearchIsLogarithmic` counts predicate calls rather than timing anything: a range of a
billion must cost about 30 calls.

## How to run

```bash
go test ./patterns/binarysearchanswer
go test -v -run SearchIsLogarithmic ./patterns/binarysearchanswer
```
