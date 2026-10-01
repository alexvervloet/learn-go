package main

import (
	"os"
	"path/filepath"
	"testing"
)

// tree makes base/public/hello.txt and base/secret.txt, and returns public.
func tree(t *testing.T) string {
	t.Helper()

	base := t.TempDir()
	public := filepath.Join(base, "public")

	if err := os.Mkdir(public, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(public, "hello.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "secret.txt"), []byte("the secret"), 0o600); err != nil {
		t.Fatal(err)
	}

	return public
}

// TestDotDotEscapesAJoin is the classic bug, and the lexical check that fixes it.
func TestDotDotEscapesAJoin(t *testing.T) {
	public := tree(t)

	if b, err := readNaive(public, "../secret.txt"); err != nil || string(b) != "the secret" {
		t.Fatalf("readNaive(../secret.txt) = %q, %v; the join was expected to escape", b, err)
	}

	if _, err := readLexical(public, "../secret.txt"); err == nil {
		t.Error("the lexical check let ../ through")
	}

	if _, err := readRooted(public, "../secret.txt"); err == nil {
		t.Error("os.Root let ../ through")
	}

	for _, read := range []func(string, string) ([]byte, error){readNaive, readLexical, readRooted} {
		if b, err := read(public, "hello.txt"); err != nil || string(b) != "hello" {
			t.Errorf("a name inside the directory: got %q, %v", b, err)
		}
	}
}

// TestASymlinkBeatsTheLexicalCheck is why os.Root exists. "link" is a local name, so a string check passes
// it, and it points outside, so opening it reads the secret. Only a check made while resolving can see that.
func TestASymlinkBeatsTheLexicalCheck(t *testing.T) {
	public := tree(t)

	if err := os.Symlink(filepath.Join("..", "secret.txt"), filepath.Join(public, "link")); err != nil {
		t.Skipf("cannot create a symlink here: %v", err)
	}

	if !filepath.IsLocal("link") {
		t.Fatal("the premise: \"link\" is a local name as far as any string check can tell")
	}

	if b, err := readLexical(public, "link"); err != nil || string(b) != "the secret" {
		t.Fatalf("readLexical(link) = %q, %v; the symlink was expected to defeat the string check", b, err)
	}

	if b, err := readRooted(public, "link"); err == nil {
		t.Errorf("os.Root followed a symlink out of the root and read %q", b)
	}
}
