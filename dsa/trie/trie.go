// Package trie implements a prefix tree over strings.
//
// A trie answers one question a hash map cannot: "what words start with this?".
// A map can tell you whether "goland" is present in O(1), and to find everything
// starting with "go" it has to look at every key. A trie walks two nodes and
// then collects a subtree.
//
// The shape is the point. Each node holds one character and a map of children,
// so the word "go" is not stored anywhere: it is the path from the root through
// 'g' to 'o', and the node at the end carries a flag saying a word finishes here.
//
//	        (root)
//	       /      \
//	      g        t
//	      |        |
//	      o*       o*
//	     / \        \
//	    a   l        p*
//	    |   |
//	    l*  a
//	        |
//	        n
//	        |
//	        g*
//
//	* marks a terminal: go, goal, golang, to, top
package trie

import (
	"iter"
	"slices"
)

// node is one character in the tree. Nothing stores the character itself: it is
// the key in the parent's children map, which halves the memory and means a
// lookup never has to compare characters.
type node struct {
	children map[rune]*node

	// terminal marks the end of a word. It is separate from "has no children"
	// because "go" is a word and also a prefix of "golang", so a terminal node
	// can have children and a leaf need not be a word.
	terminal bool

	// words counts the terminals in this subtree, including this node. Kept
	// current on insert and delete so HasPrefix and Count are O(len(prefix))
	// rather than O(subtree).
	words int
}

func newNode() *node {
	return &node{children: make(map[rune]*node)}
}

// Trie is a prefix tree. The zero value is an empty trie ready to use.
type Trie struct {
	root *node
}

// New returns an empty trie. The zero value works too; New is here for symmetry
// with the other packages.
func New() *Trie { return &Trie{} }

// Len reports the number of distinct words.
func (t *Trie) Len() int {
	if t.root == nil {
		return 0
	}
	return t.root.words
}

// Insert adds word and reports whether it was new.
//
// O(len(word)), with no dependence at all on how many words the trie holds. That
// is the property that makes a trie worth the memory.
func (t *Trie) Insert(word string) bool {
	if t.root == nil {
		t.root = newNode()
	}

	// Walk first without mutating counts, because a duplicate insert must not
	// change anything. Two passes over a short string is cheaper than undoing.
	if t.Contains(word) {
		return false
	}

	current := t.root
	current.words++

	// range over a string yields runes, so this is correct for any input. A trie
	// keyed on bytes would split a multi-byte character across nodes, which
	// happens to work for Contains and breaks every prefix operation.
	for _, c := range word {
		child, ok := current.children[c]
		if !ok {
			child = newNode()
			current.children[c] = child
		}

		child.words++
		current = child
	}

	current.terminal = true

	return true
}

// find walks the path for s and returns the node at the end, or nil.
func (t *Trie) find(s string) *node {
	if t.root == nil {
		return nil
	}

	current := t.root
	for _, c := range s {
		child, ok := current.children[c]
		if !ok {
			return nil
		}
		current = child
	}
	return current
}

// Contains reports whether word is in the trie.
//
// The terminal check is the whole difference between Contains and HasPrefix:
// inserting "golang" puts a node at "go", and "go" is not a word unless someone
// inserted it.
func (t *Trie) Contains(word string) bool {
	n := t.find(word)
	return n != nil && n.terminal
}

// HasPrefix reports whether any word starts with prefix.
//
// The empty prefix matches whenever the trie is non-empty, which falls out of
// the walk doing nothing and the root's count being the answer.
func (t *Trie) HasPrefix(prefix string) bool {
	n := t.find(prefix)
	return n != nil && n.words > 0
}

// Count reports how many words start with prefix.
//
// O(len(prefix)), because each node carries the count for its subtree. Without
// that field this would have to walk the subtree, which is the difference
// between a suggestion box that feels instant and one that does not.
func (t *Trie) Count(prefix string) int {
	n := t.find(prefix)
	if n == nil {
		return 0
	}
	return n.words
}

// Complete returns every word starting with prefix, sorted.
//
// The autocomplete operation, and the reason the package exists. Cost is
// O(len(prefix)) to find the subtree plus O(total length of the results) to
// collect it, with nothing proportional to the size of the trie.
//
// Sorted because map iteration order is random, so an unsorted result would
// change between calls on the same data. Depth-first collection on a trie is
// almost sorted already: it needs only the children at each level ordered.
func (t *Trie) Complete(prefix string) []string {
	out := slices.Collect(t.WordsWithPrefix(prefix))
	slices.Sort(out)
	return out
}

// WordsWithPrefix iterates the words starting with prefix, in map order.
//
// An iter.Seq rather than a slice, so `for w := range t.WordsWithPrefix("go")`
// with a break stops the walk instead of building the whole result first. On a
// dictionary-sized trie with a one-letter prefix that is the difference between
// ten allocations and ten thousand.
func (t *Trie) WordsWithPrefix(prefix string) iter.Seq[string] {
	return func(yield func(string) bool) {
		start := t.find(prefix)
		if start == nil {
			return
		}

		// One buffer for the whole walk, holding the path from the root. Each
		// level appends its rune, recurses, and truncates.
		buf := make([]rune, 0, len(prefix)+16)
		buf = append(buf, []rune(prefix)...)

		collect(start, buf, yield)
	}
}

// collect walks a subtree depth-first, yielding each terminal.
//
// Two things here are not obvious.
//
// It returns false as soon as yield does, and every level has to propagate that.
// This is the part of the range-over-func contract a recursive iterator makes
// easy to get wrong: ignore it and a break in the caller finishes the walk
// anyway, then panics on the next yield.
//
// buf is one buffer reused for the whole walk, appended to on the way down and
// truncated on the way back up. The first version built a fresh
// strings.Builder per child, which reads better and allocated 4.4x more:
// 10,473 allocations and 309 KB for one Complete call, against 2,373 and 137 KB
// here. It did NOT get faster, at 515 against 492 microseconds, because the walk
// is dominated by iterating a map at every node rather than by string building.
// See README.md for where the time actually goes.
//
// The append may reallocate, so siblings can end up looking at a different
// array. That is harmless: the truncation happens before the next sibling
// writes, and string(buf) copies.
func collect(n *node, buf []rune, yield func(string) bool) bool {
	if n.terminal && !yield(string(buf)) {
		return false
	}

	for c, child := range n.children {
		buf = append(buf, c)

		if !collect(child, buf, yield) {
			return false
		}

		buf = buf[:len(buf)-1]
	}

	return true
}

// All iterates every word in the trie.
func (t *Trie) All() iter.Seq[string] { return t.WordsWithPrefix("") }

// Words returns every word, sorted.
func (t *Trie) Words() []string { return t.Complete("") }

// Delete removes word and reports whether it was there.
//
// Two things have to happen, and forgetting either leaves a trie that answers
// correctly while growing without bound. The terminal flag comes off, and every
// node on the path that now leads to no words at all is unlinked. A node stays
// if it is terminal or has children, because "go" must survive deleting "golang"
// and "golang" must survive deleting "go".
func (t *Trie) Delete(word string) bool {
	if !t.Contains(word) {
		return false
	}

	runes := []rune(word)

	// The path down, kept so the prune can walk back up. A trie node has no
	// parent pointer, and adding one to support delete would cost every node
	// eight bytes forever.
	path := make([]*node, 0, len(runes)+1)
	path = append(path, t.root)

	current := t.root
	for _, c := range runes {
		current = current.children[c]
		path = append(path, current)
	}

	current.terminal = false

	for _, n := range path {
		n.words--
	}

	// Walk back up, unlinking anything that is now dead. Stop at the first node
	// that is still needed: everything above it is needed too.
	for i := len(path) - 1; i > 0; i-- {
		n := path[i]
		if n.terminal || len(n.children) > 0 {
			break
		}
		delete(path[i-1].children, runes[i-1])
	}

	return true
}

// Match reports whether any word matches pattern, where '.' matches exactly one
// character.
//
// This is the operation a hash map cannot do at any price. "c.t" has to try
// every child at the wildcard position, so the cost is O(26^wildcards) in the
// worst case, but each wildcard only branches over children that actually exist.
// In a real dictionary that is far fewer than 26.
func (t *Trie) Match(pattern string) bool {
	if t.root == nil {
		return false
	}
	return match(t.root, []rune(pattern))
}

func match(n *node, pattern []rune) bool {
	if len(pattern) == 0 {
		return n.terminal
	}

	c, rest := pattern[0], pattern[1:]

	if c != '.' {
		child, ok := n.children[c]
		return ok && match(child, rest)
	}

	// The wildcard: try every child that exists.
	for _, child := range n.children {
		if match(child, rest) {
			return true
		}
	}
	return false
}

// LongestPrefixOf returns the longest word in the trie that is a prefix of s.
//
// Not an interview question, and the most useful method here. It is how an HTTP
// router matches /users/42/posts against a registered /users/, how a phone
// network picks a carrier from a number, and how an IP router picks a route.
func (t *Trie) LongestPrefixOf(s string) (string, bool) {
	if t.root == nil {
		return "", false
	}

	current := t.root
	best, bestLen := "", -1
	consumed := 0

	if current.terminal {
		best, bestLen = "", 0
	}

	for _, c := range s {
		child, ok := current.children[c]
		if !ok {
			break
		}

		consumed += len(string(c))
		current = child

		// Keep walking past a match: a longer one may be further down.
		if current.terminal {
			best, bestLen = s[:consumed], consumed
		}
	}

	return best, bestLen >= 0
}
