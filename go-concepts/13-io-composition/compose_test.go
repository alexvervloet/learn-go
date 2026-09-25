package main

import (
	"io"
	"strings"
	"testing"
)

func TestMultiReaderConcatenates(t *testing.T) {
	tests := []struct {
		name  string
		parts []string
		want  string
	}{
		{"three parts", []string{"a", "b", "c"}, "abc"},
		{"one part", []string{"only"}, "only"},
		{"no parts", nil, ""},
		{"empty parts are skipped", []string{"a", "", "b"}, "ab"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := multiReaderConcatenates(tt.parts...)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// TestPutBackTheFirstBytes is the practical MultiReader use: a reader cannot
// be rewound, so the consumed prefix is glued back on.
func TestPutBackTheFirstBytes(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		sniff        int
		wantDetected string
	}{
		{"json", `{"key": "value"}`, 1, "json"},
		{"xml", `<root/>`, 1, "xml"},
		{"yaml", "---\nkey: value", 3, "yaml"},
		{"unknown", "plain text", 1, "unknown"},
		{"input shorter than the sniff", "{", 10, "json"},
		{"empty input", "", 4, "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			detected, full, err := putBackTheFirstBytes(strings.NewReader(tt.input), tt.sniff)
			if err != nil {
				t.Fatalf("sniff: %v", err)
			}

			if detected != tt.wantDetected {
				t.Errorf("detected = %q, want %q", detected, tt.wantDetected)
			}
			// The whole input must survive the sniff.
			if full != tt.input {
				t.Errorf("full = %q, want the complete input %q", full, tt.input)
			}
		})
	}
}

func TestMultiWriterFansOut(t *testing.T) {
	buffer, digest, discarded, err := multiWriterFansOut("fan me out")

	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if buffer != "fan me out" {
		t.Errorf("buffer = %q, want the payload", buffer)
	}
	if len(digest) != 16 {
		t.Errorf("digest prefix = %q, want 16 chars", digest)
	}
	if !discarded {
		t.Error("io.Discard should have accepted the write")
	}
}

// TestMultiWriterStopsAtTheFirstFailure: a failing destination must not be
// silently skipped.
func TestMultiWriterStopsAtTheFirstFailure(t *testing.T) {
	err, goodGotSome := multiWriterStopsAtTheFirstFailure()

	if err == nil {
		t.Error("MultiWriter should report a destination's failure")
	}
	if !goodGotSome {
		t.Error("the working destination should still have received the write")
	}
}

func TestTeeReaderHashesWhileReading(t *testing.T) {
	const payload = "checksum me while streaming"

	consumed, digest, err := teeReaderHashesWhileReading(strings.NewReader(payload))
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	if consumed != payload {
		t.Errorf("consumed = %q, want the payload unchanged", consumed)
	}
	if len(digest) != 16 {
		t.Errorf("digest = %q, want 16 chars", digest)
	}

	// The tee must not alter the stream.
	direct, _, err := hashWhileCopying(strings.NewReader(payload))
	if err != nil {
		t.Fatalf("copy: %v", err)
	}
	if direct != int64(len(payload)) {
		t.Errorf("byte counts disagree: %d vs %d", direct, len(payload))
	}
}

// TestTeeOnlySeesWhatIsRead is the property people misread: the tee writer
// receives bytes as they are CONSUMED, so stopping early hashes only a prefix.
func TestTeeOnlySeesWhatIsRead(t *testing.T) {
	const source = "the full payload is longer than this"

	tests := []struct {
		readBytes int
	}{{1}, {10}, {len(source)}}

	for _, tt := range tests {
		hashed, err := teeOnlySeesWhatIsRead(source, tt.readBytes)
		if err != nil {
			t.Fatalf("read %d: %v", tt.readBytes, err)
		}
		if hashed != tt.readBytes {
			t.Errorf("reading %d bytes teed %d, want %d", tt.readBytes, hashed, tt.readBytes)
		}
	}
}

func TestLimitReader(t *testing.T) {
	tests := []struct {
		name          string
		source        string
		limit         int64
		wantRead      string
		wantTruncated bool
	}{
		{"input longer than the limit", "a very long input indeed", 10, "a very lon", true},
		{"input shorter than the limit", "short", 10, "short", false},
		{"input exactly the limit", "0123456789", 10, "0123456789", false},
		{"one byte over", "01234567890", 10, "0123456789", true},
		{"zero limit", "anything", 0, "", true},
		{"empty input", "", 10, "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			read, truncated, err := limitReaderProtectsAgainstUnboundedInput(tt.source, tt.limit)

			if err != nil {
				t.Fatalf("read: %v", err)
			}
			if read != tt.wantRead {
				t.Errorf("read = %q, want %q", read, tt.wantRead)
			}
			if truncated != tt.wantTruncated {
				t.Errorf("truncated = %t, want %t", truncated, tt.wantTruncated)
			}
		})
	}
}

// TestLimitReaderBoundary is the case the limit+1 trick exists for: an input
// of exactly the limit must not be reported as truncated.
func TestLimitReaderBoundaryIsExact(t *testing.T) {
	exact := strings.Repeat("x", 100)

	read, truncated, err := limitReaderProtectsAgainstUnboundedInput(exact, 100)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if truncated {
		t.Error("an input of exactly the limit must not report truncation")
	}
	if len(read) != 100 {
		t.Errorf("read %d bytes, want 100", len(read))
	}

	oneMore := exact + "x"
	_, truncated, _ = limitReaderProtectsAgainstUnboundedInput(oneMore, 100)
	if !truncated {
		t.Error("one byte over the limit must report truncation")
	}
}

func TestSectionReader(t *testing.T) {
	const source = "0123456789abcdef"

	tests := []struct {
		name           string
		offset, length int64
		want           string
	}{
		{"middle window", 4, 6, "456789"},
		{"from the start", 0, 4, "0123"},
		{"to the end", 12, 4, "cdef"},
		{"length past the end", 12, 100, "cdef"},
		{"offset past the end", 100, 4, ""},
		{"zero length", 4, 0, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := sectionReaderWindowsOverAReaderAt(source, tt.offset, tt.length)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNopCloser(t *testing.T) {
	data, closeErr := nopCloserAddsAClose("wrapped")

	if data != "wrapped" {
		t.Errorf("data = %q, want wrapped", data)
	}
	if closeErr != nil {
		t.Errorf("NopCloser.Close = %v, want nil", closeErr)
	}

	// It must satisfy io.ReadCloser, which is the whole point. The explicit
	// type IS the assertion: if NopCloser's signature ever narrowed, this
	// line would stop compiling.
	var _ io.ReadCloser = io.NopCloser(strings.NewReader("")) //nolint:staticcheck // the explicit type is the assertion
}
