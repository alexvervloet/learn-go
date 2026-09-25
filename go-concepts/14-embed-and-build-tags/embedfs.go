package main

import (
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sort"
	"strings"
)

// embed.FS is an fs.FS
// ====================
//
// Which is the whole reason it is useful rather than merely convenient.
// Everything in the standard library that reads a filesystem takes an fs.FS,
// so an embed.FS drops into all of it:
//
//	http.FileServerFS(assets)          serve it over HTTP
//	template.ParseFS(assets, "...")    parse templates from it
//	fs.WalkDir(assets, ".", fn)        walk it
//	fs.Sub(assets, "assets/static")    strip a prefix
//
// And because os.DirFS produces an fs.FS too, the same code can read from disk
// in development and from the binary in production, with one line changed.

// walkEmbedded lists every file in the embedded tree with its size, which is
// the quickest way to see what actually made it into the binary.
func walkEmbedded() (files []string, totalBytes int64, err error) {
	err = fs.WalkDir(assetsFS, ".", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}

		info, ierr := d.Info()
		if ierr != nil {
			return fmt.Errorf("stat %s: %w", path, ierr)
		}

		files = append(files, fmt.Sprintf("%s (%d bytes)", path, info.Size()))
		totalBytes += info.Size()
		return nil
	})

	if err != nil {
		return nil, 0, fmt.Errorf("walk: %w", err)
	}

	sort.Strings(files)
	return files, totalBytes, nil
}

// subStripsAPrefix is how you avoid serving assets/static/app.css at the URL
// /static/assets/static/app.css. fs.Sub returns a view rooted at the given
// directory.
func subStripsAPrefix() (beforeNames, afterNames []string, err error) {
	before, err := fs.ReadDir(assetsFS, "assets/static")
	if err != nil {
		return nil, nil, fmt.Errorf("read before: %w", err)
	}
	for _, e := range before {
		beforeNames = append(beforeNames, "assets/static/"+e.Name())
	}

	static, err := fs.Sub(assetsFS, "assets/static")
	if err != nil {
		return nil, nil, fmt.Errorf("sub: %w", err)
	}

	after, err := fs.ReadDir(static, ".")
	if err != nil {
		return nil, nil, fmt.Errorf("read after: %w", err)
	}
	for _, e := range after {
		afterNames = append(afterNames, e.Name())
	}

	return beforeNames, afterNames, nil
}

// PageData is what the embedded template renders.
type PageData struct {
	Title     string
	GoVersion string
	Platform  string
	Version   string
}

// renderEmbeddedTemplate parses templates straight out of the binary.
//
// ParseFS takes glob patterns, and a template referencing another (page.html
// uses footer.html's "footer" block) needs both parsed together, which is why
// the pattern is a glob rather than a single file.
func renderEmbeddedTemplate(title string) (string, error) {
	tmpl, err := template.ParseFS(assetsFS, "assets/templates/*.html")
	if err != nil {
		return "", fmt.Errorf("parse templates: %w", err)
	}

	data := PageData{
		Title:     title,
		GoVersion: runtime.Version(),
		Platform:  runtime.GOOS + "/" + runtime.GOARCH,
		Version:   Version(),
	}

	var sb strings.Builder
	if err := tmpl.ExecuteTemplate(&sb, "page.html", data); err != nil {
		return "", fmt.Errorf("execute: %w", err)
	}

	return sb.String(), nil
}

// serveEmbeddedAssets serves the embedded tree over HTTP and makes one request
// against it, with no filesystem involved at any point.
//
// http.FileServerFS (Go 1.22) takes an fs.FS directly. Before it, this needed
// http.FS(assets) to adapt one to an http.FileSystem.
func serveEmbeddedAssets(requestPath string) (status int, contentType string, body string, err error) {
	static, err := fs.Sub(assetsFS, "assets/static")
	if err != nil {
		return 0, "", "", fmt.Errorf("sub: %w", err)
	}

	mux := http.NewServeMux()
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServerFS(static)))

	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + requestPath) //nolint:noctx // a local test server, one request
	if err != nil {
		return 0, "", "", fmt.Errorf("get: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // read path

	data := make([]byte, 64)
	n, _ := resp.Body.Read(data)

	return resp.StatusCode, resp.Header.Get("Content-Type"), string(data[:n]), nil
}

// sameCodeForDiskOrBinary is the pattern worth taking away: a function that
// takes fs.FS reads from either, so development can use the disk (with live
// reloading) and production the binary, with one line changed at the call site.
func sameCodeForDiskOrBinary(fsys fs.FS, path string) (string, error) {
	data, err := fs.ReadFile(fsys, path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	return strings.TrimSpace(string(data)), nil
}

// demoEmbedFS prints fs.FS composition.
func demoEmbedFS() {
	files, total, err := walkEmbedded()
	fmt.Printf("  walking the embedded tree (%d bytes total, err=%v):\n", total, err)
	for _, f := range files {
		fmt.Printf("    %s\n", f)
	}

	before, after, err := subStripsAPrefix()
	fmt.Printf("\n  fs.Sub strips a prefix (err=%v):\n", err)
	fmt.Printf("    before: %v\n", before)
	fmt.Printf("    after:  %v\n", after)

	rendered, err := renderEmbeddedTemplate("Embedded page")
	fmt.Printf("\n  a template parsed from the binary (err=%v):\n", err)
	for _, line := range strings.Split(strings.TrimSpace(rendered), "\n") {
		fmt.Printf("    | %s\n", line)
	}

	status, contentType, body, err := serveEmbeddedAssets("/static/app.css")
	fmt.Printf("\n  GET /static/app.css -> %d %s (err=%v)\n", status, contentType, err)
	fmt.Printf("    %s...\n", strings.SplitN(body, "\n", 2)[0])

	status, _, _, _ = serveEmbeddedAssets("/static/missing.css")
	fmt.Printf("  GET /static/missing.css -> %d\n", status)

	fromBinary, err := sameCodeForDiskOrBinary(assetsFS, "assets/version.txt")
	fmt.Printf("\n  the same function reading from the embedded FS: %q (err=%v)\n", fromBinary, err)
}
