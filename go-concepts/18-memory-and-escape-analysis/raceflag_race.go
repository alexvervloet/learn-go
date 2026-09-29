//go:build race

package main

// raceDetectorEnabled reports whether this binary was built with -race. A race
// build reserves a larger guard area at the bottom of every goroutine stack, so
// the stack measurements in stacks_test.go need to know. See lesson 16 for why
// a pair of build-tagged files is the way to ask.
const raceDetectorEnabled = true
