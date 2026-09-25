package main

import (
	"bytes"
	"compress/gzip"
	"io"
	"strings"
	"testing"
)

// TestReportToABuffer is the ordinary case, and the reason Report takes an
// io.Writer: the test needs no filesystem, no cleanup, and no temp directory.
func TestReportToABuffer(t *testing.T) {
	var buf bytes.Buffer

	if err := Report(&buf, "Title", []string{"one", "two"}); err != nil {
		t.Fatalf("Report: %v", err)
	}

	want := "Title\n=====\n 1. one\n 2. two\n"
	if got := buf.String(); got != want {
		t.Errorf("Report wrote:\n%q\nwant:\n%q", got, want)
	}
}

func TestReportEdgeCases(t *testing.T) {
	tests := []struct {
		name  string
		title string
		rows  []string
		want  string
	}{
		{"no rows", "T", nil, "T\n=\n"},
		{"empty title", "", []string{"a"}, "\n\n 1. a\n"},
		{"ten rows align", "T", make([]string, 10), "T\n=\n 1. \n 2. \n 3. \n 4. \n 5. \n 6. \n 7. \n 8. \n 9. \n10. \n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := Report(&buf, tt.title, tt.rows); err != nil {
				t.Fatalf("Report: %v", err)
			}
			if got := buf.String(); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// TestReportPropagatesWriteErrors is the path that a real filesystem test
// cannot reach without filling a disk. A four-line writer reaches it directly.
func TestReportPropagatesWriteErrors(t *testing.T) {
	tests := []struct {
		name      string
		allow     int
		wantStage string
	}{
		{"fails on the title", 0, "write title"},
		{"fails partway through the title", 3, "write title"},
		{"fails on a row", 20, "write row"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Report(&failingWriter{allow: tt.allow}, "Title", []string{"one", "two", "three"})

			if err == nil {
				t.Fatal("expected the write failure to propagate")
			}
			if !strings.Contains(err.Error(), tt.wantStage) {
				t.Errorf("error %q should name the stage %q", err, tt.wantStage)
			}
			// The wrapped cause must survive to the caller.
			if !strings.Contains(err.Error(), "device full") {
				t.Errorf("error %q lost the underlying cause", err)
			}
		})
	}
}

// TestCountingWriterDecorates checks the decorator forwards correctly and
// counts what it forwarded.
func TestCountingWriterDecorates(t *testing.T) {
	var inner bytes.Buffer
	cw := &countingWriter{next: &inner}

	if err := Report(cw, "Title", []string{"one", "two"}); err != nil {
		t.Fatalf("Report: %v", err)
	}

	want := "Title\n=====\n 1. one\n 2. two\n"
	if inner.String() != want {
		t.Errorf("wrapped writer received %q, want %q", inner.String(), want)
	}
	if cw.bytes != len(want) {
		t.Errorf("counted %d bytes, want %d", cw.bytes, len(want))
	}
	if cw.lines != 4 {
		t.Errorf("counted %d lines, want 4", cw.lines)
	}
}

// TestCountingWriterSatisfiesIOWriter proves the decorator is a drop-in: io.Copy
// accepts it without knowing anything about it.
func TestCountingWriterSatisfiesIOWriter(t *testing.T) {
	var inner bytes.Buffer
	cw := &countingWriter{next: &inner}

	var w io.Writer = cw // the assignment is the assertion

	n, err := io.Copy(w, strings.NewReader("hello\nworld\n"))
	if err != nil {
		t.Fatalf("io.Copy: %v", err)
	}
	if n != 12 {
		t.Errorf("io.Copy reported %d bytes, want 12", n)
	}
	if cw.lines != 2 {
		t.Errorf("counted %d lines, want 2", cw.lines)
	}
}

// TestGzipRoundTrips confirms the compressed output is real gzip, and records
// the fact that it is bigger than the input at this size.
func TestGzipRoundTrips(t *testing.T) {
	var raw bytes.Buffer
	if err := Report(&raw, "Interfaces", []string{"a", "b", "c"}); err != nil {
		t.Fatalf("Report: %v", err)
	}

	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	if err := Report(zw, "Interfaces", []string{"a", "b", "c"}); err != nil {
		t.Fatalf("Report to gzip: %v", err)
	}
	// Close flushes the final block and writes the trailer. Skipping it
	// truncates the stream, and the decompressor below would report
	// "unexpected EOF" rather than anything about the missing Close.
	if err := zw.Close(); err != nil {
		t.Fatalf("gzip Close: %v", err)
	}

	zr, err := gzip.NewReader(&compressed)
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	// A gzip reader's Close reports checksum and length mismatches, so the
	// error is worth checking even in a test. `defer zr.Close()` would discard
	// it, which errcheck flags.
	defer func() {
		if err := zr.Close(); err != nil {
			t.Errorf("gzip reader Close: %v", err)
		}
	}()

	got, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("read compressed: %v", err)
	}
	if string(got) != raw.String() {
		t.Errorf("round trip produced %q, want %q", got, raw.String())
	}
}

// TestSixDestinations runs the whole demo and checks each destination did what
// it claimed.
func TestSixDestinations(t *testing.T) {
	out, hash, gzipped, discarded, counted, failed := sameReportSixDestinations(
		"Interfaces", []string{"structural typing", "small interfaces", "accept interfaces"})

	if !strings.HasPrefix(out, "Interfaces\n==========\n") {
		t.Errorf("buffer output started with %q", out[:min(30, len(out))])
	}
	if len(hash) != 16 {
		t.Errorf("hash prefix has %d chars, want 16", len(hash))
	}
	if gzipped <= 0 {
		t.Error("gzip produced no output")
	}
	if discarded != nil {
		t.Errorf("io.Discard should never fail, got %v", discarded)
	}
	if counted[0] != len(out) {
		t.Errorf("counting writer saw %d bytes, buffer holds %d", counted[0], len(out))
	}
	if failed == nil {
		t.Error("the failing writer should have produced an error")
	}
}
