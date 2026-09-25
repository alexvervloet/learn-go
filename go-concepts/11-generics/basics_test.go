package main

import (
	"slices"
	"strings"
	"testing"
)

func TestMaxMinClamp(t *testing.T) {
	t.Run("ints", func(t *testing.T) {
		tests := []struct{ a, b, wantMax, wantMin int }{
			{3, 5, 5, 3},
			{5, 3, 5, 3},
			{4, 4, 4, 4},
			{-5, -3, -3, -5},
			{0, 0, 0, 0},
		}
		for _, tt := range tests {
			if got := Max(tt.a, tt.b); got != tt.wantMax {
				t.Errorf("Max(%d, %d) = %d, want %d", tt.a, tt.b, got, tt.wantMax)
			}
			if got := Min(tt.a, tt.b); got != tt.wantMin {
				t.Errorf("Min(%d, %d) = %d, want %d", tt.a, tt.b, got, tt.wantMin)
			}
		}
	})

	t.Run("strings", func(t *testing.T) {
		if got := Max("apple", "banana"); got != "banana" {
			t.Errorf("Max = %q, want banana", got)
		}
		if got := Min("apple", "banana"); got != "apple" {
			t.Errorf("Min = %q, want apple", got)
		}
	})

	t.Run("floats", func(t *testing.T) {
		if got := Max(1.5, 2.5); got != 2.5 {
			t.Errorf("Max = %v, want 2.5", got)
		}
	})

	t.Run("clamp", func(t *testing.T) {
		tests := []struct{ v, low, high, want int }{
			{15, 0, 10, 10},
			{-5, 0, 10, 0},
			{5, 0, 10, 5},
			{0, 0, 10, 0},
			{10, 0, 10, 10},
		}
		for _, tt := range tests {
			if got := Clamp(tt.v, tt.low, tt.high); got != tt.want {
				t.Errorf("Clamp(%d, %d, %d) = %d, want %d", tt.v, tt.low, tt.high, got, tt.want)
			}
		}
	})
}

func TestPairs(t *testing.T) {
	t.Run("string to int", func(t *testing.T) {
		got := Pairs([]string{"go", "generics", "type"}, func(s string) int { return len(s) })
		if want := []int{2, 8, 4}; !slices.Equal(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("string to string", func(t *testing.T) {
		got := Pairs([]string{"go", "rust"}, strings.ToUpper)
		if want := []string{"GO", "RUST"}; !slices.Equal(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("empty input", func(t *testing.T) {
		got := Pairs(nil, func(v int) int { return v })
		if len(got) != 0 {
			t.Errorf("got %v, want empty", got)
		}
	})
}

func TestZero(t *testing.T) {
	if got := Zero[int](); got != 0 {
		t.Errorf("Zero[int]() = %d, want 0", got)
	}
	if got := Zero[string](); got != "" {
		t.Errorf("Zero[string]() = %q, want empty", got)
	}
	if got := Zero[[]int](); got != nil {
		t.Errorf("Zero[[]int]() = %v, want nil", got)
	}
	if got := Zero[*Product](); got != nil {
		t.Errorf("Zero[*Product]() = %v, want nil", got)
	}
	if got := Zero[Point](); got != (Point{}) {
		t.Errorf("Zero[Point]() = %v, want the zero struct", got)
	}
}

// TestInferenceFromArgumentsOnly pins the behaviour the README describes:
// untyped constants infer int, and an explicit type argument changes that.
func TestInferenceFromArgumentsOnly(t *testing.T) {
	inferred, explicit := inferenceFailsOnReturnTypeAlone()

	if inferred != 5 {
		t.Errorf("Max(3, 5) = %d, want 5", inferred)
	}
	if explicit != 5.0 {
		t.Errorf("Max[float64](3, 5) = %v, want 5.0", explicit)
	}
}

func TestUntypedConstantsStillWork(t *testing.T) {
	a, b := untypedConstantsStillWork()

	if a != 3.0 {
		t.Errorf("Max(2.5, 3) = %v, want 3", a)
	}
	if b != 10 {
		t.Errorf("Max(named 5, 10) = %v, want 10", b)
	}
}

func TestContains(t *testing.T) {
	tests := []struct {
		name     string
		haystack []int
		needle   int
		want     bool
	}{
		{"present", []int{1, 2, 3}, 2, true},
		{"absent", []int{1, 2, 3}, 9, false},
		{"empty", nil, 1, false},
		{"zero value present", []int{0, 1}, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Contains(tt.haystack, tt.needle); got != tt.want {
				t.Errorf("Contains(%v, %d) = %t, want %t", tt.haystack, tt.needle, got, tt.want)
			}
		})
	}

	t.Run("works on structs, which are comparable", func(t *testing.T) {
		points := []Point{{1, 2}, {3, 4}}
		if !Contains(points, Point{3, 4}) {
			t.Error("Contains should find an equal struct")
		}
	})
}

func TestPrintGenericIsJustAny(t *testing.T) {
	// The whole point: identical output, so the type parameter bought nothing.
	for _, v := range []any{42, "text", 1.5, true} {
		if printGeneric(v) != printAny(v) {
			t.Errorf("printGeneric(%v) and printAny(%v) differ", v, v)
		}
	}
}
