package main

import (
	"fmt"
	"sync"
)

// Closing
// =======
//
// close(ch) means "no more values are coming". It is a broadcast: every
// receiver, now and later, sees it.
//
//	receive on a closed channel  -> zero value, immediately, forever
//	send on a closed channel     -> PANIC
//	close a closed channel       -> PANIC
//	close a nil channel          -> PANIC
//
// Three rules cover nearly every channel bug:
//
//  1. Only the sender closes. It is the only party that knows there is no more.
//  2. Closing is optional. Channels are garbage collected. Close only when a
//     receiver needs to know the stream ended, which means when someone ranges.
//  3. Never close twice. With several senders, none of them knows it is last.

// receiveFromClosedReturnsZero shows that a closed channel is not an error
// condition for a receiver. It yields the element type's zero value, forever.
func receiveFromClosedReturnsZero() (values []int, oks []bool) {
	ch := make(chan int, 2)
	ch <- 10
	ch <- 20
	close(ch)

	// The two buffered values come out first, with ok=true. Closing does not
	// discard what was already sent.
	for i := 0; i < 4; i++ {
		v, ok := <-ch
		values = append(values, v)
		oks = append(oks, ok)
	}

	return values, oks
}

// commaOkDisambiguates is why the two-value form exists. A plain <-ch returning
// 0 could mean "a real zero was sent" or "the channel is closed", and there is
// no way to tell them apart.
func commaOkDisambiguates() (realZero, realZeroOK, afterClose int, afterCloseOK bool) {
	ch := make(chan int, 1)
	ch <- 0 // a genuine zero value

	v1, ok1 := <-ch
	close(ch)
	v2, ok2 := <-ch

	okAsInt := 0
	if ok1 {
		okAsInt = 1
	}
	return v1, okAsInt, v2, ok2
}

// rangeStopsOnClose is the idiomatic consumer. It receives until the channel
// closes and drains, then the loop ends. Without the close it blocks forever,
// which is leak shape 2 from lesson 06.
func rangeStopsOnClose(n int) (received []int) {
	ch := make(chan int)

	go func() {
		defer close(ch) // the sender closes, in a defer so a panic still does
		for i := 0; i < n; i++ {
			ch <- i
		}
	}()

	for v := range ch {
		received = append(received, v)
	}

	return received
}

// capturePanic runs fn and returns the panic message, or "" if it returned.
func capturePanic(fn func()) (message string) {
	defer func() {
		if r := recover(); r != nil {
			message = fmt.Sprint(r)
		}
	}()

	fn()
	return ""
}

// theThreePanics collects the three ways closing goes wrong.
func theThreePanics() map[string]string {
	return map[string]string{
		"send on closed": capturePanic(func() {
			ch := make(chan int, 1)
			close(ch)
			ch <- 1
		}),
		"close of closed": capturePanic(func() {
			ch := make(chan int)
			close(ch)
			close(ch)
		}),
		"close of nil": capturePanic(func() {
			var ch chan int // nil
			close(ch)
		}),
	}
}

// multipleSendersNeedACloser is the pattern for the case rule 3 warns about.
// No individual sender can close, because none of them knows it is last. One
// goroutine waits for all of them and closes once.
//
// This shape, WaitGroup plus a closer goroutine, is the standard answer and
// worth knowing by heart.
func multipleSendersNeedACloser(senders, perSender int) (received int) {
	ch := make(chan int)

	var wg sync.WaitGroup
	for s := 0; s < senders; s++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perSender; i++ {
				ch <- s*perSender + i
			}
		}()
	}

	// One closer. It owns the close, and it is the only goroutine that can know
	// every sender has finished.
	go func() {
		wg.Wait()
		close(ch)
	}()

	for range ch {
		received++
	}

	return received
}

// closeAsBroadcast is the other major use of close, and it has nothing to do
// with streaming values. Every receiver on a closed channel is released at
// once, so a channel of struct{} closed by one goroutine is a broadcast signal
// to any number of waiters.
//
// This is how a "done" or "quit" channel works, and how sync.Once-style
// one-shot notification is usually spelled.
func closeAsBroadcast(waiters int) (released int) {
	var (
		mu sync.Mutex
		wg sync.WaitGroup
	)

	start := make(chan struct{}) // no values will ever be sent on this

	for i := 0; i < waiters; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			<-start // every one of these blocks until the close

			mu.Lock()
			defer mu.Unlock()
			released++
		}()
	}

	close(start) // releases all of them simultaneously
	wg.Wait()

	return released
}

// sendOnClosedIsUnavoidableWithMultipleSenders documents why "just check
// whether it is closed first" does not work. There is no such check, and even
// if there were, the channel could close between the check and the send.
//
// The answer is structural: give senders a done channel to select on, so they
// stop sending before the close happens. Lesson 08 covers the select form.
func whyThereIsNoIsClosed() []string {
	return []string{
		"there is no isClosed(ch): the check would be stale before the send",
		"len(ch) and cap(ch) say nothing about closed state",
		"the fix is structural: senders select on a done channel and stop first",
		"or: one closer goroutine after a WaitGroup, as above",
	}
}

// demoClosing prints closed-channel behaviour and the panics.
func demoClosing() {
	values, oks := receiveFromClosedReturnsZero()
	fmt.Printf("  4 receives from a closed channel holding 2 values:\n")
	fmt.Printf("    values: %v\n    ok:     %v\n", values, oks)

	realZero, realZeroOK, afterClose, afterCloseOK := commaOkDisambiguates()
	fmt.Printf("\n  a real zero:      v=%d ok=%d\n", realZero, realZeroOK)
	fmt.Printf("  after close:      v=%d ok=%t   <- same value, different meaning\n", afterClose, afterCloseOK)

	fmt.Printf("\n  range over a channel closed by its sender: %v\n", rangeStopsOnClose(5))

	fmt.Println("\n  the three close panics:")
	panics := theThreePanics()
	for _, k := range []string{"send on closed", "close of closed", "close of nil"} {
		fmt.Printf("    %-16s %s\n", k+":", panics[k])
	}

	fmt.Printf("\n  4 senders x 25 values, one closer goroutine: %d received\n",
		multipleSendersNeedACloser(4, 25))
	fmt.Printf("  close as a broadcast: %d waiters released at once\n", closeAsBroadcast(100))

	fmt.Println("\n  why there is no isClosed():")
	for _, s := range whyThereIsNoIsClosed() {
		fmt.Printf("    %s\n", s)
	}
}
