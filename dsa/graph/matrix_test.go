package graph

import (
	"math/rand/v2"
	"slices"
	"testing"
)

func TestMatrixBasics(t *testing.T) {
	m := NewMatrix(4)

	m.AddEdge(0, 1)
	m.AddEdge(0, 2)
	m.AddWeightedEdge(1, 3, 5)

	if m.Len() != 4 {
		t.Errorf("Len() = %d, want 4", m.Len())
	}
	if !m.HasEdge(0, 1) || !m.HasEdge(1, 0) {
		t.Error("an undirected edge should work both ways")
	}
	if m.HasEdge(0, 3) {
		t.Error("HasEdge(0, 3) is true for an edge that was never added")
	}
	if got := m.Weight(1, 3); got != 5 {
		t.Errorf("Weight(1, 3) = %d, want 5", got)
	}
	if got := m.Neighbours(0); !slices.Equal(got, []int{1, 2}) {
		t.Errorf("Neighbours(0) = %v, want [1 2]", got)
	}
	if got := m.Degree(0); got != 2 {
		t.Errorf("Degree(0) = %d, want 2", got)
	}
}

func TestMatrixDirected(t *testing.T) {
	m := NewDirectedMatrix(3)
	m.AddEdge(0, 1)

	if !m.HasEdge(0, 1) {
		t.Error("the edge is missing")
	}
	if m.HasEdge(1, 0) {
		t.Error("a directed edge is walkable backwards")
	}
}

// TestMatrixZeroWeightRemovesTheEdge: storing "no edge" as 0 means a weight of 0
// cannot be expressed, and that limitation is worth stating in a test rather than
// leaving as a surprise.
func TestMatrixZeroWeightRemovesTheEdge(t *testing.T) {
	m := NewMatrix(2)
	m.AddEdge(0, 1)
	m.AddWeightedEdge(0, 1, 0)

	if m.HasEdge(0, 1) {
		t.Error("a weight of 0 should remove the edge, since 0 is how absence is stored")
	}
}

func TestMatrixBFS(t *testing.T) {
	m := NewDirectedMatrix(5)
	m.AddEdge(0, 1)
	m.AddEdge(0, 2)
	m.AddEdge(1, 3)
	m.AddEdge(2, 3)
	m.AddEdge(3, 4)

	if got := m.BFS(0); !slices.Equal(got, []int{0, 1, 2, 3, 4}) {
		t.Errorf("BFS(0) = %v, want [0 1 2 3 4]", got)
	}
}

// TestMatrixAgreesWithList runs the same random graph through both
// representations, which is the only way to be confident they mean the same thing.
func TestMatrixAgreesWithList(t *testing.T) {
	const n = 60
	r := rand.New(rand.NewPCG(5, 8))

	list := NewDirected[int]()
	for i := range n {
		list.AddNode(i)
	}
	m := NewDirectedMatrix(n)

	for range n * 3 {
		from, to := r.IntN(n), r.IntN(n)
		list.AddEdge(from, to)
		m.AddEdge(from, to)
	}

	for i := range n {
		listNeighbours := list.Neighbours(i)
		slices.Sort(listNeighbours) // the matrix can only give index order

		if got := m.Neighbours(i); !slices.Equal(got, listNeighbours) {
			t.Fatalf("node %d: matrix says %v, list says %v", i, got, listNeighbours)
		}

		for j := range n {
			if m.HasEdge(i, j) != list.HasEdge(i, j) {
				t.Fatalf("edge %d -> %d: matrix says %v, list says %v",
					i, j, m.HasEdge(i, j), list.HasEdge(i, j))
			}
		}
	}

	listReach := list.Reachable(0)
	matrixReach := m.BFS(0)
	slices.Sort(listReach)
	slices.Sort(matrixReach)

	if !slices.Equal(listReach, matrixReach) {
		t.Errorf("reachable sets differ: %v and %v", listReach, matrixReach)
	}
}

// Density
// =======
//
// The comparison that decides between the two representations. Same node count,
// varying edge count, measured on the two things each one is supposed to be good
// at: a single adjacency question, and a full traversal.

// randomGraph builds both representations of the same random graph, with
// avgDegree edges out of each node.
func randomGraph(n, avgDegree int) (*Graph[int], *Matrix) {
	r := rand.New(rand.NewPCG(11, 13))

	list := NewDirected[int]()
	for i := range n {
		list.AddNode(i)
	}
	m := NewDirectedMatrix(n)

	for from := range n {
		for range avgDegree {
			to := r.IntN(n)
			list.AddEdge(from, to)
			m.AddEdge(from, to)
		}
	}

	return list, m
}

func BenchmarkHasEdge(b *testing.B) {
	const n = 1000

	for _, degree := range []int{4, 50, 500} {
		list, m := randomGraph(n, degree)

		b.Run(labelFor("list", degree), func(b *testing.B) {
			for i := 0; b.Loop(); i++ {
				sinkBool = list.HasEdge(i%n, (i*7)%n)
			}
		})

		b.Run(labelFor("matrix", degree), func(b *testing.B) {
			for i := 0; b.Loop(); i++ {
				sinkBool = m.HasEdge(i%n, (i*7)%n)
			}
		})
	}
}

func BenchmarkTraversal(b *testing.B) {
	const n = 1000

	for _, degree := range []int{4, 50, 500} {
		list, m := randomGraph(n, degree)

		b.Run(labelFor("list", degree), func(b *testing.B) {
			for b.Loop() {
				count := 0
				for range list.BFS(0) {
					count++
				}
				sinkInt = count
			}
		})

		b.Run(labelFor("matrix", degree), func(b *testing.B) {
			for b.Loop() {
				sinkInt = len(m.BFS(0))
			}
		})
	}
}

func labelFor(kind string, degree int) string {
	return kind + "/degree " + itoa(degree)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// TestMemoryComparison is not a benchmark because the numbers do not vary between
// runs: they are arithmetic. Logged rather than asserted, because the point is the
// ratio and not any particular value.
func TestMemoryComparison(t *testing.T) {
	for _, tc := range []struct{ n, degree int }{
		{100, 4}, {1000, 4}, {10_000, 4}, {1000, 500},
	} {
		list, m := randomGraph(tc.n, tc.degree)

		// One Edge is a V plus an int, so 16 bytes for an int node, plus a slice
		// header per node.
		listBytes := list.EdgeCount()*16 + tc.n*24
		matrixBytes := m.Bytes()

		t.Logf("n=%-6d degree=%-4d edges=%-8d list ~%-10d bytes  matrix %-12d bytes  ratio %.0fx",
			tc.n, tc.degree, list.EdgeCount(), listBytes, matrixBytes,
			float64(matrixBytes)/float64(listBytes))
	}
}

var (
	sinkBool bool
	sinkInt  int
)
