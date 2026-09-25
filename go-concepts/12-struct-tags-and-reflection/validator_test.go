package main

import (
	"errors"
	"strings"
	"testing"
)

func validRequest() SignupRequest {
	return SignupRequest{
		Email:    "ana@example.com",
		Password: "a-long-enough-password",
		Age:      30,
		Plan:     "pro",
		Tags:     []string{"go"},
	}
}

func TestValidateAcceptsAValidRequest(t *testing.T) {
	if err := Validate(validRequest()); err != nil {
		t.Errorf("Validate = %v, want nil", err)
	}
}

func TestValidateRules(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*SignupRequest)
		wantField string
		wantRule  string
	}{
		{"missing email", func(r *SignupRequest) { r.Email = "" }, "email", "required"},
		{"malformed email, no at", func(r *SignupRequest) { r.Email = "nope" }, "email", "email"},
		{"malformed email, no dot", func(r *SignupRequest) { r.Email = "a@b" }, "email", "email"},
		{"malformed email, leading at", func(r *SignupRequest) { r.Email = "@b.c" }, "email", "email"},
		{"malformed email, trailing at", func(r *SignupRequest) { r.Email = "a@" }, "email", "email"},
		{"short password", func(r *SignupRequest) { r.Password = "short" }, "password", "min=8"},
		{"missing password", func(r *SignupRequest) { r.Password = "" }, "password", "required"},
		{"long password", func(r *SignupRequest) { r.Password = strings.Repeat("x", 100) }, "password", "max=72"},
		{"young", func(r *SignupRequest) { r.Age = 12 }, "age", "min=18"},
		{"old", func(r *SignupRequest) { r.Age = 200 }, "age", "max=120"},
		{"bad plan", func(r *SignupRequest) { r.Plan = "platinum" }, "plan", "oneof=free pro enterprise"},
		{"missing plan", func(r *SignupRequest) { r.Plan = "" }, "plan", "required"},
		{"too many tags", func(r *SignupRequest) { r.Tags = make([]string, 6) }, "tags", "max=5"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := validRequest()
			tt.mutate(&req)

			err := Validate(req)
			if err == nil {
				t.Fatalf("expected a failure on %s", tt.wantField)
			}

			fields := FieldErrors(err)
			rule, ok := fields[tt.wantField]
			if !ok {
				t.Fatalf("no error reported for %q; got %v", tt.wantField, fields)
			}
			if rule != tt.wantRule {
				t.Errorf("rule = %q, want %q", rule, tt.wantRule)
			}
		})
	}
}

// TestValidateBoundariesAreInclusive pins the edges, which is where off-by-one
// rules hide.
func TestValidateBoundariesAreInclusive(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*SignupRequest)
	}{
		{"age exactly 18", func(r *SignupRequest) { r.Age = 18 }},
		{"age exactly 120", func(r *SignupRequest) { r.Age = 120 }},
		{"password exactly 8", func(r *SignupRequest) { r.Password = strings.Repeat("x", 8) }},
		{"password exactly 72", func(r *SignupRequest) { r.Password = strings.Repeat("x", 72) }},
		{"exactly 5 tags", func(r *SignupRequest) { r.Tags = make([]string, 5) }},
		{"no tags at all", func(r *SignupRequest) { r.Tags = nil }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := validRequest()
			tt.mutate(&req)

			if err := Validate(req); err != nil {
				t.Errorf("boundary value rejected: %v", err)
			}
		})
	}
}

// TestValidateReportsEveryFailure: a form with five bad fields should tell the
// user all five.
func TestValidateReportsEveryFailure(t *testing.T) {
	err := Validate(SignupRequest{
		Email:    "not-an-email",
		Password: "short",
		Age:      12,
		Plan:     "platinum",
		Tags:     make([]string, 6),
	})

	if err == nil {
		t.Fatal("expected failures")
	}

	fields := FieldErrors(err)
	for _, want := range []string{"email", "password", "age", "plan", "tags"} {
		if _, ok := fields[want]; !ok {
			t.Errorf("no failure reported for %q; got %v", want, fields)
		}
	}
}

// TestValidateUsesTheJSONName: error messages should name the field the way
// the caller sent it, not the Go field name.
func TestValidateUsesTheJSONName(t *testing.T) {
	err := Validate(SignupRequest{Plan: "free", Password: "long-enough-x", Email: ""})

	if err == nil {
		t.Fatal("expected a failure")
	}
	if !strings.Contains(err.Error(), "field email") {
		t.Errorf("err = %q, want the JSON name \"email\" rather than \"Email\"", err)
	}
	if strings.Contains(err.Error(), "field Email") {
		t.Errorf("err = %q, should not use the Go field name", err)
	}
}

// TestUntaggedFieldsAreSkipped: Referrer has no validate tag and must never
// fail, whatever its value.
func TestUntaggedFieldsAreSkipped(t *testing.T) {
	req := validRequest()
	req.Referrer = "" // no rule applies

	if err := Validate(req); err != nil {
		t.Errorf("an untagged field caused a failure: %v", err)
	}
}

// TestUnknownRuleIsReported is the choice that turns a silent typo into a
// loud one. Most validators ignore an unknown rule.
func TestUnknownRuleIsReported(t *testing.T) {
	err := Validate(TypoRequest{Email: "ana@example.com"})

	if err == nil {
		t.Fatal("a misspelled rule should be reported, not ignored")
	}
	if !errors.Is(err, ErrUnknownRule) {
		t.Errorf("err = %v, want it to wrap ErrUnknownRule", err)
	}
	if !strings.Contains(err.Error(), "requried") {
		t.Errorf("err = %q, want it to quote the typo", err)
	}
}

func TestValidateRejectsNonStructs(t *testing.T) {
	tests := []struct {
		name  string
		input any
	}{
		{"an int", 42},
		{"a string", "text"},
		{"a slice", []int{1}},
		{"a nil pointer", (*SignupRequest)(nil)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := Validate(tt.input); err == nil {
				t.Error("expected an error")
			}
		})
	}
}

func TestValidateAcceptsAPointer(t *testing.T) {
	req := validRequest()

	if err := Validate(&req); err != nil {
		t.Errorf("a pointer to a valid struct should pass: %v", err)
	}
}

func TestValidationErrorMessage(t *testing.T) {
	e := &ValidationError{Field: "email", Rule: "required", Value: ""}

	got := e.Error()
	for _, want := range []string{"email", "required"} {
		if !strings.Contains(got, want) {
			t.Errorf("message %q should contain %q", got, want)
		}
	}
}

func TestFieldErrorsOnNil(t *testing.T) {
	if got := FieldErrors(nil); got != nil {
		t.Errorf("FieldErrors(nil) = %v, want nil", got)
	}
	if got := FieldErrors(errors.New("unrelated")); len(got) != 0 {
		t.Errorf("FieldErrors on an unrelated error = %v, want empty", got)
	}
}

// TestApplyRuleArgumentErrors covers the malformed-tag paths, which are
// author mistakes rather than user input.
func TestApplyRuleArgumentErrors(t *testing.T) {
	type badTags struct {
		NoArg      string `validate:"min"`
		BadArg     string `validate:"min=abc"`
		WrongKind  int    `validate:"email"`
		OneofNoArg string `validate:"oneof"`
	}

	err := Validate(badTags{})
	if err == nil {
		t.Fatal("expected errors for malformed rules")
	}

	for _, want := range []string{"needs an argument", "non-numeric", "needs a string", "needs arguments"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err %q should mention %q", err, want)
		}
	}
}

func TestApplyBoundOnUnsupportedKind(t *testing.T) {
	type badTarget struct {
		Flag bool `validate:"min=1"`
	}

	err := Validate(badTarget{})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "does not apply") {
		t.Errorf("err = %q", err)
	}
}
