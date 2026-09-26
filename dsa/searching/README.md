# Searching

## Binary search is not about sorted arrays

That is the one idea here. Binary search needs a **monotonic predicate**: a question
about an index whose answer is false for a while and then true forever.

```
index      0      1      2      3      4      5      6
value      2      3      5      7     11     13     17
>= 7?    false  false  false  true   true   true   true
                               ^
                               the boundary, which is what we are finding
```

A sorted array is one way to get such a predicate, and not the only one. Once you
see it this way, every function in this package is two lines on top of one:

```go
func Partition(n int, pred func(int) bool) int
```

`LowerBound` supplies `s[i] >= target`. `FindPeak` supplies `s[i] > s[i+1]` on an
array with no order at all. `SearchAnswer` supplies a predicate with no array
involved. That is the whole package.

`Partition` is exactly `sort.Search`. It is written out because the loop invariant
is where every off-by-one bug lives:

```
pred is false for every index <  lo
pred is true  for every index >= hi
```

Both halves are vacuously true at the start, and the loop shrinks the gap without
breaking either. When `lo == hi`, that is the answer.

## The variants exist because of ties

With distinct elements there is one binary search and nothing to discuss. With
duplicates there are four questions, and conflating them is the other place the
bugs live.

For `[1, 3, 3, 3, 5, 7, 7, 9]` and target `3`:

| | | |
|---|---|---|
| `LowerBound` | first index with `s[i] >= 3` | 1 |
| `UpperBound` | first index with `s[i] > 3` | 4 |
| `Count` | `UpperBound - LowerBound` | 3 |
| `Range` | both, as a slice range | `s[1:4]` |

`LowerBound` and `UpperBound` are the primitives; `First`, `Last`, `Count` and
`Range` are one line each. All of them are **total**: they return an index even when
nothing matched, and that index is where the value belongs. A search that returns
`-1` for absent throws away information it already computed.

## Count, and why it is two searches

`Count` does two binary searches rather than finding one match and walking outwards.
The walk is O(k) in the number of matches. A million elements:

| | two bounds | walk outwards |
|---|---|---|
| all distinct | 42.8 ns | **30.4 ns** |
| 1,024 distinct values | **44.0 ns** | 594.6 ns |
| all identical | **46.9 ns** | 567,487 ns |

The walk is 1.4x faster when there is nothing to walk and **12,100x slower** when
there is. Two bounds is flat, which is the property worth having.

## The midpoint overflow is reachable in Go

`mid := lo + (hi-lo)/2`, not `(lo+hi)/2`. The usual advice, and the usual Go
rebuttal is that a slice can never be long enough: a slice of length maxint/2 would
need exabytes.

That rebuttal is wrong, and `TestMidpointOverflowIsReachable` proves it. A slice of
**zero-size elements** needs no memory:

```go
const huge = 3 << 61            // 6.9e18, over maxint64/2
s := make([]struct{}, huge)     // costs nothing, struct{} is 0 bytes

lo, hi := len(s)/2, len(s)-1
(lo + hi) / 2        // -4035225266123964416   wrapped
lo + (hi-lo)/2       //  5188146770730811391   correct
```

And indexing with the wrapped value panics:
`runtime error: index out of range [-4035225266123964416]`.

`Partition` handles that length fine, which is the point of the test. You will never
have such a slice. It is nice that the habit is defensible in Go rather than just
inherited from Java.

## Searching things that are not sorted

**`SearchRotated`.** A sorted slice rotated by an unknown amount is not sorted, so
`LowerBound` is useless on it. What is still true: at any midpoint, at least one half
is properly sorted, and a sorted half can be tested for containment with one range
check. So each step still discards half.

```
original  [1 2 3 4 5 6 7]
rotated   [4 5 6 7 1 2 3]     rotated left by 3
```

Two details that are easy to get wrong, and both cost me a failing test:

`s[lo] <= s[mid]`, not `<`. When `lo == mid`, which happens on every two-element
window, those are the same element and `<` is false for both branches. With `<` the
code fell through to the duplicate-handling path and returned `-1` for the present
target in `[3, 0]`.

Duplicates have to be checked **first**. When `s[lo] == s[mid] == s[hi]` there is no
way to tell which half is sorted, so the only option is to shrink by one from each
end. That makes `[2 2 2 2 2 0 2]` an O(n) case, and it is why this takes
`cmp.Ordered` rather than a promise of distinctness.

**`RotationPoint`.** The predicate is `s[i] <= s[len-1]`, false across the first run
and true across the second:

```
values     4  5  6  7  1  2  3
<= s[n-1]  F  F  F  F  T  T  T
                       ^
```

Comparing against the **last** element is what makes it monotonic. Comparing against
the first passes on every rotated input and fails on an unrotated one. And for a
slice rotated left by k the answer is `len-k`, not `k`, which I got backwards in the
test rather than the function.

**`FindPeak`.** The clearest demonstration of the whole idea. There is no ordering
to exploit, a linear scan is the obvious solution, and bisection works anyway. The
predicate is "am I descending yet", false while climbing and true afterwards. A peak
must exist between the two, because index 0 beats its imaginary left neighbour and
the last index beats its imaginary right one.

23 comparisons to find a peak in 1,048,576 elements.

## Linear search is not always the loser

Searching for every element of a sorted slice in turn, so the average target is in
the middle:

| n | linear | binary | slices.BinarySearch |
|---|---|---|---|
| 4 | **2.18 ns** | 4.96 | 5.15 |
| 8 | **2.72** | 5.78 | 6.06 |
| 16 | **4.00** | 6.50 | 6.70 |
| 32 | 7.68 | **7.11** | 7.28 |
| 64 | 17.54 | **7.69** | 7.97 |
| 128 | 37.17 | **8.97** | 9.26 |
| 1,024 | 272.7 | **29.59** | 34.01 |

The crossover is around **32 elements**. Below it, O(n) beats O(log n), because a
linear scan reads memory in the order the prefetcher expects and has no branch whose
direction depends on a comparison. Binary search jumps around, and at n=4 the whole
slice is in one cache line anyway.

This is the same effect that puts an insertion sort inside `slices.Sort`, and it is
why a linear scan over a small slice is not a thing to apologise for.

## Against the standard library

1,048,576 elements, random targets:

| | ns/op |
|---|---|
| `sort.SearchInts` | **89.3** |
| this package's `BinarySearch` | 109.8 |
| `slices.BinarySearch` | 114.6 |
| `slices.BinarySearchFunc` | 159.5 |

The oldest API is the fastest, and the reason is the comparison. `sort.SearchInts`
asks a two-way question (`s[i] >= x`) once per step. `slices.BinarySearch` uses a
three-way `cmp.Compare`, which does more work per step in exchange for reporting the
found flag without a final check. `BinarySearchFunc` adds a non-inlinable func value
on top, for the 1.4x seen elsewhere in this repo.

None of this matters at 110 nanoseconds. Call `slices.BinarySearch`.

## Exponential search

Double a bound until it passes the target, then bisect inside it. O(log i) in the
target's **position** rather than O(log n) in the length.

1,048,576 elements:

| target at index | exponential | binary |
|---|---|---|
| 0 | **2.18 ns** | 30.39 |
| 10 | **8.12** | 35.71 |
| 1,000 | **15.87** | 37.95 |
| 524,288 | 45.50 | **31.62** |
| 1,048,575 | 56.60 | **46.52** |

14x faster at the front, 1.4x slower in the middle. Worth it in two situations: the
target is usually near the front, or the length is unknown, as with a sorted stream
or a paginated API where you can ask for item k but not for a count.

## The Go-specific parts

**`Partition` takes `func(int) bool`, not a slice.** That is what lets `FindPeak` and
`SearchAnswer` reuse it with no array. It is also `sort.Search`'s signature, which
predates generics and needed no changing.

**`FindPeak[int](nil)`** needs the explicit type argument: Go cannot infer `T` from an
untyped `nil`.

**`SearchAnswer` shifts inclusive bounds into `Partition`'s `[0, n)` space** rather
than duplicating the loop. Getting two inclusive bounds right is the fiddly part, and
doing it once beats doing it per problem. It handles negative bounds, which the shift
is what makes true.

**A non-monotonic predicate does not error.** It returns a meaningless index. That is
the single most common way to misuse binary search and nothing in the type system
can catch it.

## How to run

```bash
go test ./searching
go test -v -run 'Overflow|Logarithmic' ./searching
go test -run XXX -bench BenchmarkCrossover ./searching
go test -run XXX -bench BenchmarkExponential ./searching
```
