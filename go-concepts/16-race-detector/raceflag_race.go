//go:build race

package main

// raceDetectorEnabled reports whether this binary was built with -race.
//
// The same pattern as 09-sync-primitives/raceflag_race.go, and for the same
// reason: this lesson demonstrates data races on purpose, and the detector is
// right to fail the package for them. Rather than delete the examples or drop
// -race from CI, the tests that exercise a deliberate race skip themselves
// when the detector is watching.
//
// The standard library does exactly this in internal/race.
const raceDetectorEnabled = true
