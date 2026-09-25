package main

import (
	"slices"
	"strings"
	"testing"
)

// TestRuntimePanics asserts every runtime panic in the catalogue actually
// panics, with a message that names the problem. If a Go release ever changed
// one of these messages, this test would catch the README going stale.
func TestRuntimePanics(t *testing.T) {
	wantFragments := map[string]string{
		"index out of range":        "index out of range",
		"nil pointer dereference":   "nil pointer dereference",
		"nil map write":             "assignment to entry in nil map",
		"integer divide by zero":    "integer divide by zero",
		"slice bounds out of range": "slice bounds out of range",
		"interface conversion":      "interface conversion",
	}

	kinds := runtimePanics()
	if len(kinds) != len(wantFragments) {
		t.Fatalf("catalogue has %d entries, expectations have %d", len(kinds), len(wantFragments))
	}

	for _, pk := range kinds {
		t.Run(pk.name, func(t *testing.T) {
			msg := capturePanic(pk.trigger)

			if msg == "" {
				t.Fatalf("%s did not panic", pk.name)
			}
			want, ok := wantFragments[pk.name]
			if !ok {
				t.Fatalf("no expectation for %q", pk.name)
			}
			if !strings.Contains(msg, want) {
				t.Errorf("panic message %q should contain %q", msg, want)
			}
		})
	}
}

func TestCapturePanicReturnsEmptyWhenNothingPanics(t *testing.T) {
	if got := capturePanic(func() {}); got != "" {
		t.Errorf("capturePanic(no-op) = %q, want empty", got)
	}
}

func TestArea(t *testing.T) {
	tests := []struct {
		name string
		s    Shape
		size float64
		want float64
	}{
		{"square", ShapeSquare, 3, 9},
		{"triangle", ShapeTriangle, 4, 8},
		{"circle", ShapeCircle, 1, 3.14159265358979},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Area(tt.s, tt.size); got != tt.want {
				t.Errorf("Area(%v, %v) = %v, want %v", tt.s, tt.size, got, tt.want)
			}
		})
	}

	t.Run("an unhandled shape panics rather than returning zero", func(t *testing.T) {
		msg := capturePanic(func() { _ = Area(Shape(99), 3) })
		if !strings.Contains(msg, "unhandled shape 99") {
			t.Errorf("panic message = %q, want it to name the shape", msg)
		}
	})

	t.Run("the zero Shape is also unhandled", func(t *testing.T) {
		// Shape starts at iota+1, so the zero value is not a real shape and
		// must not silently compute a circle.
		var unset Shape
		if msg := capturePanic(func() { _ = Area(unset, 3) }); msg == "" {
			t.Error("the zero Shape should panic, not return an area")
		}
	})
}

func TestParseCSVHeader(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    []string
		wantErr string
	}{
		{"simple", "id,name", []string{"id", "name"}, ""},
		{"trims whitespace", " id , name ", []string{"id", "name"}, ""},
		{"single column", "id", []string{"id"}, ""},
		{"empty", "", nil, "empty header"},
		{"only spaces", "   ", nil, "empty header"},
		{"empty column", "id,,name", nil, "column 1 is empty"},
		{"duplicate", "id,name,id", nil, `duplicate column "id"`},
		{"duplicate after trimming", "id, id", nil, `duplicate column "id"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseCSVHeader(tt.in)

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error %q", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error = %q, want it to contain %q", err, tt.wantErr)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

// TestMustPanicsWhereParseReturns is the contract of a Must function: same
// input, one panics and one reports.
func TestMustPanicsWhereParseReturns(t *testing.T) {
	const bad = "id,id"

	if _, err := ParseCSVHeader(bad); err == nil {
		t.Fatal("ParseCSVHeader should have returned an error")
	}

	msg := capturePanic(func() { _ = MustParseCSVHeader(bad) })
	if msg == "" {
		t.Fatal("MustParseCSVHeader should have panicked")
	}
	if !strings.Contains(msg, "duplicate column") {
		t.Errorf("panic %q should carry the underlying reason", msg)
	}

	// And the happy path must not panic.
	if got := MustParseCSVHeader("id,name"); !slices.Equal(got, []string{"id", "name"}) {
		t.Errorf("MustParseCSVHeader(valid) = %v", got)
	}
}

func TestRingInvariant(t *testing.T) {
	t.Run("a valid size works", func(t *testing.T) {
		r := newRing(3)
		for _, s := range []string{"a", "b", "c", "d"} {
			r.push(s)
		}
		// Size 3, four pushes: the first slot was overwritten.
		want := []string{"d", "b", "c"}
		if !slices.Equal(r.items, want) {
			t.Errorf("items = %v, want %v", r.items, want)
		}
	})

	t.Run("a non-positive size panics", func(t *testing.T) {
		for _, size := range []int{0, -1} {
			if msg := capturePanic(func() { _ = newRing(size) }); msg == "" {
				t.Errorf("newRing(%d) should panic", size)
			}
		}
	})

	t.Run("a corrupted invariant panics", func(t *testing.T) {
		r := newRing(3)
		r.head = 99 // simulate the bug this guard exists to catch

		msg := capturePanic(func() { r.push("x") })
		if !strings.Contains(msg, "invariant violated") {
			t.Errorf("panic = %q, want the invariant message", msg)
		}
	})
}

func TestFatalErrorsAreDocumented(t *testing.T) {
	got := fatalErrorsCannotBeRecovered()

	if len(got) < 4 {
		t.Errorf("expected at least 4 documented fatal errors, got %d", len(got))
	}
	if !slices.Contains(got, "concurrent map writes") {
		t.Error("the list should include concurrent map writes, the most common one")
	}
}
