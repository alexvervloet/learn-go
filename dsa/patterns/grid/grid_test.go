package grid

import (
	"math/rand/v2"
	"slices"
	"testing"
)

// parse turns a picture into a grid, so the tests read like the problem statements.
func parse(rows ...string) [][]byte {
	g := make([][]byte, len(rows))
	for i, row := range rows {
		g[i] = []byte(row)
	}
	return g
}

func isLand(b byte) bool   { return b == '1' }
func isOpen(b byte) bool   { return b != '#' }
func alwaysTrue(byte) bool { return true }

func TestInBounds(t *testing.T) {
	g := parse("abc", "de") // deliberately ragged

	cases := []struct {
		row, col int
		want     bool
	}{
		{0, 0, true}, {0, 2, true}, {0, 3, false},
		{1, 0, true}, {1, 1, true},
		{1, 2, false}, // the second row is shorter, which a len(grid[0]) check misses
		{-1, 0, false}, {0, -1, false}, {2, 0, false},
	}

	for _, tc := range cases {
		if got := InBounds(g, tc.row, tc.col); got != tc.want {
			t.Errorf("InBounds(%d, %d) = %v, want %v", tc.row, tc.col, got, tc.want)
		}
	}
}

func TestCountRegions(t *testing.T) {
	tests := []struct {
		name        string
		rows        []string
		four, eight int
	}{
		{
			name: "classic islands",
			rows: []string{
				"11000",
				"11000",
				"00100",
				"00011",
			},
			// Eight-way merges all three: (1,1) touches (2,2) diagonally, and (2,2)
			// touches (3,3). Traced wrong the first time.
			four: 3, eight: 1,
		},
		{
			name: "one big island",
			rows: []string{"111", "111", "111"},
			four: 1, eight: 1,
		},
		{
			name: "all water",
			rows: []string{"000", "000"},
			four: 0, eight: 0,
		},
		{
			name: "a checkerboard, where diagonals matter",
			rows: []string{
				"101",
				"010",
				"101",
			},
			four: 5, eight: 1,
		},
		{
			name: "single cell",
			rows: []string{"1"},
			four: 1, eight: 1,
		},
		{name: "empty", rows: nil, four: 0, eight: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := parse(tt.rows...)

			if got := CountRegions(g, isLand, FourWay); got != tt.four {
				t.Errorf("four-way = %d, want %d", got, tt.four)
			}
			if got := CountRegions(g, isLand, EightWay); got != tt.eight {
				t.Errorf("eight-way = %d, want %d", got, tt.eight)
			}
		})
	}
}

// TestCountRegionsDoesNotMutate: the shorter implementation sinks the island by
// overwriting it, which is a surprising thing for a function called Count to do.
func TestCountRegionsDoesNotMutate(t *testing.T) {
	rows := []string{"110", "010", "001"}

	g := parse(rows...)
	CountRegions(g, isLand, FourWay)
	Regions(g, isLand, FourWay)
	LargestRegion(g, isLand, FourWay)

	for i, row := range rows {
		if string(g[i]) != row {
			t.Errorf("row %d became %q, want %q", i, string(g[i]), row)
		}
	}
}

func TestRegionsAndLargest(t *testing.T) {
	g := parse(
		"11000",
		"11000",
		"00100",
		"00011",
	)

	regions := Regions(g, isLand, FourWay)

	if len(regions) != 3 {
		t.Fatalf("got %d regions, want 3", len(regions))
	}

	sizes := make([]int, len(regions))
	for i, r := range regions {
		sizes[i] = len(r)
	}
	if !slices.Equal(sizes, []int{4, 1, 2}) {
		t.Errorf("region sizes = %v, want [4 1 2]", sizes)
	}
	if got := LargestRegion(g, isLand, FourWay); got != 4 {
		t.Errorf("LargestRegion = %d, want 4", got)
	}

	// Every cell in every region must actually be land, and no cell may appear twice.
	seen := map[Cell]bool{}
	for _, region := range regions {
		for _, cell := range region {
			if !isLand(g[cell.Row][cell.Col]) {
				t.Errorf("region contains %v, which is not land", cell)
			}
			if seen[cell] {
				t.Errorf("%v appears in two regions", cell)
			}
			seen[cell] = true
		}
	}

	// And every land cell must be in exactly one region.
	land := 0
	for row := range g {
		for col := range g[row] {
			if isLand(g[row][col]) {
				land++
			}
		}
	}
	if len(seen) != land {
		t.Errorf("regions cover %d cells but there are %d land cells", len(seen), land)
	}
}

func TestFloodFill(t *testing.T) {
	g := parse(
		"111",
		"110",
		"101",
	)

	filled := FloodFill(g, 1, 1, '2')

	if filled != 6 {
		t.Errorf("filled %d cells, want 6", filled)
	}

	want := []string{"222", "220", "201"}
	for i, row := range want {
		if string(g[i]) != row {
			t.Errorf("row %d = %q, want %q", i, string(g[i]), row)
		}
	}
}

// TestFloodFillSameColour is the case that loops forever without the early return. There is
// no separate visited set, so a fill to the colour already there cannot tell a filled cell
// from an unfilled one.
func TestFloodFillSameColour(t *testing.T) {
	g := parse("11", "11")

	// Without the early return this does not come back: the grid is the visited set, so
	// filling to the colour already there leaves no way to tell a filled cell from an
	// unfilled one. The test for that is that this line returns at all.
	filled := FloodFill(g, 0, 0, '1')

	if filled != 0 {
		t.Errorf("filled %d cells, want 0", filled)
	}
	if string(g[0]) != "11" {
		t.Errorf("the grid changed: %q", string(g[0]))
	}
}

func TestFloodFillOutOfBounds(t *testing.T) {
	g := parse("11", "11")

	for _, c := range []Cell{{-1, 0}, {0, -1}, {2, 0}, {0, 2}} {
		if got := FloodFill(g, c.Row, c.Col, '2'); got != 0 {
			t.Errorf("FloodFill at %v filled %d cells", c, got)
		}
	}
	if string(g[0]) != "11" {
		t.Error("an out-of-bounds fill changed the grid")
	}
}

func TestShortestPath(t *testing.T) {
	g := parse(
		".....",
		".###.",
		".....",
		".###.",
		".....",
	)

	steps, ok := ShortestPath(g, Cell{0, 0}, Cell{4, 4}, isOpen, FourWay)
	if !ok {
		t.Fatal("no path found")
	}
	if steps != 8 {
		t.Errorf("steps = %d, want 8", steps)
	}

	// Eight-way allows diagonals, so it is shorter, but not as much shorter as it looks:
	// rows 1 and 3 are walled except at the edges, so the path still has to funnel
	// through them. (0,0) (1,0) (2,1) (2,2) (2,3) (3,4) (4,4) is six steps.
	steps, ok = ShortestPath(g, Cell{0, 0}, Cell{4, 4}, isOpen, EightWay)
	if !ok || steps != 6 {
		t.Errorf("eight-way = %d, %v; want 6, true", steps, ok)
	}
	if steps >= 8 {
		t.Error("eight-way should be shorter than four-way")
	}
}

func TestShortestPathUnreachable(t *testing.T) {
	g := parse(
		"..#..",
		"..#..",
		"..#..",
	)

	if _, ok := ShortestPath(g, Cell{0, 0}, Cell{0, 4}, isOpen, FourWay); ok {
		t.Error("found a path through a wall")
	}

	// A blocked start or goal is not reachable either.
	if _, ok := ShortestPath(g, Cell{0, 2}, Cell{0, 0}, isOpen, FourWay); ok {
		t.Error("started from a wall")
	}
	if _, ok := ShortestPath(g, Cell{0, 0}, Cell{0, 2}, isOpen, FourWay); ok {
		t.Error("ended at a wall")
	}

	// Out of bounds.
	if _, ok := ShortestPath(g, Cell{-1, 0}, Cell{0, 0}, isOpen, FourWay); ok {
		t.Error("started from outside the grid")
	}
}

func TestShortestPathToItself(t *testing.T) {
	g := parse("...", "...")

	steps, ok := ShortestPath(g, Cell{1, 1}, Cell{1, 1}, isOpen, FourWay)
	if !ok || steps != 0 {
		t.Errorf("= %d, %v; want 0, true", steps, ok)
	}
}

func TestPathTo(t *testing.T) {
	g := parse(
		".....",
		".###.",
		".....",
	)

	path, ok := PathTo(g, Cell{0, 0}, Cell{2, 0}, isOpen, FourWay)
	if !ok {
		t.Fatal("no path found")
	}

	// The reported length must match ShortestPath.
	steps, _ := ShortestPath(g, Cell{0, 0}, Cell{2, 0}, isOpen, FourWay)
	if len(path)-1 != steps {
		t.Errorf("path has %d steps, ShortestPath says %d", len(path)-1, steps)
	}

	// It has to start and end in the right places.
	if path[0] != (Cell{0, 0}) || path[len(path)-1] != (Cell{2, 0}) {
		t.Errorf("path runs %v to %v", path[0], path[len(path)-1])
	}

	// Every step must be adjacent, in bounds, and passable.
	for i := 1; i < len(path); i++ {
		dr := path[i].Row - path[i-1].Row
		dc := path[i].Col - path[i-1].Col

		if abs(dr)+abs(dc) != 1 {
			t.Errorf("step %d from %v to %v is not adjacent", i, path[i-1], path[i])
		}
		if !InBounds(g, path[i].Row, path[i].Col) {
			t.Errorf("step %d is out of bounds: %v", i, path[i])
		}
		if !isOpen(g[path[i].Row][path[i].Col]) {
			t.Errorf("step %d walks into a wall: %v", i, path[i])
		}
	}
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// TestPathToMatchesShortestPathEverywhere: the parent-pointer reconstruction is the part
// that goes wrong, so it is checked against the distance-only version at every cell of
// many random mazes.
func TestPathToMatchesShortestPathEverywhere(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))

	for range 300 {
		rows, cols := 1+r.IntN(8), 1+r.IntN(8)

		g := make([][]byte, rows)
		for i := range g {
			g[i] = make([]byte, cols)
			for j := range g[i] {
				g[i][j] = '.'
				if r.IntN(3) == 0 {
					g[i][j] = '#'
				}
			}
		}

		start := Cell{r.IntN(rows), r.IntN(cols)}

		for row := range rows {
			for col := range cols {
				goal := Cell{row, col}

				steps, ok := ShortestPath(g, start, goal, isOpen, FourWay)
				path, pathOK := PathTo(g, start, goal, isOpen, FourWay)

				if ok != pathOK {
					t.Fatalf("ShortestPath says %v and PathTo says %v for %v to %v",
						ok, pathOK, start, goal)
				}
				if !ok {
					continue
				}
				if len(path)-1 != steps {
					t.Fatalf("path %v has %d steps, ShortestPath says %d", path, len(path)-1, steps)
				}
			}
		}
	}
}

func TestSpread(t *testing.T) {
	// 2 is rotten, 1 is fresh, 0 is empty.
	rotten := func(b byte) bool { return b == '2' }
	fresh := func(b byte) bool { return b == '1' }

	tests := []struct {
		name string
		rows []string
		want int
		all  bool
	}{
		{
			name: "classic",
			rows: []string{"211", "110", "011"},
			want: 4, all: true,
		},
		{
			name: "one unreachable",
			rows: []string{"211", "011", "101"},
			want: 0, all: false,
		},
		{
			name: "nothing fresh",
			rows: []string{"000"},
			want: 0, all: true,
		},
		{
			name: "nothing rotten, something fresh",
			rows: []string{"111"},
			want: 0, all: false,
		},
		{
			name: "already all rotten",
			rows: []string{"222"},
			want: 0, all: true,
		},
		{
			name: "multi-source: two sources halve the time",
			rows: []string{"2111112"},
			want: 3, all: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := parse(tt.rows...)

			rounds, all := Spread(g, rotten, fresh, FourWay)

			if all != tt.all {
				t.Errorf("all = %v, want %v", all, tt.all)
			}
			if all && rounds != tt.want {
				t.Errorf("rounds = %d, want %d", rounds, tt.want)
			}
		})
	}
}

// TestMultiSourceBeatsPerSource is the point of the pattern: one pass from every source at
// once gives the same answer as a BFS per source, and the single-source version is what a
// first attempt looks like.
func TestMultiSourceBeatsPerSource(t *testing.T) {
	r := rand.New(rand.NewPCG(3, 4))

	for range 200 {
		rows, cols := 1+r.IntN(7), 1+r.IntN(7)

		g := make([][]byte, rows)
		for i := range g {
			g[i] = make([]byte, cols)
			for j := range g[i] {
				g[i][j] = '.'
				if r.IntN(4) == 0 {
					g[i][j] = 'X'
				}
			}
		}

		isTarget := func(b byte) bool { return b == 'X' }

		got := DistanceToNearest(g, isTarget, alwaysTrue, FourWay)

		// The obvious version: one BFS from each cell, looking for the nearest target.
		for row := range rows {
			for col := range cols {
				want := -1
				for tr := range rows {
					for tc := range cols {
						if !isTarget(g[tr][tc]) {
							continue
						}
						steps, ok := ShortestPath(g, Cell{row, col}, Cell{tr, tc}, alwaysTrue, FourWay)
						if ok && (want < 0 || steps < want) {
							want = steps
						}
					}
				}

				if got[row][col] != want {
					t.Fatalf("at (%d,%d) multi-source says %d, per-source says %d",
						row, col, got[row][col], want)
				}
			}
		}
	}
}

func TestDistanceToNearest(t *testing.T) {
	g := parse(
		"X..",
		"...",
		"..X",
	)

	isTarget := func(b byte) bool { return b == 'X' }
	got := DistanceToNearest(g, isTarget, alwaysTrue, FourWay)

	want := [][]int{
		{0, 1, 2},
		{1, 2, 1},
		{2, 1, 0},
	}

	for row := range want {
		if !slices.Equal(got[row], want[row]) {
			t.Errorf("row %d = %v, want %v", row, got[row], want[row])
		}
	}
}

func TestDistanceToNearestUnreachable(t *testing.T) {
	g := parse(
		"X#.",
		"###",
		"...",
	)

	isTarget := func(b byte) bool { return b == 'X' }
	got := DistanceToNearest(g, isTarget, isOpen, FourWay)

	if got[0][0] != 0 {
		t.Errorf("the target itself is at distance %d, want 0", got[0][0])
	}
	if got[2][0] != -1 {
		t.Errorf("a walled-off cell is at distance %d, want -1", got[2][0])
	}
}

func TestPerimeter(t *testing.T) {
	tests := []struct {
		name string
		rows []string
		want int
	}{
		{"one cell", []string{"1"}, 4},
		{"two adjacent", []string{"11"}, 6},
		{"a 2x2 block", []string{"11", "11"}, 8},
		{"classic", []string{"0110", "1111", "0110", "0100"}, 16},
		{"none", []string{"000"}, 0},
		{"two separate cells", []string{"101"}, 8},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Perimeter(parse(tt.rows...), isLand); got != tt.want {
				t.Errorf("Perimeter = %d, want %d", got, tt.want)
			}
		})
	}
}

// TestRaggedGridsDoNotPanic: Go allows rows of different lengths, so every bounds check is
// per row. A single len(grid[0]) check panics on the longer rows.
func TestRaggedGridsDoNotPanic(t *testing.T) {
	g := parse("11", "1", "111")

	if got := CountRegions(g, isLand, FourWay); got == 0 {
		t.Error("found no regions in a grid that is all land")
	}
	Regions(g, isLand, FourWay)
	LargestRegion(g, isLand, FourWay)
	Perimeter(g, isLand)
	DistanceToNearest(g, isLand, alwaysTrue, FourWay)
	FloodFill(g, 0, 0, '2')

	if _, ok := ShortestPath(g, Cell{0, 0}, Cell{2, 2}, alwaysTrue, FourWay); !ok {
		t.Error("no path across a ragged grid that is fully connected")
	}
}
