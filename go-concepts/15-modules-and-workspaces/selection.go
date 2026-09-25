package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Minimal version selection
// =========================
//
// Most package managers resolve to the NEWEST version satisfying the
// constraints. Go picks the OLDEST version that satisfies everyone: for each
// module, the maximum of the minimums anybody requires.
//
//	your module   requires lib v1.2.0
//	dependency A  requires lib v1.4.0
//	dependency B  requires lib v1.3.0
//	------------------------------------
//	selected               lib v1.4.0    not v1.9.9, the newest release
//
// Two consequences worth stating:
//
//	Adding a dependency cannot silently upgrade anything else.
//	The build is reproducible with no lock file, because go.mod is one.
//
// Upgrades happen only when asked: `go get -u`, `go get lib@v1.5.0`.

// Version is a parsed semantic version.
type Version struct {
	Major, Minor, Patch int
	Prerelease          string
}

// ParseVersion parses "v1.2.3" and "v1.2.3-rc1".
func ParseVersion(s string) (Version, error) {
	raw := strings.TrimPrefix(s, "v")

	var pre string
	if i := strings.IndexAny(raw, "-+"); i >= 0 {
		pre = raw[i+1:]
		raw = raw[:i]
	}

	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return Version{}, fmt.Errorf("parse version %q: want three dot-separated numbers", s)
	}

	nums := make([]int, 3)
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return Version{}, fmt.Errorf("parse version %q: %w", s, err)
		}
		if n < 0 {
			return Version{}, fmt.Errorf("parse version %q: negative component", s)
		}
		nums[i] = n
	}

	return Version{Major: nums[0], Minor: nums[1], Patch: nums[2], Prerelease: pre}, nil
}

// String renders it back.
func (v Version) String() string {
	s := fmt.Sprintf("v%d.%d.%d", v.Major, v.Minor, v.Patch)
	if v.Prerelease != "" {
		s += "-" + v.Prerelease
	}
	return s
}

// Compare orders two versions: -1, 0 or 1.
//
// A prerelease sorts BEFORE its release, so v1.2.0-rc1 < v1.2.0. That rule is
// why a prerelease is never selected by accident: nothing requiring v1.2.0
// will settle for v1.2.0-rc1.
func (v Version) Compare(other Version) int {
	for _, pair := range [][2]int{
		{v.Major, other.Major},
		{v.Minor, other.Minor},
		{v.Patch, other.Patch},
	} {
		switch {
		case pair[0] < pair[1]:
			return -1
		case pair[0] > pair[1]:
			return 1
		}
	}

	switch {
	case v.Prerelease == other.Prerelease:
		return 0
	case v.Prerelease == "":
		return 1 // a release beats its own prerelease
	case other.Prerelease == "":
		return -1
	default:
		return strings.Compare(v.Prerelease, other.Prerelease)
	}
}

// Requirement is one module requiring one version of another.
type Requirement struct {
	By      string // who requires it
	Module  string // what they require
	Version string
}

// SelectVersions implements MVS: for each module, the maximum of every
// required minimum.
//
// The real algorithm walks the whole module graph transitively and handles
// replace, exclude and major-version separation. This is the core rule, which
// is the part worth understanding.
func SelectVersions(requirements []Requirement) (selected map[string]string, err error) {
	best := make(map[string]Version)

	for _, r := range requirements {
		v, perr := ParseVersion(r.Version)
		if perr != nil {
			return nil, fmt.Errorf("%s requires %s: %w", r.By, r.Module, perr)
		}

		current, seen := best[r.Module]
		if !seen || v.Compare(current) > 0 {
			best[r.Module] = v
		}
	}

	selected = make(map[string]string, len(best))
	for module, v := range best {
		selected[module] = v.String()
	}
	return selected, nil
}

// newestWins is the alternative most ecosystems use, for the comparison. Given
// the list of every PUBLISHED version it picks the latest compatible one,
// which is why adding one dependency can move another.
func newestWins(available map[string][]string, requirements []Requirement) (map[string]string, error) {
	needed := make(map[string]struct{})
	for _, r := range requirements {
		needed[r.Module] = struct{}{}
	}

	selected := make(map[string]string, len(needed))
	for module := range needed {
		versions := available[module]
		if len(versions) == 0 {
			return nil, fmt.Errorf("no published versions for %s", module)
		}

		var best Version
		for _, raw := range versions {
			v, err := ParseVersion(raw)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", module, err)
			}
			if v.Compare(best) > 0 {
				best = v
			}
		}
		selected[module] = best.String()
	}

	return selected, nil
}

// upgradeScenario is the practical difference, stated as a scenario the demo
// and the tests both use.
func upgradeScenario() (requirements []Requirement, available map[string][]string) {
	requirements = []Requirement{
		{By: "your-module", Module: "lib", Version: "v1.2.0"},
		{By: "dependency-a", Module: "lib", Version: "v1.4.0"},
		{By: "dependency-b", Module: "lib", Version: "v1.3.0"},
		{By: "your-module", Module: "logger", Version: "v2.0.0"},
		{By: "dependency-a", Module: "logger", Version: "v2.1.0"},
	}

	available = map[string][]string{
		"lib":    {"v1.2.0", "v1.3.0", "v1.4.0", "v1.7.0", "v1.9.9"},
		"logger": {"v2.0.0", "v2.1.0", "v2.5.0"},
	}

	return requirements, available
}

// whyMVSMatters is the summary, because the rule sounds like a detail and
// changes how the ecosystem behaves.
func whyMVSMatters() []string {
	return []string{
		"adding a dependency cannot silently upgrade anything else",
		"the build is reproducible with no lock file: go.mod IS the lock file",
		"go build never edits go.mod; only an explicit go get does",
		"a new release of a dependency does not reach you until you ask for it",
		"the cost: you do not get bug fixes automatically either, so upgrade deliberately",
	}
}

// demoSelection prints MVS against the newest-wins alternative.
func demoSelection() {
	requirements, available := upgradeScenario()

	fmt.Println("  three modules requiring different minimums:")
	for _, r := range requirements {
		fmt.Printf("    %-14s requires %-8s %s\n", r.By, r.Module, r.Version)
	}

	mvs, err := SelectVersions(requirements)
	if err != nil {
		fmt.Printf("    selection failed: %v\n", err)
		return
	}

	newest, err := newestWins(available, requirements)
	if err != nil {
		fmt.Printf("    newest-wins failed: %v\n", err)
		return
	}

	modules := make([]string, 0, len(mvs))
	for m := range mvs {
		modules = append(modules, m)
	}
	sort.Strings(modules)

	fmt.Printf("\n    %-10s %-24s %s\n", "MODULE", "GO (minimal selection)", "NEWEST-WINS")
	for _, m := range modules {
		fmt.Printf("    %-10s %-24s %s\n", m, mvs[m], newest[m])
	}
	fmt.Println("    ...Go picks the maximum of the minimums, not the latest release")

	fmt.Println("\n  version ordering, including prereleases:")
	versions := []string{"v1.2.0", "v1.10.0", "v1.2.0-rc1", "v2.0.0", "v1.9.9"}
	parsed := make([]Version, 0, len(versions))
	for _, raw := range versions {
		v, perr := ParseVersion(raw)
		if perr != nil {
			continue
		}
		parsed = append(parsed, v)
	}
	sort.Slice(parsed, func(i, j int) bool { return parsed[i].Compare(parsed[j]) < 0 })

	rendered := make([]string, 0, len(parsed))
	for _, v := range parsed {
		rendered = append(rendered, v.String())
	}
	fmt.Printf("    %s\n", strings.Join(rendered, " < "))
	fmt.Println("    ...a prerelease sorts BEFORE its release, so it is never selected by accident")

	fmt.Println("\n  why it matters:")
	for _, s := range whyMVSMatters() {
		fmt.Printf("    %s\n", s)
	}
}
