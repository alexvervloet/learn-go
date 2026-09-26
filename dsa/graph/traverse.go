package graph

import "iter"

// Traversals
// ==========
//
// Two ways to visit everything reachable from a node, and the only difference is
// which container holds the frontier.
//
//	BFS  a QUEUE   visits by distance: all neighbours, then all their neighbours
//	DFS  a STACK   visits by depth: follow one path to the end, then back up
//
// Swap the queue for a stack and breadth-first becomes depth-first. That is worth
// knowing because it means one of them is never harder to write than the other,
// and because the choice decides what the traversal is good for:
//
//	BFS  shortest path in an UNWEIGHTED graph, level-by-level anything
//	DFS  cycle detection, topological sort, connected components, backtracking
//
// Both are O(V+E) and both need a visited set. Without one, a graph with a cycle
// loops forever, and even an acyclic graph with a diamond in it visits nodes
// twice.

// BFS iterates the nodes reachable from start, nearest first.
//
// The visited mark goes on when a node is ENQUEUED, not when it is dequeued.
// Marking on dequeue looks equivalent and is not: a node with two in-edges gets
// queued twice before either copy comes off, so it is visited twice and the queue
// can grow to O(E) instead of O(V).
func (g *Graph[V]) BFS(start V) iter.Seq[V] {
	return func(yield func(V) bool) {
		if !g.Has(start) {
			return
		}

		visited := map[V]bool{start: true}
		queue := []V{start}

		for len(queue) > 0 {
			current := queue[0]
			queue = queue[1:]

			if !yield(current) {
				return
			}

			for _, e := range g.adj[current] {
				if visited[e.To] {
					continue
				}
				visited[e.To] = true
				queue = append(queue, e.To)
			}
		}
	}
}

// BFSLevels iterates the nodes reachable from start, one distance band at a time.
//
// The trick is to record the queue's length before draining it: everything in the
// queue at that moment is exactly one level. This is how "how many friends of
// friends" and "shortest path in moves" problems are answered without tracking a
// distance per node.
func (g *Graph[V]) BFSLevels(start V) iter.Seq2[int, []V] {
	return func(yield func(int, []V) bool) {
		if !g.Has(start) {
			return
		}

		visited := map[V]bool{start: true}
		queue := []V{start}

		for depth := 0; len(queue) > 0; depth++ {
			level := make([]V, len(queue))
			copy(level, queue)
			queue = queue[:0]

			for _, current := range level {
				for _, e := range g.adj[current] {
					if visited[e.To] {
						continue
					}
					visited[e.To] = true
					queue = append(queue, e.To)
				}
			}

			if !yield(depth, level) {
				return
			}
		}
	}
}

// DFS iterates the nodes reachable from start, depth first.
//
// Iterative with an explicit stack, and the neighbours are pushed in REVERSE so
// that popping visits them in insertion order. Without the reversal a stack-based
// DFS visits neighbours backwards, which is correct and looks like a bug in every
// test expectation.
//
// The visited mark goes on at POP time here, not at push time, which is the
// opposite of BFS. A node can be pushed several times before it is popped, and
// checking on pop is what keeps the first path to reach it winning.
func (g *Graph[V]) DFS(start V) iter.Seq[V] {
	return func(yield func(V) bool) {
		if !g.Has(start) {
			return
		}

		visited := make(map[V]bool)
		stack := []V{start}

		for len(stack) > 0 {
			last := len(stack) - 1
			current := stack[last]
			stack = stack[:last]

			if visited[current] {
				continue
			}
			visited[current] = true

			if !yield(current) {
				return
			}

			edges := g.adj[current]
			for i := len(edges) - 1; i >= 0; i-- {
				if !visited[edges[i].To] {
					stack = append(stack, edges[i].To)
				}
			}
		}
	}
}

// DFSRecursive iterates depth first using the call stack.
//
// Here for the comparison: it is shorter, it visits in the same order, and it
// needs no reversal trick because the loop over neighbours already runs forwards.
// The explicit-stack version exists because a recursive DFS on a graph with a
// long path is as deep as that path, and because the iterative one can be paused,
// which is what makes an iterator natural.
func (g *Graph[V]) DFSRecursive(start V) iter.Seq[V] {
	return func(yield func(V) bool) {
		if !g.Has(start) {
			return
		}

		visited := make(map[V]bool)

		var walk func(V) bool
		walk = func(v V) bool {
			visited[v] = true

			if !yield(v) {
				return false
			}

			for _, e := range g.adj[v] {
				if visited[e.To] {
					continue
				}
				if !walk(e.To) {
					return false
				}
			}
			return true
		}

		walk(start)
	}
}

// Reachable returns every node reachable from start, including start itself, in
// BFS order.
func (g *Graph[V]) Reachable(start V) []V {
	var out []V
	for v := range g.BFS(start) {
		out = append(out, v)
	}
	return out
}

// Components returns the connected components of an undirected graph, each in BFS
// order, with the components themselves in node insertion order.
//
// On a DIRECTED graph this returns something, and that something is not a
// meaningful answer: the directed equivalent is strongly connected components,
// which needs Tarjan's or Kosaraju's algorithm. Rather than return a plausible
// wrong answer, this reports it.
func (g *Graph[V]) Components() ([][]V, error) {
	if g.directed {
		return nil, ErrDirected
	}

	seen := make(map[V]bool, len(g.nodes))
	var out [][]V

	for _, start := range g.nodes {
		if seen[start] {
			continue
		}

		var component []V
		for v := range g.BFS(start) {
			seen[v] = true
			component = append(component, v)
		}
		out = append(out, component)
	}

	return out, nil
}

// IsConnected reports whether every node is reachable from every other, for an
// undirected graph. An empty graph is connected.
func (g *Graph[V]) IsConnected() (bool, error) {
	components, err := g.Components()
	if err != nil {
		return false, err
	}
	return len(components) <= 1, nil
}
