package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"runtime"
	"strings"
	"sync"
	"time"
)

// io.Pipe
// =======
//
//	pr, pw := io.Pipe()
//
// An in-memory pipe with NO buffer. Every Write blocks until a Read consumes
// it, so the two ends must run in different goroutines or the program
// deadlocks.
//
// What it is for: feeding a function that wants an io.Reader from code that
// produces output by writing. Streaming a JSON encoding into an HTTP request
// body, for instance, without building the whole document in memory first.
//
// Two rules:
//
//	the writer MUST be closed, or the reader blocks forever
//	CloseWithError propagates a failure, so the reader learns WHY

// streamJSONWithoutPipe is the version that does not stream: it builds the
// entire payload in memory, then hands over a reader. Fine for a small
// document, and an out-of-memory error for a large one.
func streamJSONWithoutPipe(records []Record) (io.Reader, int, error) {
	var sb strings.Builder

	enc := json.NewEncoder(&sb)
	for _, r := range records {
		if err := enc.Encode(r); err != nil {
			return nil, 0, fmt.Errorf("encode: %w", err)
		}
	}

	return strings.NewReader(sb.String()), sb.Len(), nil
}

// Record is the payload type.
type Record struct {
	ID    int    `json:"id"`
	Email string `json:"email"`
}

// streamJSONWithPipe produces the same bytes with a bounded memory footprint:
// the encoder writes, the consumer reads, and only what is in flight exists.
//
// The producer runs in its own goroutine because every Write blocks until the
// consumer reads. Doing it inline deadlocks on the first record.
func streamJSONWithPipe(records []Record) io.Reader {
	pr, pw := io.Pipe()

	go func() {
		// CloseWithError(nil) is the same as Close, so a single deferred call
		// handles both paths. Without a close, the reader never sees EOF.
		var err error
		defer func() { _ = pw.CloseWithError(err) }()

		enc := json.NewEncoder(pw)
		for _, r := range records {
			if err = enc.Encode(r); err != nil {
				// The deferred CloseWithError carries this to the reader.
				return
			}
		}
	}()

	return pr
}

// ErrProducerFailed stands in for whatever goes wrong mid-stream.
var ErrProducerFailed = errors.New("the producer gave up")

// pipeCloseWithErrorReachesTheReader is the property that makes pipes usable
// for real work. A producer that fails halfway must not look like a producer
// that finished, and a plain Close would be exactly that.
func pipeCloseWithErrorReachesTheReader(failAfter int) (read string, err error) {
	pr, pw := io.Pipe()

	go func() {
		for i := 0; i < 10; i++ {
			if i == failAfter {
				_ = pw.CloseWithError(fmt.Errorf("record %d: %w", i, ErrProducerFailed))
				return
			}
			if _, werr := fmt.Fprintf(pw, "record-%d\n", i); werr != nil {
				return
			}
		}
		_ = pw.Close()
	}()

	data, err := io.ReadAll(pr)
	return string(data), err
}

// forgettingToCloseBlocksForever asserts the other rule. The read is run with
// a timeout so the demo survives; in real code it is a hung goroutine.
func forgettingToCloseBlocksForever() (completed bool) {
	pr, pw := io.Pipe()

	go func() {
		_, _ = io.WriteString(pw, "some data")
		// No Close. The reader waits for an EOF that never comes.
	}()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = io.ReadAll(pr)
	}()

	select {
	case <-done:
		return true
	case <-time.After(50 * time.Millisecond):
		// Unblock the reader so the goroutines can exit rather than leak.
		_ = pr.CloseWithError(errors.New("test cleanup"))
		<-done
		return false
	}
}

// readerClosingStopsTheWriter is the reverse direction: a consumer that stops
// early must not leave the producer blocked forever. Closing the READ end
// makes every subsequent Write return an error, so the producer's own error
// handling ends it.
func readerClosingStopsTheWriter() (writesBeforeClose int, writeErr error) {
	pr, pw := io.Pipe()

	writeDone := make(chan struct{})
	go func() {
		defer close(writeDone)

		for i := 0; i < 1000; i++ {
			if _, err := fmt.Fprintf(pw, "line-%d\n", i); err != nil {
				writeErr = err
				return
			}
			writesBeforeClose++
		}
	}()

	// Take a little, then hang up.
	buf := make([]byte, 20)
	_, _ = io.ReadFull(pr, buf)
	_ = pr.CloseWithError(errors.New("consumer done"))

	<-writeDone
	return writesBeforeClose, writeErr
}

// pipeIsUnbuffered demonstrates the synchronous handshake: a Write does not
// return until a Read has taken the bytes, exactly like an unbuffered channel.
//
// The events are collected under a mutex and the writer is joined with a
// WaitGroup before they are read. An earlier version used a buffered channel
// closed from the main goroutine, and `go test -race` reported a race: the
// writer could still be sending when the close happened. Recording events from
// several goroutines needs real synchronisation, even in a demo.
func pipeIsUnbuffered() (order []string, err error) {
	pr, pw := io.Pipe()

	var (
		mu     sync.Mutex
		wg     sync.WaitGroup
		events []string
	)

	record := func(s string) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, s)
	}

	wg.Go(func() {
		record("writer: about to write")
		_, _ = io.WriteString(pw, "payload")
		record("writer: write returned")
		_ = pw.Close()
	})

	// Give the writer time to reach its Write and block there.
	time.Sleep(20 * time.Millisecond)
	record("reader: about to read")

	buf := make([]byte, 7)
	if _, err = io.ReadFull(pr, buf); err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	record("reader: read returned")

	wg.Wait() // the writer's last event has landed by now

	mu.Lock()
	defer mu.Unlock()
	return append([]string(nil), events...), nil
}

// demoPipes prints pipe behaviour.
func demoPipes() {
	records := []Record{
		{ID: 1, Email: "ana@example.com"},
		{ID: 2, Email: "bo@example.com"},
	}

	buffered, size, err := streamJSONWithoutPipe(records)
	data, _ := io.ReadAll(buffered)
	fmt.Printf("  without a pipe: built %d bytes in memory first (err=%v)\n", size, err)
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		fmt.Printf("    | %s\n", line)
	}

	streamed, _ := io.ReadAll(streamJSONWithPipe(records))
	fmt.Printf("  with a pipe: identical bytes, nothing buffered: %t\n",
		string(streamed) == string(data))

	read, err := pipeCloseWithErrorReachesTheReader(3)
	fmt.Printf("\n  a producer failing at record 3:\n")
	fmt.Printf("    the reader got %d bytes and err=%v\n", len(read), err)
	fmt.Printf("    errors.Is(err, ErrProducerFailed) = %t\n", errors.Is(err, ErrProducerFailed))

	fmt.Printf("\n  forgetting to close the writer: the read completed: %t\n",
		forgettingToCloseBlocksForever())

	writes, writeErr := readerClosingStopsTheWriter()
	fmt.Printf("  the consumer hanging up stopped the producer after %d writes\n", writes)
	fmt.Printf("    the producer saw: %v\n", writeErr)

	order, err := pipeIsUnbuffered()
	fmt.Printf("\n  a pipe is an unbuffered handshake (err=%v):\n", err)
	for _, e := range order {
		fmt.Printf("    %s\n", e)
	}
}

// runtimeGosched and runtimeNumGoroutine keep pipes_test.go from needing a
// runtime import of its own.
func runtimeGosched() { runtime.Gosched() }

func runtimeNumGoroutine() int { return runtime.NumGoroutine() }
