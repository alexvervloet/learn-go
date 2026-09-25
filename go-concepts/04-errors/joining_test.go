package main

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestValidateAllReportsEveryFailure(t *testing.T) {
	tests := []struct {
		name       string
		userName   string
		email      string
		age        int
		wantFields []string
	}{
		{"all valid", "Ana", "ana@example.com", 30, nil},
		{"one bad field", "Ana", "nope", 30, []string{fieldEmail}},
		{"two bad fields", "", "nope", 30, []string{fieldName, fieldEmail}},
		{"all three bad", "", "nope", 200, []string{fieldName, fieldEmail, fieldAge}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateAll(tt.userName, tt.email, tt.age)

			if len(tt.wantFields) == 0 {
				if err != nil {
					t.Fatalf("expected nil, got %v", err)
				}
				return
			}

			if err == nil {
				t.Fatalf("expected errors for %v", tt.wantFields)
			}
			if got := allFieldsFrom(err); !slices.Equal(got, tt.wantFields) {
				t.Errorf("failing fields = %v, want %v", got, tt.wantFields)
			}

			// Each failure gets its own line in the joined message.
			lines := strings.Split(err.Error(), "\n")
			if len(lines) != len(tt.wantFields) {
				t.Errorf("joined message has %d lines, want %d:\n%s", len(lines), len(tt.wantFields), err)
			}
		})
	}
}

// TestJoinOfNilsIsNil is the property that lets validateAll skip a length
// check on the happy path.
func TestJoinSkipsNils(t *testing.T) {
	count, allNil, someNil := joinSkipsNils()

	if allNil != nil {
		t.Errorf("Join(nil, nil, nil) = %v, want nil", allNil)
	}
	if someNil == nil {
		t.Fatal("Join with two real errors should not be nil")
	}
	if count != 2 {
		t.Errorf("the join holds %d errors, want 2 — nils should be dropped", count)
	}
}

func TestIsSearchesEveryBranch(t *testing.T) {
	first, second, absent := isSearchesEveryBranch()

	if !first {
		t.Error("errors.Is should find the sentinel in the first branch")
	}
	if !second {
		t.Error("errors.Is should find the sentinel in the second branch")
	}
	if absent {
		t.Error("errors.Is must not report a sentinel that is in neither branch")
	}
}

// TestAllFieldsFromHandlesBothUnwrapShapes covers the tree walk against a
// structure mixing single-wrap and multi-wrap nodes.
func TestAllFieldsFromHandlesBothUnwrapShapes(t *testing.T) {
	nested := errors.Join(
		&ValidationError{Field: fieldName, Value: "", Rule: ruleRequired},
		errors.Join(
			&ValidationError{Field: fieldEmail, Value: "x", Rule: ruleEmail},
			&RetryableError{Err: &ValidationError{Field: fieldAge, Value: 999, Rule: ruleAgeRange}},
		),
	)

	got := allFieldsFrom(nested)
	want := []string{fieldName, fieldEmail, fieldAge}
	if !slices.Equal(got, want) {
		t.Errorf("allFieldsFrom = %v, want %v", got, want)
	}
}

func TestAllFieldsFromOnNilAndUnrelated(t *testing.T) {
	if got := allFieldsFrom(nil); got != nil {
		t.Errorf("allFieldsFrom(nil) = %v, want nil", got)
	}
	if got := allFieldsFrom(ErrNotFound); got != nil {
		t.Errorf("allFieldsFrom(ErrNotFound) = %v, want nil", got)
	}
}
