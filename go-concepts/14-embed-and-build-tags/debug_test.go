package main

import (
	"strings"
	"testing"
)

// TestDebugFlagMatchesTheBuild: exactly one of debug_on.go and debug_off.go is
// compiled, and DebugEnabled says which.
//
// Both halves are tested, in different runs:
//
//	go test ./14-embed-and-build-tags              -> the production half
//	go test -tags debug ./14-embed-and-build-tags  -> the debug half
//
// That is the discipline build tags require: a file behind a tag is not
// compiled by default, so it is not type checked, not vetted and not covered
// unless something builds it. CI runs both.
func TestDebugFlagMatchesTheBuild(t *testing.T) {
	explanation := debugModeExplanation()

	if len(explanation) < 3 {
		t.Fatalf("expected an explanation, got %v", explanation)
	}

	first := explanation[0]
	if DebugEnabled {
		if !strings.Contains(first, "WAS built with") {
			t.Errorf("DebugEnabled is true but the explanation says %q", first)
		}
	} else {
		if !strings.Contains(first, "WITHOUT") {
			t.Errorf("DebugEnabled is false but the explanation says %q", first)
		}
	}
}

// TestAssertPassesOnATrueCondition must hold in BOTH builds: in production
// Assert does nothing, and in debug it checks and passes.
func TestAssertPassesOnATrueCondition(t *testing.T) {
	Assert(true, "this should never fire")
	AssertFunc(func() bool { return true }, "nor this")
	Tracef("a trace line from %s", t.Name())
}

// TestAssertBehaviourDependsOnTheBuild is the interesting half: a FALSE
// condition panics in a debug build and does nothing in production.
func TestAssertBehaviourDependsOnTheBuild(t *testing.T) {
	panicked := func() (msg string) {
		defer func() {
			if r := recover(); r != nil {
				msg = strings.Join([]string{"panic:", toString(r)}, " ")
			}
		}()
		Assert(false, "deliberately false")
		return ""
	}()

	if DebugEnabled {
		if panicked == "" {
			t.Error("a debug build should panic on a false assertion")
		}
		if !strings.Contains(panicked, "deliberately false") {
			t.Errorf("panic = %q, want it to carry the message", panicked)
		}
		if !strings.Contains(panicked, "debug_test.go") {
			t.Errorf("panic = %q, want it to name the caller's file", panicked)
		}
		return
	}

	if panicked != "" {
		t.Errorf("a production build should not panic, got %q", panicked)
	}
}

// TestAssertFuncDoesNotEvaluateInProduction is the reason AssertFunc exists
// alongside Assert: a plain bool argument is still computed before the call,
// however empty the function is.
func TestAssertFuncDoesNotEvaluateInProduction(t *testing.T) {
	evaluated := false

	func() {
		defer func() { _ = recover() }()
		AssertFunc(func() bool {
			evaluated = true
			return true
		}, "checked")
	}()

	if DebugEnabled && !evaluated {
		t.Error("a debug build should run the check")
	}
	if !DebugEnabled && evaluated {
		t.Error("a production build should NOT run the check — that is the point of the closure")
	}
}

func toString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return strings.TrimSpace(strings.ReplaceAll(strings.TrimSpace(sprint(v)), "\n", " "))
}
