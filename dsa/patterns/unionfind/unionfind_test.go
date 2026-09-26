package unionfind

import (
	"cmp"
	"math"
	"math/rand/v2"
	"slices"
	"testing"
)

func TestZeroValueIsUsable(t *testing.T) {
	var s Sets[string]

	if s.Count() != 0 || s.Len() != 0 {
		t.Error("a new collection should be empty")
	}

	// Find adds implicitly, which is what lets a caller feed edges without declaring
	// vertices first.
	if got := s.Find("a"); got != "a" {
		t.Errorf("Find(\"a\") = %q, want \"a\"", got)
	}
	if s.Count() != 1 || s.Len() != 1 {
		t.Errorf("after one Find: Count = %d, Len = %d; want 1, 1", s.Count(), s.Len())
	}
}

func TestAddAndUnion(t *testing.T) {
	s := New[int]()

	for i := range 5 {
		if !s.Add(i) {
			t.Errorf("Add(%d) reported the element already existed", i)
		}
	}
	if s.Add(0) {
		t.Error("adding the same element twice reported it was new")
	}
	if s.Count() != 5 {
		t.Errorf("Count = %d, want 5", s.Count())
	}

	if !s.Union(0, 1) {
		t.Error("Union(0, 1) reported they were already connected")
	}
	if s.Union(0, 1) {
		t.Error("unioning twice reported a merge the second time")
	}
	if s.Count() != 4 {
		t.Errorf("Count = %d after one union, want 4", s.Count())
	}

	if !s.Connected(0, 1) {
		t.Error("0 and 1 should be connected")
	}
	if s.Connected(0, 2) {
		t.Error("0 and 2 should not be connected")
	}
}

func TestTransitivity(t *testing.T) {
	s := New[string]()

	s.Union("a", "b")
	s.Union("b", "c")
	s.Union("d", "e")

	if !s.Connected("a", "c") {
		t.Error("a and c should be connected through b")
	}
	if s.Connected("a", "d") {
		t.Error("a and d should not be connected")
	}
	if s.Count() != 2 {
		t.Errorf("Count = %d, want 2", s.Count())
	}
	if got := s.SizeOf("a"); got != 3 {
		t.Errorf("SizeOf(\"a\") = %d, want 3", got)
	}
	if got := s.SizeOf("d"); got != 2 {
		t.Errorf("SizeOf(\"d\") = %d, want 2", got)
	}

	// Merging the two sets makes everything connected.
	s.Union("c", "d")
	if !s.Connected("a", "e") {
		t.Error("everything should now be connected")
	}
	if s.Count() != 1 {
		t.Errorf("Count = %d, want 1", s.Count())
	}
	if got := s.SizeOf("a"); got != 5 {
		t.Errorf("SizeOf = %d, want 5", got)
	}
}

// TestMatchesNaiveGrouping: the oracle is the obvious O(n^2) approach of keeping a label per
// element and relabelling on every union.
func TestMatchesNaiveGrouping(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))

	for range 2000 {
		const n = 20

		s := New[int]()
		labels := make([]int, n)
		for i := range labels {
			labels[i] = i
			s.Add(i)
		}

		for range r.IntN(30) {
			a, b := r.IntN(n), r.IntN(n)

			wasConnected := labels[a] == labels[b]
			merged := s.Union(a, b)

			if merged == wasConnected {
				t.Fatalf("Union(%d, %d) returned %v but they were connected = %v",
					a, b, merged, wasConnected)
			}

			// Relabel, which is the naive merge.
			if !wasConnected {
				old, replacement := labels[b], labels[a]
				for i := range labels {
					if labels[i] == old {
						labels[i] = replacement
					}
				}
			}

			// Every pair must agree.
			for i := range n {
				for j := range n {
					if s.Connected(i, j) != (labels[i] == labels[j]) {
						t.Fatalf("Connected(%d, %d) = %v, naive says %v",
							i, j, s.Connected(i, j), labels[i] == labels[j])
					}
				}
			}
		}

		// The set count must match the number of distinct labels.
		distinct := map[int]bool{}
		for _, l := range labels {
			distinct[l] = true
		}
		if s.Count() != len(distinct) {
			t.Fatalf("Count = %d, naive says %d", s.Count(), len(distinct))
		}
	}
}

// TestPathCompressionFlattensTheTree measures the optimisation rather than asserting it. A
// chain of a thousand unions, then one Find per element: with compression the second pass
// costs almost nothing.
func TestPathCompressionFlattensTheTree(t *testing.T) {
	const n = 1000

	s := New[int]()
	for i := 1; i < n; i++ {
		s.Union(i-1, i)
	}

	afterUnions := s.FindSteps()

	// First pass: whatever depth the unions left.
	for i := range n {
		s.Find(i)
	}
	firstPass := s.FindSteps() - afterUnions

	// Second pass: everything should now be one step from the root, or already at it.
	for i := range n {
		s.Find(i)
	}
	secondPass := s.FindSteps() - afterUnions - firstPass

	t.Logf("chain of %d: %d steps during unions, %d on the first pass, %d on the second",
		n, afterUnions, firstPass, secondPass)

	if secondPass > n {
		t.Errorf("the second pass took %d steps for %d elements; compression is not working",
			secondPass, n)
	}
	if secondPass > firstPass {
		t.Errorf("the second pass (%d) cost more than the first (%d)", secondPass, firstPass)
	}
}

// TestUnionBySizeKeepsTreesShallow: union by size is the half that path compression cannot
// replace. Unioning in a deliberately adversarial order should still leave shallow trees.
func TestUnionBySizeKeepsTreesShallow(t *testing.T) {
	const n = 4096

	s := New[int]()
	for i := range n {
		s.Add(i)
	}

	// Merge in pairs, then pairs of pairs, which is the order that builds a balanced
	// tree with union by size and a chain without it.
	for step := 1; step < n; step *= 2 {
		for i := 0; i+step < n; i += 2 * step {
			s.Union(i, i+step)
		}
	}

	if s.Count() != 1 {
		t.Fatalf("Count = %d, want 1", s.Count())
	}

	before := s.FindSteps()
	for i := range n {
		s.Find(i)
	}
	steps := s.FindSteps() - before

	// A balanced tree of 4096 elements is at most 12 deep, and union by size plus the
	// compression from earlier Finds should keep this far below n*log2(n).
	limit := n * 12
	if steps > limit {
		t.Errorf("%d steps to Find every element, want well under %d", steps, limit)
	}
	t.Logf("%d elements, %d steps to Find all of them (%.2f per element)",
		n, steps, float64(steps)/float64(n))
}

func TestGroups(t *testing.T) {
	s := New[string]()
	s.Union("b", "a")
	s.Union("d", "c")
	s.Add("e")

	got := s.Groups(cmp.Compare[string])

	want := [][]string{{"a", "b"}, {"c", "d"}, {"e"}}
	if len(got) != len(want) {
		t.Fatalf("got %d groups, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if !slices.Equal(got[i], want[i]) {
			t.Errorf("group %d = %v, want %v", i, got[i], want[i])
		}
	}

	// Repeated calls give the same answer, which a map-ordered implementation would not.
	for range 50 {
		if again := s.Groups(cmp.Compare[string]); !slices.EqualFunc(again, got, slices.Equal) {
			t.Fatalf("two calls disagree: %v and %v", got, again)
		}
	}
}

func TestCountComponents(t *testing.T) {
	tests := []struct {
		name  string
		nodes []int
		edges []Edge[int]
		want  int
	}{
		{
			name:  "two components",
			nodes: []int{0, 1, 2, 3, 4},
			edges: []Edge[int]{{From: 0, To: 1}, {From: 1, To: 2}, {From: 3, To: 4}},
			want:  2,
		},
		{
			name:  "an isolated node counts",
			nodes: []int{0, 1, 2},
			edges: []Edge[int]{{From: 0, To: 1}},
			want:  2,
		},
		{
			name:  "no edges",
			nodes: []int{0, 1, 2},
			want:  3,
		},
		{name: "nothing", want: 0},
		{
			name:  "one big component",
			nodes: []int{0, 1, 2, 3},
			edges: []Edge[int]{{From: 0, To: 1}, {From: 1, To: 2}, {From: 2, To: 3}},
			want:  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CountComponents(tt.nodes, tt.edges); got != tt.want {
				t.Errorf("CountComponents = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestHasCycle(t *testing.T) {
	tests := []struct {
		name  string
		edges []Edge[string]
		want  bool
	}{
		{"a tree", []Edge[string]{{From: "a", To: "b"}, {From: "b", To: "c"}}, false},
		{"a triangle", []Edge[string]{
			{From: "a", To: "b"}, {From: "b", To: "c"}, {From: "c", To: "a"},
		}, true},
		{"a self loop", []Edge[string]{{From: "a", To: "a"}}, true},
		{"a repeated edge", []Edge[string]{{From: "a", To: "b"}, {From: "a", To: "b"}}, true},
		{"two separate trees", []Edge[string]{{From: "a", To: "b"}, {From: "c", To: "d"}}, false},
		{"a cycle in the second component", []Edge[string]{
			{From: "a", To: "b"},
			{From: "c", To: "d"}, {From: "d", To: "e"}, {From: "e", To: "c"},
		}, true},
		{"nothing", nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			edge, got := HasCycle(tt.edges)

			if got != tt.want {
				t.Errorf("HasCycle = %v, want %v", got, tt.want)
			}
			if got && !slices.Contains(tt.edges, edge) {
				t.Errorf("HasCycle returned %v, which is not one of the edges", edge)
			}
		})
	}
}

// TestHasCycleMatchesEdgeCounting: for an undirected graph, a cycle exists exactly when the
// edge count reaches the node count minus the component count.
func TestHasCycleMatchesEdgeCounting(t *testing.T) {
	r := rand.New(rand.NewPCG(3, 4))

	for range 3000 {
		const n = 8

		var edges []Edge[int]
		seen := map[[2]int]bool{}

		for range r.IntN(10) {
			a, b := r.IntN(n), r.IntN(n)

			// Skip duplicates and self-loops, so the oracle below is valid.
			if a == b {
				continue
			}
			key := [2]int{min(a, b), max(a, b)}
			if seen[key] {
				continue
			}
			seen[key] = true

			edges = append(edges, Edge[int]{From: a, To: b})
		}

		nodes := make([]int, n)
		for i := range nodes {
			nodes[i] = i
		}

		components := CountComponents(nodes, edges)

		// A forest of c components over n nodes has exactly n-c edges. More means a cycle.
		want := len(edges) > n-components

		if _, got := HasCycle(edges); got != want {
			t.Fatalf("edges=%v: HasCycle = %v, edge counting says %v", edges, got, want)
		}
	}
}

func TestMinimumSpanningTree(t *testing.T) {
	// The standard example.
	nodes := []string{"A", "B", "C", "D", "E", "F", "G"}
	edges := []Edge[string]{
		{From: "A", To: "B", Weight: 7},
		{From: "A", To: "D", Weight: 5},
		{From: "B", To: "C", Weight: 8},
		{From: "B", To: "D", Weight: 9},
		{From: "B", To: "E", Weight: 7},
		{From: "C", To: "E", Weight: 5},
		{From: "D", To: "E", Weight: 15},
		{From: "D", To: "F", Weight: 6},
		{From: "E", To: "F", Weight: 8},
		{From: "E", To: "G", Weight: 9},
		{From: "F", To: "G", Weight: 11},
	}

	kept, total := MinimumSpanningTree(nodes, edges)

	if total != 39 {
		t.Errorf("total = %d, want 39 (kept %v)", total, kept)
	}
	if len(kept) != len(nodes)-1 {
		t.Errorf("kept %d edges, want %d for a spanning tree", len(kept), len(nodes)-1)
	}

	// Everything must end up connected.
	s := New[string]()
	for _, e := range kept {
		s.Union(e.From, e.To)
	}
	if s.Count() != 1 {
		t.Errorf("the result has %d components, want 1", s.Count())
	}
}

func TestMinimumSpanningForest(t *testing.T) {
	// Two disconnected components: there is no spanning TREE, and a forest is the useful
	// answer.
	nodes := []int{0, 1, 2, 3, 4}
	edges := []Edge[int]{
		{From: 0, To: 1, Weight: 1},
		{From: 2, To: 3, Weight: 2},
	}

	kept, total := MinimumSpanningTree(nodes, edges)

	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}
	if len(kept) != 2 {
		t.Errorf("kept %d edges, want 2", len(kept))
	}
	// A caller wanting a single tree checks this.
	if len(kept) == len(nodes)-1 {
		t.Error("the graph is disconnected, so the result should not be a spanning tree")
	}
}

// TestMSTMatchesBruteForce enumerates every subset of edges and finds the cheapest spanning
// one, which is the definition of a minimum spanning tree.
func TestMSTMatchesBruteForce(t *testing.T) {
	r := rand.New(rand.NewPCG(5, 7))

	for range 1500 {
		n := 2 + r.IntN(5)

		nodes := make([]int, n)
		for i := range nodes {
			nodes[i] = i
		}

		var edges []Edge[int]
		seen := map[[2]int]bool{}
		for range r.IntN(10) {
			a, b := r.IntN(n), r.IntN(n)
			if a == b {
				continue
			}
			key := [2]int{min(a, b), max(a, b)}
			if seen[key] {
				continue
			}
			seen[key] = true
			edges = append(edges, Edge[int]{From: a, To: b, Weight: 1 + r.IntN(20)})
		}

		components := CountComponents(nodes, edges)
		wantEdges := n - components

		// Every subset of the right size that connects everything it can.
		best := math.MaxInt
		for mask := 0; mask < 1<<len(edges); mask++ {
			var subset []Edge[int]
			weight := 0
			for i, e := range edges {
				if mask&(1<<i) != 0 {
					subset = append(subset, e)
					weight += e.Weight
				}
			}
			if len(subset) != wantEdges {
				continue
			}
			if CountComponents(nodes, subset) != components {
				continue // does not connect as much as the full edge set does
			}
			best = min(best, weight)
		}

		kept, total := MinimumSpanningTree(nodes, edges)

		if len(kept) != wantEdges {
			t.Fatalf("edges=%v: kept %d edges, want %d", edges, len(kept), wantEdges)
		}
		if wantEdges == 0 {
			continue
		}
		if total != best {
			t.Fatalf("edges=%v: MST weight %d, brute force says %d", edges, total, best)
		}
	}
}

func TestMergeAccounts(t *testing.T) {
	records := map[string][]string{
		"alice":   {"a@x.com", "alice@y.com"},
		"alicia":  {"alice@y.com"}, // shares with alice
		"bob":     {"bob@x.com"},
		"robert":  {"bob@x.com", "rob@z.com"}, // shares with bob
		"roberto": {"rob@z.com"},              // shares with robert, so also with bob
		"carol":   {"carol@x.com"},
	}

	got := MergeAccounts(records)

	want := [][]string{
		{"alice", "alicia"},
		{"bob", "robert", "roberto"},
		{"carol"},
	}

	if len(got) != len(want) {
		t.Fatalf("got %d groups, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if !slices.Equal(got[i], want[i]) {
			t.Errorf("group %d = %v, want %v", i, got[i], want[i])
		}
	}
}

// TestMergeAccountsIsTransitive is the property the naive pairwise approach gets wrong: A
// and C belong together even though they share nothing directly.
func TestMergeAccountsIsTransitive(t *testing.T) {
	records := map[string][]string{
		"a": {"1"},
		"b": {"1", "2"},
		"c": {"2"},
	}

	got := MergeAccounts(records)

	if len(got) != 1 {
		t.Fatalf("got %d groups, want 1: %v", len(got), got)
	}
	if !slices.Equal(got[0], []string{"a", "b", "c"}) {
		t.Errorf("group = %v, want [a b c]", got[0])
	}
}

func TestMergeAccountsDegenerate(t *testing.T) {
	if got := MergeAccounts(nil); len(got) != 0 {
		t.Errorf("MergeAccounts(nil) = %v", got)
	}

	// A record with no identifiers is its own group.
	got := MergeAccounts(map[string][]string{"lonely": nil})
	if len(got) != 1 || !slices.Equal(got[0], []string{"lonely"}) {
		t.Errorf("= %v, want [[lonely]]", got)
	}
}

// TestMergeAccountsIsDeterministic: the underlying map has no order, so without the sorting
// the output would differ between runs.
func TestMergeAccountsIsDeterministic(t *testing.T) {
	records := map[string][]string{}
	for i := range 30 {
		name := string(rune('a' + i%26))
		records[name+string(rune('0'+i/26))] = []string{string(rune('A' + i%7))}
	}

	first := MergeAccounts(records)

	for range 100 {
		if again := MergeAccounts(records); !slices.EqualFunc(again, first, slices.Equal) {
			t.Fatalf("two runs disagree:\n%v\n%v", first, again)
		}
	}
}
