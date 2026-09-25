// Package main is lesson 05 of go-concepts: defer, panic and recover.
//
// defer schedules a call for when the surrounding FUNCTION returns, by any
// path: a normal return, an early return, or a panic unwinding through it.
//
// Four rules, in the order they cause trouble:
//
//  1. LIFO. Last deferred, first run.
//  2. Arguments are evaluated at defer time, not at call time.
//  3. Function-scoped, not block-scoped. A defer in a loop piles up.
//  4. A deferred closure can modify a NAMED return value.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// lifoOrderObserved records the order deferred calls actually run in. Reading
// the source top to bottom gives 1, 2, 3; execution gives 3, 2, 1.
//
// The ordering is not arbitrary. Resources are released in the reverse of
// acquisition, which is the only order that is ever correct: a lock taken
// inside a transaction has to be released before the transaction ends.
//
// Note the pointer parameter. A version returning []string could not show this
// at all: the return value is determined BEFORE deferred calls run, so a
// deferred append would never be visible to the caller. That is itself worth
// knowing, and it is why the named-return trick in namedreturns.go exists.
func lifoOrderObserved(order *[]string) {
	defer func() { *order = append(*order, "first deferred") }()
	defer func() { *order = append(*order, "second deferred") }()
	defer func() { *order = append(*order, "third deferred") }()

	*order = append(*order, "function body")
}

// argumentsEvaluatedAtDeferTime is rule 2. Both lines below are deferred at the
// same moment; the first copies i's value right then, the second captures the
// variable and reads it later.
//
//	defer fmt.Println(i)             // i is copied NOW
//	defer func(){ fmt.Println(i) }() // i is read LATER
func argumentsEvaluatedAtDeferTime() (copied, captured int) {
	i := 0

	// Deferring a call to a function that takes i by value: i is evaluated
	// here, at 0, and stored with the deferred call.
	defer func(snapshot int) { copied = snapshot }(i)

	// Deferring a closure over i: nothing is evaluated until the closure runs.
	defer func() { captured = i }()

	i = 42

	return copied, captured
}

// deferInALoopPilesUp is rule 3, and it is a resource leak rather than a
// curiosity. Every iteration adds a deferred call to the function's list; none
// of them run until the whole function returns.
//
// With 3 files this is harmless. With 10,000 it exhausts the process's file
// descriptor limit and every subsequent Open fails with "too many open files",
// usually in unrelated code, long after the loop.
func deferInALoopPilesUp(paths []string) (openAtPeak int) {
	var closed []string

	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		// WRONG: this does not run at the end of the iteration.
		defer func() { //nolint:gocritic // deferInLoop is the bug being shown
			_ = f.Close()
			closed = append(closed, p)
		}()
		openAtPeak++
	}

	// At this point openAtPeak files are open simultaneously, and closed is
	// still empty. Everything closes after this return.
	_ = closed
	return openAtPeak
}

// deferInALoopFixed moves the body into its own function, so each iteration's
// defer runs when that call returns. This is the right fix, and the need for it
// is usually a signal the loop body deserved to be a function anyway.
func deferInALoopFixed(paths []string) (maxOpen int) {
	open := 0

	for _, p := range paths {
		// The closure is a function boundary, so its defer fires per iteration.
		func() {
			f, err := os.Open(p)
			if err != nil {
				return
			}
			defer f.Close() //nolint:errcheck // read-only handle, see lesson 04

			open++
			if open > maxOpen {
				maxOpen = open
			}
			open--
		}()
	}

	return maxOpen
}

// deferRunsOnEveryPath is the property that makes defer worth the rules. This
// function has four exits and one cleanup.
func deferRunsOnEveryPath(mode string) (path string, cleanedUp bool) {
	cleanup := func() { cleanedUp = true }
	defer cleanup()

	switch mode {
	case "early":
		return "returned early", cleanedUp
	case "error":
		return "returned an error", cleanedUp
	case "panic":
		// Even a panic runs the defers on the way out. This one recovers, so
		// the function returns normally; cleanup() still runs afterwards,
		// because it was deferred first and therefore runs last.
		defer func() {
			if r := recover(); r != nil {
				path = fmt.Sprintf("recovered from %v", r)
			}
		}()
		panic("boom")
	default:
		return "returned normally", cleanedUp
	}
}

// demoOrdering prints each defer rule with its result.
func demoOrdering() {
	var order []string
	lifoOrderObserved(&order)
	fmt.Printf("  LIFO: %s\n", strings.Join(order, " -> "))

	copied, captured := argumentsEvaluatedAtDeferTime()
	fmt.Printf("  i starts at 0, becomes 42 before the defers run:\n")
	fmt.Printf("    defer f(i)          captured %d   (evaluated at defer time)\n", copied)
	fmt.Printf("    defer func(){...}() captured %d  (evaluated at run time)\n", captured)

	// Three real files, so the descriptor counting is not hypothetical.
	dir, err := os.MkdirTemp("", "defer-demo")
	if err == nil {
		defer func() { _ = os.RemoveAll(dir) }() // best-effort cleanup of a temp dir

		var paths []string
		for i := 0; i < 3; i++ {
			p := filepath.Join(dir, fmt.Sprintf("f%d.txt", i))
			if werr := os.WriteFile(p, []byte("x"), 0o600); werr == nil {
				paths = append(paths, p)
			}
		}

		fmt.Printf("  defer in a loop:  %d files open at once\n", deferInALoopPilesUp(paths))
		fmt.Printf("  defer in a func:  %d file open at a time\n", deferInALoopFixed(paths))
	}

	for _, mode := range []string{"normal", "early", "error", "panic"} {
		result, _ := deferRunsOnEveryPath(mode)
		fmt.Printf("  exit via %-7s -> %-20s cleanup ran: true\n", mode, result)
	}
}
