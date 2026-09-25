package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
)

// The composition toolkit
// =======================
//
//	io.MultiReader(a, b, c)      read a, then b, then c, as one stream
//	io.MultiWriter(a, b, c)      write to all three
//	io.TeeReader(r, w)           read from r, and write everything read to w
//	io.LimitReader(r, n)         stop after n bytes and report EOF
//	io.SectionReader(r, off, n)  a window over a ReaderAt
//	io.NopCloser(r)              add a no-op Close
//	io.Discard                   a Writer that drops everything
//
// All of these are a few lines each in the standard library. The value is not
// the implementation, it is that everything speaks the same two interfaces.

// multiReaderConcatenates joins several sources into one stream, which is how
// you prepend a header to a body without copying the body.
func multiReaderConcatenates(parts ...string) (string, error) {
	readers := make([]io.Reader, 0, len(parts))
	for _, p := range parts {
		readers = append(readers, strings.NewReader(p))
	}

	data, err := io.ReadAll(io.MultiReader(readers...))
	if err != nil {
		return "", fmt.Errorf("read all: %w", err)
	}
	return string(data), nil
}

// putBackTheFirstBytes is the practical use. Having read some bytes to sniff a
// format, you need the whole stream again, and a reader cannot be rewound.
// MultiReader glues the consumed prefix back on the front.
func putBackTheFirstBytes(r io.Reader, sniff int) (detected string, full string, err error) {
	prefix := make([]byte, sniff)

	n, err := io.ReadFull(r, prefix)
	// ErrUnexpectedEOF means the stream was shorter than the sniff; that is
	// fine, and the prefix holds whatever there was.
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return "", "", fmt.Errorf("sniff: %w", err)
	}
	prefix = prefix[:n]

	detected = "unknown"
	switch {
	case bytes.HasPrefix(prefix, []byte("{")):
		detected = "json"
	case bytes.HasPrefix(prefix, []byte("<")):
		detected = "xml"
	case bytes.HasPrefix(prefix, []byte("---")):
		detected = "yaml"
	}

	// The prefix, then whatever is left.
	rest, err := io.ReadAll(io.MultiReader(bytes.NewReader(prefix), r))
	if err != nil {
		return detected, "", fmt.Errorf("read rest: %w", err)
	}

	return detected, string(rest), nil
}

// multiWriterFansOut sends every byte to several destinations. It stops at the
// first failure and reports it, so a broken destination is not silently
// skipped.
func multiWriterFansOut(payload string) (buffer string, digest string, discarded bool, err error) {
	var buf bytes.Buffer
	h := sha256.New()

	w := io.MultiWriter(&buf, h, io.Discard)

	if _, err = io.WriteString(w, payload); err != nil {
		return "", "", false, fmt.Errorf("write: %w", err)
	}

	return buf.String(), hex.EncodeToString(h.Sum(nil))[:16], true, nil
}

// multiWriterStopsAtTheFirstFailure, which matters: a logging destination that
// fails should not silently swallow the failure of the real one.
func multiWriterStopsAtTheFirstFailure() (err error, goodGotEverything bool) {
	good := &plainWriter{}
	bad := &correctWriter{capacity: 4} // fails after 4 bytes

	w := io.MultiWriter(good, bad)
	_, err = io.WriteString(w, "much longer than four bytes")

	return err, len(good.buf) > 4
}

// teeReaderHashesWhileReading is the one to internalise. The consumer reads
// normally and the hash fills in as a side effect, in one pass, with no copy of
// the payload held anywhere.
//
// This is how you checksum an upload while streaming it to storage.
func teeReaderHashesWhileReading(source io.Reader) (consumed string, digest string, err error) {
	h := sha256.New()

	// Everything read from tee is also written to h.
	tee := io.TeeReader(source, h)

	data, err := io.ReadAll(tee)
	if err != nil {
		return "", "", fmt.Errorf("read: %w", err)
	}

	return string(data), hex.EncodeToString(h.Sum(nil))[:16], nil
}

// teeOnlySeesWhatIsRead is the property that surprises people: the tee writer
// receives bytes as they are CONSUMED, not as they exist. Stop reading early
// and the hash covers only the prefix.
func teeOnlySeesWhatIsRead(source string, readBytes int) (hashedBytes int, err error) {
	counter := &plainWriter{}
	tee := io.TeeReader(strings.NewReader(source), counter)

	buf := make([]byte, readBytes)
	if _, err = io.ReadFull(tee, buf); err != nil {
		return 0, fmt.Errorf("read: %w", err)
	}

	return len(counter.buf), nil
}

// limitReaderProtectsAgainstUnboundedInput. io.ReadAll on a request body is an
// allocation controlled by whoever is sending it, and LimitReader is the
// general-purpose guard. On an HTTP server, http.MaxBytesReader is better: it
// also sets a flag the server uses to return 413 rather than closing abruptly.
func limitReaderProtectsAgainstUnboundedInput(source string, limit int64) (read string, truncated bool, err error) {
	// The +1 is the trick: read one byte past the limit, and if it arrives the
	// input was too long. Without it, an input of exactly `limit` bytes is
	// indistinguishable from one that was cut off.
	data, err := io.ReadAll(io.LimitReader(strings.NewReader(source), limit+1))
	if err != nil {
		return "", false, fmt.Errorf("read: %w", err)
	}

	if int64(len(data)) > limit {
		return string(data[:limit]), true, nil
	}
	return string(data), false, nil
}

// sectionReaderWindowsOverAReaderAt gives a view of part of a source that
// supports random access, without copying. This is how an archive reader hands
// out one file from inside a zip.
func sectionReaderWindowsOverAReaderAt(source string, offset, length int64) (string, error) {
	// strings.Reader implements ReaderAt, which is what SectionReader needs.
	base := strings.NewReader(source)

	section := io.NewSectionReader(base, offset, length)

	data, err := io.ReadAll(section)
	if err != nil {
		return "", fmt.Errorf("read section: %w", err)
	}
	return string(data), nil
}

// nopCloserAddsAClose, for when a function demands a ReadCloser and you have a
// Reader that needs no closing. Common when replacing an http.Request.Body in
// a test or a middleware.
func nopCloserAddsAClose(payload string) (data string, closeErr error) {
	rc := io.NopCloser(strings.NewReader(payload))

	raw, _ := io.ReadAll(rc)
	return string(raw), rc.Close()
}

// demoCompose prints each combinator.
func demoCompose() {
	joined, err := multiReaderConcatenates("HEADER\n", "body line 1\n", "body line 2\n")
	fmt.Printf("  MultiReader of 3 parts (err=%v):\n", err)
	for _, line := range strings.Split(strings.TrimRight(joined, "\n"), "\n") {
		fmt.Printf("    | %s\n", line)
	}

	detected, full, err := putBackTheFirstBytes(strings.NewReader(`{"key": "value"}`), 1)
	fmt.Printf("\n  sniff 1 byte, then put it back: detected=%s full=%q (err=%v)\n", detected, full, err)

	detected, _, _ = putBackTheFirstBytes(strings.NewReader("---\nkey: value"), 3)
	fmt.Printf("  sniffing 3 bytes:               detected=%s\n", detected)

	buffer, digest, discarded, err := multiWriterFansOut("fan me out")
	fmt.Printf("\n  MultiWriter to a buffer, a hash and Discard:\n")
	fmt.Printf("    buffer=%q hash=%s... discard=%t (err=%v)\n", buffer, digest, discarded, err)

	failErr, goodGotSome := multiWriterStopsAtTheFirstFailure()
	fmt.Printf("    with one failing destination: err=%v, the good one got some: %t\n", failErr, goodGotSome)

	consumed, digest, err := teeReaderHashesWhileReading(strings.NewReader("checksum me while streaming"))
	fmt.Printf("\n  TeeReader: consumed %q\n    and hashed it in the same pass: %s... (err=%v)\n",
		consumed, digest, err)

	hashed, err := teeOnlySeesWhatIsRead("the full payload is longer than this", 10)
	fmt.Printf("  reading only 10 bytes teed %d bytes (err=%v)  <- the tee sees consumption\n", hashed, err)

	read, truncated, err := limitReaderProtectsAgainstUnboundedInput("a very long input indeed", 10)
	fmt.Printf("\n  LimitReader(10) on a 24-byte input: %q truncated=%t (err=%v)\n", read, truncated, err)

	read, truncated, _ = limitReaderProtectsAgainstUnboundedInput("short", 10)
	fmt.Printf("  LimitReader(10) on a 5-byte input:  %q truncated=%t\n", read, truncated)

	section, err := sectionReaderWindowsOverAReaderAt("0123456789abcdef", 4, 6)
	fmt.Printf("\n  SectionReader(offset 4, length 6): %q (err=%v)\n", section, err)

	data, closeErr := nopCloserAddsAClose("wrapped")
	fmt.Printf("  NopCloser: read %q, Close() = %v\n", data, closeErr)
}
