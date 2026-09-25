package main

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// Settability
// ===========
//
// A reflect.Value can be written to only if it is ADDRESSABLE and EXPORTED.
//
//	reflect.ValueOf(user)              a COPY; nothing in it is addressable
//	reflect.ValueOf(&user).Elem()      addressable, because it came from a pointer
//
// This is why every Unmarshal in Go takes a pointer: without one there is
// nothing to write to. CanSet() reports it, and checking beats recovering.

// Settings is the target for the examples below.
type Settings struct {
	Host    string
	Port    int
	Debug   bool
	Timeout float64
	secret  string //nolint:unused // unexported: visible, never settable
}

// cannotSetACopy demonstrates the failure. reflect.ValueOf takes an `any`,
// which copies, and a copy has no address.
func cannotSetACopy() (canSet bool, panicMessage string) {
	s := Settings{Host: "localhost"}

	rv := reflect.ValueOf(s) // a copy
	field := rv.Field(0)

	canSet = field.CanSet() // false

	func() {
		defer func() {
			if r := recover(); r != nil {
				panicMessage = fmt.Sprint(r)
			}
		}()
		field.SetString("changed") // panics
	}()

	return canSet, panicMessage
}

// canSetThroughAPointer is the fix. ValueOf(&s) gives a pointer Value; Elem()
// dereferences it to an addressable struct Value.
func canSetThroughAPointer() (canSet bool, host string) {
	s := Settings{Host: "localhost"}

	rv := reflect.ValueOf(&s).Elem() // addressable
	field := rv.Field(0)

	canSet = field.CanSet()
	if canSet {
		field.SetString("changed")
	}

	return canSet, s.Host
}

// unexportedFieldsAreNeverSettable, even through a pointer. The field is
// addressable and still not settable, because reflection respects export rules.
//
// It can be defeated with unsafe, and doing so breaks the guarantee the rest of
// the package relies on. Do not.
func unexportedFieldsAreNeverSettable() (addressable, settable bool) {
	s := Settings{}
	rv := reflect.ValueOf(&s).Elem()

	secret := rv.FieldByName("secret")

	return secret.CanAddr(), secret.CanSet()
}

// setFieldByName sets one field from a string, converting to the field's type.
// This is the core of every config loader, form binder and CLI flag parser
// that works from tags.
func setFieldByName(target any, name, value string) error {
	rv := reflect.ValueOf(target)

	// Reject a non-pointer up front with a message that says what to do,
	// rather than letting CanSet fail confusingly three lines later.
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return fmt.Errorf("target must be a non-nil pointer, got %T", target)
	}

	elem := rv.Elem()
	if elem.Kind() != reflect.Struct {
		return fmt.Errorf("target must point to a struct, got %s", elem.Kind())
	}

	field := elem.FieldByName(name)
	if !field.IsValid() {
		return fmt.Errorf("no field %q on %T", name, target)
	}
	if !field.CanSet() {
		return fmt.Errorf("field %q is not settable (unexported?)", name)
	}

	// Switching on Kind rather than Type, so named types work.
	switch field.Kind() {
	case reflect.String:
		field.SetString(value)

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(value, 10, field.Type().Bits())
		if err != nil {
			return fmt.Errorf("field %q: %w", name, err)
		}
		// SetInt takes an int64 and narrows to the field's actual width, and
		// OverflowInt is how you check before it silently wraps (lesson 01).
		if field.OverflowInt(n) {
			return fmt.Errorf("field %q: %d overflows %s", name, n, field.Type())
		}
		field.SetInt(n)

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, err := strconv.ParseUint(value, 10, field.Type().Bits())
		if err != nil {
			return fmt.Errorf("field %q: %w", name, err)
		}
		field.SetUint(n)

	case reflect.Bool:
		b, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("field %q: %w", name, err)
		}
		field.SetBool(b)

	case reflect.Float32, reflect.Float64:
		f, err := strconv.ParseFloat(value, field.Type().Bits())
		if err != nil {
			return fmt.Errorf("field %q: %w", name, err)
		}
		field.SetFloat(f)

	default:
		return fmt.Errorf("field %q: unsupported kind %s", name, field.Kind())
	}

	return nil
}

// LoadFromMap fills a struct from string values, keyed by field name. Sixty
// lines of reflection replacing a switch that would otherwise be rewritten for
// every config struct in the codebase.
func LoadFromMap(target any, values map[string]string) error {
	var errs []string

	// Sorted for deterministic error ordering, because map iteration is not.
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sortStrings(keys)

	for _, k := range keys {
		if err := setFieldByName(target, k, values[k]); err != nil {
			errs = append(errs, err.Error())
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("load: %s", strings.Join(errs, "; "))
	}
	return nil
}

// settabilityRules is the summary.
func settabilityRules() []string {
	return []string{
		"reflect.ValueOf(x) copies: nothing in the copy is addressable",
		"reflect.ValueOf(&x).Elem() is addressable, and therefore settable",
		"an unexported field is addressable and NEVER settable",
		"CanSet() reports it; check rather than recover",
		"this is why every Unmarshal in Go takes a pointer",
	}
}

// demoSettability prints the rules in action.
func demoSettability() {
	canSet, panicMsg := cannotSetACopy()
	fmt.Printf("  reflect.ValueOf(s).Field(0): CanSet=%t\n", canSet)
	fmt.Printf("    setting it panics: %s\n", panicMsg)

	canSet, host := canSetThroughAPointer()
	fmt.Printf("  reflect.ValueOf(&s).Elem().Field(0): CanSet=%t, value now %q\n", canSet, host)

	addressable, settable := unexportedFieldsAreNeverSettable()
	fmt.Printf("  an unexported field: CanAddr=%t CanSet=%t\n", addressable, settable)

	var s Settings
	err := LoadFromMap(&s, map[string]string{
		"Host":    "example.com",
		"Port":    "8080",
		"Debug":   "true",
		"Timeout": "2.5",
	})
	fmt.Printf("\n  LoadFromMap: %+v (err=%v)\n", s, err)

	var bad Settings
	err = LoadFromMap(&bad, map[string]string{
		"Port":    "not-a-number",
		"Missing": "x",
		"secret":  "x",
		"Debug":   "maybe",
	})
	fmt.Printf("  with bad input:\n")
	for _, line := range strings.Split(err.Error(), "; ") {
		fmt.Printf("    %s\n", line)
	}

	err = LoadFromMap(Settings{}, map[string]string{"Host": "x"})
	fmt.Printf("  passing a value instead of a pointer: %v\n", err)

	fmt.Println("\n  the rules:")
	for _, r := range settabilityRules() {
		fmt.Printf("    %s\n", r)
	}
}
