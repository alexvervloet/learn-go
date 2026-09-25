package main

import (
	"fmt"
	"os"
)

// Build tags for platform code
// ============================
//
// The interface below has one implementation per platform, each in a file the
// compiler only looks at on that platform. There is no runtime.GOOS switch
// anywhere, and code for the wrong platform is not merely skipped at runtime:
// it is never compiled, so it cannot fail to build against a missing syscall.
//
// Two mechanisms, and they combine:
//
//	//go:build unix          a constraint comment, before the package clause
//	platform_windows.go      a FILENAME SUFFIX, with no comment needed
//
// The filename wins. A file called x_linux.go cannot build on Windows whatever
// its //go:build line says.
//
// The recognised suffixes are _GOOS, _GOARCH and _GOOS_GOARCH. So
// store_linux.go, store_amd64.go and store_linux_amd64.go are all constrained,
// and a file called linux.go is NOT, because the suffix must follow an
// underscore.

// Platform describes the host, with one implementation per operating system.
type Platform interface {
	// Name reports the platform family.
	Name() string

	// PathListSeparator is ':' on Unix and ';' on Windows, which is the
	// smallest difference that actually breaks programs.
	PathListSeparator() rune

	// LineEnding is "\n" or "\r\n".
	LineEnding() string

	// TempDirName is the conventional temporary directory.
	TempDirName() string

	// IsCaseSensitiveFS reports the usual default, which is a guess rather
	// than a fact: macOS ships case-insensitive by default and can be
	// formatted case-sensitive, and Linux filesystems vary too. Named
	// honestly so nobody builds security on it.
	IsCaseSensitiveFS() bool
}

// CurrentPlatform returns the implementation for this build. The variable is
// assigned in exactly one of the platform files, and the compiler sees exactly
// one of them, so there is no ambiguity and no switch.
var CurrentPlatform Platform = currentPlatform

// describePlatform renders the current platform's answers.
func describePlatform(p Platform) []string {
	return []string{
		fmt.Sprintf("name:                 %s", p.Name()),
		fmt.Sprintf("path list separator:  %q", string(p.PathListSeparator())),
		fmt.Sprintf("line ending:          %q", p.LineEnding()),
		fmt.Sprintf("temp directory:       %s", p.TempDirName()),
		fmt.Sprintf("case-sensitive FS:    %t (by default; not a guarantee)", p.IsCaseSensitiveFS()),
	}
}

// buildTagMechanisms documents the two ways, because people meet the filename
// one first and assume it is the only one.
func buildTagMechanisms() []string {
	return []string{
		"//go:build linux && amd64   a constraint comment, BEFORE the package clause",
		"store_linux.go              a filename suffix: _GOOS",
		"store_amd64.go              a filename suffix: _GOARCH",
		"store_linux_amd64.go        both",
		"linux.go                    NOT constrained: the suffix needs an underscore",
		"the filename wins: x_linux.go cannot build on Windows, whatever its //go:build says",
	}
}

// commonConstraints lists the terms available in a //go:build expression.
func commonConstraints() map[string]string {
	return map[string]string{
		"linux, darwin, windows": "operating system",
		"amd64, arm64, wasm":     "architecture",
		"unix":                   "any Unix-like OS, so one file instead of five",
		"cgo":                    "cgo is enabled for this build",
		"race":                   "built with -race (used by lesson 09)",
		"go1.24":                 "Go 1.24 OR LATER, not exactly 1.24",
		"ignore":                 "a conventional never-true tag, for files kept out of the build",
	}
}

// demoPlatform prints the platform-specific answers.
func demoPlatform() {
	fmt.Printf("  this binary was built for %s:\n", CurrentPlatform.Name())
	for _, line := range describePlatform(CurrentPlatform) {
		fmt.Printf("    %s\n", line)
	}

	fmt.Println("\n  the two mechanisms:")
	for _, s := range buildTagMechanisms() {
		fmt.Printf("    %s\n", s)
	}

	fmt.Println("\n  constraint terms:")
	for _, k := range []string{
		"linux, darwin, windows", "amd64, arm64, wasm", "unix", "cgo", "race", "go1.24", "ignore",
	} {
		fmt.Printf("    %-24s %s\n", k, commonConstraints()[k])
	}
}

// osPathListSeparator exposes the standard library's answer so platform_test.go
// can check the hand-written implementations against it without importing os
// itself.
func osPathListSeparator() rune { return os.PathListSeparator }
