package main

import (
	"testing"
)

func TestParseVersion(t *testing.T) {
	tests := []struct {
		in      string
		want    Version
		wantErr bool
	}{
		{"v1.2.3", Version{Major: 1, Minor: 2, Patch: 3}, false},
		{"1.2.3", Version{Major: 1, Minor: 2, Patch: 3}, false},
		{"v0.0.0", Version{}, false},
		{"v10.20.30", Version{Major: 10, Minor: 20, Patch: 30}, false},
		{"v1.2.3-rc1", Version{Major: 1, Minor: 2, Patch: 3, Prerelease: "rc1"}, false},
		{"v1.2.3+build5", Version{Major: 1, Minor: 2, Patch: 3, Prerelease: "build5"}, false},

		{"v1.2", Version{}, true},
		{"v1.2.3.4", Version{}, true},
		{"", Version{}, true},
		{"vX.Y.Z", Version{}, true},
		{"v-1.2.3", Version{}, true},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ParseVersion(tt.in)

			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseVersion(%q) error = %v, wantErr %t", tt.in, err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestVersionString(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"v1.2.3", "v1.2.3"},
		{"1.2.3", "v1.2.3"},
		{"v1.2.3-rc1", "v1.2.3-rc1"},
	}

	for _, tt := range tests {
		v, err := ParseVersion(tt.in)
		if err != nil {
			t.Fatalf("parse %q: %v", tt.in, err)
		}
		if got := v.String(); got != tt.want {
			t.Errorf("String() = %q, want %q", got, tt.want)
		}
	}
}

func TestVersionCompare(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"v1.0.0", "v1.0.0", 0},
		{"v1.0.0", "v2.0.0", -1},
		{"v2.0.0", "v1.0.0", 1},
		{"v1.2.0", "v1.10.0", -1}, // numeric, not lexical
		{"v1.0.1", "v1.0.2", -1},
		{"v1.9.9", "v1.10.0", -1},

		// A prerelease sorts BEFORE its release, which is why one is never
		// selected by accident.
		{"v1.2.0-rc1", "v1.2.0", -1},
		{"v1.2.0", "v1.2.0-rc1", 1},
		{"v1.2.0-rc1", "v1.2.0-rc2", -1},
		{"v1.2.0-rc1", "v1.2.0-rc1", 0},
	}

	for _, tt := range tests {
		t.Run(tt.a+" vs "+tt.b, func(t *testing.T) {
			a, err := ParseVersion(tt.a)
			if err != nil {
				t.Fatalf("parse %q: %v", tt.a, err)
			}
			b, err := ParseVersion(tt.b)
			if err != nil {
				t.Fatalf("parse %q: %v", tt.b, err)
			}

			if got := a.Compare(b); got != tt.want {
				t.Errorf("Compare = %d, want %d", got, tt.want)
			}
			// Antisymmetry, which a hand-written comparator can easily lose.
			if got := b.Compare(a); got != -tt.want {
				t.Errorf("reversed Compare = %d, want %d", got, -tt.want)
			}
		})
	}
}

// TestSelectVersionsIsTheMaximumOfMinimums is the whole of MVS.
func TestSelectVersions(t *testing.T) {
	tests := []struct {
		name         string
		requirements []Requirement
		want         map[string]string
	}{
		{
			name: "the maximum of the minimums",
			requirements: []Requirement{
				{By: "you", Module: "lib", Version: "v1.2.0"},
				{By: "a", Module: "lib", Version: "v1.4.0"},
				{By: "b", Module: "lib", Version: "v1.3.0"},
			},
			want: map[string]string{"lib": "v1.4.0"},
		},
		{
			name: "one requirement",
			requirements: []Requirement{
				{By: "you", Module: "lib", Version: "v1.0.0"},
			},
			want: map[string]string{"lib": "v1.0.0"},
		},
		{
			name: "several modules",
			requirements: []Requirement{
				{By: "you", Module: "a", Version: "v1.0.0"},
				{By: "you", Module: "b", Version: "v2.0.0"},
				{By: "a", Module: "b", Version: "v2.3.0"},
			},
			want: map[string]string{"a": "v1.0.0", "b": "v2.3.0"},
		},
		{
			name: "a prerelease never beats its release",
			requirements: []Requirement{
				{By: "you", Module: "lib", Version: "v1.2.0"},
				{By: "a", Module: "lib", Version: "v1.2.0-rc1"},
			},
			want: map[string]string{"lib": "v1.2.0"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := SelectVersions(tt.requirements)
			if err != nil {
				t.Fatalf("select: %v", err)
			}

			if len(got) != len(tt.want) {
				t.Fatalf("selected %d modules, want %d: %v", len(got), len(tt.want), got)
			}
			for module, want := range tt.want {
				if got[module] != want {
					t.Errorf("%s = %s, want %s", module, got[module], want)
				}
			}
		})
	}
}

func TestSelectVersionsRejectsBadVersions(t *testing.T) {
	_, err := SelectVersions([]Requirement{
		{By: "you", Module: "lib", Version: "not-a-version"},
	})

	if err == nil {
		t.Fatal("expected an error")
	}
}

func TestSelectVersionsOnNoRequirements(t *testing.T) {
	got, err := SelectVersions(nil)
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want nothing", got)
	}
}

// TestMVSDiffersFromNewestWins is the comparison that makes the rule concrete.
func TestMVSDiffersFromNewestWins(t *testing.T) {
	requirements, available := upgradeScenario()

	mvs, err := SelectVersions(requirements)
	if err != nil {
		t.Fatalf("mvs: %v", err)
	}

	newest, err := newestWins(available, requirements)
	if err != nil {
		t.Fatalf("newest: %v", err)
	}

	if mvs["lib"] != "v1.4.0" {
		t.Errorf("MVS selected lib %s, want v1.4.0 (the maximum of the minimums)", mvs["lib"])
	}
	if newest["lib"] != "v1.9.9" {
		t.Errorf("newest-wins selected lib %s, want v1.9.9 (the latest published)", newest["lib"])
	}

	// They must actually differ, or the demonstration proves nothing.
	if mvs["lib"] == newest["lib"] {
		t.Error("the two strategies agreed, so the scenario does not illustrate the difference")
	}
}

func TestNewestWinsRejectsUnknownModules(t *testing.T) {
	_, err := newestWins(map[string][]string{}, []Requirement{
		{By: "you", Module: "unknown", Version: "v1.0.0"},
	})

	if err == nil {
		t.Fatal("expected an error for a module with no published versions")
	}
}

func TestMVSDocsArePresent(t *testing.T) {
	if got := whyMVSMatters(); len(got) < 4 {
		t.Errorf("expected at least 4 documented consequences, got %d", len(got))
	}
}
