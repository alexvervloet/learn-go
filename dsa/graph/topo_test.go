package graph

import (
	"errors"
	"slices"
	"testing"
)

func TestTopoSort(t *testing.T) {
	// A build pipeline: fetch before compile, compile before test and lint, both
	// before release.
	g := NewDirected[string]()
	g.AddEdge("fetch", "compile")
	g.AddEdge("compile", "test")
	g.AddEdge("compile", "lint")
	g.AddEdge("test", "release")
	g.AddEdge("lint", "release")

	order, err := g.TopoSort()
	if err != nil {
		t.Fatalf("TopoSort failed: %v", err)
	}

	if len(order) != 5 {
		t.Fatalf("order has %d entries, want 5: %v", len(order), order)
	}

	// The reproducible answer this implementation gives. Several orders are
	// valid; asserting one of them is only reasonable because the algorithm is
	// deterministic, which is the whole reason for the insertion-order queue.
	if want := []string{"fetch", "compile", "test", "lint", "release"}; !slices.Equal(order, want) {
		t.Errorf("TopoSort() = %v, want %v", order, want)
	}
}

// TestTopoSortRespectsEveryEdge is the property to assert rather than one
// expected order, because every valid order satisfies it and nothing else does.
func TestTopoSortRespectsEveryEdge(t *testing.T) {
	g := NewDirected[int]()
	for from := range 30 {
		for _, to := range []int{from + 1, from + 7, from + 13} {
			if to < 40 {
				g.AddEdge(from, to)
			}
		}
	}

	order, err := g.TopoSort()
	if err != nil {
		t.Fatalf("TopoSort failed: %v", err)
	}

	position := make(map[int]int, len(order))
	for i, v := range order {
		position[v] = i
	}

	if len(order) != g.Len() {
		t.Fatalf("order has %d entries, graph has %d nodes", len(order), g.Len())
	}

	for _, from := range g.Nodes() {
		for _, to := range g.Neighbours(from) {
			if position[from] >= position[to] {
				t.Errorf("edge %d -> %d but %d comes first in the order", from, to, to)
			}
		}
	}
}

func TestTopoSortIncludesIsolatedNodes(t *testing.T) {
	g := NewDirected[string]()
	g.AddEdge("A", "B")
	g.AddNode("lonely")

	order, err := g.TopoSort()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(order, "lonely") {
		t.Errorf("TopoSort() = %v, dropped the isolated node", order)
	}
}

func TestTopoSortDetectsCycles(t *testing.T) {
	g := NewDirected[string]()
	g.AddEdge("A", "B")
	g.AddEdge("B", "C")
	g.AddEdge("C", "A")

	if _, err := g.TopoSort(); !errors.Is(err, ErrCycle) {
		t.Errorf("TopoSort() on a cyclic graph returned %v, want ErrCycle", err)
	}
}

func TestTopoSortRejectsUndirected(t *testing.T) {
	g := New[string]()
	g.AddEdge("A", "B")

	if _, err := g.TopoSort(); !errors.Is(err, ErrDirected) {
		t.Errorf("TopoSort() on an undirected graph returned %v, want ErrDirected", err)
	}
}

// TestTopoSortDoesNotMutateTheGraph: the obvious implementation deletes edges as
// it goes, which is a surprising thing for a function called Sort to do.
func TestTopoSortDoesNotMutateTheGraph(t *testing.T) {
	g := NewDirected[string]()
	g.AddEdge("A", "B")
	g.AddEdge("B", "C")

	edgesBefore := g.EdgeCount()

	if _, err := g.TopoSort(); err != nil {
		t.Fatal(err)
	}

	if g.EdgeCount() != edgesBefore {
		t.Errorf("EdgeCount() went from %d to %d", edgesBefore, g.EdgeCount())
	}

	// And it can be called twice with the same answer.
	first, _ := g.TopoSort()
	second, _ := g.TopoSort()
	if !slices.Equal(first, second) {
		t.Errorf("two calls disagree: %v and %v", first, second)
	}
}

func TestHasCycleDirected(t *testing.T) {
	tests := []struct {
		name  string
		edges [][2]string
		want  bool
	}{
		{"a chain", [][2]string{{"A", "B"}, {"B", "C"}}, false},
		{"a cycle", [][2]string{{"A", "B"}, {"B", "C"}, {"C", "A"}}, true},
		{"a self loop", [][2]string{{"A", "A"}}, true},
		{"a two-cycle", [][2]string{{"A", "B"}, {"B", "A"}}, true},
		{"a cycle off to one side", [][2]string{{"A", "B"}, {"C", "D"}, {"D", "C"}}, true},
		{"nothing", nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewDirected[string]()
			for _, e := range tt.edges {
				g.AddEdge(e[0], e[1])
			}

			if got := g.HasCycle(); got != tt.want {
				t.Errorf("HasCycle() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestDiamondIsNotACycle is the test that catches a two-colour visited set. Two
// paths converging on D is not a cycle, and an implementation that cannot tell a
// back edge from a converging edge says it is.
func TestDiamondIsNotACycle(t *testing.T) {
	g := sampleDAG() // A->B, A->C, B->D, C->D

	if g.HasCycle() {
		t.Error("HasCycle() = true for a diamond, which is acyclic")
	}
	if cycle := g.FindCycle(); cycle != nil {
		t.Errorf("FindCycle() = %v for an acyclic graph", cycle)
	}
	if _, err := g.TopoSort(); err != nil {
		t.Errorf("TopoSort() failed on an acyclic graph: %v", err)
	}
}

func TestHasCycleUndirected(t *testing.T) {
	tests := []struct {
		name  string
		edges [][2]string
		want  bool
	}{
		// A single undirected edge is stored both ways, so a naive directed check
		// reports a cycle here. It is a tree.
		{"one edge", [][2]string{{"A", "B"}}, false},
		{"a path", [][2]string{{"A", "B"}, {"B", "C"}, {"C", "D"}}, false},
		{"a star", [][2]string{{"A", "B"}, {"A", "C"}, {"A", "D"}}, false},
		{"a triangle", [][2]string{{"A", "B"}, {"B", "C"}, {"C", "A"}}, true},
		{"a square", [][2]string{{"A", "B"}, {"B", "C"}, {"C", "D"}, {"D", "A"}}, true},
		{"a tree with a cycle elsewhere", [][2]string{
			{"A", "B"}, {"C", "D"}, {"D", "E"}, {"E", "C"},
		}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := New[string]()
			for _, e := range tt.edges {
				g.AddEdge(e[0], e[1])
			}

			if got := g.HasCycle(); got != tt.want {
				t.Errorf("HasCycle() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFindCycle(t *testing.T) {
	g := NewDirected[string]()
	g.AddEdge("app", "lib")
	g.AddEdge("lib", "util")
	g.AddEdge("util", "lib") // the cycle
	g.AddEdge("app", "log")

	cycle := g.FindCycle()
	if cycle == nil {
		t.Fatal("FindCycle() found nothing")
	}

	// The first node is repeated at the end, so the cycle reads as a walk.
	if cycle[0] != cycle[len(cycle)-1] {
		t.Errorf("cycle = %v, expected the first node repeated at the end", cycle)
	}

	// And every step has to be a real edge.
	for i := 0; i < len(cycle)-1; i++ {
		if !g.HasEdge(cycle[i], cycle[i+1]) {
			t.Errorf("cycle = %v, but there is no edge %q -> %q", cycle, cycle[i], cycle[i+1])
		}
	}

	if want := []string{"lib", "util", "lib"}; !slices.Equal(cycle, want) {
		t.Errorf("cycle = %v, want %v", cycle, want)
	}
}
