package main

import (
	"context"
	"runtime"
	"slices"
	"sync"
	"testing"
	"time"
)

func collect(ch <-chan int) []int {
	var out []int
	for v := range ch {
		out = append(out, v)
	}
	return out
}

func TestPipelineStages(t *testing.T) {
	ctx := context.Background()

	t.Run("generate", func(t *testing.T) {
		withTimeout(t, 2*time.Second, func() {
			got := collect(generate(ctx, 1, 2, 3))
			if want := []int{1, 2, 3}; !slices.Equal(got, want) {
				t.Errorf("got %v, want %v", got, want)
			}
		})
	})

	t.Run("generate with no values closes immediately", func(t *testing.T) {
		withTimeout(t, 2*time.Second, func() {
			if got := collect(generate(ctx)); got != nil {
				t.Errorf("got %v, want nothing", got)
			}
		})
	})

	t.Run("square", func(t *testing.T) {
		withTimeout(t, 2*time.Second, func() {
			got := collect(square(ctx, generate(ctx, 1, 2, 3, 4)))
			if want := []int{1, 4, 9, 16}; !slices.Equal(got, want) {
				t.Errorf("got %v, want %v", got, want)
			}
		})
	})

	t.Run("filter", func(t *testing.T) {
		withTimeout(t, 2*time.Second, func() {
			even := func(v int) bool { return v%2 == 0 }
			got := collect(filter(ctx, generate(ctx, 1, 2, 3, 4, 5, 6), even))
			if want := []int{2, 4, 6}; !slices.Equal(got, want) {
				t.Errorf("got %v, want %v", got, want)
			}
		})
	})

	t.Run("three stages composed", func(t *testing.T) {
		withTimeout(t, 2*time.Second, func() {
			even := func(v int) bool { return v%2 == 0 }
			got := collect(filter(ctx, square(ctx, generate(ctx, 1, 2, 3, 4, 5, 6, 7, 8)), even))
			if want := []int{4, 16, 36, 64}; !slices.Equal(got, want) {
				t.Errorf("got %v, want %v", got, want)
			}
		})
	})
}

func TestFanOutFanIn(t *testing.T) {
	withTimeout(t, 5*time.Second, func() {
		ctx := context.Background()

		src := generate(ctx, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10)
		workers := fanOut(ctx, src, 4, func(v int) int { return v * 10 })

		got := collect(fanIn(ctx, workers...))
		slices.Sort(got)

		want := []int{10, 20, 30, 40, 50, 60, 70, 80, 90, 100}
		if !slices.Equal(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})
}

// TestFanInClosesOnce would panic on a double close. Repeating it catches a
// race in the closer that a single run might miss.
func TestFanInClosesOnce(t *testing.T) {
	ctx := context.Background()

	for run := 0; run < 50; run++ {
		withTimeout(t, 2*time.Second, func() {
			a := generate(ctx, 1)
			b := generate(ctx, 2)
			c := generate(ctx)

			got := collect(fanIn(ctx, a, b, c))
			slices.Sort(got)

			if want := []int{1, 2}; !slices.Equal(got, want) {
				t.Errorf("run %d: got %v, want %v", run, got, want)
			}
		})
	}
}

// TestPipelineCancellationDoesNotLeak is the rule-2 test: a consumer that stops
// early must not strand the upstream stages.
func TestPipelineCancellationDoesNotLeak(t *testing.T) {
	if testing.Short() {
		t.Skip("goroutine leak check")
	}

	before := runtime.NumGoroutine()

	withTimeout(t, 5*time.Second, func() {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel() // the conditional cancel below may not be reached

		// A long pipeline whose consumer takes three values and leaves.
		stream := orDone(ctx, filter(ctx, square(ctx, generate(ctx, makeRange(1000)...)),
			func(v int) bool { return true }))

		taken := 0
		for range stream {
			taken++
			if taken == 3 {
				cancel()
				break
			}
		}

		if taken != 3 {
			t.Errorf("took %d values, want 3", taken)
		}
	})

	// Give the stages time to notice the cancellation and return.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		runtime.Gosched()
		if runtime.NumGoroutine() <= before {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Errorf("pipeline leaked goroutines: %d before, %d after", before, runtime.NumGoroutine())
}

func makeRange(n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = i
	}
	return out
}

func TestOrDone(t *testing.T) {
	t.Run("passes everything through when not cancelled", func(t *testing.T) {
		withTimeout(t, 2*time.Second, func() {
			ctx := context.Background()
			got := collect(orDone(ctx, generate(ctx, 1, 2, 3)))
			if want := []int{1, 2, 3}; !slices.Equal(got, want) {
				t.Errorf("got %v, want %v", got, want)
			}
		})
	})

	t.Run("stops on cancellation", func(t *testing.T) {
		withTimeout(t, 2*time.Second, func() {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			src := generate(ctx, makeRange(1000)...)

			count := 0
			for range orDone(ctx, src) {
				count++
				if count == 5 {
					cancel()
				}
			}

			if count >= 1000 {
				t.Errorf("received %d values, want far fewer after cancelling", count)
			}
		})
	})
}

func TestTee(t *testing.T) {
	withTimeout(t, 5*time.Second, func() {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		t1, t2 := tee(ctx, generate(ctx, 1, 2, 3, 4, 5))

		var (
			a, b []int
			wg   sync.WaitGroup
		)
		wg.Add(2)
		go func() { defer wg.Done(); a = collect(t1) }()
		go func() { defer wg.Done(); b = collect(t2) }()
		wg.Wait()

		want := []int{1, 2, 3, 4, 5}
		if !slices.Equal(a, want) {
			t.Errorf("first consumer got %v, want %v", a, want)
		}
		if !slices.Equal(b, want) {
			t.Errorf("second consumer got %v, want %v", b, want)
		}
	})
}

func TestBridge(t *testing.T) {
	withTimeout(t, 5*time.Second, func() {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		streams := make(chan (<-chan int), 3)
		for i := 0; i < 3; i++ {
			streams <- generate(ctx, i*10, i*10+1)
		}
		close(streams)

		got := collect(bridge(ctx, streams))

		if want := []int{0, 1, 10, 11, 20, 21}; !slices.Equal(got, want) {
			t.Errorf("got %v, want %v — bridge should preserve stream order", got, want)
		}
	})
}

func TestBridgeOnEmptyStream(t *testing.T) {
	withTimeout(t, 2*time.Second, func() {
		ctx := context.Background()

		streams := make(chan (<-chan int))
		close(streams)

		if got := collect(bridge(ctx, streams)); got != nil {
			t.Errorf("got %v, want nothing", got)
		}
	})
}

func TestFirstResultWins(t *testing.T) {
	t.Run("the fastest source wins", func(t *testing.T) {
		withTimeout(t, 5*time.Second, func() {
			got, err := firstResultWins(context.Background(),
				func(ctx context.Context) int { return slowSource(ctx, 200, 1) },
				func(ctx context.Context) int { return slowSource(ctx, 1, 2) },
				func(ctx context.Context) int { return slowSource(ctx, 100, 3) },
			)

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != 2 {
				t.Errorf("winner = %d, want 2 (the 1ms source)", got)
			}
		})
	})

	t.Run("cancellation beats every source", func(t *testing.T) {
		withTimeout(t, 5*time.Second, func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
			defer cancel()

			_, err := firstResultWins(ctx,
				func(ctx context.Context) int { return slowSource(ctx, 5000, 1) },
			)

			if err == nil {
				t.Error("expected the context deadline to win")
			}
		})
	})

	// The losers must not leak. The result channel is buffered for exactly
	// that reason: they still send, and nobody is reading.
	t.Run("losing sources do not leak", func(t *testing.T) {
		if testing.Short() {
			t.Skip("goroutine leak check")
		}

		before := runtime.NumGoroutine()

		withTimeout(t, 5*time.Second, func() {
			_, _ = firstResultWins(context.Background(),
				func(ctx context.Context) int { return slowSource(ctx, 1, 1) },
				func(ctx context.Context) int { return slowSource(ctx, 2000, 2) },
				func(ctx context.Context) int { return slowSource(ctx, 3000, 3) },
			)
		})

		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			runtime.Gosched()
			if runtime.NumGoroutine() <= before {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}

		t.Errorf("leaked: %d goroutines before, %d after", before, runtime.NumGoroutine())
	})
}
