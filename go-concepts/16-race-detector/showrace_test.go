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
// It sits behind a build tag so it never runs in CI or in a normal `go test`.
// Without the tag this file is not compiled at all, which is lesson 14's point
// about tagged code not being type checked: `make check` builds it via the
// -tags step in the workflow, so it cannot quietly rot.
//
// What you should see:
//
//	WARNING: DATA RACE
//	Write at 0x... by goroutine N:
//	  main.TestShowMeARealRaceReport.func1()
//	Previous write at 0x... by goroutine M:
//	  main.TestShowMeARealRaceReport.func1()
//	Goroutine N (running) created at:
//	  main.TestShowMeARealRaceReport()
//
// Read the "created at" section first: that is where your go statement is.
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
