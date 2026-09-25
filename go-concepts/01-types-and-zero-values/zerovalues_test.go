package main

import (
	"encoding/json"
	"testing"
)

// Go's test runner needs no third-party library for any of this. go-concepts
// stays dependency-free on purpose so `go test ./...` works on a fresh clone
// with no network. testify appears later, in testing-concepts, where the point
// is comparing assertion styles.

func TestZeroValues(t *testing.T) {
	// Table-driven tests are the Go idiom. One slice of cases, one loop, one
	// t.Run per case so failures name themselves.
	t.Run("numeric and string zeros", func(t *testing.T) {
		var (
			i int
			f float64
			b bool
			s string
		)
		if i != 0 || f != 0 || b != false || s != "" {
			t.Errorf("expected zeros, got %d %f %t %q", i, f, b, s)
		}
	})

	t.Run("reference types are nil", func(t *testing.T) {
		var (
			p  *int
			fn func()
			ch chan int
			e  error
			xs []string
			m  map[string]int
		)
		if p != nil || fn != nil || ch != nil || e != nil || xs != nil || m != nil {
			t.Error("expected every reference type to start nil")
		}
	})

	t.Run("struct zeroes recursively", func(t *testing.T) {
		var c Config
		want := Config{} // the composite literal with no fields is the zero value
		if c != want {
			t.Errorf("var Config = %+v, want %+v", c, want)
		}
	})
}

func TestNilSliceIsUsable(t *testing.T) {
	length, capacity, appended := nilSliceIsUsable()

	if length != 0 || capacity != 0 {
		t.Errorf("nil slice len/cap = %d/%d, want 0/0", length, capacity)
	}
	if len(appended) != 1 || appended[0] != "first" {
		t.Errorf("append to nil slice = %#v, want [first]", appended)
	}
}

// TestNilSliceVsEmptySlice is the payoff test: the two values are equivalent in
// Go and different on the wire. This is the reason the distinction matters.
func TestNilSliceVsEmptySlice(t *testing.T) {
	nilSlice, emptySlice := nilSliceVsEmptySlice()

	if len(nilSlice) != len(emptySlice) {
		t.Fatalf("lengths differ: %d vs %d", len(nilSlice), len(emptySlice))
	}
	if nilSlice != nil {
		t.Error("var xs []string should be nil")
	}
	if emptySlice == nil {
		t.Error("[]string{} should not be nil")
	}

	tests := []struct {
		name  string
		value []string
		want  string
	}{
		{"nil slice marshals as null", nilSlice, `null`},
		{"empty slice marshals as array", emptySlice, `[]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := json.Marshal(tt.value)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			if string(got) != tt.want {
				t.Errorf("json.Marshal(%#v) = %s, want %s", tt.value, got, tt.want)
			}
		})
	}
}

func TestWritingToNilMapPanics(t *testing.T) {
	got := writingToNilMapPanics()
	want := "assignment to entry in nil map"
	if got != want {
		t.Errorf("recovered %q, want %q", got, want)
	}
}

func TestCounterWorksFromItsZeroValue(t *testing.T) {
	var c Counter // deliberately no constructor

	c.Add("alpha")
	c.Add("beta")

	if c.total != 2 {
		t.Errorf("total = %d, want 2", c.total)
	}
	if got, want := c.String(), "2 parts: alpha,beta"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}
