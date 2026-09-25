package main

import (
	"io/fs"
	"net/http"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

func TestWalkEmbedded(t *testing.T) {
	files, total, err := walkEmbedded()
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	if len(files) != 4 {
		t.Errorf("walked %d files, want 4: %v", len(files), files)
	}
	if total == 0 {
		t.Error("total size is 0")
	}

	for _, want := range []string{"app.css", "page.html", "footer.html", "version.txt"} {
		found := slices.ContainsFunc(files, func(f string) bool {
			return strings.Contains(f, want)
		})
		if !found {
			t.Errorf("%q not found in %v", want, files)
		}
	}

	// _draft.html is under all:assets/templates, not under assets, so the walk
	// of assetsFS must not see it.
	if slices.ContainsFunc(files, func(f string) bool { return strings.Contains(f, "_draft") }) {
		t.Errorf("the walk should not include _draft.html: %v", files)
	}
}

// TestEmbedFSSatisfiesTestFS uses the standard library's own conformance
// checker, which is a stronger test than anything hand-written: fstest.TestFS
// verifies that an fs.FS behaves consistently across Open, ReadDir, Stat, Glob
// and sub-filesystems.
func TestEmbedFSSatisfiesTestFS(t *testing.T) {
	if err := fstest.TestFS(assetsFS,
		"assets/version.txt",
		"assets/static/app.css",
		"assets/templates/page.html",
		"assets/templates/footer.html",
	); err != nil {
		t.Errorf("assetsFS does not satisfy fs.FS: %v", err)
	}
}

func TestSubStripsAPrefix(t *testing.T) {
	before, after, err := subStripsAPrefix()
	if err != nil {
		t.Fatalf("sub: %v", err)
	}

	if !slices.Equal(before, []string{"assets/static/app.css"}) {
		t.Errorf("before = %v", before)
	}
	if !slices.Equal(after, []string{"app.css"}) {
		t.Errorf("after = %v, want the prefix stripped", after)
	}
}

func TestRenderEmbeddedTemplate(t *testing.T) {
	rendered, err := renderEmbeddedTemplate("Test page")
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	for _, want := range []string{"Test page", "<h1>", "Built with Go", "version 2.4.1"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered output missing %q:\n%s", want, rendered)
		}
	}
}

// TestTemplateUsesTheFooterPartial: page.html references footer.html's
// "footer" block, so both must be parsed together. A glob does that; naming
// one file would not.
func TestTemplateUsesTheFooterPartial(t *testing.T) {
	rendered, err := renderEmbeddedTemplate("x")
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	if !strings.Contains(rendered, "<footer>") {
		t.Errorf("the footer partial was not included:\n%s", rendered)
	}
}

func TestServeEmbeddedAssets(t *testing.T) {
	t.Run("an existing file", func(t *testing.T) {
		status, contentType, body, err := serveEmbeddedAssets("/static/app.css")
		if err != nil {
			t.Fatalf("serve: %v", err)
		}

		if status != http.StatusOK {
			t.Errorf("status = %d, want 200", status)
		}
		if !strings.Contains(contentType, "css") {
			t.Errorf("Content-Type = %q, want it to mention css", contentType)
		}
		if !strings.Contains(body, ":root") {
			t.Errorf("body = %q, want the CSS content", body)
		}
	})

	t.Run("a missing file", func(t *testing.T) {
		status, _, _, err := serveEmbeddedAssets("/static/missing.css")
		if err != nil {
			t.Fatalf("serve: %v", err)
		}
		if status != http.StatusNotFound {
			t.Errorf("status = %d, want 404", status)
		}
	})

	// fs.Sub stripped the prefix, so the un-stripped path must not resolve.
	t.Run("the un-stripped path is not served", func(t *testing.T) {
		status, _, _, err := serveEmbeddedAssets("/static/assets/static/app.css")
		if err != nil {
			t.Fatalf("serve: %v", err)
		}
		if status == http.StatusOK {
			t.Error("the doubled path should not resolve")
		}
	})
}

// TestSameCodeForDiskOrBinary is the pattern worth taking away: one function,
// two sources, chosen at the call site.
func TestSameCodeForDiskOrBinary(t *testing.T) {
	fromBinary, err := sameCodeForDiskOrBinary(assetsFS, "assets/version.txt")
	if err != nil {
		t.Fatalf("from binary: %v", err)
	}
	if fromBinary != "2.4.1" {
		t.Errorf("from binary = %q, want 2.4.1", fromBinary)
	}

	// The same function against an in-memory fs.FS, which is how you test code
	// that reads files without touching a disk at all.
	memory := fstest.MapFS{
		"assets/version.txt": &fstest.MapFile{Data: []byte("9.9.9\n")},
	}

	fromMemory, err := sameCodeForDiskOrBinary(memory, "assets/version.txt")
	if err != nil {
		t.Fatalf("from memory: %v", err)
	}
	if fromMemory != "9.9.9" {
		t.Errorf("from memory = %q, want 9.9.9", fromMemory)
	}
}

func TestSameCodeForDiskOrBinaryOnAMissingFile(t *testing.T) {
	_, err := sameCodeForDiskOrBinary(assetsFS, "assets/nope.txt")

	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "nope.txt") {
		t.Errorf("err = %q, want it to name the file", err)
	}
}

// TestEmbeddedFSIsReadOnly: there is no Write method, which is a compile-time
// property. This checks the runtime half: the FS does not satisfy any writable
// interface the standard library defines.
func TestEmbeddedFSIsReadOnly(t *testing.T) {
	var fsys fs.FS = assetsFS

	if _, ok := fsys.(interface {
		Create(string) (fs.File, error)
	}); ok {
		t.Error("embed.FS should not offer Create")
	}
}
