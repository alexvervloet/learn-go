package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// The tool directive
// ==================
//
// A project needs tools as well as libraries: a linter, a code generator, a migration runner. Before Go 1.24
// the convention was a tools.go file behind a build tag that imported each tool with a blank import, so its
// version landed in go.mod. It worked and it was a hack everyone copied.
//
// Go 1.24 made it a directive:
//
//	go get -tool golang.org/x/tools/cmd/stringer@v0.30.0   adds `tool golang.org/x/tools/cmd/stringer`
//	go tool stringer                                       builds it at the pinned version and runs it
//
// The version comes from go.mod like any dependency, go.sum checks it, and the build is cached, so the second
// `go tool` is as fast as running a binary. A teammate needs nothing installed beyond Go.
//
// # Why this repository does not use it
//
// Tool dependencies are ordinary module requirements, and in a WORKSPACE every module's requirements are
// resolved together. A golangci-lint tool directive in one module would raise shared dependencies (x/tools,
// x/sync and the rest) for every module in go.work, so a lesson with no dependencies would build against
// versions chosen for a linter. So the Makefile runs tools with `go run pkg@version`, which pins the version
// without touching any go.mod. In a single-module project, the directive is the better answer.

// runToolDirective builds a tool module and an app module that declares it with a tool directive, in dir,
// then runs `go tool hello` from the app and returns what it printed.
//
// Everything is local: the tool is reached through a replace directive, and the go command runs with
// GOPROXY=off, so this needs no network and fetches nothing.
func runToolDirective(dir string) (string, error) {
	files := map[string]string{
		"hello/go.mod":  "module example.com/hello\n\ngo 1.24\n",
		"hello/main.go": "package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"hello from a tool\") }\n",
		"app/go.mod": strings.Join([]string{
			"module example.com/app",
			"",
			"go 1.24",
			"",
			"tool example.com/hello",
			"",
			"require example.com/hello v0.0.0",
			"",
			"replace example.com/hello => ../hello",
			"",
		}, "\n"),
	}

	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			return "", err
		}
	}

	cmd := exec.Command("go", "tool", "hello")
	cmd.Dir = filepath.Join(dir, "app")
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOPROXY=off", "GOFLAGS=-mod=mod", "GOTOOLCHAIN=local")

	out, err := cmd.CombinedOutput()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return "", fmt.Errorf("go tool hello failed: %s", strings.TrimSpace(string(out)))
		}

		return "", fmt.Errorf("running go tool: %w", err)
	}

	return strings.TrimSpace(string(out)), nil
}

// demoTools runs the tool directive end to end in a temporary directory.
func demoTools() {
	dir, err := os.MkdirTemp("", "tool-directive")
	if err != nil {
		fmt.Println("  ", err)
		return
	}

	defer func() { _ = os.RemoveAll(dir) }()

	out, err := runToolDirective(dir)
	if err != nil {
		fmt.Println("  ", err)
		return
	}

	fmt.Println("  app/go.mod declares `tool example.com/hello`; `go tool hello` printed:")
	fmt.Println("   ", out)
}
