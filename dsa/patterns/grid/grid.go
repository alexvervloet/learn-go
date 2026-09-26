// Package grid implements the grid traversal pattern: BFS and DFS over a matrix.
//
// # The tell
//
// A MATRIX plus one of "connected regions", "reachable", "fewest steps", "flood", or
// "spread". A grid is a graph whose nodes are cells and whose edges are implicit: every
// cell is adjacent to the four (or eight) around it. Recognising that is the pattern,
// because it means the graph algorithms apply unchanged.
//
// # BFS or DFS
//
// The same choice as in dsa/graph, and here it is almost always decided for you:
//
//	"fewest steps", "shortest path", "how many rounds"  ->  BFS
//	"how many regions", "fill this area", "is it connected"  ->  either, DFS is shorter
//
// BFS visits by distance, so the first time it reaches a cell it has done so by a
// shortest route. DFS does not have that property and cannot be patched to have it.
//
// # Multi-source BFS
//
// The trick worth taking away from this package. When a problem starts from SEVERAL
// places at once ("every rotten orange spreads simultaneously", "distance to the nearest
// zero"), push all the sources into the queue before the loop starts. That is it. The
// alternative, running a separate BFS per source and taking the minimum, is O(sources *
// cells); this is one pass.
//
// # The four ways to get a grid traversal wrong
//
//  1. Forgetting bounds checks, which panics rather than misbehaving, so at least it
//     is loud.
//  2. Marking visited on DEQUEUE rather than on enqueue, which lets a cell enter the
//     queue several times. The queue grows to O(E) and cells get processed twice.
//  3. Using the grid itself as the visited set when the caller needs it afterwards.
//  4. Treating a ragged slice-of-slices as rectangular. Go will happily hand you
//     [][]int with rows of different lengths, so bounds are per row.
package grid

// Direction offsets. Four-way is the default: two cells are adjacent when they share an
// EDGE. Eight-way adds the diagonals, and which one a problem means is usually stated
// only by example, so it is worth checking against the given test case before writing
// anything.
var (
	FourWay  = [][2]int{{-1, 0}, {1, 0}, {0, -1}, {0, 1}}
	EightWay = [][2]int{
		{-1, -1}, {-1, 0}, {-1, 1},
		{0, -1}, {0, 1},
		{1, -1}, {1, 0}, {1, 1},
	}
)

// InBounds reports whether (row, col) is inside grid.
//
// The per-row length check is not paranoia: Go allows a [][]T whose rows differ in length,
// and a single `col < len(grid[0])` check is wrong for such a grid in a way that panics
// only on the rows that are longer than the first.
func InBounds[T any](grid [][]T, row, col int) bool {
	return row >= 0 && row < len(grid) && col >= 0 && col < len(grid[row])
}

// Cell is a position in a grid.
type Cell struct {
	Row, Col int
}

// CountRegions returns the number of connected regions of cells for which match is true.
//
// "Number of islands", with the land test supplied by the caller. One DFS per unvisited
// matching cell; every cell is visited at most once, so it is O(rows*cols) however many
// regions there are.
//
// The visited set is a separate slice rather than the grid itself, because the caller
// usually still wants the grid. Sinking the island by overwriting it is the shorter
// version and it destroys the input, which is a surprising thing for a function called
// Count to do.
func CountRegions[T any](g [][]T, match func(T) bool, directions [][2]int) int {
	visited := newVisited(g)
	regions := 0

	for row := range g {
		for col := range g[row] {
			if visited[row][col] || !match(g[row][col]) {
				continue
			}

			regions++
			fill(g, visited, row, col, match, directions)
		}
	}

	return regions
}

// Regions returns the cells of each connected region for which match is true, in
// row-major order of their first cell.
func Regions[T any](g [][]T, match func(T) bool, directions [][2]int) [][]Cell {
	visited := newVisited(g)
	var out [][]Cell

	for row := range g {
		for col := range g[row] {
			if visited[row][col] || !match(g[row][col]) {
				continue
			}

			var region []Cell
			collect(g, visited, row, col, match, directions, &region)
			out = append(out, region)
		}
	}

	return out
}

// LargestRegion returns the size of the biggest connected region.
func LargestRegion[T any](g [][]T, match func(T) bool, directions [][2]int) int {
	largest := 0
	for _, region := range Regions(g, match, directions) {
		largest = max(largest, len(region))
	}
	return largest
}

func newVisited[T any](g [][]T) [][]bool {
	visited := make([][]bool, len(g))
	for row := range g {
		visited[row] = make([]bool, len(g[row]))
	}
	return visited
}

// fill is the iterative DFS behind CountRegions.
//
// Iterative rather than recursive, because a 1000x1000 grid that is entirely one region is
// a million-deep recursion. Go grows goroutine stacks so it survives, and an explicit stack
// makes the bound obvious and costs nothing here.
func fill[T any](g [][]T, visited [][]bool, row, col int, match func(T) bool, directions [][2]int) {
	stack := []Cell{{row, col}}
	visited[row][col] = true

	for len(stack) > 0 {
		cell := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		for _, d := range directions {
			r, c := cell.Row+d[0], cell.Col+d[1]

			if !InBounds(g, r, c) || visited[r][c] || !match(g[r][c]) {
				continue
			}

			visited[r][c] = true // on push, not on pop
			stack = append(stack, Cell{r, c})
		}
	}
}

func collect[T any](g [][]T, visited [][]bool, row, col int, match func(T) bool, directions [][2]int, out *[]Cell) {
	stack := []Cell{{row, col}}
	visited[row][col] = true

	for len(stack) > 0 {
		cell := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		*out = append(*out, cell)

		for _, d := range directions {
			r, c := cell.Row+d[0], cell.Col+d[1]

			if !InBounds(g, r, c) || visited[r][c] || !match(g[r][c]) {
				continue
			}

			visited[r][c] = true
			stack = append(stack, Cell{r, c})
		}
	}
}

// FloodFill replaces the region containing (row, col) with replacement, in place, and
// returns how many cells changed.
//
// The early return when the cell already holds the replacement is not an optimisation, it
// is required: without it the fill has no way to tell a filled cell from an unfilled one
// and loops forever.
func FloodFill[T comparable](g [][]T, row, col int, replacement T) int {
	if !InBounds(g, row, col) {
		return 0
	}

	original := g[row][col]
	if original == replacement {
		return 0 // otherwise the recursion has no termination condition
	}

	filled := 0
	stack := []Cell{{row, col}}
	g[row][col] = replacement

	for len(stack) > 0 {
		cell := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		filled++

		for _, d := range FourWay {
			r, c := cell.Row+d[0], cell.Col+d[1]

			if !InBounds(g, r, c) || g[r][c] != original {
				continue
			}

			g[r][c] = replacement // the grid IS the visited set here
			stack = append(stack, Cell{r, c})
		}
	}

	return filled
}

// ShortestPath returns the fewest steps from start to goal through cells for which
// passable is true, and false if the goal is unreachable.
//
// BFS, and the reason it gives the right answer is that it visits cells in distance order:
// the first time it reaches the goal it has done so by a shortest route, because a shorter
// one would have appeared in an earlier level.
//
// The distance grid doubles as the visited set, using -1 for unvisited, which removes a
// second slice and a class of bug where the two disagree.
func ShortestPath[T any](g [][]T, start, goal Cell, passable func(T) bool, directions [][2]int) (int, bool) {
	if !InBounds(g, start.Row, start.Col) || !InBounds(g, goal.Row, goal.Col) {
		return 0, false
	}
	if !passable(g[start.Row][start.Col]) || !passable(g[goal.Row][goal.Col]) {
		return 0, false
	}

	distance := make([][]int, len(g))
	for row := range g {
		distance[row] = make([]int, len(g[row]))
		for col := range distance[row] {
			distance[row][col] = -1
		}
	}

	distance[start.Row][start.Col] = 0
	queue := []Cell{start}

	for len(queue) > 0 {
		cell := queue[0]
		queue = queue[1:]

		if cell == goal {
			return distance[cell.Row][cell.Col], true
		}

		for _, d := range directions {
			r, c := cell.Row+d[0], cell.Col+d[1]

			if !InBounds(g, r, c) || distance[r][c] >= 0 || !passable(g[r][c]) {
				continue
			}

			distance[r][c] = distance[cell.Row][cell.Col] + 1
			queue = append(queue, Cell{r, c})
		}
	}

	return 0, false
}

// PathTo returns the actual shortest path from start to goal, inclusive of both.
//
// A parent grid rather than carrying the path along the queue. A BFS that appends the
// path-so-far to every queue entry costs O(cells^2) memory where this costs O(cells).
func PathTo[T any](g [][]T, start, goal Cell, passable func(T) bool, directions [][2]int) ([]Cell, bool) {
	if !InBounds(g, start.Row, start.Col) || !InBounds(g, goal.Row, goal.Col) {
		return nil, false
	}
	if !passable(g[start.Row][start.Col]) || !passable(g[goal.Row][goal.Col]) {
		return nil, false
	}

	parent := make([][]Cell, len(g))
	seen := newVisited(g)
	for row := range g {
		parent[row] = make([]Cell, len(g[row]))
	}

	seen[start.Row][start.Col] = true
	parent[start.Row][start.Col] = start
	queue := []Cell{start}

	for len(queue) > 0 {
		cell := queue[0]
		queue = queue[1:]

		if cell == goal {
			// Walk the parents back, then reverse.
			path := []Cell{goal}
			for at := goal; at != start; {
				at = parent[at.Row][at.Col]
				path = append(path, at)
			}
			for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
				path[i], path[j] = path[j], path[i]
			}
			return path, true
		}

		for _, d := range directions {
			r, c := cell.Row+d[0], cell.Col+d[1]

			if !InBounds(g, r, c) || seen[r][c] || !passable(g[r][c]) {
				continue
			}

			seen[r][c] = true
			parent[r][c] = cell
			queue = append(queue, Cell{r, c})
		}
	}

	return nil, false
}

// Multi-source BFS
// ================

// Spread returns how many rounds it takes for every cell matching affected to spread to
// every cell matching susceptible, and false if any susceptible cell is never reached.
//
// "Rotting oranges". The multi-source trick is the whole solution: push every initially
// affected cell into the queue BEFORE the loop starts, and the BFS naturally advances all
// of them one round at a time.
//
// Running a separate BFS from each source and taking the minimum per cell gives the same
// answer at O(sources * cells). This is one pass.
func Spread[T any](g [][]T, affected, susceptible func(T) bool, directions [][2]int) (int, bool) {
	distance := make([][]int, len(g))
	var queue []Cell
	remaining := 0

	for row := range g {
		distance[row] = make([]int, len(g[row]))
		for col := range g[row] {
			distance[row][col] = -1

			switch {
			case affected(g[row][col]):
				distance[row][col] = 0
				queue = append(queue, Cell{row, col}) // every source, up front
			case susceptible(g[row][col]):
				remaining++
			}
		}
	}

	rounds := 0

	for len(queue) > 0 {
		cell := queue[0]
		queue = queue[1:]

		for _, d := range directions {
			r, c := cell.Row+d[0], cell.Col+d[1]

			if !InBounds(g, r, c) || distance[r][c] >= 0 || !susceptible(g[r][c]) {
				continue
			}

			distance[r][c] = distance[cell.Row][cell.Col] + 1
			rounds = max(rounds, distance[r][c])
			remaining--

			queue = append(queue, Cell{r, c})
		}
	}

	return rounds, remaining == 0
}

// DistanceToNearest returns, for every cell, the number of steps to the closest cell
// matching target. Unreachable cells get -1.
//
// The same multi-source BFS. Doing it per cell would be O(cells^2).
func DistanceToNearest[T any](g [][]T, target, passable func(T) bool, directions [][2]int) [][]int {
	distance := make([][]int, len(g))
	var queue []Cell

	for row := range g {
		distance[row] = make([]int, len(g[row]))
		for col := range g[row] {
			distance[row][col] = -1

			if target(g[row][col]) {
				distance[row][col] = 0
				queue = append(queue, Cell{row, col})
			}
		}
	}

	for len(queue) > 0 {
		cell := queue[0]
		queue = queue[1:]

		for _, d := range directions {
			r, c := cell.Row+d[0], cell.Col+d[1]

			if !InBounds(g, r, c) || distance[r][c] >= 0 || !passable(g[r][c]) {
				continue
			}

			distance[r][c] = distance[cell.Row][cell.Col] + 1
			queue = append(queue, Cell{r, c})
		}
	}

	return distance
}

// Perimeter returns the total perimeter of the cells matching match: the number of edges
// that face a non-matching cell or the outside.
//
// Not a traversal at all, and here because it is the answer to a problem that looks like
// one. "Island perimeter" needs no BFS, no DFS and no visited set: count each matching
// cell's four sides and subtract the ones facing another matching cell. Reaching for a
// traversal because the problem mentions islands is the mistake.
func Perimeter[T any](g [][]T, match func(T) bool) int {
	total := 0

	for row := range g {
		for col := range g[row] {
			if !match(g[row][col]) {
				continue
			}

			for _, d := range FourWay {
				r, c := row+d[0], col+d[1]

				if !InBounds(g, r, c) || !match(g[r][c]) {
					total++
				}
			}
		}
	}

	return total
}
