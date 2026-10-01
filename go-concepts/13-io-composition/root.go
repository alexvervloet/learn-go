package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Untrusted paths: os.Root
// ========================
//
// A server that serves files named by the request has to keep the name inside one directory. The classic
// mistake is filepath.Join(dir, name), and "../secret" walks straight out.
//
// The classic fix is a LEXICAL check: Clean the name, refuse ".." and absolute paths. filepath.IsLocal does
// exactly that, correctly. It is still not enough, because it looks at the string and not at the disk: a
// name like "link" is perfectly local, and if "link" is a symlink to ../secret, opening it reads the secret.
// No check on the string can see that.
//
// os.Root (Go 1.24) checks while it OPENS. Every component of the name is resolved inside the directory, a
// ".." that would climb out is refused, and so is a symlink whose target is outside. It is the same idea as
// openat2's RESOLVE_BENEATH on Linux, made portable.
//
// What it does not do, per its documentation: stop a path crossing into another mounted filesystem, or keep
// a name away from /proc or device files that live inside the root. It confines names to a tree; it is not a
// sandbox.

// readNaive is the classic bug: join and open.
func readNaive(dir, name string) ([]byte, error) {
	return os.ReadFile(filepath.Join(dir, name))
}

// errNotLocal is what the lexical check refuses with.
var errNotLocal = errors.New("path is not local")

// readLexical is the classic fix: refuse any name that is not local as a string.
func readLexical(dir, name string) ([]byte, error) {
	if !filepath.IsLocal(name) {
		return nil, fmt.Errorf("%w: %q", errNotLocal, name)
	}

	return os.ReadFile(filepath.Join(dir, name))
}

// readRooted resolves the name inside dir and cannot leave it, whatever the name or the symlinks say.
func readRooted(dir, name string) ([]byte, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}

	defer func() { _ = root.Close() }()

	return root.ReadFile(name)
}

// demoRoot builds a directory with a secret beside it and a symlink pointing at the secret, then tries all
// three readers.
func demoRoot() {
	base, err := os.MkdirTemp("", "root-demo")
	if err != nil {
		fmt.Println("  cannot make a temp dir:", err)
		return
	}

	defer func() { _ = os.RemoveAll(base) }()

	public := filepath.Join(base, "public")
	_ = os.Mkdir(public, 0o755)
	_ = os.WriteFile(filepath.Join(public, "hello.txt"), []byte("hello"), 0o600)
	_ = os.WriteFile(filepath.Join(base, "secret.txt"), []byte("the secret"), 0o600)

	symlinked := os.Symlink(filepath.Join("..", "secret.txt"), filepath.Join(public, "link")) == nil

	try := func(name string) {
		fmt.Printf("  %-16q", name)

		for _, r := range []struct {
			label string
			read  func(string, string) ([]byte, error)
		}{{"naive", readNaive}, {"lexical", readLexical}, {"os.Root", readRooted}} {
			if b, err := r.read(public, name); err != nil {
				fmt.Printf("  %s: refused", r.label)
			} else {
				fmt.Printf("  %s: %q", r.label, b)
			}
		}

		fmt.Println()
	}

	try("hello.txt")
	try("../secret.txt")

	if symlinked {
		try("link")
	} else {
		fmt.Println("  (no symlink on this system, so the case that beats the lexical check is skipped)")
	}
}
