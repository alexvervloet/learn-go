package main

import (
	"strings"
	"testing"
)

func TestSampleGoModIsEmbedded(t *testing.T) {
	if sampleGoMod == "" {
		t.Fatal("sampleGoMod is empty — check the //go:embed directive (lesson 14)")
	}
	if !strings.Contains(sampleGoMod, "module github.com/example/service/v2") {
		t.Errorf("the embedded file does not look like a go.mod")
	}
}

func TestParseGoMod(t *testing.T) {
	directives, err := parseGoMod(sampleGoMod)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	byKind := directivesByKind(directives)

	tests := []struct {
		kind      string
		wantCount int
	}{
		{"module", 1},
		{"go", 1},
		{"toolchain", 1},
		{"require", 7},
		{"replace", 1},
		{"exclude", 1},
		{"retract", 2},
	}

	for _, tt := range tests {
		t.Run(tt.kind, func(t *testing.T) {
			if got := len(byKind[tt.kind]); got != tt.wantCount {
				t.Errorf("%d %s directives, want %d: %v", got, tt.kind, tt.wantCount, byKind[tt.kind])
			}
		})
	}
}

// TestParseGoModTracksIndirect: the // indirect marker distinguishes what you
// import from what your dependencies do, and go mod tidy maintains it.
func TestParseGoModTracksIndirect(t *testing.T) {
	directives, err := parseGoMod(sampleGoMod)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	var direct, indirect int
	for _, d := range directives {
		if d.Kind != "require" {
			continue
		}
		if d.Indirect {
			indirect++
			continue
		}
		direct++
	}

	if direct != 3 {
		t.Errorf("%d direct requirements, want 3", direct)
	}
	if indirect != 4 {
		t.Errorf("%d indirect requirements, want 4", indirect)
	}
}

func TestParseGoModRejectsEmptyInput(t *testing.T) {
	for _, input := range []string{"", "   ", "// only a comment\n"} {
		if _, err := parseGoMod(input); err == nil {
			t.Errorf("parseGoMod(%q) should have failed", input)
		}
	}
}

// TestMajorVersion covers both conventions: the module /vN form and gopkg.in's
// .vN suffix. The second is easy to get wrong and silently reports v1.
func TestMajorVersion(t *testing.T) {
	tests := []struct {
		path string
		want int
	}{
		// The module convention.
		{"github.com/google/uuid", 1},
		{"github.com/jackc/pgx/v5", 5},
		{"github.com/user/lib/v2", 2},
		{"github.com/user/lib/v10", 10},
		{"golang.org/x/sync", 1},

		// gopkg.in's convention.
		{"gopkg.in/yaml.v3", 3},
		{"gopkg.in/check.v1", 1},
		{"gopkg.in/user/pkg.v2", 2},
		{"gopkg.in/nosuffix", 1},

		// Not version suffixes at all.
		{"github.com/user/vm", 1},
		{"github.com/user/v", 1},
		{"github.com/user/valid", 1},
		{"github.com/user/v1", 1},
		{"github.com/user/v0", 1},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := MajorVersion(tt.path); got != tt.want {
				t.Errorf("MajorVersion(%q) = %d, want %d", tt.path, got, tt.want)
			}
		})
	}
}

func TestImportPathFor(t *testing.T) {
	const base = "github.com/user/lib"

	tests := []struct {
		major int
		want  string
	}{
		{0, base},
		{1, base},
		{2, base + "/v2"},
		{7, base + "/v7"},
	}

	for _, tt := range tests {
		if got := ImportPathFor(base, tt.major); got != tt.want {
			t.Errorf("ImportPathFor(%q, %d) = %q, want %q", base, tt.major, got, tt.want)
		}
	}
}

// TestRoundTrip: a path built for a major version must report that major back.
func TestImportPathRoundTrip(t *testing.T) {
	const base = "github.com/user/lib"

	for major := 1; major <= 12; major++ {
		path := ImportPathFor(base, major)

		if got := MajorVersion(path); got != major {
			t.Errorf("ImportPathFor(%d) = %q, which reports major %d", major, path, got)
		}
	}
}

// TestMajorsCanCoexist is the property semantic import versioning exists for:
// two majors of one library are different packages and can both be in a build.
func TestMajorsCanCoexist(t *testing.T) {
	const base = "github.com/user/lib"

	v1 := ImportPathFor(base, 1)
	v3 := ImportPathFor(base, 3)

	if !canCoexist(v1, v3) {
		t.Error("v1 and v3 should be different import paths")
	}
	if canCoexist(v3, v3) {
		t.Error("the same path twice cannot coexist")
	}
}

func TestGoDirectiveDocsArePresent(t *testing.T) {
	features := goDirectiveGatesLanguageFeatures()

	if len(features) < 4 {
		t.Errorf("expected at least 4 documented versions, got %d", len(features))
	}
	// The one that matters most for this repo: lesson 06's loop variables.
	if !strings.Contains(features["go 1.22"], "loop variable") {
		t.Errorf("go 1.22 should mention loop variables, got %q", features["go 1.22"])
	}
}
