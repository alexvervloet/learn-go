package main

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"time"
)

// Goroutine leaks
// ===============
//
// A goroutine blocked forever is never collected. It holds its stack and
// everything the stack references, for the life of the process. The garbage
// collector cannot help: a blocked goroutine is reachable by definition.
//
// Nothing warns you. No error, no log line, no growing error rate. The symptom
// is memory climbing slowly and a goroutine count that only goes up, which is
// why runtime.NumGoroutine() belongs on every service's metrics endpoint.
//
// The rule: EVERY goroutine needs a known way to exit. If you cannot say in one
// sentence how it terminates, it leaks.

// countGoroutines reports the current count after giving the runtime a moment
// to finish tearing down anything that has already returned.
//
// The settle is necessary and slightly unsatisfying: a goroutine that has
// returned is not deducted from the count instantly. Tests that assert on
// goroutine counts have to allow for this, which is one reason goleak exists.
func countGoroutines() int {
	for i := 0; i < 20; i++ {
		runtime.Gosched()
		time.Sleep(time.Millisecond)
	}
	return runtime.NumGoroutine()
}

// Leak 1: sending on a channel nobody reads
// -----------------------------------------
//
// The classic shape. A producer is started, the consumer takes the first result
// and returns early, and the producer blocks forever on its second send.

// leakySender returns after reading one value. The goroutine is still trying to
// send the second, on an unbuffered channel with no reader left.
func leakySender() int {
	ch := make(chan int) // unbuffered: every send needs a live receiver

	go func() {
		for i := 0; ; i++ {
			ch <- i // blocks forever once the caller stops reading
		}
	}()

	return <-ch // take one, abandon the goroutine
}

// fixedSenderWithContext gives the producer a way out. When the caller cancels,
// the select's other arm fires and the goroutine returns.
func fixedSenderWithContext() int {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // the producer's exit signal, guaranteed on every path

	ch := make(chan int)

	go func() {
		for i := 0; ; i++ {
			select {
			case ch <- i:
			case <-ctx.Done():
				return // the known way out
			}
		}
	}()

	return <-ch
}

// fixedSenderWithBuffer is the other fix, and it only works when the number of
// sends is known and bounded. A buffer of 1 means the single send completes
// whether or not anyone reads it, so the goroutine finishes.
//
// This is the right answer for a one-shot result channel and the wrong answer
// for a stream, where the producer would just block once the buffer filled.
func fixedSenderWithBuffer() int {
	ch := make(chan int, 1) // room for exactly the one send that happens

	go func() {
		ch <- 42 // completes immediately, goroutine returns
	}()

	return <-ch
}

// Leak 2: receiving from a channel nobody closes
// ----------------------------------------------

// leakyReceiver ranges over a channel that is never closed. range on a channel
// ends only when the channel is closed, so this blocks forever.
func leakyReceiver() {
	ch := make(chan int, 3)
	ch <- 1
	ch <- 2
	ch <- 3
	// No close(ch).

	go func() {
		for range ch { //nolint:revive // the missing close is the bug
			// Drains the three buffered values, then blocks forever.
		}
	}()
}

// fixedReceiver closes the channel, so range terminates. The sender always
// closes, never the receiver: closing from the receiving side risks a send on
// a closed channel, which panics.
func fixedReceiver() (received int) {
	ch := make(chan int, 3)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range ch {
			received++
		}
	}()

	ch <- 1
	ch <- 2
	ch <- 3
	close(ch) // the sender closes, and range ends

	wg.Wait()
	return received
}

// Leak 3: ignoring cancellation
// -----------------------------

// leakyWorker keeps working after its caller has given up, because nothing ever
// tells it to stop. The caller's timeout protected the CALLER, not the work.
func leakyWorker(work time.Duration) {
	go func() {
		time.Sleep(work) // no cancellation path
	}()
}

// fixedWorker checks for cancellation at every step. The work is chunked so
// there is somewhere to check; a single uninterruptible operation cannot be
// cancelled at all, which is a design constraint worth knowing before choosing
// a library.
func fixedWorker(ctx context.Context, steps int, each time.Duration) (completed int, err error) {
	for i := 0; i < steps; i++ {
		select {
		case <-ctx.Done():
			return completed, ctx.Err()
		case <-time.After(each):
			completed++
		}
	}
	return completed, nil
}

// demoLeaks prints each leak shape alongside its fix, with goroutine counts.
func demoLeaks() {
	baseline := countGoroutines()
	fmt.Printf("  baseline goroutines: %d\n", baseline)

	_ = leakySender()
	afterLeak := countGoroutines()
	fmt.Printf("\n  leak 1, send with no reader:\n")
	fmt.Printf("    after leakySender():          %d  (+%d, leaked)\n", afterLeak, afterLeak-baseline)

	_ = fixedSenderWithContext()
	afterCtx := countGoroutines()
	fmt.Printf("    after fixedSenderWithContext: %d  (+%d)\n", afterCtx, afterCtx-afterLeak)

	_ = fixedSenderWithBuffer()
	afterBuf := countGoroutines()
	fmt.Printf("    after fixedSenderWithBuffer:  %d  (+%d)\n", afterBuf, afterBuf-afterCtx)

	before := countGoroutines()
	leakyReceiver()
	after := countGoroutines()
	fmt.Printf("\n  leak 2, range over a channel never closed:\n")
	fmt.Printf("    after leakyReceiver(): %d  (+%d, leaked)\n", after, after-before)
	fmt.Printf("    fixedReceiver() drained %d values and its goroutine returned\n", fixedReceiver())

	fmt.Printf("\n  leak 3, work that ignores cancellation:\n")
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()

	completed, err := fixedWorker(ctx, 100, 5*time.Millisecond)
	fmt.Printf("    fixedWorker stopped after %d of 100 steps: %v\n", completed, err)

	leakyWorker(500 * time.Millisecond)
	fmt.Printf("    leakyWorker is still sleeping; nothing can stop it\n")

	fmt.Printf("\n  goroutines still alive at the end of this demo: %d (baseline was %d)\n",
		countGoroutines(), baseline)
}
