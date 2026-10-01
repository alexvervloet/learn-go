package main

import (
	"bytes"
	"fmt"
	"runtime/trace"
	"time"
)

// The flight recorder
// ===================
//
// A profile says where time went on average. An execution trace says what every goroutine was doing, moment
// by moment, which is what you want for the one request in a thousand that took two seconds. The trouble was
// always WHEN to trace: tracing everything costs too much to leave on, and by the time the slow request has
// happened, starting a trace is too late.
//
// runtime/trace.FlightRecorder (Go 1.25) keeps the trace running into a ring buffer of the last few seconds
// and throws the rest away. When something goes wrong, WriteTo snapshots the recent past. So the trace you
// get starts BEFORE the problem, which is the part you could never capture on purpose.
//
// The shape in a service: start one recorder at startup, and when a request runs past its budget, write a
// snapshot to a file, at most once a minute so a slow period does not fill the disk. `go tool trace` opens
// the file.
//
// Two rules the API enforces: only one flight recorder may run at a time, and a stopped one cannot snapshot.

// slowCallTrace runs work under a flight recorder and returns a snapshot if any call took longer than budget,
// or nil if none did.
func slowCallTrace(budget time.Duration, calls []func()) ([]byte, error) {
	fr := trace.NewFlightRecorder(trace.FlightRecorderConfig{MinAge: 5 * time.Second, MaxBytes: 16 << 20})

	if err := fr.Start(); err != nil {
		return nil, fmt.Errorf("start flight recorder: %w", err)
	}

	defer fr.Stop()

	for _, call := range calls {
		start := time.Now()
		call()

		if time.Since(start) > budget {
			// The snapshot is taken now, after the slow call, and covers the window leading up to it.
			var b bytes.Buffer
			if _, err := fr.WriteTo(&b); err != nil {
				return nil, fmt.Errorf("snapshot: %w", err)
			}

			return b.Bytes(), nil
		}
	}

	return nil, nil
}

// demoFlight runs fast calls and one slow one, and reports the snapshot the slow one triggered.
func demoFlight() {
	fast := func() { time.Sleep(time.Millisecond) }
	slow := func() { time.Sleep(60 * time.Millisecond) }

	snapshot, err := slowCallTrace(20*time.Millisecond, []func(){fast, fast, fast, slow, fast})
	if err != nil {
		fmt.Println("  ", err)
		return
	}

	fmt.Printf("  budget 20ms; one call took 60ms; snapshot of the recent past: %d bytes\n", len(snapshot))
	fmt.Println("  written to a file, `go tool trace snapshot.out` shows every goroutine leading up to it")
}
