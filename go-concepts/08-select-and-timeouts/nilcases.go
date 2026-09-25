package main

import (
	"fmt"
	"slices"
	"time"
)

// Disabling a select case with a nil channel
// ==========================================
//
// A nil channel is never ready, so a select case on one is never chosen. That
// turns an assignment into a switch:
//
//	a = nil   // this arm is now off
//
// The reason it matters is the opposite problem. A CLOSED channel is always
// ready, and a receive on it returns the zero value instantly, forever. A
// select that keeps a closed channel in it spins at 100% CPU without making
// progress. This is the busy closed-channel loop, and it is the most common
// select bug there is.

// mergeBusyLoop is the bug. When `a` closes, its case becomes permanently
// ready, so the select picks it over and over, appending zero values as fast as
// the CPU allows.
//
// The iteration cap is the only thing keeping this function from running
// forever; without it, this is a hung process at full CPU.
func mergeBusyLoop(a, b <-chan int, maxIterations int) (out []int, iterations int, hitCap bool) {
	aOpen, bOpen := true, true

	for aOpen || bOpen {
		iterations++
		if iterations >= maxIterations {
			return out, iterations, true
		}

		select {
		case v, ok := <-a:
			if !ok {
				aOpen = false
				// BUG: `a` is still in the select, still closed, still ready.
				// Every subsequent iteration lands here and does nothing.
				continue
			}
			out = append(out, v)
		case v, ok := <-b:
			if !ok {
				bOpen = false
				continue
			}
			out = append(out, v)
		}
	}

	return out, iterations, false
}

// mergeNilFix is the same function with one line changed per branch: setting
// the channel variable to nil takes that arm out of the select entirely.
//
// The loop condition then ends naturally when both are nil.
func mergeNilFix(a, b <-chan int) (out []int, iterations int) {
	for a != nil || b != nil {
		iterations++

		select {
		case v, ok := <-a:
			if !ok {
				a = nil // the fix: this case can never be chosen again
				continue
			}
			out = append(out, v)
		case v, ok := <-b:
			if !ok {
				b = nil
				continue
			}
			out = append(out, v)
		}
	}

	return out, iterations
}

// spinDetector measures the pathology properly, which takes some care to set
// up. If BOTH channels close promptly the broken loop terminates anyway, and
// the bug hides: the loop condition goes false and nobody notices.
//
// The bug needs one channel CLOSED while the other is still OPEN and slow. Then
// the closed channel's case is permanently ready, select keeps choosing it, and
// the loop spins at full speed doing nothing while it waits for the slow one.
//
// That asymmetry is exactly what happens in production: one upstream finishes
// early, another is still streaming, and CPU goes to 100% for no visible reason.
func spinDetector(values int, gap time.Duration) (brokenIterations, fixedIterations, expected int) {
	// closedEarly is drained and closed straight away. slow trickles values.
	setup := func() (<-chan int, <-chan int) {
		closedEarly := make(chan int)
		close(closedEarly)

		slow := make(chan int)
		go func() {
			defer close(slow)
			for i := 0; i < values; i++ {
				time.Sleep(gap)
				slow <- i
			}
		}()

		return closedEarly, slow
	}

	a1, b1 := setup()
	_, brokenIterations, _ = mergeBusyLoop(a1, b1, 5_000_000)

	a2, b2 := setup()
	_, fixedIterations = mergeNilFix(a2, b2)

	// The fixed version iterates once per value, plus one per close.
	return brokenIterations, fixedIterations, values + 2
}

// toggleACaseOnAndOff shows the other use: a nil channel is not only for
// "finished", it is for "not right now". Here the output arm is disabled until
// there is something to send, which avoids sending a zero value.
//
// This shape appears whenever a goroutine has to both receive work and emit
// results, and must not emit when it has nothing.
func toggleACaseOnAndOff(inputs []int) (emitted []int) {
	in := make(chan int, len(inputs))
	for _, v := range inputs {
		in <- v
	}
	close(in)

	out := make(chan int, len(inputs))

	var (
		pending  int
		hasValue bool
		inCh     = in
	)

	for {
		// outCh is nil whenever there is nothing to send, so the send arm is
		// off and select cannot choose it.
		var outCh chan<- int
		if hasValue {
			outCh = out
		}

		if inCh == nil && !hasValue {
			break // input exhausted and nothing buffered
		}

		select {
		case v, ok := <-inCh:
			if !ok {
				inCh = nil // input done; stop selecting on it
				continue
			}
			pending = v * 2
			hasValue = true
			inCh = nil // stop taking input until the current value is emitted

		case outCh <- pending:
			hasValue = false
			inCh = in // ready for the next input
		}
	}

	close(out)
	for v := range out {
		emitted = append(emitted, v)
	}
	return emitted
}

// cpuBurnComparison times both versions against the asymmetric setup, so the
// cost of the bug is a measured number rather than an assertion.
func cpuBurnComparison(values int, gap time.Duration) (brokenElapsed, fixedElapsed time.Duration, brokenSpins int) {
	setup := func() (<-chan int, <-chan int) {
		closedEarly := make(chan int)
		close(closedEarly)

		slow := make(chan int)
		go func() {
			defer close(slow)
			for i := 0; i < values; i++ {
				time.Sleep(gap)
				slow <- i
			}
		}()

		return closedEarly, slow
	}

	a1, b1 := setup()
	start := time.Now()
	_, brokenSpins, _ = mergeBusyLoop(a1, b1, 5_000_000)
	brokenElapsed = time.Since(start)

	a2, b2 := setup()
	start = time.Now()
	_, _ = mergeNilFix(a2, b2)
	fixedElapsed = time.Since(start)

	return brokenElapsed, fixedElapsed, brokenSpins
}

// demoNilCases prints the busy loop and its fix.
func demoNilCases() {
	const (
		values = 5
		gap    = 10 * time.Millisecond
	)

	broken, fixed, expected := spinDetector(values, gap)

	fmt.Printf("  one channel closed immediately, another delivering %d values %v apart:\n", values, gap)
	fmt.Printf("    with the bug:  %9d select iterations\n", broken)
	fmt.Printf("    with nil fix:  %9d select iterations (expected %d)\n", fixed, expected)
	fmt.Printf("    ratio:         %9.0fx more work for the same output\n", float64(broken)/float64(fixed))

	brokenElapsed, fixedElapsed, spins := cpuBurnComparison(values, gap)
	fmt.Printf("\n  same work, wall clock:\n")
	fmt.Printf("    with the bug:  %v, %d iterations (one CPU core saturated throughout)\n",
		brokenElapsed.Round(time.Millisecond), spins)
	fmt.Printf("    with nil fix:  %v, parked in the select the whole time\n",
		fixedElapsed.Round(time.Millisecond))
	fmt.Println("    ...both finish. Only one of them cost a core to do it.")

	emitted := toggleACaseOnAndOff([]int{1, 2, 3, 4})
	fmt.Printf("\n  toggling a SEND case on and off: %v\n", emitted)

	a := make(chan int, 3)
	b := make(chan int, 3)
	for i := 0; i < 3; i++ {
		a <- i
		b <- i + 10
	}
	close(a)
	close(b)
	merged, _ := mergeNilFix(a, b)
	slices.Sort(merged)
	fmt.Printf("  merged output: %v\n", merged)
}
