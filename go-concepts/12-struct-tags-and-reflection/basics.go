package main

import (
	"fmt"
	"reflect"
	"strings"
	"time"
)

// reflect, in three types
// =======================
//
//	reflect.TypeOf(v)   -> reflect.Type   what type is this?
//	reflect.ValueOf(v)  -> reflect.Value  the value, inspectable and sometimes settable
//	value.Kind()        -> reflect.Kind   the underlying CATEGORY
//
// Type and Kind are different, and the difference matters:
//
//	type Celsius float64
//	reflect.TypeOf(Celsius(20)).String()  ->  "main.Celsius"
//	reflect.TypeOf(Celsius(20)).Kind()    ->  reflect.Float64
//
// Code that switches on Kind handles every named type for free. Code that
// compares Type does not, which is the reflection version of lesson 11's
// tilde.

// Celsius is a named float64, for the Type-vs-Kind demonstration.
type Celsius float64

// typeVsKind returns both for several values.
func typeVsKind(v any) (typeName, kindName string) {
	t := reflect.TypeOf(v)
	if t == nil {
		return "<nil>", "invalid" // reflect.TypeOf(nil) is nil
	}
	return t.String(), t.Kind().String()
}

// describeValue walks any value and describes it, which is the shape every
// reflective library starts from.
//
// The switch is on KIND, not Type, so it handles named types, and it recurses
// so it handles nesting.
func describeValue(v any) string {
	return describeReflectValue(reflect.ValueOf(v), 0)
}

func describeReflectValue(rv reflect.Value, depth int) string {
	if depth > 4 {
		return "..." // guard against a cyclic structure
	}

	// An invalid Value comes from reflect.ValueOf(nil).
	if !rv.IsValid() {
		return "nil"
	}

	switch rv.Kind() {
	case reflect.Pointer, reflect.Interface:
		if rv.IsNil() {
			return "nil"
		}
		return describeReflectValue(rv.Elem(), depth+1)

	case reflect.Struct:
		t := rv.Type()
		parts := make([]string, 0, rv.NumField())

		for i := 0; i < rv.NumField(); i++ {
			field := t.Field(i)

			// An unexported field can be SEEN but not READ. Calling
			// Interface() on it panics with "cannot return value obtained
			// from unexported field or method", so it must be skipped.
			if !field.IsExported() {
				parts = append(parts, fmt.Sprintf("%s: <unexported>", field.Name))
				continue
			}

			parts = append(parts, fmt.Sprintf("%s: %s",
				field.Name, describeReflectValue(rv.Field(i), depth+1)))
		}

		return t.Name() + "{" + strings.Join(parts, ", ") + "}"

	case reflect.Slice, reflect.Array:
		if rv.Kind() == reflect.Slice && rv.IsNil() {
			return "nil"
		}
		parts := make([]string, 0, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			parts = append(parts, describeReflectValue(rv.Index(i), depth+1))
		}
		return "[" + strings.Join(parts, " ") + "]"

	case reflect.Map:
		if rv.IsNil() {
			return "nil"
		}
		// MapKeys returns keys in the map's random order, so sort the rendered
		// strings for stable output. Same lesson as 02.
		parts := make([]string, 0, rv.Len())
		for _, k := range rv.MapKeys() {
			parts = append(parts, fmt.Sprintf("%v: %s",
				k.Interface(), describeReflectValue(rv.MapIndex(k), depth+1)))
		}
		sortStrings(parts)
		return "{" + strings.Join(parts, ", ") + "}"

	case reflect.String:
		return fmt.Sprintf("%q", rv.String())

	default:
		return fmt.Sprintf("%v", rv.Interface())
	}
}

// sortStrings sorts in place, avoiding a slices import in this file.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// fieldNames returns the exported field names of a struct type, which is the
// smallest useful reflective operation and the one most libraries need first.
func fieldNames(v any) []string {
	t := reflect.TypeOf(v)
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil
	}

	out := make([]string, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		if f := t.Field(i); f.IsExported() {
			out = append(out, f.Name)
		}
	}
	return out
}

// methodsOf lists a type's exported methods. Note that the method set follows
// lesson 03's rules: reflect on a value sees value-receiver methods only, and
// reflect on a pointer sees both.
func methodsOf(v any) []string {
	t := reflect.TypeOf(v)

	out := make([]string, 0, t.NumMethod())
	for i := 0; i < t.NumMethod(); i++ {
		out = append(out, t.Method(i).Name)
	}
	return out
}

// Counter has one value-receiver method and one pointer-receiver method, so
// methodsOf shows the difference.
type Counter struct{ n int }

func (c Counter) String() string { return fmt.Sprintf("count=%d", c.n) }
func (c *Counter) Inc()          { c.n++ }

// callMethodByName invokes a method found by name, which is the mechanism
// behind RPC dispatch, template function calls, and plugin systems.
//
// The arguments and results are []reflect.Value, which is where reflective
// code stops looking like Go.
func callMethodByName(v any, name string, args ...any) (results []any, err error) {
	rv := reflect.ValueOf(v)

	method := rv.MethodByName(name)
	if !method.IsValid() {
		return nil, fmt.Errorf("no method %q on %T", name, v)
	}

	// Arity is checked at runtime rather than by the compiler, which is the
	// whole trade reflection makes.
	if method.Type().NumIn() != len(args) {
		return nil, fmt.Errorf("method %q takes %d arguments, got %d",
			name, method.Type().NumIn(), len(args))
	}

	in := make([]reflect.Value, len(args))
	for i, a := range args {
		in[i] = reflect.ValueOf(a)
	}

	for _, r := range method.Call(in) {
		results = append(results, r.Interface())
	}
	return results, nil
}

// zeroValueOfType builds a value from a Type alone, which is how a decoder
// creates the thing it is about to fill.
func zeroValueOfType(t reflect.Type) any {
	return reflect.New(t).Elem().Interface()
}

// demoBasics prints reflection basics.
func demoBasics() {
	fmt.Println("  Type vs Kind:")
	for _, v := range []any{
		42, "text", 1.5, true,
		Celsius(20),
		time.Second,
		[]int{1, 2},
		map[string]int{"a": 1},
		Counter{},
		&Counter{},
		nil,
	} {
		typeName, kindName := typeVsKind(v)
		fmt.Printf("    %-22s type=%-18s kind=%s\n", fmt.Sprintf("%v", v), typeName, kindName)
	}
	fmt.Println("    ...Celsius and time.Duration are NAMED types with float64/int64 kinds")

	fmt.Printf("\n  describeValue on a nested struct:\n    %s\n",
		describeValue(TaggedUser{ID: 1, Email: "ana@example.com", Name: "Ana", hidden: "x"}))
	fmt.Printf("    on a slice: %s\n", describeValue([]Celsius{20, 22}))
	fmt.Printf("    on a map:   %s\n", describeValue(map[string]int{"b": 2, "a": 1}))
	fmt.Printf("    on nil:     %s\n", describeValue(nil))

	fmt.Printf("\n  fieldNames(TaggedUser{}): %v\n", fieldNames(TaggedUser{}))
	fmt.Printf("  methodsOf(Counter{}):     %v   (value receiver only)\n", methodsOf(Counter{}))
	fmt.Printf("  methodsOf(&Counter{}):    %v   (both)\n", methodsOf(&Counter{}))

	results, err := callMethodByName(Counter{n: 7}, "String")
	fmt.Printf("\n  callMethodByName(Counter{7}, \"String\") -> %v, err=%v\n", results, err)

	_, err = callMethodByName(Counter{}, "Missing")
	fmt.Printf("  a missing method is a RUNTIME error: %v\n", err)

	_, err = callMethodByName(Counter{}, "String", "unexpected")
	fmt.Printf("  wrong arity is a RUNTIME error too: %v\n", err)

	fmt.Printf("\n  zeroValueOfType(int)       = %v\n", zeroValueOfType(reflect.TypeOf(0)))
	fmt.Printf("  zeroValueOfType(TaggedUser) = %v\n", zeroValueOfType(reflect.TypeOf(TaggedUser{})))
}
