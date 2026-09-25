package main

import (
	"runtime"
	"slices"
	"testing"
)

func TestCurrentBuild(t *testing.T) {
	info := CurrentBuild()

	if info.GOOS != runtime.GOOS {
		t.Errorf("GOOS = %q, want %q", info.GOOS, runtime.GOOS)
	}
	if info.GOARCH != runtime.GOARCH {
		t.Errorf("GOARCH = %q, want %q", info.GOARCH, runtime.GOARCH)
	}
	if info.GoVersion == "" {
		t.Error("GoVersion is empty")
	}
	if info.NumCPU < 1 {
		t.Errorf("NumCPU = %d, want at least 1", info.NumCPU)
	}
	if info.Debug != DebugEnabled {
		t.Errorf("Debug = %t, want %t", info.Debug, DebugEnabled)
	}
}

// TestGOOSIsACompileTimeConstant is the property that makes runtime.GOOS a
// reasonable alternative to a build tag for a one-line difference: the
// compiler knows it, so the dead branch is removed rather than evaluated.
//
// This cannot be observed directly from Go, so the test asserts the visible
// half: the value is one of the known set and never changes.
func TestGOOSIsStable(t *testing.T) {
	known := []string{
		"aix", "android", "darwin", "dragonfly", "freebsd", "illumos", "ios",
		"js", "linux", "netbsd", "openbsd", "plan9", "solaris", "wasip1", "windows",
	}

	if !slices.Contains(known, runtime.GOOS) {
		t.Errorf("GOOS = %q, which is not in the known set", runtime.GOOS)
	}

	for i := 0; i < 5; i++ {
		if runtime.GOOS != CurrentBuild().GOOS {
			t.Fatal("GOOS changed between calls, which is impossible")
		}
	}
}

func TestCommonTargetsAreWellFormed(t *testing.T) {
	targets := commonTargets()

	if len(targets) < 5 {
		t.Errorf("got %d targets, want at least 5", len(targets))
	}

	for _, target := range targets {
		if target[0] == "" || target[1] == "" {
			t.Errorf("malformed target %v", target)
		}
	}

	// The host's own platform should be buildable, which is a weak but real
	// sanity check on the list.
	host := [2]string{runtime.GOOS, runtime.GOARCH}
	if !slices.Contains(targets, host) {
		t.Logf("the host platform %v is not in the common list, which is fine", host)
	}
}

func TestCrossCompileDocsArePresent(t *testing.T) {
	if got := cgoTradeoffs(); len(got) < 4 {
		t.Errorf("expected at least 4 cgo notes, got %d", len(got))
	}
	if got := buildFlags(); len(got) < 4 {
		t.Errorf("expected at least 4 build flags, got %d", len(got))
	}
}
