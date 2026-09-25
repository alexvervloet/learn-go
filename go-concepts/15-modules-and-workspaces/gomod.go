// Package main is lesson 15 of go-concepts: modules and workspaces.
//
// go.mod declares a module's identity, its language version, and the MINIMUM
// version of everything it depends on. go.sum records the cryptographic hash
// of the content at those versions.
//
// Together they make a build reproducible with no lock file, because go.mod IS
// the lock file: `go build` never changes it, and only an explicit `go get`
// does.
package main

import (
	_ "embed"
	"fmt"
	"sort"
	"strings"
)

// A realistic go.mod, embedded so the parser below has something to work on
// without reading outside the module.
//
// The file is named sample.go.mod rather than go.mod for a practical reason:
// a real go.mod anywhere in the tree would make that directory a module, and
// the toolchain would try to build it.
//
//go:embed examples/sample.go.mod
var sampleGoMod string

// Directive is one parsed line of a go.mod.
type Directive struct {
	Kind     string // module, go, toolchain, require, replace, exclude, retract
	Path     string
	Version  string
	Indirect bool
}

// parseGoMod is a deliberately small parser: enough to show the structure,
// nowhere near enough for real use.
//
// The real one is golang.org/x/mod/modfile, which handles comments, blocks,
// line continuations and the retract ranges this skips. Reach for that rather
// than this whenever the answer matters.
func parseGoMod(content string) (directives []Directive, err error) {
	var block string // the current require(...) / retract(...) block, if any

	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(raw)

		// Strip a trailing comment, but remember whether it said indirect.
		indirect := strings.Contains(line, "// indirect")
		if i := strings.Index(line, "//"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		if line == "" {
			continue
		}

		// Closing a block.
		if line == ")" {
			block = ""
			continue
		}

		// Opening a block: `require (`, `retract (`.
		if open := strings.TrimSuffix(line, " ("); open != line {
			block = open
			continue
		}

		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}

		// Inside a block, the directive kind is implicit.
		kind := block
		if kind == "" {
			kind = fields[0]
			fields = fields[1:]
		}

		d := Directive{Kind: kind, Indirect: indirect}
		switch {
		case len(fields) >= 2:
			d.Path, d.Version = fields[0], fields[1]
		case len(fields) == 1:
			d.Path = fields[0]
		default:
			continue
		}

		// `replace a => b` puts the target in the version slot; keep it
		// readable rather than pretending it is a version.
		if d.Kind == "replace" && len(fields) >= 3 {
			d.Version = strings.Join(fields[1:], " ")
		}

		directives = append(directives, d)
	}

	if len(directives) == 0 {
		return nil, fmt.Errorf("parse go.mod: no directives found")
	}
	return directives, nil
}

// directivesByKind groups the parsed result, which is how the demo prints it.
func directivesByKind(directives []Directive) map[string][]Directive {
	out := make(map[string][]Directive)
	for _, d := range directives {
		out[d.Kind] = append(out[d.Kind], d)
	}
	return out
}

// Semantic import versioning
// --------------------------
//
// Go's answer to breaking changes is unusual: v2 and later go IN THE IMPORT
// PATH.
//
//	github.com/user/lib       v0 and v1
//	github.com/user/lib/v2    v2
//	github.com/user/lib/v3    v3
//
// So v1 and v3 are different packages to the compiler and can coexist in one
// build. A transitive dependency stuck on v1 does not block you from using v3,
// which is the problem this solves and which most ecosystems do not.

// MajorVersion extracts the major version implied by an import path.
//
// Two conventions, because gopkg.in predates modules and never changed:
//
//	github.com/jackc/pgx/v5    the module convention: a /vN path element
//	gopkg.in/yaml.v3           gopkg.in's convention: a .vN SUFFIX
//
// The gopkg.in form is worth handling rather than treating as v1: it is still
// one of the most-imported hosts in Go, and a function that quietly reports
// yaml.v3 as v1 is wrong in a way nobody notices.
func MajorVersion(importPath string) int {
	// gopkg.in/pkg.vN and gopkg.in/user/pkg.vN
	if strings.HasPrefix(importPath, "gopkg.in/") {
		if i := strings.LastIndex(importPath, ".v"); i >= 0 {
			if major, ok := parseMajor(importPath[i+2:]); ok {
				return major
			}
		}
		return 1
	}

	// The module convention: a trailing /vN path element.
	parts := strings.Split(importPath, "/")
	last := parts[len(parts)-1]

	if !strings.HasPrefix(last, "v") || len(last) < 2 {
		return 1 // no suffix means v0 or v1
	}

	major, ok := parseMajor(last[1:])
	if !ok {
		return 1 // not a version suffix, just a package called something like "vm"
	}
	return major
}

// parseMajor reads a bare major-version number, reporting whether it was one.
func parseMajor(s string) (major int, ok bool) {
	if s == "" {
		return 0, false
	}

	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
		major = major*10 + int(c-'0')
	}

	if major < 2 {
		return 1, true
	}
	return major, true
}

// ImportPathFor returns the import path a given major version needs.
func ImportPathFor(basePath string, major int) string {
	if major < 2 {
		return basePath
	}
	return fmt.Sprintf("%s/v%d", basePath, major)
}

// canCoexist reports whether two import paths can appear in the same build.
// Two majors of the same library can; the same path twice cannot.
func canCoexist(a, b string) bool { return a != b }

// goDirectiveGatesLanguageFeatures documents the part people miss: the `go`
// line is a LANGUAGE version, and it changes how existing code compiles.
func goDirectiveGatesLanguageFeatures() map[string]string {
	return map[string]string{
		"go 1.21": "the built-in min, max and clear; log/slog in the stdlib",
		"go 1.22": "PER-ITERATION loop variables (lesson 06) and range-over-int",
		"go 1.23": "range-over-function iterators (lesson 11); timers become collectable",
		"go 1.24": "generic type aliases; the omitzero JSON tag (lesson 12)",
		"go 1.25": "sync.WaitGroup.Go (lesson 09); container-aware GOMAXPROCS",
	}
}

// demoGoMod prints a parsed go.mod and versioning rules.
func demoGoMod() {
	directives, err := parseGoMod(sampleGoMod)
	if err != nil {
		fmt.Printf("  parse failed: %v\n", err)
		return
	}

	byKind := directivesByKind(directives)

	kinds := make([]string, 0, len(byKind))
	for k := range byKind {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)

	fmt.Printf("  a realistic go.mod, parsed (%d directives):\n", len(directives))
	for _, kind := range kinds {
		fmt.Printf("    %s:\n", kind)
		for _, d := range byKind[kind] {
			suffix := ""
			if d.Indirect {
				suffix = "  // indirect"
			}
			fmt.Printf("      %-42s %s%s\n", d.Path, d.Version, suffix)
		}
	}

	fmt.Println("\n  semantic import versioning:")
	for _, path := range []string{
		"github.com/google/uuid",
		"github.com/jackc/pgx/v5",
		"gopkg.in/yaml.v3",
		"gopkg.in/check.v1",
		"golang.org/x/sync",
		"github.com/spf13/viper",
	} {
		fmt.Printf("    %-32s major v%d\n", path, MajorVersion(path))
	}

	base := "github.com/user/lib"
	fmt.Printf("\n  the same library at three majors:\n")
	for _, major := range []int{1, 2, 3} {
		fmt.Printf("    v%d -> %s\n", major, ImportPathFor(base, major))
	}
	fmt.Printf("    v1 and v3 can coexist in one build: %t\n",
		canCoexist(ImportPathFor(base, 1), ImportPathFor(base, 3)))

	fmt.Println("\n  the go directive is a LANGUAGE version:")
	features := goDirectiveGatesLanguageFeatures()
	for _, v := range []string{"go 1.21", "go 1.22", "go 1.23", "go 1.24", "go 1.25"} {
		fmt.Printf("    %-8s %s\n", v, features[v])
	}
}
