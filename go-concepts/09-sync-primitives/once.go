package main

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
)

// sync.Once
// =========
//
//	var once sync.Once
//	once.Do(func() { conn = connect() })
//
// Exactly one call runs the function. Every other call BLOCKS until that one
// finishes, then returns. The second half is the part people miss: Do is not
// "skip if already running", it is "wait until done", which is what makes it
// safe for initialising something others will immediately use.
//
// Go 1.21 added OnceValue, OnceValues and OnceFunc, which are almost always
// what you actually wanted.

// expensiveResource stands in for a database handle, a parsed config, or a
// compiled template: costly to build, needed by many goroutines.
type expensiveResource struct {
	ID int
}

// onceInitialiser shows the classic shape, and counts how many times the
// initialiser actually ran.
type onceInitialiser struct {
	once     sync.Once
	runs     atomic.Int64
	resource *expensiveResource
}

// Get returns the resource, building it on first use.
func (o *onceInitialiser) Get() *expensiveResource {
	o.once.Do(func() {
		o.runs.Add(1)
		o.resource = &expensiveResource{ID: 42}
	})
	return o.resource
}

// onceBlocksUntilTheFirstCallFinishes proves the waiting behaviour. Every
// goroutine that arrives during initialisation is released only when the
// initialiser returns, so none of them can observe a half-built resource.
func onceBlocksUntilTheFirstCallFinishes(goroutines int, initialise func()) (runs int64, allSawResult bool) {
	var (
		once     sync.Once
		runCount atomic.Int64
		ready    atomic.Int64
		wg       sync.WaitGroup
	)

	for i := 0; i < goroutines; i++ {
		wg.Go(func() {
			once.Do(func() {
				runCount.Add(1)
				initialise()
				ready.Store(1) // set LAST, inside the Do
			})
			// Every goroutine reaching here must see ready == 1, because Do
			// did not return until the initialiser had finished.
			if ready.Load() == 1 {
				return
			}
			ready.Store(-1) // a goroutine escaped early: the guarantee is broken
		})
	}
	wg.Wait()

	return runCount.Load(), ready.Load() == 1
}

// aPanicStillCountsAsDone is the sharp edge. If the function passed to Do
// panics, Once marks itself complete anyway and will NEVER run it again. Every
// later Do returns immediately, leaving whatever the function was supposed to
// set up unset.
//
// For fallible initialisation this is exactly wrong, and it is why the
// retryable version below exists.
func aPanicStillCountsAsDone() (firstPanicked bool, secondRan bool) {
	var (
		once sync.Once
		runs int
	)

	func() {
		defer func() {
			if r := recover(); r != nil {
				firstPanicked = true
			}
		}()
		once.Do(func() {
			runs++
			panic("initialisation failed")
		})
	}()

	// The Once is already "done". This body never executes.
	once.Do(func() {
		runs++
		secondRan = true
	})

	return firstPanicked, secondRan
}

// onceValueCachesAResult is the Go 1.21 replacement for the manual pattern. It
// runs f once and returns the same value to every caller, with no mutable
// package-level variable to guard.
func onceValueCachesAResult() (values []int, computations *atomic.Int64) {
	var runs atomic.Int64

	get := sync.OnceValue(func() int {
		runs.Add(1)
		return 99
	})

	for i := 0; i < 5; i++ {
		values = append(values, get())
	}

	return values, &runs
}

// onceValuesForFallibleInit handles the common (value, error) shape. Note the
// behaviour: the error is cached too, so a failed initialisation stays failed
// forever. That is right for a missing config file and wrong for a transient
// network problem.
func onceValuesForFallibleInit(fail bool) (results []string, errs []error, runs int64) {
	var count atomic.Int64

	connect := sync.OnceValues(func() (string, error) {
		count.Add(1)
		if fail {
			return "", errors.New("connection refused")
		}
		return "connected", nil
	})

	for i := 0; i < 3; i++ {
		v, err := connect()
		results = append(results, v)
		errs = append(errs, err)
	}

	return results, errs, count.Load()
}

// retryableOnce is what you want when initialisation can fail transiently: on
// error, the next caller tries again.
//
// sync.Once cannot do this by design, so this uses a mutex and a flag. The
// double-check inside the lock is the same pattern as Cache.GetOrCompute.
type retryableOnce struct {
	mu       sync.Mutex
	done     bool
	value    string
	attempts atomic.Int64
}

// Do runs init until it succeeds once, then caches the result.
func (r *retryableOnce) Do(init func() (string, error)) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.done {
		return r.value, nil
	}

	r.attempts.Add(1)
	v, err := init()
	if err != nil {
		return "", err // not marked done: the next caller retries
	}

	r.value = v
	r.done = true
	return v, nil
}

// demoOnce prints Once behaviour and its replacements.
func demoOnce() {
	init := &onceInitialiser{}
	var wg sync.WaitGroup
	ids := make([]int, 100)
	for i := 0; i < 100; i++ {
		wg.Go(func() { ids[i] = init.Get().ID })
	}
	wg.Wait()

	same := true
	for _, id := range ids {
		if id != 42 {
			same = false
		}
	}
	fmt.Printf("  100 goroutines calling Get(): initialiser ran %d time(s), all got 42: %t\n",
		init.runs.Load(), same)

	runs, allSaw := onceBlocksUntilTheFirstCallFinishes(50, func() {})
	fmt.Printf("  Do blocks callers until the first finishes: ran %d time(s), all saw the result: %t\n",
		runs, allSaw)

	panicked, secondRan := aPanicStillCountsAsDone()
	fmt.Printf("\n  a panicking initialiser: first panicked=%t, second call ran=%t\n", panicked, secondRan)
	fmt.Println("  ...Once is marked done even on panic. It will never run again.")

	values, computations := onceValueCachesAResult()
	fmt.Printf("\n  sync.OnceValue, 5 calls: %v, computed %d time(s)\n", values, computations.Load())

	results, errs, runsOK := onceValuesForFallibleInit(false)
	fmt.Printf("  sync.OnceValues, success: %v errs=%v ran %d time(s)\n", results, errs, runsOK)

	_, errsFail, runsFail := onceValuesForFallibleInit(true)
	fmt.Printf("  sync.OnceValues, failure: errs=%v ran %d time(s)  <- the error is cached too\n",
		errsFail[0], runsFail)

	var retry retryableOnce
	attempt := 0
	for i := 0; i < 3; i++ {
		v, err := retry.Do(func() (string, error) {
			attempt++
			if attempt < 3 {
				return "", errors.New("transient failure")
			}
			return "connected", nil
		})
		fmt.Printf("  retryableOnce call %d: %q err=%v\n", i+1, v, err)
	}
	fmt.Printf("  retryableOnce made %d attempts before succeeding\n", retry.attempts.Load())
}
