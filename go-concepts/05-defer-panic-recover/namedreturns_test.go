package main

import (
	"errors"
	"strings"
	"testing"
)

func TestNamedReturnCanBeModified(t *testing.T) {
	if got := canModifyNamedReturn(); got != "ORIGINAL" {
		t.Errorf("canModifyNamedReturn() = %q, want %q", got, "ORIGINAL")
	}
	if got := cannotModifyUnnamedReturn(); got != "original" {
		t.Errorf("cannotModifyUnnamedReturn() = %q, want %q — the defer has no target", got, "original")
	}
}

func TestPanicToError(t *testing.T) {
	tests := []struct {
		name    string
		fn      func()
		wantErr bool
		wantMsg string
	}{
		{"no panic", func() {}, false, ""},
		{"panic with a string", func() { panic("boom") }, true, "recovered: boom"},
		{"panic with an int", func() { panic(42) }, true, "recovered: 42"},
		{"runtime panic", func() { var xs []int; _ = xs[1] }, true, "index out of range"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := panicToError(tt.fn)

			if (err != nil) != tt.wantErr {
				t.Fatalf("panicToError() = %v, wantErr %t", err, tt.wantErr)
			}
			if tt.wantMsg != "" && !strings.Contains(err.Error(), tt.wantMsg) {
				t.Errorf("error %q should contain %q", err, tt.wantMsg)
			}
		})
	}
}

// TestPanicToErrorPreservesErrorValues is why the recover checks for an error
// before formatting: errors.Is must keep working through a recovered panic.
func TestPanicToErrorPreservesErrorValues(t *testing.T) {
	sentinel := errors.New("the cause")

	err := panicToError(func() { panic(sentinel) })

	if err == nil {
		t.Fatal("expected an error")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("errors.Is should find the panicked error in %v", err)
	}
}

// TestPanicToErrorBrokenLosesIt asserts the bug. A recover without a named
// return is worse than no recover: the panic is swallowed and reported as
// success.
func TestPanicToErrorBrokenLosesIt(t *testing.T) {
	err := panicToErrorBroken(func() { panic("boom") })

	if err != nil {
		t.Errorf("panicToErrorBroken() = %v, want nil — the assignment goes nowhere", err)
	}
}

func TestProcessWithClose(t *testing.T) {
	diskFull := errors.New("no space left on device")
	brokenPipe := errors.New("broken pipe")

	tests := []struct {
		name      string
		closeErr  error
		writeErr  error
		wantErrs  []error
		wantCount int
	}{
		{"both succeed", nil, nil, nil, 1},
		{"close fails", diskFull, nil, []error{diskFull}, 1},
		{"write fails", nil, brokenPipe, []error{brokenPipe}, 1},
		{"both fail", diskFull, brokenPipe, []error{diskFull, brokenPipe}, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &resource{name: "test.txt", closeErr: tt.closeErr, writeErr: tt.writeErr}

			err := processWithClose(r, []string{"a", "b"})

			if len(tt.wantErrs) == 0 {
				if err != nil {
					t.Fatalf("expected nil, got %v", err)
				}
			}
			for _, want := range tt.wantErrs {
				if !errors.Is(err, want) {
					t.Errorf("errors.Is should find %v in %v", want, err)
				}
			}
			// Close must run exactly once on every path, including the ones
			// where the body already failed.
			if r.closeCalls != tt.wantCount {
				t.Errorf("Close called %d times, want %d", r.closeCalls, tt.wantCount)
			}
		})
	}
}

// TestProcessDiscardingCloseLosesTheError is the bug, asserted.
func TestProcessDiscardingCloseLosesTheError(t *testing.T) {
	diskFull := errors.New("no space left on device")
	r := &resource{name: "test.txt", closeErr: diskFull}

	err := processDiscardingClose(r, []string{"a"})

	if err != nil {
		t.Errorf("got %v, want nil — the close error is discarded", err)
	}
	if r.closeCalls != 1 {
		t.Errorf("Close called %d times, want 1", r.closeCalls)
	}
}
