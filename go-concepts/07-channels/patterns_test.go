package main

import (
	"slices"
	"testing"
	"time"
)

func TestPingPong(t *testing.T) {
	withTimeout(t, 2*time.Second, func() {
		got := pingPong(3)

		// Two entries per round, in strict alternation, because the channels
		// are unbuffered and neither side can run ahead.
		want := []string{
			"pong received 0", "ping received 1",
			"pong received 10", "ping received 11",
			"pong received 20", "ping received 21",
		}
		if !slices.Equal(got, want) {
			t.Errorf("log =\n  %v\nwant\n  %v", got, want)
		}
	})
}

// TestBoundedConcurrency is the semaphore's contract: never more than `limit`
// in flight, and all tasks eventually run.
func TestBoundedConcurrency(t *testing.T) {
	tests := []struct {
		name           string
		tasks, limit   int
		wantPeakAtMost int
	}{
		{"limit below task count", 20, 4, 4},
		{"limit of one serialises", 10, 1, 1},
		{"limit above task count", 3, 10, 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withTimeout(t, 10*time.Second, func() {
				peak := boundedConcurrency(tt.tasks, tt.limit, 5*time.Millisecond)

				if peak > tt.wantPeakAtMost {
					t.Errorf("peak concurrency = %d, want at most %d", peak, tt.wantPeakAtMost)
				}
				if peak < 1 {
					t.Errorf("peak concurrency = %d, want at least 1", peak)
				}
			})
		})
	}
}

// TestSemaphoreReleasesOnPanic: release is deferred, so a panicking task must
// not strand a permit. Without the defer, the pool drains and deadlocks.
func TestSemaphoreReleasesOnPanic(t *testing.T) {
	withTimeout(t, 2*time.Second, func() {
		sem := newSemaphore(1)

		func() {
			defer func() { _ = recover() }()
			sem.acquire()
			defer sem.release()
			panic("boom")
		}()

		// If release had not run, this would block forever.
		sem.acquire()
		sem.release()
	})
}

func TestDoneChannel(t *testing.T) {
	for _, workers := range []int{1, 50, 500} {
		withTimeout(t, 5*time.Second, func() {
			if got := doneChannel(workers); got != workers {
				t.Errorf("stopped %d of %d workers", got, workers)
			}
		})
	}
}

// TestCounterServer: state owned by one goroutine, reached only by messages.
// -race confirms the map is never touched concurrently.
func TestCounterServer(t *testing.T) {
	withTimeout(t, 2*time.Second, func() {
		requests := make(chan request)
		done := make(chan struct{})
		defer close(done)

		go counterServer(requests, done)

		var hits []int
		for i := 0; i < 5; i++ {
			hits = append(hits, askCounter(requests, "hits"))
		}
		if want := []int{1, 2, 3, 4, 5}; !slices.Equal(hits, want) {
			t.Errorf("hits = %v, want %v", hits, want)
		}

		if got := askCounter(requests, "other"); got != 1 {
			t.Errorf("a second key started at %d, want 1", got)
		}
	})
}

// TestCounterServerStopsOnClosedRequests covers the other exit path.
func TestCounterServerStopsOnClosedRequests(t *testing.T) {
	withTimeout(t, 2*time.Second, func() {
		requests := make(chan request)
		done := make(chan struct{})
		stopped := make(chan struct{})

		go func() {
			defer close(stopped)
			counterServer(requests, done)
		}()

		close(requests)
		<-stopped // must return rather than spinning on a closed channel

		close(done)
	})
}

func TestFanInSimple(t *testing.T) {
	withTimeout(t, 2*time.Second, func() {
		a := make(chan int)
		b := make(chan int)

		go func() {
			defer close(a)
			for i := 0; i < 3; i++ {
				a <- i
			}
		}()
		go func() {
			defer close(b)
			for i := 10; i < 13; i++ {
				b <- i
			}
		}()

		var got []int
		for v := range fanInSimple(a, b) {
			got = append(got, v)
		}

		// Interleaving is nondeterministic; the SET is not.
		slices.Sort(got)
		if want := []int{0, 1, 2, 10, 11, 12}; !slices.Equal(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})
}

// TestFanInClosesExactlyOnce: with two inputs, a naive implementation closes
// the output twice and panics. This runs it repeatedly to catch a race in the
// closer.
func TestFanInClosesExactlyOnce(t *testing.T) {
	for run := 0; run < 50; run++ {
		withTimeout(t, 2*time.Second, func() {
			a := make(chan int)
			b := make(chan int)
			close(a)
			close(b)

			count := 0
			for range fanInSimple(a, b) {
				count++
			}
			if count != 0 {
				t.Errorf("run %d: got %d values from two closed channels", run, count)
			}
		})
	}
}
