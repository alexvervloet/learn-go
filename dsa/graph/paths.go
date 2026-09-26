package graph

import (
	"container/heap"
	"errors"
	"math"
)

// Errors returned by the path and ordering functions. Sentinel errors so callers
// can use errors.Is, which is the Go convention for "this specific thing went
// wrong" as opposed to a formatted message.
var (
	// ErrNoPath means the destination is not reachable from the source.
	ErrNoPath = errors.New("graph: no path")

	// ErrNotFound means a node is not in the graph at all, which is a different
	// problem from being unreachable and usually a caller bug.
	ErrNotFound = errors.New("graph: node not found")

	// ErrNegativeWeight means Dijkstra was asked to do something it cannot.
	ErrNegativeWeight = errors.New("graph: negative edge weight")

	// ErrCycle means a topological sort was asked for on a graph that has one.
	ErrCycle = errors.New("graph: cycle detected")

	// ErrDirected means an undirected-only operation was called on a directed
	// graph.
	ErrDirected = errors.New("graph: operation requires an undirected graph")
)

// ShortestPath returns the fewest-edges path from from to to, ignoring weights.
//
// BFS, and the reason BFS gives the right answer is that it visits nodes in
// distance order: the first time it reaches the destination it has done so by a
// shortest route, because any shorter one would have been found in an earlier
// level. Nothing about that argument survives if the edges have weights, which is
// what Dijkstra is for.
//
// The path is rebuilt from a parent map rather than carried along the search. A
// BFS that appends the path so far to every queue entry is a common shape and
// costs O(V^2) memory for a graph where it could be O(V).
func (g *Graph[V]) ShortestPath(from, to V) ([]V, error) {
	if !g.Has(from) || !g.Has(to) {
		return nil, ErrNotFound
	}
	if from == to {
		return []V{from}, nil
	}

	parent := map[V]V{from: from}
	queue := []V{from}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		for _, e := range g.adj[current] {
			if _, seen := parent[e.To]; seen {
				continue
			}
			parent[e.To] = current

			if e.To == to {
				return rebuild(parent, from, to), nil
			}

			queue = append(queue, e.To)
		}
	}

	return nil, ErrNoPath
}

// rebuild walks the parent map backwards from to and reverses the result.
func rebuild[V comparable](parent map[V]V, from, to V) []V {
	path := []V{to}

	for current := to; current != from; {
		current = parent[current]
		path = append(path, current)
	}

	// Reverse in place. slices.Reverse exists and this is spelled out because the
	// index arithmetic is the part people get wrong.
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}

	return path
}

// Distances returns the number of edges from start to every reachable node.
func (g *Graph[V]) Distances(start V) (map[V]int, error) {
	if !g.Has(start) {
		return nil, ErrNotFound
	}

	dist := map[V]int{start: 0}
	queue := []V{start}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		for _, e := range g.adj[current] {
			if _, seen := dist[e.To]; seen {
				continue
			}
			dist[e.To] = dist[current] + 1
			queue = append(queue, e.To)
		}
	}

	return dist, nil
}

// Dijkstra returns the cheapest path from from to to, and its total weight.
//
// The algorithm is BFS with a priority queue instead of a plain queue: always
// expand the unvisited node with the smallest known distance. That greedy choice
// is provably right because, with no negative weights, the smallest tentative
// distance in the frontier cannot be improved by going through anything else in
// the frontier, since every detour only adds weight.
//
// Negative weights break exactly that argument, so they are rejected rather than
// silently producing a wrong answer. The fix for negative weights is
// Bellman-Ford, which is O(V*E) instead of O(E log V).
func (g *Graph[V]) Dijkstra(from, to V) ([]V, int, error) {
	if !g.Has(from) || !g.Has(to) {
		return nil, 0, ErrNotFound
	}

	for _, v := range g.nodes {
		for _, e := range g.adj[v] {
			if e.Weight < 0 {
				return nil, 0, ErrNegativeWeight
			}
		}
	}

	dist := map[V]int{from: 0}
	parent := map[V]V{from: from}
	done := make(map[V]bool)

	pq := &priorityQueue[V]{{node: from, dist: 0}}
	heap.Init(pq)

	for pq.Len() > 0 {
		// heap.Pop returns any, so the assertion is unavoidable. This is what
		// container/heap costs: it predates generics and its interface is
		// expressed in any.
		current := heap.Pop(pq).(item[V])

		if done[current.node] {
			continue // a stale entry, see the note on decrease-key below
		}
		done[current.node] = true

		if current.node == to {
			return rebuild(parent, from, to), current.dist, nil
		}

		for _, e := range g.adj[current.node] {
			if done[e.To] {
				continue
			}

			through := current.dist + e.Weight
			if known, seen := dist[e.To]; seen && known <= through {
				continue
			}

			dist[e.To] = through
			parent[e.To] = current.node

			// A textbook Dijkstra would DECREASE the existing entry's key.
			// container/heap can do that with heap.Fix, but only if you track
			// each node's index in the heap. Pushing a second entry and skipping
			// stale pops instead is what almost every real implementation does:
			// the heap holds up to O(E) entries rather than O(V), and the log
			// factor absorbs it.
			heap.Push(pq, item[V]{node: e.To, dist: through})
		}
	}

	return nil, 0, ErrNoPath
}

// DijkstraAll returns the cheapest distance from start to every reachable node.
func (g *Graph[V]) DijkstraAll(start V) (map[V]int, error) {
	if !g.Has(start) {
		return nil, ErrNotFound
	}

	dist := map[V]int{start: 0}
	done := make(map[V]bool)

	pq := &priorityQueue[V]{{node: start, dist: 0}}

	for pq.Len() > 0 {
		current := heap.Pop(pq).(item[V])

		if done[current.node] {
			continue
		}
		done[current.node] = true

		for _, e := range g.adj[current.node] {
			if e.Weight < 0 {
				return nil, ErrNegativeWeight
			}

			through := current.dist + e.Weight
			if known, seen := dist[e.To]; seen && known <= through {
				continue
			}

			dist[e.To] = through
			heap.Push(pq, item[V]{node: e.To, dist: through})
		}
	}

	return dist, nil
}

// Infinity is the distance reported for an unreachable node by callers that want
// a number rather than a missing map key.
const Infinity = math.MaxInt

// item is one entry in the priority queue.
type item[V comparable] struct {
	node V
	dist int
}

// priorityQueue implements container/heap for item.
//
// This is the part of Go that shows its age. heap.Interface needs five methods,
// two of which come from sort.Interface, and Push and Pop take and return `any`
// because the package predates type parameters. A generic heap is thirty lines
// and the stdlib still does not have one; see patterns/heap for that version.
//
// The subtlety is that heap.Push and h.Push are different functions. heap.Push
// appends through this method and then sifts up; the method itself must not sift.
// Calling h.Push directly, which compiles, leaves the heap invariant broken and
// produces wrong answers with no error anywhere.
type priorityQueue[V comparable] []item[V]

func (pq priorityQueue[V]) Len() int { return len(pq) }

// Less makes this a MIN-heap: the smallest distance comes out first. Reversing it
// is the entire difference between Dijkstra and a longest-path search.
func (pq priorityQueue[V]) Less(i, j int) bool { return pq[i].dist < pq[j].dist }

func (pq priorityQueue[V]) Swap(i, j int) { pq[i], pq[j] = pq[j], pq[i] }

// Push appends. The pointer receiver is required: a value receiver would append
// to a copy and lose the element, which compiles and silently does nothing.
func (pq *priorityQueue[V]) Push(x any) { *pq = append(*pq, x.(item[V])) }

// Pop removes and returns the LAST element. heap.Pop has already swapped the
// minimum into that position, which is why this looks like it returns the wrong
// end.
func (pq *priorityQueue[V]) Pop() any {
	old := *pq
	last := len(old) - 1

	popped := old[last]

	var zero item[V]
	old[last] = zero // drop the reference so a V holding a pointer is not pinned

	*pq = old[:last]
	return popped
}
