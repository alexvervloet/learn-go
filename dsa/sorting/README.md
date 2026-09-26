# Sorting

Six algorithms in one package, benchmarked against each other and against the
standard library.

## Use slices.Sort

That is the real answer, and the numbers here say so: `slices.Sort` beats the best
hand-written algorithm in this package by 1.6x on random input and by 46x on
sorted input. The reason to write the six first is that everything `slices.Sort`
does is visible in what the six get wrong.

| algorithm | best | average | worst | memory | stable |
|---|---|---|---|---|---|
| bubble | O(n) | O(n²) | O(n²) | O(1) | yes |
| insertion | O(n) | O(n²) | O(n²) | O(1) | yes |
| selection | O(n²) | O(n²) | O(n²) | O(1) | no |
| merge | O(n log n) | O(n log n) | O(n log n) | O(n) | yes |
| quick | O(n log n) | O(n log n) | O(n²) | O(log n) | no |
| heap | O(n log n) | O(n log n) | O(n log n) | O(1) | no |

Three of those cells are the whole story. Insertion sort's O(n) best case is why
it is *inside* every production sort. Quicksort's O(n²) worst case is why pdqsort
keeps a fallback. Merge sort's O(n) memory is why it is not the default despite
being the only stable one of the three fast algorithms.

## Comparison counts, exactly

Timing is noisy; comparison counts are not. The compare function is a parameter,
so the tests count calls, and these are exact numbers at n=500:

| input | insertion | selection |
|---|---|---|
| sorted | **499** | 124,750 |
| nearly sorted | 563 | 124,750 |
| random | 64,090 | 124,750 |
| reversed | 124,750 | 124,750 |

Two algorithms with the same O(n²) average, and one of them spans **250x** across
inputs while the other does not move at all. Selection sort's scan for the minimum
cannot be cut short, so it has no best case. Insertion sort's inner loop stops as
soon as it finds a smaller element, and 499 is exactly n−1.

That is why insertion sort is inside `slices.Sort` and selection sort is inside
nothing.

Merge sort has the same property, for a different reason: at n=4,096 it uses 4,095
comparisons on sorted input against 46,267 on random. The line responsible is

```go
if compare(s[mid-1], s[mid]) <= 0 {
    return   // the halves are already in order, so skip the merge
}
```

## Where insertion sort wins

Random input, time per sort including the restore copy:

| n | insertion | quick | merge | heap | slices.Sort |
|---|---|---|---|---|---|
| 8 | **41.9 ns** | 78.0 | 78.5 | 108.0 | 24.8 |
| 16 | **112 ns** | 180 | 194 | 297 | 65 |
| 32 | **352 ns** | 451 | 496 | 779 | 144 |
| 64 | 1,201 ns | **1,075** | 1,185 | 1,828 | 372 |
| 128 | 5,208 ns | **2,664** | 2,711 | 4,394 | 938 |

The crossover is somewhere around 48 elements. Below it the O(n log n) algorithms
lose to an O(n²) one, because their recursion, their pivot selection and their
buffers are all overhead that n=32 does not amortise. Every production sort
contains an insertion sort for exactly this reason, and this package's merge and
quick both switch to one at 12 elements.

## At 100,000 elements

| shape | merge | quick | heap | slices.Sort | slices.SortFunc | SortStableFunc |
|---|---|---|---|---|---|---|
| random | 9.88 ms | 10.08 | 14.89 | **6.34** | 9.58 | 21.60 |
| sorted | 0.33 | 4.20 | 10.61 | **0.09** | 0.26 | 0.39 |
| reversed | 3.31 | 4.28 | 10.59 | **0.12** | 0.29 | 3.27 |
| nearly sorted | 0.71 | 4.34 | 10.60 | 1.14 | 3.61 | **0.51** |
| duplicates | 4.71 | 1.11 | 8.42 | **0.63** | 1.37 | 5.04 |

Things worth taking from that table:

**Heapsort is the slowest of the three fast algorithms on every shape**, and its
comparison count is not the reason. Sifting jumps from index i to 2i+1, which for
a large slice is a different cache line at every level. Quicksort's partition walks
two pointers linearly through memory, which is the access pattern hardware is built
for. Heapsort earns its place by having no bad case at all, which is why pdqsort
keeps it as the bail-out.

**Merge sort beats quicksort on four of five shapes**, because of the skip-the-merge
line, and loses on duplicates where three-way partitioning wins outright.

**`SortStableFunc` beats `slices.Sort` on nearly sorted input** (0.51 ms against
1.14 ms) and is 3.4x slower on random input. It is a block merge sort, which is a
different algorithm and not just a flag.

**Merge sort beats `slices.Sort` on nearly sorted input,** 0.71 ms against 1.14 ms,
and it is the only cell in the table where anything here beats the stdlib. The
stdlib's own `SortStableFunc` then beats both at 0.51 ms. So the one shape where a
hand-written algorithm wins is a shape the standard library already has a better
answer for.

## Why slices.Sort wins, measured

It is the comparison function, not the algorithm. At n=8:

| | ns/op |
|---|---|
| insertion sort, `>` inlined, no func value | **23.3** |
| `slices.Sort` | 24.8 |
| `InsertionFunc(s, cmp.Compare)` called directly | 41.1 |
| `quickSort(s, cmp.Compare, …)`, one level deeper | 77.0 |

`slices.Sort` is within 6% of a hand-written insertion sort with the operator
inlined. Passing `cmp.Compare` as a func value costs **76%** even when the compiler
can see it. Passing it through one more call, so it arrives as an opaque parameter
and cannot be devirtualised, costs **230%**.

At 100,000 elements the same effect is smaller but still decisive:

| | ns/op |
|---|---|
| `slices.Sort` (operator inlined) | 5,132,310 |
| `slices.SortFunc` with `cmp.Compare` | 7,582,243 |
| `sort.Slice` with a closure | 8,468,309 |

1.48x for the func value, 1.65x for a closure. Everything in this package pays that
1.48x, by design, so stability is observable and so the signature matches
`slices.SortFunc`.

## The quicksort bug this package had

Quicksort has two tuning decisions, and getting one of them wrong produced a bug
that no test caught and only a benchmark exposed.

**Three-way partitioning** (Dutch national flag) puts equal elements in the middle
and never looks at them again. Two-way partitioning puts them on one side, so a
slice of identical values splits n/0 every time, at O(n²).

**The pivot** was the median of the first, middle and last elements, which is the
textbook choice. With a three-way partition it is not safe, and the reason is
subtle. The partition moves large elements to the tail by swapping them with the
shrinking right boundary, and on sorted input that rotation leaves the right half
in a specific shape:

```
sorted 1..1000, pivot 501   ->   right half is [503, 504, ..., 1000, 502]
```

Sorted ascending, except the **smallest** element is now last. Median-of-three then
samples first, middle and last, and one of its three samples is the minimum, so the
median of the three is the *second smallest* value in the subarray. The split is
1/1/497. The same thing happens at the next level, so recursion depth goes from
log(n) to **√n**.

Nothing failed. Every test passed, the output was correct, and the only symptom was
that sorted input benchmarked 46% slower than random input.

The fix is **Tukey's ninther**: the median of the medians of three groups of three,
spread across the slice. Nine samples instead of three, so one anomalous position
cannot carry the result. Partitions consumed along the deepest path, before and
after:

| n | median of three | ninther |
|---|---|---|
| 1,000 | 31 | **10** |
| 10,000 | 105 | **14** |
| 100,000 | over 200 | **18** |

And what those two decisions are worth against a first-element pivot with two-way
partitioning, at n=3,000:

| shape | naive | tuned |
|---|---|---|
| random | 196 µs | 194 µs |
| sorted | 9,663 µs | **97 µs** |
| reversed | 10,456 µs | **105 µs** |
| duplicates | 3,317 µs | **21 µs** |

Nine comparisons for the pivot instead of three costs nothing measurable on random
input and turns three common shapes from unusable into the fastest cases in the
table.

## The depth budget

The third thing pdqsort does: count partitions, and switch to heapsort if the
budget runs out. That converts quicksort's O(n²) worst case into a guaranteed
O(n log n) at the cost of being slower than a good quicksort on that input.

This also had a bug. The budget was recomputed from the *current* subslice as
`2*ilog2(len(s))`, which tightens as the recursion descends while the depth grows.
A 13-element subslice allows 6 partitions, and a balanced quicksort reaches
13-element subslices at depth 13, so **the fallback fired on every input**: 1,909
unintended heapsort calls on 100,000 random elements.

Computing it once from the original length fixes it.
`TestQuickDoesNotFallBackOnNaturalInput` is the test that would have caught it, and
it only exists because the fallback is a *parameter* of `quickSort` rather than a
direct call to `HeapFunc`:

```go
quickSort(s, cmp.Compare, budgetFor(len(s)), func(sub []int, c Compare[int]) {
    fallbacks++
    HeapFunc(sub, c)
})
```

`TestQuickFallbackWorks` runs the other side, with a budget of zero, because an
untested guarantee is not one.

## Merge sort's two optimisations, priced

100,000 random elements:

| | time | allocations |
|---|---|---|
| one buffer, insertion below 12 | **7.81 ms** | 1, totalling 803 KB |
| one buffer, no threshold | 8.75 ms | 1 |
| a buffer per merge, no threshold | 10.37 ms | **32,767, totalling 12.7 MB** |

The buffer reuse is worth 1.33x and the threshold 1.12x. The allocation count falls
by 32,767x and the time only by 1.33x, because Go's allocator is fast and these
buffers die young. Worth doing, and not worth the 2.4x I guessed before measuring.

## Stability

A stable sort keeps equal elements in their original order. It matters whenever you
sort twice: sort by name, then stably by department, and you get departments
alphabetical with names alphabetical inside each. An unstable sort throws the first
pass away.

Testing it needs a value with a tag, because two equal integers swapping is
invisible. `TestStability` uses 60 elements over 4 keys, which is deliberately
above merge and quick's insertion-sort threshold: below it they would fall through
to insertion sort and *look* stable.

Both stable algorithms hang on one character:

```go
// merge: on a tie the LEFT half wins, and it holds the elements that came first
if compare(buf[left], buf[right]) <= 0 { ... }

// insertion: stop on an element that is not GREATER, so equal elements never swap
for ; j >= 0 && compare(s[j], current) > 0; j-- { ... }
```

Changing either `<=` to `<`, or either `>` to `>=`, leaves every ordering test
passing and breaks stability.

## The Go-specific parts

**Every algorithm takes `Compare[T] = func(a, b T) int`,** the same signature as
`slices.SortFunc`, so a call site can be swapped between them with no other change.
The `cmp.Ordered` wrappers are the convenience form, and
`TestFuncAndOrderedAgree` checks they are exactly the `Func` version with
`cmp.Compare`.

**Benchmarking a sort needs the input restored between iterations,** or the second
iteration sorts sorted data and every one after measures the best case. The copy is
*inside* the timer here: `b.StopTimer` costs roughly a microsecond per call, which
is fine for a 5 ms sort and swamps a 50 ns one. With `StopTimer` the n=8 benchmarks
had to be killed. `BenchmarkCopyOnly` measures the copy at each size: 2.8 ns at
n=8, 18 µs at n=100,000, so under 0.5% where it matters and about 7% at n=8.

**`ilog2` uses bit shifts rather than `math.Log2`,** to keep a float conversion out
of the recursion.

**Quicksort recurses into the smaller side and loops on the larger.** That keeps the
stack at O(log n) even with uneven splits; recursing into both is O(n) in the worst
case, which is a crash rather than a slow sort.

## How to run

```bash
go test ./sorting
go test -v -run 'BestCase|NoBestCase|Skips' ./sorting     # the comparison counts
go test -run XXX -bench BenchmarkSmall -benchmem ./sorting
go test -run XXX -bench BenchmarkFast -benchtime 50x ./sorting
go test -run XXX -bench QuickVariants -benchtime 50x ./sorting
```

`-benchtime 50x` on the large benchmarks: the default one second per benchmark
across five shapes and seven algorithms takes a while, and 50 iterations of a 10 ms
sort is plenty.
