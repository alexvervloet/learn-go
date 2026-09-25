package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// noSleep replaces time.Sleep so the retry tests run instantly. Injecting the
// clock is the difference between a 50ms test suite and a 5s one.
func noSleep(time.Duration) {}

func TestRetry(t *testing.T) {
	transient := func(failUntil int) attemptFunc {
		return func(n int) error {
			if n < failUntil {
				return &RetryableError{After: time.Millisecond, Err: errors.New("connection reset")}
			}
			return nil
		}
	}

	tests := []struct {
		name         string
		maxAttempts  int
		fn           attemptFunc
		wantErr      bool
		wantAttempts int
	}{
		{"succeeds first try", 5, transient(1), false, 1},
		{"succeeds on the third", 5, transient(3), false, 3},
		{"exhausts the budget", 3, transient(99), true, 3},
		{"single attempt allowed", 1, transient(99), true, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			attempts := 0
			err := retry(tt.maxAttempts, noSleep, func(n int) error {
				attempts = n
				return tt.fn(n)
			})

			if (err != nil) != tt.wantErr {
				t.Fatalf("retry() error = %v, wantErr %t", err, tt.wantErr)
			}
			if attempts != tt.wantAttempts {
				t.Errorf("made %d attempts, want %d", attempts, tt.wantAttempts)
			}
		})
	}
}

// TestRetryStopsOnNonRetryable is the behaviour that makes the classification
// worth the trouble: a validation failure must not be tried five times.
func TestRetryStopsOnNonRetryable(t *testing.T) {
	attempts := 0
	err := retry(5, noSleep, func(n int) error {
		attempts = n
		return &ValidationError{Field: fieldEmail, Value: "nope", Rule: ruleEmail}
	})

	if attempts != 1 {
		t.Errorf("made %d attempts, want 1 — a non-retryable error must stop the loop", attempts)
	}
	if err == nil {
		t.Fatal("expected the error to propagate")
	}

	// The original error stays reachable through the retry wrapper.
	var verr *ValidationError
	if !errors.As(err, &verr) {
		t.Errorf("retry lost the underlying error: %v", err)
	}
}

// TestRetryPreservesTheLastError: after giving up, the caller still needs to
// know what kept failing.
func TestRetryPreservesTheLastError(t *testing.T) {
	err := retry(3, noSleep, func(int) error {
		return &RetryableError{After: time.Millisecond, Err: ErrConflict}
	})

	if err == nil {
		t.Fatal("expected an error after exhausting attempts")
	}
	if !errors.Is(err, ErrConflict) {
		t.Errorf("errors.Is should still find ErrConflict in %v", err)
	}
	if !strings.Contains(err.Error(), "giving up after 3 attempts") {
		t.Errorf("message %q should say how many attempts were made", err)
	}
}

func TestRetryRejectsBadBudget(t *testing.T) {
	for _, n := range []int{0, -1} {
		called := false
		err := retry(n, noSleep, func(int) error {
			called = true
			return nil
		})
		if err == nil {
			t.Errorf("retry(%d) should reject the budget", n)
		}
		if called {
			t.Errorf("retry(%d) must not call fn", n)
		}
	}
}

// TestRetrySleepsForTheDurationTheErrorAsked kept the decision in the error,
// so check the loop actually honours it.
func TestRetryHonoursTheDelayFromTheError(t *testing.T) {
	var slept []time.Duration
	record := func(d time.Duration) { slept = append(slept, d) }

	_ = retry(3, record, func(n int) error {
		return &RetryableError{After: time.Duration(n) * time.Second, Err: ErrConflict}
	})

	// Two sleeps for three attempts: no sleep after the last one.
	want := []time.Duration{1 * time.Second, 2 * time.Second}
	if len(slept) != len(want) {
		t.Fatalf("slept %d times, want %d (%v)", len(slept), len(want), slept)
	}
	for i := range want {
		if slept[i] != want[i] {
			t.Errorf("sleep %d = %v, want %v", i, slept[i], want[i])
		}
	}
}

func TestWriteAndReadConfig(t *testing.T) {
	// t.TempDir is cleaned up automatically, including on failure. Prefer it
	// over os.MkdirTemp plus a defer, which loses the directory when the test
	// panics.
	path := filepath.Join(t.TempDir(), "config.toml")
	content := []byte("key = value\nother = 2\n")

	if err := writeConfig(path, content); err != nil {
		t.Fatalf("writeConfig: %v", err)
	}

	got, err := readConfig(path)
	if err != nil {
		t.Fatalf("readConfig: %v", err)
	}
	if string(got) != string(content) {
		t.Errorf("round trip gave %q, want %q", got, content)
	}
}

// TestReadConfigHandlesLongFiles exercises the read loop past one buffer, which
// is where the "consume n before checking the error" rule matters.
func TestReadConfigHandlesLongFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "long.txt")
	content := []byte(strings.Repeat("abcdefgh", 500)) // 4000 bytes, 32-byte buffer

	if err := writeConfig(path, content); err != nil {
		t.Fatalf("writeConfig: %v", err)
	}

	got, err := readConfig(path)
	if err != nil {
		t.Fatalf("readConfig: %v", err)
	}
	if len(got) != len(content) {
		t.Errorf("read %d bytes, want %d — the final partial chunk was likely dropped", len(got), len(content))
	}
	if string(got) != string(content) {
		t.Error("content mismatch")
	}
}

func TestConfigErrorsAreWrapped(t *testing.T) {
	t.Run("open a missing file", func(t *testing.T) {
		_, err := readConfig(filepath.Join(t.TempDir(), "nope.txt"))
		if err == nil {
			t.Fatal("expected an error")
		}
		if !errors.Is(err, os.ErrNotExist) {
			t.Errorf("errors.Is should find os.ErrNotExist in %v", err)
		}
		if !strings.HasPrefix(err.Error(), "open ") {
			t.Errorf("message %q should name the operation", err)
		}
	})

	t.Run("create in a missing directory", func(t *testing.T) {
		err := writeConfig(filepath.Join(t.TempDir(), "no-such-dir", "x.txt"), []byte("x"))
		if err == nil {
			t.Fatal("expected an error")
		}
		if !errors.Is(err, os.ErrNotExist) {
			t.Errorf("errors.Is should find os.ErrNotExist in %v", err)
		}
	})
}

func TestMustAbsolutePath(t *testing.T) {
	got := mustAbsolutePath(".")

	if !filepath.IsAbs(got) {
		t.Errorf("mustAbsolutePath(\".\") = %q, want an absolute path", got)
	}
}
