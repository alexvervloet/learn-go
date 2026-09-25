package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Workspaces
// ==========
//
// A go.work at the repository root makes several modules resolve against each
// other locally, with no replace directives in any go.mod:
//
//	go 1.27
//
//	use (
//	    ./go-concepts
//	    ./dsa
//	)
//
// Before go.work (Go 1.18), developing two modules together meant a `replace`
// with a filesystem path in go.mod, which had to be removed before publishing
// and which somebody always forgot. go.work is never published, so the mistake
// is not available.
//
// This repository is the example: one module per area, so working through the
// language lessons does not download a Postgres driver.

// WorkspaceInfo is what a go.work declares.
type WorkspaceInfo struct {
	GoVersion string
	Modules   []string
	Replaces  []string
}

// parseGoWork is another deliberately small parser, enough to show the shape.
// golang.org/x/mod/modfile has the real one.
func parseGoWork(content string) (WorkspaceInfo, error) {
	info := WorkspaceInfo{}
	block := ""

	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(raw)
		if i := strings.Index(line, "//"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		if line == "" {
			continue
		}

		if line == ")" {
			block = ""
			continue
		}
		if open := strings.TrimSuffix(line, " ("); open != line {
			block = open
			continue
		}

		fields := strings.Fields(line)
		kind := block
		if kind == "" {
			kind = fields[0]
			fields = fields[1:]
		}

		switch kind {
		case "go":
			if len(fields) > 0 {
				info.GoVersion = fields[0]
			}
		case "use":
			if len(fields) > 0 {
				info.Modules = append(info.Modules, fields[0])
			}
		case "replace":
			info.Replaces = append(info.Replaces, strings.Join(fields, " "))
		}
	}

	if info.GoVersion == "" {
		return info, fmt.Errorf("parse go.work: no go directive")
	}
	return info, nil
}

// findGoWork walks up from the working directory looking for go.work, which is
// what the toolchain itself does. GOWORK=off disables the search entirely, and
// GOWORK=/path/to/go.work points it somewhere specific.
func findGoWork() (path string, content string, err error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", "", fmt.Errorf("getwd: %w", err)
	}

	for {
		candidate := filepath.Join(dir, "go.work")

		data, rerr := os.ReadFile(candidate) //nolint:gosec // a path built by walking up from the cwd
		if rerr == nil {
			return candidate, string(data), nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", "", fmt.Errorf("no go.work found above %s", dir)
		}
		dir = parent
	}
}

// listWorkspaceModules asks the toolchain rather than parsing, which is the
// right way round: `go list -m` reports what is actually selected, including
// modules reached through a replace.
//
// This is also the command the repository's Makefile uses to work around the
// ./... problem below.
func listWorkspaceModules() (modules []string, err error) {
	cmd := exec.Command("go", "list", "-m", "-f", "{{.Path}}")

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list -m: %w", err)
	}

	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			modules = append(modules, line)
		}
	}

	sort.Strings(modules)
	return modules, nil
}

// theDotDotDotTrap is the first thing anyone hits with a workspace, and it was
// the first thing this repository hit:
//
//	$ go vet ./...
//	pattern ./...: directory prefix . does not contain modules listed in
//	go.work or their selected dependencies
//
// The root of a workspace is not itself a module. `./...` is resolved against
// modules, so starting at a non-module directory matches nothing, and it is an
// ERROR rather than an empty result, which at least fails loudly.
//
// The fix is to ask the workspace what is in it:
//
//	go build $(go list -m -f '{{.Dir}}/...')
//
// which expands to an absolute path per module and keeps working as modules are
// added. Hardcoding the list means editing the Makefile every time.
func theDotDotDotTrap() []string {
	return []string{
		"go vet ./... from a workspace root: \"directory prefix . does not contain modules\"",
		"the root is not a module, so ./... resolves against nothing",
		"the fix: go vet $(go list -m -f '{{.Dir}}/...')",
		"this repository's Makefile does exactly that; see LESSONS.md",
	}
}

// workspaceIsLocalOnly is the property that keeps a workspace honest. go.work
// is not published and not consulted by anyone consuming your module, so each
// module must still build on its own.
//
// The consequence to plan for: a workspace can hide a missing require. Module A
// importing module B works locally through go.work even if A's go.mod never
// mentions B, and fails the moment anyone builds A alone.
//
// GOWORK=off is how you check.
func workspaceIsLocalOnly() []string {
	return []string{
		"go.work is not published and consumers never see it",
		"so a workspace can HIDE a missing require: it resolves locally and fails in CI",
		"GOWORK=off builds as a consumer would, which is the check",
		"a CI job that builds one module in isolation does the same thing",
		"this repo's modules have no cross-imports yet, so the hazard is ahead rather than behind",
	}
}

// verifyModuleBuildsAlone runs a build with GOWORK=off, which is how you prove
// a module stands on its own rather than leaning on the workspace.
//
// The two failure modes are different and must not be conflated:
//
//	the build ran and the code did not compile  -> ok=false, err=nil
//	the build could not be run at all           -> err != nil
//
// exec.ExitError is what distinguishes them: it means the process started and
// exited non-zero, so the compiler had its say. Anything else (a missing `go`
// binary, a directory that does not exist) is a problem with the harness rather
// than with the module, and returning nil for it would report a broken test
// setup as a broken module.
//
// An earlier version returned nil unconditionally, and golangci-lint's nilerr
// flagged it, correctly.
func verifyModuleBuildsAlone(moduleDir string) (ok bool, output string, err error) {
	cmd := exec.Command("go", "build", "./...")
	cmd.Dir = moduleDir
	cmd.Env = append(os.Environ(), "GOWORK=off")

	out, runErr := cmd.CombinedOutput()
	output = strings.TrimSpace(string(out))

	if runErr == nil {
		return true, output, nil
	}

	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		// The compiler ran and rejected the code. That is the answer, not an
		// error in asking the question.
		return false, output, nil
	}

	return false, output, fmt.Errorf("running go build in %s: %w", moduleDir, runErr)
}

// whenToUseAWorkspace, because the answer is not "always".
func whenToUseAWorkspace() map[string]string {
	return map[string]string{
		"several modules in one repository":   "yes: this repo's case, so each area keeps its own dependencies",
		"developing a library and its user":   "yes: the reason go.work was added, replacing a replace directive",
		"one module in a repository":          "no: a workspace adds a file and buys nothing",
		"pinning a dependency for everyone":   "no: that is a require, or a replace in go.mod",
		"making a monorepo build as one unit": "no: that is one module with several packages",
	}
}

// demoWorkspace prints this repository's own workspace.
func demoWorkspace() {
	path, content, err := findGoWork()
	if err != nil {
		fmt.Printf("  no go.work found: %v\n", err)
	} else {
		info, perr := parseGoWork(content)
		fmt.Printf("  this repository's go.work (%s):\n", path)
		fmt.Printf("    go %s, %d module(s), %d replace(s) (err=%v)\n",
			info.GoVersion, len(info.Modules), len(info.Replaces), perr)
		for _, m := range info.Modules {
			fmt.Printf("      use %s\n", m)
		}
	}

	modules, err := listWorkspaceModules()
	fmt.Printf("\n  go list -m reports %d module(s) (err=%v):\n", len(modules), err)
	for _, m := range modules {
		fmt.Printf("    %s\n", m)
	}

	fmt.Println("\n  the ./... trap:")
	for _, s := range theDotDotDotTrap() {
		fmt.Printf("    %s\n", s)
	}

	fmt.Println("\n  a workspace is local only:")
	for _, s := range workspaceIsLocalOnly() {
		fmt.Printf("    %s\n", s)
	}

	fmt.Println("\n  when to use one:")
	guidance := whenToUseAWorkspace()
	keys := make([]string, 0, len(guidance))
	for k := range guidance {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Printf("    %-36s %s\n", k, guidance[k])
	}
}
