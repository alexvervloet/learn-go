//go:build race

package main

// raceEnabled reports whether this binary was built with -race. See the
// !race build of this file for why it is a build tag and not a function.
const raceEnabled = true
