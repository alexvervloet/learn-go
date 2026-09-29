package concurrency

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// TestMutexCounterIsSafe is the test that only means something under -race.
//
// Without -race it passes for both implementations, because the increments happen to land correctly
// often enough and the final total is checked rather than the intermediate states. That is the whole
// problem with testing concurrency by looking at results: a data race does not reliably produce a
// wrong answer, it produces undefined behaviour that usually looks fine.
//
//	go test ./concurrency          passes
//	go test -race ./concurrency    passes, and would fail for UnsafeCounter
func TestMutexCounterIsSafe(t *testing.T) {
	const goroutines = 100
	const perGoroutine = 100

	c := &MutexCounter{}

	var wg sync.WaitGroup
	for range goroutines {
		wg.Go(func() {
			for range perGoroutine {
				c.Add(1)
			}
		})
	}
	wg.Wait()

	if got := c.Value(); got != goroutines*perGoroutine {
		t.Errorf("Value = %d, want %d", got, goroutines*perGoroutine)
	}
}

// TestUnsafeCounterLosesIncrements shows why the result-checking approach is not enough on its own,
// and why it is also not nothing.
//
// With enough contention the lost updates become visible without -race. The test asserts the count
// is wrong, which is an odd thing to assert and is the honest way to demonstrate it: if this ever
// starts passing with the right count, the demonstration has stopped working rather than the code
// having become correct.
func TestUnsafeCounterLosesIncrements(t *testing.T) {
	// The race is the point, so under -race this test fails by design. Skipping it is the
	// only option: there is no way to tell the detector to ignore one access.
	if raceDetectorEnabled {
		t.Skip("this test races on purpose, which -race correctly reports")
	}
	if testing.Short() {
		t.Skip("needs enough contention to lose an increment")
	}

	const goroutines = 200
	const perGoroutine = 1000
	const want = goroutines * perGoroutine

	// Several attempts, because losing an increment is probabilistic.
	for attempt := range 5 {
		c := &UnsafeCounter{}

		var wg sync.WaitGroup
		for range goroutines {
			wg.Go(func() {
				for range perGoroutine {
					c.Add(1)
				}
			})
		}
		wg.Wait()

		if got := c.Value(); got != want {
			t.Logf("attempt %d: got %d, want %d, so %d increments were lost",
				attempt, got, want, want-got)
			return
		}
	}

	t.Log("no increments were lost in five attempts, which is possible and is exactly why " +
		"-race exists: the race is real whether or not it produces a wrong answer")
}

// Goroutine leak detection
// ========================

// checkNoLeaks registers a cleanup that fails the test if goroutines were left behind.
//
// This is a hand-rolled uber-go/goleak, and it is fifteen lines. The two details that make it work
// rather than flake:
//
//	it RETRIES, because a goroutine that is finishing needs a moment to be reaped and a
//	  single NumGoroutine reading right after the test is a coin flip
//	it uses runtime.Gosched to give the scheduler a chance, not just time.Sleep
//
// What it cannot do that goleak can: name the leaked goroutines. goleak parses a stack dump to
// report which function leaked, which is worth the dependency on a real service. The count alone is
// enough to fail the build and send someone looking.
func checkNoLeaks(t *testing.T) {
	t.Helper()

	before := runtime.NumGoroutine()

	t.Cleanup(func() {
		// Up to a second of retries. A leak is permanent, so a real one still fails; a
		// goroutine in the process of exiting gets time to do so.
		for range 100 {
			runtime.Gosched()

			if runtime.NumGoroutine() <= before {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}

		after := runtime.NumGoroutine()

		buf := make([]byte, 1<<16)
		n := runtime.Stack(buf, true)

		t.Errorf("goroutines leaked: %d before, %d after\n%s", before, after, buf[:n])
	})
}

// TestLeakyFetchLeaks demonstrates the leak, and asserts it, so the demonstration cannot rot.
//
// The assertion is backwards on purpose: it requires that a goroutine WAS left behind. If
// LeakyFetch is ever fixed, this test fails and says so, which is better than silently becoming a
// test of nothing.
func TestLeakyFetchLeaks(t *testing.T) {
	before := runtime.NumGoroutine()

	// A context that is already done, so LeakyFetch returns immediately and abandons its
	// goroutine mid-send.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := LeakyFetch(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}

	// Give the goroutine time to reach its blocking send.
	for range 50 {
		runtime.Gosched()
		if runtime.NumGoroutine() > before {
			break
		}
		time.Sleep(time.Millisecond)
	}

	after := runtime.NumGoroutine()

	if after <= before {
		t.Errorf("no goroutine leaked (%d before, %d after); LeakyFetch may have been fixed, "+
			"in which case delete this test", before, after)
	}

	t.Logf("%d goroutines before, %d after: one is blocked forever on an unbuffered send",
		before, after)

	// That goroutine is stuck for the life of the process. Nothing here can clean it up,
	// which is the point.
}

// TestFetchDoesNotLeak is the same scenario against the fixed version.
func TestFetchDoesNotLeak(t *testing.T) {
	checkNoLeaks(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := Fetch(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

// TestFetchSucceeds, so the leak fix did not break the happy path. A "does not leak" test that
// passes because the function does nothing is a common and embarrassing outcome.
func TestFetchSucceeds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		got, err := Fetch(context.Background(), time.Second)
		if err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		if got != "done" {
			t.Errorf("Fetch = %q, want %q", got, "done")
		}
	})
}

// TestFetchRespectsTheDeadline, with exact timing, which is what synctest is for. On a real clock
// this test would either sleep for a second or assert a tolerance.
func TestFetchRespectsTheDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		start := time.Now()

		_, err := Fetch(ctx, time.Hour)

		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("err = %v, want DeadlineExceeded", err)
		}
		if elapsed := time.Since(start); elapsed != 100*time.Millisecond {
			t.Errorf("returned after %v, want exactly 100ms", elapsed)
		}
	})
}

// Worker pool
// ===========

func TestPool(t *testing.T) {
	checkNoLeaks(t)

	p := NewPool(4)

	// The drain starts BEFORE the submits. The first version of this test submitted all
	// twenty jobs and then started draining, and it deadlocked: the results channel holds
	// four, the workers block sending once it is full, so nothing reads from jobs and Submit
	// blocks forever. See TestPoolDeadlocksIfResultsAreNotDrained.
	var failures, successes int
	done := make(chan struct{})

	go func() {
		defer close(done)

		for err := range p.Results() {
			if err != nil {
				failures++
				continue
			}
			successes++
		}
	}()

	const jobs = 20
	for i := range jobs {
		if err := p.Submit(context.Background(), func() error {
			if i%5 == 0 {
				return errors.New("job failed")
			}
			return nil
		}); err != nil {
			t.Fatalf("Submit(%d): %v", i, err)
		}
	}

	p.Close()
	<-done

	if failures != 4 {
		t.Errorf("failures = %d, want 4", failures)
	}
	if successes != jobs-4 {
		t.Errorf("successes = %d, want %d", successes, jobs-4)
	}
}

// TestPoolDeadlocksIfResultsAreNotDrained pins down the trap rather than leaving it to be
// rediscovered.
//
// Submitting more jobs than the results buffer holds, without draining, blocks forever. The test
// asserts that it blocks, using a context to escape, so the day the design changes this test fails
// and says the contract moved.
func TestPoolDeadlocksIfResultsAreNotDrained(t *testing.T) {
	const workers = 2

	p := NewPool(workers) // results holds `workers` too

	// How many submits succeed before the pool wedges, and getting this wrong was the first
	// version of this test: `workers` for the buffer slots, plus `workers` more that the
	// workers pick up and then block trying to deliver. So 2n, not n.
	for i := range 2 * workers {
		if err := p.Submit(context.Background(), func() error { return nil }); err != nil {
			t.Fatalf("Submit(%d): %v", i, err)
		}
	}

	// Every worker is now blocked sending into a full results channel, so none is reading
	// jobs. The next Submit cannot proceed.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := p.Submit(ctx, func() error { return nil })

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Submit returned %v; expected it to block, because the results channel "+
			"is full and no worker is reading jobs", err)
	}

	// Drain so the deferred Close below can finish, and so the workers are not leaked.
	go func() {
		//nolint:revive // draining
		for range p.Results() {
		}
	}()
	p.Close()
}

// TestCollectDoesNotDeadlock is the same workload through the API that starts the drain for you.
func TestCollectDoesNotDeadlock(t *testing.T) {
	checkNoLeaks(t)

	jobs := make([]func() error, 50)
	for i := range jobs {
		jobs[i] = func() error {
			if i%5 == 0 {
				return errors.New("job failed")
			}
			return nil
		}
	}

	results, err := Collect(context.Background(), 4, jobs)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	if len(results) != len(jobs) {
		t.Fatalf("got %d results, want %d", len(results), len(jobs))
	}

	failures := 0
	for _, err := range results {
		if err != nil {
			failures++
		}
	}
	if failures != 10 {
		t.Errorf("failures = %d, want 10", failures)
	}
}

// TestCollectDegenerate and with a silly worker count, because both are things a caller passes.
func TestCollectDegenerate(t *testing.T) {
	checkNoLeaks(t)

	results, err := Collect(context.Background(), 4, nil)
	if err != nil {
		t.Fatalf("Collect with no jobs: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("got %d results from no jobs", len(results))
	}

	// Zero workers would mean nothing ever runs, so it is clamped to one.
	results, err = Collect(context.Background(), 0, []func() error{
		func() error { return nil },
	})
	if err != nil {
		t.Fatalf("Collect with zero workers: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("got %d results, want 1", len(results))
	}
}

// TestPoolCloseIsIdempotent, because Close in a defer plus Close on the happy path is the normal
// shape and a second close of a channel panics.
func TestPoolCloseIsIdempotent(t *testing.T) {
	checkNoLeaks(t)

	p := NewPool(2)

	go func() {
		//nolint:revive // draining so Close does not block
		for range p.Results() {
		}
	}()

	p.Close()
	p.Close() // must not panic
	p.Close()
}

// TestSubmitAfterCloseIsAnError rather than a panic, which is what the select on p.closed buys. A
// send on a closed channel panics in a goroutine the caller does not own, and that is the worst
// kind of failure to diagnose.
func TestSubmitAfterCloseIsAnError(t *testing.T) {
	checkNoLeaks(t)

	p := NewPool(1)

	go func() {
		//nolint:revive // draining
		for range p.Results() {
		}
	}()

	p.Close()

	err := p.Submit(context.Background(), func() error { return nil })

	if !errors.Is(err, ErrPoolClosed) {
		t.Errorf("err = %v, want ErrPoolClosed", err)
	}
}

// TestSubmitRespectsContext: a full pool with no workers free blocks, and the context is the way
// out.
func TestSubmitRespectsContext(t *testing.T) {
	checkNoLeaks(t)

	p := NewPool(1)
	defer p.Close()

	blocked := make(chan struct{})
	release := make(chan struct{})

	// Occupy the only worker.
	if err := p.Submit(context.Background(), func() error {
		close(blocked)
		<-release
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	<-blocked

	// The worker is busy and jobs is unbuffered, so this Submit blocks until the context
	// gives up.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := p.Submit(ctx, func() error { return nil })

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want DeadlineExceeded", err)
	}

	close(release)

	go func() {
		//nolint:revive // draining so the deferred Close does not block
		for range p.Results() {
		}
	}()
}

// Rate limiting with a fake clock
// ==============================

// TestLimiterWithSynctest is what the fake clock makes possible: a token-bucket test with exact
// assertions and no sleeping.
//
// On a real clock this test would either sleep for whole intervals or use millisecond intervals and
// assert a tolerance. Both are slow, flaky, or both.
func TestLimiterWithSynctest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// 3 per second.
		l := NewLimiter(3, time.Second)

		// The bucket starts full.
		for i := range 3 {
			if !l.Allow() {
				t.Fatalf("Allow %d was refused from a full bucket", i)
			}
		}
		if l.Allow() {
			t.Error("a fourth Allow succeeded from an empty bucket")
		}

		// Nothing refills before the interval is up, asserted at the boundary.
		time.Sleep(999 * time.Millisecond)
		if l.Allow() {
			t.Error("the bucket refilled 1ms early")
		}

		// And exactly at the interval it is full again.
		time.Sleep(time.Millisecond)
		if got := l.Tokens(); got != 3 {
			t.Errorf("Tokens = %d, want 3 after one interval", got)
		}
	})
}

// TestLimiterWaitBlocksUntilATokenIsFree, with the wait measured exactly.
func TestLimiterWaitBlocksUntilATokenIsFree(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := NewLimiter(1, time.Second)

		// Take the only token.
		if !l.Allow() {
			t.Fatal("the first Allow was refused")
		}

		start := time.Now()

		if err := l.Wait(context.Background()); err != nil {
			t.Fatalf("Wait: %v", err)
		}

		elapsed := time.Since(start)

		// Wait polls every interval/10, so it notices the refill at the first poll at or
		// after 1s: that is the tenth poll, at exactly 1s.
		if elapsed != time.Second {
			t.Errorf("waited %v, want exactly 1s", elapsed)
		}
	})
}

// TestLimiterWaitRespectsContext, and the deadline lands between polls, which is the interleaving a
// real clock would make a race.
func TestLimiterWaitRespectsContext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := NewLimiter(1, time.Second)

		if !l.Allow() {
			t.Fatal("the first Allow was refused")
		}

		ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
		defer cancel()

		start := time.Now()

		err := l.Wait(ctx)

		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("err = %v, want DeadlineExceeded", err)
		}
		if elapsed := time.Since(start); elapsed != 250*time.Millisecond {
			t.Errorf("gave up after %v, want exactly 250ms", elapsed)
		}
	})
}

// TestLimiterUnderConcurrency is the part synctest does NOT replace: the fake clock makes timing
// deterministic and says nothing about data races. This needs -race.
func TestLimiterUnderConcurrency(t *testing.T) {
	l := NewLimiter(100, time.Hour) // one hour, so no refill during the test

	var (
		mu      sync.Mutex
		allowed int
	)

	var wg sync.WaitGroup
	for range 500 {
		wg.Go(func() {
			if l.Allow() {
				mu.Lock()
				allowed++
				mu.Unlock()
			}
		})
	}
	wg.Wait()

	// Exactly the capacity, no more and no fewer. A racy refill or a racy decrement shows up
	// here as a number that is not 100, and under -race as a reported race.
	if allowed != 100 {
		t.Errorf("allowed %d of 500, want exactly 100", allowed)
	}
}

// TestSynctestDoesNotWaitForRealWork is the limitation worth knowing. The fake clock advances when
// every goroutine in the bubble is "durably blocked", and a goroutine spinning on the CPU is not
// blocked at all, so time does not move and the test hangs.
//
// The rule: everything inside a bubble must block on a channel, a mutex, or the time package.
// Anything touching the network or an external process breaks it, which is why the synctest docs
// list that first.
func TestSynctestDoesNotWaitForRealWork(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// This works: the sleep is a durable block, so the clock jumps.
		start := time.Now()
		time.Sleep(time.Hour)

		if elapsed := time.Since(start); elapsed != time.Hour {
			t.Errorf("elapsed = %v, want exactly 1h", elapsed)
		}

		// A busy loop would NOT advance the clock, because the goroutine is runnable
		// rather than blocked. Not demonstrated here, because demonstrating it means
		// hanging the test.
		t.Log("the clock jumped an hour instantly. A CPU-bound loop in a bubble does not " +
			"advance it, because the goroutine is runnable rather than durably blocked.")
	})
}
