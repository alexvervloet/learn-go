# Dynamic programming

## The tell

"The number of ways to…", "the minimum cost to reach…", "the longest common…". Underneath
all of them is **overlapping subproblems**: a recursive solution that solves the same
smaller problem many times.

If the subproblems do **not** overlap, it is divide and conquer, not dynamic programming.
Merge sort splits into halves that share nothing, so memoising it buys nothing.

## The two questions

Every problem here comes down to getting two things right, in this order:

1. **What is the state?** The smallest set of values that identifies a subproblem.
2. **What is the recurrence?** How a state's answer follows from smaller states.

Everything else, memoisation against tabulation and the space optimisations, is mechanical
once those two are settled. When a DP problem feels impossible it is almost always question
one that is wrong: **the state is missing a dimension.**

`LISQuadratic` is the clearest example. The state is "the longest increasing subsequence
*ending at* i", not "the longest so far", because the second one does not say what can be
appended to.

## Top-down or bottom-up

| | |
|---|---|
| **memoisation** (top-down) | write the recursion, add a cache. Closest to how you think, and it only computes the states actually needed. |
| **tabulation** (bottom-up) | fill a table in dependency order. No recursion, no hashing, and it is what lets the table shrink to one or two rows. |

Fibonacci is implemented four ways so the difference is visible in one place. At n=30:

| | ns/op | against naive |
|---|---|---|
| `FibNaive` | 4,030,412 | |
| `FibMemo` | 1,056 | 3,800x |
| `FibTable` | 283 | 14,200x |
| `FibRolling` | 21 | **193,000x** |

Two things in that table are worth more than the headline.

**Memoisation is 3.7x slower than tabulation**, because of the map hashing and the
recursion. Adding a cache to a recursive function is the easy conversion, and it is not the
fast one.

**The rolling version is another 13.6x faster than the table**, because the recurrence only
reaches back two entries so only two need keeping. That optimisation is available to
tabulation and not to memoisation, and it is the reason to do the extra work of finding the
dependency order.

What it costs: the table is gone, so the intermediate values are gone. Anything that must
reconstruct a path rather than report a total cannot do it, which is why
`LongestCommonSubsequence` and `Knapsack01` keep their full tables and `EditDistance` does
not.

## Why the naive version is that bad

`TestFibNaiveCallCount` counts the calls rather than timing them, which is exact:

| n | naive calls | memoised calls |
|---|---|---|
| 5 | 15 | 6 |
| 10 | 177 | 11 |
| 20 | 21,891 | 21 |
| 25 | 242,785 | 26 |

The count is exactly `2·Fib(n+1) − 1`, so it grows at the same exponential rate as the
answer. Fib(5) calls Fib(1) five times. **Those repeated calls are the overlapping
subproblems**, and spotting them is the whole pattern.

## The functions

| | |
|---|---|
| `FibNaive`, `FibMemo`, `FibTable`, `FibRolling` | one function, four techniques |
| `ClimbStairs` | Fibonacci wearing a hat |
| `CoinChangeWays` / `CoinChangePermutations` | combinations against permutations |
| `MinCoins` | fewest coins, where greedy is wrong |
| `LongestCommonSubsequence` | two-dimensional state, with reconstruction |
| `EditDistance` | Levenshtein, in O(min(m,n)) space |
| `Knapsack01` | the optimisation version of subset sum |
| `LISQuadratic` / `LIS` | O(n²) DP against O(n log n) patience sorting |
| `UniquePaths`, `MinPathSum` | grid paths on one row of state |
| `LongestPalindromicSubstring` | where the DP is beaten by something simpler |
| `WordBreak` | segmentation, with reconstruction |

## Loop order is not a detail

`CoinChangeWays` and `CoinChangePermutations` differ **only** in which loop is outside:

```go
// combinations: coins outside, so each coin is considered once for all amounts,
// and a combination is only ever built in one order
for _, coin := range coins { for sum := coin; sum <= amount; sum++ { ... } }

// permutations: amounts outside, so every ordering is counted separately
for sum := 1; sum <= amount; sum++ { for _, coin := range coins { ... } }
```

For coins `{1,2}` and amount 3: two combinations (`1+1+1`, `1+2`) and three permutations
(`1+2` and `2+1` are different). This is the most commonly conflated pair in the pattern,
so both are here and `TestCoinChangeLoopOrderMatters` checks that permutations are never
fewer than combinations across random inputs.

The same subtlety in a different disguise: `Knapsack01` iterates capacity **forwards** over
a full 2D table, but the one-row version must go **backwards**, or an item gets taken
twice. That is the identical trap as `SubsetSumDP` over in [pvsnp/](../../pvsnp/).

## Greedy is wrong, concretely

For coins `{1, 3, 4}` and amount 6, greedy takes 4, then 1, then 1: three coins. The answer
is 3+3: two. `TestGreedyCoinsIsWrong` asserts greedy does worse rather than just claiming
it.

Greedy works for every real currency, which is why the intuition survives, and it does not
work in general.

## The space optimisation is about memory, not speed

`EditDistance` keeps two rows; the textbook version keeps the whole m×n table. Measured
against each other on equal-length random strings:

| n | two rows | full table | memory |
|---|---|---|---|
| 100 | **24.2 µs**, 2.6 KB | 48.8 µs, 94 KB | 36x less |
| 1,000 | 3.50 ms, 24.6 KB | **2.99 ms**, 8.2 MB | 335x less |
| 4,000 | **38.6 ms**, 98 KB | 44.0 ms, 131 MB | 1,335x less |

The time is a wash past small n, and it loses at n=1,000. The memory is not a wash: 98 KB
against 131 MB at n=4,000, and it keeps diverging. That is the argument, and "it is also
faster" is not one.

## LIS, two ways

| n | O(n log n) | O(n²) | ratio |
|---|---|---|---|
| 100 | 1.9 µs | 4.4 µs | 2.3x |
| 1,000 | 18.3 µs | 503 µs | 27x |
| 10,000 | 274 µs | 87.4 ms | **319x** |
| 100,000 | 3.32 ms | | |

The fast version is not dynamic programming, which is why both are here. `tails[k]` holds
the **smallest possible** tail of an increasing subsequence of length k+1, and that slice is
always sorted, so each new element's position is a binary search.

Keeping the smallest possible tail is the insight: a smaller tail can be extended by
strictly more future elements, and it costs nothing, because the length is unchanged.

Reconstruction needs a parent pointer per element, because `tails` holds values rather than
positions and gets overwritten as it goes.

## Knapsack is pseudo-polynomial

| capacity | time | memory |
|---|---|---|
| 100 | 30.7 µs | 94 KB |
| 1,000 | 218 µs | 835 KB |
| 10,000 | 1.59 ms | 8.3 MB |
| 100,000 | 14.8 ms | 81 MB |

Exactly linear in the capacity's **value**. Ten times the capacity is ten times the work
for one more digit of input, which is the same point [pvsnp/](../../pvsnp/) makes about
subset sum at length. This is the optimisation version of that problem.

## Where the DP is not the answer

`LongestPalindromicSubstring` does **not** use the DP. The DP is O(n²) time and O(n²)
space; expanding around every centre is O(n²) time and O(1) space, and it is shorter. When
the obvious DP is beaten by something simpler, the something simpler belongs in the file
with a note saying so. (Manacher's algorithm does it in O(n) and is a different subject.)

The 2n−1 centres are the part to get right: n single-rune centres and n−1 gaps between
runes, because a palindrome can have even or odd length.

## Testing

Every function is checked against brute force on thousands of random inputs. Where an
answer is not unique, the test checks the **property** instead:

- `LongestCommonSubsequence` returns one of several possible answers, so the test verifies
  the returned string is a subsequence of both inputs, has the reported length, and that
  no longer common subsequence exists (by enumerating all 2ⁿ subsequences on short inputs).
- `EditDistance` is checked against the three properties of a metric: identity, symmetry,
  and the triangle inequality. Any of them failing means the recurrence is wrong.
- `Knapsack01` is checked against enumeration of all 2ⁿ subsets, and the returned items are
  re-weighed and re-valued against the reported total.
- `LIS` is checked against the quadratic DP, and the returned subsequence is verified to be
  strictly increasing and a real subsequence of the input.
- `WordBreak`'s segmentation is rejoined and compared with the input, and every segment is
  checked against the dictionary.

Two of the fixed expectations in that file were wrong when written, and the brute-force
checks are what caught them: my hand-computed knapsack answer was 35 short, and I claimed a
common subsequence of `naïve` and `naive` that used an `i` that `naïve` does not contain.

## How to run

```bash
go test ./patterns/dynamicprogramming
go test -v -run FibNaiveCallCount ./patterns/dynamicprogramming
go test -run XXX -bench BenchmarkFib -benchtime 20x ./patterns/dynamicprogramming
go test -run XXX -bench 'LIS|EditDistance' -benchmem -benchtime 20x ./patterns/dynamicprogramming
```
