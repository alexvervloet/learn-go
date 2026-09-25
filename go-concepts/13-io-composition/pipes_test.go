package main

import (
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
	"time"
)

func sampleRecords() []Record {
	return []Record{
		{ID: 1, Email: "ana@example.com"},
		{ID: 2, Email: "bo@example.com"},
	}
}

// TestPipeProducesIdenticalBytes: streaming must not change the output, only
// the memory profile.
func TestPipeProducesIdenticalBytes(t *testing.T) {
	buffered, size, err := streamJSONWithoutPipe(sampleRecords())
	if err != nil {
		t.Fatalf("buffered: %v", err)
	}

	want, err := io.ReadAll(buffered)
	if err != nil {
		t.Fatalf("read buffered: %v", err)
	}
	if size != len(want) {
		t.Errorf("reported size %d, actual %d", size, len(want))
	}

	got, err := io.ReadAll(streamJSONWithPipe(sampleRecords()))
	if err != nil {
		t.Fatalf("read streamed: %v", err)
	}

	if string(got) != string(want) {
		t.Errorf("streamed:\n%s\nbuffered:\n%s", got, want)
	}
}

func TestPipeOnNoRecords(t *testing.T) {
	got, err := io.ReadAll(streamJSONWithPipe(nil))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %q, want nothing", got)
	}
}

// TestCloseWithErrorReachesTheReader is what makes a pipe usable for real
// work: a producer that fails must not look like one that finished.
func TestCloseWithErrorReachesTheReader(t *testing.T) {
	read, err := pipeCloseWithErrorReachesTheReader(3)

	if err == nil {
		t.Fatal("the reader should have seen the producer's failure")
	}
	if !errors.Is(err, ErrProducerFailed) {
		t.Errorf("err = %v, want it to wrap ErrProducerFailed", err)
	}
	if !strings.Contains(err.Error(), "record 3") {
		t.Errorf("err = %q, want it to name where it failed", err)
	}

	// The records written before the failure still arrived.
	if !strings.Contains(read, "record-0") {
		t.Errorf("read = %q, want the successful records to have arrived", read)
	}
	if strings.Contains(read, "record-3") {
		t.Errorf("read = %q, should not contain the record that failed", read)
	}
}

// TestPlainCloseLooksLikeSuccess is the contrast: the same producer closing
// normally reports no error, which is why CloseWithError matters.
func TestPipeClosingNormallyReportsNoError(t *testing.T) {
	read, err := pipeCloseWithErrorReachesTheReader(100) // never fails

	if err != nil {
		t.Errorf("err = %v, want nil when the producer completes", err)
	}
	if !strings.Contains(read, "record-9") {
		t.Errorf("read = %q, want all ten records", read)
	}
}

// TestForgettingToCloseBlocksForever asserts the rule.
func TestForgettingToCloseBlocksForever(t *testing.T) {
	if forgettingToCloseBlocksForever() {
		t.Error("a pipe read should not complete when the writer is never closed")
	}
}

// TestReaderClosingStopsTheWriter is the reverse direction: a consumer that
// hangs up must not leave the producer blocked.
func TestReaderClosingStopsTheWriter(t *testing.T) {
	writes, writeErr := readerClosingStopsTheWriter()

	if writeErr == nil {
		t.Error("the producer should have seen an error once the reader closed")
	}
	if writes >= 1000 {
		t.Errorf("the producer made %d writes, want it stopped early", writes)
	}
	if writes < 1 {
		t.Errorf("the producer made %d writes, want at least one before the close", writes)
	}
}

// TestPipeIsUnbuffered asserts only the ordering the pipe GUARANTEES.
//
// Guaranteed: a Write does not return until a Read has consumed the bytes, so
// "writer: write returned" comes after "reader: about to read".
//
// NOT guaranteed: whether "writer: write returned" lands before or after
// "reader: read returned". The write unblocks as soon as ReadFull takes the
// bytes, which is before ReadFull returns. Asserting that order would be
// asserting a race, which lesson 07 taught the hard way.
func TestPipeIsUnbuffered(t *testing.T) {
	order, err := pipeIsUnbuffered()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}

	if len(order) != 4 {
		t.Fatalf("got %d events, want 4: %v", len(order), order)
	}

	indexOf := func(event string) int { return slices.Index(order, event) }

	aboutToWrite := indexOf("writer: about to write")
	writeReturned := indexOf("writer: write returned")
	aboutToRead := indexOf("reader: about to read")
	readReturned := indexOf("reader: read returned")

	for name, i := range map[string]int{
		"writer: about to write": aboutToWrite,
		"writer: write returned": writeReturned,
		"reader: about to read":  aboutToRead,
		"reader: read returned":  readReturned,
	} {
		if i < 0 {
			t.Fatalf("event %q missing from %v", name, order)
		}
	}

	if aboutToWrite > writeReturned {
		t.Errorf("the writer's own events are out of order: %v", order)
	}
	if aboutToRead > readReturned {
		t.Errorf("the reader's own events are out of order: %v", order)
	}
	// The guarantee: the write could not return before a read started.
	if writeReturned < aboutToRead {
		t.Errorf("the write returned before the read began, which an unbuffered pipe forbids:\n  %v", order)
	}
}

// TestPipeDoesNotLeakOnEarlyExit: the producer goroutine must end when the
// consumer stops reading.
func TestPipeDoesNotLeakOnEarlyExit(t *testing.T) {
	if testing.Short() {
		t.Skip("goroutine leak check")
	}

	before := countGoroutines()

	for i := 0; i < 50; i++ {
		pr := streamJSONWithPipe(sampleRecords())

		// Read one byte and hang up.
		buf := make([]byte, 1)
		_, _ = pr.Read(buf)
		if closer, ok := pr.(io.Closer); ok {
			_ = closer.Close()
		}
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if countGoroutines() <= before+5 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Errorf("leaked goroutines: %d before, %d after", before, countGoroutines())
}

// countGoroutines settles the scheduler and reports the count.
func countGoroutines() int {
	for i := 0; i < 20; i++ {
		runtimeGosched()
		time.Sleep(time.Millisecond)
	}
	return runtimeNumGoroutine()
}
