//go:build race

package main

// raceDetectorEnabled reports whether this binary was built with -race.
//
// Go defines a `race` build tag when the race detector is on, and this pair of
// files is the standard way to branch on it. The standard library does exactly
// this in internal/race.
//
// This repo needs it because several lessons demonstrate data races ON PURPOSE.
// Under `go test -race` those demonstrations are correctly reported and fail
// the package, which would mean either dropping the examples or dropping the
// race job from CI. Neither is acceptable, so the tests that exercise a
// deliberate race skip themselves when the detector is watching.
const raceDetectorEnabled = true
