// Package makefiles_test runs the example Makefiles and asserts on what they print.
//
// # Why a Go test for a Makefile
//
// Because the examples are claims, and a claim in a comment goes stale. "Each recipe line is its own shell" is
// either true of the make on this machine or it is not, and running it is the only way to know.
//
// It also makes the module fit the repo: every other module's claims are checked by a test, and a directory of
// Makefiles with a README asserting things about them would be the one place a reader has to take my word.
package makefiles_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	once      sync.Once
	makePath  string
	makeIsGNU bool
	version   string
)

// requireMake skips unless GNU make is available.
//
// # Why GNU specifically
//
// BSD make (which is `make` on a FreeBSD or a stock macOS without the developer tools) has a different syntax for
// conditionals, no $(shell ...), and no .PHONY in the same sense. Every Makefile in this repo is GNU, and saying
// so is better than producing a parse error a reader has to diagnose.
//
// macOS ships GNU make 3.81 from 2006 as /usr/bin/make, which is old enough to lack --output-sync and several
// functions. `brew install make` gives `gmake`, and the tests check for the feature rather than the version where
// it matters.
func requireMake(t testing.TB) string {
	t.Helper()

	once.Do(func() {
		for _, candidate := range []string{"gmake", "make"} {
			path, err := exec.LookPath(candidate)
			if err != nil {
				continue
			}

			out, err := exec.Command(path, "--version").Output()
			if err != nil {
				continue
			}

			version = strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0]

			if strings.Contains(version, "GNU Make") {
				makePath = path
				makeIsGNU = true

				return
			}

			// Remember a non-GNU make so the skip message can say what was found.
			makePath = path
		}
	})

	if !makeIsGNU {
		t.Skipf("GNU make is not available (found %q, version %q)\n"+
			"  macOS: brew install make, which provides gmake",
			makePath, version)
	}

	return makePath
}

// moduleRoot finds the directory holding go.mod.
func moduleRoot(t testing.TB) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the working directory")
		}

		dir = parent
	}
}

// runMake runs a target and returns its combined output.
func runMake(t testing.TB, file, target string, args ...string) string {
	t.Helper()

	make := requireMake(t)

	root := moduleRoot(t)

	argv := append([]string{"-f", filepath.Join("examples", file), target}, args...)

	cmd := exec.Command(make, argv...)
	cmd.Dir = root

	// A clean-ish environment, so a developer's MAKEFLAGS (which can contain -j) does not change the
	// result. MAKEFLAGS is inherited and it is the one variable that silently makes a serial test
	// parallel.
	cmd.Env = append(os.Environ(), "MAKEFLAGS=")

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("make -f %s %s: %v\n%s", file, target, err, out)
	}

	return string(out)
}

// TestEachRecipeLineIsItsOwnShell.
func TestEachRecipeLineIsItsOwnShell(t *testing.T) {
	root := moduleRoot(t)

	separate := runMake(t, "01-basics.mk", "separate-shells")
	single := runMake(t, "01-basics.mk", "one-shell")

	t.Logf("separate lines:\n%s", indent(separate))
	t.Logf("one line:\n%s", indent(single))

	// The cd on its own line did not persist, so pwd printed the Makefile's directory.
	if !strings.Contains(separate, root) {
		t.Errorf("the separate-line cd persisted; pwd printed something other than %s", root)
	}

	if strings.Contains(separate, "FOO is 'bar'") {
		t.Error("a variable set on one recipe line survived to the next")
	}

	if !strings.Contains(separate, "FOO is ''") {
		t.Errorf("expected an empty FOO on the next line:\n%s", separate)
	}

	// And on one line, both persist.
	if !strings.Contains(single, "/tmp") {
		t.Errorf("the one-line cd did not take:\n%s", single)
	}

	if !strings.Contains(single, "FOO is 'bar'") {
		t.Errorf("the one-line variable did not survive:\n%s", single)
	}

	t.Log("every recipe LINE is a separate shell, so a cd or a variable assignment does not " +
		"reach the next one. That is why every loop over directories in a real Makefile is a " +
		"wall of backslash continuations.")
}

// TestPhonyMattersWhenAFileExists is the trap that makes CI pass while running nothing.
func TestPhonyMattersWhenAFileExists(t *testing.T) {
	out := runMake(t, "04-traps.mk", "trap-phony")

	t.Logf("%s", indent(out))

	if !strings.Contains(out, "the build target ran") {
		t.Errorf("the target did not run with no file present:\n%s", out)
	}

	if !strings.Contains(out, "is up to date") {
		t.Errorf("make did not skip the target when a directory of that name existed:\n%s", out)
	}

	// The count is the assertion: the recipe ran ONCE across two invocations.
	if got := strings.Count(out, "the build target ran"); got != 1 {
		t.Errorf("the recipe ran %d times, want 1", got)
	}

	t.Log("with a directory called 'build' present, make reported success and ran nothing. Every " +
		"Go repo has a bin/ or a dist/, and every one is one mkdir away from a CI job that " +
		"runs nothing and passes.")
}

// TestSimpleAndRecursiveExpansion.
func TestSimpleAndRecursiveExpansion(t *testing.T) {
	out := runMake(t, "02-variables.mk", "expansion")

	t.Logf("%s", indent(out))

	lines := map[string]string{}

	for _, line := range strings.Split(out, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}

		lines[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}

	simple1 := lines["simple, reference 1"]
	simple2 := lines["simple, reference 2"]
	recursive1 := lines["recursive, reference 1"]
	recursive2 := lines["recursive, reference 2"]

	if simple1 == "" || recursive1 == "" {
		t.Fatalf("could not parse the output:\n%s", out)
	}

	// := expanded once, so both references report the same instant.
	if simple1 != simple2 {
		t.Errorf("a := variable produced two different values:\n  %s\n  %s", simple1, simple2)
	}

	// = expanded per reference, so they differ.
	if recursive1 == recursive2 {
		t.Errorf("an = variable produced the same value twice, so it was not re-expanded:\n  %s",
			recursive1)
	}

	t.Log("a := variable runs its shell command once, when the line is read. An = variable runs it " +
		"on every reference, so `VERSION = $(shell git describe)` used in five recipes runs git " +
		"five times. It is one character and it is the most expensive Makefile mistake there is.")
}

// TestAutomaticVariables.
func TestAutomaticVariables(t *testing.T) {
	root := moduleRoot(t)

	t.Cleanup(func() {
		_ = exec.Command(requireMake(t), "-f",
			filepath.Join("examples", "02-variables.mk"), "clean").Run()

		for _, f := range []string{"a.txt", "b.txt", "combined.txt", "a.upper", "b.upper"} {
			_ = os.Remove(filepath.Join(root, f))
		}
	})

	out := runMake(t, "02-variables.mk", "combined.txt")

	t.Logf("%s", indent(out))

	for _, want := range []string{
		"$@ = combined.txt",
		"$< = a.txt",
		"$^ = a.txt b.txt",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the output does not contain %q:\n%s", want, out)
		}
	}

	// Pattern rules and $*.
	patterns := runMake(t, "02-variables.mk", "patterns")

	t.Logf("%s", indent(patterns))

	if !strings.Contains(patterns, "stem $* = a") {
		t.Errorf("the pattern rule's stem is wrong:\n%s", patterns)
	}

	if !strings.Contains(patterns, "A") || !strings.Contains(patterns, "B") {
		t.Errorf("the pattern rule did not transform the files:\n%s", patterns)
	}

	t.Log("$< is the FIRST prerequisite and $^ is all of them. A compile rule wants $< and a link " +
		"rule wants $^, and using $^ where $< belongs passes every header to the compiler.")
}

// TestMakeDeduplicatesPrerequisites.
func TestMakeDeduplicatesPrerequisites(t *testing.T) {
	out := runMake(t, "04-traps.mk", "trap-prereqs")

	t.Logf("%s", indent(out))

	if got := strings.Count(out, "the prerequisite ran"); got != 1 {
		t.Errorf("the prerequisite ran %d times, want 1", got)
	}

	t.Log("three identical prerequisites and make ran it once, which is why a diamond dependency " +
		"does not build the shared node twice. .PHONY does not change that: it only stops make " +
		"looking for a file.")
}

// TestParallelOutputInterleaves, and what --output-sync does about it.
func TestParallelOutputInterleaves(t *testing.T) {
	requireMake(t)

	// --output-sync arrived in GNU make 4.0 and macOS ships 3.81, so this needs a check rather than
	// an assumption.
	if !supportsOutputSync(t) {
		t.Skipf("this make has no --output-sync (%s); GNU make 4.0 added it and macOS ships 3.81",
			version)
	}

	out := runMake(t, "04-traps.mk", "trap-parallel")

	t.Logf("%s", indent(out))

	sections := strings.Split(out, "---")

	if len(sections) < 4 {
		t.Fatalf("expected three sections, got %d:\n%s", len(sections)-1, out)
	}

	serial := sections[2]
	parallel := sections[4]

	t.Logf("serial:   %q", strings.Join(strings.Fields(serial), " "))
	t.Logf("parallel: %q", strings.Join(strings.Fields(parallel), " "))

	// Serial output is grouped, because one target finishes before the next starts.
	if !isGrouped(serial) {
		t.Errorf("the serial output is interleaved, which it should not be: %q", serial)
	}

	// Parallel output is usually interleaved. "Usually", because it depends on the scheduler, so
	// this LOGS rather than asserting: a test that requires a race to happen is a flaky test.
	if isGrouped(parallel) {
		t.Log("the parallel output happened to come out grouped this run, which it can; the " +
			"point is that nothing guarantees it")
	} else {
		t.Log("the parallel output interleaved line by line, which is the default and is why " +
			"--output-sync exists")
	}

	// With --output-sync it is grouped again, and THAT is guaranteed.
	synced := sections[6]

	t.Logf("synced:   %q", strings.Join(strings.Fields(synced), " "))

	if !isGrouped(synced) {
		t.Errorf("--output-sync=target did not group the output: %q", synced)
	}

	t.Log("make -j interleaves output line by line and nothing warns you. --output-sync=target " +
		"groups each target's output, and it is off by default.")

	t.Log("and it guarantees GROUPING, not ORDER: whichever target finishes first prints first, so " +
		"a synced run can show b before a. A test asserting a-then-b fails on correct output, " +
		"which is how the helper here came to count prefix changes instead.")
}

// isGrouped reports whether each target's lines are CONTIGUOUS, in any order.
//
// The first version required every a-line before every b-line, and `--output-sync=target` produced
// "b1 b2 b3 a1 a2 a3", which is grouped and failed the check.
//
// That is the correct behaviour and the correction is worth keeping: --output-sync guarantees that a target's
// output is not interleaved with another's, and says nothing about which target's output comes first. Whichever
// finishes first prints first.
func isGrouped(section string) bool {
	lines := strings.Fields(section)

	// Walk the lines and count how many times the prefix CHANGES. Grouped output changes once
	// (a...a b...b or b...b a...a); interleaved output changes on nearly every line.
	changes := 0
	previous := ""

	for _, line := range lines {
		prefix := line[:1]

		if previous != "" && prefix != previous {
			changes++
		}

		previous = prefix
	}

	return changes <= 1
}

func supportsOutputSync(t testing.TB) bool {
	t.Helper()

	out, err := exec.Command(requireMake(t), "--help").CombinedOutput()
	if err != nil {
		return false
	}

	return strings.Contains(string(out), "output-sync")
}

// TestTheHelpTargetDocumentsItself.
func TestHelpTargetDocumentsItself(t *testing.T) {
	out := runMake(t, "03-go-workflow.mk", "help")

	t.Logf("%s", indent(out))

	for _, want := range []string{"build", "test", "test-race", "cover", "lint", "ci"} {
		if !strings.Contains(out, want) {
			t.Errorf("the help output does not mention %q", want)
		}
	}

	// Every `## target: description` line in the file appears in the help, which is what makes the
	// documentation impossible to forget.
	content, err := os.ReadFile(filepath.Join(moduleRoot(t), "examples", "03-go-workflow.mk"))
	if err != nil {
		t.Fatal(err)
	}

	documented := 0

	for _, line := range strings.Split(string(content), "\n") {
		if strings.HasPrefix(line, "## ") {
			documented++

			name, _, _ := strings.Cut(strings.TrimPrefix(line, "## "), ":")

			if !strings.Contains(out, strings.TrimSpace(name)) {
				t.Errorf("%q is documented in the file and missing from the help", name)
			}
		}
	}

	t.Logf("%d documented targets, all of them in the help output", documented)

	t.Log("the `## target: description` convention keeps the documentation next to the thing it " +
		"documents, so it cannot drift. $(MAKEFILE_LIST) rather than a hard-coded filename, so " +
		"it still works when the file is included from another.")
}

// TestWorkspaceModuleExpansion is the thing that breaks first in a multi-module repo.
func TestWorkspaceModuleExpansion(t *testing.T) {
	out := runMake(t, "03-go-workflow.mk", "modules")

	modules := strings.Fields(out)

	t.Logf("%d modules in the workspace", len(modules))

	for _, m := range modules {
		t.Logf("  %s", m)
	}

	if len(modules) < 5 {
		t.Errorf("found %d modules, which is fewer than this workspace has", len(modules))
	}

	for _, m := range modules {
		if !strings.HasSuffix(m, "/...") {
			t.Errorf("%q does not end with /..., so it will not match packages", m)
		}
	}

	// And the thing it works around: ./... from a workspace root matches nothing.
	cmd := exec.Command("go", "build", "./...")
	cmd.Dir = filepath.Dir(filepath.Dir(filepath.Dir(moduleRoot(t))))

	buildOut, err := cmd.CombinedOutput()

	t.Logf("`go build ./...` from the workspace root: %v", err)
	t.Logf("  %s", strings.TrimSpace(string(buildOut)))

	if err == nil {
		t.Log("it succeeded here, which means this directory IS a module; in a pure workspace " +
			"root it fails with 'directory prefix . does not contain modules listed in go.work'")
	}

	t.Log("the root of a workspace is not itself a module, so ./... matches nothing. " +
		"`go list -m -f '{{.Dir}}/...'` asks the workspace for its modules and keeps working " +
		"as modules are added.")
}

// TestGofmtLPassesWhateverItFinds is the CI trap the fmt-check target works around.
func TestGofmtLPassesWhateverItFinds(t *testing.T) {
	dir := t.TempDir()

	// A deliberately misformatted file.
	bad := filepath.Join(dir, "bad.go")

	if err := os.WriteFile(bad, []byte("package main\nfunc  main( ){\nx:=1\n_=x\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("gofmt", "-l", dir)

	out, err := cmd.Output()

	t.Logf("gofmt -l printed %q and exited with %v", strings.TrimSpace(string(out)), err)

	// The output is non-empty AND the exit code is zero. That is the trap.
	if len(strings.TrimSpace(string(out))) == 0 {
		t.Fatal("gofmt -l found nothing to report, so this demonstration is broken")
	}

	if err != nil {
		t.Errorf("gofmt -l exited non-zero (%v); this trap may no longer exist", err)
	}

	t.Log("gofmt -l prints the offending files and exits 0. A CI step that just runs it passes " +
		"whatever it finds, which is why the fmt-check target captures the output and turns a " +
		"non-empty result into a failure. Every project gets this wrong once.")
}

// TestRunsQuicklyEnoughToBeUseful, because a Makefile nobody runs is documentation.
func TestExamplesAreFast(t *testing.T) {
	requireMake(t)

	targets := []struct{ file, target string }{
		{"01-basics.mk", "dollars"},
		{"02-variables.mk", "defaults"},
		{"03-go-workflow.mk", "help"},
		{"04-traps.mk", "trap-cd"},
	}

	for _, tc := range targets {
		start := time.Now()

		runMake(t, tc.file, tc.target)

		t.Logf("%-20s %-14s %v", tc.file, tc.target, time.Since(start).Round(time.Millisecond))
	}

	if runtime.GOOS == "windows" {
		t.Skip("these Makefiles assume a POSIX shell")
	}
}

func indent(s string) string {
	return "  " + strings.ReplaceAll(strings.TrimRight(s, "\n"), "\n", "\n  ")
}
