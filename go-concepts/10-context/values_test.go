package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestValuesAreImmutableAndLayered(t *testing.T) {
	parentHas, childHas, parentValue, childValue := valuesAreImmutableAndLayered()

	if !parentHas || !childHas {
		t.Fatal("both contexts should hold a request id")
	}
	if parentValue != "parent-id" {
		t.Errorf("parent value = %q, want %q — WithValue must not mutate the parent", parentValue, "parent-id")
	}
	if childValue != "child-id" {
		t.Errorf("child value = %q, want %q", childValue, "child-id")
	}
}

func TestTypedKeysCannotCollide(t *testing.T) {
	requestID, user, trace := typedKeysCannotCollide()

	if requestID != "req-123" {
		t.Errorf("request id = %q, want req-123", requestID)
	}
	if user == nil || user.Email != "ana@example.com" {
		t.Errorf("user = %+v, want ana@example.com", user)
	}
	if trace != "trace-abc" {
		t.Errorf("trace = %q, want trace-abc", trace)
	}
}

// TestStringKeysCollide asserts the bug the typed-key idiom prevents.
func TestStringKeysCollide(t *testing.T) {
	a, b := stringKeysCollide()

	if a != b {
		t.Error("with a shared string key, both packages read the same value")
	}
	if a != "package-b-value" {
		t.Errorf("read %q, want package-b-value — A's value should be shadowed", a)
	}
}

// TestGettersReportAbsence: the ok result is the only way to distinguish
// "absent" from "present and empty", exactly as with a map.
func TestGettersReportAbsence(t *testing.T) {
	empty := context.Background()

	if _, ok := RequestID(empty); ok {
		t.Error("RequestID on an empty context should report false")
	}
	if _, ok := CurrentUser(empty); ok {
		t.Error("CurrentUser on an empty context should report false")
	}
	if _, ok := Trace(empty); ok {
		t.Error("Trace on an empty context should report false")
	}

	// Present but empty is a different answer.
	withEmpty := WithRequestID(empty, "")
	v, ok := RequestID(withEmpty)
	if !ok {
		t.Error("a stored empty string should still report ok")
	}
	if v != "" {
		t.Errorf("value = %q, want empty", v)
	}
}

// TestWrongTypeIsNotAPanic: the getters use the comma-ok assertion, so a value
// of the wrong type reports absent rather than panicking. That matters, because
// the key is only as private as the package.
func TestWrongTypeReportsAbsent(t *testing.T) {
	ctx := context.WithValue(context.Background(), requestIDKey{}, 42) // an int, not a string

	if _, ok := RequestID(ctx); ok {
		t.Error("a value of the wrong type should report absent, not succeed")
	}
}

func TestLookupIsLinear(t *testing.T) {
	found, missing := lookupIsLinear(50)

	if !found {
		t.Error("the stored key should be found past 50 filler layers")
	}
	if missing {
		t.Error("a key that was never stored should not be found")
	}
}

func TestLogWithContext(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ctx = WithRequestID(ctx, "req-1")
	ctx = WithTrace(ctx, "trace-1")
	ctx = WithUser(ctx, &User{ID: 7, Email: "ana@example.com"})

	line := logWithContext(ctx, "handling")

	for _, want := range []string{"req=req-1", "trace=trace-1", "user=ana@example.com", "handling"} {
		if !strings.Contains(line, want) {
			t.Errorf("log line %q should contain %q", line, want)
		}
	}
}

func TestLogWithContextOnAnEmptyContext(t *testing.T) {
	line := logWithContext(context.Background(), "no values")

	if !strings.Contains(line, "user=anonymous") {
		t.Errorf("line %q should fall back to anonymous", line)
	}
	if !strings.Contains(line, "budget=none") {
		t.Errorf("line %q should report no deadline", line)
	}
}

func TestContextGuidanceIsDocumented(t *testing.T) {
	if got := whatBelongs(); len(got) < 3 {
		t.Errorf("expected at least 3 things that belong, got %d", len(got))
	}
	if got := whatDoesNotBelongInAContext(); len(got) < 4 {
		t.Errorf("expected at least 4 things that do not, got %d", len(got))
	}
}
