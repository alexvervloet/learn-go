// Package trie collects the problems the trie pattern is for.
//
// The data structure itself lives in dsa/trie, with the insert, delete and prefix
// operations and the measurements arguing about when it is worth using. This package is
// about RECOGNISING when a problem wants one, which is a different skill.
//
// # The tell
//
// "Prefix", "autocomplete", "dictionary of words", "starts with", "wildcard search". The
// underlying signal is that many strings SHARE prefixes and the question is about those
// shared prefixes rather than about whole strings.
//
// If the question is only "is this exact string in the set", it is a hash map. dsa/trie has
// the numbers: a map is 5.5x faster at that.
//
// # The three shapes
//
// The problems below fall into three groups, and telling them apart is most of the work.
//
//	WALK ONE STRING DOWN THE TRIE
//	  ReplaceWords, LongestCommonPrefix. One pass, O(len(input)). The trie is a lookup
//	  table for "the shortest known prefix of this".
//
//	COLLECT A SUBTREE
//	  Autocomplete. Walk to the prefix, then gather everything below. The cost is the
//	  size of the ANSWER, not the size of the dictionary, which is the whole point.
//
//	EXPLORE THE TRIE AND SOMETHING ELSE TOGETHER
//	  WordSearchII, and it is the one worth learning. Walking a grid and a trie in
//	  lockstep turns "find all 10,000 of these words" into one traversal, because a grid
//	  path that is not a prefix of any word is abandoned immediately.
//
// # The fourth shape: bits, not letters
//
// MaxXORPair uses a trie over the BITS of an integer rather than the characters of a string.
// That generalisation is the least obvious and the most useful: any sequence with a
// branching factor small enough to enumerate can go in a trie, and greedy bit-by-bit choices
// become a walk down one.
package trie

import (
	"cmp"
	"slices"
	"strings"

	dsatrie "github.com/alexvervloet/learn-go/dsa/trie"
)

// Shape one: walk one string down the trie
// ========================================

// ReplaceWords replaces every word in a sentence with the shortest dictionary root that is a
// prefix of it, leaving words with no matching root alone.
//
// "The cattle was rattled by the battery" with roots {cat, bat, rat} becomes "the cat was rat
// by the bat".
//
// The naive version checks every root against every word, at O(words * roots * length). This
// walks each word down the trie once and stops at the first terminal, at O(total input
// length) regardless of how many roots there are. On a dictionary of ten thousand roots that
// is the difference between the two approaches.
func ReplaceWords(sentence string, roots []string) string {
	t := dsatrie.New()
	for _, r := range roots {
		if r != "" {
			t.Insert(r)
		}
	}

	words := strings.Fields(sentence)
	for i, w := range words {
		// LongestPrefixOf finds the longest; the problem wants the SHORTEST, so the
		// walk has to stop at the first terminal instead.
		if root, ok := shortestPrefix(t, w); ok {
			words[i] = root
		}
	}

	return strings.Join(words, " ")
}

// shortestPrefix returns the shortest word in the trie that is a prefix of s.
//
// dsa/trie offers LongestPrefixOf, which is what a router wants. This wants the opposite, and
// the difference is one word: stop at the first terminal instead of remembering the last.
// There is no way to build it from LongestPrefixOf, so it walks the trie through the public
// prefix operations instead, which costs an extra Contains call per character.
func shortestPrefix(t *dsatrie.Trie, s string) (string, bool) {
	runes := []rune(s)

	for i := 1; i <= len(runes); i++ {
		candidate := string(runes[:i])

		if t.Contains(candidate) {
			return candidate, true
		}
		if !t.HasPrefix(candidate) {
			break // no longer prefix can match either
		}
	}

	return "", false
}

// LongestCommonPrefix returns the longest prefix shared by every given word.
//
// Not a trie problem, and it is here to say so. Building a trie and walking down while each
// node has exactly one child and is not terminal works, and it costs O(total length) to
// build a structure that is then walked once and thrown away.
//
// Comparing the first word against each of the others character by character is O(total
// length) too, allocates nothing, and is four lines. Reaching for the fancy structure because
// the word "prefix" appears is the mistake this function exists to name.
func LongestCommonPrefix(words []string) string {
	if len(words) == 0 {
		return ""
	}

	prefix := []rune(words[0])

	for _, w := range words[1:] {
		runes := []rune(w)

		if len(runes) < len(prefix) {
			prefix = prefix[:len(runes)]
		}

		for i := range prefix {
			if runes[i] != prefix[i] {
				prefix = prefix[:i]
				break
			}
		}

		if len(prefix) == 0 {
			return ""
		}
	}

	return string(prefix)
}

// Shape two: collect a subtree
// ============================

// Suggestion is a word and how often it was seen.
type Suggestion struct {
	Word  string
	Count int
}

// Autocompleter ranks the words starting with a prefix by frequency, then alphabetically.
//
// The realistic version of the problem, and the ranking is what makes it realistic: a search
// box that returned matches in alphabetical order would be useless. The frequencies come from
// the caller, which is where they come from in production too.
//
// Cost is O(len(prefix)) to find the subtree plus O(matches) to collect and rank it, with
// nothing proportional to the size of the dictionary. That is the property worth having, and
// dsa/trie's README has the measurement showing a sorted slice beats it anyway for a STATIC
// dictionary. This wins when the counts change.
type Autocompleter struct {
	trie   *dsatrie.Trie
	counts map[string]int
}

// NewAutocompleter returns an empty autocompleter.
func NewAutocompleter() *Autocompleter {
	return &Autocompleter{trie: dsatrie.New(), counts: make(map[string]int)}
}

// Record adds one occurrence of word.
func (a *Autocompleter) Record(word string) {
	if word == "" {
		return
	}

	a.trie.Insert(word)
	a.counts[word]++
}

// RecordMany adds one occurrence of each word.
func (a *Autocompleter) RecordMany(words []string) {
	for _, w := range words {
		a.Record(w)
	}
}

// Len reports how many distinct words are known.
func (a *Autocompleter) Len() int { return a.trie.Len() }

// Suggest returns the highest-ranked completions of prefix.
//
// The iterator from dsa/trie is used rather than Complete, so the whole match list is never
// materialised in sorted order just to be re-sorted by frequency.
func (a *Autocompleter) Suggest(prefix string, limit int) []Suggestion {
	if limit <= 0 {
		return nil
	}

	var out []Suggestion
	for word := range a.trie.WordsWithPrefix(prefix) {
		out = append(out, Suggestion{Word: word, Count: a.counts[word]})
	}

	// Most frequent first, ties alphabetically. Without the tie-break the order depends
	// on the trie's map iteration and changes between runs.
	slices.SortFunc(out, func(x, y Suggestion) int {
		if x.Count != y.Count {
			return cmp.Compare(y.Count, x.Count)
		}
		return cmp.Compare(x.Word, y.Word)
	})

	return out[:min(limit, len(out))]
}

// Shape three: explore the trie and something else together
// =========================================================

// node is a minimal trie node, exposed within this package so a search can hold a POSITION
// in the trie rather than re-walking a prefix string.
//
// dsa/trie deliberately exposes no nodes: it is a container with a prefix API, and that is
// the right shape for a container. It is the wrong shape for this problem, and the benchmark
// says so in numbers. See WordSearchIIByPrefix.
type node struct {
	children map[rune]*node
	word     string // the complete word ending here, or ""
}

func newNode() *node { return &node{children: make(map[rune]*node)} }

func buildTrie(words []string) *node {
	root := newNode()

	for _, w := range words {
		if w == "" {
			continue
		}

		current := root
		for _, c := range w {
			child, ok := current.children[c]
			if !ok {
				child = newNode()
				current.children[c] = child
			}
			current = child
		}

		// Storing the whole word at the terminal rather than a bool means the search
		// never has to reconstruct it from the path.
		current.word = w
	}

	return root
}

// WordSearchII returns every word from the dictionary that can be spelled by walking the grid
// one cell at a time, horizontally or vertically, without reusing a cell.
//
// The problem the pattern is usually sold with, and the measurements are more modest than the
// sales pitch. Running a single-word search once per word is O(words * cells * 4^length), and
// walking the grid and the trie together is one traversal, so the dictionary size stops
// multiplying the work.
//
// In practice, measured against a per-word search:
//
//	                                    trie      per word   trie is
//	8x8,   4 letters, words 3-6,  100    56 us      49 us     0.86x  LOSES
//	8x8,   4 letters, words 3-6,  1000  510 us     315 us     0.62x  LOSES
//	12x12, 6 letters, words 6-10, 1000  635 us     950 us     1.50x
//	12x12, 6 letters, words 6-10, 5000  2.4 ms     4.8 ms     2.02x
//
// The reason the advantage is small: a per-word search also abandons a path on the first
// character that does not match, and it stops at the FIRST occurrence, while this has to
// explore every path. The trie wins on big grids with long words and large dictionaries, and
// by about 2x rather than the order of magnitude the pattern is usually sold with.
//
// The search carries a *node, so descending one cell is one map lookup. The obvious
// alternative, carrying the prefix STRING and asking the trie about it, re-walks the prefix
// from the root on every step and is 2.5x to 6.3x slower than this. That version is
// WordSearchIIByPrefix, kept for the comparison, and it loses to a per-word search at every
// size tried.
//
// Found words go in a set, because the same word can be spellable by several paths and the
// answer is a set of words rather than a list of paths.
func WordSearchII(grid [][]rune, dictionary []string) []string {
	if len(grid) == 0 || len(grid[0]) == 0 || len(dictionary) == 0 {
		return nil
	}

	root := buildTrie(dictionary)
	found := make(map[string]bool)

	const visited = rune(0)

	var explore func(row, col int, at *node)
	explore = func(row, col int, at *node) {
		if row < 0 || row >= len(grid) || col < 0 || col >= len(grid[row]) {
			return
		}

		c := grid[row][col]
		if c == visited {
			return
		}

		// The pruning that makes this work, in one map lookup: no word continues this
		// way, so stop.
		next, ok := at.children[c]
		if !ok {
			return
		}

		if next.word != "" {
			found[next.word] = true
		}

		grid[row][col] = visited

		explore(row-1, col, next)
		explore(row+1, col, next)
		explore(row, col-1, next)
		explore(row, col+1, next)

		grid[row][col] = c // undo, so other paths can use this cell
	}

	for row := range grid {
		for col := range grid[row] {
			explore(row, col, root)
		}
	}

	out := make([]string, 0, len(found))
	for w := range found {
		out = append(out, w)
	}
	slices.Sort(out)

	return out
}

// WordSearchIIByPrefix is the same algorithm carrying the prefix string instead of a node,
// using only dsa/trie's public API.
//
// It is here because it is what the pattern looks like when the trie is a container rather
// than a structure you can hold a position in, and because the cost of that is much larger
// than it looks: every step calls HasPrefix and Contains, each of which walks the prefix from
// the root again, so a path of length k costs O(k) map lookups per cell instead of one.
//
// It is 2.5x to 6.3x slower than the node-carrying version and slower than running a separate
// single-word search for every word, at every size tried. The
// abstraction costs more than the algorithm saves, which is the thing worth taking away: the
// pattern needs a trie you can hold a POSITION in, not a trie you can ask questions of.
func WordSearchIIByPrefix(grid [][]rune, dictionary []string) []string {
	if len(grid) == 0 || len(grid[0]) == 0 || len(dictionary) == 0 {
		return nil
	}

	t := dsatrie.New()
	for _, w := range dictionary {
		if w != "" {
			t.Insert(w)
		}
	}

	found := make(map[string]bool)
	const visited = rune(0)

	var explore func(row, col int, path []rune)
	explore = func(row, col int, path []rune) {
		if row < 0 || row >= len(grid) || col < 0 || col >= len(grid[row]) {
			return
		}

		c := grid[row][col]
		if c == visited {
			return
		}

		path = append(path, c)
		prefix := string(path)

		if !t.HasPrefix(prefix) {
			return
		}
		if t.Contains(prefix) {
			found[prefix] = true
		}

		grid[row][col] = visited

		explore(row-1, col, path)
		explore(row+1, col, path)
		explore(row, col-1, path)
		explore(row, col+1, path)

		grid[row][col] = c
	}

	for row := range grid {
		for col := range grid[row] {
			explore(row, col, nil)
		}
	}

	out := make([]string, 0, len(found))
	for w := range found {
		out = append(out, w)
	}
	slices.Sort(out)

	return out
}

// Shape four: a trie over bits
// ============================

// BitTrie stores non-negative integers by their binary digits, most significant first.
//
// The generalisation worth taking away: a trie does not need letters. Any sequence with a
// small branching factor works, and for integers the branching factor is two.
//
// What it buys is that a greedy bit-by-bit decision becomes a walk. "Find the value that
// maximises x XOR value" is answered by preferring the opposite bit at every level, which is
// O(bits) instead of O(n).
type BitTrie struct {
	children [2]*BitTrie
	count    int // values passing through, so Remove can prune
	bits     int
}

// NewBitTrie returns a trie for values of the given bit width. 32 covers every uint32 and is
// the usual choice; 64 covers every non-negative int.
func NewBitTrie(bits int) *BitTrie {
	if bits <= 0 || bits > 63 {
		bits = 32
	}
	return &BitTrie{bits: bits}
}

// Insert adds a value.
func (b *BitTrie) Insert(value int) {
	node := b
	node.count++

	for i := b.bits - 1; i >= 0; i-- {
		bit := (value >> i) & 1

		if node.children[bit] == nil {
			node.children[bit] = &BitTrie{bits: b.bits}
		}
		node = node.children[bit]
		node.count++
	}
}

// Len reports how many values are stored, counting duplicates.
func (b *BitTrie) Len() int { return b.count }

// MaxXORWith returns the largest value of x XOR v over the stored values v, and false if
// nothing is stored.
//
// The greedy walk: XOR is 1 exactly when the bits differ, and a higher bit outweighs every
// lower bit combined, so at each level take the opposite branch if it exists. O(bits),
// against O(n) for checking every stored value.
func (b *BitTrie) MaxXORWith(x int) (int, bool) {
	if b.count == 0 {
		return 0, false
	}

	node := b
	best := 0

	for i := b.bits - 1; i >= 0; i-- {
		bit := (x >> i) & 1
		want := 1 - bit // the opposite bit makes this position of the XOR a 1

		if node.children[want] != nil && node.children[want].count > 0 {
			best |= 1 << i
			node = node.children[want]
			continue
		}

		node = node.children[bit]
		if node == nil {
			return 0, false // cannot happen while count > 0, and cheap to rule out
		}
	}

	return best, true
}

// MaxXORPair returns the largest XOR of any two values in the slice.
//
// O(n * bits) with the trie, against O(n^2) for every pair. At n = 200,000 that is the
// difference between 6 million operations and 40 billion.
func MaxXORPair(values []int) (int, bool) {
	if len(values) < 2 {
		return 0, false
	}

	t := NewBitTrie(32)
	t.Insert(values[0])

	best := 0
	for _, v := range values[1:] {
		// Only values already inserted are considered, so each pair is tried once and
		// no value is paired with itself.
		if got, ok := t.MaxXORWith(v); ok {
			best = max(best, got)
		}
		t.Insert(v)
	}

	return best, true
}
