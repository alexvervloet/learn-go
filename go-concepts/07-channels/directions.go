package main

import (
	"fmt"
	"strings"
	"sync"
)

// Directional channel types
// =========================
//
//	chan int     bidirectional
//	chan<- int   send only    (the arrow points INTO the channel)
//	<-chan int   receive only (the arrow points OUT of the channel)
//
// The conversion is automatic and one-way: a chan int becomes either, and
// neither converts back. So a function that takes <-chan int cannot close it,
// cannot send on it, and the compiler enforces that at the call site rather
// than in review.
//
// Use them on every channel parameter. The cost is four characters and the
// benefit is that the data flow is visible in the signature.

// produce takes a send-only channel. It closes when done, which is correct
// because it is the sender.
//
// Note that closing IS allowed on a chan<-. Close is a sender operation, and
// the restriction that matters is the receiver being unable to close.
func produce(out chan<- int, n int) {
	defer close(out)

	for i := 0; i < n; i++ {
		out <- i * i
	}
}

// consume takes a receive-only channel. The compiler will not let it send or
// close, so a whole class of "the consumer closed the channel" bug cannot be
// written here.
//
//	out <- 1    -> invalid operation: cannot send to receive-only channel
//	close(in)   -> invalid operation: cannot close receive-only channel
func consume(in <-chan int) (sum int, count int) {
	for v := range in {
		sum += v
		count++
	}
	return sum, count
}

// generator is the most common shape in Go concurrency: a function that starts
// a goroutine and returns a receive-only channel for its output.
//
// Returning <-chan rather than chan is the whole point. The caller gets the
// values and cannot interfere with the stream, so the generator's goroutine
// remains the sole owner of the send side and the only thing that can close it.
func generator(words []string) <-chan string {
	out := make(chan string)

	go func() {
		defer close(out)
		for _, w := range words {
			out <- strings.ToUpper(w)
		}
	}()

	return out // implicitly converted to receive-only
}

// bridge shows both directions in one signature, which is what every stage of
// a pipeline looks like. Lesson 08 builds full pipelines out of these.
func bridge(in <-chan int, out chan<- string) {
	defer close(out)

	for v := range in {
		out <- fmt.Sprintf("value-%d", v)
	}
}

// ownershipTransfer is the idea behind "share memory by communicating". The
// sender builds a value, hands it over, and never touches it again. After the
// send, the receiver is the sole owner, so no lock is needed anywhere.
//
// The discipline is not enforced by the compiler. Keeping a reference to a
// slice or pointer after sending it, and then writing to it, is a data race
// that looks like perfectly ordinary code.
type batch struct {
	ID    int
	Items []string
}

// buildAndSend constructs a batch and gives it away. The local variable goes
// out of scope immediately after the send, which is the pattern that makes the
// transfer obvious to a reader.
func buildAndSend(out chan<- *batch, id int, items []string) {
	b := &batch{ID: id, Items: append([]string(nil), items...)} // a copy we own

	out <- b
	// Nothing below may touch b. It belongs to the receiver now.
}

// receiveAndOwn is free to mutate what it was given, with no lock, because
// ownership moved with the value.
func receiveAndOwn(in <-chan *batch) []string {
	var out []string

	for b := range in {
		b.Items = append(b.Items, fmt.Sprintf("processed-by-%d", b.ID)) // safe: sole owner
		out = append(out, strings.Join(b.Items, "+"))
	}

	return out
}

// demoDirections prints directional channels and ownership transfer.
func demoDirections() {
	ch := make(chan int)
	go produce(ch, 5)
	sum, count := consume(ch)
	fmt.Printf("  produce -> consume: %d values summing to %d\n", count, sum)

	var got []string
	for w := range generator([]string{"go", "channels", "ownership"}) {
		got = append(got, w)
	}
	fmt.Printf("  generator returning <-chan: %v\n", got)

	nums := make(chan int)
	strs := make(chan string)
	go produce(nums, 3)
	go bridge(nums, strs)

	var bridged []string
	for s := range strs {
		bridged = append(bridged, s)
	}
	fmt.Printf("  two-stage bridge: %v\n", bridged)

	batches := make(chan *batch)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(batches)
		for i := 1; i <= 3; i++ {
			buildAndSend(batches, i, []string{fmt.Sprintf("item-%d", i)})
		}
	}()

	owned := receiveAndOwn(batches)
	wg.Wait()
	fmt.Printf("  ownership transfer, no locks: %v\n", owned)

	fmt.Println("\n  what the compiler refuses:")
	fmt.Println("    out <- v  on a <-chan  -> cannot send to receive-only channel")
	fmt.Println("    close(in) on a <-chan  -> cannot close receive-only channel")
	fmt.Println("    <-out     on a chan<-  -> cannot receive from send-only channel")
}
