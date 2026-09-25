package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestLifoOrder(t *testing.T) {
	var order []string
	lifoOrderObserved(&order)

	want := []string{"function body", "third deferred", "second deferred", "first deferred"}
	if !slices.Equal(order, want) {
		t.Errorf("order = %v, want %v", order, want)
	}
}

// TestArgumentsEvaluatedAtDeferTime is the rule people get wrong. Both defers
// see the same variable; only one of them reads it late.
func TestArgumentsEvaluatedAtDeferTime(t *testing.T) {
	copied, captured := argumentsEvaluatedAtDeferTime()

	if copied != 0 {
		t.Errorf("deferred call argument = %d, want 0 — it is evaluated at defer time", copied)
	}
	if captured != 42 {
		t.Errorf("deferred closure saw %d, want 42 — it reads the variable when it runs", captured)
	}
}

// TestDeferInALoopPilesUp asserts the leak, so the example cannot quietly stop
// being wrong.
func TestDeferInALoopPilesUp(t *testing.T) {
	paths := writeTempFiles(t, 5)

	t.Run("defer in the loop body holds every file open", func(t *testing.T) {
		if got := deferInALoopPilesUp(paths); got != len(paths) {
			t.Errorf("peak open files = %d, want %d", got, len(paths))
		}
	})

	t.Run("defer in a nested function holds one at a time", func(t *testing.T) {
		if got := deferInALoopFixed(paths); got != 1 {
			t.Errorf("peak open files = %d, want 1", got)
		}
	})
}

func TestDeferRunsOnEveryPath(t *testing.T) {
	tests := []struct {
		mode string
		want string
	}{
		{"normal", "returned normally"},
		{"early", "returned early"},
		{"error", "returned an error"},
		{"panic", "recovered from boom"},
	}

	for _, tt := range tests {
		t.Run(tt.mode, func(t *testing.T) {
			path, _ := deferRunsOnEveryPath(tt.mode)
			if path != tt.want {
				t.Errorf("path = %q, want %q", path, tt.want)
			}
		})
	}
}

// writeTempFiles creates n files in a directory the test framework cleans up.
// t.TempDir is removed automatically, including when the test panics, which
// os.MkdirTemp plus a defer is not.
func writeTempFiles(t *testing.T, n int) []string {
	t.Helper()

	dir := t.TempDir()
	paths := make([]string, 0, n)

	for i := 0; i < n; i++ {
		p := filepath.Join(dir, "f"+string(rune('0'+i))+".txt")
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
		paths = append(paths, p)
	}

	return paths
}
