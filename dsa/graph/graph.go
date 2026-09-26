// Package graph implements directed and undirected graphs as adjacency lists,
// with the traversals and shortest-path algorithms that sit on top of them.
//
// A graph is nodes and the edges between them, and almost every non-trivial
// problem is a graph problem once you see it. A road network, a package
// dependency tree, a social network, a state machine, a build pipeline, a maze:
// all the same structure, and the same six algorithms answer questions about all
// of them.
//
// # Adjacency list or adjacency matrix
//
// Two ways to store the edges, and the choice is about density, not taste.
//
//	list    map[V][]Edge      memory O(V+E), "are these adjacent" O(degree)
//	matrix  [][]int           memory O(V^2), "are these adjacent" O(1)
//
// A road network has maybe four edges per intersection, so a matrix for a million
// intersections would be a trillion entries to hold four million edges. A list
// wins by a factor of 250,000. A dense graph where most pairs are connected flips
// it: the matrix is one contiguous allocation with no pointers to chase, and
// matrix.go measures where the crossover actually falls.
//
// This package is adjacency-list first, because real graphs are sparse.
//
// # Determinism
//
// Neighbours come back in the order their edges were added, not in map order. A
// graph has no inherent order, so BFS and DFS could legitimately return any of
// several answers, and an implementation backed by a map returns a DIFFERENT one
// on every run. That makes tests and examples impossible to write and hides real
// bugs behind "it is random anyway". Insertion order costs one slice per node and
// makes every output here reproducible.
package graph

import (
	"fmt"
	"slices"
	"strings"
)

// Edge is a link to a node, with a weight. Unweighted edges get weight 1.
type Edge[V comparable] struct {
	To     V
	Weight int
}

// Graph is a set of nodes and the edges between them.
//
// Must be created with New or NewDirected: the zero value has no maps and no
// direction, and a graph that silently treats itself as undirected when you meant
// otherwise is worse than a nil panic.
type Graph[V comparable] struct {
	directed bool
	nodes    []V             // insertion order, for deterministic iteration
	adj      map[V][]Edge[V] // insertion order per node
	incoming map[V]int       // in-degree, maintained for topological sort
}

// New returns an empty undirected graph. An edge added between a and b can be
// walked in both directions.
func New[V comparable]() *Graph[V] {
	return &Graph[V]{adj: make(map[V][]Edge[V]), incoming: make(map[V]int)}
}

// NewDirected returns an empty directed graph. An edge from a to b cannot be
// walked from b.
func NewDirected[V comparable]() *Graph[V] {
	g := New[V]()
	g.directed = true
	return g
}

// Directed reports whether edges are one-way.
func (g *Graph[V]) Directed() bool { return g.directed }

// Len reports the number of nodes.
func (g *Graph[V]) Len() int { return len(g.nodes) }

// EdgeCount reports the number of edges. An undirected edge is counted once,
// although it is stored twice.
func (g *Graph[V]) EdgeCount() int {
	total := 0
	for _, edges := range g.adj {
		total += len(edges)
	}
	if g.directed {
		return total
	}
	return total / 2
}

// AddNode adds a node with no edges and reports whether it was new.
//
// Needed because a node can exist without edges, and an algorithm that only ever
// sees nodes through edges silently loses every isolated one.
func (g *Graph[V]) AddNode(v V) bool {
	if _, ok := g.adj[v]; ok {
		return false
	}

	g.adj[v] = nil
	g.nodes = append(g.nodes, v)
	g.incoming[v] = 0

	return true
}

// Has reports whether the node is in the graph.
func (g *Graph[V]) Has(v V) bool {
	_, ok := g.adj[v]
	return ok
}

// AddEdge adds an unweighted edge and reports whether it was new.
func (g *Graph[V]) AddEdge(from, to V) bool { return g.AddWeightedEdge(from, to, 1) }

// AddWeightedEdge adds an edge with a weight and reports whether it was new.
//
// Adding the same edge twice replaces the weight rather than adding a parallel
// edge. A multigraph is a different structure; silently accumulating duplicates
// is a bug that shows up as a wrong shortest path much later.
func (g *Graph[V]) AddWeightedEdge(from, to V, weight int) bool {
	g.AddNode(from)
	g.AddNode(to)

	if replaced := g.setEdge(from, to, weight); replaced {
		if !g.directed {
			g.setEdge(to, from, weight)
		}
		return false
	}

	g.incoming[to]++

	if !g.directed {
		g.setEdge(to, from, weight)
		g.incoming[from]++
	}

	return true
}

// setEdge writes one direction and reports whether it replaced an existing edge.
func (g *Graph[V]) setEdge(from, to V, weight int) bool {
	for i := range g.adj[from] {
		if g.adj[from][i].To == to {
			g.adj[from][i].Weight = weight
			return true
		}
	}

	g.adj[from] = append(g.adj[from], Edge[V]{To: to, Weight: weight})
	return false
}

// RemoveEdge removes an edge and reports whether it was there.
func (g *Graph[V]) RemoveEdge(from, to V) bool {
	if !g.unsetEdge(from, to) {
		return false
	}

	g.incoming[to]--

	if !g.directed {
		g.unsetEdge(to, from)
		g.incoming[from]--
	}

	return true
}

func (g *Graph[V]) unsetEdge(from, to V) bool {
	edges := g.adj[from]

	idx := slices.IndexFunc(edges, func(e Edge[V]) bool { return e.To == to })
	if idx < 0 {
		return false
	}

	// slices.Delete preserves order, which is the point of storing edges in a
	// slice at all. The swap-with-last trick would be O(1) and would make every
	// traversal in this package non-reproducible.
	g.adj[from] = slices.Delete(edges, idx, idx+1)
	return true
}

// HasEdge reports whether an edge runs from from to to.
//
// O(degree), because the edges are a slice. A map[V]map[V]int would make this
// O(1) and would give up insertion order, which every test and example here
// depends on. For the degrees real graphs have, four or five, the scan is faster
// than hashing anyway.
func (g *Graph[V]) HasEdge(from, to V) bool {
	_, ok := g.Weight(from, to)
	return ok
}

// Weight returns the weight of an edge and reports whether the edge exists.
func (g *Graph[V]) Weight(from, to V) (int, bool) {
	for _, e := range g.adj[from] {
		if e.To == to {
			return e.Weight, true
		}
	}
	return 0, false
}

// Nodes returns the nodes in insertion order, as a copy.
func (g *Graph[V]) Nodes() []V { return slices.Clone(g.nodes) }

// Edges returns the edges out of v in insertion order, as a copy.
func (g *Graph[V]) Edges(v V) []Edge[V] { return slices.Clone(g.adj[v]) }

// Neighbours returns the nodes reachable from v by one edge, in insertion order.
func (g *Graph[V]) Neighbours(v V) []V {
	out := make([]V, 0, len(g.adj[v]))
	for _, e := range g.adj[v] {
		out = append(out, e.To)
	}
	return out
}

// Degree returns the number of edges out of v.
func (g *Graph[V]) Degree(v V) int { return len(g.adj[v]) }

// InDegree returns the number of edges into v.
//
// Maintained incrementally rather than counted on demand, because Kahn's
// topological sort needs it for every node and recounting would make that
// algorithm O(V*E) instead of O(V+E).
func (g *Graph[V]) InDegree(v V) int { return g.incoming[v] }

// String renders the graph as one line per node, in insertion order.
func (g *Graph[V]) String() string {
	arrow := " -- "
	if g.directed {
		arrow = " -> "
	}

	var sb strings.Builder
	for _, v := range g.nodes {
		fmt.Fprintf(&sb, "%v%s", v, arrow)

		parts := make([]string, 0, len(g.adj[v]))
		for _, e := range g.adj[v] {
			if e.Weight == 1 {
				parts = append(parts, fmt.Sprint(e.To))
				continue
			}
			parts = append(parts, fmt.Sprintf("%v(%d)", e.To, e.Weight))
		}

		if len(parts) == 0 {
			sb.WriteString("(none)")
		} else {
			sb.WriteString(strings.Join(parts, ", "))
		}
		sb.WriteString("\n")
	}

	return sb.String()
}
