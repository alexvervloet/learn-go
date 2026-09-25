package main

import (
	"errors"
	"io"
	"io/fs"
	"testing"
)

func TestIdenticalTextStillDiffers(t *testing.T) {
	sameText, sameValue := identicalTextStillDiffers()

	if !sameText {
		t.Error("the two errors should have identical messages")
	}
	if sameValue {
		t.Error("errors.New values must compare unequal even with matching text")
	}
}

// TestEqualityBreaksOnceWrapped is the bug that errors.Is exists to fix.
func TestEqualityBreaksOnceWrapped(t *testing.T) {
	withEq, withIs := equalityBreaksOnceWrapped(0)

	if withEq {
		t.Error("== should NOT match a wrapped sentinel — that is the whole problem")
	}
	if !withIs {
		t.Error("errors.Is should match a wrapped sentinel")
	}
}

func TestHTTPStatusFor(t *testing.T) {
	tests := []struct {
		name string
		id   int
		want int
	}{
		{"success", 1, 200},
		{"not found", 0, 404},
		{"not found, negative id", -5, 404},
		{"permission", 403, 403},
		{"conflict", 409, 409},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := httpStatusFor(fetchRecord(tt.id)); got != tt.want {
				t.Errorf("httpStatusFor(fetchRecord(%d)) = %d, want %d", tt.id, got, tt.want)
			}
		})
	}

	t.Run("unknown error falls through to 500", func(t *testing.T) {
		if got := httpStatusFor(errors.New("something else entirely")); got != 500 {
			t.Errorf("got %d, want 500", got)
		}
	})

	t.Run("status survives extra wrapping", func(t *testing.T) {
		// Three more layers of context must not change the classification.
		err := fetchRecord(0)
		for i := 0; i < 3; i++ {
			err = errors.Join(err)
		}
		if got := httpStatusFor(err); got != 404 {
			t.Errorf("got %d after extra wrapping, want 404", got)
		}
	})
}

func TestStdlibSentinels(t *testing.T) {
	isEOF, isNotExist := stdlibSentinels()

	if !isEOF {
		t.Error("errors.Is should find a wrapped io.EOF")
	}
	if !isNotExist {
		t.Error("errors.Is should find a wrapped fs.ErrNotExist")
	}

	// And the negative case, so the test is not trivially satisfiable.
	if errors.Is(fetchRecord(0), io.EOF) {
		t.Error("an unrelated error must not match io.EOF")
	}
	if errors.Is(fetchRecord(0), fs.ErrNotExist) {
		t.Error("an unrelated error must not match fs.ErrNotExist")
	}
}

func TestSentinelsAreDistinct(t *testing.T) {
	all := []error{ErrNotFound, ErrPermission, ErrConflict, ErrServerFault}

	for i, a := range all {
		for j, b := range all {
			if i == j {
				continue
			}
			if errors.Is(a, b) {
				t.Errorf("sentinel %v must not match %v", a, b)
			}
		}
	}
}
