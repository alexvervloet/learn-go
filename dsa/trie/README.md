# Trie

A prefix tree. It answers one question a hash map cannot: *what starts with
this?*

A map finds `golang` in O(1) and, to find everything beginning with `go`, has to
look at every key it holds. A trie walks two nodes and then has the answer as a
subtree.

```
        (root)
       /      \
      g        t
      |        |
      o*       o*
     / \        \
    a   l        p*
    |   |
    l*  a
        |
        n
        |
        g*

  * marks a terminal: go, goal, golang, to, top
```

Nothing in that tree stores a word. `go` is the path from the root through `g` to
`o`, and the node at the end carries a flag saying a word ends here. No node
stores its own character either: the character is the key in the parent's
children map, which saves the memory and means a lookup never compares
characters.

## The two flags that make it work

**`terminal`** is separate from "has no children", because `go` is both a word and
a prefix of `golang`. A terminal node can have children, and a leaf need not be a
word. `Contains` checks the flag; `HasPrefix` does not. That one line is the whole
difference between them.

**`words`** counts the terminals in each node's subtree, kept current on insert
and delete. Without it, `Count("pre")` has to walk the subtree. With it, the
answer is O(len(prefix)), and the measurement below shows that is worth a field
on every node.

## Deletion has two halves, and one of them is invisible

Clearing the terminal flag is the obvious half. The other is unlinking every node
on the path that now leads to no words at all, and a trie that skips it answers
every query correctly while growing without bound. No behavioural test catches
that, so `TestDeletePrunesDeadNodes` counts nodes directly.

A node survives the prune if it is terminal or has children, which covers both
directions: `go` must survive deleting `golang`, and `golang` must survive
deleting `go`.

`Delete` also has to walk the path down before walking back up, because a trie
node has no parent pointer. Adding one to support deletion would cost eight bytes
on every node forever, to save one small slice per delete.

## The measurements, and they are not flattering

20,000 words over a 26-letter alphabet with heavy prefix sharing, 42,189 nodes,
2,359 words under `pre`. Apple M2 Max, Go 1.27:

| Operation | Trie | Hash map | Sorted slice |
|---|---|---|---|
| exact lookup | 89.9 ns | **16.4 ns** | |
| `Count("pre")` | **10.3 ns** | 172 µs | |
| `Complete("pre")` | 492 µs | 413 µs | **28.6 µs** |
| build from 20k words | 6.95 ms | **426 µs** | |
| memory to build | 7.3 MB, 113k allocs | **874 KB, 65 allocs** | |

**The hash map wins exact lookup by 5.5x** and building by 16x. That is expected
and worth stating plainly: never reach for a trie to answer "is this word in the
set".

**The trie wins `Count` by 16,700x.** 10 nanoseconds against 172 microseconds,
because it walks three nodes and reads an integer while the map looks at twenty
thousand keys. Same for `HasPrefix`. This is the strongest argument in the
package, and it comes from the `words` field rather than from the tree shape.

**A sorted slice with binary search beats the trie at autocomplete by 17x.** This
is the result I did not expect, and it is the honest headline. `slices.BinarySearch`
to the first match, then walk while the prefix holds. 28.6 µs against 492 µs.

## Where those 492 microseconds go

| | |
|---|---|
| walking the subtree | 292 µs (60%) |
| sorting the results | 174 µs (36%) |
| collecting into a slice | 18 µs (4%) |

All three costs trace back to one decision, `map[rune]*node`:

**The walk is slow** because it iterates a map at every one of ~5,000 nodes, at
roughly 58 ns each.

**The sort is needed** because map iteration order is random, so without it
`Complete` would return a different order on every call over identical data.

**Every result has to be built** as a fresh string from the path, 2,359
allocations, where the sorted slice returns strings it already holds and
allocates 13 times in total.

`[26]*node` children fix the first two at once: array iteration is 8x faster and
comes out in alphabetical order, so no sort is needed at all.

| | map children | array children |
|---|---|---|
| lookup | 89.9 ns | **11.2 ns** |
| build | 6.95 ms | **2.29 ms** |
| memory | **7.3 MB** | 9.5 MB |

This trie keeps the map, and the reason is `TestUnicode`. `Complete("naï")` works,
and an array indexed by `c - 'a'` panics on the first non-ASCII character. A
fixed-alphabet trie is faster and only handles a fixed alphabet. Real
implementations that need both use a hybrid: an array for the common case and a
map for the rest.

## So when is a trie the right answer

| | Use |
|---|---|
| "is this in the set" | hash map |
| static word list, prefix search | sorted slice and binary search |
| how many / any words under this prefix | **trie** (`Count`, `HasPrefix`) |
| the set changes while you query it | **trie** (a sorted slice needs re-sorting) |
| wildcard patterns like `c.t` | **trie** (nothing else can) |
| longest registered prefix of an input | **trie** (`LongestPrefixOf`) |

`LongestPrefixOf` is the least famous method here and the most used in real code.
It is how an HTTP router matches `/users/42/posts` against a registered
`/users/`, how a phone network picks a carrier from a number, and how an IP
router picks a route.

## The Go-specific parts

**The zero value works.** `var t Trie` inserts fine; the first `Insert` allocates
the root.

**Keyed on runes, not bytes.** `for _, c := range word` yields runes, so a
multi-byte character is one node. Keyed on bytes, `naïve` splits `ï` across two
nodes, which happens to work for `Contains` and breaks every prefix operation.

**`WordsWithPrefix` returns `iter.Seq[string]`,** so a `break` stops the walk
instead of discarding a fully built slice. The recursive `collect` has to
propagate the `false` from `yield` at every level, which is the part of the
range-over-func contract a recursive iterator makes easiest to get wrong: ignore
it and the walk finishes anyway, then panics on the next `yield`.

**`collect` reuses one `[]rune` buffer,** appending on the way down and truncating
on the way back up. The first version built a fresh `strings.Builder` per child,
which reads better and allocated 4.4x more (10,473 allocations against 2,373).
It was not faster, because the walk is dominated by map iteration, not by string
building. The rewrite was still right, for the GC pressure rather than the clock.

## How to run

```bash
go test ./trie
go test -bench . -benchmem ./trie
go test -bench NodeLayout -benchmem ./trie   # map children against [26]*node
```
