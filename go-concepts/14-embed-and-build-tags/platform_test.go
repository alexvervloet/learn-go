package main

import (
	"runtime"
	"strings"
	"testing"
)

// TestCurrentPlatformMatchesTheBuild: exactly one platform file is compiled,
// and it must be the right one.
func TestCurrentPlatformMatchesTheBuild(t *testing.T) {
	name := CurrentPlatform.Name()

	switch runtime.GOOS {
	case "windows":
		if name != "windows" {
			t.Errorf("platform = %q on %s, want windows", name, runtime.GOOS)
		}
	case "linux", "darwin", "freebsd", "openbsd", "netbsd", "dragonfly", "solaris", "aix", "illumos":
		if name != "unix" {
			t.Errorf("platform = %q on %s, want unix", name, runtime.GOOS)
		}
	default:
		// js/wasm, wasip1/wasm, plan9. The fallback exists for exactly these,
		// and without it this package does not compile for them at all.
		if name != "other" {
			t.Errorf("platform = %q on %s, want other", name, runtime.GOOS)
		}
	}
}

// TestPlatformAnswersAreConsistent checks the implementation is internally
// coherent, whichever one was compiled.
func TestPlatformAnswersAreConsistent(t *testing.T) {
	p := CurrentPlatform

	if p.Name() == "" {
		t.Error("Name() is empty")
	}
	if p.TempDirName() == "" {
		t.Error("TempDirName() is empty")
	}

	sep := p.PathListSeparator()
	if sep != ':' && sep != ';' {
		t.Errorf("PathListSeparator() = %q, want : or ;", string(sep))
	}

	ending := p.LineEnding()
	if ending != "\n" && ending != "\r\n" {
		t.Errorf("LineEnding() = %q, want \\n or \\r\\n", ending)
	}

	// The two must agree: Windows uses ; and \r\n, everything else : and \n.
	if (sep == ';') != (ending == "\r\n") {
		t.Errorf("inconsistent: separator %q with line ending %q", string(sep), ending)
	}
}

// TestPlatformMatchesTheStandardLibrary: os.PathListSeparator is the real
// answer, so the hand-written one must agree with it.
func TestPlatformMatchesTheStandardLibrary(t *testing.T) {
	want := osPathListSeparator()

	if got := CurrentPlatform.PathListSeparator(); got != want {
		t.Errorf("PathListSeparator() = %q, but os says %q", string(got), string(want))
	}
}

func TestDescribePlatform(t *testing.T) {
	lines := describePlatform(CurrentPlatform)

	if len(lines) != 5 {
		t.Errorf("got %d lines, want 5", len(lines))
	}
	for _, want := range []string{"name:", "path list separator:", "line ending:", "temp directory:"} {
		found := false
		for _, l := range lines {
			if strings.HasPrefix(l, want) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("no line starting with %q in %v", want, lines)
		}
	}
}

func TestBuildTagDocsArePresent(t *testing.T) {
	if got := buildTagMechanisms(); len(got) < 4 {
		t.Errorf("expected at least 4 documented mechanisms, got %d", len(got))
	}
	if got := commonConstraints(); len(got) < 5 {
		t.Errorf("expected at least 5 documented constraints, got %d", len(got))
	}
}
