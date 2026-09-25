package main

import (
	"fmt"
	"time"
)

// Deadlock
// ========
//
// When EVERY goroutine is blocked, the runtime notices and aborts:
//
//	fatal error: all goroutines are asleep - deadlock!
//
// This is a fatal error, not a panic. recover cannot catch it, and no deferred
// call runs. That is deliberate: there is nothing left running to recover into.
//
// The detection is all-or-nothing. A program with one goroutine spinning and
// twenty blocked will hang silently instead, which is the harder case. For that
// there is `go test -timeout`, and GOTRACEBACK=all to dump every stack.
//
// None of the functions below actually deadlock. Each runs its blocking part in
// a goroutine and gives up after a timeout, so the demo survives. The shapes are
// what matter.

// deadlockShape names a way to deadlock and provides a version that would.
type deadlockShape struct {
	name string
	why  string
	// try runs the blocking operation; it returns only if the operation
	// unexpectedly completes.
	try func()
}

// wouldBlockForever runs fn in a goroutine and reports whether it finished
// within d. Every shape below blocks, so this returns false for all of them.
//
// The goroutine is deliberately leaked. That is what a deadlocked goroutine IS,
// and pretending otherwise would misrepresent the problem. In a real program
// the leak is the bug.
func wouldBlockForever(fn func(), d time.Duration) (completed bool) {
	done := make(chan struct{})

	go func() {
		defer close(done)
		fn()
	}()

	select {
	case <-done:
		return true
	case <-time.After(d):
		return false
	}
}

// deadlockShapes is the catalogue. Each of these, run on its own in main with
// no other goroutines, produces "fatal error: all goroutines are asleep".
func deadlockShapes() []deadlockShape {
	return []deadlockShape{
		{
			name: "unbuffered send, no receiver",
			why:  "the send waits for a receiver that never arrives",
			try: func() {
				ch := make(chan int)
				ch <- 1
			},
		},
		{
			name: "receive, no sender",
			why:  "the receive waits for a value nobody sends",
			try: func() {
				ch := make(chan int)
				<-ch
			},
		},
		{
			name: "full buffer, no receiver",
			why:  "capacity 1 absorbs the first send; the second has nowhere to go",
			try: func() {
				ch := make(chan int, 1)
				ch <- 1
				ch <- 2
			},
		},
		{
			name: "nil channel",
			why:  "send and receive on a nil channel block forever, by definition",
			try: func() {
				var ch chan int // nil, never made
				ch <- 1
			},
		},
		{
			name: "range over a channel never closed",
			why:  "range ends only on close; without one it waits for a value forever",
			try: func() {
				ch := make(chan int, 1)
				ch <- 1
				for range ch { //nolint:revive // the missing close is the shape
				}
			},
		},
		{
			name: "two goroutines waiting on each other",
			why:  "each holds what the other needs; neither can proceed",
			try: func() {
				a, b := make(chan int), make(chan int)
				go func() {
					<-a // waits for the other side to send on a
					b <- 1
				}()
				<-b // waits for the goroutine, which is waiting for us
				a <- 1
			},
		},
	}
}

// nilChannelBlocksForever is worth isolating, because it looks like a mistake
// and is genuinely useful. A nil channel blocks on both send and receive, which
// in a select means that arm is never chosen.
//
// That is how you disable a select case at runtime: set the channel variable to
// nil. Lesson 08 uses it to stop watching an input that has finished.
func nilChannelBlocksForever() (sendCompleted, receiveCompleted bool) {
	var ch chan int

	return wouldBlockForever(func() { ch <- 1 }, 20*time.Millisecond),
		wouldBlockForever(func() { <-ch }, 20*time.Millisecond)
}

// detectionIsAllOrNothing explains the limit of the runtime's detector, which
// is the part people are surprised by in production.
func detectionIsAllOrNothing() []string {
	return []string{
		"the runtime aborts only when EVERY goroutine is blocked",
		"one goroutine spinning, or a live timer, and it hangs silently instead",
		"a blocked net.Conn read counts as runnable, so servers never trip it",
		"so: go test -timeout, GOTRACEBACK=all, and /debug/pprof/goroutine",
	}
}

// demoDeadlocks prints the catalogue without deadlocking.
func demoDeadlocks() {
	fmt.Println("  each of these blocks forever (run alone in main: fatal error):")
	for _, shape := range deadlockShapes() {
		completed := wouldBlockForever(shape.try, 20*time.Millisecond)
		fmt.Printf("    %-38s blocked: %t\n", shape.name, !completed)
		fmt.Printf("      %s\n", shape.why)
	}

	send, receive := nilChannelBlocksForever()
	fmt.Printf("\n  nil channel: send completed=%t receive completed=%t\n", send, receive)
	fmt.Println("    ...which is useful: a nil channel in a select disables that arm (lesson 08)")

	fmt.Println("\n  the detector's limits:")
	for _, s := range detectionIsAllOrNothing() {
		fmt.Printf("    %s\n", s)
	}
}
