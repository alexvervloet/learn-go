package main

import (
	"bytes"
	"runtime/trace"
	"testing"
	"time"
)

// TestASlowCallProducesATrace is the flight recorder's use: a snapshot exists only when something went
// wrong, and it is a real execution trace.
func TestASlowCallProducesATrace(t *testing.T) {
	fast := func() {}
	slow := func() { time.Sleep(50 * time.Millisecond) }

	none, err := slowCallTrace(20*time.Millisecond, []func(){fast, fast})
	if err != nil {
		t.Fatal(err)
	}
	if none != nil {
		t.Errorf("no call ran over budget, and there is a %d-byte snapshot anyway", len(none))
	}

	snapshot, err := slowCallTrace(20*time.Millisecond, []func(){fast, slow, fast})
	if err != nil {
		t.Fatal(err)
	}

	// An execution trace starts with a header naming its FORMAT version, "go 1.NN trace". Not the
	// toolchain's: Go 1.27 writes "go 1.26 trace", so the test checks the shape, not the number.
	if !bytes.HasPrefix(snapshot, []byte("go 1.")) || !bytes.Contains(snapshot[:16], []byte(" trace")) {
		t.Fatalf("the snapshot does not start with a trace header: %q", snapshot[:min(16, len(snapshot))])
	}

	t.Logf("snapshot: %d bytes", len(snapshot))
}

// TestOnlyOneFlightRecorderRuns is the rule the API enforces, and a stopped recorder has nothing to give.
func TestOnlyOneFlightRecorderRuns(t *testing.T) {
	first := trace.NewFlightRecorder(trace.FlightRecorderConfig{})
	if err := first.Start(); err != nil {
		t.Fatal(err)
	}

	if err := trace.NewFlightRecorder(trace.FlightRecorderConfig{}).Start(); err == nil {
		t.Error("a second flight recorder started while the first was running")
	}

	first.Stop()

	if _, err := first.WriteTo(&bytes.Buffer{}); err == nil {
		t.Error("a stopped flight recorder produced a snapshot")
	}
}
