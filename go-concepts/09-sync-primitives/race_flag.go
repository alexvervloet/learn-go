//go:build !race

package main

// raceEnabled reports whether this binary was built with -race.
//
// There is no runtime function for this. The `race` build tag is set by the
// toolchain when -race is on, so two files with opposite constraints is the
// standard way to expose it, and it is what the standard library does
// internally (internal/race).
const raceEnabled = false
