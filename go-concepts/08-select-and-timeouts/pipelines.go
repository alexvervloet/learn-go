package main

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"
)

// timeAfterMillis is a small helper so the demo reads in milliseconds.
func timeAfterMillis(ms int) <-chan time.Time {
	return time.After(time.Duration(ms) * time.Millisecond)
}

// Pipelines
// =========
//
// A pipeline is a series of stages, each a function that takes a receive-only
// input channel and returns a receive-only output channel, running its work in
// its own goroutine.
//
//	generate -> square -> filter -> collect
//
// Two rules make stages composable, and breaking either one leaks goroutines:
//
//  1. Every stage CLOSES its output when its input is exhausted or cancelled.
//     Downstream ranges then terminate on their own.
//  2. Every stage SELECTS on done/ctx alongside every send. Otherwise a
//     consumer that stops early leaves the whole pipeline blocked upstream.
//
// The second is the one people forget, because the pipeline works perfectly
// until something cancels it.

// generate is the source stage. It takes no input channel because it has none.
func generate(ctx context.Context, values ...int) <-chan int {
	out := make(chan int)

	go func() {
		defer close(out) // rule 1

		for _, v := range values {
			select {
			case out <- v:
			case <-ctx.Done(): // rule 2
				return
			}
		}
	}()

	return out
}

// square is a middle stage. Its shape is the template for every transform:
// range the input, select on the send.
func square(ctx context.Context, in <-chan int) <-chan int {
	out := make(chan int)

	go func() {
		defer close(out)

		for v := range in { // ends when the upstream stage closes
			select {
			case out <- v * v:
			case <-ctx.Done():
				return
			}
		}
	}()

	return out
}

// filter drops values that fail the predicate.
func filter(ctx context.Context, in <-chan int, keep func(int) bool) <-chan int {
	out := make(chan int)

	go func() {
		defer close(out)

		for v := range in {
			if !keep(v) {
				continue
			}
			select {
			case out <- v:
			case <-ctx.Done():
				return
			}
		}
	}()

	return out
}

// fanOut starts n goroutines reading the same input channel. Go's channels are
// safe for concurrent use, so several receivers on one channel each get
// distinct values with no coordination.
//
// This is how you parallelise a slow stage: the rest of the pipeline is
// unchanged, and only this stage gets wider.
func fanOut(ctx context.Context, in <-chan int, n int, work func(int) int) []<-chan int {
	outs := make([]<-chan int, 0, n)

	for i := 0; i < n; i++ {
		out := make(chan int)
		outs = append(outs, out)

		go func() {
			defer close(out)

			for v := range in {
				select {
				case out <- work(v):
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	return outs
}

// fanIn merges several channels into one. The WaitGroup counts the forwarders;
// a separate goroutine closes the output exactly once when all have finished.
//
// Closing inside a forwarder would close it once per input and panic on the
// second.
func fanIn(ctx context.Context, ins ...<-chan int) <-chan int {
	out := make(chan int)

	var wg sync.WaitGroup
	forward := func(in <-chan int) {
		defer wg.Done()

		for v := range in {
			select {
			case out <- v:
			case <-ctx.Done():
				return
			}
		}
	}

	wg.Add(len(ins))
	for _, in := range ins {
		go forward(in)
	}

	go func() {
		wg.Wait()
		close(out) // exactly once
	}()

	return out
}

// orDone wraps a channel so it stops on cancellation. It exists because
// `for v := range ch` cannot be cancelled: range has no select in it.
//
// With orDone, a consumer writes the ordinary range and still stops promptly:
//
//	for v := range orDone(ctx, ch) { ... }
func orDone(ctx context.Context, in <-chan int) <-chan int {
	out := make(chan int)

	go func() {
		defer close(out)

		for {
			select {
			case v, ok := <-in:
				if !ok {
					return
				}
				select {
				case out <- v:
				case <-ctx.Done():
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	return out
}

// tee duplicates one stream into two, so two consumers each see every value.
//
// The subtlety: both outputs must be written before moving to the next input,
// or one consumer falls behind and the other races ahead unboundedly. Setting
// each local variable to nil after its send is the nil-channel trick again,
// used here to send to each output exactly once per value.
func tee(ctx context.Context, in <-chan int) (<-chan int, <-chan int) {
	out1 := make(chan int)
	out2 := make(chan int)

	go func() {
		defer close(out1)
		defer close(out2)

		for v := range orDone(ctx, in) {
			// Shadow them per value; nil-ing a local disables that send.
			a, b := out1, out2

			for i := 0; i < 2; i++ {
				select {
				case <-ctx.Done():
					return
				case a <- v:
					a = nil // sent to out1; do not send again this round
				case b <- v:
					b = nil
				}
			}
		}
	}()

	return out1, out2
}

// bridge flattens a channel of channels into one stream. This is what you get
// from a source that produces batches, each batch its own channel.
func bridge(ctx context.Context, chanStream <-chan <-chan int) <-chan int {
	out := make(chan int)

	go func() {
		defer close(out)

		for {
			var stream <-chan int

			select {
			case maybeStream, ok := <-chanStream:
				if !ok {
					return
				}
				stream = maybeStream
			case <-ctx.Done():
				return
			}

			for v := range orDone(ctx, stream) {
				select {
				case out <- v:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return out
}

// firstResultWins races several sources and returns whichever answers first,
// cancelling the rest. The buffered result channel is essential: the losers
// still try to send, and an unbuffered channel would block them forever.
func firstResultWins(ctx context.Context, sources ...func(context.Context) int) (int, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel() // tells the losers to stop

	results := make(chan int, len(sources)) // room for every source, so none blocks

	for _, src := range sources {
		go func() {
			results <- src(ctx)
		}()
	}

	select {
	case v := <-results:
		return v, nil
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

// demoPipelines prints a full pipeline and each combinator.
func demoPipelines() {
	ctx := context.Background()

	// generate -> square -> filter
	nums := generate(ctx, 1, 2, 3, 4, 5, 6, 7, 8)
	squares := square(ctx, nums)
	evens := filter(ctx, squares, func(v int) bool { return v%2 == 0 })

	var got []int
	for v := range evens {
		got = append(got, v)
	}
	fmt.Printf("  generate -> square -> filter(even): %v\n", got)

	// fan-out then fan-in
	src := generate(ctx, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10)
	workers := fanOut(ctx, src, 4, func(v int) int { return v * 10 })

	var merged []int
	for v := range fanIn(ctx, workers...) {
		merged = append(merged, v)
	}
	slices.Sort(merged)
	fmt.Printf("  fan-out to 4, fan-in:               %v\n", merged)

	// or-done: a consumer that stops early.
	//
	// The defer is not redundant with the cancel() inside the loop. go vet's
	// lostcancel check flags exactly this: if the stream ended before the
	// consumer had taken 3 values, the conditional cancel would never run and
	// the context would leak. Every WithCancel wants a deferred cancel, and
	// calling it twice is explicitly safe.
	cancelCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	stream := generate(cancelCtx, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10)

	var taken []int
	for v := range orDone(cancelCtx, stream) {
		taken = append(taken, v)
		if len(taken) == 3 {
			cancel() // stop the whole pipeline
			break
		}
	}
	fmt.Printf("  or-done, consumer stopped after 3:  %v\n", taken)

	// tee
	teeCtx, teeCancel := context.WithCancel(ctx)
	defer teeCancel()

	t1, t2 := tee(teeCtx, generate(teeCtx, 1, 2, 3))
	var a, b []int
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for v := range t1 {
			a = append(a, v)
		}
	}()
	go func() {
		defer wg.Done()
		for v := range t2 {
			b = append(b, v)
		}
	}()
	wg.Wait()
	fmt.Printf("  tee into two consumers:             %v and %v\n", a, b)

	// bridge
	bridgeCtx, bridgeCancel := context.WithCancel(ctx)
	defer bridgeCancel()

	streams := make(chan (<-chan int), 3)
	for i := 0; i < 3; i++ {
		streams <- generate(bridgeCtx, i*10, i*10+1)
	}
	close(streams)

	var flattened []int
	for v := range bridge(bridgeCtx, streams) {
		flattened = append(flattened, v)
	}
	fmt.Printf("  bridge of 3 channels:               %v\n", flattened)

	// first result wins
	winner, err := firstResultWins(ctx,
		func(ctx context.Context) int { return slowSource(ctx, 100, 1) },
		func(ctx context.Context) int { return slowSource(ctx, 1, 2) },
		func(ctx context.Context) int { return slowSource(ctx, 50, 3) },
	)
	fmt.Printf("  first result wins (source 2 fastest): %d, err=%v\n", winner, err)
}

// slowSource simulates a source that takes a while, and respects cancellation
// so the losers of firstResultWins actually stop.
func slowSource(ctx context.Context, delayMillis, id int) int {
	select {
	case <-ctx.Done():
		return -1
	case <-timeAfterMillis(delayMillis):
		return id
	}
}
