package redblack

import (
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/alexvervloet/learn-go/dsa/bst"
)

// The comparison the package exists for: the same keys, in the same order, into a
// balanced tree and an unbalanced one. bst is imported from the test only, so the
// two packages stay independent.

const benchN = 100_000

func shuffled(n int) []int { return rand.New(rand.NewPCG(42, 42)).Perm(n) }

func BenchmarkGetSortedInput(b *testing.B) {
	b.Run("redblack", func(b *testing.B) {
		tr := New[int, int]()
		for i := range benchN {
			tr.Put(i, i)
		}
		b.Logf("height %d", tr.Height())
		b.ResetTimer()
		for i := 0; b.Loop(); i++ {
			sinkBool = tr.Contains(i % benchN)
		}
	})

	// Same keys, same order, unbalanced. Smaller n: at 100,000 a lookup in a
	// chain takes long enough that this benchmark would run for minutes.
	b.Run("bst", func(b *testing.B) {
		const n = 10_000
		tr := bst.New[int, int]()
		for i := range n {
			tr.Put(i, i)
		}
		b.Logf("height %d at n=%d", tr.Height(), n)
		b.ResetTimer()
		for i := 0; b.Loop(); i++ {
			sinkBool = tr.Contains(i % n)
		}
	})
}

func BenchmarkGetShuffledInput(b *testing.B) {
	keys := shuffled(benchN)

	b.Run("redblack", func(b *testing.B) {
		tr := New[int, int]()
		for _, k := range keys {
			tr.Put(k, k)
		}
		b.Logf("height %d", tr.Height())
		b.ResetTimer()
		for i := 0; b.Loop(); i++ {
			sinkBool = tr.Contains(keys[i%len(keys)])
		}
	})

	b.Run("bst", func(b *testing.B) {
		tr := bst.New[int, int]()
		for _, k := range keys {
			tr.Put(k, k)
		}
		b.Logf("height %d", tr.Height())
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

// What the balancing costs on the way in. Every Put does the rotations and the
// colour flips, plus this implementation's extra Contains lookup to decide
// whether the key is new.
func BenchmarkPut(b *testing.B) {
	keys := shuffled(10_000)

	b.Run("redblack shuffled", func(b *testing.B) {
		for b.Loop() {
			tr := New[int, int]()
			for _, k := range keys {
				tr.Put(k, k)
			}
			sinkInt = tr.Len()
		}
	})

	b.Run("bst shuffled", func(b *testing.B) {
		for b.Loop() {
			tr := bst.New[int, int]()
			for _, k := range keys {
				tr.Put(k, k)
			}
			sinkInt = tr.Len()
		}
	})

	b.Run("redblack sorted", func(b *testing.B) {
		for b.Loop() {
			tr := New[int, int]()
			for i := range 10_000 {
				tr.Put(i, i)
			}
			sinkInt = tr.Len()
		}
	})

	// The one that hurts: O(n^2), because every insert walks the whole chain.
	b.Run("bst sorted", func(b *testing.B) {
		for b.Loop() {
			tr := bst.New[int, int]()
			for i := range 10_000 {
				tr.Put(i, i)
			}
			sinkInt = tr.Len()
		}
	})

	b.Run("map shuffled", func(b *testing.B) {
		for b.Loop() {
			m := make(map[int]int)
			for _, k := range keys {
				m[k] = k
			}
			sinkInt = len(m)
		}
	})
}

func BenchmarkDelete(b *testing.B) {
	keys := shuffled(10_000)

	b.Run("redblack", func(b *testing.B) {
		for b.Loop() {
			tr := New[int, int]()
			for _, k := range keys {
				tr.Put(k, k)
			}
			for _, k := range keys {
				tr.Delete(k)
			}
			sinkInt = tr.Len()
		}
	})

	b.Run("bst", func(b *testing.B) {
		for b.Loop() {
			tr := bst.New[int, int]()
			for _, k := range keys {
				tr.Put(k, k)
			}
			for _, k := range keys {
				tr.Delete(k)
			}
			sinkInt = tr.Len()
		}
	})
}

func BenchmarkSortedIteration(b *testing.B) {
	keys := shuffled(benchN)

	b.Run("redblack", func(b *testing.B) {
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
}

var (
	sinkBool bool
	sinkInt  int
)
