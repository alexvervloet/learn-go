package main

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Custom error types
// ==================
//
// Reach for a type when the caller needs DATA about the failure, not just its
// identity. A sentinel answers "what kind of failure?". A type answers "which
// field, which line, how long should I wait?".
//
// Retrieve one with errors.As, which walks the chain and assigns the first
// match:
//
//	var verr *ValidationError
//	if errors.As(err, &verr) { use verr.Field }
//
// The argument is a POINTER to the target. Passing the target itself is the
// mistake everyone makes once; errors.As panics on it rather than failing
// quietly, which is the right call.

// Field names, as constants. Three places construct ValidationErrors for the
// same fields, and a typo in one of them would produce an error naming a field
// the caller's switch does not handle. Constants make that a compile error.
//
// Note that fieldEmail and ruleEmail hold the same text and are NOT the same
// thing: one names an input, the other names a validation rule. Collapsing them
// into one constant because the strings match today is how coupling starts.
const (
	fieldName  = "name"
	fieldEmail = "email"
	fieldAge   = "age"

	ruleRequired = "required"
	ruleEmail    = "email"
	ruleAgeRange = "range(0,150)"
)

// ValidationError says which field failed and why. The pointer receiver on
// Error is the convention for error types with fields; see lesson 03 for the
// typed-nil hazard that comes with it.
type ValidationError struct {
	Field string
	Value any
	Rule  string
}

// Error implements error.
func (e *ValidationError) Error() string {
	return fmt.Sprintf("field %q: %v fails rule %q", e.Field, e.Value, e.Rule)
}

// RetryableError wraps another error and adds how long to wait. Implementing
// Unwrap is what keeps the wrapped error findable through this layer: without
// it, errors.Is and errors.As stop here.
type RetryableError struct {
	After time.Duration
	Err   error
}

// Error implements error.
func (e *RetryableError) Error() string {
	return fmt.Sprintf("retry after %v: %v", e.After, e.Err)
}

// Unwrap makes this type transparent to errors.Is and errors.As. Any error type
// that holds another one must implement it, or it becomes a wall in the chain.
func (e *RetryableError) Unwrap() error { return e.Err }

// HTTPError carries a status code, which is the classic case for a type over a
// sentinel: there are 40-odd statuses and nobody wants 40 sentinels.
type HTTPError struct {
	Status int
	URL    string
	Err    error
}

// Error implements error.
func (e *HTTPError) Error() string {
	return fmt.Sprintf("GET %s: unexpected status %d", e.URL, e.Status)
}

// Unwrap keeps any underlying cause reachable.
func (e *HTTPError) Unwrap() error { return e.Err }

// Is lets a type participate in errors.Is on its own terms. Here it declares
// that any 5xx HTTPError matches ErrConflict's cousin: a caller can ask
// "is this retryable?" without knowing the status codes.
//
// Implementing Is is uncommon and powerful. Use it when a type has a natural
// notion of "counts as" that a plain chain walk cannot express.
func (e *HTTPError) Is(target error) bool {
	return target == ErrServerFault && e.Status >= 500
}

// ErrServerFault is the abstract condition HTTPError.Is answers to.
var ErrServerFault = errors.New("server fault")

// validateUser returns a *ValidationError wrapped in context, which is the
// normal shape: the type carries data, the wrapper carries location.
func validateUser(name, email string, age int) error {
	switch {
	case name == "":
		return fmt.Errorf("validate user: %w", &ValidationError{Field: fieldName, Value: name, Rule: ruleRequired})
	case !strings.Contains(email, "@"):
		return fmt.Errorf("validate user: %w", &ValidationError{Field: fieldEmail, Value: email, Rule: ruleEmail})
	case age < 0 || age > 150:
		return fmt.Errorf("validate user: %w", &ValidationError{Field: fieldAge, Value: age, Rule: ruleAgeRange})
	default:
		return nil
	}
}

// fieldFromError is the payoff for using a type: the caller reads the field
// name out of the error and can highlight the right input box.
func fieldFromError(err error) (field string, ok bool) {
	var verr *ValidationError
	if errors.As(err, &verr) {
		return verr.Field, true
	}
	return "", false
}

// retryDelayFrom digs a RetryableError out of a chain and reads its delay.
// Returns 0 and false when the error is not retryable, which is the signal to
// give up rather than loop.
func retryDelayFrom(err error) (time.Duration, bool) {
	var rerr *RetryableError
	if errors.As(err, &rerr) {
		return rerr.After, true
	}
	return 0, false
}

// deeplyWrapped builds a chain with a custom type at the bottom, a retryable in
// the middle, and plain context on top, to show errors.As reaching through all
// of it.
func deeplyWrapped() error {
	base := &ValidationError{Field: fieldEmail, Value: "nope", Rule: ruleEmail}
	retryable := &RetryableError{After: 2 * time.Second, Err: base}
	return fmt.Errorf("process signup: %w", retryable)
}

// demoCustom prints errors.As reaching through several layers.
func demoCustom() {
	for _, c := range []struct {
		name, email string
		age         int
	}{
		{"Ana", "ana@example.com", 30},
		{"", "ana@example.com", 30},
		{"Ana", "nope", 30},
		{"Ana", "ana@example.com", 200},
	} {
		err := validateUser(c.name, c.email, c.age)
		if err == nil {
			fmt.Printf("  validateUser(%q, %q, %d) -> ok\n", c.name, c.email, c.age)
			continue
		}
		field, _ := fieldFromError(err)
		fmt.Printf("  validateUser(%q, %q, %d) -> %v   [field=%s]\n", c.name, c.email, c.age, err, field)
	}

	deep := deeplyWrapped()
	fmt.Printf("\n  three layers: %v\n", deep)
	field, _ := fieldFromError(deep)
	delay, retryable := retryDelayFrom(deep)
	fmt.Printf("    errors.As found the ValidationError: field=%s\n", field)
	fmt.Printf("    errors.As found the RetryableError:  after=%v, retryable=%t\n", delay, retryable)

	// A custom Is method lets one check cover a whole family.
	serverErr := &HTTPError{Status: 503, URL: "https://api.example.com/users"}
	clientErr := &HTTPError{Status: 404, URL: "https://api.example.com/users"}
	fmt.Printf("\n  503 Is ErrServerFault: %t\n", errors.Is(serverErr, ErrServerFault))
	fmt.Printf("  404 Is ErrServerFault: %t\n", errors.Is(clientErr, ErrServerFault))
}
