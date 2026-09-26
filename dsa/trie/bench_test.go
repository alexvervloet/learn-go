package trie

import (
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
)

// words builds a deterministic, dictionary-shaped list: a few thousand strings
// over a 26-letter alphabet with heavy prefix sharing, which is what decides
// whether a trie is worth its memory.
//
// Generated rather than read from /usr/share/dict/words, because that file does
// not exist on Windows and the CI matrix includes it.
func words(n int) []string {
	r := rand.New(rand.NewPCG(1, 2)) // fixed seed: the same list every run

	// A small stem set forces prefix sharing. A random 8-letter string shares
	// almost nothing with any other, which would flatter the map and make the
	// trie look pointless.
	stems := []string{
		"go", "gopher", "golang", "gone", "good", "pre", "pres", "press",
		"pro", "prod", "produce", "un", "under", "understand", "re", "read",
		"ready", "st", "str", "string", "strong", "co", "con", "cont", "test",
	}

	seen := make(map[string]bool, n)
	out := make([]string, 0, n)

	for len(out) < n {
		var sb strings.Builder
		sb.WriteString(stems[r.IntN(len(stems))])

		for range 1 + r.IntN(5) {
			sb.WriteByte(byte('a' + r.IntN(26)))
		}

		w := sb.String()
		if seen[w] {
			continue
		}
		seen[w] = true
		out = append(out, w)
	}

	return out
}

const corpusSize = 20_000

// A hash map wins a lookup and loses a prefix query, which is the whole reason
// to have both.

func BenchmarkContains(b *testing.B) {
	corpus := words(corpusSize)

	b.Run("trie", func(b *testing.B) {
		tr := build(corpus...)
		b.ResetTimer()
		for i := 0; b.Loop(); i++ {
			sinkBool = tr.Contains(corpus[i%len(corpus)])
		}
	})

	b.Run("map", func(b *testing.B) {
		m := make(map[string]bool, len(corpus))
		for _, w := range corpus {
			m[w] = true
		}
		b.ResetTimer()
		for i := 0; b.Loop(); i++ {
			sinkBool = m[corpus[i%len(corpus)]]
		}
	})
}

// The prefix query. The trie walks three nodes and collects a subtree; the map
// has to look at all twenty thousand keys.
func BenchmarkComplete(b *testing.B) {
	corpus := words(corpusSize)

	b.Run("trie", func(b *testing.B) {
		tr := build(corpus...)
		b.ResetTimer()
		for b.Loop() {
			sinkStrings = tr.Complete("pre")
		}
	})

	b.Run("map scan", func(b *testing.B) {
		m := make(map[string]bool, len(corpus))
		for _, w := range corpus {
			m[w] = true
		}
		b.ResetTimer()
		for b.Loop() {
			var out []string
			for w := range m {
				if strings.HasPrefix(w, "pre") {
					out = append(out, w)
				}
			}
			slices.Sort(out)
			sinkStrings = out
		}
	})

	b.Run("sorted slice", func(b *testing.B) {
		sorted := slices.Clone(corpus)
		slices.Sort(sorted)
		b.ResetTimer()
		for b.Loop() {
			// Binary search to the first match, then walk while the prefix
			// holds. This is what a trie competes against in practice, and it
			// is better than most people expect.
			i, _ := slices.BinarySearch(sorted, "pre")
			var out []string
			for ; i < len(sorted) && strings.HasPrefix(sorted[i], "pre"); i++ {
				out = append(out, sorted[i])
			}
			sinkStrings = out
		}
	})
}

// Just the walk, with no result collected, which isolates the lookup from the
// cost of building a slice of strings.
func BenchmarkCount(b *testing.B) {
	corpus := words(corpusSize)

	b.Run("trie", func(b *testing.B) {
		tr := build(corpus...)
		b.ResetTimer()
		for b.Loop() {
			sinkInt = tr.Count("pre")
		}
	})

	b.Run("map scan", func(b *testing.B) {
		m := make(map[string]bool, len(corpus))
		for _, w := range corpus {
			m[w] = true
		}
		b.ResetTimer()
		for b.Loop() {
			n := 0
			for w := range m {
				if strings.HasPrefix(w, "pre") {
					n++
				}
			}
			sinkInt = n
		}
	})
}

func BenchmarkBuild(b *testing.B) {
	corpus := words(corpusSize)

	b.Run("trie", func(b *testing.B) {
		for b.Loop() {
			sinkInt = build(corpus...).Len()
		}
	})

	b.Run("map", func(b *testing.B) {
		for b.Loop() {
			m := make(map[string]bool, len(corpus))
			for _, w := range corpus {
				m[w] = true
			}
			sinkInt = len(m)
		}
	})
}

// Node layout
// ===========
//
// map[rune]*node handles any input and costs a map header plus a hash per level.
// [26]*node is two words per slot with no hashing at all, and only works for
// lowercase ASCII. The question is what that trade is actually worth.

type arrayNode struct {
	children [26]*arrayNode
	terminal bool
}

type arrayTrie struct {
	root arrayNode
}

func (t *arrayTrie) Insert(word string) {
	current := &t.root
	for i := range len(word) {
		idx := word[i] - 'a'
		if current.children[idx] == nil {
			current.children[idx] = &arrayNode{}
		}
		current = current.children[idx]
	}
	current.terminal = true
}

func (t *arrayTrie) Contains(word string) bool {
	current := &t.root
	for i := range len(word) {
		current = current.children[word[i]-'a']
		if current == nil {
			return false
		}
	}
	return current.terminal
}

func BenchmarkNodeLayout(b *testing.B) {
	corpus := words(corpusSize)

	b.Run("map children/build", func(b *testing.B) {
		for b.Loop() {
			sinkInt = build(corpus...).Len()
		}
	})

	b.Run("array children/build", func(b *testing.B) {
		for b.Loop() {
			t := &arrayTrie{}
			for _, w := range corpus {
				t.Insert(w)
			}
			sinkAny = t
		}
	})

	b.Run("map children/lookup", func(b *testing.B) {
		tr := build(corpus...)
		b.ResetTimer()
		for i := 0; b.Loop(); i++ {
			sinkBool = tr.Contains(corpus[i%len(corpus)])
		}
	})

	b.Run("array children/lookup", func(b *testing.B) {
		t := &arrayTrie{}
		for _, w := range corpus {
			t.Insert(w)
		}
		b.ResetTimer()
		for i := 0; b.Loop(); i++ {
			sinkBool = t.Contains(corpus[i%len(corpus)])
		}
	})
}

func TestCorpusSharesPrefixes(t *testing.T) {
	corpus := words(corpusSize)

	tr := build(corpus...)
	if got := tr.Count("pre"); got < 100 {
		t.Errorf("the generated corpus has only %d words under \"pre\"; the benchmarks need a real subtree", got)
	}

	t.Logf("%d words, %d trie nodes, %d under \"pre\"",
		len(corpus), countNodes(tr.root), tr.Count("pre"))
}

var (
	sinkBool    bool
	sinkInt     int
	sinkStrings []string
	sinkAny     any
)
