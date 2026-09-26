# Union-find

## The tell

"Connected components", "merge these groups", "detect a cycle", "are these two in the same
group" — asked **as edges arrive**. That last part is the signal.

BFS or DFS answers all of those for a finished graph. Union-find answers them
**incrementally**, after every edge, with no rebuilding.

| | |
|---|---|
| you need the actual path | traversal |
| the question is only about connectivity | **union-find** |

## The structure

A forest, where each set is a tree and the root is the set's name. Two operations:

```
Find(x)      walk to the root of x's tree
Union(x, y)  point one root at the other
```

That is the entire data structure. Everything else is about keeping the trees short.

## Two optimisations, and both are needed

**Path compression**: on the way back from a `Find`, point every node passed at the root.

**Union by size**: when merging, attach the smaller tree under the larger root.

Together they give O(α(n)) amortised, where α is the inverse Ackermann function and is at
most 4 for any n that fits in the universe. Separately, each gives O(log n).

### Measuring it, and the benchmark that measured nothing

`TestOptimisationStepCounts` counts pointer hops, which is exact and needs no clock. 20,000
elements:

**Chain, `union(i-1, i)`** — the workload I reached for first:

| | hops per element |
|---|---|
| both | 2.00 |
| compression only | 2.00 |
| union by size only | 2.00 |
| **neither** | **2.00** |

All four identical. `union(i-1, i)` always makes the *new* element the smaller tree, so it
attaches under the existing root whether or not union-by-size is on, and the result is a
star either way. A benchmark that cannot tell the variants apart is worse than no benchmark,
and it is kept in the file as a labelled trap.

**Chain, `union(i, i-1)`** — the adversarial order, which hangs the big tree under the new
single node:

| | hops per element |
|---|---|
| both | 2.00 |
| compression only | 2.00 |
| union by size only | 2.00 |
| **neither** | **9,999.50** |

**Interleaved random unions and finds** — the realistic one, and the only shape that
separates all four:

| | hops per element | time (20,000 elements) |
|---|---|---|
| both | **3.42** | 1.78 ms |
| union by size only | 5.47 | 1.80 ms |
| compression only | 9.31 | 2.06 ms |
| neither | 3,053.98 | **82.7 ms** |

46x on time, 893x on hops. And the two single-optimisation variants **swap places** between
the two random workloads: with a burst of unions followed by many finds, compression-only
wins; interleaved, union-by-size wins. Which one matters more depends on the mix.

One more honest result: on the adversarial chain, union-by-size alone is *faster than both*
(108 µs against 122 µs), because path compression does extra pointer writes that buy nothing
once the tree is already a star. The hop counts are tied there; only the writes differ.

## The map costs 29x

`Sets` is backed by `map[T]T`, which is what lets it take any comparable key and add elements
implicitly. A slice-backed version over dense integer IDs does the same work in a fraction of
the time:

| | 20,000 elements |
|---|---|
| `Sets`, map-backed | 4.26 ms |
| slice-backed | **148 µs** |

29x, and none of it is the algorithm. If the elements are already integers from 0 to n−1, use
a slice. The map buys arbitrary keys and costs a hash per `Find` step.

## What it cannot do

There is **no Split and no Remove**. Union-find is one-way: sets only ever merge. A problem
that needs edges removed needs something else, usually a link-cut tree or an offline
algorithm that processes the removals in reverse.

It also has no notion of **direction**, so it cannot do directed cycle detection. Given
`a → c` and `b → c` it reports a cycle and there is none. That needs the three-colour DFS in
[dsa/graph](../../graph/).

## The applications

| | |
|---|---|
| `CountComponents` | components over nodes and edges |
| `HasCycle` | one line: an edge whose endpoints are already connected |
| `MinimumSpanningTree` | Kruskal's algorithm, which is union-find plus a sort |
| `MergeAccounts` | records sharing any identifier |

**`MinimumSpanningTree` returns a forest, not a tree.** On a disconnected graph there is no
spanning tree, and the cheapest spanning structure of each component is more useful than an
error. Callers needing a single tree compare `len(kept)` against `len(nodes)-1`.

The greedy choice is correct by the **cut property**: for any way of splitting the nodes in
two, the cheapest edge crossing the split is in some minimum spanning tree. Taking edges in
weight order means every edge kept is the cheapest crossing the split between what it joins.

**`MergeAccounts` is here for the modelling step, not the algorithm.** The naive approach
compares every pair of records, which is O(n²) and gets **transitivity** wrong: if A shares
an identifier with B and B with C, then A and C are the same even though they share nothing.

The trick is to put records and identifiers in the **same** structure and union each record
with its own identifiers. A shared identifier then pulls two records into one set without
them ever being compared, and transitivity comes for free.

## Two things that are easy to get wrong

**A representative is arbitrary, not meaningful.** It changes as sets merge. Code that stores
a `Find` result and compares it later is a bug, because the same set can have a different
representative after any `Union`.

**`Groups` sorts.** The underlying map has no order, so without it the result differs between
runs and no test can be written. That is the same decision as in
[dsa/hashmap](../../hashmap/) and [dsa/graph](../../graph/). It costs O(n log n) on a
structure whose operations are otherwise nearly constant, which is why it is a separate
function rather than something `Count` does.

## Testing

`TestMatchesNaiveGrouping` is the main one: keep a label per element, relabel on every union,
and check **every pair** agrees with `Connected` after **every** union. That is O(n²) per
step and obviously correct, which is what an oracle should be.

`TestMSTMatchesBruteForce` enumerates every subset of edges and finds the cheapest spanning
one. `TestHasCycleMatchesEdgeCounting` uses the identity that a forest of c components over n
nodes has exactly n−c edges, so more than that means a cycle.

## How to run

```bash
go test ./patterns/unionfind
go test -v -run OptimisationStepCounts ./patterns/unionfind
go test -run XXX -bench . -benchtime 20x ./patterns/unionfind
```
