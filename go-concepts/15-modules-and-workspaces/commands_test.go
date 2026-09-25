package main

import (
	"strings"
	"testing"
)

func TestModuleCommandsAreWellFormed(t *testing.T) {
	commands := moduleCommands()

	if len(commands) < 12 {
		t.Errorf("got %d commands, want at least 12", len(commands))
	}

	seen := make(map[string]struct{}, len(commands))
	for _, c := range commands {
		if c.Name == "" || c.Does == "" || c.Modifies == "" {
			t.Errorf("incomplete entry: %+v", c)
		}
		if !strings.HasPrefix(c.Name, "go ") {
			t.Errorf("%q does not look like a go command", c.Name)
		}
		if _, dup := seen[c.Name]; dup {
			t.Errorf("duplicate command %q", c.Name)
		}
		seen[c.Name] = struct{}{}
	}
}

// TestBuildDoesNotModifyGoMod is the distinction people get wrong, recorded in
// the reference so it cannot drift.
func TestBuildIsRecordedAsNonMutating(t *testing.T) {
	for _, c := range moduleCommands() {
		if c.Name != "go build ./..." {
			continue
		}
		if !strings.Contains(c.Modifies, "NOTHING") {
			t.Errorf("go build should be recorded as non-mutating, got %q", c.Modifies)
		}
		return
	}
	t.Error("go build is not in the command reference")
}

func TestTidyDiffIsRecordedAsTheCICheck(t *testing.T) {
	for _, c := range moduleCommands() {
		if !strings.Contains(c.Name, "tidy -diff") {
			continue
		}
		if !strings.Contains(c.Modifies, "nothing") {
			t.Errorf("tidy -diff should modify nothing, got %q", c.Modifies)
		}
		return
	}
	t.Error("go mod tidy -diff is not in the command reference")
}

func TestEnvironmentVariablesAreDocumented(t *testing.T) {
	env := environmentVariables()

	for _, want := range []string{"GOPROXY", "GOPRIVATE", "GOWORK", "GOFLAGS"} {
		if env[want] == "" {
			t.Errorf("%s is not documented", want)
		}
	}
}

func TestCIChecksAreDocumented(t *testing.T) {
	checks := ciChecks()

	if len(checks) < 3 {
		t.Errorf("got %d CI checks, want at least 3", len(checks))
	}

	// The two that actually catch things.
	var hasTidy, hasWorkOff bool
	for _, c := range checks {
		if strings.Contains(c.Name, "tidy -diff") {
			hasTidy = true
		}
		if strings.Contains(c.Name, "GOWORK=off") {
			hasWorkOff = true
		}
	}
	if !hasTidy {
		t.Error("go mod tidy -diff should be among the CI checks")
	}
	if !hasWorkOff {
		t.Error("the GOWORK=off isolation check should be among the CI checks")
	}
}

func TestGoSumExplanationIsPresent(t *testing.T) {
	notes := theGoSumMisunderstanding()

	if len(notes) < 4 {
		t.Errorf("expected at least 4 notes, got %d", len(notes))
	}

	joined := strings.Join(notes, " ")
	if !strings.Contains(joined, "go.mod") || !strings.Contains(joined, "go.sum") {
		t.Error("the explanation should contrast go.mod with go.sum")
	}
}
