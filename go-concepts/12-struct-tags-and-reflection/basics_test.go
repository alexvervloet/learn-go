package main

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestTypeVsKind is the distinction that makes reflective code work on named
// types: switching on Kind covers them, comparing Type does not.
func TestTypeVsKind(t *testing.T) {
	tests := []struct {
		name     string
		value    any
		wantType string
		wantKind string
	}{
		{"int", 42, "int", "int"},
		{"string", "text", "string", "string"},
		{"named float", Celsius(20), "main.Celsius", "float64"},
		{"named int64", time.Second, "time.Duration", "int64"},
		{"slice", []int{1}, "[]int", "slice"},
		{"map", map[string]int{}, "map[string]int", "map"},
		{"struct", Counter{}, "main.Counter", "struct"},
		{"pointer", &Counter{}, "*main.Counter", "ptr"},
		{"nil", nil, "<nil>", "invalid"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotType, gotKind := typeVsKind(tt.value)

			if gotType != tt.wantType {
				t.Errorf("type = %q, want %q", gotType, tt.wantType)
			}
			if gotKind != tt.wantKind {
				t.Errorf("kind = %q, want %q", gotKind, tt.wantKind)
			}
		})
	}
}

func TestDescribeValue(t *testing.T) {
	tests := []struct {
		name  string
		value any
		want  string
	}{
		{"int", 42, "42"},
		{"string", "text", `"text"`},
		{"nil", nil, "nil"},
		{"nil slice", []int(nil), "nil"},
		{"slice", []int{1, 2}, "[1 2]"},
		{"named slice", []Celsius{20, 22}, "[20 22]"},
		{"map sorted", map[string]int{"b": 2, "a": 1}, "{a: 1, b: 2}"},
		{"nil map", map[string]int(nil), "nil"},
		{"nil pointer", (*Counter)(nil), "nil"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := describeValue(tt.value); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// TestDescribeValueSkipsUnexportedFields: reading one through Interface()
// panics, so a walker has to check IsExported.
func TestDescribeValueSkipsUnexportedFields(t *testing.T) {
	got := describeValue(TaggedUser{ID: 1, Email: "a@b.c", hidden: "secret"})

	if !strings.Contains(got, "hidden: <unexported>") {
		t.Errorf("got %q, want the unexported field marked", got)
	}
	if strings.Contains(got, "secret") {
		t.Errorf("the unexported VALUE leaked into %q", got)
	}
}

// TestDescribeValueFollowsPointers exercises the recursion.
func TestDescribeValueFollowsPointers(t *testing.T) {
	c := &Counter{n: 7}

	got := describeValue(c)
	if !strings.Contains(got, "Counter{") {
		t.Errorf("got %q, want it to dereference the pointer", got)
	}
}

// TestDescribeValueGuardsAgainstDepth: without the depth cap, a self-
// referential structure would recurse forever.
func TestDescribeValueGuardsAgainstDepth(t *testing.T) {
	type nested struct{ Next *nested }

	deep := &nested{}
	current := deep
	for i := 0; i < 20; i++ {
		current.Next = &nested{}
		current = current.Next
	}

	got := describeValue(deep) // must return rather than blow the stack
	if !strings.Contains(got, "...") {
		t.Errorf("got %q, want the depth guard to have fired", got)
	}
}

func TestFieldNames(t *testing.T) {
	want := []string{"ID", "Email", "Name", "Password", "Internal"}

	if got := fieldNames(TaggedUser{}); !slices.Equal(got, want) {
		t.Errorf("got %v, want %v — unexported fields should be skipped", got, want)
	}
	if got := fieldNames(&TaggedUser{}); !slices.Equal(got, want) {
		t.Errorf("a pointer gave %v, want the same", got)
	}
	if got := fieldNames(42); got != nil {
		t.Errorf("a non-struct gave %v, want nil", got)
	}
}

// TestMethodSetsThroughReflection is lesson 03's rule, observed from outside:
// a value sees value-receiver methods, a pointer sees both.
func TestMethodSetsThroughReflection(t *testing.T) {
	valueMethods := methodsOf(Counter{})
	pointerMethods := methodsOf(&Counter{})

	if !slices.Equal(valueMethods, []string{"String"}) {
		t.Errorf("value methods = %v, want [String]", valueMethods)
	}
	if !slices.Contains(pointerMethods, "Inc") || !slices.Contains(pointerMethods, "String") {
		t.Errorf("pointer methods = %v, want both Inc and String", pointerMethods)
	}
	if len(pointerMethods) <= len(valueMethods) {
		t.Error("a pointer's method set should be larger than its value's")
	}
}

func TestCallMethodByName(t *testing.T) {
	t.Run("calls a method", func(t *testing.T) {
		results, err := callMethodByName(Counter{n: 7}, "String")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(results) != 1 || results[0] != "count=7" {
			t.Errorf("results = %v, want [count=7]", results)
		}
	})

	t.Run("mutates through a pointer", func(t *testing.T) {
		c := &Counter{}

		if _, err := callMethodByName(c, "Inc"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if c.n != 1 {
			t.Errorf("n = %d, want 1 — Inc should have mutated the receiver", c.n)
		}
	})

	// The trade reflection makes: these are runtime errors rather than
	// compile errors.
	t.Run("a missing method", func(t *testing.T) {
		_, err := callMethodByName(Counter{}, "Missing")
		if err == nil {
			t.Fatal("expected an error")
		}
		if !strings.Contains(err.Error(), "Missing") {
			t.Errorf("err = %q, want it to name the method", err)
		}
	})

	t.Run("wrong arity", func(t *testing.T) {
		_, err := callMethodByName(Counter{}, "String", "unexpected")
		if err == nil {
			t.Fatal("expected an error")
		}
		if !strings.Contains(err.Error(), "argument") {
			t.Errorf("err = %q, want it to mention arity", err)
		}
	})

	t.Run("a pointer method on a value is not in the method set", func(t *testing.T) {
		// Counter (value) has no Inc, so this must fail rather than silently
		// mutating a copy.
		_, err := callMethodByName(Counter{}, "Inc")
		if err == nil {
			t.Error("Inc should not be callable on a Counter value")
		}
	})
}

func TestZeroValueOfType(t *testing.T) {
	tests := []struct {
		name string
		typ  reflect.Type
		want any
	}{
		{"int", reflect.TypeOf(0), 0},
		{"string", reflect.TypeOf(""), ""},
		{"bool", reflect.TypeOf(false), false},
		{"struct", reflect.TypeOf(Counter{}), Counter{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := zeroValueOfType(tt.typ); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSortStrings(t *testing.T) {
	got := []string{"c", "a", "b", "a"}
	sortStrings(got)

	if want := []string{"a", "a", "b", "c"}; !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}

	var empty []string
	sortStrings(empty) // must not panic
}
