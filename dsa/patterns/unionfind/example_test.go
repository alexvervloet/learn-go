package unionfind_test

import (
	"cmp"
	"fmt"

	"github.com/alexvervloet/learn-go/dsa/patterns/unionfind"
)

// The zero value works, and elements are added implicitly, which is what lets a caller feed
// in edges without declaring the vertices first.
func ExampleSets() {
	var s unionfind.Sets[string]

	s.Union("a", "b")
	s.Union("b", "c")
	s.Union("d", "e")

	fmt.Println("a and c:", s.Connected("a", "c")) // through b
	fmt.Println("a and d:", s.Connected("a", "d"))
	fmt.Println("sets:   ", s.Count())
	fmt.Println("size of a's set:", s.SizeOf("a"))

	s.Union("c", "d")
	fmt.Println("after merging:", s.Count(), "set of", s.SizeOf("a"))

	// Output:
	// a and c: true
	// a and d: false
	// sets:    2
	// size of a's set: 3
	// after merging: 1 set of 5
}

// Union returns false when the two are already connected, and that one line is the whole of
// cycle detection.
func ExampleSets_Union() {
	var s unionfind.Sets[int]

	fmt.Println(s.Union(1, 2)) // merged
	fmt.Println(s.Union(2, 3)) // merged
	fmt.Println(s.Union(1, 3)) // already connected, so this edge closes a cycle

	// Output:
	// true
	// true
	// false
}

// Groups sorts, because the underlying map has no order and an unsorted result would differ
// between runs.
func ExampleSets_Groups() {
	var s unionfind.Sets[string]
	s.Union("b", "a")
	s.Union("d", "c")
	s.Add("e")

	fmt.Println(s.Groups(cmp.Compare[string]))

	// Output:
	// [[a b] [c d] [e]]
}

// An isolated node is still a component, and edges alone cannot reveal one, so the nodes are
// passed separately.
func ExampleCountComponents() {
	nodes := []int{0, 1, 2, 3, 4}
	edges := []unionfind.Edge[int]{
		{From: 0, To: 1}, {From: 1, To: 2}, {From: 3, To: 4},
	}

	fmt.Println(unionfind.CountComponents(nodes, edges))

	// An extra isolated node is a third component.
	fmt.Println(unionfind.CountComponents(append(nodes, 5), edges))

	// Output:
	// 2
	// 3
}

// An edge whose endpoints are already connected closes a cycle. The answer is available
// after every single edge, with no rebuilding, which is what a traversal cannot offer.
func ExampleHasCycle() {
	tree := []unionfind.Edge[string]{{From: "a", To: "b"}, {From: "b", To: "c"}}
	triangle := append(tree, unionfind.Edge[string]{From: "c", To: "a"})

	_, ok := unionfind.HasCycle(tree)
	fmt.Println("tree:    ", ok)

	edge, ok := unionfind.HasCycle(triangle)
	fmt.Println("triangle:", ok, "closed by", edge.From, "to", edge.To)

	// Output:
	// tree:     false
	// triangle: true closed by c to a
}

// Kruskal's algorithm is union-find plus a sort: take edges cheapest first, and keep one
// exactly when it joins two different components.
func ExampleMinimumSpanningTree() {
	nodes := []string{"A", "B", "C", "D", "E", "F", "G"}
	edges := []unionfind.Edge[string]{
		{From: "A", To: "B", Weight: 7}, {From: "A", To: "D", Weight: 5},
		{From: "B", To: "C", Weight: 8}, {From: "B", To: "D", Weight: 9},
		{From: "B", To: "E", Weight: 7}, {From: "C", To: "E", Weight: 5},
		{From: "D", To: "E", Weight: 15}, {From: "D", To: "F", Weight: 6},
		{From: "E", To: "F", Weight: 8}, {From: "E", To: "G", Weight: 9},
		{From: "F", To: "G", Weight: 11},
	}

	kept, total := unionfind.MinimumSpanningTree(nodes, edges)

	for _, e := range kept {
		fmt.Printf("%s-%s %d\n", e.From, e.To, e.Weight)
	}
	fmt.Println("total:", total)

	// Output:
	// A-D 5
	// C-E 5
	// D-F 6
	// A-B 7
	// B-E 7
	// E-G 9
	// total: 39
}

// On a disconnected graph there is no spanning tree, and a forest is more useful than an
// error. A caller wanting one tree compares len(kept) against len(nodes)-1.
func ExampleMinimumSpanningTree_forest() {
	nodes := []int{0, 1, 2, 3, 4}
	edges := []unionfind.Edge[int]{
		{From: 0, To: 1, Weight: 1},
		{From: 2, To: 3, Weight: 2},
	}

	kept, total := unionfind.MinimumSpanningTree(nodes, edges)

	fmt.Println("edges:", len(kept), "total:", total)
	fmt.Println("is a single tree:", len(kept) == len(nodes)-1)

	// Output:
	// edges: 2 total: 3
	// is a single tree: false
}

// The modelling step is the lesson, not the algorithm. Records and identifiers go into the
// SAME structure, so a shared identifier pulls two records together without them ever being
// compared, and transitivity comes for free.
func ExampleMergeAccounts() {
	records := map[string][]string{
		"alice":   {"a@x.com", "alice@y.com"},
		"alicia":  {"alice@y.com"},
		"bob":     {"bob@x.com"},
		"robert":  {"bob@x.com", "rob@z.com"},
		"roberto": {"rob@z.com"},
		"carol":   {"carol@x.com"},
	}

	for _, group := range unionfind.MergeAccounts(records) {
		fmt.Println(group)
	}

	// Output:
	// [alice alicia]
	// [bob robert roberto]
	// [carol]
}
