# Red-black tree

A balanced binary search tree. Height stays within 2·log₂(n) whatever order the
keys arrive in, which is the one thing [bst/](../bst/) cannot promise.

That package shows the problem. Sorted keys into an ordinary search tree produce a
linked list, and sorted keys are what you get from a database dump, a log file, or
an auto-increment ID.

| 100,000 sorted keys | height | lookup |
|---|---|---|
| plain BST | 100,000 | linear |
| red-black | **17** | 36.6 ns |

17 is `log₂(100,000) + 1`. For sorted input this tree comes out *optimally*
balanced.

## It is a 2-3 tree in disguise

A 2-3 tree balances itself by letting a node hold either one key or two, and by
growing upward when a node splits rather than downward. That is easy to reason
about and irritating to implement, because a node has two shapes.

The trick is to represent a two-key node as two ordinary nodes joined by a link
painted **red**:

```
   2-3 tree node          red-black representation

      (30 50)                    50
      /  |  \                   /
     a   b   c                30          <- red link to its parent
                             /  \
                            a    b    c
```

Every node is now the same shape, and red means *this node is really part of its
parent*. **Left-leaning** means a red link always goes to the left child, which
halves the cases to handle. That is the only difference from the classic
formulation, and it is what makes this version short enough to hold in your head.

## Three rules

1. No two red links in a row.
2. No red link leans right.
3. Every path from the root to a nil link crosses the same number of **black**
   links.

Rule 3 does the work. On its own it would force a perfectly balanced tree, since
red links would not exist. Red links let one path be longer than another, and rule
1 caps by how much: at most double. Hence height ≤ 2·log₂(n).

Rule 3 is also the only one whose violation cannot be seen locally, which is why
`IsValid` checks it with a whole-tree walk and the other two with a node-local
one.

## Three operations maintain all of it

| | |
|---|---|
| `rotateLeft` | a red right link becomes a red left link |
| `rotateRight` | a red left link becomes a red right link |
| `flipColors` | two red children become one red parent, a 2-3 node splitting |

Insertion walks down, attaches a **red** node, and applies those three on the way
back up in a fixed order:

```go
func balance(h *node) *node {
    if isRed(h.right) && !isRed(h.left)  { h = rotateLeft(h) }
    if isRed(h.left)  && isRed(h.left.left) { h = rotateRight(h) }
    if isRed(h.left)  && isRed(h.right)  { flipColors(h) }
    return h
}
```

That order is not arbitrary. Step 2 can only produce the situation step 3 handles,
and step 3 can only produce a violation at the *parent*, which the parent's own
call to `balance` will see. That is the entire induction, and it is why insertion
is four lines.

New nodes are red because a red link does not change any black height, so
attaching one cannot break rule 3. It can break rules 1 and 2, and those are local.

The tree gets taller only when `flipColors` reaches the root and `Put` paints it
black again. **A red-black tree grows from the root, never from the leaves.**

## Deletion is the hard part, honestly

The strategy is one sentence: **make sure the node being removed is red before
removing it.** A red node contributes nothing to any black height, so unlinking it
cannot break rule 3.

Two more transformations provide that guarantee, running on the way *down*:

| | |
|---|---|
| `moveRedLeft` | the walk is heading left and the left child has no key to spare, so borrow one from the right sibling or the parent |
| `moveRedRight` | the mirror image |

`flipColors` is written as `h.color = !h.color` rather than with an explicit
colour precisely so it can run in both directions: splitting a node on the way up
during insertion, and merging one on the way down during deletion.

`del` dereferences `h.left.left` without a nil check, which is safe only because
`Delete` confirms the key is present first. That lookup is not redundant; it is
what every dereference in the function rests on.

## The measurements

10,000 to 100,000 integer keys, Apple M2 Max, Go 1.27.

**Sorted input**, which is the case that matters:

| | height | lookup | build |
|---|---|---|---|
| red-black, n=100,000 | 17 | **36.6 ns** | |
| plain BST, n=10,000 | 10,000 | 5,978 ns | |
| red-black, n=10,000 | 14 | | **869 µs** |
| plain BST, n=10,000 | 10,000 | | 72,570 µs |

The lookup rows use different n because a linear scan of 100,000 nodes makes the
benchmark run for minutes. The balanced tree holds **ten times more keys** and is
still 163x faster to search. Building is 83x faster, because inserting sorted keys
into a chain is O(n²).

**Shuffled input**, which is the case people quote:

| | height | lookup |
|---|---|---|
| red-black | 23 | 92.4 ns |
| plain BST | 38 | 98.5 ns |
| sorted slice | | 74.7 ns |
| hash map | | **8.7 ns** |

**7%.** A search tree built from random keys is already close to balanced, with an
expected height around 2·ln(n) ≈ 23 for 100,000 keys, and all the balancing
machinery buys almost nothing. Anyone benchmarking a balanced tree on random input
and concluding it is not worth the complexity has measured the one case where they
are right.

**What balancing costs**, 10,000 shuffled keys:

| | red-black | plain BST |
|---|---|---|
| build | 1,307 µs | **634 µs** |
| build and delete every key | 3,881 µs | **1,150 µs** |
| memory | 480 KB | **320 KB** |

Rotations and colour flips are not free: 2.1x on insertion and 3.4x on deletion.
The trade is paying that, always, to never hit the 163x.

For comparison, a `map[int]int` builds the same 10,000 keys in 305 µs, 4.3x faster
than this tree and 2.1x faster than the unbalanced one. Sorted iteration is where
the tree wins: 742 µs against 5,286 µs for collecting a map's keys and sorting
them.

## One bool costs 16 bytes per node

480 KB against the BST's 320 KB for 10,000 nodes is 48 bytes against 32, and the
only difference between the two node types is `color bool`.

| | |
|---|---|
| four 8-byte fields | 32 bytes, lands in the 32-byte size class |
| plus one bool | 40 bytes after alignment padding, lands in the **48**-byte class |

Go's allocator rounds every allocation up to a size class, and 33 through 48 all
cost 48. So a struct that fits a class exactly is a good place to be, and one byte
past it costs sixteen. `TestNodeSizeAndAlignment` asserts the struct sizes and
`TestAllocationPerNode` measures the 48 from the other direction, through
`runtime.MemStats`.

A production implementation steals the colour bit from a pointer, since node
addresses are 8-byte aligned and the low three bits are always zero. That is also
why production implementations of this are unreadable.

## Testing a balanced tree

The mistake worth avoiding: a red-black tree with broken colours still answers
every query correctly. It is an unbalanced tree wearing the name, and the bug
shows up months later as a performance regression nobody can explain.

So `IsValid` checks all three rules plus the ordering plus the node count, and the
tests call it after **every single mutation**, not once at the end:

- `TestInvariantsHoldAfterEveryInsert` — 30 trials of 150 random inserts, checking
  after each one.
- `TestInvariantsHoldAfterEveryDelete` — 30 trials of 120 random deletes, checking
  the full key list and all three rules after each one.
- `TestIsValidCatchesBrokenColours` — takes a valid tree, breaks only a colour, and
  asserts that `is23` or `isBalanced` catches it while `isOrdered` still passes.
  A test for the test.
- `TestNoRedRightLinks` — the left-leaning property itself, on 2,000 random keys.
- `TestDeleteStaysBalanced` — deletes the lower half of 20,000 keys in ascending
  order, the worst pattern for an implementation that always promotes the
  successor.

## The Go-specific parts

**`red = true` and `black = false`** as untyped constants, with the colour stored
on the child, meaning "the link from my parent to me". A node has exactly one
parent link, which is what lets a single bool carry it.

**`isRed` is a function, not a field read,** so `nil` counts as black and no rule
above needs a nil check.

**`put` returns `(*node, bool)`** rather than asking `Contains` first. The first
version did ask, and that second walk down the tree cost 26%: 1.77 ms against
1.31 ms for 10,000 keys. `Delete` still asks, because there the lookup is load
bearing.

**`All` and `Range` return `iter.Seq2`,** propagating `yield`'s `false` up through
the recursion, the same as in bst.

**The generic functions are free functions, not methods,** because Go does not
allow extra type parameters on methods and `rotateLeft` needs `[K, V]` from
somewhere. Free functions on `*node[K, V]` are the idiomatic way around it.

## How to run

```bash
go test ./redblack
go test -bench . -benchmem ./redblack
go test -v -run Balanced ./redblack       # the heights, logged
go test -v -run NodeSize ./redblack       # the 48 bytes
```

## Credit

The left-leaning formulation is Robert Sedgewick's, from *Left-leaning Red-Black
Trees* (2008) and *Algorithms, 4th edition*. The insight that matters is that
insisting red links lean left cuts the case analysis roughly in half, which turns
deletion from something you look up into something you can derive.
