package main

import (
	"errors"
	"sync"
	"testing"
)

func TestOnceRunsExactlyOnce(t *testing.T) {
	init := &onceInitialiser{}

	var wg sync.WaitGroup
	results := make([]*expensiveResource, 500)
	for i := 0; i < 500; i++ {
		wg.Go(func() { results[i] = init.Get() })
	}
	wg.Wait()

	if got := init.runs.Load(); got != 1 {
		t.Errorf("initialiser ran %d times, want exactly 1", got)
	}

	// Every goroutine must get the SAME pointer, not just an equal value.
	first := results[0]
	for i, r := range results {
		if r != first {
			t.Fatalf("goroutine %d got a different resource pointer", i)
		}
	}
}

// TestOnceBlocksUntilTheFirstCallFinishes is the half people miss: Do is
// "wait until done", not "skip if running".
func TestOnceBlocksUntilTheFirstCallFinishes(t *testing.T) {
	runs, allSaw := onceBlocksUntilTheFirstCallFinishes(200, func() {})

	if runs != 1 {
		t.Errorf("ran %d times, want 1", runs)
	}
	if !allSaw {
		t.Error("a goroutine returned from Do before the initialiser had finished")
	}
}

// TestAPanicStillCountsAsDone is the sharp edge that makes Once wrong for
// fallible initialisation.
func TestAPanicStillCountsAsDone(t *testing.T) {
	firstPanicked, secondRan := aPanicStillCountsAsDone()

	if !firstPanicked {
		t.Error("the first Do should have propagated the panic")
	}
	if secondRan {
		t.Error("the second Do should NOT have run — Once is done even after a panic")
	}
}

func TestOnceValueCachesAResult(t *testing.T) {
	values, computations := onceValueCachesAResult()

	if len(values) != 5 {
		t.Fatalf("got %d values, want 5", len(values))
	}
	for i, v := range values {
		if v != 99 {
			t.Errorf("value %d = %d, want 99", i, v)
		}
	}
	if got := computations.Load(); got != 1 {
		t.Errorf("computed %d times, want 1", got)
	}
}

func TestOnceValuesCachesErrorsToo(t *testing.T) {
	t.Run("success is cached", func(t *testing.T) {
		results, errs, runs := onceValuesForFallibleInit(false)

		if runs != 1 {
			t.Errorf("ran %d times, want 1", runs)
		}
		for i, err := range errs {
			if err != nil {
				t.Errorf("call %d: unexpected error %v", i, err)
			}
			if results[i] != "connected" {
				t.Errorf("call %d: got %q, want \"connected\"", i, results[i])
			}
		}
	})

	// This is the behaviour to know about: a failed initialisation stays
	// failed forever, which is right for a missing file and wrong for a
	// transient network problem.
	t.Run("failure is cached too", func(t *testing.T) {
		_, errs, runs := onceValuesForFallibleInit(true)

		if runs != 1 {
			t.Errorf("ran %d times, want 1 — the error is cached, not retried", runs)
		}
		for i, err := range errs {
			if err == nil {
				t.Errorf("call %d: expected the cached error", i)
			}
		}
	})
}

func TestRetryableOnce(t *testing.T) {
	var r retryableOnce

	attempt := 0
	init := func() (string, error) {
		attempt++
		if attempt < 3 {
			return "", errors.New("transient")
		}
		return "connected", nil
	}

	for i := 0; i < 2; i++ {
		if _, err := r.Do(init); err == nil {
			t.Fatalf("call %d should have failed", i+1)
		}
	}

	v, err := r.Do(init)
	if err != nil {
		t.Fatalf("third call: %v", err)
	}
	if v != "connected" {
		t.Errorf("got %q, want \"connected\"", v)
	}

	// Once it has succeeded, it stops calling init.
	before := r.attempts.Load()
	for i := 0; i < 5; i++ {
		if _, err := r.Do(init); err != nil {
			t.Errorf("cached call returned %v", err)
		}
	}
	if after := r.attempts.Load(); after != before {
		t.Errorf("made %d more attempts after success, want 0", after-before)
	}
}

// TestRetryableOnceUnderConcurrency: several goroutines racing must still
// produce exactly one successful initialisation.
func TestRetryableOnceUnderConcurrency(t *testing.T) {
	var r retryableOnce

	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Go(func() {
			v, err := r.Do(func() (string, error) { return "value", nil })
			if err != nil || v != "value" {
				t.Errorf("got %q, %v", v, err)
			}
		})
	}
	wg.Wait()

	if got := r.attempts.Load(); got != 1 {
		t.Errorf("init ran %d times, want 1", got)
	}
}
