package main

import "fmt"

// Declaration forms
// =================
//
//	var x int           // zero value, explicit type
//	var x = 5           // type inferred from the value
//	var x int = 5       // both, redundant, vet will not complain but reviewers will
//	x := 5              // short form, function bodies only
//
// The short form has one rule worth internalising: it declares at least one new
// variable on the left, and reuses any that already exist in THE SAME scope. A
// new scope makes it declare a new variable that shadows the outer one, which
// is where the bugs live.

// shadowingTrap shows the classic Go shadowing bug. The inner := inside the if
// block creates a NEW err that goes out of scope at the closing brace, so the
// outer err stays nil and the caller sees success.
//
// go vet does not catch this. `go build -gcflags=-m` does not either. The
// shadow linter in golangci-lint does, which is why .golangci.yml enables it.
func shadowingTrap() (outerErr error) {
	if true {
		// This := declares a new err, scoped to the if block.
		err := fmt.Errorf("something failed")
		_ = err
	}
	// outerErr was never touched. The failure vanished.
	return outerErr
}

// shadowingFixed assigns to the existing variable with = instead of :=.
func shadowingFixed() (outerErr error) {
	if true {
		outerErr = fmt.Errorf("something failed")
	}
	return outerErr
}

// multipleAssignment returns several values, which Go uses instead of tuples.
// Unlike Python there is no tuple object here: this is a language-level feature
// of function results, and the values cannot be stored as one thing.
func multipleAssignment() (int, string, error) {
	return 42, "answer", nil
}

// swapWithoutTemp works because the right-hand side is fully evaluated before
// any assignment happens, exactly like Python's a, b = b, a.
func swapWithoutTemp(a, b int) (int, int) {
	a, b = b, a
	return a, b
}

// blankIdentifier shows the three jobs _ does: discard a result, satisfy the
// "declared and not used" compile error, and assert an interface at compile
// time (see interfaceGuard below).
func blankIdentifier() int {
	n, _, _ := multipleAssignment() // keep the int, discard the rest
	return n
}

// Compile-time interface assertion. If Counter ever stops satisfying
// fmt.Stringer, this line fails the build with a clear message, at the
// definition rather than at some distant call site. Costs nothing at runtime:
// the value is discarded.
var _ fmt.Stringer = (*Counter)(nil)

// demoDeclarations prints the shadowing difference, which is the part of this
// file worth seeing rather than reading.
func demoDeclarations() {
	fmt.Printf("  shadowingTrap()  -> %v   (the error was lost)\n", shadowingTrap())
	fmt.Printf("  shadowingFixed() -> %v\n", shadowingFixed())

	n, s, err := multipleAssignment()
	fmt.Printf("  multipleAssignment() -> %d, %q, %v\n", n, s, err)

	a, b := swapWithoutTemp(1, 2)
	fmt.Printf("  swapWithoutTemp(1, 2) -> %d, %d\n", a, b)

	fmt.Printf("  blankIdentifier() -> %d\n", blankIdentifier())
}
