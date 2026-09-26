// Package redblack implements a left-leaning red-black tree: a balanced binary
// search tree whose height stays within 2*log2(n) whatever order the keys arrive
// in.
//
// The bst package shows the problem this solves. An ordinary search tree built
// from sorted keys is a linked list, and sorted keys are what you get from a
// database dump, a log file, or an auto-increment ID. At 10,000 keys that is a
// height of 10,000 and a lookup 173x slower than it should be. This tree gives
// height 14 for the same input.
//
// # What a red-black tree actually is
//
// It is a 2-3 tree in disguise. A 2-3 tree keeps itself balanced by storing
// either one key or TWO keys in a node, and growing by splitting a node upwards
// rather than by getting taller downwards. That is easy to reason about and
// annoying to implement, because a node has two shapes.
//
// The trick is to represent a two-key node as two ordinary nodes joined by a
// link painted RED:
//
//	2-3 tree node        red-black representation
//
//	   (30 50)                  50
//	   /  |  \                 /
//	  a   b   c              30            <- joined to 50 by a red link
//	                        /  \
//	                       a    b     c
//
// So every node is the same shape, and "red" means "this node is really part of
// its parent". Left-leaning means a red link always goes to the left child,
// which halves the number of cases to handle and is the only difference from the
// classic formulation.
//
// # The invariants
//
// Three rules, and together they force the height to log n:
//
//  1. No node has two red links in a row.
//  2. No red link leans right.
//  3. Every path from the root to a nil link crosses the same number of BLACK
//     links. This is the one that does the work.
//
// Rule 3 alone would give a perfectly balanced tree if red links did not exist.
// Red links let a path be longer, and rule 1 caps how much longer: at most
// double. Hence height <= 2*log2(n), and in practice much closer to log2(n).
//
// # Three operations maintain all of it
//
//	rotateLeft   a red right link becomes a red left link
//	rotateRight  a red left link becomes a red right link
//	flipColors   two red children become one red parent (a 2-3 node splitting)
//
// Insertion walks down, attaches a red node, and then applies those three on the
// way back up, in a fixed order. That fixed order is `balance`, and it is four
// lines. The hard part of a red-black tree is not insertion.
//
// Deletion is the hard part, and this is the honest version of why: it needs two
// more transformations, moveRedLeft and moveRedRight, whose job is to guarantee
// that the node about to be removed is red, because removing a red node cannot
// break rule 3. Sedgewick's insight, and the reason this implementation is the
// one worth learning, is that the whole thing then fits in forty lines.
package redblack

import (
	"cmp"
	"iter"
)

// A link's colour. Stored on the child, meaning "the link from my parent to me".
const (
	red   = true
	black = false
)

type node[K cmp.Ordered, V any] struct {
	key         K
	value       V
	left, right *node[K, V]

	// color is the colour of the link from this node's PARENT, not of the node
	// itself. Storing it on the child is what makes it a single bool: a node has
	// exactly one parent link. The root's colour is black by convention and
	// never read.
	color bool
}

// isRed reports whether the link into n is red. nil links are black, and having
// this as a function rather than a field read is what keeps every rule below
// from needing a nil check.
func isRed[K cmp.Ordered, V any](n *node[K, V]) bool {
	// `n.color` rather than `n.color == red`, which staticcheck rejects as a
	// comparison to a bool constant (S1002). The named constants are still worth
	// having, because `color: red` at a construction site says what a bare true
	// does not.
	return n != nil && n.color
}

// Tree is a balanced binary search tree. The zero value is an empty tree ready
// to use.
type Tree[K cmp.Ordered, V any] struct {
	root  *node[K, V]
	count int
}

// New returns an empty tree. The zero value works too.
func New[K cmp.Ordered, V any]() *Tree[K, V] { return &Tree[K, V]{} }

// Len reports the number of keys.
func (t *Tree[K, V]) Len() int { return t.count }

// Get returns the value for key and reports whether it was present.
//
// Identical to an ordinary search tree: the colours have no part in a lookup.
// They exist to keep the height small, and a lookup only ever benefits.
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

// rotateLeft turns a red RIGHT link into a red LEFT link.
//
//	  h                x
//	 / \\             // \
//	a    x    ->      h    c
//	    / \          / \
//	   b   c        a   b
//
// The new parent inherits the old parent's colour, and the old parent becomes
// red, because the pair of them still represents the same 2-3 node.
func rotateLeft[K cmp.Ordered, V any](h *node[K, V]) *node[K, V] {
	x := h.right

	h.right = x.left
	x.left = h

	x.color = h.color
	h.color = red

	return x
}

// rotateRight turns a red LEFT link into a red RIGHT link, the mirror image.
func rotateRight[K cmp.Ordered, V any](h *node[K, V]) *node[K, V] {
	x := h.left

	h.left = x.right
	x.right = h

	x.color = h.color
	h.color = red

	return x
}

// flipColors inverts h and both of its children.
//
// With two red children it turns them black and h red, which is a 2-3 node
// splitting and passing a key up to its parent. Run on the way DOWN during
// deletion it does the opposite, borrowing a key from the parent. One function
// for both directions is why this variant is short, and it is also why the
// function is `!h.color` rather than an explicit colour: it has to work in
// either direction.
func flipColors[K cmp.Ordered, V any](h *node[K, V]) {
	h.color = !h.color
	h.left.color = !h.left.color
	h.right.color = !h.right.color
}

// balance restores all three invariants at one node, assuming its subtrees are
// already fine.
//
// The order matters and is not arbitrary:
//
//  1. A red right link with a black left link leans the wrong way: rotate left.
//  2. Two reds in a row on the left: rotate right, which makes them siblings.
//  3. Two red children: flip, which pushes the red up to the parent.
//
// Step 2 can only produce the situation step 3 handles, and step 3 can only
// produce a violation at the parent, which the parent's own call to balance will
// see. That is the whole induction.
func balance[K cmp.Ordered, V any](h *node[K, V]) *node[K, V] {
	if isRed(h.right) && !isRed(h.left) {
		h = rotateLeft(h)
	}
	if isRed(h.left) && isRed(h.left.left) {
		h = rotateRight(h)
	}
	if isRed(h.left) && isRed(h.right) {
		flipColors(h)
	}
	return h
}

// Put inserts or replaces key, and reports whether the key was new.
func (t *Tree[K, V]) Put(key K, value V) bool {
	var isNew bool
	t.root, isNew = put(t.root, key, value)

	// The root has no parent link, so it is black by definition. flipColors can
	// leave it red, and this is where that is undone. Each time it happens the
	// black height of the whole tree goes up by one, which is the only way this
	// tree ever gets taller: from the root, never from the leaves.
	t.root.color = black

	if isNew {
		t.count++
	}
	return isNew
}

// put returns the rebalanced subtree and whether a node was added.
//
// Threading that bool back up is less tidy than asking Contains first, and the
// first version did ask. That cost a second full walk down the tree on every
// insert, measured at 26% for 10,000 shuffled keys: 1.77 ms before, 1.31 ms
// after.
func put[K cmp.Ordered, V any](h *node[K, V], key K, value V) (*node[K, V], bool) {
	if h == nil {
		// New nodes are RED, always. A red link does not change the black height,
		// so attaching one cannot break rule 3. It can break rules 1 and 2, and
		// those are what balance fixes on the way back up.
		return &node[K, V]{key: key, value: value, color: red}, true
	}

	var isNew bool

	switch cmp.Compare(key, h.key) {
	case -1:
		h.left, isNew = put(h.left, key, value)
	case 1:
		h.right, isNew = put(h.right, key, value)
	default:
		h.value = value
	}

	return balance(h), isNew
}

// Min returns the smallest key and its value.
func (t *Tree[K, V]) Min() (K, V, bool) {
	if t.root == nil {
		var zeroK K
		var zeroV V
		return zeroK, zeroV, false
	}
	n := minNode(t.root)
	return n.key, n.value, true
}

func minNode[K cmp.Ordered, V any](h *node[K, V]) *node[K, V] {
	for h.left != nil {
		h = h.left
	}
	return h
}

// Max returns the largest key and its value.
func (t *Tree[K, V]) Max() (K, V, bool) {
	if t.root == nil {
		var zeroK K
		var zeroV V
		return zeroK, zeroV, false
	}

	h := t.root
	for h.right != nil {
		h = h.right
	}
	return h.key, h.value, true
}

// Floor returns the largest key less than or equal to key.
func (t *Tree[K, V]) Floor(key K) (K, V, bool) {
	var best *node[K, V]

	for current := t.root; current != nil; {
		if current.key == key {
			return current.key, current.value, true
		}
		if current.key < key {
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

	for current := t.root; current != nil; {
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

// moveRedLeft is called on the way down when the deletion is heading left and
// the left child is a 2-node, meaning it has no key to spare.
//
// It borrows one: flip to make h's children red, and if h's right child has one
// to give, rotate it across. Afterwards h.left is guaranteed red, which is what
// the recursion needs.
func moveRedLeft[K cmp.Ordered, V any](h *node[K, V]) *node[K, V] {
	flipColors(h)

	if isRed(h.right.left) {
		h.right = rotateRight(h.right)
		h = rotateLeft(h)
		flipColors(h)
	}
	return h
}

// moveRedRight is the mirror image, for a deletion heading right.
func moveRedRight[K cmp.Ordered, V any](h *node[K, V]) *node[K, V] {
	flipColors(h)

	if isRed(h.left.left) {
		h = rotateRight(h)
		flipColors(h)
	}
	return h
}

// DeleteMin removes the smallest key and reports whether there was one.
func (t *Tree[K, V]) DeleteMin() bool {
	if t.root == nil {
		return false
	}

	if !isRed(t.root.left) && !isRed(t.root.right) {
		t.root.color = red // lend the root's blackness to the walk down
	}

	t.root = deleteMin(t.root)
	if t.root != nil {
		t.root.color = black
	}

	t.count--
	return true
}

func deleteMin[K cmp.Ordered, V any](h *node[K, V]) *node[K, V] {
	if h.left == nil {
		// h is the smallest, and the invariant maintained on the way down
		// guarantees it is red, so unlinking it cannot change any black height.
		return nil
	}

	if !isRed(h.left) && !isRed(h.left.left) {
		h = moveRedLeft(h)
	}

	h.left = deleteMin(h.left)

	return balance(h)
}

// DeleteMax removes the largest key and reports whether there was one.
func (t *Tree[K, V]) DeleteMax() bool {
	if t.root == nil {
		return false
	}

	if !isRed(t.root.left) && !isRed(t.root.right) {
		t.root.color = red
	}

	t.root = deleteMax(t.root)
	if t.root != nil {
		t.root.color = black
	}

	t.count--
	return true
}

func deleteMax[K cmp.Ordered, V any](h *node[K, V]) *node[K, V] {
	// A left-leaning tree has to lean the red right before walking right, or the
	// largest key may be hanging off a red left link that the walk never sees.
	if isRed(h.left) {
		h = rotateRight(h)
	}

	if h.right == nil {
		return nil
	}

	if !isRed(h.right) && !isRed(h.right.left) {
		h = moveRedRight(h)
	}

	h.right = deleteMax(h.right)

	return balance(h)
}

// Delete removes key and reports whether it was there.
//
// The strategy is the one thing to remember: make sure the node being removed is
// red before removing it. A red node's link contributes nothing to any black
// height, so unlinking it cannot break rule 3, and rules 1 and 2 are local
// enough for balance to repair on the way back up.
//
// moveRedLeft and moveRedRight are what provide that guarantee, borrowing a key
// from a sibling or a parent on the way down so the target is never a lone
// 2-node.
func (t *Tree[K, V]) Delete(key K) bool {
	if !t.Contains(key) {
		return false
	}

	if !isRed(t.root.left) && !isRed(t.root.right) {
		t.root.color = red
	}

	t.root = del(t.root, key)
	if t.root != nil {
		t.root.color = black
	}

	t.count--
	return true
}

func del[K cmp.Ordered, V any](h *node[K, V], key K) *node[K, V] {
	if key < h.key {
		// h.left cannot be nil: Delete checked the key is present, so the left
		// subtree has it. Every dereference below rests on that check, which is
		// why Delete does the lookup first instead of letting del discover the
		// key is absent.
		if !isRed(h.left) && !isRed(h.left.left) {
			h = moveRedLeft(h)
		}
		h.left = del(h.left, key)

		return balance(h)
	}

	if isRed(h.left) {
		h = rotateRight(h)
	}

	// The key is here and there is nothing to the right, so this node is the
	// largest in its subtree and, by the invariant, red.
	if key == h.key && h.right == nil {
		return nil
	}

	if !isRed(h.right) && !isRed(h.right.left) {
		h = moveRedRight(h)
	}

	if key == h.key {
		// Two children: take the in-order successor's payload and delete the
		// successor, exactly as an unbalanced tree does. The rebalancing is what
		// deleteMin adds on top.
		successor := minNode(h.right)
		h.key = successor.key
		h.value = successor.value
		h.right = deleteMin(h.right)
	} else {
		h.right = del(h.right, key)
	}

	return balance(h)
}

// All iterates the keys in sorted order.
func (t *Tree[K, V]) All() iter.Seq2[K, V] {
	return func(yield func(K, V) bool) {
		inOrder(t.root, yield)
	}
}

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

// Range iterates the keys in [lo, hi] in sorted order, pruning subtrees that
// cannot contain a match.
func (t *Tree[K, V]) Range(lo, hi K) iter.Seq2[K, V] {
	return func(yield func(K, V) bool) {
		rangeWalk(t.root, lo, hi, yield)
	}
}

func rangeWalk[K cmp.Ordered, V any](n *node[K, V], lo, hi K, yield func(K, V) bool) bool {
	if n == nil {
		return true
	}
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

// Height returns the number of levels, counting both red and black links.
func (t *Tree[K, V]) Height() int { return height(t.root) }

func height[K cmp.Ordered, V any](n *node[K, V]) int {
	if n == nil {
		return 0
	}
	return 1 + max(height(n.left), height(n.right))
}

// BlackHeight returns the number of black links on any root-to-nil path.
//
// Every path has the same count, which is invariant 3, so "any path" is
// well defined. This is the number that is actually held constant; Height is
// what you get when the red links are counted too, and it can be up to twice as
// large.
func (t *Tree[K, V]) BlackHeight() int {
	n := 0
	for current := t.root; current != nil; current = current.left {
		if !isRed(current) {
			n++
		}
	}
	return n
}

// IsValid checks all three invariants plus the ordering, and is called after
// every mutation in the tests.
//
// Checking a balanced tree only for correct ordering is the mistake worth
// avoiding: a red-black tree with broken colours still answers every query
// correctly, and it is just a slow unbalanced tree wearing the name. The bug
// would show up as a performance regression months later.
func (t *Tree[K, V]) IsValid() bool {
	return t.isOrdered() && t.is23() && t.isBalanced() && countNodes(t.root) == t.count
}

func (t *Tree[K, V]) isOrdered() bool { return ordered(t.root, nil, nil) }

func ordered[K cmp.Ordered, V any](n *node[K, V], lo, hi *K) bool {
	if n == nil {
		return true
	}
	if lo != nil && n.key <= *lo {
		return false
	}
	if hi != nil && n.key >= *hi {
		return false
	}
	return ordered(n.left, lo, &n.key) && ordered(n.right, &n.key, hi)
}

// is23 checks rules 1 and 2: no red link leans right, and no two reds in a row.
func (t *Tree[K, V]) is23() bool { return is23(t.root, t.root) }

func is23[K cmp.Ordered, V any](root, n *node[K, V]) bool {
	if n == nil {
		return true
	}
	if isRed(n.right) {
		return false // leans right
	}
	if n != root && isRed(n) && isRed(n.left) {
		return false // two in a row
	}
	return is23(root, n.left) && is23(root, n.right)
}

// isBalanced checks rule 3: every root-to-nil path crosses the same number of
// black links. This is the invariant that makes the tree balanced, and the only
// one whose violation cannot be seen locally.
func (t *Tree[K, V]) isBalanced() bool {
	return blackBalanced(t.root, t.BlackHeight())
}

func blackBalanced[K cmp.Ordered, V any](n *node[K, V], remaining int) bool {
	if n == nil {
		return remaining == 0
	}
	if !isRed(n) {
		remaining--
	}
	return blackBalanced(n.left, remaining) && blackBalanced(n.right, remaining)
}

func countNodes[K cmp.Ordered, V any](n *node[K, V]) int {
	if n == nil {
		return 0
	}
	return 1 + countNodes(n.left) + countNodes(n.right)
}
