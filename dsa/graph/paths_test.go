package graph

import (
	"errors"
	"maps"
	"slices"
	"testing"
)

// A weighted graph where the cheapest route is not the shortest one, which is the
// only shape that tells BFS and Dijkstra apart.
//
//	A --1-- B --1-- C      three edges, weight 3
//	 \-------10------/      one edge,   weight 10
func detourGraph() *Graph[string] {
	g := New[string]()
	g.AddWeightedEdge("A", "B", 1)
	g.AddWeightedEdge("B", "C", 1)
	g.AddWeightedEdge("A", "C", 10)
	return g
}

func TestShortestPathCountsEdgesNotWeights(t *testing.T) {
	g := detourGraph()

	path, err := g.ShortestPath("A", "C")
	if err != nil {
		t.Fatalf("ShortestPath failed: %v", err)
	}

	// BFS takes the single expensive edge, because it counts hops.
	if want := []string{"A", "C"}; !slices.Equal(path, want) {
		t.Errorf("ShortestPath(A, C) = %v, want %v", path, want)
	}
}

func TestDijkstraCountsWeights(t *testing.T) {
	g := detourGraph()

	path, cost, err := g.Dijkstra("A", "C")
	if err != nil {
		t.Fatalf("Dijkstra failed: %v", err)
	}

	if want := []string{"A", "B", "C"}; !slices.Equal(path, want) {
		t.Errorf("Dijkstra(A, C) = %v, want %v", path, want)
	}
	if cost != 2 {
		t.Errorf("cost = %d, want 2", cost)
	}
}

func TestShortestPathSameNode(t *testing.T) {
	g := detourGraph()

	path, err := g.ShortestPath("A", "A")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"A"}; !slices.Equal(path, want) {
		t.Errorf("ShortestPath(A, A) = %v, want %v", path, want)
	}
}

func TestShortestPathErrors(t *testing.T) {
	g := New[string]()
	g.AddEdge("A", "B")
	g.AddEdge("C", "D") // a separate component

	tests := []struct {
		name     string
		from, to string
		want     error
	}{
		{"unreachable", "A", "C", ErrNoPath},
		{"absent source", "Z", "A", ErrNotFound},
		{"absent destination", "A", "Z", ErrNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := g.ShortestPath(tt.from, tt.to); !errors.Is(err, tt.want) {
				t.Errorf("ShortestPath(%q, %q) = %v, want %v", tt.from, tt.to, err, tt.want)
			}
			if _, _, err := g.Dijkstra(tt.from, tt.to); !errors.Is(err, tt.want) {
				t.Errorf("Dijkstra(%q, %q) = %v, want %v", tt.from, tt.to, err, tt.want)
			}
		})
	}
}

// TestDijkstraRejectsNegativeWeights: the greedy argument depends on every detour
// costing more, and a negative edge breaks it. Returning a wrong answer quietly
// would be worse than refusing.
func TestDijkstraRejectsNegativeWeights(t *testing.T) {
	g := NewDirected[string]()
	g.AddWeightedEdge("A", "B", 2)
	g.AddWeightedEdge("A", "C", 5)
	g.AddWeightedEdge("B", "C", -4) // A->B->C costs -2, which Dijkstra will miss

	if _, _, err := g.Dijkstra("A", "C"); !errors.Is(err, ErrNegativeWeight) {
		t.Errorf("Dijkstra returned %v, want ErrNegativeWeight", err)
	}
	if _, err := g.DijkstraAll("A"); !errors.Is(err, ErrNegativeWeight) {
		t.Errorf("DijkstraAll returned %v, want ErrNegativeWeight", err)
	}
}

func TestDistances(t *testing.T) {
	g := NewDirected[string]()
	g.AddEdge("A", "B")
	g.AddEdge("B", "C")
	g.AddEdge("C", "D")
	g.AddEdge("A", "D") // a shortcut
	g.AddNode("E")      // unreachable

	dist, err := g.Distances("A")
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]int{"A": 0, "B": 1, "C": 2, "D": 1}
	if !maps.Equal(dist, want) {
		t.Errorf("Distances(A) = %v, want %v", dist, want)
	}
	if _, ok := dist["E"]; ok {
		t.Error("an unreachable node has a distance")
	}
}

func TestDijkstraAll(t *testing.T) {
	// The classic example, with the answer known independently.
	//
	//	A -4- B -8- C
	//	|     |     |
	//	8     11    2
	//	|     |     |
	//	H -7- I -6- G
	g := New[string]()
	for _, e := range []struct {
		from, to string
		w        int
	}{
		{"A", "B", 4}, {"A", "H", 8},
		{"B", "C", 8}, {"B", "H", 11},
		{"C", "D", 7}, {"C", "F", 4}, {"C", "I", 2},
		{"D", "E", 9}, {"D", "F", 14},
		{"E", "F", 10},
		{"F", "G", 2},
		{"G", "H", 1}, {"G", "I", 6},
		{"H", "I", 7},
	} {
		g.AddWeightedEdge(e.from, e.to, e.w)
	}

	dist, err := g.DijkstraAll("A")
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]int{
		"A": 0, "B": 4, "C": 12, "D": 19, "E": 21,
		"F": 11, "G": 9, "H": 8, "I": 14,
	}
	if !maps.Equal(dist, want) {
		t.Errorf("DijkstraAll(A) = %v\nwant %v", dist, want)
	}

	// And the single-target version has to agree with it.
	for node, expected := range want {
		_, cost, err := g.Dijkstra("A", node)
		if err != nil {
			t.Errorf("Dijkstra(A, %q) failed: %v", node, err)
			continue
		}
		if cost != expected {
			t.Errorf("Dijkstra(A, %q) = %d, DijkstraAll says %d", node, cost, expected)
		}
	}
}

// TestDijkstraPathIsWalkable: a cost with a path that does not exist is a bug that
// a cost-only assertion misses entirely.
func TestDijkstraPathIsWalkable(t *testing.T) {
	g := New[string]()
	g.AddWeightedEdge("A", "B", 3)
	g.AddWeightedEdge("B", "C", 4)
	g.AddWeightedEdge("A", "D", 1)
	g.AddWeightedEdge("D", "C", 1)

	path, cost, err := g.Dijkstra("A", "C")
	if err != nil {
		t.Fatal(err)
	}

	total := 0
	for i := 0; i < len(path)-1; i++ {
		w, ok := g.Weight(path[i], path[i+1])
		if !ok {
			t.Fatalf("the path uses a non-existent edge %q -> %q", path[i], path[i+1])
		}
		total += w
	}

	if total != cost {
		t.Errorf("the path costs %d but Dijkstra reported %d", total, cost)
	}
	if want := []string{"A", "D", "C"}; !slices.Equal(path, want) {
		t.Errorf("path = %v, want %v", path, want)
	}
}

// TestDijkstraStaleEntries exercises the decrease-key shortcut. A node reached
// first by an expensive route and later by a cheap one leaves a stale heap entry,
// and the `done` check is what stops it being used.
func TestDijkstraStaleEntries(t *testing.T) {
	g := NewDirected[string]()
	g.AddWeightedEdge("S", "A", 100)
	g.AddWeightedEdge("S", "B", 1)
	g.AddWeightedEdge("B", "A", 1) // A is much cheaper this way
	g.AddWeightedEdge("A", "T", 1)

	path, cost, err := g.Dijkstra("S", "T")
	if err != nil {
		t.Fatal(err)
	}

	if cost != 3 {
		t.Errorf("cost = %d, want 3", cost)
	}
	if want := []string{"S", "B", "A", "T"}; !slices.Equal(path, want) {
		t.Errorf("path = %v, want %v", path, want)
	}
}

func TestPriorityQueueIsAMinHeap(t *testing.T) {
	g := New[string]()
	for i, w := range []int{5, 3, 9, 1, 7} {
		g.AddWeightedEdge("S", string(rune('a'+i)), w)
	}

	dist, err := g.DijkstraAll("S")
	if err != nil {
		t.Fatal(err)
	}

	// Every neighbour's distance is its own edge weight, which only holds if the
	// queue hands them back cheapest first.
	for i, w := range []int{5, 3, 9, 1, 7} {
		node := string(rune('a' + i))
		if dist[node] != w {
			t.Errorf("dist[%q] = %d, want %d", node, dist[node], w)
		}
	}
}
