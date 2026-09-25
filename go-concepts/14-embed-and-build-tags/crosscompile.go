package main

import (
	"fmt"
	"runtime"
)

// Cross-compilation
// =================
//
//	GOOS=linux   GOARCH=amd64 go build -o app-linux-amd64
//	GOOS=darwin  GOARCH=arm64 go build -o app-darwin-arm64
//	GOOS=windows GOARCH=amd64 go build -o app.exe
//
// No toolchain to install and no container. `go tool dist list` prints every
// supported pair, and there are over forty.
//
// The caveat is CGO. Anything using cgo needs a C cross-compiler for the
// target. CGO_ENABLED=0 avoids that and produces a genuinely static binary
// that runs on a `scratch` container, at the cost of the pure-Go DNS resolver
// rather than the system one, and of os/user losing its cgo-backed lookups.

// BuildInfo is what the running binary knows about how it was built.
type BuildInfo struct {
	GOOS      string
	GOARCH    string
	GoVersion string
	Compiler  string
	NumCPU    int
	Debug     bool
}

// CurrentBuild reports it. These are runtime CONSTANTS for GOOS and GOARCH:
// the compiler knows them, so `if runtime.GOOS == "windows"` is folded away in
// a non-Windows build exactly like a build tag would be.
//
// That makes runtime.GOOS a reasonable choice for a one-line difference, and
// build tags the right choice when the difference is a whole file or needs
// platform-specific imports.
func CurrentBuild() BuildInfo {
	return BuildInfo{
		GOOS:      runtime.GOOS,
		GOARCH:    runtime.GOARCH,
		GoVersion: runtime.Version(),
		Compiler:  runtime.Compiler,
		NumCPU:    runtime.NumCPU(),
		Debug:     DebugEnabled,
	}
}

// commonTargets is the set most projects actually ship.
func commonTargets() [][2]string {
	return [][2]string{
		{"linux", "amd64"},
		{"linux", "arm64"},
		{"darwin", "amd64"},
		{"darwin", "arm64"},
		{"windows", "amd64"},
		{"js", "wasm"},
	}
}

// cgoTradeoffs is the part people discover at deploy time.
func cgoTradeoffs() []string {
	return []string{
		"CGO_ENABLED=1 (default when a C toolchain is present): cross-compiling needs a C cross-compiler",
		"CGO_ENABLED=0: a fully static binary, runs on scratch and distroless images",
		"CGO_ENABLED=0 switches net to the pure-Go DNS resolver, which ignores some /etc/nsswitch.conf setups",
		"CGO_ENABLED=0 also loses os/user's cgo lookups, falling back to parsing /etc/passwd",
		"any C binding (sqlite3, some crypto) requires cgo and cannot be disabled",
	}
}

// buildFlags are the ones worth knowing for a release build.
func buildFlags() map[string]string {
	return map[string]string{
		"-ldflags=-s -w":        "strip the symbol table and DWARF info: a noticeably smaller binary",
		"-ldflags=-X main.ver=": "set a string variable at link time, for version stamping",
		"-trimpath":             "remove local filesystem paths, for reproducible builds",
		"-tags":                 "enable custom build tags",
		"-race":                 "the race detector; not cross-compilable and ~10x slower",
	}
}

// demoCrossCompile prints build information.
func demoCrossCompile() {
	info := CurrentBuild()
	fmt.Printf("  this binary:\n")
	fmt.Printf("    GOOS/GOARCH: %s/%s\n", info.GOOS, info.GOARCH)
	fmt.Printf("    Go version:  %s (%s compiler)\n", info.GoVersion, info.Compiler)
	fmt.Printf("    NumCPU:      %d\n", info.NumCPU)
	fmt.Printf("    debug build: %t\n", info.Debug)

	fmt.Println("\n  common targets:")
	for _, t := range commonTargets() {
		fmt.Printf("    GOOS=%-8s GOARCH=%-6s go build\n", t[0], t[1])
	}

	fmt.Println("\n  the cgo trade:")
	for _, s := range cgoTradeoffs() {
		fmt.Printf("    %s\n", s)
	}

	fmt.Println("\n  build flags worth knowing:")
	for _, k := range []string{"-ldflags=-s -w", "-ldflags=-X main.ver=", "-trimpath", "-tags", "-race"} {
		fmt.Printf("    %-22s %s\n", k, buildFlags()[k])
	}
}

// sprint is a tiny helper so debug_test.go can render a panic value without
// importing fmt into a file that is otherwise about build tags.
func sprint(v any) string { return fmt.Sprint(v) }
