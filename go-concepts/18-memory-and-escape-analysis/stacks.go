package main

import (
	"fmt"
	"runtime"
	"time"
)

// Stack growth
// ============
//
// A goroutine stack starts at 8KB and grows by COPYING: the runtime allocates
// a larger stack, copies every frame, and rewrites the pointers into them. The
// default maximum is 1GB on 64-bit.
//
// That copy is why Go can afford tiny initial stacks and therefore hundreds of
// thousands of goroutines, and it is why lesson 05's recursive parser handled a
// million levels of nesting where Python raises at 1000 and C segfaults at 8MB.
//
// It is also why a Go stack cannot hold a pointer from C: the addresses move.

// deepRecursion recurses to the given depth, doing enough per frame that the
// frames are not optimised away.
func deepRecursion(depth int) int {
	if depth <= 0 {
		return 0
	}

	// A local per frame, so the stack genuinely grows.
	var padding [16]byte
	padding[0] = byte(depth)

	return int(padding[0]) + deepRecursion(depth-1)
}

// measureStackGrowth recurses and reports the goroutine's stack size before
// and after, read from runtime.MemStats.
//
// StackInuse is process-wide rather than per-goroutine, so this runs the
// recursion in a dedicated goroutine and measures around it. It is indicative
// rather than exact, which is the honest description of every stack
// measurement available from Go.
func measureStackGrowth(depth int) (beforeKB, duringKB uint64) {
	var before, during runtime.MemStats

	runtime.ReadMemStats(&before)

	peak := make(chan uint64, 1)
	done := make(chan struct{})

	go func() {
		defer close(done)

		// Recurse, then sample from the deepest frame.
		var sample func(int) int
		sample = func(d int) int {
			if d <= 0 {
				var m runtime.MemStats
				runtime.ReadMemStats(&m)
				peak <- m.StackInuse
				return 0
			}
			var padding [16]byte
			padding[0] = byte(d)
			return int(padding[0]) + sample(d-1)
		}
		_ = sample(depth)
	}()

	<-done
	during.StackInuse = <-peak

	return before.StackInuse / 1024, during.StackInuse / 1024
}

// stackFacts are the numbers worth knowing.
func stackFacts() map[string]string {
	return map[string]string{
		"initial size":  "8KB per goroutine (2KB before Go 1.19 raised it)",
		"growth":        "by copying: allocate bigger, copy the frames, rewrite the pointers",
		"maximum":       "1GB on 64-bit, 250MB on 32-bit; SetMaxStack changes it",
		"exceeding it":  "fatal error: stack overflow, which recover cannot catch",
		"shrinking":     "the GC shrinks a stack that is using less than a quarter of it",
		"why it copies": "so a goroutine can start tiny; the cost is that addresses move",
	}
}

// whyStacksMatter connects it to the rest.
func whyStacksMatter() []string {
	return []string{
		"tiny initial stacks are what make 100,000 goroutines affordable (lesson 06)",
		"growth by copying is why deep recursion works where C segfaults (lesson 05)",
		"and why a Go pointer cannot be handed to C and kept: the address moves",
		"a value on the stack is free: no allocation, no collector, good cache locality",
		"so escape analysis is the difference between free and not, per value",
	}
}

// goroutineStackCost measures what a goroutine actually costs in memory, which
// is the number behind lesson 06's claim.
func goroutineStackCost(n int) (perGoroutineBytes uint64) {
	var before, after runtime.MemStats

	runtime.GC()
	runtime.ReadMemStats(&before)

	start := make(chan struct{})
	done := make(chan struct{})

	for i := 0; i < n; i++ {
		go func() {
			<-start // park, so they all exist at once
			done <- struct{}{}
		}()
	}

	// Let them all park.
	time.Sleep(50 * time.Millisecond)
	runtime.ReadMemStats(&after)

	close(start)
	for i := 0; i < n; i++ {
		<-done
	}

	if after.StackInuse <= before.StackInuse {
		return 0
	}
	return (after.StackInuse - before.StackInuse) / uint64(n)
}

// demoStacks prints stack behaviour.
func demoStacks() {
	fmt.Println("  the facts:")
	for _, k := range []string{
		"initial size", "growth", "maximum", "exceeding it", "shrinking", "why it copies",
	} {
		fmt.Printf("    %-14s %s\n", k, stackFacts()[k])
	}

	fmt.Printf("\n  deep recursion:\n")
	for _, depth := range []int{100, 10_000, 1_000_000} {
		start := time.Now()
		result := deepRecursion(depth)
		fmt.Printf("    %9d frames -> %d, in %v\n", depth, result, time.Since(start).Round(time.Microsecond))
	}
	fmt.Println("    ...Python raises RecursionError at 1000; C segfaults on a fixed 8MB stack")

	beforeKB, duringKB := measureStackGrowth(50_000)
	fmt.Printf("\n  process stack in use: %d KB before, %d KB at 50,000 frames deep\n",
		beforeKB, duringKB)

	perGoroutine := goroutineStackCost(10_000)
	fmt.Printf("  10,000 parked goroutines cost ~%d bytes of stack each\n", perGoroutine)

	fmt.Println("\n  why stacks matter:")
	for _, s := range whyStacksMatter() {
		fmt.Printf("    %s\n", s)
	}
}
