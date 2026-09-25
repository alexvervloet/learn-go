// Package main is lesson 13 of go-concepts: io composition.
//
//	type Reader interface { Read(p []byte) (n int, err error) }
//	type Writer interface { Write(p []byte) (n int, err error) }
//
// The Read contract has one clause everyone gets wrong:
//
//	Read may return n > 0 AND err == io.EOF, in the same call.
//
// So a loop that checks the error before processing n bytes drops the last
// chunk of every stream that ends that way. Process n first, then the error.
package main

import (
	"errors"
	"fmt"
	"io"
	"strings"
)

// eofWithDataReader returns data and io.EOF in the SAME call, which is legal
// and is what strings.Reader, bytes.Reader and many network readers do.
//
// Most readers happen to return (n, nil) then (0, io.EOF), which is why the
// broken loop below usually works and fails only sometimes. That is the worst
// kind of bug.
type eofWithDataReader struct {
	data []byte
	pos  int
}

func (r *eofWithDataReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}

	n := copy(p, r.data[r.pos:])
	r.pos += n

	// On the LAST chunk, return n bytes AND io.EOF together. Earlier chunks
	// return (n, nil) as usual, so a broken loop appears to work until it
	// reaches the end, which is what makes the bug so hard to spot.
	if r.pos >= len(r.data) {
		return n, io.EOF
	}
	return n, nil
}

// readBroken checks the error before consuming n, so it loses everything the
// final call returned.
func readBroken(r io.Reader) string {
	var out []byte
	buf := make([]byte, 8)

	for {
		n, err := r.Read(buf)
		if err != nil {
			break // WRONG: the n bytes just read are discarded
		}
		out = append(out, buf[:n]...)
	}

	return string(out)
}

// readCorrect consumes n first, then interprets the error.
func readCorrect(r io.Reader) (string, error) {
	var out []byte
	buf := make([]byte, 8)

	for {
		n, err := r.Read(buf)

		// Rule 1: process what you were given, whatever the error says.
		if n > 0 {
			out = append(out, buf[:n]...)
		}

		if err != nil {
			// Rule 2: io.EOF is not a failure. It is how a reader finishes.
			if errors.Is(err, io.EOF) {
				return string(out), nil
			}
			return string(out), fmt.Errorf("read: %w", err)
		}
	}
}

// zeroNilReader returns (0, nil) a few times before producing anything. The
// contract permits it, callers must not treat it as EOF, and implementations
// should avoid it because a caller looping on it spins.
type zeroNilReader struct {
	stalls int
	data   string
	sent   bool
}

func (r *zeroNilReader) Read(p []byte) (int, error) {
	if r.stalls > 0 {
		r.stalls--
		return 0, nil // legal, and unhelpful
	}
	if r.sent {
		return 0, io.EOF
	}

	r.sent = true
	return copy(p, r.data), nil
}

// handlesZeroNil shows that the correct loop survives it, because it treats
// n == 0 as "nothing this time" rather than as the end.
func handlesZeroNil(r io.Reader) (data string, reads int, err error) {
	var out []byte
	buf := make([]byte, 16)

	for {
		n, rerr := r.Read(buf)
		reads++

		if n > 0 {
			out = append(out, buf[:n]...)
		}
		if rerr != nil {
			if errors.Is(rerr, io.EOF) {
				return string(out), reads, nil
			}
			return string(out), reads, rerr
		}
		if reads > 100 {
			return string(out), reads, errors.New("reader stalled")
		}
	}
}

// The Write contract
// ------------------
//
// Write must write ALL of p or return an error. Returning n < len(p) with a
// nil error breaks the contract, and io.Copy reports io.ErrShortWrite when it
// sees one.

// shortWriter returns fewer bytes than it was given, with no error. This is
// the broken shape.
type shortWriter struct {
	written []byte
}

func (w *shortWriter) Write(p []byte) (int, error) {
	// Accept half, claim success. A caller believing the contract now thinks
	// everything landed.
	half := len(p) / 2
	w.written = append(w.written, p[:half]...)
	return half, nil // WRONG: n < len(p) with err == nil
}

// correctWriter honours the contract: all or an error.
type correctWriter struct {
	written  []byte
	capacity int
}

func (w *correctWriter) Write(p []byte) (int, error) {
	remaining := w.capacity - len(w.written)

	if len(p) > remaining {
		w.written = append(w.written, p[:remaining]...)
		// A short write MUST come with an error saying why.
		return remaining, fmt.Errorf("writer full after %d bytes: %w", remaining, io.ErrShortWrite)
	}

	w.written = append(w.written, p...)
	return len(p), nil
}

// copyDetectsShortWrites: io.Copy checks, so a broken writer is caught at the
// boundary rather than silently truncating.
func copyDetectsShortWrites() (correctBytes int64, shortErr, correctErr error) {
	short := &shortWriter{}
	_, shortErr = io.Copy(short, strings.NewReader("hello world"))

	full := &correctWriter{capacity: 1000}
	correctBytes, correctErr = io.Copy(full, strings.NewReader("hello world"))

	return correctBytes, shortErr, correctErr
}

// eofIsNotAnError is the other half of the contract people misread. Wrapping
// io.EOF in failure context makes every successful read look like an error in
// the logs.
func eofIsNotAnError() (wrongMessage string, rightMessage string) {
	r := strings.NewReader("")

	buf := make([]byte, 8)
	_, err := r.Read(buf)

	// The wrong way: treat EOF as a failure.
	wrong := fmt.Errorf("failed to read stream: %w", err)

	// The right way: recognise it and stop.
	right := "read completed"
	if errors.Is(err, io.EOF) {
		right = "read completed: reached end of input"
	}

	return wrong.Error(), right
}

// demoContract prints the contract traps.
func demoContract() {
	const payload = "hello, contract"

	broken := readBroken(&eofWithDataReader{data: []byte(payload)})
	correct, err := readCorrect(&eofWithDataReader{data: []byte(payload)})

	fmt.Printf("  a reader returning (n > 0, io.EOF) together:\n")
	fmt.Printf("    error-first loop: %q   <- the LAST chunk is lost\n", broken)
	fmt.Printf("    n-first loop:     %q   (err=%v)\n", correct, err)

	data, reads, err := handlesZeroNil(&zeroNilReader{stalls: 3, data: "eventually"})
	fmt.Printf("\n  a reader returning (0, nil) three times: %q after %d reads (err=%v)\n",
		data, reads, err)
	fmt.Println("    ...n == 0 means \"nothing this time\", never \"the end\"")

	correctBytes, shortErr, correctErr := copyDetectsShortWrites()
	fmt.Printf("\n  io.Copy into a writer that short-writes: %v\n", shortErr)
	fmt.Printf("  io.Copy into a correct writer:           %d bytes, err=%v\n", correctBytes, correctErr)

	wrong, right := eofIsNotAnError()
	fmt.Printf("\n  EOF handled badly:  %s\n", wrong)
	fmt.Printf("  EOF handled well:   %s\n", right)
}
