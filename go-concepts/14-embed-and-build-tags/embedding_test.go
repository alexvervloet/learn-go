package main

import (
	"slices"
	"strings"
	"testing"
)

// TestEmbeddedContentIsNotEmpty is the most important test in this file, and
// the only defence against the no-space trap.
//
//	// go:embed assets/version.txt    <- a space: an ordinary comment
//	var broken string                    and broken stays ""
//
// Nothing reports that: not the compiler, not go vet, not golangci-lint. A
// test asserting non-empty content is what turns a silent production failure
// into a red build.
//
// Every embedded variable in this package is checked here. Adding one without
// adding a case is how the next person gets caught.
func TestEmbeddedContentIsNotEmpty(t *testing.T) {
	t.Run("version string", func(t *testing.T) {
		if versionFile == "" {
			t.Fatal("versionFile is empty — check the //go:embed directive has no space after //")
		}
		if Version() != "2.4.1" {
			t.Errorf("Version() = %q, want 2.4.1", Version())
		}
	})

	t.Run("css bytes", func(t *testing.T) {
		if len(appCSS) == 0 {
			t.Fatal("appCSS is empty — check the //go:embed directive")
		}
		if !strings.Contains(string(appCSS), "body") {
			t.Errorf("the embedded CSS does not look like CSS: %q", appCSS)
		}
	})

	t.Run("assets FS", func(t *testing.T) {
		entries, err := assetsFS.ReadDir("assets")
		if err != nil {
			t.Fatalf("assetsFS is not readable: %v", err)
		}
		if len(entries) == 0 {
			t.Fatal("assetsFS is empty")
		}
	})

	t.Run("migrations FS", func(t *testing.T) {
		names, err := Migrations()
		if err != nil {
			t.Fatalf("Migrations: %v", err)
		}
		if len(names) == 0 {
			t.Fatal("no migrations were embedded")
		}
	})
}

// TestVersionIsTrimmed: the file ends with a newline, as text files do, and
// the accessor must not leak it.
func TestVersionIsTrimmed(t *testing.T) {
	if strings.ContainsAny(Version(), "\r\n") {
		t.Errorf("Version() = %q, want it trimmed", Version())
	}
	if versionFile == Version() {
		t.Error("the raw embedded file should still contain its trailing newline")
	}
}

func TestEmbeddedTypes(t *testing.T) {
	versionLen, cssLen, fsEntries, err := embeddedTypes()
	if err != nil {
		t.Fatalf("embeddedTypes: %v", err)
	}

	if versionLen == 0 {
		t.Error("the version string is empty")
	}
	if cssLen == 0 {
		t.Error("the CSS bytes are empty")
	}
	// assets/ holds static/, templates/ and version.txt.
	if fsEntries != 3 {
		t.Errorf("assets/ has %d entries, want 3", fsEntries)
	}
}

// TestHiddenFilesAreSkipped is the all: distinction, which is the trap that
// silently omits a partial template.
func TestHiddenFilesAreSkipped(t *testing.T) {
	withoutHidden, withHidden, err := hiddenFilesAreSkipped()
	if err != nil {
		t.Fatalf("hiddenFilesAreSkipped: %v", err)
	}

	if slices.Contains(withoutHidden, "_draft.html") {
		t.Errorf("the default pattern should skip _draft.html, got %v", withoutHidden)
	}
	if !slices.Contains(withHidden, "_draft.html") {
		t.Errorf("all: should include _draft.html, got %v", withHidden)
	}
	if len(withHidden) != len(withoutHidden)+1 {
		t.Errorf("all: found %d files, default found %d; want exactly one more",
			len(withHidden), len(withoutHidden))
	}
}

func TestMigrations(t *testing.T) {
	names, err := Migrations()
	if err != nil {
		t.Fatalf("Migrations: %v", err)
	}

	want := []string{"001_create_users.sql", "002_add_created_at.sql"}
	if !slices.Equal(names, want) {
		t.Errorf("got %v, want %v", names, want)
	}

	// Ordering is the whole reason for the numeric prefixes, and fs.ReadDir
	// documents that it sorts by filename. Worth asserting rather than
	// assuming, because a migration runner depends on it.
	if !slices.IsSorted(names) {
		t.Errorf("migrations are not in filename order: %v", names)
	}
}

func TestMigrationContent(t *testing.T) {
	content, err := MigrationContent("001_create_users.sql")
	if err != nil {
		t.Fatalf("MigrationContent: %v", err)
	}

	if !strings.Contains(content, "CREATE TABLE users") {
		t.Errorf("content does not look like the migration: %q", content)
	}
}

func TestMigrationContentOnAMissingFile(t *testing.T) {
	_, err := MigrationContent("999_nonexistent.sql")

	if err == nil {
		t.Fatal("expected an error for a missing migration")
	}
	if !strings.Contains(err.Error(), "999_nonexistent.sql") {
		t.Errorf("err = %q, want it to name the file", err)
	}
}

func TestEmbedDocsArePresent(t *testing.T) {
	if got := theNoSpaceTrap(); len(got) < 3 {
		t.Errorf("expected at least 3 notes on the no-space trap, got %d", len(got))
	}
	if got := embedIsReadOnly(); len(got) < 3 {
		t.Errorf("expected at least 3 notes on read-only-ness, got %d", len(got))
	}
}
