package main

import (
	"strings"
	"testing"
)

// TestSampleReportIsARealReport: the transcribed report must keep the shape a
// reader will actually see, or the anatomy below describes nothing.
func TestSampleReportIsARealReport(t *testing.T) {
	for _, want := range []string{
		"WARNING: DATA RACE",
		"Read at 0x",
		"Previous write at 0x",
		"created at:",
	} {
		if !strings.Contains(sampleReport, want) {
			t.Errorf("the sample report is missing %q", want)
		}
	}
}

// TestSampleReportHasMatchingAddresses is what makes it one race rather than
// two: both halves must name the same address.
func TestSampleReportHasMatchingAddresses(t *testing.T) {
	const addr = "0x00c0002a211c"

	if strings.Count(sampleReport, addr) != 2 {
		t.Errorf("expected the address %s exactly twice, got %d",
			addr, strings.Count(sampleReport, addr))
	}
}

func TestAnatomyIsOrdered(t *testing.T) {
	sections := anatomy()

	if len(sections) < 4 {
		t.Fatalf("got %d sections, want at least 4", len(sections))
	}

	var hasPriorityOne bool
	for _, s := range sections {
		if s.Name == "" || s.Tells == "" {
			t.Errorf("incomplete section: %+v", s)
		}
		if s.Priority < 1 || s.Priority > 3 {
			t.Errorf("section %q has priority %d, want 1-3", s.Name, s.Priority)
		}
		if s.Priority == 1 {
			hasPriorityOne = true
		}
	}

	if !hasPriorityOne {
		t.Error("no section is marked as the place to look first")
	}
}

// TestCreationSiteIsThePriority: the "created at" section is the one people
// skip and the one that usually identifies the bug.
func TestCreationSiteIsThePriority(t *testing.T) {
	for _, s := range anatomy() {
		if !strings.Contains(s.Name, "CREATED") {
			continue
		}
		if s.Priority != 1 {
			t.Errorf("the creation site has priority %d, want 1", s.Priority)
		}
		return
	}
	t.Error("the creation site is not in the anatomy")
}

func TestReadingDocsArePresent(t *testing.T) {
	if got := whatThisReportSaid(); len(got) < 5 {
		t.Errorf("expected at least 5 notes on the sample, got %d", len(got))
	}
	if got := actOnIt(); len(got) < 5 {
		t.Errorf("expected at least 5 checklist steps, got %d", len(got))
	}
	if got := commandsForRaces(); len(got) < 5 {
		t.Errorf("expected at least 5 commands, got %d", len(got))
	}
	if got := waitGroupGoObscuresTheCreationSite(); len(got) < 3 {
		t.Errorf("expected at least 3 notes on the WaitGroup.Go caveat, got %d", len(got))
	}
}

// TestCommandsAreWellFormed: GORACE entries are environment variables, the
// rest are commands, and mixing them up would mislead.
func TestCommandsAreWellFormed(t *testing.T) {
	for cmd, description := range commandsForRaces() {
		if description == "" {
			t.Errorf("%q has no description", cmd)
		}
		if !strings.HasPrefix(cmd, "go ") && !strings.HasPrefix(cmd, "GORACE") {
			t.Errorf("%q is neither a go command nor a GORACE variable", cmd)
		}
	}
}
