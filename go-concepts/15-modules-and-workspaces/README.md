# Modules and workspaces

> 📚 [go-concepts](../README.md) · **Step 15 of 18** · [⬅ 14-embed-and-build-tags](../14-embed-and-build-tags/) · Next: [16-race-detector](../16-race-detector/) ➡

## What is this?

How Go decides which version of which dependency you get, and how several
modules in one repository work together.

This repo is the example. It is a **workspace** of independent modules, one per
area, which is the direct analogue of the Python repo's per-folder
`requirements.txt`: someone working through the language lessons should not
download pgx and Redis clients to do so.

## `go.mod`

```
module github.com/alexvervloet/learn-go/go-concepts

go 1.27

require (
    github.com/google/uuid v1.6.0
    golang.org/x/sync v0.8.0 // indirect
)
```

| Directive | What it does |
|---|---|
| `module` | This module's import path. Must match where it is fetched from. |
| `go` | The **language version**, not the toolchain. `go 1.27` enables 1.27 semantics. |
| `toolchain` | The minimum toolchain to build it. Added automatically when needed. |
| `require` | A dependency and its minimum version. |
| `// indirect` | Required by a dependency, not imported directly by you. |
| `replace` | Substitute a module, usually a local path during development. |
| `exclude` | Refuse a specific version, forcing selection elsewhere. |
| `retract` | *You* declaring one of *your own* published versions unusable. |

The `go` directive is a language version and gates real behaviour. Lesson 06's
per-iteration loop variables only apply to a module declaring `go 1.22` or
later, which is a rare case of a language change gated on a file's contents.

## Semantic import versioning

Go's answer to breaking changes is unusual and worth understanding: **v2 and
later go in the import path.**

```
github.com/user/lib         v0 and v1
github.com/user/lib/v2      v2
github.com/user/lib/v3      v3
```

So v1 and v3 of the same library are, to the compiler, different packages that
can coexist in one build. A transitive dependency stuck on v1 does not block
you from using v3.

v0 is explicitly unstable: no compatibility promise, and every v0 release may
break. A library that never reaches v1 is telling you something.

## Minimal version selection

Most package managers resolve to the *newest* version satisfying the
constraints. Go picks the **oldest version that satisfies everyone**.

```
your module requires  lib v1.2.0
dependency A requires lib v1.4.0
dependency B requires lib v1.3.0

selected: v1.4.0    the maximum of the minimums, not the newest release
```

Adding a dependency cannot silently upgrade anything else. The build is
reproducible without a lock file, because `go.mod` *is* the lock file, and
`go build` never changes it. Upgrades happen only when you ask:

```bash
go get -u ./...          # upgrade everything
go get lib@v1.5.0        # one module, one version
go get -u=patch ./...    # patch releases only
```

## `go.sum` is not a lock file

`go.mod` records versions; `go.sum` records **cryptographic hashes** of the
content at those versions. It exists to detect a module whose content changed
under a tag it already had, which is a supply-chain attack rather than a version
question.

By default the toolchain also checks the **checksum database** at
`sum.golang.org`, a transparency log. Private modules must be excluded with
`GOPRIVATE` or `GONOSUMDB`, or the fetch leaks your internal module paths to a
public service:

```bash
export GOPRIVATE=github.com/mycompany/*
```

Commit `go.sum`. Never edit it by hand.

## `go mod tidy`

Adds what the code imports, removes what it does not, and updates `go.sum`. Run
it after changing imports; CI should check it produces no diff:

```bash
go mod tidy -diff    # non-zero exit if tidy would change anything (Go 1.23+)
```

Before 1.23 this needed a copy-run-diff dance, and a stale `go.mod` merged
quietly.

## Workspaces

A `go.work` at the root makes several modules resolve against each other
locally, with no `replace` directives in any `go.mod`:

```
go 1.27

use (
    ./go-concepts
    ./dsa
    ./backends/learning/http-tutorial
)
```

Before workspaces, developing two modules together meant a `replace` in
`go.mod`, which is a local path that must be removed before publishing and
which someone always forgets. `go.work` is not published, so the problem cannot
happen.

Two things to know:

**`./...` does not work from a workspace root.** The root is not a module, so
the pattern matches nothing and `go` reports an error. Ask the workspace for its
modules instead:

```bash
go build $(go list -m -f '{{.Dir}}/...')
```

This repo's `Makefile` does exactly that, and [LESSONS.md](../../LESSONS.md) has
the story.

**`go.work` overrides `go.mod` for local development and nothing else.** A CI
job that builds one module in isolation does not see it, which is the point:
the workspace is a convenience, and the modules must stand alone.

## Vendoring

`go mod vendor` copies every dependency into `vendor/`, and the build then uses
it and ignores the network. Worth it when you need hermetic builds or your
network is untrusted. The cost is a large diff on every upgrade and a directory
of code that is not yours in every code review.

Most projects do not need it. The module proxy already provides availability,
and `go.sum` already provides integrity.

## What the files cover

| File | What it teaches |
|---|---|
| `gomod.go` | Parsing a real `go.mod`, every directive, semantic import versioning |
| `selection.go` | Minimal version selection, worked through, against the newest-wins alternative |
| `workspace.go` | Reading this repo's own `go.work`, the `./...` trap, isolation |
| `commands.go` | The commands worth knowing, and what each actually changes |
| `main.go` | Runs every demo in order |
| `*_test.go` | Tests, including MVS against a table of scenarios |

## How to run

```bash
go run ./15-modules-and-workspaces
go test ./15-modules-and-workspaces
```
