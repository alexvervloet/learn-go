// Package main is lesson 04 of go-concepts: errors.
//
// error is an interface with one method:
//
//	type error interface { Error() string }
//
// That is the whole mechanism. Errors are ordinary values, which is why they
// can be compared, stored, wrapped, joined and returned like anything else.
//
// The four constructors, in the order you should reach for them:
//
//	errors.New("...")              a fixed message
//	fmt.Errorf("doing x: %w", err) wrap, keeping the original findable
//	var ErrX = errors.New("...")   a sentinel callers branch on
//	type XError struct{...}        a type carrying data about the failure
package main

import (
	"errors"
	"fmt"
	"strings"
)

// newVsErrorf shows the two basic constructors. errors.New is for a message
// with nothing variable in it; fmt.Errorf is for anything else.
func newVsErrorf(path string) (fixed, formatted error) {
	fixed = errors.New("configuration is missing")
	formatted = fmt.Errorf("read config %q: file does not exist", path)
	return fixed, formatted
}

// wrapWithPercentW is the default way to add context. The %w verb records the
// wrapped error inside the new one, so errors.Is and errors.As can still find
// it however many layers later.
//
// Exactly one %w per Errorf call was the original rule; Go 1.20 allows several,
// and errors.Is searches all of them.
func wrapWithPercentW(cause error) error {
	return fmt.Errorf("load user profile: %w", cause)
}

// wrapWithPercentV is the mistake. It renders the cause into the message text
// and throws the value away. The string looks identical to the %w version, so
// nothing about the output reveals the problem. What breaks is every
// errors.Is check downstream, silently.
//
// The good news, and the practical takeaway: errorlint catches this. Running
// golangci-lint over this file reports "non-wrapping format verb for
// fmt.Errorf. Use %w to format errors". The suppression below exists only so
// the demo can keep being wrong.
func wrapWithPercentV(cause error) error {
	return fmt.Errorf("load user profile: %v", cause) //nolint:errorlint // %v instead of %w is the bug being shown
}

// unwrapReachesTheCause: %w makes Unwrap return the original error. %v leaves
// nothing to unwrap, so Unwrap returns nil.
func unwrapReachesTheCause(wrapped error) error {
	return errors.Unwrap(wrapped)
}

// layeredContext builds the three-layer chain from the README, so the message
// convention has something concrete to apply to.
//
//	open config: read /etc/app.toml: permission denied
//	└─ handler   └─ loader           └─ syscall
func layeredContext() error {
	// Deepest layer: what actually went wrong. Usually from the stdlib or a
	// driver, and not yours to word.
	syscallErr := errors.New("permission denied")

	// Middle layer: names the operation and its subject.
	loaderErr := fmt.Errorf("read /etc/app.toml: %w", syscallErr)

	// Top layer: names the higher-level operation.
	return fmt.Errorf("open config: %w", loaderErr)
}

// chainOf renders every link in an error chain, deepest last. Useful for
// understanding what a wrapped error actually contains, and for the demo below.
func chainOf(err error) []string {
	var out []string
	for err != nil {
		out = append(out, err.Error())
		err = errors.Unwrap(err)
	}
	return out
}

// Message convention
// ==================
//
// Error strings get concatenated by wrapping, so they must compose. The three
// rules and what breaks without them:
//
//	BAD:  "Failed to open config file."
//	      capital + trailing period + "failed to"
//	      wraps into: "load settings: Failed to open config file.: no such file"
//
//	GOOD: "open config"
//	      wraps into: "load settings: open config: no such file or directory"
//
// badMessage and goodMessage exist so the test can compare the two.
func badMessage(cause error) error {
	return fmt.Errorf("Failed to open the configuration file.: %w", cause) //nolint:staticcheck // ST1005: the bad style is the demonstration
}

func goodMessage(cause error) error {
	return fmt.Errorf("open config: %w", cause)
}

// demoCreating prints the constructors and the wrapping difference.
func demoCreating() {
	fixed, formatted := newVsErrorf("/etc/app.toml")
	fmt.Printf("  errors.New:  %v\n", fixed)
	fmt.Printf("  fmt.Errorf:  %v\n", formatted)

	cause := errors.New("connection refused")
	withW := wrapWithPercentW(cause)
	withV := wrapWithPercentV(cause)

	fmt.Printf("\n  %%w -> %v\n", withW)
	fmt.Printf("  %%v -> %v\n", withV)
	fmt.Println("  ...identical text. The difference is invisible in the message:")
	fmt.Printf("    errors.Unwrap(%%w version) = %v\n", unwrapReachesTheCause(withW))
	fmt.Printf("    errors.Unwrap(%%v version) = %v\n", unwrapReachesTheCause(withV))
	fmt.Printf("    errors.Is(%%w version, cause) = %t\n", errors.Is(withW, cause))
	fmt.Printf("    errors.Is(%%v version, cause) = %t   <- the bug\n", errors.Is(withV, cause))

	fmt.Println("\n  three layers of context:")
	fmt.Printf("    %v\n", layeredContext())
	for i, link := range chainOf(layeredContext()) {
		fmt.Printf("      %s%s\n", strings.Repeat("  ", i), link)
	}

	notFound := errors.New("no such file or directory")
	fmt.Printf("\n  bad style:  %v\n", badMessage(notFound))
	fmt.Printf("  good style: %v\n", goodMessage(notFound))
}
