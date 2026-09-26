# Binary search tree

One sentence holds the whole structure up: every key in a node's left subtree is
smaller than the node, and every key in its right subtree is larger.

```
        50
      /    \
    30      70
   /  \    /  \
 20   40  60   80
```

A search compares once per level and discards half the remaining tree, so it
costs O(height). On a balanced tree that is log₂(n): twenty comparisons for a
million keys.

Nothing in this package keeps the tree balanced. The shape depends entirely on
the order the keys arrived in, and that is the most important thing about it.

## What it degenerates into

`Put` the keys 0 through 9,999 in order and every node gets one child. The height
is 10,000, and a lookup is a linked-list walk.

| insertion order | height | lookup |
|---|---|---|
| shuffled | 30 | 39 ns |
| sorted | 10,000 | 6,772 ns |

**173x**, from input that looks like the friendliest possible case. Sorted input is
not a pathological case someone has to construct for you: it is what you get from
a database dump, a log file, an auto-increment ID, or a timestamp.

The failure mode is nasty because nothing is broken. `TestDegenerates` asserts it:
the chain is a valid search tree, `IsValid` passes, and the keys still come out
sorted. Everything is correct and everything is slow. See [redblack/](../redblack/)
for the fix, which is this structure plus the bookkeeping to keep the height at
log n whatever order the keys arrive in.

## Against the alternatives

100,000 integer keys, inserted shuffled. Apple M2 Max, Go 1.27:

| | BST | hash map | sorted slice |
|---|---|---|---|
| lookup | 100 ns | **8.7 ns** | 74.9 ns |
| iterate in sorted order | 997 µs | 5,493 µs | **27.9 µs** |
| range query, 100 results | 423 ns | 510,334 ns | **200 ns** |

Read honestly, the BST loses two of three and ties the third. The hash map is
11.5x faster at lookup. The sorted slice is faster at everything, by 35x on
iteration and 2x on a range query.

So the BST's case is not speed. It is that a sorted slice costs O(n) per
insertion to stay sorted, and the tree costs O(height). A static set of keys
belongs in a sorted slice; `slices.BinarySearch` is faster than any tree you can
write and it is three lines. A set that changes while you query it in sorted order
is what a tree is for.

And against a hash map the tree answers questions the map cannot answer at any
price:

| | |
|---|---|
| `Min`, `Max` | walk left or right until you cannot |
| `All` | every key in order, O(n), no comparisons |
| `Floor(k)` | largest key ≤ k |
| `Ceiling(k)` | smallest key ≥ k |
| `Range(lo, hi)` | every key in an interval, pruning whole subtrees |

`Floor` is the one to remember. "The most recent reading at or before this
timestamp" is a `Floor` query, and it is why a time-series index is a tree and not
a map.

## Deletion, the only part with any content

Three cases:

| | |
|---|---|
| no children | unlink it |
| one child | promote the child into its place |
| two children | copy the in-order **successor** over it, then delete the successor |

The successor is the smallest key in the right subtree. It is the only key that
can take the node's place without breaking the invariant: larger than everything
on the left, smaller than everything else on the right. By construction it has no
left child, so deleting it falls into one of the first two cases.

The predecessor works identically, and always picking the same side is what makes
repeated deletion skew a tree over time. A real implementation alternates.

`TestDeleteEverythingInEveryOrder` deletes 60 keys in 200 different random orders,
checking the full key list and the invariant after every single delete. Deleting
in one order and getting away with it proves nothing.

## IsValid, and the check that looks right and isn't

The obvious validity check is "each node's left child is smaller and right child
is larger". It accepts broken trees:

```
        50
       /
     30
       \
        60      <- larger than the root, in the root's LEFT subtree
```

Every local check passes. So each node is checked against the open interval it is
allowed to occupy, narrowed on the way down.
`TestIsValidCatchesTheNaiveCheck` builds exactly that tree.

## Recursive or iterative, measured

`Put` is iterative because insertion has nothing to do on the way back up. The
trick that keeps it short is tracking a `**node`, so the nil child slot can be
written through without carrying a parent pointer and a side:

```go
link := &t.root
for *link != nil { ... link = &current.left ... }
*link = &node[K, V]{key: key, value: value}
```

`All` is recursive, and that took three attempts to settle.

The first version kept an explicit stack, on the reasoning that a chain of
100,000 nodes would be 100,000 frames deep. Go grows a goroutine stack on demand
up to 1 GB, so that depth is fine, and the version I wrote also sized its stack
from `Height()`, which is itself an O(n) walk of the whole tree on every call. That
one line cost 1.83 ms per iteration instead of 997 µs and allocated 400 KB on a
deep tree.

The second measurement compared a recursive iterator against an explicit stack
with a plain `visit func` callback and showed the stack 3x ahead. That was
measuring range-over-func, not the traversal.

With both through `iter.Seq2`, 50,000 nodes:

| shape | recursive | explicit stack |
|---|---|---|
| balanced | 488 µs | 512 µs |
| chain of right children | 401 µs | **197 µs** |
| chain of left children | **459 µs** | 766 µs, 2.2 MB, 18 allocs |

The stack version wins 2x on a right-leaning chain, where it never holds more
than one node, and loses while allocating 2.2 MB on a left-leaning one, where it
holds all 50,000 nodes before yielding the first key. Recursion is within 5% on
the shape that actually occurs and never allocates, so it wins.

A separate number from the same benchmark: the same recursive walk with a plain
callback instead of an iterator runs at 452 µs against 488 µs, so
**range-over-func costs about 8%** here. Worth knowing, and not worth giving up
`for k, v := range t.All()` for.

## The Go-specific parts

**`K cmp.Ordered`** is the constraint, so `<` and `>` work directly on the key
type and the tree accepts any integer, float, or string without a comparison
function. `cmp.Compare` is used where a three-way result is wanted, in `Put` and
`Get`.

That constraint includes floats, and `cmp.Ordered` does not exclude NaN. A NaN key
compares false against everything, so it breaks the invariant. `cmp.Compare`
orders NaN below all other values, which is why `Put` uses it rather than raw
operators.

**The zero value works.** `var t Tree[int, string]` is an empty tree.

**`All`, `Range` and `LevelOrder` return `iter.Seq2`,** and each has to propagate
the `false` from `yield` up through the recursion. Missing that is the classic
range-over-func bug: a `break` lets the walk finish anyway and then panics on the
next `yield`. `TestAllStopsEarly` and `TestRangeStopsEarly` hold it down.

**`Range` is recursive and prunes.** `n.key > lo` before descending left and
`n.key < hi` before descending right is what makes it O(height + results) instead
of O(n).

## How to run

```bash
go test ./bst
go test -bench . -benchmem ./bst
go test -bench Degenerate ./bst        # the 173x
go test -bench TraversalStyle ./bst    # the table above
```
