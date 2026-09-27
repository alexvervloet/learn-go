//go:build !race

package concurrency

// raceDetectorEnabled is false when the binary was built without -race.
const raceDetectorEnabled = false
