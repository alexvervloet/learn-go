// Package main is lesson 14 of go-concepts: go:embed and build tags.
//
// The embed directive puts files into the binary at compile time:
//
//	//go:embed assets/version.txt
//	var version string
//
// The rules are strict, and each one is a mistake someone has spent an
// afternoon on:
//
//   - the comment is //go:embed with NO SPACE after //
//   - the variable must be at PACKAGE SCOPE
//   - the type must be string, []byte or embed.FS
//   - you must import "embed", even for a string (use `import _ "embed"`)
//   - paths are relative to this file and cannot escape it: no .., no absolute
//   - a directory pattern SKIPS files starting with . or _, unless prefixed all:
package main

import (
	"embed"
	"fmt"
	"strings"
)

// A string, for text you want as text. The trailing newline in the file is
// embedded too, which is why this is trimmed at the point of use rather than
// silently.
//
//go:embed assets/version.txt
var versionFile string

// A []byte, for binary content or anything you will write to a Writer.
//
//go:embed assets/static/app.css
var appCSS []byte

// An embed.FS, for a whole tree. This is an fs.FS, so it composes with
// html/template, net/http, io/fs and everything else that takes one.
//
//go:embed assets
var assetsFS embed.FS

// Several patterns on one directive, and several directives on one variable,
// both work. This is how you embed two unrelated trees into one FS.
//
//go:embed migrations
var migrationsFS embed.FS

// The all: prefix includes files that start with . or _, which the default
// pattern silently skips. assets/templates/_draft.html exists precisely to
// demonstrate the difference.
//
//go:embed all:assets/templates
var templatesWithHidden embed.FS

// And without it, for the comparison.
//
//go:embed assets/templates
var templatesWithoutHidden embed.FS

// theNoSpaceTrap is the mistake everyone makes once: writing the directive with
// a space after the slashes makes it an ordinary comment, and the variable
// stays empty. No error, no warning, no build failure.
//
// Measured on this exact case:
//
//	go build        compiles fine
//	go vet          silent
//	golangci-lint   CATCHES IT, as staticcheck SA9009:
//	                "ineffectual compiler directive due to extraneous space"
//	at runtime      the variable is ""
//
// So the linter is the defence, and it is a good one. A test asserting the
// content is non-empty is the belt to that pair of braces, and
// embedding_test.go has one for every variable in this file, because SA9009
// only catches the SPACE form. A directive attached to the wrong declaration,
// or a pattern matching nothing at all, is a build error rather than a silent
// empty, so between the three the case is covered.
func theNoSpaceTrap() []string {
	return []string{
		"//go:embed path   -> the directive. No space after the slashes.",
		"// go:embed path  -> an ordinary comment. The variable stays empty.",
		"go build and go vet both stay silent",
		"golangci-lint catches it: staticcheck SA9009, ineffectual compiler directive",
		"so: run staticcheck, and assert non-empty content in a test as well",
	}
}

// Version returns the embedded version, trimmed. Doing the trim here rather
// than expecting a file with no trailing newline is the robust choice: text
// editors add one and .gitattributes will not save you.
func Version() string { return strings.TrimSpace(versionFile) }

// StylesheetSize reports the embedded CSS length, standing in for any binary
// asset.
func StylesheetSize() int { return len(appCSS) }

// embeddedTypes reports what each embedded variable actually holds, which is
// the quickest way to see that the three supported types behave differently.
func embeddedTypes() (versionLen, cssLen int, fsEntries int, err error) {
	entries, err := assetsFS.ReadDir("assets")
	if err != nil {
		return 0, 0, 0, fmt.Errorf("read assets: %w", err)
	}

	return len(versionFile), len(appCSS), len(entries), nil
}

// hiddenFilesAreSkipped is the all: distinction, measured rather than claimed.
//
// assets/templates holds page.html, footer.html and _draft.html. The default
// pattern sees two of them.
func hiddenFilesAreSkipped() (withoutHidden, withHidden []string, err error) {
	list := func(fsys embed.FS) ([]string, error) {
		entries, rerr := fsys.ReadDir("assets/templates")
		if rerr != nil {
			return nil, fmt.Errorf("read templates: %w", rerr)
		}

		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		return names, nil
	}

	withoutHidden, err = list(templatesWithoutHidden)
	if err != nil {
		return nil, nil, err
	}

	withHidden, err = list(templatesWithHidden)
	if err != nil {
		return nil, nil, err
	}

	return withoutHidden, withHidden, nil
}

// Migrations returns every embedded migration in filename order, which is why
// they are numbered. Reading them from an embed.FS means the schema travels
// with the binary and a deploy cannot pick up the wrong version.
func Migrations() (names []string, err error) {
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return nil, fmt.Errorf("read migrations: %w", err)
	}

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		names = append(names, e.Name())
	}

	// ReadDir returns entries sorted by filename, which is exactly the
	// property the numbering relies on. Worth knowing rather than assuming:
	// fs.ReadDir documents it.
	return names, nil
}

// MigrationContent reads one migration.
func MigrationContent(name string) (string, error) {
	data, err := migrationsFS.ReadFile("migrations/" + name)
	if err != nil {
		return "", fmt.Errorf("read migration %s: %w", name, err)
	}
	return string(data), nil
}

// embedIsReadOnly documents what you cannot do. An embed.FS has no Write, no
// Create and no Remove: the contents live in the binary's read-only data
// section. It is also safe for concurrent use, with no locking needed.
func embedIsReadOnly() []string {
	return []string{
		"embed.FS is read-only: no Write, Create or Remove exist on it",
		"the contents are in the binary's read-only data section",
		"so it is safe for concurrent use with no locking",
		"and a 50MB embedded file makes a 50MB binary",
	}
}

// demoEmbedding prints what was embedded.
func demoEmbedding() {
	versionLen, cssLen, fsEntries, err := embeddedTypes()
	fmt.Printf("  embedded as a string: %d bytes, trimmed to %q\n", versionLen, Version())
	fmt.Printf("  embedded as []byte:   %d bytes of CSS\n", cssLen)
	fmt.Printf("  embedded as embed.FS: %d entries under assets/ (err=%v)\n", fsEntries, err)

	withoutHidden, withHidden, err := hiddenFilesAreSkipped()
	fmt.Printf("\n  //go:embed assets/templates      -> %v\n", withoutHidden)
	fmt.Printf("  //go:embed all:assets/templates  -> %v\n", withHidden)
	fmt.Printf("    (err=%v) the _ prefix is skipped without all:\n", err)

	names, err := Migrations()
	fmt.Printf("\n  embedded migrations (err=%v):\n", err)
	for _, n := range names {
		content, _ := MigrationContent(n)
		firstLine := strings.SplitN(strings.TrimSpace(content), "\n", 2)[0]
		fmt.Printf("    %-24s %d bytes, starts %q\n", n, len(content), firstLine)
	}

	fmt.Println("\n  the no-space trap:")
	for _, s := range theNoSpaceTrap() {
		fmt.Printf("    %s\n", s)
	}

	fmt.Println("\n  what an embed.FS is not:")
	for _, s := range embedIsReadOnly() {
		fmt.Printf("    %s\n", s)
	}
}
