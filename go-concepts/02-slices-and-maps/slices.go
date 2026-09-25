// Package main is lesson 02 of go-concepts: slices and maps.
//
// A slice is a three-field header, not a container:
//
//	type sliceHeader struct {
//	    ptr *T   // start of the backing array
//	    len int  // elements currently addressable
//	    cap int  // elements available before a reallocation is needed
//	}
//
// The header is copied by value on every assignment and every function call.
// The array it points at is not. That single fact explains subslice aliasing,
// why append returns a value, and why a function can modify a caller's elements
// but cannot lengthen the caller's slice.
package main

import (
	"fmt"
	"slices"
)

// describe renders a slice's header so the demos can show len and cap changing.
func describe(name string, xs []int) string {
	return fmt.Sprintf("%s=%v len=%d cap=%d", name, xs, len(xs), cap(xs))
}

// subslicingShares proves that a subslice writes through to the parent. No
// copying happens at the xs[1:3] expression; only a new header is built.
func subslicingShares() (parent, child []int) {
	parent = []int{10, 20, 30, 40, 50}
	child = parent[1:3] // len 2, cap 4: from index 1 to the end of the array

	child[0] = 99 // writes parent[1]

	return parent, child
}

// appendMayMutateTheParent is the trap. child has len 2 and cap 4, so there is
// room in the shared array and append writes in place, clobbering parent[3].
//
//	parent [10 20 30 40 50]   cap 5
//	child       [20 30]       len 2, cap 4
//	append(child, 999) -> writes index 3 of the shared array
//	parent [10 20 30 999 50]
func appendMayMutateTheParent() (parent, child []int) {
	parent = []int{10, 20, 30, 40, 50}
	child = parent[1:3]

	child = append(child, 999)

	return parent, child
}

// appendCannotMutateWithFullSliceExpression is the fix. The three-index form
// parent[1:3:3] sets cap to 3-1 = 2, which equals len, so append has no spare
// room and is forced to allocate a fresh array.
//
// Use this whenever you return a subslice from a package, or pass one to code
// you do not control. It is the difference between "here is a view" and "here
// is a view that might scribble on my data".
func appendCannotMutateWithFullSliceExpression() (parent, child []int) {
	parent = []int{10, 20, 30, 40, 50}
	child = parent[1:3:3] // len 2, cap 2

	child = append(child, 999) // must allocate

	return parent, child
}

// growthReallocates records capacity after each append so the doubling is
// visible. The exact sequence is an implementation detail that has changed
// between Go releases, so the test asserts the invariants (cap >= len, cap only
// grows) rather than specific numbers.
func growthReallocates(n int) (caps []int) {
	var xs []int
	for i := 0; i < n; i++ {
		xs = append(xs, i)
		caps = append(caps, cap(xs))
	}
	return caps
}

// appendGrowing is preallocated's exact counterpart: same loop, same appends,
// only the make is missing. The two exist as a matched pair so the benchmark in
// slices_test.go measures the allocation strategy and nothing else.
func appendGrowing(n int) []int {
	var xs []int // nil, cap 0
	for i := 0; i < n; i++ {
		xs = append(xs, i)
	}
	return xs
}

// preallocated does the same work with the final size known up front. One
// allocation instead of log2(n) of them, and no copying. Benchmarked in
// slices_test.go.
func preallocated(n int) []int {
	xs := make([]int, 0, n) // len 0, cap n
	for i := 0; i < n; i++ {
		xs = append(xs, i)
	}
	return xs
}

// ignoringAppendResult shows why `go vet` treats a discarded append as an
// error. When append reallocates, the new pointer exists only in the returned
// header. Dropping it drops the appended data.
//
// This function is written the correct way; the broken form is in the README
// rather than here, because it would not survive `go vet`.
func appendMustBeAssigned() []int {
	xs := make([]int, 0, 1)
	xs = append(xs, 1)
	xs = append(xs, 2) // reallocates, and the result is kept
	return xs
}

// copyDoesNotGrow copies min(len(dst), len(src)) elements and returns that
// count. It will never extend dst, which is the mistake people make when they
// reach for copy expecting Python's list slice assignment.
func copyDoesNotGrow() (copiedIntoEmpty int, emptyAfter []int, copiedIntoSized int, sizedAfter []int) {
	src := []int{1, 2, 3}

	var dst []int                    // len 0
	copiedIntoEmpty = copy(dst, src) // copies 0 elements

	sized := make([]int, 3)
	copiedIntoSized = copy(sized, src)

	return copiedIntoEmpty, dst, copiedIntoSized, sized
}

// deleteLeaksTail performs the classic in-place delete. The element count drops
// but the backing array still holds the old value past the new length, where
// nothing will ever overwrite it.
//
//	before  [a b c d e]   len 5
//	delete index 1
//	after   [a c d e]     len 4, array is [a c d e e]
//	                                              ^ still referenced by the array
//
// For []int this wastes 8 bytes. For a slice of pointers or structs holding
// pointers, it pins whole object graphs for the lifetime of the slice.
func deleteLeaksTail(xs []string, i int) (result []string, tailStillSet string) {
	full := xs[:len(xs):len(xs)] // keep a full-length view to inspect the tail
	result = append(xs[:i], xs[i+1:]...)
	return result, full[len(full)-1]
}

// deleteClearsTail is the fix: zero the now-unreachable slot before shortening.
// slices.Delete (Go 1.21+) does exactly this, and is what to reach for in real
// code. The manual version is here to show what it is doing.
func deleteClearsTail(xs []string, i int) (result []string, tailCleared bool) {
	copy(xs[i:], xs[i+1:])
	last := len(xs) - 1
	xs[last] = "" // release the reference
	tailCleared = xs[last] == ""
	return xs[:last], tailCleared
}

// deleteWithStdlib is the same operation in one call. slices.Delete zeroes the
// vacated tail for element types that contain pointers.
func deleteWithStdlib(xs []string, i int) []string {
	return slices.Delete(xs, i, i+1)
}

// passingASliceToAFunction covers the half-mutable behaviour that confuses
// people: the callee CAN change elements the caller sees, because both headers
// point at one array, but CANNOT change the caller's length, because the header
// itself was copied.
func mutatesElements(xs []int) {
	if len(xs) > 0 {
		xs[0] = -1 // visible to the caller
	}
	xs = append(xs, 42) // invisible: only this local header grew
	_ = xs
}

// demoSlices prints each slice behaviour with its header state.
func demoSlices() {
	parent, child := subslicingShares()
	fmt.Println("  subslice writes through to the parent:")
	fmt.Printf("    %s\n    %s\n", describe("parent", parent), describe("child", child))

	parent, child = appendMayMutateTheParent()
	fmt.Println("  append with spare capacity clobbers the parent:")
	fmt.Printf("    %s\n    %s\n", describe("parent", parent), describe("child", child))

	parent, child = appendCannotMutateWithFullSliceExpression()
	fmt.Println("  parent[1:3:3] forces append to allocate:")
	fmt.Printf("    %s\n    %s\n", describe("parent", parent), describe("child", child))

	fmt.Printf("  capacity after each of 10 appends: %v\n", growthReallocates(10))
	fmt.Printf("  make([]int, 0, 10) then 10 appends: cap stays %d\n", cap(preallocated(10)))

	n1, empty, n2, sized := copyDoesNotGrow()
	fmt.Printf("  copy(nil, [1 2 3]) copied %d -> %v\n", n1, empty)
	fmt.Printf("  copy(make(3), [1 2 3]) copied %d -> %v\n", n2, sized)

	res, tail := deleteLeaksTail([]string{"a", "b", "c", "d", "e"}, 1)
	fmt.Printf("  manual delete -> %v, but the array tail still holds %q\n", res, tail)

	res, cleared := deleteClearsTail([]string{"a", "b", "c", "d", "e"}, 1)
	fmt.Printf("  delete + zero  -> %v, tail cleared: %t\n", res, cleared)
	fmt.Printf("  slices.Delete  -> %v\n", deleteWithStdlib([]string{"a", "b", "c", "d", "e"}, 1))

	xs := []int{1, 2, 3}
	mutatesElements(xs)
	fmt.Printf("  after mutatesElements: %s  (element changed, length did not)\n", describe("xs", xs))

	fmt.Printf("  appendMustBeAssigned() -> %v\n", appendMustBeAssigned())
}
