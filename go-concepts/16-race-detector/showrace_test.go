//go:build showrace

package main

import (
	"sync"
	"testing"
)

// TestShowMeARealRaceReport exists to FAIL, on purpose, so you can read a real
// report rather than the transcribed one in reading.go.
//
//	go test -race -tags showrace -run TestShowMeARealRaceReport ./16-race-detector
//
// It sits behind a build tag so it never runs in a normal `go test`. Without
// the tag this file is not compiled at all, which is lesson 14's point about
// tagged code not being type checked. CI's build-tags step passes
// `-tags debug,showrace` so the file is compiled and vetted on every run; the
// test then skips, because that step does not use -race.
//
// What you see on Go 1.27, trimmed:
//
//	WARNING: DATA RACE
//	Read at 0x... by goroutine 10:
//	  ...TestShowMeARealRaceReport.func1()
//	      16-race-detector/showrace_test.go:41
//	Previous write at 0x... by goroutine 8:
//	  ...TestShowMeARealRaceReport.func1()
//	      16-race-detector/showrace_test.go:41
//	Goroutine 10 (running) created at:
//	  sync.(*WaitGroup).Go()
//	  testing.tRunner()
//
// Read the two access stacks first: they name your code and the line. The
// "created at" section names the go statement, and with wg.Go that statement
// is inside sync.WaitGroup.Go, so the frame that called wg.Go may not appear at
// all. An earlier version of this comment promised the test function there.
func TestShowMeARealRaceReport(t *testing.T) {
	if !raceDetectorEnabled {
		t.Skip("run with -race to see the report; without it this just loses updates")
	}

	counter := 0

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Go(func() {
			counter++ // the race: read, add, write, from 100 goroutines
		})
	}
	wg.Wait()

	t.Logf("counter = %d of 100; the report above is the point", counter)
}
