package main

import (
	"bufio"
	"errors"
	"slices"
	"strings"
	"testing"
)

// TestBufioCollapsesSyscalls is the whole reason bufio exists.
func TestBufioCollapsesSyscalls(t *testing.T) {
	const size = 4096

	unbufferedBytes, unbufferedCalls, err := readByteAtATime(
		&syscallCountingReader{data: strings.Repeat("x", size)})
	if err != nil {
		t.Fatalf("unbuffered: %v", err)
	}

	bufferedBytes, bufferedCalls, err := readBufferedByteAtATime(
		&syscallCountingReader{data: strings.Repeat("x", size)})
	if err != nil {
		t.Fatalf("buffered: %v", err)
	}

	if unbufferedBytes != size || bufferedBytes != size {
		t.Fatalf("byte counts disagree: %d and %d, want %d", unbufferedBytes, bufferedBytes, size)
	}

	t.Logf("%d Read calls unbuffered, %d buffered", unbufferedCalls, bufferedCalls)

	if unbufferedCalls < int64(size) {
		t.Errorf("unbuffered made %d calls for %d bytes, want at least one per byte", unbufferedCalls, size)
	}
	if bufferedCalls > 10 {
		t.Errorf("buffered made %d calls, want a handful", bufferedCalls)
	}
}

func TestBufioCollapsesWrites(t *testing.T) {
	const writes = 4096

	var a syscallCountingWriter
	unbuffered, err := writeManySmallWrites(&a, writes)
	if err != nil {
		t.Fatalf("unbuffered: %v", err)
	}

	var b syscallCountingWriter
	buffered, err := writeBuffered(&b, writes)
	if err != nil {
		t.Fatalf("buffered: %v", err)
	}

	if len(a.written) != writes || len(b.written) != writes {
		t.Fatalf("byte counts disagree: %d and %d, want %d", len(a.written), len(b.written), writes)
	}

	if unbuffered != int64(writes) {
		t.Errorf("unbuffered made %d Write calls, want %d", unbuffered, writes)
	}
	if buffered > 10 {
		t.Errorf("buffered made %d Write calls, want a handful", buffered)
	}
}

// TestForgettingToFlushLosesData asserts the most common bufio bug.
func TestForgettingToFlushLosesData(t *testing.T) {
	const payload = "this fits in the buffer"

	withoutFlush, withFlush := forgettingToFlushLosesData(payload)

	if withoutFlush != 0 {
		t.Errorf("%d bytes reached the writer without a Flush, want 0", withoutFlush)
	}
	if withFlush != len(payload) {
		t.Errorf("%d bytes reached the writer with a Flush, want %d", withFlush, len(payload))
	}
}

func TestScanLines(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{"three lines", "alpha\nbeta\ngamma\n", []string{"alpha", "beta", "gamma"}},
		{"no trailing newline", "alpha\nbeta", []string{"alpha", "beta"}},
		{"empty input", "", nil},
		{"just a newline", "\n", []string{""}},
		{"blank lines are kept", "a\n\nb\n", []string{"a", "", "b"}},
		{"carriage returns are stripped", "a\r\nb\r\n", []string{"a", "b"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := scanLines(strings.NewReader(tt.input))
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// TestScannerTokenLimit is the sharp edge: a long line stops the scan, and only
// scanner.Err() says so.
func TestScannerTokenLimit(t *testing.T) {
	t.Run("a line under 64KB is fine", func(t *testing.T) {
		lines, err := scannerHasAMaximumTokenSize(60_000)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if lines != 2 {
			t.Errorf("read %d lines, want 2", lines)
		}
	})

	t.Run("a line over 64KB stops the scan", func(t *testing.T) {
		lines, err := scannerHasAMaximumTokenSize(70_000)

		if err == nil {
			t.Fatal("expected bufio.ErrTooLong")
		}
		if !errors.Is(err, bufio.ErrTooLong) {
			t.Errorf("err = %v, want bufio.ErrTooLong", err)
		}
		// The second line is never reached, which is the silent-data-loss
		// failure mode when Err() is not checked.
		if lines != 0 {
			t.Errorf("read %d lines before failing, want 0", lines)
		}
	})

	t.Run("a bigger buffer raises the limit", func(t *testing.T) {
		lines, err := scannerWithABiggerBuffer(70_000, 1<<20)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if lines != 2 {
			t.Errorf("read %d lines, want 2", lines)
		}
	})

	t.Run("a bigger buffer still has a limit", func(t *testing.T) {
		_, err := scannerWithABiggerBuffer(200_000, 100_000)
		if !errors.Is(err, bufio.ErrTooLong) {
			t.Errorf("err = %v, want bufio.ErrTooLong", err)
		}
	})
}

func TestSplitFunctions(t *testing.T) {
	lines, words, runes := splitFunctions("one two\nthree")

	if lines != 2 {
		t.Errorf("lines = %d, want 2", lines)
	}
	if words != 3 {
		t.Errorf("words = %d, want 3", words)
	}
	if runes != len("one two\nthree") {
		t.Errorf("runes = %d, want %d", runes, len("one two\nthree"))
	}
}

// TestReaderHasNoTokenLimit: bufio.Reader.ReadString handles arbitrarily long
// lines, which is the answer when Scanner's limit is a problem.
func TestReaderHasNoTokenLimit(t *testing.T) {
	scannerLines, readerLines, scannerErr := readerVsScannerForLongLines(70_000)

	if !errors.Is(scannerErr, bufio.ErrTooLong) {
		t.Errorf("Scanner err = %v, want bufio.ErrTooLong", scannerErr)
	}
	if scannerLines != 0 {
		t.Errorf("Scanner read %d lines, want 0", scannerLines)
	}
	if readerLines != 2 {
		t.Errorf("bufio.Reader read %d lines, want 2", readerLines)
	}
}

func BenchmarkReadUnbuffered(b *testing.B) {
	data := strings.Repeat("x", 8192)

	for b.Loop() {
		_, _, _ = readByteAtATime(&syscallCountingReader{data: data})
	}
}

func BenchmarkReadBuffered(b *testing.B) {
	data := strings.Repeat("x", 8192)

	for b.Loop() {
		_, _, _ = readBufferedByteAtATime(&syscallCountingReader{data: data})
	}
}
