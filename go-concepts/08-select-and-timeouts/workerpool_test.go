package main

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
)

func makeJobs(n int, badAt ...int) []Job {
	jobs := make([]Job, 0, n)
	for i := 1; i <= n; i++ {
		input := i
		if slices.Contains(badAt, i) {
			input = -1
		}
		jobs = append(jobs, Job{ID: i, Input: input})
	}
	return jobs
}

func TestRunPool(t *testing.T) {
	tests := []struct {
		name          string
		workers       int
		jobs          []Job
		wantResults   int
		wantSucceeded int
		wantFailed    int
	}{
		{"all succeed", 4, makeJobs(12), 12, 12, 0},
		{"one bad job", 4, makeJobs(12, 7), 12, 11, 1},
		{"several bad jobs", 3, makeJobs(10, 2, 5, 9), 10, 7, 3},
		{"one worker still processes everything", 1, makeJobs(5), 5, 5, 0},
		{"more workers than jobs", 10, makeJobs(3), 3, 3, 0},
		{"no jobs", 4, nil, 0, 0, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withTimeout(t, 10*time.Second, func() {
				results := RunPool(context.Background(), tt.workers, tt.jobs, time.Millisecond)

				if len(results) != tt.wantResults {
					t.Fatalf("got %d results, want %d — every job must be accounted for",
						len(results), tt.wantResults)
				}

				s := Summarise(results)
				if s.Succeeded != tt.wantSucceeded {
					t.Errorf("succeeded = %d, want %d", s.Succeeded, tt.wantSucceeded)
				}
				if s.Failed != tt.wantFailed {
					t.Errorf("failed = %d, want %d", s.Failed, tt.wantFailed)
				}
			})
		})
	}
}

// TestRunPoolEveryJobAppearsExactlyOnce is the property that matters most: with
// several workers pulling from one channel, no job may be processed twice or
// skipped.
func TestRunPoolEveryJobAppearsExactlyOnce(t *testing.T) {
	withTimeout(t, 10*time.Second, func() {
		jobs := makeJobs(50)

		results := RunPool(context.Background(), 8, jobs, 0)

		seen := make(map[int]int, len(jobs))
		for _, r := range results {
			seen[r.JobID]++
		}

		for _, j := range jobs {
			switch seen[j.ID] {
			case 0:
				t.Errorf("job %d was never processed", j.ID)
			case 1:
				// correct
			default:
				t.Errorf("job %d was processed %d times", j.ID, seen[j.ID])
			}
		}
	})
}

func TestRunPoolIsActuallyParallel(t *testing.T) {
	if testing.Short() {
		t.Skip("timing test")
	}

	withTimeout(t, 10*time.Second, func() {
		const (
			jobs    = 12
			each    = 20 * time.Millisecond
			workers = 4
		)

		start := time.Now()
		results := RunPool(context.Background(), workers, makeJobs(jobs), each)
		elapsed := time.Since(start)

		if len(results) != jobs {
			t.Fatalf("got %d results, want %d", len(results), jobs)
		}

		serial := time.Duration(jobs) * each
		t.Logf("%d jobs x %v with %d workers: %v (serial would be %v)",
			jobs, each, workers, elapsed.Round(time.Millisecond), serial)

		// Generous bound: with 4 workers this should be near a quarter of
		// serial, and anything below three quarters proves parallelism.
		if elapsed > serial*3/4 {
			t.Errorf("took %v, expected well under %v with %d workers", elapsed, serial, workers)
		}
	})
}

func TestRunPoolRespectsCancellation(t *testing.T) {
	withTimeout(t, 10*time.Second, func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
		defer cancel()

		results := RunPool(ctx, 2, makeJobs(50), 10*time.Millisecond)

		if len(results) >= 50 {
			t.Errorf("got %d results, want fewer than 50 — the deadline should have cut it short",
				len(results))
		}

		s := Summarise(results)
		t.Logf("%d results: %d succeeded, %d cancelled, %d failed",
			len(results), s.Succeeded, s.Cancelled, s.Failed)

		// Cancelled jobs must be classified as cancelled, not as failures.
		if s.Failed != 0 {
			t.Errorf("failed = %d, want 0 — cancellation is not a failure", s.Failed)
		}
	})
}

func TestSummarise(t *testing.T) {
	results := []Result{
		{JobID: 1, Output: 1, Worker: 1},
		{JobID: 2, Output: 4, Worker: 1},
		{JobID: 3, Err: ErrNegativeInput, Worker: 2},
		{JobID: 4, Err: context.Canceled, Worker: 2},
		{JobID: 5, Err: context.DeadlineExceeded, Worker: 3},
	}

	s := Summarise(results)

	if s.Succeeded != 2 {
		t.Errorf("succeeded = %d, want 2", s.Succeeded)
	}
	if s.Failed != 1 {
		t.Errorf("failed = %d, want 1", s.Failed)
	}
	if s.Cancelled != 2 {
		t.Errorf("cancelled = %d, want 2", s.Cancelled)
	}
	if s.TotalOut != 5 {
		t.Errorf("total output = %d, want 5", s.TotalOut)
	}
	if s.WorkersUsed[1] != 2 || s.WorkersUsed[2] != 2 || s.WorkersUsed[3] != 1 {
		t.Errorf("workers used = %v, want map[1:2 2:2 3:1]", s.WorkersUsed)
	}
}

func TestRunPoolFailFast(t *testing.T) {
	t.Run("stops at the first real failure", func(t *testing.T) {
		withTimeout(t, 10*time.Second, func() {
			results, err := RunPoolFailFast(context.Background(), 2, makeJobs(50, 3), 5*time.Millisecond)

			if err == nil {
				t.Fatal("expected an error")
			}
			if !errors.Is(err, ErrNegativeInput) {
				t.Errorf("err = %v, want ErrNegativeInput", err)
			}
			if len(results) >= 50 {
				t.Errorf("processed %d of 50 jobs — should have stopped early", len(results))
			}
		})
	})

	t.Run("no error when everything succeeds", func(t *testing.T) {
		withTimeout(t, 10*time.Second, func() {
			results, err := RunPoolFailFast(context.Background(), 4, makeJobs(10), 0)

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(results) != 10 {
				t.Errorf("got %d results, want 10", len(results))
			}
		})
	})

	// The reported error must be the CAUSE, not the cancellation it triggered.
	t.Run("reports the cause, not the cancellation", func(t *testing.T) {
		withTimeout(t, 10*time.Second, func() {
			_, err := RunPoolFailFast(context.Background(), 4, makeJobs(30, 2), 5*time.Millisecond)

			if err == nil {
				t.Fatal("expected an error")
			}
			if errors.Is(err, context.Canceled) {
				t.Errorf("err = %v, want the underlying cause rather than the cancellation", err)
			}
			if !errors.Is(err, ErrNegativeInput) {
				t.Errorf("err = %v, want ErrNegativeInput", err)
			}
		})
	})
}

func TestProcess(t *testing.T) {
	tests := []struct {
		name    string
		job     Job
		wantOut int
		wantErr error
	}{
		{"valid", Job{ID: 1, Input: 5}, 25, nil},
		{"zero is valid", Job{ID: 2, Input: 0}, 0, nil},
		{"negative rejected", Job{ID: 3, Input: -1}, 0, ErrNegativeInput},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withTimeout(t, 2*time.Second, func() {
				got, err := process(context.Background(), tt.job, 0)

				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
				if got != tt.wantOut {
					t.Errorf("output = %d, want %d", got, tt.wantOut)
				}
			})
		})
	}

	t.Run("respects cancellation", func(t *testing.T) {
		withTimeout(t, 2*time.Second, func() {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()

			_, err := process(ctx, Job{ID: 1, Input: 5}, time.Hour)

			if !errors.Is(err, context.Canceled) {
				t.Errorf("err = %v, want Canceled", err)
			}
		})
	})
}

func BenchmarkRunPool(b *testing.B) {
	jobs := makeJobs(100)
	ctx := context.Background()

	for b.Loop() {
		RunPool(ctx, 8, jobs, 0)
	}
}
