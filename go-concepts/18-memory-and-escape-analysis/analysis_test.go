package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAnalyseEscapesOnThisPackage runs the compiler on this very package and
// checks the decisions match what the lesson claims.
//
// This is a slow test (it invokes the toolchain) and a valuable one: it fails
// if a future Go release changes an escape decision the lesson depends on,
// which is exactly the kind of drift documentation suffers from.
func TestAnalyseEscapesOnThisPackage(t *testing.T) {
	if testing.Short() {
		t.Skip("shells out to the compiler")
	}

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}

	decisions, err := AnalyseEscapes(dir)
	if err != nil {
		t.Fatalf("AnalyseEscapes: %v", err)
	}

	if len(decisions) == 0 {
		t.Fatal("the compiler reported no decisions at all")
	}

	counts := countByKind(decisions)
	t.Logf("escapes=%d moved=%d does-not-escape=%d inline=%d",
		counts["escapes"], counts["moved"], counts["does-not-escape"], counts["inline"])

	if counts["escapes"] == 0 {
		t.Error("expected some values to escape in this package")
	}
	if counts["moved"] == 0 {
		t.Error("expected some variables to be moved to the heap")
	}
	if counts["inline"] == 0 {
		t.Error("expected some inlining decisions")
	}
}

// TestNewUserPointerEscapesAccordingToTheCompiler pins one specific claim from
// the prose to the compiler's own output.
func TestNewUserPointerEscapesAccordingToTheCompiler(t *testing.T) {
	if testing.Short() {
		t.Skip("shells out to the compiler")
	}

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}

	decisions, err := AnalyseEscapes(dir)
	if err != nil {
		t.Fatalf("AnalyseEscapes: %v", err)
	}

	var foundUserEscape bool
	for _, d := range decisionsFor(decisions, "escapes.go") {
		if d.Kind == "escapes" && strings.Contains(d.Message, "&User{") {
			foundUserEscape = true
			t.Logf("line %d: %s", d.Line, d.Message)
		}
	}

	if !foundUserEscape {
		t.Error("the compiler did not report &User{...} escaping, which the lesson claims it does")
	}
}

func TestAnalyseEscapesOnABadDirectory(t *testing.T) {
	if testing.Short() {
		t.Skip("shells out to the compiler")
	}

	_, err := AnalyseEscapes(filepath.Join(t.TempDir(), "no-such-package"))
	if err == nil {
		t.Error("expected an error for a directory with no Go package")
	}
}

func TestDecisionsForFiltersByFile(t *testing.T) {
	decisions := []EscapeDecision{
		{File: "a.go", Line: 10, Kind: "escapes"},
		{File: "b.go", Line: 5, Kind: "escapes"},
		{File: "a.go", Line: 3, Kind: "moved"},
	}

	got := decisionsFor(decisions, "a.go")

	if len(got) != 2 {
		t.Fatalf("got %d decisions, want 2", len(got))
	}
	// Sorted by line.
	if got[0].Line != 3 || got[1].Line != 10 {
		t.Errorf("not sorted by line: %+v", got)
	}
}

func TestCountByKind(t *testing.T) {
	decisions := []EscapeDecision{
		{Kind: "escapes"}, {Kind: "escapes"}, {Kind: "moved"}, {Kind: "inline"},
	}

	counts := countByKind(decisions)

	if counts["escapes"] != 2 {
		t.Errorf("escapes = %d, want 2", counts["escapes"])
	}
	if counts["moved"] != 1 {
		t.Errorf("moved = %d, want 1", counts["moved"])
	}
	if counts["missing"] != 0 {
		t.Errorf("an absent kind should count 0, got %d", counts["missing"])
	}
}

func TestAtoi(t *testing.T) {
	tests := []struct {
		in   string
		want int
	}{{"0", 0}, {"7", 7}, {"42", 42}, {"1234", 1234}}

	for _, tt := range tests {
		if got := atoi(tt.in); got != tt.want {
			t.Errorf("atoi(%q) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

func TestAnalysisDocsArePresent(t *testing.T) {
	if got := theTwoMessages(); len(got) < 4 {
		t.Errorf("expected at least 4 notes, got %d", len(got))
	}
	if got := inliningChangesEverything(); len(got) < 4 {
		t.Errorf("expected at least 4 notes on inlining, got %d", len(got))
	}
	if got := flagsForAnalysis(); len(got) < 4 {
		t.Errorf("expected at least 4 flags, got %d", len(got))
	}
}
