package graph_test

import (
	"errors"
	"fmt"
	"slices"

	"github.com/alexvervloet/learn-go/dsa/graph"
)

func ExampleGraph() {
	g := graph.NewDirected[string]()
	g.AddEdge("A", "B")
	g.AddEdge("A", "C")
	g.AddEdge("B", "D")
	g.AddEdge("C", "D")

	fmt.Print(g)
	fmt.Println("nodes:", g.Len(), "edges:", g.EdgeCount())

	// Output:
	// A -> B, C
	// B -> D
	// C -> D
	// D -> (none)
	// nodes: 4 edges: 4
}

// An undirected edge is stored twice and counted once.
func ExampleNew() {
	g := graph.New[string]()
	g.AddEdge("A", "B")

	fmt.Println(g.HasEdge("A", "B"), g.HasEdge("B", "A"))
	fmt.Println("edges:", g.EdgeCount())

	// Output:
	// true true
	// edges: 1
}

// BFS visits by distance, DFS by depth. The only difference in the code is a
// queue against a stack.
func ExampleGraph_BFS() {
	g := graph.NewDirected[string]()
	g.AddEdge("A", "B")
	g.AddEdge("A", "C")
	g.AddEdge("B", "D")
	g.AddEdge("C", "D")

	// Collected rather than printed one at a time: an Example's output is compared
	// after trimming the whole block, not each line, so a trailing space on the
	// first line is invisible in the source and fails the test.
	fmt.Println("BFS:", slices.Collect(g.BFS("A")))
	fmt.Println("DFS:", slices.Collect(g.DFS("A")))

	// Output:
	// BFS: [A B C D]
	// DFS: [A B D C]
}

// BFSLevels answers "how many hops away" without tracking a distance per node.
// Recording the queue's length before draining it is the whole trick.
func ExampleGraph_BFSLevels() {
	g := graph.New[string]()
	g.AddEdge("me", "ada")
	g.AddEdge("me", "bo")
	g.AddEdge("ada", "cy")
	g.AddEdge("bo", "cy")
	g.AddEdge("cy", "di")

	for hops, people := range g.BFSLevels("me") {
		fmt.Printf("%d hops: %v\n", hops, people)
	}

	// Output:
	// 0 hops: [me]
	// 1 hops: [ada bo]
	// 2 hops: [cy]
	// 3 hops: [di]
}

// ShortestPath counts edges. Dijkstra counts weights. On a graph where the
// cheapest route is not the shortest one, they disagree, and that disagreement is
// the only reason both exist.
func ExampleGraph_Dijkstra() {
	g := graph.New[string]()
	g.AddWeightedEdge("A", "B", 1)
	g.AddWeightedEdge("B", "C", 1)
	g.AddWeightedEdge("A", "C", 10)

	hops, _ := g.ShortestPath("A", "C")
	fmt.Println("fewest edges:", hops)

	cheap, cost, _ := g.Dijkstra("A", "C")
	fmt.Println("cheapest:    ", cheap, "cost", cost)

	// Output:
	// fewest edges: [A C]
	// cheapest:     [A B C] cost 2
}

// Negative weights break the argument Dijkstra rests on, so it refuses rather
// than returning a plausible wrong answer.
func ExampleGraph_Dijkstra_negativeWeight() {
	g := graph.NewDirected[string]()
	g.AddWeightedEdge("A", "B", 2)
	g.AddWeightedEdge("B", "C", -4)

	_, _, err := g.Dijkstra("A", "C")
	fmt.Println(errors.Is(err, graph.ErrNegativeWeight))
	fmt.Println(err)

	// Output:
	// true
	// graph: negative edge weight
}

// A topological order is what "run these steps in a valid sequence" means.
func ExampleGraph_TopoSort() {
	pipeline := graph.NewDirected[string]()
	pipeline.AddEdge("fetch", "compile")
	pipeline.AddEdge("compile", "test")
	pipeline.AddEdge("compile", "lint")
	pipeline.AddEdge("test", "release")
	pipeline.AddEdge("lint", "release")

	order, err := pipeline.TopoSort()
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(order)

	// Output:
	// [fetch compile test lint release]
}

// A topological order exists if and only if there is no cycle, so the same
// algorithm answers both questions.
func ExampleGraph_FindCycle() {
	deps := graph.NewDirected[string]()
	deps.AddEdge("app", "lib")
	deps.AddEdge("lib", "util")
	deps.AddEdge("util", "lib")

	_, err := deps.TopoSort()
	fmt.Println("sortable:", err == nil)
	fmt.Println("cycle:   ", deps.FindCycle())

	// Output:
	// sortable: false
	// cycle:    [lib util lib]
}

// Two paths converging is not a cycle. Telling a back edge from a converging edge
// is why cycle detection needs three colours rather than a visited set.
func ExampleGraph_HasCycle() {
	diamond := graph.NewDirected[string]()
	diamond.AddEdge("A", "B")
	diamond.AddEdge("A", "C")
	diamond.AddEdge("B", "D")
	diamond.AddEdge("C", "D")

	loop := graph.NewDirected[string]()
	loop.AddEdge("A", "B")
	loop.AddEdge("B", "A")

	fmt.Println("diamond:", diamond.HasCycle())
	fmt.Println("loop:   ", loop.HasCycle())

	// Output:
	// diamond: false
	// loop:    true
}

// Components needs AddNode, because a node with no edges is invisible to anything
// that only ever sees nodes through edges.
func ExampleGraph_Components() {
	g := graph.New[string]()
	g.AddEdge("A", "B")
	g.AddEdge("C", "D")
	g.AddNode("E")

	components, _ := g.Components()
	fmt.Println(components)

	connected, _ := g.IsConnected()
	fmt.Println("connected:", connected)

	// Output:
	// [[A B] [C D] [E]]
	// connected: false
}

// The matrix representation: one array read to answer "is there an edge", and
// O(V) memory squared to pay for it.
func ExampleMatrix() {
	m := graph.NewDirectedMatrix(4)
	m.AddEdge(0, 1)
	m.AddEdge(0, 2)
	m.AddEdge(1, 3)
	m.AddEdge(2, 3)

	fmt.Print(m)
	fmt.Println("neighbours of 0:", m.Neighbours(0))
	fmt.Println("BFS from 0:     ", m.BFS(0))

	// Output:
	//  0  1  1  0
	//  0  0  0  1
	//  0  0  0  1
	//  0  0  0  0
	// neighbours of 0: [1 2]
	// BFS from 0:      [0 1 2 3]
}
