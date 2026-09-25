# go:embed and build tags

> 📚 [go-concepts](../README.md) · **Step 14 of 18** · [⬅ 13-io-composition](../13-io-composition/) · Next: [15-modules-and-workspaces](../15-modules-and-workspaces/) ➡

## What is this?

Two features that decide what ends up in your binary, and which code the
compiler even looks at.

**`go:embed`** puts files inside the binary at compile time. A Go service can
ship its templates, migrations, static assets and default config as one file
with nothing to deploy alongside it.

**Build tags** exclude whole files from a build. This is how one codebase
supports Linux and Windows, or compiles a debug build with extra checks that
cost nothing in production because the code is not there.

Together they are why `scp ./myservice server:` is a complete deployment. Go
cross-compiles to a static binary with its assets inside, which is a genuinely
different operational story from shipping a Python service with its interpreter,
virtualenv and file tree.

## `go:embed`

```go
import _ "embed"          // required even when you never call anything

//go:embed assets/logo.svg
var logo []byte

//go:embed assets/version.txt
var version string

//go:embed assets
var assets embed.FS
```

The directive binds to the **next variable declaration**, and the rules are
strict enough to be worth listing:

- The comment must be `//go:embed`, with **no space** after `//`. A space makes
  it an ordinary comment and the variable stays empty. This is the mistake
  everyone makes once, and it is worth knowing exactly what does and does not
  catch it:

  | | Result |
  |---|---|
  | `go build` | compiles fine |
  | `go vet` | silent |
  | `golangci-lint` | **catches it**: staticcheck `SA9009`, "ineffectual compiler directive due to extraneous space" |
  | at runtime | the variable is `""` |

  So the linter is the real defence. A test asserting the embedded content is
  non-empty is worth having as well, and `embedding_test.go` has one for every
  variable in the lesson.
- The variable must be at **package scope**. Inside a function it does not
  compile.
- The type must be `string`, `[]byte`, or `embed.FS`. Nothing else.
- You must import `embed`, even if the only embedded type is `string`. Use
  `import _ "embed"` when you never reference the package.
- Paths are **relative to the source file** and cannot escape it: no `..`, no
  absolute paths, no symlinks.
- Embedding a directory **skips files starting with `.` or `_`** unless the
  pattern says `all:`.

That last one is a real trap: `//go:embed templates` silently omits
`templates/_partial.html`, and `//go:embed all:templates` includes it.

## `embed.FS` is an `fs.FS`

Which is the reason it composes with everything:

```go
//go:embed assets
var assets embed.FS

http.Handle("/static/", http.FileServerFS(assets))
template.ParseFS(assets, "assets/templates/*.html")
fs.WalkDir(assets, ".", func(path string, d fs.DirEntry, err error) error { ... })
```

`fs.Sub` strips a prefix, which is how you serve `assets/static/app.css` at
`/static/app.css` without the path appearing twice.

An `embed.FS` is read-only, safe for concurrent use, and its contents are in the
binary's read-only data section, so embedding a 50MB file makes a 50MB binary.

## Build tags

The modern syntax is `//go:build`, and it must appear **before the package
clause** with a blank line after it:

```go
//go:build linux && amd64

package main
```

The old `// +build` form still works and `gofmt` keeps the two in sync, but new
code uses `//go:build` only.

The expression takes `&&`, `||`, `!` and parentheses, over:

| Kind | Examples |
|---|---|
| OS | `linux`, `darwin`, `windows`, `js`, `wasip1` |
| Architecture | `amd64`, `arm64`, `386`, `wasm` |
| Go version | `go1.24` means "1.24 or later" |
| Toolchain | `race`, `cgo`, `unix` (any Unix-like OS) |
| Custom | anything, enabled with `-tags mytag` |

**Filename suffixes** do the same thing with no comment at all. A file named
`store_linux.go` builds only on Linux; `store_windows_amd64.go` only on Windows
on amd64. `_test.go` is the one everybody already knows.

The two mechanisms combine, and the filename wins: a file called `x_linux.go`
cannot be built on Windows whatever its `//go:build` line says.

## What build tags are actually for

**Platform code.** A file per OS implementing one interface, and no `runtime.GOOS`
switch anywhere.

**Optional dependencies.** Keep a heavyweight driver out of the default build
and let users opt in with `-tags`.

**Integration tests.** `//go:build integration` on tests that need a database,
so `go test ./...` stays fast and CI runs `-tags integration` separately.

**Debug builds.** Expensive assertions that compile to nothing in production.

The trap: code behind a tag is **not compiled by default**, so it is not type
checked, not vetted and not covered. A file behind `//go:build integration` can
be broken for months. Build every tag combination in CI, or the tag becomes a
place where code goes to rot.

### Constraints must be exhaustive

This lesson started with two platform files, `//go:build unix` and
`//go:build windows`, which looks like complete coverage and is not. Go builds
for targets that are neither, and `GOOS=js GOARCH=wasm go build` failed with:

```
platform.go:51:32: undefined: currentPlatform
```

A confusing message, because the declaration exists in two files and the
compiler is looking at neither. `platform_other.go` is the fallback, carrying
`//go:build !unix && !windows`.

Nothing warns you that a constraint set is incomplete. Only building for the
uncovered target does. Either provide a fallback, or make the gap fail loudly:

```go
//go:build !unix && !windows

package main

func init() { panic("unsupported platform: " + runtime.GOOS) }
```

With the fallback in place, all seven targets build from one macOS machine:

| Target | Binary |
|---|---|
| linux/amd64 | 14.3 MB |
| linux/arm64 | 13.3 MB |
| darwin/arm64 | 13.7 MB |
| windows/amd64 | 14.5 MB |
| js/wasm | 18.9 MB |
| wasip1/wasm | 18.8 MB |
| plan9/amd64 | 10.2 MB |

`-ldflags="-s -w"` takes the darwin build from 13.7 MB to 9.4 MB by stripping
the symbol table and DWARF info, at the cost of readable stack traces from a
core dump.

## Cross-compilation

```bash
GOOS=linux GOARCH=amd64 go build -o app-linux-amd64
GOOS=darwin GOARCH=arm64 go build -o app-darwin-arm64
GOOS=windows GOARCH=amd64 go build -o app.exe
```

No toolchain to install, no container. `go tool dist list` prints every
supported pair, and there are over forty.

The caveat is **cgo**. Anything using cgo (the standard `net` and `os/user`
packages on some platforms, and every C library binding) needs a C
cross-compiler. `CGO_ENABLED=0` avoids it, and gives a genuinely static binary
that runs on a scratch container, at the cost of the pure-Go DNS resolver rather
than the system one.

## What the files cover

| File | What it teaches |
|---|---|
| `embedding.go` | `string`, `[]byte` and `embed.FS`, the rules, the no-space trap |
| `embedfs.go` | `fs.FS` composition, `fs.Sub`, walking, templates, HTTP serving |
| `platform.go` | The interface every platform file implements |
| `platform_unix.go` | The `unix` build-tag implementation |
| `platform_windows.go` | The Windows implementation |
| `platform_other.go` | The fallback, and why exhaustiveness matters |
| `debug_on.go` / `debug_off.go` | A custom tag: assertions that vanish in production |
| `crosscompile.go` | `GOOS`/`GOARCH`, `runtime` constants, the cgo caveat |
| `main.go` | Runs every demo in order |
| `*_test.go` | Tests, including one that checks the embedded files exist |

## How to run

```bash
go run ./14-embed-and-build-tags
go test ./14-embed-and-build-tags

# With the debug tag, which compiles in the assertions
go run -tags debug ./14-embed-and-build-tags
go test -tags debug ./14-embed-and-build-tags

# Cross-compile this lesson for three platforms
GOOS=linux   GOARCH=amd64 go build -o /tmp/demo-linux  ./14-embed-and-build-tags
GOOS=windows GOARCH=amd64 go build -o /tmp/demo.exe    ./14-embed-and-build-tags
GOOS=darwin  GOARCH=arm64 go build -o /tmp/demo-darwin ./14-embed-and-build-tags
```
