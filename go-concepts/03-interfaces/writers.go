package main

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
)

// io.Writer: what a one-method interface buys you
// ===============================================
//
//	type Writer interface {
//	    Write(p []byte) (n int, err error)
//	}
//
// One method. Because it is one method, the following all satisfy it, and every
// function below works with any of them without knowing which:
//
//	*os.File          a file, or stdout/stderr
//	net.Conn          a TCP socket
//	http.ResponseWriter   an HTTP response body
//	*bytes.Buffer     memory
//	*gzip.Writer      compression, wrapping another Writer
//	hash.Hash         sha256, md5, crc32
//	io.Discard        /dev/null
//	*strings.Builder  efficient string assembly
//
// Had io.Writer required Close, Flush and Sync as well, most of that list would
// have been excluded and every one of them would need stub methods. That is the
// concrete meaning of "the bigger the interface, the weaker the abstraction".

// Report is the function under discussion. It takes io.Writer, so it has no
// idea whether it is writing to a file, a socket, a hash, or a test buffer. It
// is testable without touching a filesystem, which is the real payoff.
//
// Note that it returns the error rather than logging or panicking: a writer can
// fail at any point, and only the caller knows what that means.
func Report(w io.Writer, title string, rows []string) error {
	if _, err := fmt.Fprintf(w, "%s\n%s\n", title, strings.Repeat("=", len(title))); err != nil {
		return fmt.Errorf("write title: %w", err)
	}
	for i, r := range rows {
		if _, err := fmt.Fprintf(w, "%2d. %s\n", i+1, r); err != nil {
			return fmt.Errorf("write row %d: %w", i, err)
		}
	}
	return nil
}

// countingWriter is a Writer that wraps another Writer. Decorating is the
// pattern a one-method interface makes trivial: implement the method, hold the
// next Writer, call through.
//
// This is exactly how gzip.Writer, bufio.Writer and httptest's recorders work.
type countingWriter struct {
	next  io.Writer
	bytes int
	lines int
}

// Write implements io.Writer. It must return the number of bytes it was given
// on success, not the number it forwarded, or callers computing progress will
// be wrong. Returning n < len(p) with a nil error violates the interface's
// contract and io.Copy will treat it as io.ErrShortWrite.
func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.next.Write(p)
	c.bytes += n
	c.lines += bytes.Count(p[:n], []byte{'\n'})
	return n, err
}

// failingWriter fails after allowing a fixed number of bytes through. Real
// writers fail partway: a disk fills, a socket closes. Testing that path needs
// a writer that misbehaves on demand, and writing one is four lines.
type failingWriter struct {
	allow int
}

// Write implements io.Writer, returning a short write and an error once the
// allowance is used up.
func (f *failingWriter) Write(p []byte) (int, error) {
	if f.allow <= 0 {
		return 0, fmt.Errorf("device full")
	}
	if len(p) > f.allow {
		n := f.allow
		f.allow = 0
		return n, fmt.Errorf("device full after %d bytes", n)
	}
	f.allow -= len(p)
	return len(p), nil
}

// sameReportSixDestinations runs one function against six writers to make the
// point concrete.
func sameReportSixDestinations(title string, rows []string) (buffer string, hash string, gzipped int, discarded error, counted [2]int, failed error) {
	// 1. memory
	var buf bytes.Buffer
	_ = Report(&buf, title, rows)

	// 2. a hash: sha256.New() returns a hash.Hash, which is an io.Writer
	h := sha256.New()
	_ = Report(h, title, rows)

	// 3. compression, wrapping memory
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	_ = Report(zw, title, rows)
	_ = zw.Close() // gzip buffers; without Close the output is truncated

	// 4. /dev/null
	discarded = Report(io.Discard, title, rows)

	// 5. a decorator over memory
	var inner bytes.Buffer
	cw := &countingWriter{next: &inner}
	_ = Report(cw, title, rows)

	// 6. a writer that fails partway
	failed = Report(&failingWriter{allow: 10}, title, rows)

	return buf.String(),
		hex.EncodeToString(h.Sum(nil))[:16],
		gz.Len(),
		discarded,
		[2]int{cw.bytes, cw.lines},
		failed
}

// demoWriters prints one function writing to six unrelated destinations.
func demoWriters() {
	rows := []string{"structural typing", "small interfaces", "accept interfaces"}

	out, hash, gzipped, discarded, counted, failed := sameReportSixDestinations("Interfaces", rows)

	fmt.Println("  Report(&bytes.Buffer{}, ...):")
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		fmt.Printf("    | %s\n", line)
	}
	fmt.Printf("  Report(sha256.New(), ...)  -> digest %s...\n", hash)
	// The compressed size is LARGER than the input here, and that is correct:
	// a gzip stream carries a 10-byte header, an 8-byte trailer and a Huffman
	// table, which an 87-byte payload cannot earn back. Compression is a
	// bet that only pays above a few hundred bytes, which is why net/http
	// does not gzip small responses.
	fmt.Printf("  Report(gzip.Writer, ...)   -> %d compressed bytes from %d raw (overhead wins at this size)\n", gzipped, len(out))
	fmt.Printf("  Report(io.Discard, ...)    -> err=%v\n", discarded)
	fmt.Printf("  Report(countingWriter, ..) -> %d bytes, %d lines\n", counted[0], counted[1])
	fmt.Printf("  Report(failingWriter, ...) -> %v\n", failed)
}
