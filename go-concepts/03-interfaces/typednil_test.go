package main

import (
	"strings"
	"testing"
)

// TestTypedNilTrap pins the bug in place. If a future Go release ever changed
// interface nil-ness (it will not; this is specified behaviour), this test
// fails and the README's central example needs rewriting.
func TestTypedNilTrap(t *testing.T) {
	t.Run("broken version reports an error that did not happen", func(t *testing.T) {
		err := brokenValidate("Ana") // valid input, no problem found

		if err == nil {
			t.Fatal("expected the typed-nil trap to produce a non-nil error")
		}
		if !isTypedNil(err) {
			t.Error("err should be an interface holding a nil *ValidationError")
		}
	})

	t.Run("fixed version returns an honest nil", func(t *testing.T) {
		if err := fixedValidate("Ana"); err != nil {
			t.Errorf("fixedValidate(\"Ana\") = %v, want nil", err)
		}
	})

	t.Run("fixed version still reports real failures", func(t *testing.T) {
		err := fixedValidate("")
		if err == nil {
			t.Fatal("empty name should be rejected")
		}
		if isTypedNil(err) {
			t.Error("a real error must not be a typed nil")
		}
		if want := `field "name": must not be empty`; err.Error() != want {
			t.Errorf("Error() = %q, want %q", err.Error(), want)
		}
	})

	t.Run("a concrete return type springs the trap at the call site", func(t *testing.T) {
		// The factory itself returns an honest nil pointer...
		ptr := brokenFactory("Ana")
		if ptr != nil {
			t.Error("brokenFactory should return a nil *ValidationError")
		}

		// ...and the implicit conversion to error is what breaks.
		if err := callerOfBrokenFactory("Ana"); err == nil {
			t.Error("expected the boxed nil pointer to compare non-nil as an error")
		}
	})
}

// TestTypedNilPanicsOnMethodCall is the consequence that turns a wrong branch
// into an outage: the caller takes the error path and then uses the error.
func TestTypedNilPanicsOnMethodCall(t *testing.T) {
	err := brokenValidate("Ana")

	defer func() {
		if r := recover(); r == nil {
			t.Error("expected calling Error() on the typed nil to panic")
		}
	}()

	_ = err.Error()
}

func TestIsTypedNil(t *testing.T) {
	var nilPtr *ValidationError
	var nilMap map[string]int
	var nilSlice []int
	var nilChan chan int

	tests := []struct {
		name string
		in   any
		want bool
	}{
		{"untyped nil interface", nil, false},
		{"nil pointer", nilPtr, true},
		{"nil map", nilMap, true},
		{"nil slice", nilSlice, true},
		{"nil channel", nilChan, true},
		{"real pointer", &ValidationError{}, false},
		{"int value", 42, false},
		{"empty string", "", false},
		{"zero struct", ValidationError{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isTypedNil(tt.in); got != tt.want {
				t.Errorf("isTypedNil(%#v) = %t, want %t", tt.in, got, tt.want)
			}
		})
	}
}

// TestDescribeInterface covers the diagnostic that renders an interface's two
// words, which is what makes the typed-nil demo legible.
func TestDescribeInterface(t *testing.T) {
	var nilPtr *ValidationError

	tests := []struct {
		name     string
		in       any
		contains []string
	}{
		{"untyped nil", nil, []string{"type=<nil>", "TRUE"}},
		{"typed nil pointer", nilPtr, []string{"*main.ValidationError", "value=<nil>", "FALSE"}},
		{"real value", &ValidationError{Field: "x"}, []string{"*main.ValidationError", "value=<set>", "FALSE"}},
		{"non-pointer value", 42, []string{"type=int", "value=<set>", "FALSE"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := describeInterface(tt.in)
			for _, want := range tt.contains {
				if !strings.Contains(got, want) {
					t.Errorf("describeInterface(%v) = %q, want it to contain %q", tt.in, got, want)
				}
			}
		})
	}
}
