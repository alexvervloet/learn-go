package main

import (
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
)

func TestUpperReader(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"lowercase", "transform me", "TRANSFORM ME"},
		{"mixed", "MiXeD CaSe", "MIXED CASE"},
		{"already upper", "ALREADY", "ALREADY"},
		{"digits and punctuation untouched", "a1b2!", "A1B2!"},
		{"empty", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := io.ReadAll(NewUpperReader(strings.NewReader(tt.in)))
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			if string(got) != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// TestUpperReaderWorksAcrossChunkBoundaries: a transforming reader must be
// correct for any chunking, because it does not control the buffer size.
func TestUpperReaderWorksAcrossChunkBoundaries(t *testing.T) {
	const payload = "the quick brown fox jumps over the lazy dog"

	for _, bufSize := range []int{1, 2, 3, 7, 16, 1000} {
		r := NewUpperReader(strings.NewReader(payload))

		var out []byte
		buf := make([]byte, bufSize)
		for {
			n, err := r.Read(buf)
			out = append(out, buf[:n]...)
			if err != nil {
				break
			}
		}

		if want := strings.ToUpper(payload); string(out) != want {
			t.Errorf("buffer size %d gave %q, want %q", bufSize, out, want)
		}
	}
}

// TestUpperReaderDoesNotWrapEOF: wrapping io.EOF would break every caller that
// checks for it.
func TestUpperReaderPassesEOFThrough(t *testing.T) {
	r := NewUpperReader(strings.NewReader(""))

	n, err := r.Read(make([]byte, 8))
	if n != 0 {
		t.Errorf("n = %d, want 0", n)
	}
	if err != io.EOF { //nolint:errorlint // asserting the exact sentinel, unwrapped
		t.Errorf("err = %v, want io.EOF exactly (not wrapped)", err)
	}
}

func TestLineCountingWriter(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantLines int
		wantBytes int
	}{
		{"three lines", "one\ntwo\nthree\n", 3, 14},
		{"no trailing newline", "one\ntwo", 1, 7},
		{"empty", "", 0, 0},
		{"only newlines", "\n\n\n", 3, 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := NewLineCountingWriter(&plainWriter{})

			if _, err := io.Copy(w, strings.NewReader(tt.input)); err != nil {
				t.Fatalf("copy: %v", err)
			}
			if w.Lines != tt.wantLines {
				t.Errorf("lines = %d, want %d", w.Lines, tt.wantLines)
			}
			if w.Bytes != tt.wantBytes {
				t.Errorf("bytes = %d, want %d", w.Bytes, tt.wantBytes)
			}
		})
	}
}

// TestLineCountingWriterCountsOnlyWhatLanded: on a partial forward it must not
// count bytes the underlying writer refused.
func TestLineCountingWriterCountsOnlyWhatLanded(t *testing.T) {
	w := NewLineCountingWriter(&correctWriter{capacity: 4})

	n, err := w.Write([]byte("a\nb\nc\n"))

	if err == nil {
		t.Fatal("expected the underlying writer to fail")
	}
	if n > 4 {
		t.Errorf("reported %d bytes written, but the writer only accepted 4", n)
	}
	if w.Bytes > 4 {
		t.Errorf("counted %d bytes, want at most 4", w.Bytes)
	}
}

// TestRetainingWriterBreaksUnderReuse asserts the "never retain p" bug, which
// is invisible until the caller reuses its buffer, which io.Copy always does.
func TestRetainingWriterBreaksUnderReuse(t *testing.T) {
	retained, copied := retainingBreaksUnderReuse("abcdefghij", 3)

	if want := []string{"abc", "def", "ghi", "j"}; !slices.Equal(copied, want) {
		t.Errorf("the copying writer gave %v, want %v", copied, want)
	}

	if slices.Equal(retained, copied) {
		t.Error("the retaining writer produced correct output — the bug is gone")
	}

	// Every retained chunk points at the same reused buffer, so they all show
	// the last content written into it.
	t.Logf("retained: %v", retained)
	if len(retained) < 2 {
		t.Fatalf("expected several chunks, got %v", retained)
	}
	if retained[0] == "abc" {
		t.Error("the first retained chunk should have been overwritten by later reuse")
	}
}

func TestWordCountReader(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want int
	}{
		{"five words", "the quick brown fox jumps", 5},
		{"one word", "single", 1},
		{"empty", "", 0},
		{"only spaces", "   \n\t ", 0},
		{"leading and trailing space", "  two words  ", 2},
		{"multiple separators", "a\n\nb\t\tc", 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wc := NewWordCountReader(strings.NewReader(tt.in))

			if _, err := io.ReadAll(wc); err != nil {
				t.Fatalf("read: %v", err)
			}
			if wc.Words != tt.want {
				t.Errorf("words = %d, want %d", wc.Words, tt.want)
			}
			if !wc.Finished {
				t.Error("Finished should be set once EOF is seen")
			}
		})
	}
}

// TestWordCountReaderIsChunkIndependent: like the upper reader, it must give
// the same answer whatever the buffer size.
func TestWordCountReaderIsChunkIndependent(t *testing.T) {
	const payload = "the quick brown fox jumps over the lazy dog"

	for _, bufSize := range []int{1, 2, 5, 13, 1000} {
		wc := NewWordCountReader(strings.NewReader(payload))

		buf := make([]byte, bufSize)
		for {
			if _, err := wc.Read(buf); err != nil {
				break
			}
		}

		if wc.Words != 9 {
			t.Errorf("buffer size %d counted %d words, want 9", bufSize, wc.Words)
		}
	}
}

// TestErrorsPropagateThroughWrappers: a failure at the bottom of a stack of
// decorators must reach the top unchanged, so errors.Is keeps working.
func TestErrorsPropagateThroughWrappers(t *testing.T) {
	boom := errors.New("disk failure")

	read, err := errorsPropagateThroughWrappers(boom)

	if !errors.Is(err, boom) {
		t.Errorf("err = %v, want it to be %v through three wrappers", err, boom)
	}
	if read != "AAAAA" {
		t.Errorf("read = %q, want the 5 bytes produced before the failure", read)
	}
}

func TestErrorAfterReader(t *testing.T) {
	boom := errors.New("boom")
	r := NewErrorAfterReader("0123456789", 4, boom)

	buf := make([]byte, 10)

	n, err := r.Read(buf)
	if n != 4 || err != nil {
		t.Fatalf("first read = %d, %v; want 4, nil", n, err)
	}
	if string(buf[:n]) != "0123" {
		t.Errorf("read %q, want 0123", buf[:n])
	}

	n, err = r.Read(buf)
	if n != 0 || !errors.Is(err, boom) {
		t.Errorf("second read = %d, %v; want 0, boom", n, err)
	}
}

func TestImplementationRulesAreDocumented(t *testing.T) {
	if got := implementationRules(); len(got) < 5 {
		t.Errorf("expected at least 5 rules, got %d", len(got))
	}
}
