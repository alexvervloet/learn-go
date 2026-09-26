package grid_test

import (
	"fmt"

	"github.com/alexvervloet/learn-go/dsa/patterns/grid"
)

func parse(rows ...string) [][]byte {
	g := make([][]byte, len(rows))
	for i, row := range rows {
		g[i] = []byte(row)
	}
	return g
}

func isLand(b byte) bool { return b == '1' }
func isOpen(b byte) bool { return b != '#' }

// Whether diagonals count is usually stated only by example, so it is worth checking
// against the given test case before writing anything.
func ExampleCountRegions() {
	g := parse(
		"11000",
		"11000",
		"00100",
		"00011",
	)

	fmt.Println("four-way: ", grid.CountRegions(g, isLand, grid.FourWay))
	fmt.Println("eight-way:", grid.CountRegions(g, isLand, grid.EightWay))

	// Output:
	// four-way:  3
	// eight-way: 1
}

func ExampleLargestRegion() {
	g := parse(
		"11000",
		"11000",
		"00100",
		"00011",
	)

	for _, region := range grid.Regions(g, isLand, grid.FourWay) {
		fmt.Println(len(region), region)
	}
	fmt.Println("largest:", grid.LargestRegion(g, isLand, grid.FourWay))

	// Output:
	// 4 [{0 0} {0 1} {1 1} {1 0}]
	// 1 [{2 2}]
	// 2 [{3 3} {3 4}]
	// largest: 4
}

// The grid is the visited set here, which is why filling to the colour already there has to
// return early: otherwise there is no way to tell a filled cell from an unfilled one.
func ExampleFloodFill() {
	g := parse(
		"111",
		"110",
		"101",
	)

	filled := grid.FloodFill(g, 1, 1, '2')

	fmt.Println("filled", filled, "cells")
	for _, row := range g {
		fmt.Println(string(row))
	}

	fmt.Println("same colour:", grid.FloodFill(g, 1, 1, '2'))

	// Output:
	// filled 6 cells
	// 222
	// 220
	// 201
	// same colour: 0
}

// BFS gives the shortest path because it visits by distance: the first time it reaches the
// goal it has done so by a shortest route.
func ExampleShortestPath() {
	g := parse(
		".....",
		".###.",
		".....",
		".###.",
		".....",
	)

	four, _ := grid.ShortestPath(g, grid.Cell{Row: 0, Col: 0}, grid.Cell{Row: 4, Col: 4}, isOpen, grid.FourWay)
	eight, _ := grid.ShortestPath(g, grid.Cell{Row: 0, Col: 0}, grid.Cell{Row: 4, Col: 4}, isOpen, grid.EightWay)

	fmt.Println("four-way: ", four)
	fmt.Println("eight-way:", eight)

	// Output:
	// four-way:  8
	// eight-way: 6
}

// A parent grid rather than carrying the path along the queue: O(cells) memory instead of
// O(cells²).
func ExamplePathTo() {
	g := parse(
		"...",
		".#.",
		"...",
	)

	path, _ := grid.PathTo(g, grid.Cell{Row: 0, Col: 0}, grid.Cell{Row: 2, Col: 2}, isOpen, grid.FourWay)

	for _, cell := range path {
		fmt.Printf("(%d,%d) ", cell.Row, cell.Col)
	}
	fmt.Println()

	// Output:
	// (0,0) (1,0) (2,0) (2,1) (2,2)
}

// Multi-source BFS: push every source into the queue before the loop starts, and the search
// advances all of them one round at a time.
func ExampleSpread() {
	rotten := func(b byte) bool { return b == '2' }
	fresh := func(b byte) bool { return b == '1' }

	// One source at the left end.
	one := parse("2111111")
	rounds, all := grid.Spread(one, rotten, fresh, grid.FourWay)
	fmt.Println("one source: ", rounds, all)

	// Two sources, at both ends, and the same grid takes half as long.
	two := parse("2111112")
	rounds, all = grid.Spread(two, rotten, fresh, grid.FourWay)
	fmt.Println("two sources:", rounds, all)

	// A fresh cell nothing can reach.
	blocked := parse("21", "01", "10")
	_, all = grid.Spread(blocked, rotten, fresh, grid.FourWay)
	fmt.Println("unreachable:", all)

	// Output:
	// one source:  6 true
	// two sources: 3 true
	// unreachable: false
}

// The same multi-source pass answers "how far is the nearest X" for every cell at once.
// Doing it per cell is O(cells²).
func ExampleDistanceToNearest() {
	g := parse(
		"X..",
		"...",
		"..X",
	)

	isTarget := func(b byte) bool { return b == 'X' }
	anywhere := func(byte) bool { return true }

	for _, row := range grid.DistanceToNearest(g, isTarget, anywhere, grid.FourWay) {
		fmt.Println(row)
	}

	// Output:
	// [0 1 2]
	// [1 2 1]
	// [2 1 0]
}

// A problem that mentions islands and needs no traversal at all. Reaching for BFS because
// the word appears is the mistake.
func ExamplePerimeter() {
	g := parse(
		"0110",
		"1111",
		"0110",
		"0100",
	)

	fmt.Println(grid.Perimeter(g, isLand))

	// Output:
	// 16
}
