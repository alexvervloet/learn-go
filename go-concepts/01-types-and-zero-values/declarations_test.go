package main

import "testing"

// TestShadowingTrap asserts the bug, not the fix. A test that pins down broken
// behaviour is how you keep a teaching example honest: if a future Go release
// ever made this an error, this test would fail and the README would need
// rewriting.
func TestShadowingTrap(t *testing.T) {
	if err := shadowingTrap(); err != nil {
		t.Errorf("shadowingTrap() = %v, want nil — the shadowed error should be lost", err)
	}
	if err := shadowingFixed(); err == nil {
		t.Error("shadowingFixed() = nil, want an error")
	}
}

func TestSwapWithoutTemp(t *testing.T) {
	tests := []struct {
		name         string
		a, b         int
		wantA, wantB int
	}{
		{"distinct values", 1, 2, 2, 1},
		{"equal values", 5, 5, 5, 5},
		{"zero and negative", 0, -3, -3, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotA, gotB := swapWithoutTemp(tt.a, tt.b)
			if gotA != tt.wantA || gotB != tt.wantB {
				t.Errorf("swapWithoutTemp(%d, %d) = %d, %d; want %d, %d",
					tt.a, tt.b, gotA, gotB, tt.wantA, tt.wantB)
			}
		})
	}
}

func TestMultipleAssignment(t *testing.T) {
	n, s, err := multipleAssignment()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 42 || s != "answer" {
		t.Errorf("got %d, %q; want 42, \"answer\"", n, s)
	}
	if got := blankIdentifier(); got != 42 {
		t.Errorf("blankIdentifier() = %d, want 42", got)
	}
}
