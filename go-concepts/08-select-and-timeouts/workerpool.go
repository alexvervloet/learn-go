package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// A worker pool with results, errors and cancellation
// ===================================================
//
// The shape almost every Go service needs: N workers pulling from a job
// channel, writing to a results channel, stopping on cancellation, and
// reporting failures without losing successes.
//
//	jobs ──┬──> worker 1 ──┐
//	       ├──> worker 2 ──┼──> results
//	       └──> worker 3 ──┘
//
// Several receivers on one channel is safe and needs no coordination: each
// value goes to exactly one of them. That is what makes the fan-out free.

// Job is a unit of work.
type Job struct {
	ID    int
	Input int
}

// Result pairs a job with its outcome. Carrying the error IN the result rather
// than on a separate channel is the choice that makes the consumer simple: one
// channel to range over, and every job accounted for.
type Result struct {
	JobID  int
	Output int
	Err    error
	Worker int
}

// ErrNegativeInput is returned by the sample processor for invalid input.
var ErrNegativeInput = errors.New("input must not be negative")

// process is the work itself. It respects cancellation, because a worker that
// cannot be interrupted makes the whole pool uncancellable.
func process(ctx context.Context, j Job, each time.Duration) (int, error) {
	if j.Input < 0 {
		return 0, fmt.Errorf("job %d: %w", j.ID, ErrNegativeInput)
	}

	select {
	case <-ctx.Done():
		return 0, fmt.Errorf("job %d: %w", j.ID, ctx.Err())
	case <-time.After(each):
		return j.Input * j.Input, nil
	}
}

// RunPool runs jobs across `workers` goroutines and returns every result.
//
// The structure, in order:
//
//  1. A feeder goroutine sends jobs and closes the jobs channel.
//  2. N workers range over jobs, so they exit when it closes.
//  3. A closer goroutine waits for the workers and closes results.
//  4. The caller ranges over results until it closes.
//
// Each of those four is a "known way to exit". Remove any one and the pool
// either leaks or deadlocks.
func RunPool(ctx context.Context, workers int, jobs []Job, each time.Duration) []Result {
	jobCh := make(chan Job)
	resultCh := make(chan Result)

	// 1. Feeder. Selecting on ctx.Done means cancellation stops the feed
	//    rather than blocking here when the workers have gone.
	go func() {
		defer close(jobCh)

		for _, j := range jobs {
			select {
			case jobCh <- j:
			case <-ctx.Done():
				return
			}
		}
	}()

	// 2. Workers.
	var wg sync.WaitGroup
	wg.Add(workers)

	for w := 1; w <= workers; w++ {
		go func() {
			defer wg.Done()

			for j := range jobCh {
				out, err := process(ctx, j, each)

				select {
				case resultCh <- Result{JobID: j.ID, Output: out, Err: err, Worker: w}:
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	// 3. Closer.
	go func() {
		wg.Wait()
		close(resultCh)
	}()

	// 4. Collector.
	var results []Result
	for r := range resultCh {
		results = append(results, r)
	}

	return results
}

// PoolSummary aggregates what a caller usually wants out of a pool run.
type PoolSummary struct {
	Succeeded   int
	Failed      int
	Cancelled   int
	TotalOut    int
	WorkersUsed map[int]int
}

// Summarise folds results into counts. Note that it separates cancellation from
// failure: a job cancelled because the caller gave up is not the same as a job
// that was invalid, and reporting them together hides whether the work was
// actually wrong.
func Summarise(results []Result) PoolSummary {
	s := PoolSummary{WorkersUsed: make(map[int]int)}

	for _, r := range results {
		s.WorkersUsed[r.Worker]++

		switch {
		case r.Err == nil:
			s.Succeeded++
			s.TotalOut += r.Output
		case errors.Is(r.Err, context.Canceled), errors.Is(r.Err, context.DeadlineExceeded):
			s.Cancelled++
		default:
			s.Failed++
		}
	}

	return s
}

// RunPoolFailFast stops at the first real failure, cancelling the rest. This is
// the other common policy, and the difference from RunPool is one cancel call.
//
// Worth being explicit about the trade: fail-fast returns sooner and gives an
// incomplete picture. For a batch import, knowing all 12 bad rows in one pass
// beats discovering them one deploy at a time.
func RunPoolFailFast(ctx context.Context, workers int, jobs []Job, each time.Duration) ([]Result, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		results  []Result
		firstErr error
		resultCh = make(chan Result)
		jobCh    = make(chan Job)
		wg       sync.WaitGroup
	)

	go func() {
		defer close(jobCh)
		for _, j := range jobs {
			select {
			case jobCh <- j:
			case <-ctx.Done():
				return
			}
		}
	}()

	wg.Add(workers)
	for w := 1; w <= workers; w++ {
		go func() {
			defer wg.Done()
			for j := range jobCh {
				out, err := process(ctx, j, each)
				select {
				case resultCh <- Result{JobID: j.ID, Output: out, Err: err, Worker: w}:
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	go func() {
		wg.Wait()
		close(resultCh)
	}()

	for r := range resultCh {
		results = append(results, r)

		// Cancellation errors are a CONSEQUENCE of stopping, not a reason to
		// stop. Treating them as the first error would report the wrong cause.
		if r.Err != nil && firstErr == nil &&
			!errors.Is(r.Err, context.Canceled) && !errors.Is(r.Err, context.DeadlineExceeded) {
			firstErr = r.Err
			cancel()
		}
	}

	return results, firstErr
}

// demoWorkerPool prints both pool policies.
func demoWorkerPool() {
	jobs := make([]Job, 0, 12)
	for i := 1; i <= 12; i++ {
		input := i
		if i == 7 {
			input = -1 // one bad job
		}
		jobs = append(jobs, Job{ID: i, Input: input})
	}

	ctx := context.Background()

	start := time.Now()
	results := RunPool(ctx, 4, jobs, 10*time.Millisecond)
	elapsed := time.Since(start)
	summary := Summarise(results)

	fmt.Printf("  12 jobs, 4 workers, 10ms each:\n")
	fmt.Printf("    %d succeeded, %d failed, %d cancelled, sum=%d\n",
		summary.Succeeded, summary.Failed, summary.Cancelled, summary.TotalOut)
	fmt.Printf("    took %v (serial would be ~120ms)\n", elapsed.Round(time.Millisecond))
	fmt.Printf("    work per worker: %v\n", summary.WorkersUsed)

	results, err := RunPoolFailFast(ctx, 4, jobs, 10*time.Millisecond)
	failFast := Summarise(results)
	fmt.Printf("\n  fail-fast on the same jobs:\n")
	fmt.Printf("    stopped with: %v\n", err)
	fmt.Printf("    %d succeeded, %d failed, %d cancelled of %d jobs\n",
		failFast.Succeeded, failFast.Failed, failFast.Cancelled, len(jobs))

	// Cancellation partway through.
	timeoutCtx, cancel := context.WithTimeout(ctx, 25*time.Millisecond)
	defer cancel()

	results = RunPool(timeoutCtx, 2, jobs, 10*time.Millisecond)
	cut := Summarise(results)
	fmt.Printf("\n  with a 25ms deadline and 2 workers:\n")
	fmt.Printf("    %d succeeded, %d cancelled, %d results of %d jobs\n",
		cut.Succeeded, cut.Cancelled, len(results), len(jobs))
}
