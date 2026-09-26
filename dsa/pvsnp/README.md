# P vs NP

## The distinction that actually matters

P and NP are not "easy" and "hard". They are two different questions:

| | |
|---|---|
| **P** | can you **find** an answer in polynomial time |
| **NP** | can you **check** a proposed answer in polynomial time |

Every problem in this package is in NP, and for all of them checking is trivial.
Hand me a subset and I add it up in O(n). Hand me a tour and I measure it in O(n).
What nobody knows how to do is find them without, in effect, trying everything.

That asymmetry is why every solver here has a `Verify` function next to it. **The
verifier is the definition of the problem.** The solver is the part we cannot do
quickly.

There is a second distinction hiding in the TSP section. The decision question ("is
there a tour shorter than k") is NP-**complete**, and a tour is a certificate for it.
The optimisation question ("what is the shortest tour") is NP-**hard**, and has no
short certificate at all, because proving a tour is shortest means ruling out the
others. That is why `VerifyTour` checks validity and says nothing about optimality.

## What the exponents mean

| n | 2ⁿ | (n−1)! |
|---|---|---|
| 10 | 1,024 | 362,880 |
| 20 | 1,048,576 | 121,645,100,408,832,000 |
| 30 | 1,073,741,824 | overflows int64 |
| 62 | 4,611,686,018,427,387,904 | overflows int64 |
| 63 | overflows int64 | overflows int64 |

`TourCount` returns −1 once the **answer** stops fitting in 64 bits, which happens at
n=22. That is a function whose return value overflows before its runtime becomes a
problem.

## Subset sum

Given a set of numbers and a target, is there a subset summing to the target exactly?
One of Karp's original 21 NP-complete problems, and the easiest to state.

### Brute force

Every subset of n elements is an n-bit number, so counting from 0 to 2ⁿ−1 enumerates
them all and bit i says whether element i is in. That correspondence is why this is
the natural brute force.

Worst case (unreachable target, so every subset is tried):

| n | time |
|---|---|
| 10 | 3.5 µs |
| 15 | 152 µs |
| 20 | 5.8 ms |
| 24 | 117 ms |
| 26 | 514 ms |

Doubling per element. n=30 is 8 seconds, n=40 is 2.3 hours, n=50 is 100 days.

The inner loop walks only the **set** bits, using `m &= m - 1` to clear the lowest and
`bits.TrailingZeros64` to find it. That runs `popcount(mask)` times instead of n,
which on average halves the work and buys exactly one more element of n. Which tells
you something about fighting an exponential with constant factors.

### The dynamic program, and why it is not a solution

`SubsetSumDP` is O(n × target). Same numbers, target 100,001:

| n | brute force | DP |
|---|---|---|
| 10 | 3.5 µs | 876 µs |
| 20 | 5.8 ms | 1.64 ms |
| 24 | 117 ms | 2.08 ms |
| 26 | 514 ms | 2.18 ms |

The DP is 250x slower at n=10 and 236x faster at n=26, because it barely depends on n
at all. It depends on the **target**:

| target | time |
|---|---|
| 1,000 | 13.6 µs |
| 10,000 | 233 µs |
| 100,000 | 1.65 ms |
| 1,000,000 | 16.2 ms |
| 10,000,000 | 162 ms |

Exactly linear in the target. And that is why this does not put subset sum in P.

**The input size is the number of bits needed to write the problem down.** A target of
one billion is 30 bits, or 10 bytes of decimal. O(n × target) is therefore
O(n × 2^bits), which is exponential in the input size. Concretely:

| target | bits | bytes to write it | table entries | table size |
|---|---|---|---|---|
| 1,023 | 10 | 4 | 1,024 | under 1 MB |
| 1,048,575 | 20 | 7 | 1,048,576 | 8 MB |
| 33,554,431 | 25 | 8 | 33,554,432 | 256 MB |
| 2⁴⁰ | 40 | 13 | 1.1 × 10¹² | **8 TB** |

Thirteen bytes of input, eight terabytes of table. That is what
"pseudo-polynomial" means, and it is the single most commonly misunderstood point in
the subject.

### The one line that matters

```go
for s := target; s >= v; s-- {   // descending
```

Ascending lets element i be used twice: reach sum s with it, then reach s+v from the
s it just created. That turns subset sum into unbounded knapsack, a different and
easier problem. `TestDPDescendingLoopMatters` writes out both versions and checks
that a single `3` cannot reach `6`.

### Counting is a different question

`SubsetSumCount` returns how many subsets work. Counting is not easier than finding,
and in general it is strictly harder: counting solutions is #P-complete, a class above
NP. Here the same table does it, because sums compose additively. That is a property
of this problem, not a general fact.

## Travelling salesman

### Two exact solvers

**Brute force**, O(n × n!). City 0 is fixed as the start, because a tour is a cycle
and rotating changes nothing, which alone divides the work by n. The other easy
factor of two, that a tour and its reverse cost the same, is **not** taken, because
`Distances` is allowed to be asymmetric and a solver that is silently wrong on
one-way streets is worse than one that is twice as slow.

**Held-Karp**, O(n² × 2ⁿ). The idea: the cost of the best path visiting a **set** of
cities and ending at j does not depend on the order it visited them in. So the state
is (set, endpoint) rather than (sequence), and there are 2ⁿ × n states instead of n!
sequences. That is the bitmask-DP trick in one sentence: collapse a permutation into
a subset.

| n | brute force | Held-Karp |
|---|---|---|
| 6 | 6.1 µs | 1.5 µs |
| 8 | 196 µs | 10.6 µs |
| 10 | 14.3 ms | 89.5 µs |
| 11 | 157 ms | |
| 12 | | 623 µs |
| 14 | | 3.69 ms |
| 16 | | 16.9 ms |
| 18 | | 74.7 ms |

At n=10, 159x. Brute force dies at 11; Held-Karp reaches 18 comfortably.

And both are still exponential. Held-Karp's wall is memory, not time: n=25 needs
25 × 2²⁵ entries, which is 6.7 GB.

### Heuristics

| | cost | what it does |
|---|---|---|
| `TSPNearestNeighbour` | O(n²) | always go to the closest unvisited city |
| `TSPNearestNeighbourBest` | O(n³) | run it from every start, keep the best |
| `TwoOpt` | O(n²) per pass | reverse any segment that shortens the tour |

Average excess over optimal, measured against Held-Karp on random Euclidean
instances:

| n | nearest neighbour | best start | then 2-opt |
|---|---|---|---|
| 5 | +4.3% | +1.2% | +0.2% |
| 7 | +7.4% | +1.6% | +0.1% |
| 9 | +10.1% | +2.1% | +0.3% |
| 12 | +13.6% | +3.5% | +0.7% |

Two things to take from that. The gap **widens with n**, so a heuristic measured only
on tiny instances looks better than it is. And running a greedy algorithm from every
starting point and keeping the winner is the cheapest possible improvement to it:
three quarters of the gap, for a factor of n in time.

**Averages hide the risk.** Over 20,000 random 5-to-8-city instances the worst single
nearest-neighbour result was **1.47x** optimal, and there is no constant factor it
stays within: the worst case grows as log(n). 2-opt takes that same instance all the
way back to optimal.

`TestNearestNeighbourHasNoGuarantee` uses that instance. Its first version put four
cities on a **line** and expected the heuristic to do badly, which it cannot: on a
line every tour is the same out-and-back walk, so the ratio was exactly 1.00 in every
case and the test asserted nothing while appearing to demonstrate something.

### Where the heuristics run

| n | nearest neighbour | best start | 2-opt |
|---|---|---|---|
| 10 | 342 ns | 2.3 µs | 779 ns |
| 100 | 11.6 µs | 1.51 ms | 49.3 µs |
| 1,000 | 1.12 ms | 1.07 **s** | 9.61 ms |

At n=1,000 nearest neighbour plus 2-opt costs 10.7 ms and no exact solver in the
world will finish. `TSPNearestNeighbourBest` at 1.07 seconds is O(n³) making itself
felt, and it is the one to drop first at scale.

### 2-opt assumes symmetry

The move takes edges (a,b) and (c,d) and replaces them with (a,c) and (b,d), which
requires **reversing** the segment between b and c. On an asymmetric matrix that
reversal changes the cost of every edge inside the segment, and the O(1) delta

```go
before := d[a][b] + d[c][e]
after  := d[a][c] + d[b][e]
```

is then wrong. The exact solvers handle asymmetry; `TwoOpt` does not, and
`TestAsymmetricDistances` covers the solvers on a one-way-street instance where the
reverse tour costs nine times as much.

## The Go-specific parts

**Bitmask subsets with `math/bits`.** `bits.TrailingZeros64` finds the lowest set bit,
`m &= m - 1` clears it, and `bits.OnesCount64` is a popcount instruction. Together
they make "iterate the members of this subset" a two-line loop that costs one
instruction per member.

**`&^` is AND NOT.** `mask &^= 1 << city` clears a bit. Go spells it as one operator
where C needs `&= ~`.

**Held-Karp's table is a flat `[]int`,** indexed `mask*n + j`, not `[][]int`. A slice
of slices for n=18 would be 262,144 allocations and a pointer chase per lookup.

**`unreachable = math.MaxInt / 4`,** not `math.MaxInt`. The DP adds a cost to a
tentative distance, and two sentinels added together must not wrap. Dividing by four
costs nothing and removes the whole class of bug.

**`permute` hands the caller the live slice, not a copy.** `TSPBrute` clones before
keeping it. Forgetting that is the classic bug, and it makes every stored permutation
identical to the last one, so `TestPermuteVisitsEveryPermutationOnce` checks the count
of **distinct** permutations rather than the count of calls.

**`Points` rounds to int.** Float distances make tests depend on exact
floating-point arithmetic, and a tie broken differently on another platform gives a
different, equally optimal tour. Integers remove the need for an epsilon.

**Sentinel errors.** `ErrNoSolution` means no answer exists. `ErrTooLarge` means one
exists and this function will not find it. Conflating them would hide the entire
subject of the package.

## How to run

```bash
go test ./pvsnp
go test -v -run 'Blowup|Growth|Quality|NoGuarantee' ./pvsnp
go test -run XXX -bench BenchmarkSubsetSum -benchtime 20x ./pvsnp
go test -run XXX -bench BenchmarkTSP -benchtime 10x ./pvsnp
```

`-benchtime 20x` and `10x`: the default one second per benchmark would run brute
force at n=26 twice and take a minute doing it.
