package trie

import (
	"math/rand/v2"
	"strings"
	"testing"
)

// The measurement the pattern turns on: walking the grid and the trie together, against
// running a single-word search once per dictionary word.
// Two configurations, because the first is where the trie loses and the second is where it
// wins. A benchmark showing only one of them would be an argument rather than a measurement.
func BenchmarkWordSearch(b *testing.B) {
	r := rand.New(rand.NewPCG(1, 2))

	configs := []struct {
		name                           string
		size, alphabet, minLen, maxLen int
		words                          []int
	}{
		{"small grid, short words", 8, 4, 3, 6, []int{100, 1000}},
		{"large grid, long words", 12, 6, 6, 10, []int{1000, 5000}},
	}

	for _, cfg := range configs {
		for _, words := range cfg.words {
			g := make([][]rune, cfg.size)
			for i := range g {
				g[i] = make([]rune, cfg.size)
				for j := range g[i] {
					g[i][j] = rune('a' + r.IntN(cfg.alphabet))
				}
			}

			dict := make([]string, words)
			for i := range dict {
				n := cfg.minLen + r.IntN(cfg.maxLen-cfg.minLen+1)
				bs := make([]byte, n)
				for j := range bs {
					bs[j] = byte('a' + r.IntN(cfg.alphabet))
				}
				dict[i] = string(bs)
			}

			label := cfg.name + "/" + itoa(words) + " words"

			b.Run(label+"/trie, node-carrying", func(b *testing.B) {
				for b.Loop() {
					sinkStrings = WordSearchII(g, dict)
				}
			})

			b.Run(label+"/trie, prefix-carrying", func(b *testing.B) {
				for b.Loop() {
					sinkStrings = WordSearchIIByPrefix(g, dict)
				}
			})

			b.Run(label+"/one search per word", func(b *testing.B) {
				for b.Loop() {
					var found []string
					for _, w := range dict {
						if singleWordSearch(g, w) {
							found = append(found, w)
						}
					}
					sinkStrings = found
				}
			})
		}
	}
}

// singleWordSearch is patterns/backtracking's WordSearch, copied here so the benchmark does
// not depend on that package and so the comparison is exactly one traversal per word.
func singleWordSearch(g [][]rune, word string) bool {
	target := []rune(word)
	if len(target) == 0 {
		return true
	}

	var walk func(row, col, at int) bool
	walk = func(row, col, at int) bool {
		if row < 0 || row >= len(g) || col < 0 || col >= len(g[row]) {
			return false
		}
		if g[row][col] != target[at] {
			return false
		}
		if at == len(target)-1 {
			return true
		}

		was := g[row][col]
		g[row][col] = 0

		found := walk(row-1, col, at+1) || walk(row+1, col, at+1) ||
			walk(row, col-1, at+1) || walk(row, col+1, at+1)

		g[row][col] = was
		return found
	}

	for row := range g {
		for col := range g[row] {
			if walk(row, col, 0) {
				return true
			}
		}
	}
	return false
}

// The bit trie against checking every pair.
func BenchmarkMaxXORPair(b *testing.B) {
	r := rand.New(rand.NewPCG(3, 4))

	for _, n := range []int{100, 1000, 10_000} {
		values := make([]int, n)
		for i := range values {
			values[i] = r.IntN(1 << 30)
		}

		b.Run(itoa(n)+"/bit trie", func(b *testing.B) {
			for b.Loop() {
				sinkInt, _ = MaxXORPair(values)
			}
		})

		// O(n^2), so the largest size is skipped.
		if n <= 1000 {
			b.Run(itoa(n)+"/every pair", func(b *testing.B) {
				for b.Loop() {
					best := 0
					for i := range values {
						for j := i + 1; j < n; j++ {
							best = max(best, values[i]^values[j])
						}
					}
					sinkInt = best
				}
			})
		}
	}
}

// ReplaceWords against checking every root, as the dictionary grows.
func BenchmarkReplaceWords(b *testing.B) {
	r := rand.New(rand.NewPCG(5, 7))

	words := make([]string, 200)
	for i := range words {
		bs := make([]byte, 4+r.IntN(6))
		for j := range bs {
			bs[j] = byte('a' + r.IntN(6))
		}
		words[i] = string(bs)
	}
	sentence := strings.Join(words, " ")

	for _, n := range []int{10, 1000, 10_000} {
		roots := make([]string, n)
		for i := range roots {
			bs := make([]byte, 2+r.IntN(3))
			for j := range bs {
				bs[j] = byte('a' + r.IntN(6))
			}
			roots[i] = string(bs)
		}

		b.Run(itoa(n)+" roots/trie", func(b *testing.B) {
			for b.Loop() {
				sinkString = ReplaceWords(sentence, roots)
			}
		})

		b.Run(itoa(n)+" roots/every root", func(b *testing.B) {
			for b.Loop() {
				fields := strings.Fields(sentence)
				for i, w := range fields {
					best := ""
					for _, root := range roots {
						if !strings.HasPrefix(w, root) {
							continue
						}
						if best == "" || len(root) < len(best) {
							best = root
						}
					}
					if best != "" {
						fields[i] = best
					}
				}
				sinkString = strings.Join(fields, " ")
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

var (
	sinkInt     int
	sinkString  string
	sinkStrings []string
)
