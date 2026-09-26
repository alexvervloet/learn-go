package bst

import (
	"cmp"
	"iter"
	"math/rand/v2"
	"slices"
	"testing"
)

const benchN = 100_000

func shuffledKeys(n int) []int {
	return rand.New(rand.NewPCG(42, 42)).Perm(n)
}

// Lookup, four ways. The BST is here to lose to the hash map and to be beaten by
// a sorted slice, and to win the one thing neither of them can do.

func BenchmarkGet(b *testing.B) {
	keys := shuffledKeys(benchN)

	b.Run("bst shuffled", func(b *testing.B) {
		tr := New[int, int]()
		for _, k := range keys {
			tr.Put(k, k)
		}
		b.ResetTimer()
		for i := 0; b.Loop(); i++ {
			sinkBool = tr.Contains(keys[i%len(keys)])
		}
	})

	b.Run("map", func(b *testing.B) {
		m := make(map[int]int, benchN)
		for _, k := range keys {
			m[k] = k
		}
		b.ResetTimer()
		for i := 0; b.Loop(); i++ {
			_, sinkBool = m[keys[i%len(keys)]]
		}
	})

	b.Run("sorted slice", func(b *testing.B) {
		sorted := slices.Clone(keys)
		slices.Sort(sorted)
		b.ResetTimer()
		for i := 0; b.Loop(); i++ {
			_, sinkBool = slices.BinarySearch(sorted, keys[i%len(keys)])
		}
	})
}

// The cost of a degenerate tree, measured rather than asserted. Sorted insertion
// makes a chain, so a lookup is a linear walk.
//
// Smaller n than the rest, because building the chain is O(n^2) and a lookup in
// it is O(n): at 100,000 this benchmark alone would run for minutes.
func BenchmarkDegenerate(b *testing.B) {
	const n = 10_000

	b.Run("shuffled insert", func(b *testing.B) {
		tr := New[int, int]()
		for _, k := range rand.New(rand.NewPCG(1, 1)).Perm(n) {
			tr.Put(k, k)
		}
		b.Logf("height %d", tr.Height())
		b.ResetTimer()
		for i := 0; b.Loop(); i++ {
			sinkBool = tr.Contains(i % n)
		}
	})

	b.Run("sorted insert", func(b *testing.B) {
		tr := New[int, int]()
		for i := range n {
			tr.Put(i, i)
		}
		b.Logf("height %d", tr.Height())
		b.ResetTimer()
		for i := 0; b.Loop(); i++ {
			sinkBool = tr.Contains(i % n)
		}
	})
}

// The operation that justifies the structure: every key in sorted order.
func BenchmarkSortedIteration(b *testing.B) {
	keys := shuffledKeys(benchN)

	b.Run("bst", func(b *testing.B) {
		tr := New[int, int]()
		for _, k := range keys {
			tr.Put(k, k)
		}
		b.ResetTimer()
		for b.Loop() {
			n := 0
			for range tr.All() {
				n++
			}
			sinkInt = n
		}
	})

	b.Run("map collect and sort", func(b *testing.B) {
		m := make(map[int]int, benchN)
		for _, k := range keys {
			m[k] = k
		}
		b.ResetTimer()
		for b.Loop() {
			out := make([]int, 0, len(m))
			for k := range m {
				out = append(out, k)
			}
			slices.Sort(out)
			sinkInt = len(out)
		}
	})

	b.Run("sorted slice", func(b *testing.B) {
		sorted := slices.Clone(keys)
		slices.Sort(sorted)
		b.ResetTimer()
		for b.Loop() {
			n := 0
			for range sorted {
				n++
			}
			sinkInt = n
		}
	})
}

// A range query. The BST prunes subtrees; the slice binary-searches and walks.
// A hash map cannot do this without looking at every key.
func BenchmarkRange(b *testing.B) {
	keys := shuffledKeys(benchN)
	const lo, hi = 40_000, 40_100

	b.Run("bst", func(b *testing.B) {
		tr := New[int, int]()
		for _, k := range keys {
			tr.Put(k, k)
		}
		b.ResetTimer()
		for b.Loop() {
			n := 0
			for range tr.Range(lo, hi) {
				n++
			}
			sinkInt = n
		}
	})

	b.Run("sorted slice", func(b *testing.B) {
		sorted := slices.Clone(keys)
		slices.Sort(sorted)
		b.ResetTimer()
		for b.Loop() {
			i, _ := slices.BinarySearch(sorted, lo)
			n := 0
			for ; i < len(sorted) && sorted[i] <= hi; i++ {
				n++
			}
			sinkInt = n
		}
	})

	b.Run("map scan", func(b *testing.B) {
		m := make(map[int]int, benchN)
		for _, k := range keys {
			m[k] = k
		}
		b.ResetTimer()
		for b.Loop() {
			n := 0
			for k := range m {
				if k >= lo && k <= hi {
					n++
				}
			}
			sinkInt = n
		}
	})
}

// allWithStack is the version All used to be: an explicit stack instead of
// recursion. Kept here so the choice can be measured rather than argued about.
//
// It returns an iter.Seq2, exactly as All does, so the benchmark compares the
// traversal and nothing else. An earlier version of this took a plain
// `visit func(K, V)` and looked 3x faster, which was measuring the cost of
// range-over-func rather than the cost of the stack.
func allWithStack[K cmp.Ordered, V any](t *Tree[K, V]) iter.Seq2[K, V] {
	return func(yield func(K, V) bool) {
		stack := make([]*node[K, V], 0, 64)
		current := t.root

		for current != nil || len(stack) > 0 {
			for current != nil {
				stack = append(stack, current)
				current = current.left
			}

			last := len(stack) - 1
			current = stack[last]
			stack = stack[:last]

			if !yield(current.key, current.value) {
				return
			}

			current = current.right
		}
	}
}

// Recursive against an explicit stack, on a balanced tree and on a chain.
//
// The reason to expect the explicit stack to win is that a deep recursion forces
// the runtime to grow the goroutine stack, copying it each time. The reason it
// loses anyway is in README.md.
func BenchmarkTraversalStyle(b *testing.B) {
	const n = 50_000

	balanced := New[int, int]()
	for _, k := range rand.New(rand.NewPCG(9, 9)).Perm(n) {
		balanced.Put(k, k)
	}

	// Ascending keys give a chain of RIGHT children, which is the best case for
	// an explicit stack: it never holds more than one node.
	rightChain := New[int, int]()
	for i := range n {
		rightChain.Put(i, i)
	}

	// Descending keys give a chain of LEFT children, which is the worst case:
	// the stack has to hold all 50,000 nodes before the first key is yielded.
	leftChain := New[int, int]()
	for i := n - 1; i >= 0; i-- {
		leftChain.Put(i, i)
	}

	for _, tc := range []struct {
		name string
		tree *Tree[int, int]
	}{
		{"balanced", balanced},
		{"right chain", rightChain},
		{"left chain", leftChain},
	} {
		b.Run(tc.name+"/recursive", func(b *testing.B) {
			for b.Loop() {
				count := 0
				for range tc.tree.All() {
					count++
				}
				sinkInt = count
			}
		})

		b.Run(tc.name+"/explicit stack", func(b *testing.B) {
			for b.Loop() {
				count := 0
				for range allWithStack(tc.tree) {
					count++
				}
				sinkInt = count
			}
		})

		// And the same walk with a plain callback instead of an iterator, to
		// price range-over-func on its own.
		b.Run(tc.name+"/recursive callback", func(b *testing.B) {
			for b.Loop() {
				count := 0
				inOrder(tc.tree.root, func(int, int) bool { count++; return true })
				sinkInt = count
			}
		})
	}
}

var (
	sinkBool bool
	sinkInt  int
)
