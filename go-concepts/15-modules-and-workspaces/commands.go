package main

import (
	"fmt"
	"sort"
)

// The commands worth knowing
// ==========================
//
// Grouped by what they actually CHANGE, because that is the distinction people
// get wrong: `go build` never edits go.mod, and `go get` always might.

// What a command changes. Named constants rather than repeated strings: the
// grouping below switches on them, and a typo in one would silently create a
// group of one.
const (
	modifiesNothing   = "nothing"
	modifiesModSum    = "go.mod and go.sum"
	modifiesWorkspace = "go.work"
	createsGoMod      = "creates go.mod"
	createsGoWork     = "creates go.work"
	createsVendor     = "creates vendor/"
	modifiesEveryMod  = "every module's go.mod"

	// go build is called out specifically, because "a build never edits
	// go.mod" is the part people get wrong.
	modifiesNothingOnBuild = "NOTHING: since Go 1.16 a build never edits go.mod"

	// tidy -diff reports rather than changes, which is what makes it the CI
	// check.
	modifiesNothingReports = "nothing: this is the CI check"
)

// Command describes one, and what it modifies.
type Command struct {
	Name     string
	Does     string
	Modifies string
}

// moduleCommands is the set worth having in your fingers.
func moduleCommands() []Command {
	return []Command{
		{
			Name:     "go mod init <path>",
			Does:     "create go.mod with the given module path",
			Modifies: createsGoMod,
		},
		{
			Name:     "go mod tidy",
			Does:     "add what the code imports, remove what it does not",
			Modifies: modifiesModSum,
		},
		{
			Name:     "go mod tidy -diff",
			Does:     "report what tidy WOULD change, exiting non-zero if anything (Go 1.23+)",
			Modifies: modifiesNothingReports,
		},
		{
			Name:     "go get <module>@<version>",
			Does:     "add or change one requirement",
			Modifies: modifiesModSum,
		},
		{
			Name:     "go get -u ./...",
			Does:     "upgrade every dependency to its latest minor or patch",
			Modifies: modifiesModSum,
		},
		{
			Name:     "go get -u=patch ./...",
			Does:     "upgrade to the latest PATCH only, which is the conservative option",
			Modifies: modifiesModSum,
		},
		{
			Name:     "go get <module>@none",
			Does:     "remove a requirement",
			Modifies: modifiesModSum,
		},
		{
			Name:     "go build ./...",
			Does:     "compile",
			Modifies: modifiesNothingOnBuild,
		},
		{
			Name:     "go mod verify",
			Does:     "check the downloaded modules against go.sum",
			Modifies: modifiesNothing,
		},
		{
			Name:     "go mod download",
			Does:     "populate the module cache without building",
			Modifies: modifiesNothing, // the Docker layer-caching step
		},
		{
			Name:     "go mod graph",
			Does:     "print the full module requirement graph",
			Modifies: modifiesNothing,
		},
		{
			Name:     "go mod why <module>",
			Does:     "explain why a module is in the build, as an import chain",
			Modifies: modifiesNothing,
		},
		{
			Name:     "go mod vendor",
			Does:     "copy every dependency into vendor/",
			Modifies: createsVendor,
		},
		{
			Name:     "go list -m all",
			Does:     "list every selected module version",
			Modifies: modifiesNothing,
		},
		{
			Name:     "go list -m -u all",
			Does:     "the same, marking which have newer versions available",
			Modifies: modifiesNothing,
		},
		{
			Name:     "go work init ./a ./b",
			Does:     "create go.work covering those modules",
			Modifies: createsGoWork,
		},
		{
			Name:     "go work use ./c",
			Does:     "add a module to the workspace",
			Modifies: modifiesWorkspace,
		},
		{
			Name:     "go work sync",
			Does:     "push the workspace's selected versions back into each go.mod",
			Modifies: modifiesEveryMod,
		},
	}
}

// environmentVariables that change what the toolchain fetches and trusts.
func environmentVariables() map[string]string {
	return map[string]string{
		"GOPROXY":     "where modules are fetched from; proxy.golang.org by default, 'direct' to bypass",
		"GOPRIVATE":   "patterns that skip BOTH the proxy and the checksum database",
		"GONOSUMDB":   "patterns that skip the checksum database only",
		"GOFLAGS":     "flags applied to every go command, e.g. -mod=readonly",
		"GOWORK":      "'off' ignores go.work; a path uses a specific one",
		"GOMODCACHE":  "where downloaded modules live; $GOPATH/pkg/mod by default",
		"CGO_ENABLED": "0 for a static binary and simple cross-compilation (lesson 14)",
	}
}

// ciChecks are the ones worth having, and why.
func ciChecks() []Command {
	return []Command{
		{
			Name:     "go mod tidy -diff",
			Does:     "fail if go.mod or go.sum is stale",
			Modifies: modifiesNothing, // before Go 1.23 this needed a copy-run-diff dance
		},
		{
			Name:     "go mod verify",
			Does:     "fail if a cached module's content does not match go.sum",
			Modifies: modifiesNothing,
		},
		{
			Name:     "go build with GOFLAGS=-mod=readonly",
			Does:     "fail rather than silently editing go.mod (the default since 1.16)",
			Modifies: modifiesNothing,
		},
		{
			Name:     "GOWORK=off go build ./... in each module",
			Does:     "prove each module builds without the workspace",
			Modifies: modifiesNothing,
		},
	}
}

// theGoSumMisunderstanding is worth stating plainly, because "go.sum is the
// lock file" is repeated constantly and is wrong.
func theGoSumMisunderstanding() []string {
	return []string{
		"go.mod records VERSIONS: it is the lock file",
		"go.sum records HASHES of the content at those versions",
		"go.sum exists to detect a module whose content changed under an existing tag",
		"that is a supply-chain attack, not a version question",
		"deleting go.sum does not unpin anything; it removes the integrity check",
		"commit both, and never edit go.sum by hand",
	}
}

// demoCommands prints the command reference.
func demoCommands() {
	fmt.Println("  commands, grouped by what they change:")

	byModification := make(map[string][]Command)
	for _, c := range moduleCommands() {
		var key string

		switch c.Modifies {
		case modifiesNothing, modifiesNothingReports, modifiesNothingOnBuild:
			key = "reads only"
		case modifiesWorkspace, createsGoWork, modifiesEveryMod:
			key = "workspace"
		case createsGoMod, createsVendor:
			key = "creates a file"
		default:
			key = "modifies go.mod / go.sum"
		}

		byModification[key] = append(byModification[key], c)
	}

	groups := make([]string, 0, len(byModification))
	for g := range byModification {
		groups = append(groups, g)
	}
	sort.Strings(groups)

	for _, g := range groups {
		fmt.Printf("\n    %s:\n", g)
		for _, c := range byModification[g] {
			fmt.Printf("      %-26s %s\n", c.Name, c.Does)
		}
	}

	fmt.Println("\n  environment variables:")
	env := environmentVariables()
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Printf("    %-12s %s\n", k, env[k])
	}

	fmt.Println("\n  CI checks worth having:")
	for _, c := range ciChecks() {
		fmt.Printf("    %-42s %s\n", c.Name, c.Does)
	}

	fmt.Println("\n  go.sum is not the lock file:")
	for _, s := range theGoSumMisunderstanding() {
		fmt.Printf("    %s\n", s)
	}
}
