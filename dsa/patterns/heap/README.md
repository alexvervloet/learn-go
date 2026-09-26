# Heap

## The pattern

Any question of the form **"the k largest"**, **"the k most frequent"** or **"the k
closest"** is a heap. The instinct is to sort and take the first k, which is
O(n log n). A heap of size k is O(n log k), and when k is small that is effectively
O(n).

The trick is counter-intuitive: **to find the k largest, keep a min-heap of size k.**
The smallest of your current best sits at the top, so deciding whether a new element
belongs is one comparison and evicting the loser is one pop. A max-heap would put the
wrong element where you can see it.

## What it buys

A million random ints:

| k | heap | sort and slice | heap is |
|---|---|---|---|
| 1 | 0.85 ms | 59.5 ms | **70x** faster |
| 10 | 0.87 ms | 58.7 ms | **68x** |
| 100 | 0.93 ms | 58.2 ms | **62x** |
| 1,000 | 1.46 ms | 59.0 ms | **40x** |
| 10,000 | 6.58 ms | 62.4 ms | **9.5x** |
| 100,000 | 44.4 ms | 60.1 ms | 1.35x |

Both columns are non-mutating, so `sort and slice` includes cloning the input. That
clone is about 1 ms of the 59; the sort is the rest.

The win holds up to k ≈ n/10, which is further than the complexity alone suggests it
should. The reason is in `KLargest`: one `Peek` comparison rejects most elements
without touching the heap at all, so the log k only gets paid by the survivors.

## The structure

The tree lives in the slice, with no pointers. The children of index i are at 2i+1 and
2i+2, and the parent of i is at (i−1)/2.

```
        index:   0    1    2    3    4    5
        value:   1    3    2    7    5    4

                      1
                    /   \
                   3     2
                  / \   /
                 7   5 4
```

That is the whole data structure, and it is why a heap allocates once where a tree
allocates per node.

**`From` heapifies in O(n), not O(n log n).** Sifting down from the last parent
backwards does almost no work for most nodes, because most nodes are near the leaves.

| n | `From` | push each |
|---|---|---|
| 100 | 929 ns | 1,111 ns |
| 10,000 | 106 µs | 152 µs |
| 1,000,000 | 9.96 ms | 15.4 ms |

**`PushPop` does one sift instead of two,** which halves the inner loop of every top-k
problem. When the pushed value would come straight back out, it does not touch the heap
at all.

## Against container/heap

`container/heap` predates generics. Using it means a type with five methods, two of them
taking and returning `any`, and three traps that compile silently:

- `heap.Push` and `yourType.Push` are different functions. Calling the second directly
  leaves the invariant broken and produces wrong answers with no error.
- Your `Pop` must return the **last** element, because `heap.Pop` swaps the minimum
  there first.
- `Push` and `Pop` need pointer receivers, or they append to a copy and silently do
  nothing.

The generic heap here has none of that, and **it is slower**: 2.04 ms against 1.39 ms
for 10,000 pushes and pops, a factor of 1.46.

That was not what I expected. `container/heap` calls `Less` on a concrete type, which
the compiler devirtualises and inlines down to `h[i] < h[j]`. This heap calls
`h.compare(a, b)` through a func field, which it cannot. It is the same 1.48x the
[sorting](../../sorting/) package measured between `slices.Sort` and
`slices.SortFunc`, for the same reason.

So: use this one for the ergonomics. If a profile blames the heap, write the five
methods.

## Running median, two heaps

"Report the median after every value in a stream". Keeping a sorted slice is O(n) per
insert; sorting per query is O(n log n). Two heaps are O(log n) per insert and O(1) per
query.

```
     lower (max-heap)        upper (min-heap)
     [1 3 5 |7|]             [|8| 9 12 20]
             ^                 ^
             the median lives between these two
```

20,000 values, median read after every one:

| | |
|---|---|
| two heaps | **1.65 ms** |
| binary search and insert into a sorted slice | 11.6 ms |
| re-sort every time (on a tenth of the input) | 58.8 ms |

`Add` is deliberately two steps: push onto whichever side the value belongs, *then*
rebalance. Doing both at once means a case analysis that is easy to get wrong; in
sequence each step is obviously correct.

`Median` returns the **lower** of the two middle values for an even count, because `T`
is `cmp.Ordered` and averaging is not defined for strings. `MedianPair` returns both,
and `MedianFloat` averages them for numeric types.

## MergeSorted, and a hypothesis that was wrong

Merging k sorted lists with a heap of k cursors is O(N log k) against
concatenate-and-sort's O(N log N). A million elements:

| k | heap | concatenate and sort |
|---|---|---|
| 2 | **9.1 ms** | 17.8 ms |
| 10 | 32.2 ms | **27.8 ms** |
| 100 | 77.3 ms | **37.6 ms** |

The heap loses from about k=10, which is backwards from the asymptotics.

My first explanation was cache thrashing across k memory streams. **That is wrong.**
Shrinking the total to 1,024 ints, so everything fits in L1, made the heap
*relatively worse*: 8.5x slower at k=100, against 1.75x at four million elements. If
cache were the mechanism, shrinking the working set would have helped it.

The actual reason is the cost of one comparison. The heap does log2(k) comparisons per
element, each an indirect call through a func field that then indexes twice
(`lists[c.list][c.at]`). `slices.Sort` does about log2(N) comparisons, each an inlined
`<`. That is roughly 7x per comparison, so the break-even is near
log2(k) = log2(N)/7, which for a million elements is k ≈ 6. The measurement agrees.

None of which retires the algorithm. It holds **only k cursors**, so it merges inputs
that do not fit in memory. That is what an external sort, a log aggregator and an
LSM-tree compaction need, and concatenating is not available to any of them.

## The Go-specific parts

**`NewMax` swaps its arguments rather than negating.** The tempting one-liner is
`cmp.Compare(-a, -b)`, and it is wrong for exactly one input: `-math.MinInt` overflows
back to `math.MinInt`, so `math.MinInt` ends up on top of a max-heap.
`TestNewMaxDoesNotNegate` asserts the broken version is broken. Negating the *result*
(`-cmp.Compare(a, b)`) is safe, because `cmp.Compare` only returns −1, 0 or 1.

**`Pop` zeroes the vacated slot** before shrinking the slice, so a `Heap[*Job]` does not
pin a job. The same leak as the stack and the queue, and just as invisible.

**`All` returns `iter.Seq` and is documented as unordered.** `TestAllIsNotSorted`
asserts that, because it is the kind of assumption that survives every small test.

**`KMostFrequent` breaks ties by value,** and the tie-break inside the heap is
*reversed* relative to the output order: the heap holds the survivors and evicts from
the top, so to keep the smallest value on a tie, the largest must be nearest the exit.
`TestKMostFrequentIsDeterministic` runs it 200 times, which is what proves map
iteration order is not leaking into the answer.

**`From` takes ownership of its slice.** That is what makes it allocation-free, and
`TestFromTakesOwnership` pins it down so nobody relies on the opposite.

## How to run

```bash
go test ./patterns/heap
go test -run XXX -bench KLargest -benchtime 30x ./patterns/heap
go test -run XXX -bench AgainstContainerHeap -benchtime 30x ./patterns/heap
go test -run XXX -bench MedianStream -benchtime 20x ./patterns/heap
```
