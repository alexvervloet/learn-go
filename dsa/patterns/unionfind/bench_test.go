package unionfind

import (
	"math/rand/v2"
	"testing"
)

// The four combinations of the two optimisations, written out so the claim that both are
// needed can be measured rather than asserted.
//
// Indices rather than a map, so the comparison is about the algorithm and not about hashing.
type plain struct {
	parent   []int
	size     []int
	compress bool
	bySize   bool
	steps    int
}

func newPlain(n int, compress, bySize bool) *plain {
	p := &plain{
		parent:   make([]int, n),
		size:     make([]int, n),
		compress: compress,
		bySize:   bySize,
	}
	for i := range p.parent {
		p.parent[i] = i
		p.size[i] = 1
	}
	return p
}

func (p *plain) find(x int) int {
	root := x
	for p.parent[root] != root {
		root = p.parent[root]
		p.steps++
	}

	if p.compress {
		for p.parent[x] != root {
			next := p.parent[x]
			p.parent[x] = root
			x = next
		}
	}

	return root
}

func (p *plain) union(x, y int) {
	rootX, rootY := p.find(x), p.find(y)
	if rootX == rootY {
		return
	}

	if p.bySize && p.size[rootX] < p.size[rootY] {
		rootX, rootY = rootY, rootX
	}

	p.parent[rootY] = rootX
	p.size[rootX] += p.size[rootY]
}

// Three workloads, because the obvious one distinguishes nothing.
//
// chainForwards is what I reached for first, and all four variants score identically on it:
// union(i-1, i) always makes the NEW element the smaller tree, so it attaches under the
// existing root whether or not union-by-size is on, and the result is a star either way.
// A benchmark that cannot tell the variants apart is worse than no benchmark.
func chainForwards(p *plain, n int) {
	for i := 1; i < n; i++ {
		p.union(i-1, i)
	}
	for i := range n {
		p.find(i)
	}
}

// chainBackwards is the adversarial order: union(i, i-1) hangs the BIG tree under the new
// single node when union-by-size is off, which builds a chain of length n.
func chainBackwards(p *plain, n int) {
	for i := 1; i < n; i++ {
		p.union(i, i-1)
	}
	for i := range n {
		p.find(i)
	}
}

// interleaved is the realistic one, and the only one that separates all four: random unions
// and random finds mixed together, which is how the structure is actually used.
func interleaved(p *plain, n int) {
	r := rand.New(rand.NewPCG(1, 2))

	for range n * 3 {
		if r.IntN(2) == 0 {
			p.union(r.IntN(n), r.IntN(n))
			continue
		}
		p.find(r.IntN(n))
	}
}

var variants = []struct {
	name             string
	compress, bySize bool
}{
	{"both", true, true},
	{"compression only", true, false},
	{"union by size only", false, true},
	{"neither", false, false},
}

func BenchmarkOptimisations(b *testing.B) {
	const n = 20_000

	for _, v := range variants {
		b.Run("interleaved/"+v.name, func(b *testing.B) {
			for b.Loop() {
				p := newPlain(n, v.compress, v.bySize)
				interleaved(p, n)
				sinkInt = p.steps
			}
		})
	}

	for _, v := range variants {
		b.Run("adversarial chain/"+v.name, func(b *testing.B) {
			for b.Loop() {
				p := newPlain(n, v.compress, v.bySize)
				chainBackwards(p, n)
				sinkInt = p.steps
			}
		})
	}
}

// The same four, counting pointer hops rather than nanoseconds. Exact, and it is the number
// that explains the timings.
func TestOptimisationStepCounts(t *testing.T) {
	const n = 20_000

	shapes := []struct {
		name string
		run  func(*plain, int)
	}{
		{"chain, forwards (distinguishes nothing)", chainForwards},
		{"chain, backwards (adversarial)", chainBackwards},
		{"interleaved random (realistic)", interleaved},
	}

	for _, shape := range shapes {
		t.Logf("--- %s", shape.name)

		hops := make(map[string]int, len(variants))

		for _, v := range variants {
			p := newPlain(n, v.compress, v.bySize)
			shape.run(p, n)
			hops[v.name] = p.steps

			t.Logf("  %-19s %12d hops (%8.2f per element)",
				v.name, p.steps, float64(p.steps)/float64(n))
		}

		// Both optimisations must always be the best, or at worst tied.
		for _, v := range variants[1:] {
			if hops["both"] > hops[v.name] {
				t.Errorf("%s: both (%d hops) lost to %s (%d hops)",
					shape.name, hops["both"], v.name, hops[v.name])
			}
		}
	}

	// On the realistic workload, every single-optimisation variant must be measurably
	// worse, and the unoptimised one dramatically so.
	both := newPlain(n, true, true)
	interleaved(both, n)

	neither := newPlain(n, false, false)
	interleaved(neither, n)

	if neither.steps < 100*both.steps {
		t.Errorf("unoptimised took %d hops against %d; expected at least 100x",
			neither.steps, both.steps)
	}
}

// The real Sets type against the fastest plain variant, to price the map.
func BenchmarkMapOverhead(b *testing.B) {
	const n = 20_000

	b.Run("Sets, map-backed", func(b *testing.B) {
		for b.Loop() {
			s := New[int]()
			for i := 1; i < n; i++ {
				s.Union(i-1, i)
			}
			for i := range n {
				s.Find(i)
			}
			sinkInt = s.Count()
		}
	})

	b.Run("plain, slice-backed", func(b *testing.B) {
		for b.Loop() {
			p := newPlain(n, true, true)
			chainForwards(p, n)
			sinkInt = p.steps
		}
	})
}

func BenchmarkMinimumSpanningTree(b *testing.B) {
	r := rand.New(rand.NewPCG(1, 2))

	for _, n := range []int{100, 1000, 10_000} {
		nodes := make([]int, n)
		for i := range nodes {
			nodes[i] = i
		}

		edges := make([]Edge[int], 0, n*4)
		for i := 1; i < n; i++ {
			edges = append(edges, Edge[int]{From: r.IntN(i), To: i, Weight: r.IntN(1000)})
		}
		for range n * 3 {
			a, b := r.IntN(n), r.IntN(n)
			if a != b {
				edges = append(edges, Edge[int]{From: a, To: b, Weight: r.IntN(1000)})
			}
		}

		b.Run(itoa(n)+" nodes", func(b *testing.B) {
			for b.Loop() {
				_, total := MinimumSpanningTree(nodes, edges)
				sinkInt = total
			}
		})
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

var sinkInt int
