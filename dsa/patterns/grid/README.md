# Grid

## The tell

A **matrix** plus one of "connected regions", "reachable", "fewest steps", "flood", or
"spread".

A grid is a graph whose nodes are cells and whose edges are implicit: every cell is adjacent
to the four (or eight) around it. Recognising that is the pattern, because it means
everything in [dsa/graph](../../graph/) applies unchanged and nothing new has to be learned.

## BFS or DFS

Almost always decided for you:

| | |
|---|---|
| "fewest steps", "shortest path", "how many rounds" | **BFS** |
| "how many regions", "fill this area", "is it connected" | either; DFS is shorter |

BFS visits by distance, so the first time it reaches a cell it has done so by a shortest
route. DFS does not have that property and cannot be patched to have it.

## Multi-source BFS

The trick worth taking away from this package. When a problem starts from **several places
at once** ("every rotten orange spreads simultaneously", "distance to the nearest zero"),
push all the sources into the queue **before the loop starts**. That is the whole change.

```go
for row := range g {
    for col := range g[row] {
        if affected(g[row][col]) {
            distance[row][col] = 0
            queue = append(queue, Cell{row, col})   // every source, up front
        }
    }
}
```

The alternative, a separate BFS per source taking the minimum per cell, is
O(sources × cells). This is one pass. `TestMultiSourceBeatsPerSource` runs both on random
grids and checks they agree at every cell.

`Spread` and `DistanceToNearest` are both this, and `ExampleSpread` shows the payoff
directly: the same row of oranges takes 6 rounds from one source and 3 from two.

## The four ways to get a grid traversal wrong

**1. Forgetting bounds checks.** This panics rather than misbehaving, so at least it is
loud.

**2. Marking visited on dequeue rather than on enqueue.** A cell then enters the queue
several times, the queue grows to O(edges) instead of O(cells), and cells get processed
twice. Same bug as in the graph package, same diamond-shaped test catches it.

**3. Using the grid as the visited set when the caller still wants it.** `FloodFill` does
this on purpose and says so; `CountRegions` deliberately does not, because sinking the
island by overwriting it is a surprising thing for a function called `Count` to do.
`TestCountRegionsDoesNotMutate` holds that down.

**4. Treating a ragged slice-of-slices as rectangular.** Go will happily hand you a
`[][]int` whose rows differ in length. A single `col < len(grid[0])` check is wrong in a way
that panics only on the rows longer than the first, so `InBounds` checks `len(grid[row])`.
`TestRaggedGridsDoNotPanic` runs every function over a ragged grid.

## FloodFill's early return is required

```go
if original == replacement {
    return 0
}
```

Not an optimisation. The grid is the visited set, so filling to the colour already there
leaves no way to tell a filled cell from an unfilled one, and the search never terminates.
`TestFloodFillSameColour` is that case, and the test for it is that the call returns at all.

## Four-way or eight-way

`FourWay` means sharing an **edge**; `EightWay` adds the diagonals. Which one a problem
means is usually stated only by example, so check against the given test case before writing
anything.

It changes answers a lot. On this grid:

```
11000
11000
00100
00011
```

four-way finds **3** regions and eight-way finds **1**, because (1,1) touches (2,2)
diagonally and (2,2) touches (3,3). I traced that wrong the first time and the test caught
it.

## Perimeter is here to mark a boundary

"Island perimeter" mentions islands, so it looks like a traversal problem. It needs no BFS,
no DFS and no visited set: count each matching cell's four sides and subtract the ones
facing another matching cell. Reaching for a traversal because the problem mentions islands
is the mistake, which is why the function is in this package rather than left out of it.

## The Go-specific parts

**Every function takes a `match func(T) bool`,** so the same code answers "count the
islands", "count the open regions" and "count the regions of colour 3" without the grid
needing a particular element type. `[][]byte` is the natural choice for a grid you can write
as strings in a test.

**The fills are iterative with an explicit stack.** A 1000×1000 grid that is entirely one
region is a million-deep recursion. Go grows goroutine stacks so it survives, and the
explicit bound costs nothing here. That trade came out the other way in
[dsa/bst](../../bst/), where recursion measured faster and never allocated; the difference
is that a grid's depth is bounded by its size and a tree's is bounded by its shape.

**`ShortestPath` uses the distance grid as the visited set,** with −1 for unvisited. One
slice instead of two, and no class of bug where the two disagree.

## How to run

```bash
go test ./patterns/grid
```
