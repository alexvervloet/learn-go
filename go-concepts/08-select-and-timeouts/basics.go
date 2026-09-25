// Package main is lesson 08 of go-concepts: select and timeouts.
//
//	select {
//	case v := <-a:      // whichever is ready first
//	case b <- v:        // sends work too
//	case <-ctx.Done():  // cancellation
//	default:            // optional: runs when nothing else is ready
//	}
//
// Rules:
//
//   - several ready  -> one chosen UNIFORMLY AT RANDOM, not in source order
//   - none ready, default present -> default, immediately
//   - none ready, no default      -> blocks until one is
//   - select{}                    -> blocks forever
//   - a nil channel is never ready, which is how you disable a case
package main

import (
	"fmt"
	"sync"
	"time"
)

// randomChoiceAmongReady sends on both channels first, so both cases are ready
// every time, then records which one select picked. Over many rounds the split
// approaches 50/50.
//
// The randomness is specified behaviour, not an implementation detail. It
// exists to prevent starvation: without it, a busy channel written first in the
// source would permanently starve a quieter one below it.
func randomChoiceAmongReady(rounds int) (firstChosen, secondChosen int) {
	for i := 0; i < rounds; i++ {
		a := make(chan int, 1)
		b := make(chan int, 1)
		a <- 1
		b <- 2

		select {
		case <-a:
			firstChosen++
		case <-b:
			secondChosen++
		}
	}

	return firstChosen, secondChosen
}

// defaultMakesItNonBlocking turns select into a try. Nothing is ever sent on
// this channel, so without the default the select would block forever.
func defaultMakesItNonBlocking() (tookDefault bool) {
	ch := make(chan int)

	select {
	case <-ch:
		return false
	default:
		return true
	}
}

// tryReceive is the reusable form: take a value if one is waiting, otherwise
// report that none was, without blocking.
func tryReceive(ch <-chan int) (value int, ok bool) {
	select {
	case v, open := <-ch:
		return v, open
	default:
		return 0, false
	}
}

// trySend is the counterpart, and the correct way to ask "can I send without
// blocking". Checking len(ch) < cap(ch) first is wrong: another goroutine can
// fill the buffer between the check and the send.
func trySend(ch chan<- int, v int) (sent bool) {
	select {
	case ch <- v:
		return true
	default:
		return false
	}
}

// dropOnFullChannel is what trySend is for. Under load, dropping is often
// better than queueing without limit: metrics, logs and telemetry usually
// prefer losing a sample to consuming memory until the process dies.
//
// It returns how many were accepted and how many dropped.
func dropOnFullChannel(capacity, attempts int) (accepted, dropped int) {
	ch := make(chan int, capacity) // nobody receives

	for i := 0; i < attempts; i++ {
		if trySend(ch, i) {
			accepted++
		} else {
			dropped++
		}
	}

	return accepted, dropped
}

// latestValueWins keeps a channel holding only the most recent value, by
// draining the old one before sending. This is what you want for a "current
// state" signal such as a config reload or a progress update, where an old
// value is worthless once a newer one exists.
func latestValueWins(ch chan int, v int) {
	// Clear whatever is queued. The select-with-default makes this safe even
	// when the channel is already empty.
	select {
	case <-ch:
	default:
	}

	select {
	case ch <- v:
	default:
		// Someone refilled it between the drain and the send. Rare, and the
		// value is about to be superseded anyway.
	}
}

// selectOnSendsToo: people forget that a select case can be a SEND. This is
// what makes a producer cancellable, which was the fix for leak shape 1 in
// lesson 06.
func selectOnSendsToo(values int) (sent int, cancelled bool) {
	out := make(chan int) // nobody receives
	done := make(chan struct{})

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < values; i++ {
			select {
			case out <- i:
				sent++
			case <-done:
				cancelled = true
				return
			}
		}
	}()

	time.Sleep(10 * time.Millisecond)
	close(done) // the producer is blocked on its send; this releases it
	wg.Wait()

	return sent, cancelled
}

// emptySelectBlocksForever documents `select{}`. It is occasionally used as
// "park this goroutine permanently" in a main that has handed all work to
// others, and it is almost always a mistake, because a blocked main cannot
// handle a shutdown signal.
func emptySelectBlocksForever() string {
	return "select{} parks the goroutine with no way out; prefer waiting on a signal channel"
}

// demoBasics prints the select rules.
func demoBasics() {
	first, second := randomChoiceAmongReady(10_000)
	fmt.Printf("  both cases ready, 10,000 rounds: first %d (%.1f%%), second %d (%.1f%%)\n",
		first, float64(first)/100, second, float64(second)/100)
	fmt.Println("  ...uniformly random, not source order. This prevents starvation.")

	fmt.Printf("\n  select with default on an empty channel took default: %t\n", defaultMakesItNonBlocking())

	ch := make(chan int, 1)
	_, ok := tryReceive(ch)
	fmt.Printf("  tryReceive on empty:  ok=%t\n", ok)
	ch <- 42
	v, ok := tryReceive(ch)
	fmt.Printf("  tryReceive with data: v=%d ok=%t\n", v, ok)

	accepted, dropped := dropOnFullChannel(10, 100)
	fmt.Printf("\n  100 sends into a buffer of 10, nobody receiving: %d accepted, %d dropped\n",
		accepted, dropped)

	latest := make(chan int, 1)
	for i := 1; i <= 5; i++ {
		latestValueWins(latest, i)
	}
	fmt.Printf("  latest-value-wins after sending 1..5: %d (len %d)\n", <-latest, len(latest))

	sent, cancelled := selectOnSendsToo(1000)
	fmt.Printf("\n  a blocked producer cancelled via select-on-send: sent=%d cancelled=%t\n", sent, cancelled)

	fmt.Printf("  %s\n", emptySelectBlocksForever())
}
