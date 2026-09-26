package hashmap

import (
	"fmt"
	"testing"
)

// The comparison that matters: this table against the builtin map. The builtin
// should win, and by how much is the interesting part, because it says what the
// runtime buys with bucketed storage, top-byte tags, and hash functions written
// in assembly.

func benchKeys(n int) []string {
	keys := make([]string, n)
	for i := range keys {
		keys[i] = fmt.Sprintf("user_%08d", i)
	}
	return keys
}

const benchN = 10_000

func BenchmarkGet(b *testing.B) {
	keys := benchKeys(benchN)

	b.Run("hashmap", func(b *testing.B) {
		m := New[string, int](benchN)
		for i, k := range keys {
			m.Put(k, i)
		}

		b.ResetTimer()
		for i := 0; b.Loop(); i++ {
			m.Get(keys[i%benchN])
		}
	})

	b.Run("builtin", func(b *testing.B) {
		m := make(map[string]int, benchN)
		for i, k := range keys {
			m[k] = i
		}

		b.ResetTimer()
		for i := 0; b.Loop(); i++ {
			_ = m[keys[i%benchN]]
		}
	})
}

func BenchmarkGetMiss(b *testing.B) {
	keys := benchKeys(benchN)
	misses := make([]string, benchN)
	for i := range misses {
		misses[i] = fmt.Sprintf("absent_%08d", i)
	}

	b.Run("hashmap", func(b *testing.B) {
		m := New[string, int](benchN)
		for i, k := range keys {
			m.Put(k, i)
		}

		b.ResetTimer()
		for i := 0; b.Loop(); i++ {
			m.Get(misses[i%benchN])
		}
	})

	b.Run("builtin", func(b *testing.B) {
		m := make(map[string]int, benchN)
		for i, k := range keys {
			m[k] = i
		}

		b.ResetTimer()
		for i := 0; b.Loop(); i++ {
			_ = m[misses[i%benchN]]
		}
	})
}

// Building from empty, which is where the resize cost shows up.
func BenchmarkBuild(b *testing.B) {
	keys := benchKeys(benchN)

	b.Run("hashmap", func(b *testing.B) {
		for b.Loop() {
			m := New[string, int](0)
			for i, k := range keys {
				m.Put(k, i)
			}
		}
	})

	b.Run("hashmap presized", func(b *testing.B) {
		for b.Loop() {
			m := New[string, int](benchN)
			for i, k := range keys {
				m.Put(k, i)
			}
		}
	})

	b.Run("builtin", func(b *testing.B) {
		for b.Loop() {
			m := make(map[string]int)
			for i, k := range keys {
				m[k] = i
			}
		}
	})

	b.Run("builtin presized", func(b *testing.B) {
		for b.Loop() {
			m := make(map[string]int, benchN)
			for i, k := range keys {
				m[k] = i
			}
		}
	})
}

// What the three hash functions cost on their own, separated from the table.
func BenchmarkHash(b *testing.B) {
	const key = "user_00004242"

	b.Run("SumBytes", func(b *testing.B) {
		for b.Loop() {
			sink = SumBytes(key)
		}
	})

	b.Run("FNV1a", func(b *testing.B) {
		for b.Loop() {
			sink = FNV1a(key)
		}
	})

	b.Run("runtime", func(b *testing.B) {
		m := New[string, int](8)
		for b.Loop() {
			sink = m.hashOf(key)
		}
	})
}

var sink uint64
