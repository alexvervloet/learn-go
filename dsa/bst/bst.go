// Package bst implements an unbalanced binary search tree.
//
// The invariant is one sentence: every key in a node's left subtree is smaller
// than the node, and every key in its right subtree is larger. Everything else
// follows from it, including the reason this structure is usually the wrong
// choice.
//
//	       50
//	     /    \
//	   30      70
//	  /  \    /  \
//	20   40  60   80
//
// A search compares once per level and throws away half the remaining tree, so
// it costs O(height). On a balanced tree the height is log2(n): twenty
// comparisons for a million keys. The catch is that nothing here keeps the tree
// balanced, and the shape depends entirely on the order the keys arrived in.
// Insert 1..n in order and every node has one child, the height is n, and every
// operation is a linked-list walk. See the numbers in README.md, and see the
// redblack package for the fix.
//
// What a BST offers that a hash map does not: the keys come out sorted in O(n),
// and Min, Max, Floor, Ceiling and range queries all work. A hash map answers
// none of those at any price.
package bst

import (
	"cmp"
	"iter"
)

type node[K cmp.Ordered, V any] struct {
	key         K
	value       V
	left, right *node[K, V]
}

// Tree is a binary search tree. The zero value is an empty tree ready to use.
type Tree[K cmp.Ordered, V any] struct {
	root  *node[K, V]
	count int
}

// New returns an empty tree. The zero value works too.
func New[K cmp.Ordered, V any]() *Tree[K, V] { return &Tree[K, V]{} }

// Len reports the number of keys.
func (t *Tree[K, V]) Len() int { return t.count }

// Put inserts or replaces key, and reports whether the key was new.
//
// Iterative, because insertion has no reason to recurse: it walks down and
// attaches one node, with nothing to do on the way back up. The trick that makes
// the loop short is tracking a **node, so the nil child slot can be written
// through without keeping a parent pointer and a side.
func (t *Tree[K, V]) Put(key K, value V) bool {
	link := &t.root // the pointer that will hold the new node

	for *link != nil {
		current := *link

		switch cmp.Compare(key, current.key) {
		case 0:
			current.value = value // replace, no new node
			return false
		case -1:
			link = &current.left
		default:
			link = &current.right
		}
	}

	// link points at the nil child slot where the key belongs. Writing through
	// it is what attaches the node, which is why the loop tracks a **node
	// rather than a parent pointer and a side.
	*link = &node[K, V]{key: key, value: value}
	t.count++

	return true
}

// Get returns the value for key and reports whether it was present.
func (t *Tree[K, V]) Get(key K) (V, bool) {
	current := t.root

	for current != nil {
		switch cmp.Compare(key, current.key) {
		case 0:
			return current.value, true
		case -1:
			current = current.left
		default:
			current = current.right
		}
	}

	var zero V
	return zero, false
}

// Contains reports whether key is present.
func (t *Tree[K, V]) Contains(key K) bool {
	_, ok := t.Get(key)
	return ok
}

// Min returns the smallest key and its value.
//
// Walk left until you cannot. The leftmost node has no left child by the
// invariant, so it holds the smallest key.
func (t *Tree[K, V]) Min() (K, V, bool) {
	if t.root == nil {
		var zeroK K
		var zeroV V
		return zeroK, zeroV, false
	}

	current := t.root
	for current.left != nil {
		current = current.left
	}
	return current.key, current.value, true
}

// Max returns the largest key and its value.
func (t *Tree[K, V]) Max() (K, V, bool) {
	if t.root == nil {
		var zeroK K
		var zeroV V
		return zeroK, zeroV, false
	}

	current := t.root
	for current.right != nil {
		current = current.right
	}
	return current.key, current.value, true
}

// Floor returns the largest key less than or equal to key.
//
// This is one of the operations a hash map cannot answer. "The most recent
// reading at or before this timestamp" is a Floor query, and it is why a time
// series index is a tree rather than a map.
func (t *Tree[K, V]) Floor(key K) (K, V, bool) {
	var best *node[K, V]

	current := t.root
	for current != nil {
		if current.key == key {
			return current.key, current.value, true
		}

		if current.key < key {
			// A candidate, but there may be a closer one on the right.
			best = current
			current = current.right
			continue
		}
		current = current.left
	}

	if best == nil {
		var zeroK K
		var zeroV V
		return zeroK, zeroV, false
	}
	return best.key, best.value, true
}

// Ceiling returns the smallest key greater than or equal to key.
func (t *Tree[K, V]) Ceiling(key K) (K, V, bool) {
	var best *node[K, V]

	current := t.root
	for current != nil {
		if current.key == key {
			return current.key, current.value, true
		}

		if current.key > key {
			best = current
			current = current.left
			continue
		}
		current = current.right
	}

	if best == nil {
		var zeroK K
		var zeroV V
		return zeroK, zeroV, false
	}
	return best.key, best.value, true
}

// Delete removes key and reports whether it was there.
//
// Three cases, and the third is the only interesting one.
//
//	no children    unlink it
//	one child      promote the child into its place
//	two children   replace its key and value with its in-order SUCCESSOR
//	               (the smallest key in the right subtree), then delete the
//	               successor, which by construction has no left child and so
//	               falls into one of the first two cases
//
// The successor is the only key that can take the node's place without breaking
// the invariant: it is larger than everything on the left and smaller than
// everything else on the right. The predecessor works identically, and always
// choosing one of the two is what makes repeated deletion skew a tree left or
// right over time.
func (t *Tree[K, V]) Delete(key K) bool {
	link := &t.root

	// Find the link pointing at the node to remove.
	for *link != nil && (*link).key != key {
		if key < (*link).key {
			link = &(*link).left
			continue
		}
		link = &(*link).right
	}

	target := *link
	if target == nil {
		return false
	}

	switch {
	case target.left == nil:
		*link = target.right // covers no children too, since right is nil

	case target.right == nil:
		*link = target.left

	default:
		// Two children. Find the successor and the link that holds it.
		succLink := &target.right
		for (*succLink).left != nil {
			succLink = &(*succLink).left
		}
		successor := *succLink

		// Move the successor's payload up, then unlink the successor. It has no
		// left child, so its right child (possibly nil) takes its place.
		target.key = successor.key
		target.value = successor.value
		*succLink = successor.right
	}

	t.count--
	return true
}

// Height returns the number of levels. An empty tree is 0 and a single node is 1.
//
// The number that says whether this tree is doing its job. log2(n)+1 is the best
// possible and n is the worst; Put in sorted order gets you the worst.
func (t *Tree[K, V]) Height() int { return height(t.root) }

func height[K cmp.Ordered, V any](n *node[K, V]) int {
	if n == nil {
		return 0
	}
	return 1 + max(height(n.left), height(n.right))
}

// All iterates the keys in sorted order.
//
// This is the operation that justifies the structure. An in-order walk is O(n)
// with no comparisons at all: the ordering work was done at insertion time. A
// hash map has to collect and sort, at O(n log n).
//
// Recursive, which was not the first choice and is the measured one. The first
// version kept an explicit stack, on the reasoning that a degenerate tree is a
// chain and recursing down it would be 100,000 frames deep. Go grows a goroutine
// stack on demand up to 1 GB, so that depth is not a problem at any size that
// fits in memory, and BenchmarkTraversalStyle shows the trade going the other way:
//
//	shape                recursive   explicit stack
//	balanced, 50k        488 us      512 us
//	chain of right kids  401 us      197 us
//	chain of left kids   459 us      766 us + 2.2 MB
//
// The stack version wins 2x on a right-leaning chain, where it never holds more
// than one node, and loses while allocating 2.2 MB on a left-leaning one, where
// it has to hold all 50,000 before yielding the first key. Recursion is within 5%
// on the shape that actually occurs and never allocates, so it wins.
func (t *Tree[K, V]) All() iter.Seq2[K, V] {
	return func(yield func(K, V) bool) {
		inOrder(t.root, yield)
	}
}

// inOrder walks left, visits, walks right, and propagates a false from yield up
// through every level. Missing that propagation is the classic range-over-func
// bug: a break in the caller would let the walk finish anyway and then panic on
// the next yield.
func inOrder[K cmp.Ordered, V any](n *node[K, V], yield func(K, V) bool) bool {
	if n == nil {
		return true
	}
	if !inOrder(n.left, yield) {
		return false
	}
	if !yield(n.key, n.value) {
		return false
	}
	return inOrder(n.right, yield)
}

// Keys returns the keys in sorted order.
func (t *Tree[K, V]) Keys() []K {
	out := make([]K, 0, t.count)
	for k := range t.All() {
		out = append(out, k)
	}
	return out
}

// Range iterates the keys in [lo, hi] in sorted order.
//
// The other query a hash map cannot do. It prunes: a subtree entirely below lo or
// entirely above hi is never entered, so the cost is O(height + results) rather
// than O(n).
func (t *Tree[K, V]) Range(lo, hi K) iter.Seq2[K, V] {
	return func(yield func(K, V) bool) {
		rangeWalk(t.root, lo, hi, yield)
	}
}

func rangeWalk[K cmp.Ordered, V any](n *node[K, V], lo, hi K, yield func(K, V) bool) bool {
	if n == nil {
		return true
	}

	// Only descend left if something down there can be in range.
	if n.key > lo && !rangeWalk(n.left, lo, hi, yield) {
		return false
	}

	if n.key >= lo && n.key <= hi && !yield(n.key, n.value) {
		return false
	}

	if n.key < hi && !rangeWalk(n.right, lo, hi, yield) {
		return false
	}

	return true
}

// LevelOrder iterates the keys breadth-first, which is the order that shows the
// tree's shape. Nothing else here needs it; it is the traversal you want when
// debugging or drawing a tree.
func (t *Tree[K, V]) LevelOrder() iter.Seq2[K, V] {
	return func(yield func(K, V) bool) {
		if t.root == nil {
			return
		}

		queue := []*node[K, V]{t.root}

		for len(queue) > 0 {
			n := queue[0]
			queue = queue[1:]

			if !yield(n.key, n.value) {
				return
			}

			if n.left != nil {
				queue = append(queue, n.left)
			}
			if n.right != nil {
				queue = append(queue, n.right)
			}
		}
	}
}

// IsValid checks the invariant across the whole tree, which is what the tests
// assert after every mutation.
//
// The naive version, "left child is smaller and right child is larger", passes
// trees that are broken: a node in the far left subtree can be larger than the
// root while satisfying every local check. So each node is checked against the
// open interval it is allowed to occupy, narrowed on the way down.
func (t *Tree[K, V]) IsValid() bool {
	return valid(t.root, nil, nil) && countNodes(t.root) == t.count
}

func valid[K cmp.Ordered, V any](n *node[K, V], lo, hi *K) bool {
	if n == nil {
		return true
	}
	if lo != nil && n.key <= *lo {
		return false
	}
	if hi != nil && n.key >= *hi {
		return false
	}
	return valid(n.left, lo, &n.key) && valid(n.right, &n.key, hi)
}

func countNodes[K cmp.Ordered, V any](n *node[K, V]) int {
	if n == nil {
		return 0
	}
	return 1 + countNodes(n.left) + countNodes(n.right)
}
