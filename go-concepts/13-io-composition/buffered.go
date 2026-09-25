package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
)

// bufio
// =====
//
// bufio.Reader and bufio.Writer batch small operations into large ones. When
// each underlying Read or Write is a syscall, that is the difference between
// one syscall per byte and one per 4KB.
//
// bufio.Scanner is the convenient line reader, with one sharp edge: a MAXIMUM
// TOKEN SIZE, 64KB by default. A longer line stops the scan with
// bufio.ErrTooLong, and code that ignores scanner.Err() silently processes half
// a file.

// syscallCountingReader stands in for a file or socket: every Read is
// expensive, and the counter shows how many happen.
type syscallCountingReader struct {
	data  string
	pos   int
	reads atomic.Int64
}

func (r *syscallCountingReader) Read(p []byte) (int, error) {
	r.reads.Add(1)

	if r.pos >= len(r.data) {
		return 0, io.EOF
	}

	n := copy(p, r.data[r.pos:])
	r.pos += n
	return n, nil
}

// readByteAtATime is the pathological case: one Read per byte.
func readByteAtATime(r *syscallCountingReader) (bytesRead int, syscalls int64, err error) {
	buf := make([]byte, 1)

	for {
		n, rerr := r.Read(buf)
		bytesRead += n

		if rerr != nil {
			if errors.Is(rerr, io.EOF) {
				return bytesRead, r.reads.Load(), nil
			}
			return bytesRead, r.reads.Load(), rerr
		}
	}
}

// readBufferedByteAtATime does the same thing through bufio, which reads in
// 4KB blocks and serves single bytes from memory.
func readBufferedByteAtATime(r *syscallCountingReader) (bytesRead int, syscalls int64, err error) {
	br := bufio.NewReader(r)

	for {
		_, rerr := br.ReadByte()
		if rerr != nil {
			if errors.Is(rerr, io.EOF) {
				return bytesRead, r.reads.Load(), nil
			}
			return bytesRead, r.reads.Load(), rerr
		}
		bytesRead++
	}
}

// syscallCountingWriter is the write-side equivalent.
type syscallCountingWriter struct {
	written []byte
	writes  atomic.Int64
}

func (w *syscallCountingWriter) Write(p []byte) (int, error) {
	w.writes.Add(1)
	w.written = append(w.written, p...)
	return len(p), nil
}

// writeManySmallWrites is the unbuffered version.
func writeManySmallWrites(w *syscallCountingWriter, n int) (syscalls int64, err error) {
	for i := 0; i < n; i++ {
		if _, err = io.WriteString(w, "x"); err != nil {
			return w.writes.Load(), fmt.Errorf("write: %w", err)
		}
	}
	return w.writes.Load(), nil
}

// writeBuffered batches them.
//
// FLUSH IS NOT OPTIONAL. A bufio.Writer holds up to its buffer size in memory,
// and without a Flush the tail is lost silently. This is the single most
// common bufio bug, and `defer w.Flush()` discards the error that tells you
// about it, so the flush belongs on the happy path with its error checked.
func writeBuffered(w *syscallCountingWriter, n int) (syscalls int64, err error) {
	bw := bufio.NewWriter(w)

	for i := 0; i < n; i++ {
		if _, err = bw.WriteString("x"); err != nil {
			return w.writes.Load(), fmt.Errorf("write: %w", err)
		}
	}

	if err = bw.Flush(); err != nil {
		return w.writes.Load(), fmt.Errorf("flush: %w", err)
	}

	return w.writes.Load(), nil
}

// forgettingToFlushLosesData asserts the bug.
func forgettingToFlushLosesData(payload string) (withoutFlush, withFlush int) {
	var a syscallCountingWriter
	bw := bufio.NewWriter(&a)
	_, _ = bw.WriteString(payload)
	// No Flush. Everything under the buffer size is still in memory.
	withoutFlush = len(a.written)

	var b syscallCountingWriter
	bw2 := bufio.NewWriter(&b)
	_, _ = bw2.WriteString(payload)
	_ = bw2.Flush()
	withFlush = len(b.written)

	return withoutFlush, withFlush
}

// scanLines is the everyday Scanner use.
//
// Checking scanner.Err() is mandatory. Scan() returns false both at the end of
// the input and on a failure, and without the check the two are
// indistinguishable: a truncated file looks exactly like a complete one.
func scanLines(r io.Reader) (lines []string, err error) {
	scanner := bufio.NewScanner(r)

	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	// NOT optional.
	if err := scanner.Err(); err != nil {
		return lines, fmt.Errorf("scan: %w", err)
	}
	return lines, nil
}

// scannerHasAMaximumTokenSize is the sharp edge. The default is 64KB; a longer
// line ends the scan with bufio.ErrTooLong, and only scanner.Err() reveals it.
func scannerHasAMaximumTokenSize(lineLength int) (linesRead int, err error) {
	input := strings.Repeat("x", lineLength) + "\nsecond line\n"

	scanner := bufio.NewScanner(strings.NewReader(input))

	for scanner.Scan() {
		linesRead++
	}

	if err := scanner.Err(); err != nil {
		return linesRead, fmt.Errorf("scan: %w", err)
	}
	return linesRead, nil
}

// scannerWithABiggerBuffer raises the limit. The first argument is the initial
// buffer and the second is the maximum; passing nil lets Scanner allocate as
// it grows.
func scannerWithABiggerBuffer(lineLength, maxTokenSize int) (linesRead int, err error) {
	input := strings.Repeat("x", lineLength) + "\nsecond line\n"

	scanner := bufio.NewScanner(strings.NewReader(input))
	scanner.Buffer(nil, maxTokenSize)

	for scanner.Scan() {
		linesRead++
	}

	if err := scanner.Err(); err != nil {
		return linesRead, fmt.Errorf("scan: %w", err)
	}
	return linesRead, nil
}

// splitFunctions: a Scanner can tokenise by line (the default), word, rune or
// byte, or by anything you write yourself.
func splitFunctions(input string) (lines, words, runes int) {
	count := func(split bufio.SplitFunc) int {
		s := bufio.NewScanner(strings.NewReader(input))
		s.Split(split)

		n := 0
		for s.Scan() {
			n++
		}
		return n
	}

	return count(bufio.ScanLines), count(bufio.ScanWords), count(bufio.ScanRunes)
}

// readerVsScannerForLongLines: bufio.Reader.ReadString has no token limit, so
// it is the answer when lines may be arbitrarily long. The cost is that it
// keeps the delimiter and does not handle \r\n for you.
func readerVsScannerForLongLines(lineLength int) (scannerLines int, readerLines int, scannerErr error) {
	input := strings.Repeat("x", lineLength) + "\nsecond\n"

	scannerLines, scannerErr = func() (int, error) {
		s := bufio.NewScanner(strings.NewReader(input))
		n := 0
		for s.Scan() {
			n++
		}
		return n, s.Err()
	}()

	br := bufio.NewReader(strings.NewReader(input))
	for {
		_, err := br.ReadString('\n')
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			break
		}
		readerLines++
	}

	return scannerLines, readerLines, scannerErr
}

// demoBuffered prints buffering behaviour.
func demoBuffered() {
	const payload = 4096

	unbufferedBytes, unbufferedCalls, _ := readByteAtATime(
		&syscallCountingReader{data: strings.Repeat("x", payload)})
	bufferedBytes, bufferedCalls, _ := readBufferedByteAtATime(
		&syscallCountingReader{data: strings.Repeat("x", payload)})

	fmt.Printf("  reading %d bytes one at a time:\n", payload)
	fmt.Printf("    unbuffered: %d bytes, %d Read calls\n", unbufferedBytes, unbufferedCalls)
	fmt.Printf("    bufio:      %d bytes, %d Read calls\n", bufferedBytes, bufferedCalls)

	var a syscallCountingWriter
	unbufferedWrites, _ := writeManySmallWrites(&a, payload)
	var b syscallCountingWriter
	bufferedWrites, _ := writeBuffered(&b, payload)

	fmt.Printf("\n  writing %d single bytes:\n", payload)
	fmt.Printf("    unbuffered: %d Write calls\n", unbufferedWrites)
	fmt.Printf("    bufio:      %d Write calls\n", bufferedWrites)

	withoutFlush, withFlush := forgettingToFlushLosesData("this fits in the buffer")
	fmt.Printf("\n  forgetting Flush: %d bytes reached the writer; with Flush: %d\n",
		withoutFlush, withFlush)

	lines, err := scanLines(strings.NewReader("alpha\nbeta\ngamma\n"))
	fmt.Printf("\n  Scanner over 3 lines: %v (err=%v)\n", lines, err)

	linesRead, err := scannerHasAMaximumTokenSize(70_000)
	fmt.Printf("  a 70,000-byte line: %d lines read, err=%v\n", linesRead, err)
	fmt.Printf("    errors.Is(err, bufio.ErrTooLong) = %t\n", errors.Is(err, bufio.ErrTooLong))

	linesRead, err = scannerWithABiggerBuffer(70_000, 1<<20)
	fmt.Printf("  with Buffer(nil, 1MB):  %d lines read, err=%v\n", linesRead, err)

	l, w, r := splitFunctions("one two\nthree")
	fmt.Printf("\n  split functions on %q: %d lines, %d words, %d runes\n", "one two\\nthree", l, w, r)

	scannerLines, readerLines, scannerErr := readerVsScannerForLongLines(70_000)
	fmt.Printf("\n  a 70KB line: Scanner read %d lines (err=%v), bufio.Reader read %d\n",
		scannerLines, scannerErr, readerLines)
	fmt.Println("    ...ReadString has no token limit; Scanner does")
}
