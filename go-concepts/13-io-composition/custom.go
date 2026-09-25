package main

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode"
)

// Writing your own Reader and Writer
// ==================================
//
// Both interfaces are one method, so implementing them is easy and getting the
// CONTRACT right is the part that needs care. The rules, restated:
//
//	Read   return n > 0 whenever you have data, even alongside io.EOF
//	       never return (0, nil) in a loop: it makes callers spin
//	       report io.EOF when finished, not a custom "done" error
//
//	Write  write ALL of p, or return an error saying why
//	       return len(p) on success, not the count you forwarded
//	       never retain p: the caller may reuse it immediately

// upperReader is a transforming Reader: it uppercases as it reads, with no
// buffering of the whole stream.
//
// The subtlety in any transforming reader: the transform must be safe to apply
// to an ARBITRARY CHUNK. Uppercasing is, byte for byte, in ASCII. A transform
// that needs to see a whole token (decoding UTF-8, say, or parsing) cannot be
// written this way and needs a bufio.Reader underneath.
type upperReader struct {
	src io.Reader
}

// NewUpperReader wraps src.
func NewUpperReader(src io.Reader) io.Reader { return &upperReader{src: src} }

// Read implements io.Reader.
func (u *upperReader) Read(p []byte) (int, error) {
	n, err := u.src.Read(p)

	// Transform what we got, BEFORE returning, and regardless of err. The n
	// bytes are valid even when err is io.EOF.
	for i := 0; i < n; i++ {
		if p[i] >= 'a' && p[i] <= 'z' {
			p[i] -= 32
		}
	}

	// The error is passed through unchanged. Wrapping io.EOF here would break
	// every caller that checks for it.
	return n, err
}

// lineCountingWriter is a decorating Writer that counts lines as they pass.
type lineCountingWriter struct {
	dst   io.Writer
	Lines int
	Bytes int
}

// NewLineCountingWriter wraps dst.
func NewLineCountingWriter(dst io.Writer) *lineCountingWriter {
	return &lineCountingWriter{dst: dst}
}

// Write implements io.Writer.
//
// Note what it returns on a partial forward: n from the underlying writer, and
// the underlying error. Returning len(p) with an error would claim more was
// written than actually was, and io.Copy's accounting would be wrong.
func (w *lineCountingWriter) Write(p []byte) (int, error) {
	n, err := w.dst.Write(p)

	// Count only what actually landed.
	for _, b := range p[:n] {
		if b == '\n' {
			w.Lines++
		}
	}
	w.Bytes += n

	return n, err
}

// retainingWriter is the bug the "never retain p" rule exists for. It keeps
// the caller's slice instead of copying, so when the caller reuses the buffer
// (which io.Copy does, every iteration) the stored data changes underneath.
type retainingWriter struct {
	chunks [][]byte
}

func (w *retainingWriter) Write(p []byte) (int, error) {
	w.chunks = append(w.chunks, p) // WRONG: p belongs to the caller
	return len(p), nil
}

// copyingWriter does it correctly.
type copyingWriter struct {
	chunks [][]byte
}

func (w *copyingWriter) Write(p []byte) (int, error) {
	chunk := make([]byte, len(p))
	copy(chunk, p)
	w.chunks = append(w.chunks, chunk)
	return len(p), nil
}

// retainingBreaksUnderReuse demonstrates it. The caller reuses one buffer, as
// io.Copy does, and the retaining writer's stored chunks all end up pointing at
// the same memory holding the last chunk's content.
func retainingBreaksUnderReuse(payload string, chunkSize int) (retained []string, copied []string) {
	bad := &retainingWriter{}
	good := &copyingWriter{}

	writeInChunks := func(w io.Writer) {
		buf := make([]byte, chunkSize) // ONE buffer, reused
		src := strings.NewReader(payload)

		for {
			n, err := src.Read(buf)
			if n > 0 {
				_, _ = w.Write(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}

	writeInChunks(bad)
	writeInChunks(good)

	for _, c := range bad.chunks {
		retained = append(retained, string(c))
	}
	for _, c := range good.chunks {
		copied = append(copied, string(c))
	}

	return retained, copied
}

// wordCountReader is a Reader that also accumulates statistics, which is the
// shape of a progress reporter or a rate limiter.
type wordCountReader struct {
	src      io.Reader
	Words    int
	inWord   bool
	Finished bool
}

// NewWordCountReader wraps src.
func NewWordCountReader(src io.Reader) *wordCountReader {
	return &wordCountReader{src: src}
}

// Read implements io.Reader, counting word boundaries as bytes pass.
func (w *wordCountReader) Read(p []byte) (int, error) {
	n, err := w.src.Read(p)

	for _, b := range p[:n] {
		if unicode.IsSpace(rune(b)) {
			w.inWord = false
			continue
		}
		if !w.inWord {
			w.Words++
			w.inWord = true
		}
	}

	if errors.Is(err, io.EOF) {
		w.Finished = true
	}
	return n, err
}

// errorAfterReader fails partway, for testing error propagation through a
// chain of wrappers. Every real pipeline needs this and nobody has one.
type errorAfterReader struct {
	data  string
	after int
	pos   int
	err   error
}

// NewErrorAfterReader returns a reader that produces `after` bytes then fails.
func NewErrorAfterReader(data string, after int, err error) io.Reader {
	return &errorAfterReader{data: data, after: after, err: err}
}

func (r *errorAfterReader) Read(p []byte) (int, error) {
	if r.pos >= r.after {
		return 0, r.err
	}

	remaining := r.after - r.pos
	if len(p) > remaining {
		p = p[:remaining]
	}

	n := copy(p, r.data[r.pos:])
	r.pos += n
	return n, nil
}

// errorsPropagateThroughWrappers checks that a failure at the bottom of a
// stack of decorators reaches the top unchanged, so errors.Is still works.
func errorsPropagateThroughWrappers(failWith error) (read string, err error) {
	src := NewErrorAfterReader("aaaaaaaaaaaaaaaaaaaa", 5, failWith)

	// Three layers of wrapping.
	stacked := NewUpperReader(NewWordCountReader(NewUpperReader(src)))

	data, err := io.ReadAll(stacked)
	return string(data), err
}

// implementationRules is the checklist.
func implementationRules() []string {
	return []string{
		"Read: return n > 0 whenever you have data, even alongside io.EOF",
		"Read: never return (0, nil) in a loop; callers will spin",
		"Read: report io.EOF when finished, and do not wrap it",
		"Write: write ALL of p or return an error",
		"Write: return len(p) on success, not the count you forwarded on",
		"Write: never retain p; the caller reuses it on the next call",
	}
}

// demoCustom prints custom reader and writer behaviour.
func demoCustom() {
	data, err := io.ReadAll(NewUpperReader(strings.NewReader("transform me as I stream")))
	fmt.Printf("  a transforming Reader: %q (err=%v)\n", data, err)

	counter := NewLineCountingWriter(&plainWriter{})
	_, _ = io.Copy(counter, strings.NewReader("one\ntwo\nthree\n"))
	fmt.Printf("  a decorating Writer: %d lines, %d bytes\n", counter.Lines, counter.Bytes)

	retained, copied := retainingBreaksUnderReuse("abcdefghij", 3)
	fmt.Printf("\n  a Writer that retains the caller's slice:\n")
	fmt.Printf("    retaining: %v   <- every chunk points at the same reused buffer\n", retained)
	fmt.Printf("    copying:   %v\n", copied)

	wc := NewWordCountReader(strings.NewReader("the quick brown fox jumps"))
	_, _ = io.ReadAll(wc)
	fmt.Printf("\n  a counting Reader: %d words, finished=%t\n", wc.Words, wc.Finished)

	boom := errors.New("disk failure")
	read, err := errorsPropagateThroughWrappers(boom)
	fmt.Printf("\n  a failure three wrappers down: read %q\n", read)
	fmt.Printf("    err=%v, errors.Is(err, boom) = %t\n", err, errors.Is(err, boom))

	fmt.Println("\n  the implementation checklist:")
	for _, r := range implementationRules() {
		fmt.Printf("    %s\n", r)
	}
}
