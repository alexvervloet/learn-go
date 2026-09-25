package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProfileKindsAreDocumented(t *testing.T) {
	kinds := profileKinds()

	if len(kinds) < 6 {
		t.Errorf("got %d profile kinds, want at least 6", len(kinds))
	}

	seen := make(map[string]struct{}, len(kinds))
	for _, k := range kinds {
		if k.Name == "" || k.Shows == "" || k.TestFlag == "" || k.WhenToUse == "" {
			t.Errorf("incomplete entry: %+v", k)
		}
		if _, dup := seen[k.Name]; dup {
			t.Errorf("duplicate profile %q", k.Name)
		}
		seen[k.Name] = struct{}{}
	}

	for _, want := range []string{"cpu", "heap", "allocs", "block", "mutex", "trace"} {
		if _, ok := seen[want]; !ok {
			t.Errorf("the %q profile is not documented", want)
		}
	}
}

func TestHeapAndAllocsAreDistinguished(t *testing.T) {
	notes := strings.Join(heapVsAllocs(), " ")

	if !strings.Contains(notes, "LEAK") {
		t.Error("the heap profile should be described as the leak-finding one")
	}
	if !strings.Contains(notes, "GC PRESSURE") {
		t.Error("the allocs profile should be described as the GC-pressure one")
	}
}

func TestFlatVsCumIsDocumented(t *testing.T) {
	notes := flatVsCum()

	if len(notes) < 4 {
		t.Errorf("expected at least 4 notes, got %d", len(notes))
	}

	joined := strings.Join(notes, " ")
	if !strings.Contains(joined, "flat") || !strings.Contains(joined, "cum") {
		t.Error("both terms should be explained")
	}
}

// TestWriteCPUProfileProducesAFile: the from-code path must actually work,
// because a demo that silently writes nothing teaches the wrong thing.
func TestWriteCPUProfile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cpu.out")

	if err := writeCPUProfile(path, func() { sinkInt = burnCPU(1000) }); err != nil {
		t.Fatalf("writeCPUProfile: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Size() == 0 {
		t.Error("the profile is empty")
	}
}

func TestWriteHeapProfile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "heap.out")

	if err := writeHeapProfile(path, func() { sinkSlice = growPresized(10_000) }); err != nil {
		t.Fatalf("writeHeapProfile: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Size() == 0 {
		t.Error("the profile is empty")
	}
}

func TestWriteProfileReportsAnUnwritablePath(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "no-such-dir", "cpu.out")

	if err := writeCPUProfile(bad, func() {}); err == nil {
		t.Error("expected an error for an unwritable path")
	}
	if err := writeHeapProfile(bad, func() {}); err == nil {
		t.Error("expected an error for an unwritable path")
	}
}

func TestBurnCPUIsDeterministic(t *testing.T) {
	first := burnCPU(100)

	for i := 0; i < 3; i++ {
		if got := burnCPU(100); got != first {
			t.Fatalf("run %d gave %d, first gave %d", i, got, first)
		}
	}
}

func TestProfilingDocsArePresent(t *testing.T) {
	if got := profileCommands(); len(got) < 5 {
		t.Errorf("expected at least 5 commands, got %d", len(got))
	}
	if got := interpretingACPUProfile(); len(got) < 5 {
		t.Errorf("expected at least 5 interpretation steps, got %d", len(got))
	}
}

func BenchmarkBurnCPU(b *testing.B) {
	for b.Loop() {
		sinkInt = burnCPU(100)
	}
}
