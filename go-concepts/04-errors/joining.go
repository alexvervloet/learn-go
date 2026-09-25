package main

import (
	"errors"
	"fmt"
	"strings"
)

// errors.Join
// ===========
//
// Go 1.20 added errors.Join, which combines several errors into one whose
// Error() is each message on its own line, and which errors.Is and errors.As
// search across ALL of them.
//
//	err := errors.Join(err1, err2, err3)
//	errors.Is(err, ErrNotFound)   // true if any of the three is
//
// Nil arguments are skipped, and joining nothing but nils returns nil. That
// property is what makes the accumulate-then-return pattern below clean: no
// length check, no explicit nil branch.

// validateAll checks every field and reports every failure, instead of stopping
// at the first. A form with four bad fields should tell the user all four, not
// send them round the loop four times.
func validateAll(name, email string, age int) error {
	var errs []error

	if name == "" {
		errs = append(errs, &ValidationError{Field: fieldName, Value: name, Rule: ruleRequired})
	}
	if !strings.Contains(email, "@") {
		errs = append(errs, &ValidationError{Field: fieldEmail, Value: email, Rule: ruleEmail})
	}
	if age < 0 || age > 150 {
		errs = append(errs, &ValidationError{Field: fieldAge, Value: age, Rule: ruleAgeRange})
	}

	// Join of an empty slice is nil, so the happy path needs no special case.
	return errors.Join(errs...)
}

// allFieldsFrom pulls every ValidationError out of a joined error. errors.As
// stops at the FIRST match, so it cannot enumerate. Walking the tree is the
// only way, and the shape of the walk is worth seeing:
//
//   - an error may implement Unwrap() error        (a single wrapper)
//   - or Unwrap() []error                          (a join)
//
// errors.Is and errors.As handle both. Code that wants every match has to.
func allFieldsFrom(err error) []string {
	var fields []string

	var walk func(error)
	walk = func(e error) {
		if e == nil {
			return
		}

		// A direct type assertion, not errors.As, and deliberately so: As
		// searches the whole subtree and would report the same nested error
		// once per ancestor. Here each node is tested exactly once, and the
		// recursion reaches the rest.
		//
		//nolint:errorlint // the walk needs this node only, not its subtree
		if verr, ok := e.(*ValidationError); ok {
			fields = append(fields, verr.Field)
		}

		// Two unwrap shapes exist. fmt.Errorf with one %w gives the first;
		// errors.Join and fmt.Errorf with several %w give the second.
		switch x := e.(type) { //nolint:errorlint // inspecting the chain's structure, not matching a target
		case interface{ Unwrap() error }:
			walk(x.Unwrap())
		case interface{ Unwrap() []error }:
			for _, sub := range x.Unwrap() {
				walk(sub)
			}
		}
	}
	walk(err)

	return fields
}

// joinSkipsNils demonstrates the property that makes the pattern above work.
func joinSkipsNils() (count int, allNil, someNil error) {
	allNil = errors.Join(nil, nil, nil)

	someNil = errors.Join(nil, ErrNotFound, nil, ErrPermission)

	if u, ok := someNil.(interface{ Unwrap() []error }); ok { //nolint:errorlint // reading the join's shape
		count = len(u.Unwrap())
	}
	return count, allNil, someNil
}

// isSearchesEveryBranch: a joined error matches any sentinel inside it.
func isSearchesEveryBranch() (findsFirst, findsSecond, findsAbsent bool) {
	err := errors.Join(
		fmt.Errorf("step one: %w", ErrNotFound),
		fmt.Errorf("step two: %w", ErrPermission),
	)

	return errors.Is(err, ErrNotFound), errors.Is(err, ErrPermission), errors.Is(err, ErrConflict)
}

// demoJoining prints multi-error validation.
func demoJoining() {
	err := validateAll("", "nope", 200)
	fmt.Println("  validateAll(\"\", \"nope\", 200):")
	for _, line := range strings.Split(err.Error(), "\n") {
		fmt.Printf("    %s\n", line)
	}
	fmt.Printf("  fields that failed: %v\n", allFieldsFrom(err))

	if ok := validateAll("Ana", "ana@example.com", 30); ok == nil {
		fmt.Println("  validateAll(valid input) -> nil, with no length check needed")
	}

	count, allNil, someNil := joinSkipsNils()
	fmt.Printf("  Join(nil, nil, nil) = %v\n", allNil)
	fmt.Printf("  Join(nil, a, nil, b) holds %d errors\n", count)
	_ = someNil

	first, second, absent := isSearchesEveryBranch()
	fmt.Printf("  Is over a join: ErrNotFound=%t ErrPermission=%t ErrConflict=%t\n", first, second, absent)
}
