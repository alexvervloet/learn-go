package main

import (
	"errors"
	"strconv"
	"testing"
	"time"
)

func TestValidateUser(t *testing.T) {
	tests := []struct {
		name      string
		userName  string
		email     string
		age       int
		wantField string // "" means no error expected
	}{
		{"all valid", "Ana", "ana@example.com", 30, ""},
		{"missing name", "", "ana@example.com", 30, fieldName},
		{"bad email", "Ana", "nope", 30, fieldEmail},
		{"age too high", "Ana", "ana@example.com", 200, fieldAge},
		{"age negative", "Ana", "ana@example.com", -1, fieldAge},
		{"boundary age 0 is valid", "Ana", "ana@example.com", 0, ""},
		{"boundary age 150 is valid", "Ana", "ana@example.com", 150, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateUser(tt.userName, tt.email, tt.age)

			if tt.wantField == "" {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
				return
			}

			if err == nil {
				t.Fatalf("expected an error naming field %q", tt.wantField)
			}
			field, ok := fieldFromError(err)
			if !ok {
				t.Fatalf("errors.As found no *ValidationError in %v", err)
			}
			if field != tt.wantField {
				t.Errorf("field = %q, want %q", field, tt.wantField)
			}
		})
	}
}

// TestErrorsAsReachesThroughLayers is the reason errors.As beats a type
// assertion: the ValidationError sits two wrappers deep.
func TestErrorsAsReachesThroughLayers(t *testing.T) {
	err := deeplyWrapped()

	field, ok := fieldFromError(err)
	if !ok {
		t.Fatal("errors.As should reach the ValidationError through two layers")
	}
	if field != fieldEmail {
		t.Errorf("field = %q, want %q", field, fieldEmail)
	}

	delay, retryable := retryDelayFrom(err)
	if !retryable {
		t.Fatal("errors.As should reach the RetryableError")
	}
	if delay != 2*time.Second {
		t.Errorf("delay = %v, want 2s", delay)
	}

	// A plain type assertion on the outer error finds nothing, which is what
	// makes errors.As necessary rather than merely convenient.
	//nolint:errorlint // demonstrating that the assertion fails is the point
	if _, ok := err.(*ValidationError); ok {
		t.Error("the outer error should not itself be a *ValidationError")
	}
}

// TestUnwrapIsRequiredForTransparency shows what breaks when a wrapping type
// forgets Unwrap: the chain stops dead at that layer.
func TestUnwrapIsRequiredForTransparency(t *testing.T) {
	inner := &ValidationError{Field: fieldEmail, Value: "nope", Rule: ruleEmail}

	withUnwrap := &RetryableError{After: time.Second, Err: inner}
	withoutUnwrap := &opaqueWrapper{Err: inner}

	if !errors.As(error(withUnwrap), new(*ValidationError)) {
		t.Error("a type with Unwrap must stay transparent to errors.As")
	}
	if errors.As(error(withoutUnwrap), new(*ValidationError)) {
		t.Error("a type WITHOUT Unwrap should block errors.As")
	}
}

// opaqueWrapper holds an error and does not implement Unwrap, which makes it a
// wall in the chain. This is a real bug people ship.
type opaqueWrapper struct{ Err error }

func (o *opaqueWrapper) Error() string { return "opaque: " + o.Err.Error() }

func TestRetryDelayFromNonRetryable(t *testing.T) {
	err := validateUser("", "ana@example.com", 30)

	delay, retryable := retryDelayFrom(err)
	if retryable {
		t.Error("a validation failure is not retryable")
	}
	if delay != 0 {
		t.Errorf("delay = %v, want 0", delay)
	}
}

// TestCustomIsMethod checks that implementing Is lets one check cover a family
// of concrete errors.
func TestCustomIsMethod(t *testing.T) {
	tests := []struct {
		status int
		want   bool
	}{
		{500, true},
		{502, true},
		{503, true},
		{499, false},
		{404, false},
		{200, false},
	}

	for _, tt := range tests {
		t.Run("status "+strconv.Itoa(tt.status), func(t *testing.T) {
			err := &HTTPError{Status: tt.status, URL: "https://api.example.com/x"}
			if got := errors.Is(err, ErrServerFault); got != tt.want {
				t.Errorf("errors.Is(%d, ErrServerFault) = %t, want %t", tt.status, got, tt.want)
			}
		})
	}

	t.Run("works through wrapping too", func(t *testing.T) {
		err := errors.Join(&HTTPError{Status: 503, URL: "https://x"})
		if !errors.Is(err, ErrServerFault) {
			t.Error("the custom Is must still be consulted through a wrapper")
		}
	})
}
