//go:build demo_failure

// This file is behind a build tag so it never runs in the suite. Its whole purpose is to be run
// deliberately, to see what t.Helper() changes about a failure message:
//
//	go test -tags demo_failure -run Deliberate ./basics
//
// The two failures are identical except for one line in the helper. Compare the file:line each
// one reports.
package basics

import "testing"

// withHelper marks itself as a helper, so a failure inside it is attributed to its caller.
func withHelper(t *testing.T, cart *Cart) {
	t.Helper()

	if err := cart.Add("caviar", 1); err != nil {
		t.Fatalf("Add failed: %v", err)
	}
}

// withoutHelper does not, so a failure inside it is attributed to this file and line. Every
// failure routed through it reports the same location, whichever test called it.
func withoutHelper(t *testing.T, cart *Cart) {
	if err := cart.Add("caviar", 1); err != nil {
		t.Fatalf("Add failed: %v", err)
	}
}

func TestDeliberateFailureWithHelper(t *testing.T) {
	withHelper(t, newCart(t)) // the failure points HERE
}

func TestDeliberateFailureWithoutHelper(t *testing.T) {
	withoutHelper(t, newCart(t)) // the failure points inside withoutHelper
}
