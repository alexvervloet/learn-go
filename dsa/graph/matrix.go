package graph

import (
	"fmt"
	"strings"
)

// Adjacency matrix
// ================
//
// The other way to store edges: a V-by-V grid where cell [i][j] holds the weight
// of the edge from i to j, or 0 for no edge.
//
//	     A  B  C  D
//	  A  0  1  1  0
//	  B  0  0  0  1
//	  C  0  0  0  1
//	  D  0  0  0  0
//
// It trades memory for speed in one specific place. "Is there an edge from A to
// D" is one array read, where an adjacency list scans A's edges. The cost is
// O(V^2) memory whether the graph has a million edges or none, and every
// traversal has to scan a whole row to find the neighbours, which is O(V) per
// node rather than O(degree).
//
// So the crossover is about density. matrix_test.go measures where it falls; the
// short version is that real graphs are sparse enough that this is almost always
// the wrong choice, and the exceptions are dense graphs small enough to fit in
// cache, where the contiguous memory and lack of pointer chasing win outright.

// Matrix is a graph stored as an adjacency matrix. Nodes are integers 0 to n-1,
// because a matrix needs a dense index and mapping arbitrary keys onto one is a
// separate concern.
type Matrix struct {
	n        int
	directed bool

	// One flat slice rather than [][]int. A slice of slices for a 1000-node graph
	// is 1,001 allocations and a pointer chase per row; this is one allocation
	// and index arithmetic. The measurement is in matrix_test.go.
	weights []int
}

// NewMatrix returns an n-node undirected matrix graph with no edges.
func NewMatrix(n int) *Matrix {
	return &Matrix{n: n, weights: make([]int, n*n)}
}

// NewDirectedMatrix returns an n-node directed matrix graph with no edges.
func NewDirectedMatrix(n int) *Matrix {
	m := NewMatrix(n)
	m.directed = true
	return m
}

// Len reports the number of nodes.
func (m *Matrix) Len() int { return m.n }

// AddEdge adds an unweighted edge.
func (m *Matrix) AddEdge(from, to int) { m.AddWeightedEdge(from, to, 1) }

// AddWeightedEdge adds an edge with a weight. A weight of 0 removes the edge,
// because 0 is how "no edge" is stored.
func (m *Matrix) AddWeightedEdge(from, to, weight int) {
	m.weights[from*m.n+to] = weight
	if !m.directed {
		m.weights[to*m.n+from] = weight
	}
}

// HasEdge reports whether an edge exists. One array read, which is the entire
// argument for this representation.
func (m *Matrix) HasEdge(from, to int) bool { return m.weights[from*m.n+to] != 0 }

// Weight returns the weight of an edge, or 0 if there is none.
func (m *Matrix) Weight(from, to int) int { return m.weights[from*m.n+to] }

// Neighbours returns the nodes reachable from v by one edge, in index order.
//
// O(V) whatever the degree, because the whole row has to be scanned. For a sparse
// graph that is the matrix's real cost: a traversal is O(V^2) rather than O(V+E).
func (m *Matrix) Neighbours(v int) []int {
	var out []int
	row := m.weights[v*m.n : (v+1)*m.n]

	for to, w := range row {
		if w != 0 {
			out = append(out, to)
		}
	}
	return out
}

// Degree returns the number of edges out of v, also O(V).
func (m *Matrix) Degree(v int) int {
	n := 0
	for _, w := range m.weights[v*m.n : (v+1)*m.n] {
		if w != 0 {
			n++
		}
	}
	return n
}

// BFS returns the nodes reachable from start, nearest first.
//
// A slice of bools rather than a map for the visited set, which is the other
// thing a dense integer index buys: one byte per node with no hashing, against a
// map's ~50 bytes per entry and a hash per lookup.
func (m *Matrix) BFS(start int) []int {
	visited := make([]bool, m.n)
	visited[start] = true

	queue := []int{start}
	var order []int

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		order = append(order, current)

		row := m.weights[current*m.n : (current+1)*m.n]
		for to, w := range row {
			if w == 0 || visited[to] {
				continue
			}
			visited[to] = true
			queue = append(queue, to)
		}
	}

	return order
}

// Bytes reports how much memory the matrix uses for its edges, which is the
// number the density comparison turns on.
func (m *Matrix) Bytes() int { return len(m.weights) * 8 }

// String renders the matrix, for small graphs.
//
// The cells are joined rather than printed with a trailing space, because an
// Example's expected output is compared after trimming the whole block and not
// each line, so trailing whitespace is invisible in the source and fails the test.
func (m *Matrix) String() string {
	var sb strings.Builder

	cells := make([]string, m.n)
	for i := range m.n {
		for j := range m.n {
			cells[j] = fmt.Sprintf("%2d", m.weights[i*m.n+j])
		}
		sb.WriteString(strings.Join(cells, " "))
		sb.WriteString("\n")
	}

	return sb.String()
}
