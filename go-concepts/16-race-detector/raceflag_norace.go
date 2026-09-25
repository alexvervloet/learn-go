//go:build !race

package main

// raceDetectorEnabled reports whether this binary was built with -race.
// See raceflag_race.go for why this exists.
const raceDetectorEnabled = false
