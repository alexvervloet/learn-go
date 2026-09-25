package main

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

// TestPercentWVsPercentV is the heart of this lesson: the two wrapped errors
// have byte-identical messages and completely different behaviour.
func TestPercentWVsPercentV(t *testing.T) {
	cause := errors.New("connection refused")

	withW := wrapWithPercentW(cause)
	withV := wrapWithPercentV(cause)

	t.Run("the messages are identical", func(t *testing.T) {
		if withW.Error() != withV.Error() {
			t.Fatalf("expected identical text, got %q and %q", withW, withV)
		}
	})

	t.Run("%w keeps the cause reachable", func(t *testing.T) {
		if !errors.Is(withW, cause) {
			t.Error("errors.Is should find the cause through the wrapping verb")
		}
		// Identity, not errors.Is: the assertion is that Unwrap returns
		// exactly the value that was wrapped, one layer down. errors.Is would
		// also pass on a deeper match and would not test what this line tests.
		//
		//nolint:errorlint // asserting Unwrap's exact return value
		if got := errors.Unwrap(withW); got != cause {
			t.Errorf("Unwrap = %v, want the original cause", got)
		}
	})

	t.Run("%v discards it", func(t *testing.T) {
		if errors.Is(withV, cause) {
			t.Error("errors.Is must NOT find a cause rendered with the plain verb")
		}
		if got := errors.Unwrap(withV); got != nil {
			t.Errorf("Unwrap = %v, want nil — there is nothing wrapped", got)
		}
	})
}

func TestLayeredContext(t *testing.T) {
	err := layeredContext()

	want := "open config: read /etc/app.toml: permission denied"
	if err.Error() != want {
		t.Errorf("message = %q, want %q", err.Error(), want)
	}

	chain := chainOf(err)
	wantChain := []string{
		"open config: read /etc/app.toml: permission denied",
		"read /etc/app.toml: permission denied",
		"permission denied",
	}
	if !slices.Equal(chain, wantChain) {
		t.Errorf("chain = %#v, want %#v", chain, wantChain)
	}
}

// TestMessageConvention checks the composition property the convention exists
// to protect: a well-worded error reads as a sentence when wrapped.
func TestMessageConvention(t *testing.T) {
	cause := errors.New("no such file or directory")

	bad := badMessage(cause).Error()
	good := goodMessage(cause).Error()

	if !strings.Contains(bad, ".:") {
		t.Errorf("expected the bad example to show the %q artefact, got %q", ".:", bad)
	}
	if strings.ContainsAny(good[:1], "ABCDEFGHIJKLMNOPQRSTUVWXYZ") {
		t.Errorf("good message %q should not start with a capital", good)
	}
	if strings.Contains(good, "Failed") || strings.Contains(good, "error") {
		t.Errorf("good message %q should not say \"failed\" or \"error\"", good)
	}

	// The real test: wrap both one more layer and compare readability.
	wrappedGood := goodMessage(cause)
	outer := errors.Join(wrappedGood)
	if !strings.HasPrefix(outer.Error(), "open config:") {
		t.Errorf("wrapped good message reads %q", outer.Error())
	}
}

func TestNewVsErrorf(t *testing.T) {
	fixed, formatted := newVsErrorf("/etc/app.toml")

	if fixed.Error() != "configuration is missing" {
		t.Errorf("errors.New produced %q", fixed.Error())
	}
	if !strings.Contains(formatted.Error(), `"/etc/app.toml"`) {
		t.Errorf("fmt.Errorf should have quoted the path, got %q", formatted.Error())
	}

	// errors.New values are never equal to each other, even with the same text.
	if errors.Is(errors.New("x"), errors.New("x")) {
		t.Error("two errors.New values with the same text must not match")
	}
}

// TestUnwrapReachesTheCause covers the helper directly, on both wrapping verbs.
func TestUnwrapReachesTheCause(t *testing.T) {
	cause := errors.New("the cause")

	tests := []struct {
		name    string
		wrapped error
		want    error
	}{
		{"wrapped with the wrapping verb", wrapWithPercentW(cause), cause},
		{"wrapped with the plain verb", wrapWithPercentV(cause), nil},
		{"not wrapped at all", cause, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			//nolint:errorlint // asserting Unwrap's exact return value
			if got := unwrapReachesTheCause(tt.wrapped); got != tt.want {
				t.Errorf("unwrapReachesTheCause() = %v, want %v", got, tt.want)
			}
		})
	}
}
