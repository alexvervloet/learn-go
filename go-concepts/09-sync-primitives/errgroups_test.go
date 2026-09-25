package main

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestGroupAllSucceed(t *testing.T) {
	g, ctx := WithContext(context.Background())

	var count atomic.Int64
	for i := 0; i < 10; i++ {
		g.Go(func() error {
			count.Add(1)
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		t.Fatalf("Wait = %v, want nil", err)
	}
	if got := count.Load(); got != 10 {
		t.Errorf("ran %d goroutines, want 10", got)
	}

	// Wait must cancel the derived context even on success, or it leaks.
	select {
	case <-ctx.Done():
	default:
		t.Error("Wait should cancel the derived context")
	}
}

func TestGroupReturnsTheFirstError(t *testing.T) {
	g, _ := WithContext(context.Background())

	first := errors.New("first failure")

	g.Go(func() error { return first })
	g.Go(func() error {
		time.Sleep(50 * time.Millisecond)
		return errors.New("second failure")
	})

	err := g.Wait()
	if !errors.Is(err, first) {
		t.Errorf("Wait = %v, want the first error %v", err, first)
	}
}

// TestGroupCancelsOnFirstError is the behaviour that separates errgroup from a
// WaitGroup: the siblings learn to stop.
func TestGroupCancelsOnFirstError(t *testing.T) {
	g, ctx := WithContext(context.Background())

	boom := errors.New("boom")
	var cancelled atomic.Int64

	g.Go(func() error {
		time.Sleep(10 * time.Millisecond)
		return boom
	})

	for i := 0; i < 5; i++ {
		g.Go(func() error {
			select {
			case <-ctx.Done():
				cancelled.Add(1)
				return nil
			case <-time.After(5 * time.Second):
				return errors.New("was never cancelled")
			}
		})
	}

	if err := g.Wait(); !errors.Is(err, boom) {
		t.Fatalf("Wait = %v, want %v", err, boom)
	}
	if got := cancelled.Load(); got != 5 {
		t.Errorf("%d of 5 siblings noticed the cancellation, want 5", got)
	}
}

// TestGroupCancelCarriesTheCause: using CancelCauseFunc means a goroutine can
// recover WHY it was cancelled, not just that it was.
func TestGroupCancelCarriesTheCause(t *testing.T) {
	g, ctx := WithContext(context.Background())

	boom := errors.New("the real problem")
	causeSeen := make(chan error, 1)

	g.Go(func() error {
		time.Sleep(10 * time.Millisecond)
		return boom
	})
	g.Go(func() error {
		<-ctx.Done()
		causeSeen <- context.Cause(ctx)
		return nil
	})

	_ = g.Wait()

	select {
	case cause := <-causeSeen:
		if !errors.Is(cause, boom) {
			t.Errorf("context.Cause = %v, want %v", cause, boom)
		}
	case <-time.After(time.Second):
		t.Fatal("the sibling never observed a cause")
	}
}

func TestGroupSetLimit(t *testing.T) {
	g, _ := WithContext(context.Background())
	g.SetLimit(3)

	var (
		running atomic.Int64
		peak    atomic.Int64
	)

	for i := 0; i < 20; i++ {
		g.Go(func() error {
			n := running.Add(1)
			for {
				old := peak.Load()
				if n <= old || peak.CompareAndSwap(old, n) {
					break
				}
			}

			time.Sleep(5 * time.Millisecond)
			running.Add(-1)
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		t.Fatalf("Wait = %v", err)
	}
	if got := peak.Load(); got > 3 {
		t.Errorf("peak concurrency = %d, want at most 3", got)
	}
}

func TestGroupSetLimitPanicsAfterGo(t *testing.T) {
	g, _ := WithContext(context.Background())
	g.Go(func() error { return nil })

	defer func() {
		if r := recover(); r == nil {
			t.Error("SetLimit after Go should panic")
		}
	}()

	g.SetLimit(2)
}

func TestFetchAll(t *testing.T) {
	urls := []string{"a.example", "b.example", "c.example"}

	t.Run("all succeed", func(t *testing.T) {
		results, err := fetchAll(context.Background(), urls, fakeFetch("", time.Millisecond))

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(results) != len(urls) {
			t.Fatalf("got %d results, want %d", len(results), len(urls))
		}
		// Results must be in INPUT order, which indexing a pre-sized slice
		// gives and a channel would not.
		for i, r := range results {
			if r.URL != urls[i] {
				t.Errorf("result %d is %q, want %q — order should match the input", i, r.URL, urls[i])
			}
		}
	})

	t.Run("one fails", func(t *testing.T) {
		_, err := fetchAll(context.Background(), urls, fakeFetch("b.example", time.Millisecond))

		if err == nil {
			t.Fatal("expected an error")
		}
		if !strings.Contains(err.Error(), "b.example") {
			t.Errorf("error %q should name the failing url", err)
		}
	})

	t.Run("the caller's deadline propagates", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		defer cancel()

		_, err := fetchAll(ctx, urls, fakeFetch("", time.Second))

		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("err = %v, want DeadlineExceeded", err)
		}
	})

	t.Run("no urls", func(t *testing.T) {
		results, err := fetchAll(context.Background(), nil, fakeFetch("", 0))

		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if len(results) != 0 {
			t.Errorf("got %d results, want 0", len(results))
		}
	})
}

func TestWaitGroupComparisonIsDocumented(t *testing.T) {
	if got := waitGroupCannotDoThis(); len(got) < 3 {
		t.Errorf("expected at least 3 documented points, got %d", len(got))
	}
}
