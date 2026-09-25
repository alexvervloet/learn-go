package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
)

// io.Copy and its fast paths
// ==========================
//
// io.Copy(dst, src) is not a naive loop. It asks two questions first:
//
//	1. does src implement io.WriterTo?    -> src.WriteTo(dst)
//	2. does dst implement io.ReaderFrom?  -> dst.ReadFrom(src)
//
// Only if both fail does it allocate a 32KB buffer and loop. That is why
// copying an *os.File to a net.TCPConn on Linux can become a sendfile syscall
// with no bytes entering user space, and why io.Copy usually beats a
// hand-written loop.
//
// The interfaces:
//
//	type WriterTo   interface { WriteTo(w Writer) (n int64, err error) }
//	type ReaderFrom interface { ReadFrom(r Reader) (n int64, err error) }

// countingReader is a plain Reader with no fast path, so io.Copy has to use its
// buffer loop. The counter records how many Read calls that takes.
type countingReader struct {
	r     io.Reader
	reads atomic.Int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	c.reads.Add(1)
	return c.r.Read(p)
}

// writerToReader implements WriterTo, so io.Copy takes the fast path and calls
// WriteTo once instead of looping.
type writerToReader struct {
	data      string
	writeToes atomic.Int64
}

// Read exists so the type is a Reader at all; io.Copy should never call it.
func (w *writerToReader) Read(p []byte) (int, error) {
	return 0, io.EOF
}

// WriteTo is the fast path. Returning the byte count as int64 is the contract.
func (w *writerToReader) WriteTo(dst io.Writer) (int64, error) {
	w.writeToes.Add(1)

	n, err := io.WriteString(dst, w.data)
	if err != nil {
		return int64(n), fmt.Errorf("writeTo: %w", err)
	}
	return int64(n), nil
}

// readerFromWriter implements ReaderFrom, the other fast path.
type readerFromWriter struct {
	buf       bytes.Buffer
	readFroms atomic.Int64
}

// Write exists so the type is a Writer; with ReadFrom present, io.Copy prefers
// that and calls this only from inside it.
func (w *readerFromWriter) Write(p []byte) (int, error) {
	return w.buf.Write(p)
}

// ReadFrom is the fast path.
func (w *readerFromWriter) ReadFrom(src io.Reader) (int64, error) {
	w.readFroms.Add(1)
	return w.buf.ReadFrom(src)
}

// whichPathDidCopyTake runs io.Copy against each combination and reports which
// route it took, by counting the calls each type saw.
func whichPathDidCopyTake() (slowPathReads int64, writeToCalls int64, readFromCalls int64) {
	const payload = "the quick brown fox jumps over the lazy dog"

	// Neither side has a fast path: io.Copy loops with its own buffer.
	// bytes.Buffer implements ReadFrom, so the destination has to be something
	// plainer. A struct wrapping only Write does the job.
	slow := &countingReader{r: strings.NewReader(payload)}
	_, _ = io.Copy(&plainWriter{}, slow)

	// The source has WriteTo.
	src := &writerToReader{data: payload}
	_, _ = io.Copy(&plainWriter{}, src)

	// The destination has ReadFrom.
	dst := &readerFromWriter{}
	_, _ = io.Copy(dst, &plainReader{r: strings.NewReader(payload)})

	return slow.reads.Load(), src.writeToes.Load(), dst.readFroms.Load()
}

// plainWriter implements only Write, with no ReadFrom, so it cannot offer a
// fast path.
type plainWriter struct {
	buf []byte
}

func (w *plainWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	return len(p), nil
}

// plainReader implements only Read, hiding whatever fast path the wrapped
// reader might have had. Wrapping a reader is how you accidentally lose a fast
// path, which is worth knowing before writing a decorator.
type plainReader struct {
	r io.Reader
}

func (r *plainReader) Read(p []byte) (int, error) { return r.r.Read(p) }

// wrappingHidesFastPaths demonstrates that cost. strings.Reader implements
// WriteTo; wrapping it in a struct that only forwards Read takes that away,
// and io.Copy falls back to its buffer loop.
func wrappingHidesFastPaths() (directIsFast, wrappedIsFast bool) {
	// A strings.Reader is a WriterTo.
	var direct io.Reader = strings.NewReader("data")
	_, directIsFast = direct.(io.WriterTo)

	// Wrapped, it is not.
	var wrapped io.Reader = &plainReader{r: strings.NewReader("data")}
	_, wrappedIsFast = wrapped.(io.WriterTo)

	return directIsFast, wrappedIsFast
}

// copyBufferReusesABuffer, which matters only when copying many times: io.Copy
// allocates 32KB per call, and in a loop that is 32KB of garbage per iteration.
func copyBufferReusesABuffer(sources []string) (total int64, err error) {
	buf := make([]byte, 32*1024) // allocated once, reused for every copy

	var dst plainWriter
	for _, s := range sources {
		n, cerr := io.CopyBuffer(&dst, &plainReader{r: strings.NewReader(s)}, buf)
		if cerr != nil {
			return total, fmt.Errorf("copy: %w", cerr)
		}
		total += n
	}

	return total, nil
}

// copyNStopsEarly copies at most n bytes, which is io.Copy's answer to
// untrusted input. It reports io.EOF when the source ran out first, and nil
// when it stopped at the limit, which is a distinction worth handling.
func copyNStopsEarly(source string, limit int64) (copied int64, hitLimit bool, err error) {
	var dst plainWriter

	copied, err = io.CopyN(&dst, strings.NewReader(source), limit)

	switch {
	case err == nil:
		// Copied exactly `limit` bytes; there may be more in the source.
		return copied, true, nil
	case errors.Is(err, io.EOF):
		// The source ran out before the limit, which is not a failure.
		return copied, false, nil
	default:
		return copied, false, fmt.Errorf("copyN: %w", err)
	}
}

// hashWhileCopying is the everyday use of the fast paths plus MultiWriter:
// write to a destination and a hash in one pass, with no buffering of the whole
// payload and no second read.
func hashWhileCopying(source io.Reader) (written int64, digest string, err error) {
	var dst bytes.Buffer
	h := sha256.New()

	// Every byte goes to both. io.MultiWriter's Write calls each in turn and
	// stops at the first failure.
	n, err := io.Copy(io.MultiWriter(&dst, h), source)
	if err != nil {
		return n, "", fmt.Errorf("copy: %w", err)
	}

	return n, hex.EncodeToString(h.Sum(nil)), nil
}

// demoCopy prints the fast-path behaviour.
func demoCopy() {
	slowReads, writeToCalls, readFromCalls := whichPathDidCopyTake()

	fmt.Printf("  io.Copy with no fast path:       %d Read call(s), its own 32KB buffer\n", slowReads)
	fmt.Printf("  source implements WriterTo:      %d WriteTo call(s), Read never touched\n", writeToCalls)
	fmt.Printf("  destination implements ReaderFrom: %d ReadFrom call(s)\n", readFromCalls)

	directIsFast, wrappedIsFast := wrappingHidesFastPaths()
	fmt.Printf("\n  strings.Reader is a WriterTo:            %t\n", directIsFast)
	fmt.Printf("  the same reader wrapped in a decorator:  %t   <- the fast path is gone\n", wrappedIsFast)

	total, err := copyBufferReusesABuffer([]string{"one", "two", "three"})
	fmt.Printf("\n  io.CopyBuffer over 3 sources: %d bytes, one buffer, err=%v\n", total, err)

	copied, hitLimit, err := copyNStopsEarly("a much longer payload than the limit", 10)
	fmt.Printf("  io.CopyN(limit 10): copied %d, hit the limit: %t (err=%v)\n", copied, hitLimit, err)

	copied, hitLimit, err = copyNStopsEarly("short", 100)
	fmt.Printf("  io.CopyN(limit 100) on 5 bytes: copied %d, hit the limit: %t (err=%v)\n",
		copied, hitLimit, err)

	written, digest, err := hashWhileCopying(strings.NewReader("stream me"))
	fmt.Printf("\n  hashing while copying, one pass: %d bytes, sha256 %s... (err=%v)\n",
		written, digest[:16], err)
}
