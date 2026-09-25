// Package main is lesson 07 of go-concepts: channels.
//
//	ch := make(chan int)      unbuffered: a send needs a waiting receiver
//	ch := make(chan int, 3)   buffered:   a send needs space
//	ch <- v                   send
//	v := <-ch                 receive
//	v, ok := <-ch             receive, with ok=false once closed and drained
//	close(ch)                 no more values are coming
//
// The blocking behaviour is the point. An unbuffered send returning means a
// receiver HAS the value, which is a synchronisation guarantee and not merely
// a delivery one.
package main

import (
	"fmt"
	"sync"
	"time"
)

// unbufferedIsAHandshake records the order of events on both sides. The sender
// cannot proceed past its send until the receiver has taken the value, so the
// receive is always logged before the sender's "sent" line.
//
// This ordering is guaranteed by the memory model, not by timing, so the test
// can assert it exactly.
func unbufferedIsAHandshake() []string {
	var (
		mu     sync.Mutex
		events []string
		wg     sync.WaitGroup
	)

	record := func(s string) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, s)
	}

	ch := make(chan int)

	wg.Add(1)
	go func() {
		defer wg.Done()

		// Give the sender time to reach its send and block there.
		time.Sleep(20 * time.Millisecond)
		record("receiver: about to receive")
		<-ch
		record("receiver: received")
	}()

	record("sender: about to send")
	ch <- 1 // blocks here for ~20ms until the receiver arrives
	record("sender: sent")

	wg.Wait()
	return events
}

// bufferedDoesNotBlockUntilFull sends into a buffer with nobody receiving. The
// first cap sends return immediately; the next one would block.
//
// It returns the buffer's len after each send, which is the clearest way to see
// what capacity actually means.
func bufferedDoesNotBlockUntilFull(capacity, sends int) (lengths []int, blocked bool) {
	ch := make(chan int, capacity)

	for i := 0; i < sends; i++ {
		// Try the send without committing to it. The default arm fires when
		// the buffer is full, which is how this detects the block rather than
		// hanging on it. select is lesson 08; this is a preview.
		select {
		case ch <- i:
			lengths = append(lengths, len(ch))
		default:
			return lengths, true
		}
	}

	return lengths, false
}

// lenAndCap report the buffered count and the capacity. On an unbuffered
// channel both are 0, always, which occasionally surprises people checking
// len(ch) to decide whether to send.
//
// The deeper point: len(ch) is stale the instant you read it. Another goroutine
// can send or receive between the check and the action, so `if len(ch) < cap(ch)
// { ch <- v }` is not a safe way to avoid blocking. A select with default is.
func lenAndCap() (unbufferedLen, unbufferedCap, bufferedLen, bufferedCap int) {
	unbuffered := make(chan int)

	buffered := make(chan int, 5)
	buffered <- 1
	buffered <- 2

	return len(unbuffered), cap(unbuffered), len(buffered), cap(buffered)
}

// sendBlocksWithoutAReceiver proves an unbuffered send blocks, without hanging
// the program. The sender runs in its own goroutine and the main flow waits a
// short time to see whether it completed.
func sendBlocksWithoutAReceiver() (completedWithoutReceiver bool) {
	ch := make(chan int)
	done := make(chan struct{})

	go func() {
		ch <- 1 // blocks: nobody is receiving
		close(done)
	}()

	select {
	case <-done:
		return true
	case <-time.After(30 * time.Millisecond):
		// Drain so the goroutine can finish and not leak.
		<-ch
		<-done
		return false
	}
}

// receiveBlocksWithoutASender is the mirror image.
func receiveBlocksWithoutASender() (completedWithoutSender bool) {
	ch := make(chan int)
	done := make(chan struct{})

	go func() {
		<-ch // blocks: nobody is sending
		close(done)
	}()

	select {
	case <-done:
		return true
	case <-time.After(30 * time.Millisecond):
		ch <- 1 // unblock it so the goroutine exits
		<-done
		return false
	}
}

// bufferSizeIsADesignDecision documents the four cases, because "what size
// buffer?" is the question every code review of channel code asks.
func bufferSizeIsADesignDecision() map[int]string {
	return map[int]string{
		0: "I need to know the value was received. A synchronisation point.",
		1: "A one-shot result, or a semaphore permitting one holder.",
		8: "Absorb bursts up to 8. Beyond that the producer blocks: backpressure.",
		// A very large buffer is the one to be suspicious of.
		100_000: "Suspicious. If the producer outruns the consumer permanently, " +
			"this converts a fast failure into a slow memory leak.",
	}
}

// demoBasics prints blocking behaviour and capacity.
func demoBasics() {
	fmt.Println("  unbuffered send waits for a receiver:")
	for _, e := range unbufferedIsAHandshake() {
		fmt.Printf("    %s\n", e)
	}

	lengths, blocked := bufferedDoesNotBlockUntilFull(3, 5)
	fmt.Printf("\n  make(chan int, 3), sending 5 with no receiver:\n")
	fmt.Printf("    len after each successful send: %v\n", lengths)
	fmt.Printf("    send %d would have blocked: %t\n", len(lengths)+1, blocked)

	ul, uc, bl, bc := lenAndCap()
	fmt.Printf("\n  unbuffered: len=%d cap=%d    buffered(5) with 2 queued: len=%d cap=%d\n", ul, uc, bl, bc)

	fmt.Printf("\n  unbuffered send with no receiver completed: %t\n", sendBlocksWithoutAReceiver())
	fmt.Printf("  unbuffered receive with no sender completed: %t\n", receiveBlocksWithoutASender())

	fmt.Println("\n  choosing a buffer size:")
	for _, size := range []int{0, 1, 8, 100_000} {
		fmt.Printf("    %-7d %s\n", size, bufferSizeIsADesignDecision()[size])
	}
}
