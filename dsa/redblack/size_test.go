package redblack

import (
	"testing"
	"unsafe"
)

// One bool costs 16 bytes per node
// ================================
//
// The benchmarks show this tree allocating 480 KB for 10,000 nodes where the
// unbalanced one in bst allocates 320 KB, which is 48 bytes against 32. The only
// difference between the two node types is the colour, and a bool is one byte.
//
// Go's allocator rounds every allocation up to a size class. 32 is a class; 33
// through 48 all become 48. So a struct of four 8-byte fields fits a class
// exactly, and adding a single byte to it costs sixteen.
//
// The fix, if 50% of a tree's memory mattered: steal the colour bit from a
// pointer, since node addresses are 8-byte aligned and the low three bits are
// always zero. That is what a production implementation does, and it is why
// production implementations of this are unreadable.
func TestNodeSizeAndAlignment(t *testing.T) {
	// The fields are never read: they exist to give the struct the same layout
	// as a node without a colour, which is the whole measurement.
	type colourless struct {
		key         int         //nolint:unused // measured via unsafe.Sizeof, never read
		value       int         //nolint:unused // measured via unsafe.Sizeof, never read
		left, right *colourless //nolint:unused // measured via unsafe.Sizeof, never read
	}

	withColour := unsafe.Sizeof(node[int, int]{})
	without := unsafe.Sizeof(colourless{})

	t.Logf("node with a colour: %d bytes, without: %d bytes", withColour, without)

	if without != 32 {
		t.Errorf("the colourless node is %d bytes, expected 32", without)
	}
	if withColour != 40 {
		t.Errorf("the node with a colour is %d bytes, expected 40 (33 padded to the 8-byte alignment)", withColour)
	}

	// 40 bytes is what the struct occupies. What it COSTS is the next size class
	// up, which is 48, and that is the number the benchmark's B/op reports.
	if got := roundToSizeClass(int(withColour)); got != 48 {
		t.Errorf("a %d-byte struct lands in the %d-byte size class, expected 48", withColour, got)
	}
}

// roundToSizeClass returns the allocation size Go's allocator would use, for the
// small classes this test cares about. The real table is in
// runtime/sizeclasses.go and has 68 entries.
func roundToSizeClass(n int) int {
	for _, class := range []int{8, 16, 24, 32, 48, 64, 80, 96, 112, 128} {
		if n <= class {
			return class
		}
	}
	return n
}

// TestAllocationPerNode confirms the 48 from the other direction, by measuring
// rather than reasoning about size classes.
func TestAllocationPerNode(t *testing.T) {
	const n = 10_000

	var m1, m2 memStats
	readMemStats(&m1)

	tr := New[int, int]()
	for i := range n {
		tr.Put(i, i)
	}

	readMemStats(&m2)

	perNode := float64(m2.TotalAlloc-m1.TotalAlloc) / n
	t.Logf("%.0f bytes allocated per node", perNode)

	if perNode < 40 || perNode > 64 {
		t.Errorf("%.0f bytes per node, expected about 48", perNode)
	}

	// Keep the tree alive so nothing above is optimised away.
	if tr.Len() != n {
		t.Fatal("the tree lost keys")
	}
}
