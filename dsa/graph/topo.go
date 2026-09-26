package graph

// Topological sort and cycle detection
// ====================================
//
// A topological order lists the nodes of a directed graph so that every edge
// points forwards. It is what "install these packages in a valid order", "run
// these build steps", and "which migrations must run first" all reduce to.
//
// It exists if and only if the graph has no cycle, which is why the two
// algorithms below are the same algorithm read two ways. A topological sort that
// gets stuck has found a cycle, and a cycle detector that never gets stuck has
// found a topological order.

// TopoSort returns a topological order, or ErrCycle if the graph has a cycle.
//
// Kahn's algorithm: repeatedly take a node with no remaining incoming edges, emit
// it, and remove its outgoing edges. If the graph runs out of such nodes before
// running out of nodes, what is left is a cycle.
//
// The in-degree counts are copied rather than mutated, so the graph is unchanged.
// The obvious implementation deletes edges as it goes and destroys the input,
// which is a surprising thing for a function called Sort to do.
func (g *Graph[V]) TopoSort() ([]V, error) {
	if !g.directed {
		// Every undirected edge is a two-cycle, so this is never answerable.
		return nil, ErrDirected
	}

	remaining := make(map[V]int, len(g.nodes))
	var ready []V

	for _, v := range g.nodes {
		remaining[v] = g.incoming[v]
		if remaining[v] == 0 {
			ready = append(ready, v)
		}
	}

	order := make([]V, 0, len(g.nodes))

	for len(ready) > 0 {
		// Taking from the front rather than the back keeps the output in node
		// insertion order among the nodes that are ready at the same time, which
		// makes the result reproducible. Any order is a valid topological order;
		// only one of them is testable.
		current := ready[0]
		ready = ready[1:]

		order = append(order, current)

		for _, e := range g.adj[current] {
			remaining[e.To]--
			if remaining[e.To] == 0 {
				ready = append(ready, e.To)
			}
		}
	}

	if len(order) != len(g.nodes) {
		// The nodes left over are exactly those in or downstream of a cycle.
		return nil, ErrCycle
	}

	return order, nil
}

// HasCycle reports whether the graph contains a cycle.
//
// For a DIRECTED graph the test is "is any node reachable from itself", and the
// standard way to answer it is a DFS with three colours rather than two:
//
//	white  not visited
//	grey   on the current DFS path
//	black  fully explored, nothing left below it
//
// An edge to a GREY node is a back edge and therefore a cycle. An edge to a BLACK
// node is not: it means two paths converge, which a diamond does and which is
// perfectly acyclic. A two-colour visited set cannot tell those apart and reports
// every diamond as a cycle, which is the classic bug here.
//
// For an UNDIRECTED graph, every edge is a two-cycle by definition, so the
// question becomes "is there a cycle other than an edge and its reverse", and the
// test is whether a DFS ever reaches an already-visited node that is not the one
// it just came from.
func (g *Graph[V]) HasCycle() bool {
	if !g.directed {
		return g.hasUndirectedCycle()
	}

	const (
		white = 0
		grey  = 1
		black = 2
	)

	colour := make(map[V]int, len(g.nodes))

	var walk func(V) bool
	walk = func(v V) bool {
		colour[v] = grey

		for _, e := range g.adj[v] {
			switch colour[e.To] {
			case grey:
				return true // a back edge
			case white:
				if walk(e.To) {
					return true
				}
			}
			// black: a converging path, not a cycle
		}

		colour[v] = black
		return false
	}

	for _, v := range g.nodes {
		if colour[v] == white && walk(v) {
			return true
		}
	}

	return false
}

func (g *Graph[V]) hasUndirectedCycle() bool {
	visited := make(map[V]bool, len(g.nodes))

	var walk func(v V, from V, hasFrom bool) bool
	walk = func(v V, from V, hasFrom bool) bool {
		visited[v] = true

		for _, e := range g.adj[v] {
			if hasFrom && e.To == from {
				continue // the edge we arrived on, not a cycle
			}
			if visited[e.To] {
				return true
			}
			if walk(e.To, v, true) {
				return true
			}
		}
		return false
	}

	for _, v := range g.nodes {
		if !visited[v] {
			var zero V
			if walk(v, zero, false) {
				return true
			}
		}
	}

	return false
}

// FindCycle returns one cycle as a list of nodes, with the first node repeated at
// the end, or nil if the graph is acyclic. Directed graphs only.
//
// Useful in a way that HasCycle is not: a build tool that says "there is a
// dependency cycle" is much less help than one that says which packages are in it.
func (g *Graph[V]) FindCycle() []V {
	if !g.directed {
		return nil
	}

	const (
		white = 0
		grey  = 1
		black = 2
	)

	colour := make(map[V]int, len(g.nodes))
	var path []V

	var walk func(V) []V
	walk = func(v V) []V {
		colour[v] = grey
		path = append(path, v)

		for _, e := range g.adj[v] {
			switch colour[e.To] {
			case grey:
				// Cut the path back to where the cycle starts.
				start := 0
				for i, p := range path {
					if p == e.To {
						start = i
						break
					}
				}
				cycle := append([]V{}, path[start:]...)
				return append(cycle, e.To)

			case white:
				if cycle := walk(e.To); cycle != nil {
					return cycle
				}
			}
		}

		colour[v] = black
		path = path[:len(path)-1]

		return nil
	}

	for _, v := range g.nodes {
		if colour[v] == white {
			if cycle := walk(v); cycle != nil {
				return cycle
			}
		}
	}

	return nil
}
