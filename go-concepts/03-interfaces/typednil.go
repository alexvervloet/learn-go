package main

import (
	"fmt"
	"reflect"
)

// The typed-nil trap
// ==================
//
// An interface value is two words: a type and a value.
//
//	var e error = nil              type: <none>    value: nil    e == nil  TRUE
//	var p *ValidationError = nil
//	var e error = p                type: *ValidationError value: nil    e == nil  FALSE
//
// The second case is the bug. A nil pointer boxed into an interface produces an
// interface that is NOT nil, because the type word is populated. Every
// `if err != nil` downstream takes the failure branch for an error that does
// not exist.

// ValidationError is a perfectly ordinary custom error type.
type ValidationError struct {
	Field  string
	Reason string
}

// Error implements the error interface with a pointer receiver, which is the
// convention for error types with fields.
func (e *ValidationError) Error() string {
	return fmt.Sprintf("field %q: %s", e.Field, e.Reason)
}

// brokenValidate is the bug, written the way it actually appears in real code.
// The author declares a concrete error variable at the top so they can assign
// to it in branches, then returns it. On the success path it is still nil, and
// the return boxes a nil *ValidationError into a non-nil error.
//
// Note the return type is `error`, so the function LOOKS correct at the
// signature. The trap is entirely in the body.
//
// staticcheck catches this one. Running golangci-lint over this file reports
// SA4023: "brokenValidate never returns a nil interface value", and flags every
// `err != nil` comparison downstream as always true. That is the single most
// useful thing in this lesson: the trap is subtle to read and trivial to lint
// for, so put staticcheck in CI and stop thinking about it.
//
//nolint:staticcheck // SA4023 is exactly right; demonstrating the trap is the purpose
func brokenValidate(name string) error {
	var err *ValidationError // concrete type, currently nil

	if name == "" {
		err = &ValidationError{Field: "name", Reason: "must not be empty"}
	}

	// On the success path this returns a nil *ValidationError as an error
	// interface. The caller's err != nil is TRUE.
	return err
}

// fixedValidate is the same logic written so the success path returns the
// untyped nil literal. The type word stays empty and err == nil behaves.
//
// The rule that generalises: return `nil` explicitly, never a concrete-typed
// variable that happens to be nil.
func fixedValidate(name string) error {
	if name == "" {
		return &ValidationError{Field: "name", Reason: "must not be empty"}
	}
	return nil
}

// brokenFactory is the second shape the trap takes, and the more dangerous one,
// because the signature itself is wrong. Any caller that assigns the result to
// an `error` gets a non-nil interface out of a nil pointer.
//
//	err := brokenFactory("ok")   // err is a nil *ValidationError
//	var e error = err            // e is NOT nil
func brokenFactory(name string) *ValidationError {
	if name == "" {
		return &ValidationError{Field: "name", Reason: "must not be empty"}
	}
	return nil
}

// callerOfBrokenFactory is what makes brokenFactory dangerous: the boxing
// happens at the call site, far from the function that returned the nil.
//
//nolint:staticcheck // SA4023: same trap, sprung one call frame away
func callerOfBrokenFactory(name string) error {
	return brokenFactory(name) // implicit conversion to error, trap sprung
}

// isTypedNil reports whether v is a non-nil interface holding a nil pointer.
// This is a diagnostic, not a fix. If you find yourself needing it in
// production code, the real bug is upstream in a function like brokenValidate.
//
// reflect is the only way to see inside an interface value, which is itself a
// useful thing to know: the type/value pair is not reachable from the language,
// only from the reflect package.
func isTypedNil(v any) bool {
	if v == nil {
		return false // an honestly nil interface, not a typed nil
	}

	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Chan, reflect.Func, reflect.Interface:
		return rv.IsNil()
	default:
		// Value kinds cannot be nil, so a non-nil interface holding one is
		// genuinely carrying something.
		return false
	}
}

// describeInterface renders the two words of an interface value so the demo can
// show why one comparison is true and the other false.
func describeInterface(v any) string {
	if v == nil {
		return "type=<nil> value=<nil>  -> v == nil is TRUE"
	}
	t := reflect.TypeOf(v)
	nilValue := isTypedNil(v)
	return fmt.Sprintf("type=%v value=%s  -> v == nil is FALSE",
		t, map[bool]string{true: "<nil>", false: "<set>"}[nilValue])
}

// demoTypedNil prints both shapes of the trap side by side with the fix.
func demoTypedNil() {
	broken := brokenValidate("Ana") //nolint:staticcheck // SA4023 anchors here; the trap is the point
	//nolint:staticcheck // SA4023: "always true" is the finding being demonstrated
	fmt.Printf("  brokenValidate(\"Ana\")  err != nil: %-5t   %s\n", broken != nil, describeInterface(broken))

	fixed := fixedValidate("Ana")
	fmt.Printf("  fixedValidate(\"Ana\")   err != nil: %-5t   %s\n", fixed != nil, describeInterface(fixed))

	real := fixedValidate("")
	fmt.Printf("  fixedValidate(\"\")      err != nil: %-5t   %v\n", real != nil, real)

	viaFactory := callerOfBrokenFactory("Ana") //nolint:staticcheck // SA4023 anchors here
	//nolint:staticcheck // SA4023: always true, which is the bug
	fmt.Printf("  callerOfBrokenFactory  err != nil: %-5t   (boxed at the call site)\n", viaFactory != nil)

	fmt.Printf("  isTypedNil(broken)=%t  isTypedNil(fixed)=%t  isTypedNil(real)=%t\n",
		isTypedNil(broken), isTypedNil(fixed), isTypedNil(real))

	// The payoff: calling a method on the typed nil. ValidationError.Error
	// dereferences e, so this panics. Recovered here to keep the demo alive.
	func() {
		defer func() {
			if r := recover(); r != nil {
				fmt.Printf("  calling broken.Error(): panic: %v\n", r)
			}
		}()
		_ = broken.Error()
	}()
}
