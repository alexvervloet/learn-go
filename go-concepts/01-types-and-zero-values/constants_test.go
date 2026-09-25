package main

import (
	"fmt"
	"testing"
	"time"
)

func TestUntypedConstantAdoptsItsContext(t *testing.T) {
	var i int = factor                         //nolint:staticcheck // explicit type is the assertion
	var f float64 = factor                     //nolint:staticcheck // explicit type is the assertion
	var d time.Duration = factor * time.Second //nolint:staticcheck // explicit type is the assertion

	if i != 3 {
		t.Errorf("int factor = %d, want 3", i)
	}
	if f != 3.0 {
		t.Errorf("float64 factor = %f, want 3.0", f)
	}
	if d != 3*time.Second {
		t.Errorf("Duration factor = %v, want 3s", d)
	}
}

func TestLevelString(t *testing.T) {
	tests := []struct {
		level Level
		want  string
	}{
		{LevelUnset, "UNSET"},
		{LevelDebug, "DEBUG"},
		{LevelInfo, "INFO"},
		{LevelWarn, "WARN"},
		{LevelError, "ERROR"},
		{Level(99), "Level(99)"}, // Go enums are open sets
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.level.String(); got != tt.want {
				t.Errorf("Level(%d).String() = %q, want %q", int(tt.level), got, tt.want)
			}
			// fmt must pick the Stringer up automatically for %v and %s.
			if got := fmt.Sprintf("%v", tt.level); got != tt.want {
				t.Errorf("%%v of Level(%d) = %q, want %q", int(tt.level), got, tt.want)
			}
		})
	}
}

// TestLevelZeroValueIsNotARealLevel is the reason the enum starts at iota+1.
func TestLevelZeroValueIsNotARealLevel(t *testing.T) {
	var unset Level

	if unset != LevelUnset {
		t.Errorf("zero Level = %d, want LevelUnset (%d)", unset, LevelUnset)
	}
	if unset.Valid() {
		t.Error("the zero Level must not report Valid(), or an unset field means DEBUG")
	}
	if !LevelDebug.Valid() {
		t.Error("LevelDebug must be Valid()")
	}
	if Level(99).Valid() {
		t.Error("an out-of-range Level must not report Valid()")
	}
}

func TestByteSizeShiftIota(t *testing.T) {
	tests := []struct {
		name string
		got  ByteSize
		want ByteSize
	}{
		{"KB", KB, 1024},
		{"MB", MB, 1024 * 1024},
		{"GB", GB, 1024 * 1024 * 1024},
		{"TB", TB, 1024 * 1024 * 1024 * 1024},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("%s = %.0f, want %.0f", tt.name, float64(tt.got), float64(tt.want))
			}
		})
	}
}
