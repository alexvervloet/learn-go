package main

import (
	"slices"
	"testing"
)

func TestSum(t *testing.T) {
	t.Run("ints", func(t *testing.T) {
		if got := Sum([]int{1, 2, 3}); got != 6 {
			t.Errorf("got %d, want 6", got)
		}
	})

	t.Run("floats", func(t *testing.T) {
		if got := Sum([]float64{1.5, 2.5}); got != 4.0 {
			t.Errorf("got %v, want 4.0", got)
		}
	})

	// The reason the constraint uses ~: a named type must still satisfy it.
	t.Run("named types satisfy the constraint because of the tilde", func(t *testing.T) {
		if got := Sum([]Celsius{20, 22}); got != 42 {
			t.Errorf("got %v, want 42", got)
		}
		if got := Sum([]Bytes{1024, 2048}); got != 3072 {
			t.Errorf("got %v, want 3072", got)
		}
	})

	t.Run("empty", func(t *testing.T) {
		if got := Sum([]int(nil)); got != 0 {
			t.Errorf("got %d, want 0", got)
		}
	})
}

func TestMean(t *testing.T) {
	tests := []struct {
		name string
		got  float64
		want float64
	}{
		{"integer division truncates", float64(Mean([]int{2, 4, 9})), 5},
		{"float keeps the fraction", Mean([]float64{2, 4, 9}), 5},
		{"empty returns zero", float64(Mean([]int(nil))), 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("got %v, want %v", tt.got, tt.want)
			}
		})
	}
}

func TestIsEven(t *testing.T) {
	tests := []struct {
		v    int
		want bool
	}{{0, true}, {1, false}, {2, true}, {-2, true}, {-3, false}}

	for _, tt := range tests {
		if got := IsEven(tt.v); got != tt.want {
			t.Errorf("IsEven(%d) = %t, want %t", tt.v, got, tt.want)
		}
	}

	if !IsEven(Bytes(4)) {
		t.Error("IsEven should accept a named integer type")
	}
}

func TestSumStrictAcceptsOnlyInt(t *testing.T) {
	if got := SumStrict([]int{1, 2, 3}); got != 6 {
		t.Errorf("got %d, want 6", got)
	}
	// SumStrict([]Bytes{1, 2}) does not compile:
	//   Bytes does not satisfy StrictInt (possibly missing ~ for int in StrictInt)
}

func TestDeduplicate(t *testing.T) {
	tests := []struct {
		name string
		in   []int
		want []int
	}{
		{"with duplicates", []int{1, 2, 1, 3, 2}, []int{1, 2, 3}},
		{"already unique", []int{1, 2, 3}, []int{1, 2, 3}},
		{"all the same", []int{5, 5, 5}, []int{5}},
		{"empty", nil, []int{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Deduplicate(tt.in)
			if !slices.Equal(got, tt.want) {
				t.Errorf("got %v, want %v — order should be preserved", got, tt.want)
			}
		})
	}

	t.Run("structs, because comparable covers them", func(t *testing.T) {
		got := Deduplicate([]Point{{1, 2}, {1, 2}, {3, 4}})
		if want := []Point{{1, 2}, {3, 4}}; !slices.Equal(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})
}

func TestSortedUnique(t *testing.T) {
	if got := SortedUnique([]int{3, 1, 3, 2}); !slices.Equal(got, []int{1, 2, 3}) {
		t.Errorf("got %v, want [1 2 3]", got)
	}
	if got := SortedUnique([]string{"c", "a", "c", "b"}); !slices.Equal(got, []string{"a", "b", "c"}) {
		t.Errorf("got %v, want [a b c]", got)
	}
	// SortedUnique([]Point{...}) does not compile: Point is comparable, not ordered.
}

func TestDescribe(t *testing.T) {
	products := []Product{{SKU: "A1", Title: "Keyboard"}, {SKU: "B2", Title: "Mouse"}}

	if got := Describe(products); got != "Keyboard, Mouse" {
		t.Errorf("got %q, want %q", got, "Keyboard, Mouse")
	}
	if got := Describe([]Product(nil)); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestFormatAll(t *testing.T) {
	formatted, total := FormatAll([]Temperature{20.5, 22.1})

	if want := []string{"20.5°C", "22.1°C"}; !slices.Equal(formatted, want) {
		t.Errorf("formatted = %v, want %v", formatted, want)
	}
	if total != 42.6 {
		t.Errorf("total = %v, want 42.6", total)
	}
}

func TestConstraintDocsArePresent(t *testing.T) {
	if got := constraintInterfacesCannotBeVariableTypes(); len(got) < 3 {
		t.Errorf("expected at least 3 documented points, got %d", len(got))
	}
}
