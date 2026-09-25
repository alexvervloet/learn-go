package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestParseGoWork(t *testing.T) {
	const content = `go 1.27

use (
	./go-concepts
	./dsa
)

use ./standalone

replace example.com/lib => ./local-lib
`

	info, err := parseGoWork(content)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if info.GoVersion != "1.27" {
		t.Errorf("go version = %q, want 1.27", info.GoVersion)
	}

	want := []string{"./go-concepts", "./dsa", "./standalone"}
	if !slices.Equal(info.Modules, want) {
		t.Errorf("modules = %v, want %v", info.Modules, want)
	}

	if len(info.Replaces) != 1 {
		t.Errorf("replaces = %v, want one", info.Replaces)
	}
}

func TestParseGoWorkRejectsInputWithNoGoDirective(t *testing.T) {
	if _, err := parseGoWork("use ./a\n"); err == nil {
		t.Error("a go.work with no go directive should be rejected")
	}
}

func TestParseGoWorkIgnoresComments(t *testing.T) {
	const content = `// a leading comment
go 1.27

use (
	./a  // a trailing comment
	// a whole-line comment
	./b
)
`

	info, err := parseGoWork(content)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if want := []string{"./a", "./b"}; !slices.Equal(info.Modules, want) {
		t.Errorf("modules = %v, want %v", info.Modules, want)
	}
}

// TestFindGoWorkLocatesThisRepository walks up from the test's working
// directory, which is how the toolchain itself finds go.work.
func TestFindGoWorkLocatesThisRepository(t *testing.T) {
	path, content, err := findGoWork()
	if err != nil {
		t.Fatalf("findGoWork: %v", err)
	}

	if filepath.Base(path) != "go.work" {
		t.Errorf("found %q, want a file named go.work", path)
	}
	if !strings.Contains(content, "use") {
		t.Errorf("the file has no use directive:\n%s", content)
	}

	info, err := parseGoWork(content)
	if err != nil {
		t.Fatalf("parse this repo's go.work: %v", err)
	}
	if len(info.Modules) == 0 {
		t.Error("this repository's go.work lists no modules")
	}

	// Every listed module must actually exist, which catches a go.work left
	// pointing at a directory somebody moved.
	root := filepath.Dir(path)
	for _, m := range info.Modules {
		dir := filepath.Join(root, m)
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr != nil {
			t.Errorf("go.work lists %q but %s has no go.mod: %v", m, dir, statErr)
		}
	}
}

// TestListWorkspaceModulesAgreesWithGoWork: the toolchain's view and the file's
// contents must match, or the file is stale.
func TestListWorkspaceModulesAgreesWithGoWork(t *testing.T) {
	if testing.Short() {
		t.Skip("shells out to go list")
	}

	modules, err := listWorkspaceModules()
	if err != nil {
		t.Fatalf("go list -m: %v", err)
	}
	if len(modules) == 0 {
		t.Fatal("go list -m reported no modules")
	}

	_, content, err := findGoWork()
	if err != nil {
		t.Fatalf("findGoWork: %v", err)
	}
	info, err := parseGoWork(content)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if len(modules) != len(info.Modules) {
		t.Errorf("go list -m reports %d modules, go.work lists %d: %v vs %v",
			len(modules), len(info.Modules), modules, info.Modules)
	}

	// This module must be among them, which is a weak but real self-check.
	found := slices.ContainsFunc(modules, func(m string) bool {
		return strings.HasSuffix(m, "/go-concepts")
	})
	if !found {
		t.Errorf("go-concepts is not in %v", modules)
	}
}

// TestEachModuleBuildsWithoutTheWorkspace is the check that matters for a
// multi-module repository: go.work is local only, so a module leaning on it
// would fail for anyone consuming it.
//
// Right now there are no cross-module imports, so this passes trivially. It is
// here so that it starts failing the moment one is added without the matching
// require, rather than months later in somebody else's build.
func TestEachModuleBuildsWithoutTheWorkspace(t *testing.T) {
	if testing.Short() {
		t.Skip("shells out to go build")
	}

	path, content, err := findGoWork()
	if err != nil {
		t.Fatalf("findGoWork: %v", err)
	}
	info, err := parseGoWork(content)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	root := filepath.Dir(path)
	for _, m := range info.Modules {
		dir := filepath.Join(root, m)

		t.Run(m, func(t *testing.T) {
			ok, output, buildErr := verifyModuleBuildsAlone(dir)
			if buildErr != nil {
				t.Fatalf("running the build: %v", buildErr)
			}
			if !ok {
				t.Errorf("%s does not build with GOWORK=off:\n%s", m, output)
			}
		})
	}
}

func TestWorkspaceDocsArePresent(t *testing.T) {
	if got := theDotDotDotTrap(); len(got) < 3 {
		t.Errorf("expected at least 3 notes on the ./... trap, got %d", len(got))
	}
	if got := workspaceIsLocalOnly(); len(got) < 3 {
		t.Errorf("expected at least 3 notes on locality, got %d", len(got))
	}
	if got := whenToUseAWorkspace(); len(got) < 4 {
		t.Errorf("expected at least 4 pieces of guidance, got %d", len(got))
	}
}
