package main

import (
	"errors"
	"fmt"
	"strings"
)

// Named returns and defer
// =======================
//
// A deferred closure runs AFTER the return value has been set but BEFORE the
// caller sees it. With a named return, the closure can still change it:
//
//	func f() (err error) {
//	    defer func() { err = errors.New("replaced") }()
//	    return nil            // caller receives "replaced"
//	}
//
// Without the name there is nothing to assign to, and the assignment is either
// a compile error or, worse, a write to a local that nobody reads.
//
// Two patterns depend on this, and they are the only two that justify a named
// return in most codebases: converting a panic to an error, and reporting a
// Close failure.

// canModifyNamedReturn is the mechanic, isolated. `return "original"` sets
// result, then the defer overwrites it.
func canModifyNamedReturn() (result string) {
	defer func() { result = strings.ToUpper(result) }()
	return "original"
}

// cannotModifyUnnamedReturn is the same shape without the name. The closure
// assigns to a local that the caller never sees, so the change is lost.
func cannotModifyUnnamedReturn() string {
	result := "original"
	defer func() { result = strings.ToUpper(result) }() //nolint:wastedassign // the wasted write is the lesson
	return result
}

// panicToError is the first real use. A function that can panic internally
// presents an error interface to its callers.
//
// Every piece of this is load-bearing:
//
//	(err error)              the name, so the defer has a target
//	defer func() {...}()     a closure, because recover must be called directly
//	                         inside a deferred function of THIS frame
//	if r := recover()        the comma form, because recover returns nil when
//	                         there is no panic and this defer runs either way
func panicToError(fn func()) (err error) {
	defer func() {
		r := recover()
		if r == nil {
			return // no panic; leave err as whatever fn's path set
		}

		// A panic value is `any`. Preserve an error as an error so callers can
		// still use errors.Is and errors.As on it; render anything else.
		if recoveredErr, ok := r.(error); ok {
			err = fmt.Errorf("recovered: %w", recoveredErr)
			return
		}
		err = fmt.Errorf("recovered: %v", r)
	}()

	fn()
	return nil
}

// panicToErrorBroken is the same function with the name removed, which is how
// people actually write it the first time. It compiles. It recovers. And the
// error never reaches the caller, so a panic becomes a silent success.
//
// This is strictly worse than not recovering at all.
func panicToErrorBroken(fn func()) error {
	var err error

	defer func() {
		if r := recover(); r != nil {
			// Assigning to a local. The return statement below already ran.
			err = fmt.Errorf("recovered: %v", r) //nolint:wastedassign // the lost assignment is the bug
		}
	}()

	fn()
	return err
}

// resource is a stand-in for anything whose Close can fail: a file being
// written, a gzip writer, a database transaction, a network connection.
type resource struct {
	name       string
	closeErr   error
	writeErr   error
	closeCalls int
	written    []string
}

func (r *resource) Write(s string) error {
	if r.writeErr != nil {
		return r.writeErr
	}
	r.written = append(r.written, s)
	return nil
}

// Close reports a failure that only shows up at close time, which is the whole
// reason this pattern exists. Buffered data is flushed here.
func (r *resource) Close() error {
	r.closeCalls++
	return r.closeErr
}

// processWithClose is the second real use of a named return, and the pattern
// every Go codebase eventually needs.
//
// The rule it implements: when both the body and Close fail, the BODY's error
// is the more informative one and must not be overwritten. When only Close
// fails, that is the news and must not be dropped.
func processWithClose(r *resource, lines []string) (err error) {
	defer func() {
		cerr := r.Close()
		if cerr == nil {
			return
		}
		if err == nil {
			err = fmt.Errorf("close %s: %w", r.name, cerr)
			return
		}
		// Both failed. Join keeps both, and errors.Is finds either.
		err = errors.Join(err, fmt.Errorf("close %s: %w", r.name, cerr))
	}()

	for i, line := range lines {
		if werr := r.Write(line); werr != nil {
			return fmt.Errorf("write line %d to %s: %w", i, r.name, werr)
		}
	}
	return nil
}

// processDiscardingClose is the version almost everyone writes. It loses the
// Close error entirely, so a full disk looks like a successful write.
func processDiscardingClose(r *resource, lines []string) error {
	defer r.Close() //nolint:errcheck // discarding the close error is the bug being shown

	for i, line := range lines {
		if err := r.Write(line); err != nil {
			return fmt.Errorf("write line %d to %s: %w", i, r.name, err)
		}
	}
	return nil
}

// demoNamedReturns prints the named-return mechanic and both patterns.
func demoNamedReturns() {
	fmt.Printf("  named return, modified by defer:   %q\n", canModifyNamedReturn())
	fmt.Printf("  unnamed return, defer has no target: %q\n", cannotModifyUnnamedReturn())

	boom := func() { panic("something went wrong") }
	fmt.Printf("\n  panicToError(panicking fn)       -> %v\n", panicToError(boom))
	fmt.Printf("  panicToErrorBroken(panicking fn) -> %v   <- the panic vanished\n", panicToErrorBroken(boom))

	// A panic carrying an error stays an error, so errors.Is still works.
	sentinel := errors.New("the underlying cause")
	err := panicToError(func() { panic(sentinel) })
	fmt.Printf("  panic(error) recovered: %v   errors.Is finds it: %t\n", err, errors.Is(err, sentinel))

	fmt.Println()
	diskFull := errors.New("no space left on device")

	r1 := &resource{name: "report.txt", closeErr: diskFull}
	fmt.Printf("  writes ok, close fails:\n")
	fmt.Printf("    with the pattern: %v\n", processWithClose(r1, []string{"a", "b"}))

	r2 := &resource{name: "report.txt", closeErr: diskFull}
	fmt.Printf("    discarding close: %v   <- data loss, reported as success\n", processDiscardingClose(r2, []string{"a", "b"}))

	r3 := &resource{name: "report.txt", closeErr: diskFull, writeErr: errors.New("broken pipe")}
	fmt.Printf("  both fail:\n    %v\n", strings.ReplaceAll(processWithClose(r3, []string{"a"}).Error(), "\n", "\n    "))
}
