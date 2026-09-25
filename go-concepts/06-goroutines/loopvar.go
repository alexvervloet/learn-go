package main

import (
	"fmt"
	"sort"
	"sync"
)

// The loop variable
// =================
//
// Before Go 1.22, `for i := range xs` declared ONE variable reused across every
// iteration. A goroutine closing over it saw whatever value it held when the
// goroutine actually ran, which was usually the final one:
//
//	for i := 0; i < 3; i++ {
//	    go func() { fmt.Println(i) }()   // pre-1.22: often "3 3 3"
//	}
//
// The workaround, `i := i` as the first line of the body, is everywhere in
// older Go code and looks like a mistake to anyone learning the language today.
//
// Go 1.22 made loop variables per-iteration. The bug is gone. The workaround is
// now a no-op, and golangci-lint's copyloopvar flags it as redundant.
//
// The gate is per-module: a module only gets the new semantics if its go.mod
// declares `go 1.22` or later. This module declares go 1.27, so the behaviour
// below is the new one.

// eachIterationHasItsOwnVariable is the modern behaviour, asserted. Every
// goroutine sees its own iteration's value.
func eachIterationHasItsOwnVariable(n int) []int {
	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		out []int
	)

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() { // no `i := i` needed
			defer wg.Done()

			mu.Lock()
			defer mu.Unlock()
			out = append(out, i)
		}()
	}

	wg.Wait()
	sort.Ints(out) // goroutines finish in any order; sort for a stable assertion
	return out
}

// theOldWorkaroundIsNowRedundant is what the same loop looked like before 1.22.
// It still compiles and still works; it just does nothing that the language is
// not already doing.
func theOldWorkaroundIsNowRedundant(n int) []int {
	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		out []int
	)

	for i := 0; i < n; i++ {
		// copyloopvar reports this line as "The copy of the 'for' variable
		// \"i\" can be deleted (Go 1.22+)", which is the claim this function
		// exists to demonstrate, confirmed by the linter rather than asserted
		// by the comment.
		i := i //nolint:copyloopvar // the redundant copy is the demonstration
		wg.Add(1)
		go func() {
			defer wg.Done()

			mu.Lock()
			defer mu.Unlock()
			out = append(out, i)
		}()
	}

	wg.Wait()
	sort.Ints(out)
	return out
}

// simulateOldBehaviour shows what the pre-1.22 bug produced, using a shared
// variable explicitly. This is the shape the old loop desugared to, and the
// reason the output was "3 3 3": one variable, read late by everyone.
func simulateOldBehaviour(n int) []int {
	var (
		mu     sync.Mutex
		wg     sync.WaitGroup
		out    []int
		shared int // ONE variable, as pre-1.22 loops had
		ready  = make(chan struct{})
	)

	for i := 0; i < n; i++ {
		shared = i
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-ready // hold every goroutine until the loop has finished

			mu.Lock()
			defer mu.Unlock()
			out = append(out, shared) // reads the FINAL value
		}()
	}

	close(ready)
	wg.Wait()
	sort.Ints(out)
	return out
}

// rangeOverSliceToo: the change applies to range loops over slices and maps as
// well, for both the index and the value.
func rangeOverSliceToo(items []string) []string {
	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		out []string
	)

	for _, item := range items { // item is per-iteration since 1.22
		wg.Add(1)
		go func() {
			defer wg.Done()

			mu.Lock()
			defer mu.Unlock()
			out = append(out, item)
		}()
	}

	wg.Wait()
	sort.Strings(out)
	return out
}

// demoLoopVar prints the modern behaviour against a simulation of the old bug.
func demoLoopVar() {
	fmt.Printf("  Go 1.22+ per-iteration variable: %v\n", eachIterationHasItsOwnVariable(5))
	fmt.Printf("  with the old `i := i` workaround: %v  (identical, now redundant)\n", theOldWorkaroundIsNowRedundant(5))
	fmt.Printf("  what pre-1.22 produced:          %v  (one shared variable, read late)\n", simulateOldBehaviour(5))
	fmt.Printf("  range over a slice, same rule:   %v\n", rangeOverSliceToo([]string{"go", "rust", "zig"}))
}
