package main

import (
	"errors"
	"io"
	"strings"
	"testing"
)

// TestCopyTakesTheFastPath is the claim that makes io.Copy worth using over a
// hand-written loop.
func TestCopyTakesTheFastPath(t *testing.T) {
	slowReads, writeToCalls, readFromCalls := whichPathDidCopyTake()

	if slowReads < 2 {
		t.Errorf("the no-fast-path copy made %d Read calls, want at least 2 (data plus EOF)", slowReads)
	}
	if writeToCalls != 1 {
		t.Errorf("WriteTo called %d times, want exactly 1 — io.Copy should prefer it", writeToCalls)
	}
	if readFromCalls != 1 {
		t.Errorf("ReadFrom called %d times, want exactly 1", readFromCalls)
	}
}

// TestWriterToBypassesRead: when the fast path is taken, Read is never called.
func TestWriterToBypassesRead(t *testing.T) {
	src := &writerToReader{data: "payload"}
	var dst plainWriter

	n, err := io.Copy(&dst, src)
	if err != nil {
		t.Fatalf("copy: %v", err)
	}
	if n != int64(len("payload")) {
		t.Errorf("copied %d bytes, want %d", n, len("payload"))
	}
	if string(dst.buf) != "payload" {
		t.Errorf("destination holds %q, want %q", dst.buf, "payload")
	}
	if got := src.writeToes.Load(); got != 1 {
		t.Errorf("WriteTo called %d times, want 1", got)
	}
}

// TestWrappingHidesFastPaths is the cost of writing a decorator without
// thinking about it: the wrapped type's optimisations disappear.
func TestWrappingHidesFastPaths(t *testing.T) {
	directIsFast, wrappedIsFast := wrappingHidesFastPaths()

	if !directIsFast {
		t.Error("strings.Reader should implement io.WriterTo")
	}
	if wrappedIsFast {
		t.Error("a decorator forwarding only Read should NOT implement io.WriterTo")
	}
}

func TestCopyBuffer(t *testing.T) {
	total, err := copyBufferReusesABuffer([]string{"one", "two", "three"})

	if err != nil {
		t.Fatalf("copy: %v", err)
	}
	if want := int64(len("onetwothree")); total != want {
		t.Errorf("total = %d, want %d", total, want)
	}
}

func TestCopyBufferOnNoSources(t *testing.T) {
	total, err := copyBufferReusesABuffer(nil)

	if err != nil {
		t.Fatalf("copy: %v", err)
	}
	if total != 0 {
		t.Errorf("total = %d, want 0", total)
	}
}

// TestCopyNDistinguishesLimitFromEOF: hitting the limit and running out of
// input are different outcomes, and CopyN reports them differently.
func TestCopyN(t *testing.T) {
	tests := []struct {
		name         string
		source       string
		limit        int64
		wantCopied   int64
		wantHitLimit bool
	}{
		{"source longer than the limit", "a much longer payload", 10, 10, true},
		{"source shorter than the limit", "short", 100, 5, false},
		{"exactly the limit", "12345", 5, 5, true},
		{"zero limit", "anything", 0, 0, true},
		{"empty source", "", 10, 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			copied, hitLimit, err := copyNStopsEarly(tt.source, tt.limit)

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if copied != tt.wantCopied {
				t.Errorf("copied = %d, want %d", copied, tt.wantCopied)
			}
			if hitLimit != tt.wantHitLimit {
				t.Errorf("hitLimit = %t, want %t", hitLimit, tt.wantHitLimit)
			}
		})
	}
}

func TestHashWhileCopying(t *testing.T) {
	const payload = "stream me"

	written, digest, err := hashWhileCopying(strings.NewReader(payload))
	if err != nil {
		t.Fatalf("copy: %v", err)
	}
	if written != int64(len(payload)) {
		t.Errorf("written = %d, want %d", written, len(payload))
	}
	if len(digest) != 64 {
		t.Errorf("digest has %d hex chars, want 64", len(digest))
	}

	// The same input must always produce the same digest.
	_, again, err := hashWhileCopying(strings.NewReader(payload))
	if err != nil {
		t.Fatalf("copy: %v", err)
	}
	if digest != again {
		t.Errorf("digests differ between runs: %s and %s", digest, again)
	}

	// And a different input must not.
	_, different, err := hashWhileCopying(strings.NewReader(payload + "!"))
	if err != nil {
		t.Fatalf("copy: %v", err)
	}
	if digest == different {
		t.Error("different inputs produced the same digest")
	}
}

// TestCopyPropagatesReadErrors: a failure in the source must reach the caller
// rather than looking like a short copy.
func TestCopyPropagatesReadErrors(t *testing.T) {
	boom := errors.New("network reset")

	var dst plainWriter
	n, err := io.Copy(&dst, NewErrorAfterReader("aaaaaaaaaa", 4, boom))

	if !errors.Is(err, boom) {
		t.Errorf("err = %v, want %v", err, boom)
	}
	if n != 4 {
		t.Errorf("copied %d bytes before the failure, want 4", n)
	}
}

// BenchmarkCopyFastPath vs BenchmarkCopySlowPath quantifies what the interface
// check buys. Run:
//
//	go test -bench BenchmarkCopy -benchmem -run '^$' ./13-io-composition
func BenchmarkCopyFastPath(b *testing.B) {
	payload := strings.Repeat("x", 64*1024)

	for b.Loop() {
		var dst plainWriter
		_, _ = io.Copy(&dst, strings.NewReader(payload)) // strings.Reader has WriteTo
	}
}

func BenchmarkCopySlowPath(b *testing.B) {
	payload := strings.Repeat("x", 64*1024)

	for b.Loop() {
		var dst plainWriter
		// The wrapper hides WriteTo, forcing the 32KB buffer loop.
		_, _ = io.Copy(&dst, &plainReader{r: strings.NewReader(payload)})
	}
}

func BenchmarkCopyBufferReused(b *testing.B) {
	payload := strings.Repeat("x", 64*1024)
	buf := make([]byte, 32*1024)

	for b.Loop() {
		var dst plainWriter
		_, _ = io.CopyBuffer(&dst, &plainReader{r: strings.NewReader(payload)}, buf)
	}
}
