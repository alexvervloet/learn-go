//go:build race

package concurrency

// raceDetectorEnabled is true when the binary was built with -race.
//
// There is no runtime API for this, so the build-tag pair is the only way. Needed because this
// package contains a DELIBERATE data race, for the race detector to find, and a test that
// exercises it has to skip under -race or the suite fails by design.
//
// The same pattern appears in go-concepts/16-race-detector, for the same reason.
const raceDetectorEnabled = true
