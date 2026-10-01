package main

import (
	"os/exec"
	"testing"
)

// TestToolDirectiveRunsAPinnedTool runs `go tool` against a go.mod with a tool directive, offline. The tool
// is resolved like any dependency, here through a replace, and built and run by name.
func TestToolDirectiveRunsAPinnedTool(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go command on PATH")
	}

	out, err := runToolDirective(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	if out != "hello from a tool" {
		t.Errorf("go tool hello printed %q", out)
	}
}
