# Graph

Nodes and the edges between them. Almost every interesting problem is a graph
problem once you see it: a road network, a package dependency tree, a social
network, a state machine, a build pipeline, a maze. Same structure, and the same
six algorithms answer questions about all of them.

## Adjacency list or adjacency matrix

Two ways to store edges. The choice is about density, not taste.

```
  list                        matrix

  A -> [B, C]                    A  B  C  D
  B -> [D]                    A  0  1  1  0
  C -> [D]                    B  0  0  0  1
  D -> []                     C  0  0  0  1
                              D  0  0  0  0
```

Measured on 1,000 nodes at three densities. Apple M2 Max, Go 1.27:

| | list | matrix |
|---|---|---|
| `HasEdge`, degree 4 | 8.3 ns | **1.9 ns** |
| `HasEdge`, degree 50 | 43.1 ns | **1.9 ns** |
| `HasEdge`, degree 500 | 148.5 ns | **1.8 ns** |
| BFS, degree 4 | **95.8 µs** | 389.0 µs |
| BFS, degree 50 | **358.1 µs** | 668.2 µs |
| BFS, degree 500 | **2,268 µs** | 2,541 µs |

Memory, from `TestMemoryComparison`:

| nodes | degree | edges | list | matrix | matrix is |
|---|---|---|---|---|---|
| 100 | 4 | 393 | 8.7 KB | 80 KB | 9x |
| 1,000 | 4 | 3,987 | 88 KB | 8 MB | 91x |
| 10,000 | 4 | 39,995 | 880 KB | **800 MB** | 909x |
| 1,000 | 500 | 393,294 | 6.3 MB | 8 MB | 1x |

**The matrix wins exactly one thing, and it wins it decisively.** `HasEdge` is a
single array read at 1.9 ns whatever the density, where the list scans a slice and
degrades from 8 ns to 148 ns.

**It never wins the traversal.** Even at degree 500, where half the graph is
connected to every node, the list is 1.1x ahead, because the matrix has to scan
every one of 1,000 cells per node to find its neighbours while the list reads
exactly the edges that exist. At degree 4 that is a 4x loss.

So a matrix is for dense graphs where the question is repeatedly "are these two
adjacent". Floyd-Warshall all-pairs shortest paths is the canonical case: it is
O(V³) and touches every pair anyway. For anything sparse, 800 MB to hold 40,000
edges settles it.

This package is adjacency-list first, because real graphs are sparse.

## Determinism is a design decision

`Neighbours` returns edges in the order they were added, not in map order.

A graph has no inherent order, so BFS and DFS could legitimately return any of
several answers, and an implementation backed by `map[V]map[V]int` returns a
*different* one on every run. That makes tests and examples impossible to write,
and it hides real bugs behind "it is random anyway". Insertion order costs one
slice per node and one linear scan per `HasEdge`, and buys every output in this
package being reproducible.

`TestInsertionOrderIsPreserved` checks it twenty times in a row, which a
map-backed version would fail.

The cost is real and stated: `HasEdge` is O(degree) rather than O(1), and
`RemoveEdge` uses `slices.Delete` rather than the O(1) swap-with-last trick,
because that trick would reorder the edges.

## Traversals

| | container | good for |
|---|---|---|
| BFS | a **queue** | shortest path in an unweighted graph, level-by-level anything |
| DFS | a **stack** | cycle detection, topological sort, components, backtracking |

Swap the queue for a stack and breadth-first becomes depth-first. That is the
whole difference, and it means neither is harder to write than the other.

Three details that are easy to get wrong and hard to notice:

**BFS marks visited on enqueue, DFS on pop.** Marking on dequeue in BFS looks
equivalent and is not: a node with two in-edges gets queued twice before either
copy comes off, so it is visited twice and the queue can grow to O(E) rather than
O(V). `TestBFSVisitsEachNodeOnce` uses a diamond to catch it.

**An iterative DFS must push neighbours in reverse.** A stack pops in reverse, so
pushing forwards visits neighbours backwards. That is a correct depth-first order
and it looks like a bug in every test expectation. The recursive version needs no
such trick, and `TestDFS` asserts the two agree.

**`BFSLevels` records the queue's length before draining it.** Everything in the
queue at that instant is exactly one distance band, which is how "how many friends
of friends" gets answered without tracking a distance per node.

## Shortest paths

`ShortestPath` is BFS and counts **edges**. `Dijkstra` counts **weights**. On this
graph they disagree, which is the only reason both exist:

```
  A --1-- B --1-- C     three edges,  total weight 2
   \------10------/     one edge,     total weight 10
```

BFS is right about edges because it visits in distance order: the first time it
reaches the destination it has done so by a shortest route, since any shorter one
would have appeared in an earlier level. Nothing in that argument survives
weights.

Dijkstra is BFS with a priority queue: always expand the unvisited node with the
smallest known distance. That greedy choice is provably right *because* there are
no negative weights, since every detour only adds cost. A negative edge breaks
exactly that argument, so `Dijkstra` returns `ErrNegativeWeight` rather than a
plausible wrong answer. The fix for negative weights is Bellman-Ford, at O(V·E)
instead of O(E log V).

Both rebuild the path from a parent map rather than carrying it along the search. A
BFS that appends the path-so-far to every queue entry is a common shape and costs
O(V²) memory where O(V) would do.

`TestDijkstraPathIsWalkable` checks the returned path by walking it edge by edge
and adding the weights, because a correct cost with a path that does not exist is a
bug a cost-only assertion misses.

## container/heap shows its age

Dijkstra needs a priority queue, and Go's is the part of the stdlib that most
obviously predates generics:

```go
type priorityQueue[V comparable] []item[V]

func (pq priorityQueue[V]) Len() int            { ... }
func (pq priorityQueue[V]) Less(i, j int) bool  { ... }
func (pq priorityQueue[V]) Swap(i, j int)       { ... }
func (pq *priorityQueue[V]) Push(x any)         { ... }
func (pq *priorityQueue[V]) Pop() any           { ... }
```

Five methods, two inherited from `sort.Interface`, and `any` in the two that
matter. Three traps:

**`heap.Push` and `pq.Push` are different functions.** `heap.Push` appends through
your method and *then* sifts up. Your method must not sift. Calling `pq.Push`
directly compiles, leaves the invariant broken, and produces wrong answers with no
error anywhere.

**`Pop` returns the last element, not the smallest.** `heap.Pop` swaps the minimum
into the last position before calling your method, which makes the code look like
it returns the wrong end.

**`Push` and `Pop` need pointer receivers.** A value receiver on `Push` appends to
a copy, which compiles and silently does nothing.

One algorithmic shortcut worth naming. A textbook Dijkstra *decreases* an existing
entry's key. `container/heap` can do that with `heap.Fix`, but only if you track
every node's index in the heap. Pushing a second entry and skipping stale pops
instead is what almost every real implementation does: the heap holds up to O(E)
entries rather than O(V), and the log factor absorbs it.
`TestDijkstraStaleEntries` builds the case where a node is reached expensively
first and cheaply later.

## Topological sort and cycles

A topological order lists a directed graph's nodes so every edge points forwards.
It is what "install these packages in a valid order" and "which migrations run
first" reduce to, and **it exists if and only if there is no cycle.** So the two
algorithms are one algorithm read two ways: a sort that gets stuck has found a
cycle, and a detector that never gets stuck has found an order.

`TopoSort` is Kahn's algorithm: repeatedly take a node with no remaining incoming
edges, emit it, decrement its neighbours. Two decisions:

**The in-degree counts are copied.** The obvious implementation deletes edges as it
goes and destroys the input, which is a surprising thing for a function called
`Sort` to do. `TestTopoSortDoesNotMutateTheGraph` calls it twice and compares.

**The ready queue is FIFO.** Taking from the front keeps the output in node
insertion order among nodes that become ready together, which makes the result
reproducible. Every valid order is equally correct; only one of them is testable.

`HasCycle` on a directed graph needs **three** colours, not a visited set:

| | |
|---|---|
| white | not visited |
| grey | on the current DFS path |
| black | fully explored |

An edge to a **grey** node is a back edge, so a cycle. An edge to a **black** node
means two paths converged, which a diamond does and which is perfectly acyclic. A
two-colour visited set cannot tell those apart and calls every diamond a cycle.
That is the classic bug here, and `TestDiamondIsNotACycle` is aimed at it.

On an **undirected** graph every edge is a two-cycle by definition, so the question
becomes "is there a cycle other than an edge and its reverse", and the test is
whether a DFS reaches a visited node that is not the one it came from.
`TestHasCycleUndirected` starts with the single-edge case, which a naive directed
check reports as a cycle.

`FindCycle` returns the nodes, because a build tool that says "there is a
dependency cycle" is much less help than one that says which packages are in it.

## The Go-specific parts

**`V comparable`,** so nodes can be strings, ints, or any comparable struct. Not
`cmp.Ordered`: the graph never needs to order nodes, and requiring it would
exclude struct keys for no benefit.

**No usable zero value.** `New` and `NewDirected` are required, and this is the one
structure in `dsa/` where that is deliberate. A `Graph` that silently treats itself
as undirected when you meant directed is worse than a nil map panic.

**Sentinel errors** (`ErrNoPath`, `ErrNotFound`, `ErrNegativeWeight`, `ErrCycle`,
`ErrDirected`) rather than formatted messages, so callers can use `errors.Is`.
`ErrNotFound` and `ErrNoPath` are deliberately different: a node that is not in the
graph is usually a caller bug, and a node that is unreachable is usually an answer.

**Traversals return `iter.Seq`,** so a search can stop as soon as it finds what it
wants without building the full reachable set. Every one propagates `yield`'s
`false`; `TestTraversalsStopEarly` checks all four.

**`Components` refuses on a directed graph** rather than returning something
plausible. The directed equivalent is strongly connected components, which needs
Tarjan's or Kosaraju's algorithm and is a different function.

**The matrix is one flat `[]int`,** not `[][]int`. A slice of slices for 1,000
nodes is 1,001 allocations and a pointer chase per row; one flat slice is one
allocation and index arithmetic.

**`Matrix.BFS` uses `[]bool` for visited, not a map.** That is the other thing a
dense integer index buys: one byte per node with no hashing, against a map's ~50
bytes per entry and a hash per lookup. It is visible in the benchmark's `B/op`,
where the matrix allocates 43 KB against the list's 91 KB even while losing on time.

## How to run

```bash
go test ./graph
go test -bench . -benchmem ./graph
go test -v -run TestMemoryComparison ./graph   # the list-versus-matrix table
```
