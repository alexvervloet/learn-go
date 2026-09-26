package graph

import (
	"errors"
	"slices"
	"testing"
)

// The graph used throughout, from the package doc:
//
//	A -> B, A -> C, B -> D, C -> D
func sampleDAG() *Graph[string] {
	g := NewDirected[string]()
	g.AddEdge("A", "B")
	g.AddEdge("A", "C")
	g.AddEdge("B", "D")
	g.AddEdge("C", "D")
	return g
}

func TestAddNodeAndEdge(t *testing.T) {
	g := New[string]()

	if !g.AddNode("A") {
		t.Error("AddNode reported the node already existed")
	}
	if g.AddNode("A") {
		t.Error("adding the same node twice reported it was new")
	}
	if g.Len() != 1 {
		t.Errorf("Len() = %d, want 1", g.Len())
	}

	if !g.AddEdge("A", "B") {
		t.Error("AddEdge reported the edge already existed")
	}
	if g.AddEdge("A", "B") {
		t.Error("adding the same edge twice reported it was new")
	}
	if g.Len() != 2 {
		t.Errorf("Len() = %d after one edge, want 2", g.Len())
	}
	if g.EdgeCount() != 1 {
		t.Errorf("EdgeCount() = %d, want 1", g.EdgeCount())
	}
}

// TestUndirectedEdgesGoBothWays: an undirected edge is stored twice and counted
// once, and getting either half of that wrong is invisible until a traversal
// misses half the graph.
func TestUndirectedEdgesGoBothWays(t *testing.T) {
	g := New[string]()
	g.AddEdge("A", "B")

	if !g.HasEdge("A", "B") || !g.HasEdge("B", "A") {
		t.Error("an undirected edge is not walkable in both directions")
	}
	if g.EdgeCount() != 1 {
		t.Errorf("EdgeCount() = %d, want 1", g.EdgeCount())
	}
	if g.Degree("A") != 1 || g.Degree("B") != 1 {
		t.Errorf("degrees are %d and %d, want 1 and 1", g.Degree("A"), g.Degree("B"))
	}
}

func TestDirectedEdgesGoOneWay(t *testing.T) {
	g := NewDirected[string]()
	g.AddEdge("A", "B")

	if !g.HasEdge("A", "B") {
		t.Error("the edge is missing")
	}
	if g.HasEdge("B", "A") {
		t.Error("a directed edge is walkable backwards")
	}
	if g.InDegree("B") != 1 || g.InDegree("A") != 0 {
		t.Errorf("in-degrees are A=%d B=%d, want 0 and 1", g.InDegree("A"), g.InDegree("B"))
	}
}

func TestWeightedEdges(t *testing.T) {
	g := New[string]()
	g.AddWeightedEdge("A", "B", 5)

	if w, ok := g.Weight("A", "B"); !ok || w != 5 {
		t.Errorf("Weight(A, B) = %d, %v; want 5, true", w, ok)
	}
	if w, _ := g.Weight("B", "A"); w != 5 {
		t.Errorf("the reverse weight is %d, want 5", w)
	}

	// Re-adding replaces rather than duplicating.
	if g.AddWeightedEdge("A", "B", 9) {
		t.Error("re-adding an edge reported it was new")
	}
	if w, _ := g.Weight("A", "B"); w != 9 {
		t.Errorf("Weight(A, B) = %d after a replace, want 9", w)
	}
	if g.EdgeCount() != 1 {
		t.Errorf("EdgeCount() = %d, want 1: a replace must not add a parallel edge", g.EdgeCount())
	}
}

func TestRemoveEdge(t *testing.T) {
	g := New[string]()
	g.AddEdge("A", "B")
	g.AddEdge("A", "C")

	if !g.RemoveEdge("A", "B") {
		t.Error("RemoveEdge reported the edge was absent")
	}
	if g.RemoveEdge("A", "B") {
		t.Error("removing twice reported success")
	}
	if g.HasEdge("A", "B") || g.HasEdge("B", "A") {
		t.Error("both directions should be gone")
	}
	if g.Len() != 3 {
		t.Errorf("Len() = %d; removing an edge must not remove nodes", g.Len())
	}

	// Order of the remaining edges is preserved, which every traversal depends on.
	if got := g.Neighbours("A"); !slices.Equal(got, []string{"C"}) {
		t.Errorf("Neighbours(A) = %v, want [C]", got)
	}
}

// TestInsertionOrderIsPreserved is the property that makes every other test in
// this package writable.
func TestInsertionOrderIsPreserved(t *testing.T) {
	g := NewDirected[string]()
	for _, to := range []string{"zeta", "alpha", "mu", "beta"} {
		g.AddEdge("root", to)
	}

	want := []string{"zeta", "alpha", "mu", "beta"}
	for range 20 { // a map-backed implementation would differ between runs
		if got := g.Neighbours("root"); !slices.Equal(got, want) {
			t.Fatalf("Neighbours(root) = %v, want %v", got, want)
		}
	}

	if got := g.Nodes(); !slices.Equal(got, []string{"root", "zeta", "alpha", "mu", "beta"}) {
		t.Errorf("Nodes() = %v", got)
	}
}

func TestNodesAndEdgesReturnCopies(t *testing.T) {
	g := NewDirected[string]()
	g.AddEdge("A", "B")

	nodes := g.Nodes()
	nodes[0] = "mutated"

	edges := g.Edges("A")
	edges[0].To = "mutated"

	if got := g.Nodes()[0]; got != "A" {
		t.Errorf("mutating the Nodes() result changed the graph: %q", got)
	}
	if got := g.Neighbours("A")[0]; got != "B" {
		t.Errorf("mutating the Edges() result changed the graph: %q", got)
	}
}

func TestBFS(t *testing.T) {
	g := sampleDAG()

	var got []string
	for v := range g.BFS("A") {
		got = append(got, v)
	}

	if want := []string{"A", "B", "C", "D"}; !slices.Equal(got, want) {
		t.Errorf("BFS(A) = %v, want %v", got, want)
	}
}

// TestBFSVisitsEachNodeOnce is the diamond that catches marking-on-dequeue. D has
// two in-edges, so an implementation that marks visited when a node comes OFF the
// queue visits it twice.
func TestBFSVisitsEachNodeOnce(t *testing.T) {
	g := sampleDAG()

	seen := map[string]int{}
	for v := range g.BFS("A") {
		seen[v]++
	}

	for v, n := range seen {
		if n != 1 {
			t.Errorf("BFS visited %q %d times, want 1", v, n)
		}
	}
	if len(seen) != 4 {
		t.Errorf("BFS visited %d nodes, want 4", len(seen))
	}
}

func TestBFSLevels(t *testing.T) {
	g := NewDirected[string]()
	g.AddEdge("A", "B")
	g.AddEdge("A", "C")
	g.AddEdge("B", "D")
	g.AddEdge("C", "D")
	g.AddEdge("D", "E")

	var got [][]string
	for depth, level := range g.BFSLevels("A") {
		if depth != len(got) {
			t.Errorf("depth %d arrived at position %d", depth, len(got))
		}
		got = append(got, level)
	}

	want := [][]string{{"A"}, {"B", "C"}, {"D"}, {"E"}}
	if len(got) != len(want) {
		t.Fatalf("got %d levels, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if !slices.Equal(got[i], want[i]) {
			t.Errorf("level %d = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestDFS(t *testing.T) {
	g := sampleDAG()

	var iterative, recursive []string
	for v := range g.DFS("A") {
		iterative = append(iterative, v)
	}
	for v := range g.DFSRecursive("A") {
		recursive = append(recursive, v)
	}

	// A -> B -> D, back up, then C. The reversal when pushing is what makes B
	// come before C; without it the iterative version gives A C D B.
	if want := []string{"A", "B", "D", "C"}; !slices.Equal(iterative, want) {
		t.Errorf("DFS(A) = %v, want %v", iterative, want)
	}
	if !slices.Equal(iterative, recursive) {
		t.Errorf("the two DFS implementations disagree: %v and %v", iterative, recursive)
	}
}

func TestTraversalsHandleCycles(t *testing.T) {
	g := NewDirected[string]()
	g.AddEdge("A", "B")
	g.AddEdge("B", "C")
	g.AddEdge("C", "A") // back to the start

	bfs := 0
	for range g.BFS("A") {
		bfs++
		if bfs > 10 {
			t.Fatal("BFS is looping on a cycle")
		}
	}
	dfs := 0
	for range g.DFS("A") {
		dfs++
		if dfs > 10 {
			t.Fatal("DFS is looping on a cycle")
		}
	}

	if bfs != 3 || dfs != 3 {
		t.Errorf("visited %d and %d nodes, want 3 each", bfs, dfs)
	}
}

func TestTraversalsStopEarly(t *testing.T) {
	g := sampleDAG()

	for _, tc := range []struct {
		name string
		seq  func() int
	}{
		{"BFS", func() int {
			n := 0
			for range g.BFS("A") {
				n++
				if n == 2 {
					break
				}
			}
			return n
		}},
		{"DFS", func() int {
			n := 0
			for range g.DFS("A") {
				n++
				if n == 2 {
					break
				}
			}
			return n
		}},
		{"DFSRecursive", func() int {
			n := 0
			for range g.DFSRecursive("A") {
				n++
				if n == 2 {
					break
				}
			}
			return n
		}},
		{"BFSLevels", func() int {
			n := 0
			for range g.BFSLevels("A") {
				n++
				if n == 1 {
					break
				}
			}
			return n
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.seq(); got > 2 {
				t.Errorf("visited %d after breaking", got)
			}
		})
	}
}

func TestTraversalFromAbsentNode(t *testing.T) {
	g := sampleDAG()

	for v := range g.BFS("nope") {
		t.Errorf("BFS from an absent node yielded %q", v)
	}
	for v := range g.DFS("nope") {
		t.Errorf("DFS from an absent node yielded %q", v)
	}
}

func TestComponents(t *testing.T) {
	g := New[string]()
	g.AddEdge("A", "B")
	g.AddEdge("C", "D")
	g.AddNode("E") // isolated, and the reason AddNode exists

	components, err := g.Components()
	if err != nil {
		t.Fatalf("Components() failed: %v", err)
	}

	want := [][]string{{"A", "B"}, {"C", "D"}, {"E"}}
	if len(components) != len(want) {
		t.Fatalf("got %d components, want %d: %v", len(components), len(want), components)
	}
	for i := range want {
		if !slices.Equal(components[i], want[i]) {
			t.Errorf("component %d = %v, want %v", i, components[i], want[i])
		}
	}

	connected, err := g.IsConnected()
	if err != nil {
		t.Fatal(err)
	}
	if connected {
		t.Error("IsConnected() = true for a graph with three components")
	}
}

func TestComponentsRejectsDirected(t *testing.T) {
	g := sampleDAG()

	if _, err := g.Components(); !errors.Is(err, ErrDirected) {
		t.Errorf("Components() on a directed graph returned %v, want ErrDirected", err)
	}
}

func TestEmptyGraph(t *testing.T) {
	g := New[string]()

	if g.Len() != 0 || g.EdgeCount() != 0 {
		t.Error("a new graph should be empty")
	}

	components, err := g.Components()
	if err != nil || len(components) != 0 {
		t.Errorf("Components() = %v, %v", components, err)
	}

	connected, err := g.IsConnected()
	if err != nil || !connected {
		t.Errorf("IsConnected() = %v, %v; an empty graph is connected", connected, err)
	}
}

func TestString(t *testing.T) {
	directed := sampleDAG()
	want := "A -> B, C\nB -> D\nC -> D\nD -> (none)\n"
	if got := directed.String(); got != want {
		t.Errorf("String() =\n%q\nwant\n%q", got, want)
	}

	weighted := New[string]()
	weighted.AddWeightedEdge("A", "B", 7)
	if got, want := weighted.String(), "A -- B(7)\nB -- A(7)\n"; got != want {
		t.Errorf("String() =\n%q\nwant\n%q", got, want)
	}
}
