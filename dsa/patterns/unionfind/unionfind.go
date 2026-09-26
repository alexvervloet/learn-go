// Package unionfind implements disjoint-set union, also called union-find.
//
// # The tell
//
// "Connected components", "merge these groups", "detect a cycle", "are these two in the same
// group", asked AS EDGES ARRIVE. The last part is the signal. BFS or DFS answers all of
// those for a finished graph; union-find answers them incrementally, after every edge, with
// no rebuilding.
//
// Use a traversal when you need the actual path. Use this when the question is only about
// connectivity.
//
// # The structure
//
// A forest, where each set is a tree and the root is the set's name. Two operations:
//
//	Find(x)      walk to the root of x's tree
//	Union(x, y)  point one root at the other
//
// That is the whole data structure. Everything below is about keeping the trees short.
//
// # Two optimisations, and both are needed
//
// PATH COMPRESSION: on the way back from a Find, point every node passed at the root. The
// next Find on any of them is one step.
//
// UNION BY SIZE: when merging, attach the smaller tree under the larger root. Without it, a
// sequence of unions can build a chain and every Find walks it.
//
// Together they give O(alpha(n)) amortised, where alpha is the inverse Ackermann function
// and is at most 4 for any n that fits in the universe. Separately, each gives O(log n).
// Neither gives O(1).
//
// The benchmarks in this package measure all four combinations, and the gap between "both"
// and "neither" is where the pattern earns its reputation.
//
// # What it cannot do
//
// There is no Split and no Remove. Union-find is a one-way structure: sets only ever merge.
// A problem that needs edges removed needs something else, usually a link-cut tree or an
// offline algorithm that processes the removals in reverse.
package unionfind

import (
	"cmp"
	"slices"
)

// Sets is a collection of disjoint sets over comparable elements.
//
// The zero value is an empty collection ready to use. Elements are added implicitly by Find
// or Union, which is what lets a caller feed it edges without declaring the vertices first.
type Sets[T comparable] struct {
	parent map[T]T
	size   map[T]int
	count  int // number of distinct sets

	// findSteps counts pointer hops across every Find, which is how the tests and
	// benchmarks measure the optimisations rather than asserting they work.
	findSteps int
}

// New returns an empty collection. The zero value works too.
func New[T comparable]() *Sets[T] { return &Sets[T]{} }

// Add makes x a set of its own if it is not already known, and reports whether it was new.
func (s *Sets[T]) Add(x T) bool {
	if s.parent == nil {
		s.parent = make(map[T]T)
		s.size = make(map[T]int)
	}
	if _, known := s.parent[x]; known {
		return false
	}

	s.parent[x] = x // a new set is its own root
	s.size[x] = 1
	s.count++

	return true
}

// Find returns the representative of x's set, adding x if it is unknown.
//
// The representative is an arbitrary member, not a meaningful one: it changes as sets merge.
// Code that stores a Find result and compares it later is a bug, because the same set can
// have a different representative after any Union.
func (s *Sets[T]) Find(x T) T {
	s.Add(x)

	// Walk to the root, remembering the path.
	root := x
	var path []T

	for s.parent[root] != root {
		path = append(path, root)
		root = s.parent[root]
		s.findSteps++
	}

	// Path compression: everything on the way now points straight at the root.
	for _, node := range path {
		s.parent[node] = root
	}

	return root
}

// Union merges the sets containing x and y, and reports whether they were separate.
//
// The false return is what makes cycle detection one line: an edge whose endpoints are
// already connected closes a cycle.
func (s *Sets[T]) Union(x, y T) bool {
	rootX, rootY := s.Find(x), s.Find(y)
	if rootX == rootY {
		return false
	}

	// Union by size: the smaller tree hangs under the larger root, so the depth grows as
	// slowly as possible. Without this, unioning in a line builds a chain.
	if s.size[rootX] < s.size[rootY] {
		rootX, rootY = rootY, rootX
	}

	s.parent[rootY] = rootX
	s.size[rootX] += s.size[rootY]
	s.count--

	return true
}

// Connected reports whether x and y are in the same set.
func (s *Sets[T]) Connected(x, y T) bool { return s.Find(x) == s.Find(y) }

// Count returns the number of distinct sets.
func (s *Sets[T]) Count() int { return s.count }

// Len returns the number of known elements.
func (s *Sets[T]) Len() int { return len(s.parent) }

// SizeOf returns how many elements are in x's set.
func (s *Sets[T]) SizeOf(x T) int { return s.size[s.Find(x)] }

// FindSteps returns the total number of parent pointers followed across every Find so far.
//
// Exposed for the tests and benchmarks. It is the only way to check that path compression
// and union by size are doing anything, short of timing, and timing is the thing this repo
// keeps getting wrong.
func (s *Sets[T]) FindSteps() int { return s.findSteps }

// Groups returns the sets, each sorted, ordered by their smallest element.
//
// Sorted because the underlying map has no order, so without it the result differs between
// runs and no test can be written. That is the same decision as in dsa/hashmap and
// dsa/graph, and it costs O(n log n) on a structure whose operations are otherwise nearly
// constant, which is why it is a separate function rather than something Count does.
func (s *Sets[T]) Groups(less func(a, b T) int) [][]T {
	byRoot := make(map[T][]T, s.count)

	for x := range s.parent {
		root := s.Find(x)
		byRoot[root] = append(byRoot[root], x)
	}

	out := make([][]T, 0, len(byRoot))
	for _, members := range byRoot {
		slices.SortFunc(members, less)
		out = append(out, members)
	}

	slices.SortFunc(out, func(a, b []T) int { return less(a[0], b[0]) })

	return out
}

// Applications
// ============

// Edge is a weighted connection between two nodes.
type Edge[T comparable] struct {
	From, To T
	Weight   int
}

// CountComponents returns how many connected components the given edges produce over the
// given nodes.
//
// Nodes are listed separately because an isolated node is still a component and edges alone
// cannot reveal one. That is the same trap as dsa/graph's AddNode.
func CountComponents[T comparable](nodes []T, edges []Edge[T]) int {
	s := New[T]()

	for _, n := range nodes {
		s.Add(n)
	}
	for _, e := range edges {
		s.Union(e.From, e.To)
	}

	return s.Count()
}

// HasCycle reports whether the given UNDIRECTED edges contain a cycle, and returns the edge
// that closes the first one found.
//
// One line of logic: an edge whose endpoints are already connected closes a cycle. That is
// the whole algorithm, and it is why union-find is the right tool when edges arrive over
// time: the answer is available after every single edge with no rebuilding.
//
// It does not work for DIRECTED graphs, because union-find has no notion of direction. Given
// a -> b and b -> a it reports a cycle, which is right, and given a -> b and a -> c it does
// not, which is also right; but given a -> c, b -> c it reports a cycle, and there is none.
// Directed cycle detection needs the three-colour DFS in dsa/graph.
func HasCycle[T comparable](edges []Edge[T]) (Edge[T], bool) {
	s := New[T]()

	for _, e := range edges {
		if e.From == e.To {
			return e, true // a self-loop is a cycle, and Union would report it as one
		}
		if !s.Union(e.From, e.To) {
			return e, true
		}
	}

	var zero Edge[T]
	return zero, false
}

// MinimumSpanningTree returns a minimum spanning forest of the given edges, and its total
// weight.
//
// Kruskal's algorithm, which is union-find plus a sort and nothing else: consider edges
// cheapest first, and keep an edge exactly when it joins two different components.
//
// It returns a FOREST, not a tree: on a disconnected graph there is no spanning tree, and
// returning the cheapest spanning structure of each component is more useful than an error.
// Callers that need a single tree can compare len(result) against len(nodes)-1.
//
// The greedy choice is correct by the cut property: for any way of splitting the nodes into
// two groups, the cheapest edge crossing the split is in some minimum spanning tree. Taking
// edges in weight order means every edge kept is the cheapest crossing the split between
// what it joins.
func MinimumSpanningTree[T cmp.Ordered](nodes []T, edges []Edge[T]) ([]Edge[T], int) {
	sorted := slices.Clone(edges)
	slices.SortFunc(sorted, func(a, b Edge[T]) int {
		if a.Weight != b.Weight {
			return cmp.Compare(a.Weight, b.Weight)
		}
		// Ties broken by endpoints, so the result is reproducible. Any minimum spanning
		// tree is as good as any other, and only one of them is testable.
		if a.From != b.From {
			return cmp.Compare(a.From, b.From)
		}
		return cmp.Compare(a.To, b.To)
	})

	s := New[T]()
	for _, n := range nodes {
		s.Add(n)
	}

	var kept []Edge[T]
	total := 0

	for _, e := range sorted {
		if !s.Union(e.From, e.To) {
			continue // both ends already connected, so this edge would close a cycle
		}

		kept = append(kept, e)
		total += e.Weight

		if len(kept) == s.Len()-1 {
			break // a spanning tree is complete; nothing later can be needed
		}
	}

	return kept, total
}

// MergeAccounts groups records that share any identifier.
//
// The classic application, and the reason it is here is the modelling step rather than the
// algorithm. The naive approach compares every pair of records, which is O(n^2) and gets
// transitivity wrong: if A shares an identifier with B and B with C, then A and C are the
// same even though they share nothing.
//
// Union-find handles transitivity for free. The trick is to union each record with its own
// identifiers, putting records and identifiers in the SAME structure, so a shared identifier
// pulls two records into one set without them ever being compared.
func MergeAccounts(records map[string][]string) [][]string {
	s := New[string]()

	for name, identifiers := range records {
		s.Add(name)

		for _, id := range identifiers {
			// Union the record with each of its identifiers. Two records sharing an
			// identifier are then transitively connected through it.
			s.Union(name, id)
		}
	}

	// Collect the record names per set, dropping the identifiers.
	isRecord := make(map[string]bool, len(records))
	for name := range records {
		isRecord[name] = true
	}

	byRoot := make(map[string][]string)
	for name := range records {
		root := s.Find(name)
		byRoot[root] = append(byRoot[root], name)
	}

	out := make([][]string, 0, len(byRoot))
	for _, group := range byRoot {
		slices.Sort(group)
		out = append(out, group)
	}
	slices.SortFunc(out, func(a, b []string) int { return cmp.Compare(a[0], b[0]) })

	return out
}
