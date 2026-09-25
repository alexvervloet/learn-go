//go:build debug

package main

import (
	"fmt"
	"os"
	"runtime"
)

// The debug build. Selected with -tags debug; see debug_off.go for the
// production version and the reasoning.

// DebugEnabled reports whether this build has the debug tag.
const DebugEnabled = true

// Assert panics when the condition is false, naming the caller. In production
// this function is empty, so the check costs nothing there.
func Assert(condition bool, message string) {
	if condition {
		return
	}

	_, file, line, ok := runtime.Caller(1)
	if !ok {
		panic("assertion failed: " + message)
	}
	panic(fmt.Sprintf("assertion failed at %s:%d: %s", file, line, message))
}

// AssertFunc runs the check and panics when it reports false. The closure form
// exists so an expensive check is not evaluated in a production build, where
// the argument would still be computed if it were a plain bool.
func AssertFunc(check func() bool, message string) {
	if check() {
		return
	}

	_, file, line, ok := runtime.Caller(1)
	if !ok {
		panic("assertion failed: " + message)
	}
	panic(fmt.Sprintf("assertion failed at %s:%d: %s", file, line, message))
}

// Tracef writes to stderr, so it does not pollute a program's real output.
func Tracef(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "[trace] "+format+"\n", args...)
}

// debugModeExplanation describes what this build is.
func debugModeExplanation() []string {
	return []string{
		"this binary WAS built with -tags debug",
		"Assert and AssertFunc panic on a false condition, naming the file and line",
		"Tracef writes to stderr",
		"DebugEnabled is a constant true",
		"rebuild without the tag and all of this vanishes from the binary",
	}
}
