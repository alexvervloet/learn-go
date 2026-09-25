//go:build !debug

package main

// A CUSTOM build tag
// ==================
//
// This file is compiled by default. debug_on.go replaces it when you build
// with -tags debug:
//
//	go build ./14-embed-and-build-tags              -> this file
//	go build -tags debug ./14-embed-and-build-tags  -> debug_on.go
//
// The point is that the expensive version costs LITERALLY NOTHING in
// production: the code is not in the binary, so there is no branch to predict,
// no flag to read, and no way to enable it by accident.
//
// This is what `if debug { ... }` cannot give you, because that still compiles
// the body and still evaluates the condition.

// DebugEnabled reports whether this build has the debug tag. A constant, so
// the compiler folds every `if DebugEnabled` away entirely.
const DebugEnabled = false

// Assert does nothing in a normal build. An empty function body with no
// arguments used is inlined to nothing, so the call disappears.
//
// The cost of the ARGUMENTS is not free, though, which is the trap: a call
// like Assert(expensiveCheck(), "...") still evaluates expensiveCheck(). Pass a
// closure when the check itself is costly, as AssertFunc below does.
func Assert(condition bool, message string) {}

// AssertFunc takes the check as a function so the check itself is not run in a
// production build. The closure is never called, and the compiler can often
// drop the allocation entirely.
func AssertFunc(check func() bool, message string) {}

// Tracef does nothing here. In a debug build it prints.
func Tracef(format string, args ...any) {}

// debugModeExplanation describes what this build is.
func debugModeExplanation() []string {
	return []string{
		"this binary was built WITHOUT -tags debug",
		"Assert, AssertFunc and Tracef are empty and inline away to nothing",
		"DebugEnabled is a constant false, so `if DebugEnabled` is removed at compile time",
		"rebuild with `go run -tags debug ./14-embed-and-build-tags` to see the other half",
	}
}
