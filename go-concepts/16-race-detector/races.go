// Package main is lesson 16 of go-concepts: the race detector.
//
//	go test -race ./...
//
// Instruments every memory access and reports two goroutines touching the same
// address with no synchronisation between them, with a stack trace for each.
//
// A data race is FOUR conditions, all required:
//
//	two goroutines
//	the same memory
//	at least one is a write
//	nothing orders them
//
// It is undefined behaviour, not "occasionally the wrong number". The compiler
// optimises assuming races do not happen, so a racy program can produce values
// no interleaving of the source explains.
package main

import (
	"sync"
)

// Shape 1: an unsynchronised counter
// ----------------------------------
//
// counter++ is three operations: read, add, write. Two goroutines can read the
// same value, both add one, and both write the same result, losing an update.

// racyCounter has no synchronisation at all.
type racyCounter struct {
	value int
}

func (c *racyCounter) Inc() { c.value++ }

func (c *racyCounter) Value() int { return c.value }

// countWithRacyCounter runs n concurrent increments and returns the result,
// which is usually less than n.
func countWithRacyCounter(n int) int {
	c := &racyCounter{}

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Go(c.Inc)
	}
	wg.Wait()

	return c.Value()
}

// Shape 2: a concurrent map
// -------------------------
//
// This one is worse than a race: the runtime detects it and calls
// `fatal error: concurrent map writes`, which recover() cannot catch and no
// deferred call survives. That is deliberate, because a map caught mid-resize
// has a corrupt internal structure and running any more code over it would
// make things worse.

// racyMapWrite would produce `fatal error: concurrent map writes`. It is NOT
// called anywhere, because it would end the process rather than fail a test.
//
// The code is here to be read. Uncomment the call in the test to watch it
// happen, on a branch you do not intend to merge.
func racyMapWriteDescription() []string {
	return []string{
		"m := map[int]int{}",
		"for i := 0; i < 100; i++ { go func() { m[i] = i }() }",
		"-> fatal error: concurrent map writes",
		"NOT a panic: recover cannot catch it and no defer runs",
		"the runtime does this because a map caught mid-resize is already corrupt",
	}
}

// Shape 3: append to a shared slice
// ---------------------------------
//
// The subtle one. append reads the slice header, may allocate, writes elements,
// and writes the header back. Two goroutines appending concurrently race on the
// header, and the usual symptom is not a crash but LOST ELEMENTS: both read
// len 5, both write to index 5, and one value vanishes.

// racyAppend appends to one slice from many goroutines.
func racyAppend(n int) []int {
	var shared []int

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Go(func() {
			shared = append(shared, i) // races on the slice header
		})
	}
	wg.Wait()

	return shared
}

// Shape 4: unsynchronised struct fields
// -------------------------------------
//
// Two goroutines writing DIFFERENT fields of one struct is still a race if the
// fields share a machine word, and is reported as one regardless. The detector
// works at byte granularity but Go's memory model is about variables, and the
// honest rule is simply: a struct accessed from several goroutines needs
// synchronisation, field by field reasoning does not save you.

// racyConfig is mutated and read concurrently.
type racyConfig struct {
	Timeout int
	Retries int
	Name    string
}

// racyStructAccess writes and reads fields with no lock.
func racyStructAccess(iterations int) *racyConfig {
	cfg := &racyConfig{}

	var wg sync.WaitGroup

	wg.Go(func() {
		for i := 0; i < iterations; i++ {
			cfg.Timeout = i
			cfg.Retries = i * 2
			cfg.Name = "writer"
		}
	})

	wg.Go(func() {
		for i := 0; i < iterations; i++ {
			_ = cfg.Timeout
			_ = cfg.Retries
			_ = cfg.Name
		}
	})

	wg.Wait()
	return cfg
}

// Shape 5: capturing a mutable variable
// -------------------------------------
//
// Lesson 06's loop variable is fixed in Go 1.22, so the classic version of this
// is gone. The general shape is not: a closure capturing any variable that the
// enclosing function also mutates races with it.

// racyClosureCapture mutates a captured variable from several goroutines while
// the parent also reads it.
func racyClosureCapture(n int) (finalTotal int) {
	total := 0 // captured by every closure AND read here

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Go(func() {
			total += i // racy: read-modify-write on a shared variable
		})
	}
	wg.Wait()

	return total
}

// raceConditions lists the four requirements, because being able to state them
// is what turns "is this racy?" from a feeling into a check.
func raceConditions() []string {
	return []string{
		"two goroutines: one goroutine cannot race itself",
		"the same memory: distinct slice elements are distinct memory",
		"at least one write: concurrent reads are always safe",
		"nothing orders them: a mutex, channel, WaitGroup, atomic or Once all do",
	}
}

// whyUndefinedBehaviourMatters is the part people underestimate.
func whyUndefinedBehaviourMatters() []string {
	return []string{
		"a race is UNDEFINED BEHAVIOUR, not \"sometimes the wrong number\"",
		"the compiler optimises assuming races do not happen",
		"so a racy program can produce a value no interleaving of the source explains",
		"a torn read of a multi-word value (a string, a slice, an interface) can be nonsense",
		"\"it has worked for two years\" is not evidence; it is a schedule you have not hit yet",
	}
}
