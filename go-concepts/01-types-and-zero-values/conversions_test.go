package main

import (
	"math"
	"testing"
)

func TestTruncationVsRounding(t *testing.T) {
	tests := []struct {
		name          string
		in            float64
		wantTruncated int
		wantRounded   int
	}{
		{"positive below half", 2.4, 2, 2},
		{"positive at half", 2.5, 2, 3},
		{"positive above half", 2.7, 2, 3},
		{"negative below half", -2.4, -2, -2},
		{"negative at half", -2.5, -2, -3}, // math.Round moves away from zero
		{"negative above half", -2.7, -2, -3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := truncatesTowardZero(tt.in); got != tt.wantTruncated {
				t.Errorf("int(%v) = %d, want %d", tt.in, got, tt.wantTruncated)
			}
			if got := roundsProperly(tt.in); got != tt.wantRounded {
				t.Errorf("round(%v) = %d, want %d", tt.in, got, tt.wantRounded)
			}
		})
	}
}

// TestOverflowIsSilent pins the wrap-around. Nothing in Go reports this, which
// is exactly why convertsSafely exists.
func TestOverflowIsSilent(t *testing.T) {
	tests := []struct {
		name string
		in   int
		want int8
	}{
		{"in range", 100, 100},
		{"just over max", 128, -128},
		{"200 wraps", 200, -56},
		{"just under min", -129, 127},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := overflowsSilently(tt.in); got != tt.want {
				t.Errorf("int8(%d) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

func TestConvertsSafely(t *testing.T) {
	tests := []struct {
		name    string
		in      int
		want    int8
		wantErr bool
	}{
		{"zero", 0, 0, false},
		{"max", math.MaxInt8, 127, false},
		{"min", math.MinInt8, -128, false},
		{"over max", math.MaxInt8 + 1, 0, true},
		{"under min", math.MinInt8 - 1, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := convertsSafely(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("convertsSafely(%d) error = %v, wantErr %t", tt.in, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("convertsSafely(%d) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

func TestStringBytesRunes(t *testing.T) {
	tests := []struct {
		name         string
		in           string
		wantBytes    int
		wantRunes    int
		wantFirstRun rune
	}{
		{"ascii only", "hello", 5, 5, 'h'},
		{"accented latin", "héllo", 6, 5, 'h'},
		{"leading multibyte", "émile", 6, 5, 'é'},
		{"emoji is four bytes", "🙂ok", 6, 3, '🙂'},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			byteLen, runeLen, _, firstRune := stringBytesRunes(tt.in)
			if byteLen != tt.wantBytes {
				t.Errorf("byte length of %q = %d, want %d", tt.in, byteLen, tt.wantBytes)
			}
			if runeLen != tt.wantRunes {
				t.Errorf("rune count of %q = %d, want %d", tt.in, runeLen, tt.wantRunes)
			}
			if firstRune != tt.wantFirstRun {
				t.Errorf("first rune of %q = %q, want %q", tt.in, firstRune, tt.wantFirstRun)
			}
		})
	}
}

// TestRangeOverStringSkipsByWidth is the concrete reason indexing a string by
// integer is wrong for anything but ASCII.
func TestRangeOverStringSkipsByWidth(t *testing.T) {
	indexes, runes := rangeOverStringYieldsRunes("héllo")

	wantIndexes := []int{0, 1, 3, 4, 5} // é occupies bytes 1 and 2
	if len(indexes) != len(wantIndexes) {
		t.Fatalf("got %d indexes, want %d", len(indexes), len(wantIndexes))
	}
	for i := range wantIndexes {
		if indexes[i] != wantIndexes[i] {
			t.Errorf("index %d = %d, want %d", i, indexes[i], wantIndexes[i])
		}
	}
	if string(runes) != "héllo" {
		t.Errorf("runes rebuild to %q, want %q", string(runes), "héllo")
	}
}

func TestNumberFromString(t *testing.T) {
	tests := []struct {
		in      string
		want    int
		wantErr bool
	}{
		{"42", 42, false},
		{"-7", -7, false},
		{"0", 0, false},
		{"4x2", 0, true},
		{"", 0, true},
		{"3.5", 0, true}, // Atoi is integers only
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := numberFromString(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Atoi(%q) error = %v, wantErr %t", tt.in, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("Atoi(%q) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}
