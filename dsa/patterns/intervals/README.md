# Intervals

## The tell

A list of `[start, end]` pairs plus "overlap", "merge", "schedule", "how many rooms", or
"can they all fit".

## Sort first, and which endpoint decides the problem

| sort by | for |
|---|---|
| **start** | merging, inserting, "do any overlap" |
| **end** | "how many can I fit without overlap" |

Sorting by start and greedily keeping the first interval is **wrong** for the scheduling
problem, and it is wrong in a way that looks right on small examples.

`TestGreedyByEndIsOptimal` checks the earliest-finishing greedy against exhaustive search
over all 2ⁿ subsets, and also runs the two plausible wrong greedies to count how often they
lose:

| strategy | suboptimal on |
|---|---|
| sort by **end**, take earliest finisher | 0 of 3,000 |
| sort by **start**, take the first that fits | **195** of 3,000 |
| take the **shortest** that fits | **62** of 3,000 |

6.5% and 2.1%. Often enough to matter, rarely enough that a hand-written example misses it.

The counterexamples are small once you have them. For `[0,10) [1,2) [3,4)`, sorting by start
keeps only `[0,10)` where two fit. For `[1,5) [4,6) [5,9)`, the shortest is `[4,6)` and it
blocks both others, where `[1,5)` and `[5,9)` both fit.

Finishing earliest is optimal because it leaves the most room for everything after it, and
nothing is given up: any schedule can have its first interval swapped for the
earliest-finishing one without conflict.

## Half-open or closed

The decision that causes more bugs than the algorithms do. Does `[1,2]` overlap `[2,3]`?

| | |
|---|---|
| **closed** `[1,2]` and `[2,3]` | share the point 2, so **yes** |
| **half-open** `[1,2)` and `[2,3)` | touch and do not overlap, so **no** |

A meeting from 1 to 2 and one from 2 to 3 do not conflict. A fence from post 1 to 2 and one
from 2 to 3 do share a post.

This package is **half-open** throughout, which is the convention that matches scheduling.
Getting it backwards changes answers by exactly one in the cases where it matters, which is
the hardest kind of bug to spot.

Two places it shows up as a single character:

```go
// Overlaps: `<` not `<=`, or touching intervals would count as overlapping
return i.Start < other.End && other.Start < i.End

// MinRooms: -1 sorts before +1 at the same instant, so a room freed at 10
// is available at 10. Reversed, the answer is one too high on exactly the
// inputs where rooms are tight.
return cmp.Compare(a.delta, b.delta)
```

`Overlaps` and `Touches` are separate methods because both questions come up: merging needs
`Touches` (so `[1,2)` and `[2,3)` become `[1,3)`), and conflict detection needs `Overlaps`.

## The functions

| | |
|---|---|
| `Merge` | combine so nothing overlaps or touches |
| `Insert` | add one interval to a merged list, in O(n) |
| `AnyOverlap` | "can this person attend all these meetings" |
| `MinRooms` | the peak number of simultaneous intervals |
| `MaxNonOverlapping` / `MinRemovals` | the greedy scheduling result, both directions |
| `Intersection` | what two merged lists have in common |
| `Subtract` | what is left of a window after the blocks |
| `TotalCovered` | length covered, counting overlaps once |

## The parts worth reading twice

**`Insert` is O(n) in three phases**, and the shape is the thing to remember: copy
everything strictly before, absorb everything that touches while widening, copy the rest.
Appending and re-merging is O(n log n) and the usual first answer.
`TestInsertMatchesMerge` checks the two agree on 5,000 random inputs.

**`AnyOverlap` only checks adjacent pairs.** After sorting by start, if any two intervals
overlap then two *adjacent* ones do. That is the whole optimisation, and
`TestAnyOverlapMatchesAllPairs` verifies it against comparing every pair.

**`MinRooms` is a sweep**, not an interval walk. Turn each interval into a `+1` at its start
and a `-1` at its end, sort the events, track the running total, and the peak is the answer.
The obvious version, counting coverage at every instant, is the oracle in the test.

**`Intersection` advances whichever interval ends first**, because it has nothing left to
offer the other list. Two pointers, O(m+n).

**`ByStart` breaks ties on `End`.** Not cosmetic: without it the order of equal-start
intervals depends on the sort's stability, so an answer that happens to be right with one
sort changes with another.

**Empty intervals are dropped rather than carried.** An interval containing no points cannot
merge with anything, and keeping it would produce output that violates the postcondition.
`Interval{5, 3}` and `Interval{5, 5}` are both empty.

## Testing

The interesting functions are checked **pointwise** rather than against a fixed answer.
`Merge`, `Intersection` and `Subtract` all have the same property: for every point in a
small universe, the result covers it exactly when the definition says it should. That
catches off-by-one errors at the boundaries, which is where all of this goes wrong, and it
does not require knowing the right answer in advance.

`MaxNonOverlapping` is checked against enumerating all 2ⁿ subsets and taking the largest
non-overlapping one.

## How to run

```bash
go test ./patterns/intervals
go test -v -run GreedyByEndIsOptimal ./patterns/intervals
```
