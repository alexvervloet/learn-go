package main

import (
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// A tag-driven validator
// ======================
//
// The real-world payoff for tags plus reflection, and the thing
// go-playground/validator does at scale. About a hundred lines here for the
// rules that cover most of what a request body needs.
//
//	type SignupRequest struct {
//	    Email string `validate:"required,email"`
//	    Age   int    `validate:"min=18,max=120"`
//	}
//
// Worth being clear about the trade before writing it: this moves a class of
// error from compile time to runtime. A typo in a tag (`requried`) is not a
// build failure, it is a rule that silently never runs. The validator below
// rejects unknown rules for exactly that reason, which most do not.

// ValidationError names one failed rule on one field.
type ValidationError struct {
	Field string
	Rule  string
	Value any
}

// Error implements error.
func (e *ValidationError) Error() string {
	return fmt.Sprintf("field %s failed rule %q (value: %v)", e.Field, e.Rule, e.Value)
}

// ErrUnknownRule is returned when a tag names a rule the validator does not
// know. Treating this as an error rather than ignoring it is what turns a
// silent typo into a loud one.
var ErrUnknownRule = errors.New("unknown validation rule")

// Validate checks every exported field of a struct against its validate tag.
// It returns every failure, joined, rather than stopping at the first: a form
// with four bad fields should report four.
func Validate(v any) error {
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return errors.New("validate: nil pointer")
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return fmt.Errorf("validate: want a struct, got %s", rv.Kind())
	}

	rt := rv.Type()
	var errs []error

	for i := 0; i < rt.NumField(); i++ {
		field := rt.Field(i)

		// Unexported fields cannot be read, so they cannot be validated.
		if !field.IsExported() {
			continue
		}

		tag := field.Tag.Get("validate")
		if tag == "" || tag == "-" {
			continue
		}

		// The JSON name if there is one, so error messages match the wire
		// format the caller sent rather than the Go field name.
		name := field.Name
		if jsonName, _ := parseTagOptions(field.Tag.Get("json")); jsonName != "" && jsonName != "-" {
			name = jsonName
		}

		for _, rule := range strings.Split(tag, ",") {
			if rule == "" {
				continue
			}
			if err := applyRule(name, rule, rv.Field(i)); err != nil {
				errs = append(errs, err)
			}
		}
	}

	return errors.Join(errs...)
}

// applyRule runs one rule against one field.
func applyRule(name, rule string, value reflect.Value) error {
	ruleName, arg, hasArg := strings.Cut(rule, "=")

	switch ruleName {
	case "required":
		if value.IsZero() {
			return &ValidationError{Field: name, Rule: rule, Value: value.Interface()}
		}

	case "email":
		if value.Kind() != reflect.String {
			return fmt.Errorf("field %s: rule email needs a string, got %s", name, value.Kind())
		}
		// Deliberately crude. Real email validation is RFC 5322 and the only
		// honest check is sending a message to the address.
		s := value.String()
		at := strings.Index(s, "@")
		if at <= 0 || at == len(s)-1 || !strings.Contains(s[at:], ".") {
			return &ValidationError{Field: name, Rule: rule, Value: s}
		}

	case "min", "max":
		if !hasArg {
			return fmt.Errorf("field %s: rule %q needs an argument, as %s=N", name, ruleName, ruleName)
		}
		return applyBound(name, rule, ruleName, arg, value)

	case "oneof":
		if !hasArg {
			return fmt.Errorf("field %s: rule oneof needs arguments, as oneof=a b c", name)
		}
		if value.Kind() != reflect.String {
			return fmt.Errorf("field %s: rule oneof needs a string, got %s", name, value.Kind())
		}
		for _, allowed := range strings.Fields(arg) {
			if value.String() == allowed {
				return nil
			}
		}
		return &ValidationError{Field: name, Rule: rule, Value: value.String()}

	default:
		// A typo in a tag is a rule that never runs. Reporting it is the whole
		// reason this branch is not a silent `return nil`.
		return fmt.Errorf("field %s: %w %q", name, ErrUnknownRule, ruleName)
	}

	return nil
}

// applyBound implements min and max, which mean different things per kind:
// a length for strings and slices, a magnitude for numbers.
func applyBound(name, rule, ruleName, arg string, value reflect.Value) error {
	bound, err := strconv.ParseFloat(arg, 64)
	if err != nil {
		return fmt.Errorf("field %s: rule %q has a non-numeric argument %q", name, ruleName, arg)
	}

	var actual float64
	switch value.Kind() {
	case reflect.String, reflect.Slice, reflect.Map, reflect.Array:
		actual = float64(value.Len())

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		actual = float64(value.Int())

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		actual = float64(value.Uint())

	case reflect.Float32, reflect.Float64:
		actual = value.Float()

	default:
		return fmt.Errorf("field %s: rule %q does not apply to %s", name, ruleName, value.Kind())
	}

	failed := (ruleName == "min" && actual < bound) || (ruleName == "max" && actual > bound)
	if failed {
		return &ValidationError{Field: name, Rule: rule, Value: value.Interface()}
	}
	return nil
}

// SignupRequest is a realistic target.
type SignupRequest struct {
	Email    string   `json:"email" validate:"required,email"`
	Password string   `json:"password" validate:"required,min=8,max=72"`
	Age      int      `json:"age" validate:"min=18,max=120"`
	Plan     string   `json:"plan" validate:"required,oneof=free pro enterprise"`
	Tags     []string `json:"tags" validate:"max=5"`
	Referrer string   `json:"referrer"` // no tag: never checked
	internal string   //nolint:unused // unexported: skipped
}

// TypoRequest has a misspelled rule, to show the validator catching it.
type TypoRequest struct {
	Email string `json:"email" validate:"requried"` //nolint:misspell // the typo is the demonstration
}

// FieldErrors pulls every ValidationError out of a joined error, so a handler
// can build a per-field response.
func FieldErrors(err error) map[string]string {
	if err == nil {
		return nil
	}

	out := make(map[string]string)

	var walk func(error)
	walk = func(e error) {
		if e == nil {
			return
		}

		var verr *ValidationError
		if errors.As(e, &verr) {
			if _, exists := out[verr.Field]; !exists {
				out[verr.Field] = verr.Rule
			}
		}

		//nolint:errorlint // walking the tree's structure, not matching a target
		switch x := e.(type) {
		case interface{ Unwrap() []error }:
			for _, sub := range x.Unwrap() {
				walk(sub)
			}
		case interface{ Unwrap() error }:
			walk(x.Unwrap())
		}
	}
	walk(err)

	return out
}

// demoValidator prints validation.
func demoValidator() {
	valid := SignupRequest{
		Email:    "ana@example.com",
		Password: "a-long-enough-password",
		Age:      30,
		Plan:     "pro",
		Tags:     []string{"go"},
	}
	fmt.Printf("  a valid request: %v\n", Validate(valid))

	invalid := SignupRequest{
		Email:    "not-an-email",
		Password: "short",
		Age:      12,
		Plan:     "platinum",
		Tags:     []string{"a", "b", "c", "d", "e", "f"},
	}

	err := Validate(invalid)
	fmt.Println("\n  an invalid request:")
	for _, line := range strings.Split(err.Error(), "\n") {
		fmt.Printf("    %s\n", line)
	}

	fmt.Printf("\n  as a per-field map for an API response:\n")
	for _, field := range []string{"email", "password", "age", "plan", "tags"} {
		if rule, ok := FieldErrors(err)[field]; ok {
			fmt.Printf("    %-9s %s\n", field, rule)
		}
	}

	typo := Validate(TypoRequest{Email: "ana@example.com"})
	fmt.Printf("\n  a misspelled rule is reported rather than ignored:\n    %v\n", typo)
	fmt.Printf("    errors.Is(err, ErrUnknownRule) = %t\n", errors.Is(typo, ErrUnknownRule))

	fmt.Printf("\n  validating a non-struct: %v\n", Validate(42))
	fmt.Printf("  validating a nil pointer: %v\n", Validate((*SignupRequest)(nil)))
}
