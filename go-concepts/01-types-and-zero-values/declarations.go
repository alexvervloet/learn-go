package main

import (
	"fmt"
	"strconv"
)

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

// shadowingTrap shows the classic Go shadowing bug. The function has an err,
// and the := inside the if block needs a new variable for n, so it declares a
// NEW err as well, in the block's scope. The parse error lands in the inner
// err, which vanishes at the closing brace, and the caller sees success.
//
// An earlier version named the outer variable outerErr and the inner one err,
// so nothing was shadowed and the example only showed an unused variable.
//
// The compiler accepts this and so does go vet's default set. vet has a shadow
// analyzer, but it is off by default because it also flags the idiomatic
//
//	if err := f(); err != nil { ... }
//
// which shadows an outer err on purpose. Turned on across this repository it
// reported 50 findings, nearly all of that shape, so it stays off here too.
// Reading := as "declare" every time is the defence.
func shadowingTrap(input string) (n int, err error) {
	if input != "" {
		n, err := strconv.Atoi(input) // new n AND new err, both scoped to this block
		_ = n
		_ = err
	}
	// The outer n and err were never touched. The failure vanished.
	return n, err
}

// shadowingFixed assigns to the existing variables with = instead of :=.
func shadowingFixed(input string) (n int, err error) {
	if input != "" {
		n, err = strconv.Atoi(input)
	}
	return n, err
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
	_, lost := shadowingTrap("not a number")
	_, kept := shadowingFixed("not a number")
	fmt.Printf("  shadowingTrap(\"not a number\")  -> err=%v   (the error was lost)\n", lost)
	fmt.Printf("  shadowingFixed(\"not a number\") -> err=%v\n", kept)

	n, s, err := multipleAssignment()
	fmt.Printf("  multipleAssignment() -> %d, %q, %v\n", n, s, err)

	a, b := swapWithoutTemp(1, 2)
	fmt.Printf("  swapWithoutTemp(1, 2) -> %d, %d\n", a, b)

	fmt.Printf("  blankIdentifier() -> %d\n", blankIdentifier())
}
