package main

import (
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

// Concurrency is not parallelism
// ==============================
//
// Concurrency is a structure: several independent things in flight, interleaved.
// Parallelism is an execution property: several things running at the same
// instant, on different cores.
//
// A concurrent program with GOMAXPROCS=1 has zero parallelism and is still
// concurrent. The same program on 12 cores is both. Go gives you the structure
// and the runtime decides how much parallelism to apply, which is why the same
// code scales without being rewritten.
//
// Python's threading gives concurrency without parallelism for CPU work,
// permanently, because of the GIL. That is the difference that matters here.

// cpuBound is deliberately expensive and calls nothing, so it also exercises
// asynchronous preemption: before Go 1.14 a loop like this could not be
// interrupted and would starve its P.
func cpuBound(iterations int) uint64 {
	var sum uint64
	for i := 0; i < iterations; i++ {
		// Enough arithmetic that the compiler cannot delete the loop.
		sum = sum*31 + uint64(i%7)
	}
	return sum
}

// sequential runs the work one piece at a time. This is the baseline.
func sequential(chunks, iterations int) time.Duration {
	start := time.Now()
	for i := 0; i < chunks; i++ {
		_ = cpuBound(iterations)
	}
	return time.Since(start)
}

// parallel runs the same total work across goroutines. On a multi-core machine
// with GOMAXPROCS > 1 this is genuinely faster, which is the thing Python's
// threading cannot do.
func parallel(chunks, iterations int) time.Duration {
	var wg sync.WaitGroup

	start := time.Now()
	for i := 0; i < chunks; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = cpuBound(iterations)
		}()
	}
	wg.Wait()

	return time.Since(start)
}

// withGOMAXPROCS runs fn with GOMAXPROCS temporarily set to n, restoring it
// afterwards. Changing it at runtime is legal and occasionally useful for
// benchmarking; in production it is set once at startup or left alone.
func withGOMAXPROCS(n int, fn func() time.Duration) time.Duration {
	previous := runtime.GOMAXPROCS(n)
	defer runtime.GOMAXPROCS(previous)

	return fn()
}

// schedulerFacts reports what the runtime is actually configured with. Since
// Go 1.25 GOMAXPROCS is container-aware: it reads the cgroup CPU limit rather
// than the host's core count, so a pod limited to 2 CPUs on a 64-core node gets
// 2 Ps instead of 64.
//
// Before that, the standard fix was uber-go/automaxprocs, and a service that
// did not use it would create 64 Ps, thrash, and get throttled by the cgroup.
func schedulerFacts() (numCPU, gomaxprocs, numGoroutine int, version string) {
	return runtime.NumCPU(),
		runtime.GOMAXPROCS(0), // 0 queries without changing
		runtime.NumGoroutine(),
		runtime.Version()
}

// blockingSyscallDoesNotStopOthers demonstrates the handoff. When a goroutine
// blocks in a syscall its M blocks too, but the P detaches and picks up another
// M, so the other goroutines in that queue keep running.
//
// Here time.Sleep stands in for a blocking read. The counter keeps climbing
// while the sleepers are parked, which would be impossible if a blocked
// goroutine held its P.
func blockingSyscallDoesNotStopOthers(sleepers int, d time.Duration) int64 {
	var (
		wg      sync.WaitGroup
		counter atomic.Int64
		stop    = make(chan struct{})
	)

	// Goroutines that block.
	for i := 0; i < sleepers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			time.Sleep(d)
		}()
	}

	// One that works.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				counter.Add(1)
			}
		}
	}()

	time.Sleep(d + 10*time.Millisecond)
	close(stop)
	wg.Wait()

	return counter.Load()
}

// preemptionFacts documents the history, because old advice still circulates.
//
// Before Go 1.14 the scheduler was cooperative: it could only switch goroutines
// at a function call, so a tight loop with no calls occupied its P indefinitely
// and could stall a garbage collection across the whole program. The advice was
// to call runtime.Gosched() inside such loops.
//
// Go 1.14 added asynchronous preemption using OS signals, so a goroutine is
// interrupted after roughly 10ms whatever it is doing. cpuBound above is
// exactly the shape that used to hang, and does not.
func preemptionFacts() []string {
	return []string{
		"pre-1.14: cooperative, switchable only at function calls",
		"1.14+:    asynchronous, signal-based, ~10ms time slice",
		"so runtime.Gosched() in a tight loop is no longer needed",
	}
}

// demoScheduling prints scheduler configuration and a parallel speedup.
func demoScheduling() {
	numCPU, maxprocs, goroutines, version := schedulerFacts()
	fmt.Printf("  %s: NumCPU=%d GOMAXPROCS=%d live goroutines=%d\n",
		version, numCPU, maxprocs, goroutines)

	const (
		chunks     = 12
		iterations = 8_000_000
	)

	one := withGOMAXPROCS(1, func() time.Duration { return parallel(chunks, iterations) })
	all := withGOMAXPROCS(numCPU, func() time.Duration { return parallel(chunks, iterations) })
	seq := sequential(chunks, iterations)

	fmt.Printf("\n  %d chunks of CPU work:\n", chunks)
	fmt.Printf("    sequential:                  %v\n", seq.Round(time.Millisecond))
	fmt.Printf("    concurrent, GOMAXPROCS=1:    %v   (concurrent, not parallel)\n", one.Round(time.Millisecond))
	fmt.Printf("    concurrent, GOMAXPROCS=%-2d:   %v   (%.1fx faster than sequential)\n",
		numCPU, all.Round(time.Millisecond), float64(seq)/float64(all))

	progressed := blockingSyscallDoesNotStopOthers(8, 50*time.Millisecond)
	fmt.Printf("\n  while 8 goroutines were blocked, a 9th completed %d iterations\n", progressed)

	fmt.Println("\n  preemption:")
	for _, f := range preemptionFacts() {
		fmt.Printf("    %s\n", f)
	}
}
