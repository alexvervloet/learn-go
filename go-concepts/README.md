# Go concepts

> 📚 [Repository root](../README.md) · **Start here**

The part of this repo with no Python counterpart. Everything else mirrors
[learning-python-backends](https://github.com/alexvervloet/learning-python-backends)
module for module; this folder exists because the language underneath is
different in ways that do not survive translation.

If you arrive from Python knowing what a decorator, a context manager and an
`async def` are, the useful thing to know is which of those intuitions transfer:

| Python | Go | Transfers? |
|---|---|---|
| `None` | the zero value per type | No. There is no "unset". |
| `try` / `except` | `if err != nil` | No. Errors are return values you move by hand. |
| `asyncio` + `await` | goroutines + channels | Partly. No function colouring, no event loop, real parallelism. |
| duck typing | structural interfaces | Yes, and the compiler checks it. |
| `list` | slice | Mostly, until two slices share a backing array. |
| `dict` | map | Yes, minus ordering and `KeyError`. |
| decorators | higher-order functions, embedding | Partly. No syntax for it. |
| `with` | `defer` | Roughly, but `defer` is function-scoped, not block-scoped. |
| GIL | no GIL | No. Go runs goroutines in parallel, so data races are real. |

That last row is the one that costs people the most. In Python, the GIL makes
many sloppy concurrent programs accidentally correct. In Go they are races, and
a race is undefined behaviour rather than a slightly wrong number.

## The lessons

Work them in order. Each one assumes the ones before it.

| # | Lesson | Covers | Status |
|---|---|---|---|
| 01 | [types-and-zero-values](01-types-and-zero-values/) | Zero values, nil map vs nil slice, declarations, shadowing, `iota`, conversions | ✅ |
| 02 | [slices-and-maps](02-slices-and-maps/) | The slice header, the append aliasing trap, randomised map order, `slices`/`maps` | ✅ |
| 03 | [interfaces](03-interfaces/) | Structural typing, consumer-defined interfaces, the typed-nil trap, method sets, `io.Writer` | ✅ |
| 04 | [errors](04-errors/) | `%w` vs `%v`, sentinels, `errors.Is`/`As`, `errors.Join`, retry, deferred close | ✅ |
| 05 | [defer-panic-recover](05-defer-panic-recover/) | `defer` ordering, argument evaluation, `recover` placement, panic boundaries, fuzzing | ✅ |
| 06 | [goroutines](06-goroutines/) | Cost, the G-M-P scheduler, `GOMAXPROCS`, the three leak shapes, loop variables | ✅ |
| 07 | [channels](07-channels/) | Unbuffered handshake, closing rules, directional types, five patterns, six deadlocks | ✅ |
| 08 | [select-and-timeouts](08-select-and-timeouts/) | Random choice, `default`, timer leaks, nil cases, pipelines, worker pools | ✅ |
| 09 | [sync-primitives](09-sync-primitives/) | `Mutex`, `RWMutex`, `WaitGroup`, `Once`, atomics, `sync.Map`, `sync.Pool`, errgroup | ✅ |
| 10 | [context](10-context/) | Cancellation, deadlines, the tree, `Cause`, values, HTTP, the six mistakes | ✅ |
| 11 | [generics](11-generics/) | Type parameters, constraints and `~`, generic types, containers, `iter.Seq`, measured costs | ✅ |
| 12 | [struct-tags-and-reflection](12-struct-tags-and-reflection/) | Tag syntax, `omitempty` vs `omitzero`, `Type`/`Kind`, settability, a tag-driven validator | ✅ |
| 13 | [io-composition](13-io-composition/) | The Read/Write contracts, `io.Copy` fast paths, the combinators, `bufio`, `io.Pipe` | ✅ |
| 14 | [embed-and-build-tags](14-embed-and-build-tags/) | `go:embed` and its traps, `embed.FS` as `fs.FS`, platform files, custom tags, cross-compilation | ✅ |
| 15 | [modules-and-workspaces](15-modules-and-workspaces/) | `go.mod` directives, semantic import versioning, minimal version selection, `go.work` and its traps | ✅ |
| 16 | [race-detector](16-race-detector/) | Five race shapes and their fixes, safe code that looks racy, what `-race` cannot see, reading a report | ✅ |
| 17 | [benchmarks-and-pprof](17-benchmarks-and-pprof/) | `b.Loop`, the three ways a benchmark lies, allocation sources measured, pprof, `net/http/pprof` safely | ✅ |
| 18 | memory-and-escape-analysis | Stack vs heap, `-gcflags=-m`, GC tuning | ⬜ |

## How these are laid out

Go allows one package per directory, so a lesson cannot be a folder of
standalone scripts the way the Python repo's numbered files are. Each lesson is
one `package main` split across topic files:

```
04-errors/
  README.md         the why: prose, diagrams, the rules
  creating.go       one topic, with its demo function
  sentinels.go      another
  custom.go
  joining.go
  patterns.go
  main.go           runs every demo in order, with section headers
  *_test.go         one test file per topic file
```

So there are two ways to read a lesson, and they are worth using in this order:

```bash
go run ./04-errors     # watch it happen, with commentary
go test -v ./04-errors # see every claim in the README asserted
```

The tests are not an afterthought here. Where the README says something
surprising, a test pins it down, including the deliberately broken examples:
`TestTypedNilTrap` asserts that the bug still happens, so the lesson cannot
quietly stop being true.

## No dependencies

This module's `go.mod` has no `require` block and will not grow one.
Everything here is the standard library, so `go test ./...` works on a fresh
clone with no network access. `testify` shows up later, in
`backends/learning/testing-concepts/`, where comparing assertion styles is
part of the point.

## Running under `-race`

Several lessons demonstrate a data race deliberately, and the detector cannot
tell a teaching example from an accident. Rather than delete the examples or
drop `-race` from CI, this module defines `raceDetectorEnabled` from the `race`
build tag (see `09-sync-primitives/raceflag_*.go`), and the handful of tests
that exercise an intentional race skip themselves when the detector is on.

So `make test-race` is green and still meaningful. To see the reports the
examples produce, run them without the skip, or work through
[16-race-detector](16-race-detector/), which is about reading them.

## A note on the linter

Several lessons teach a bug by committing it. `golangci-lint` catches most of
them, which is genuinely the most useful thing in this folder: the typed-nil
trap, `%v` instead of `%w`, a value receiver on a mutating method, and a write
to a nil map are all found automatically. Those files carry narrow
`//nolint:` comments with a stated reason so the demo can stay broken.

In your own code, leave the linter on and delete the suppressions. See
[LESSONS.md](../LESSONS.md) for how often this came up while writing the folder.

## How to run

```bash
# From the repo root
make test DIR=./go-concepts/...
make lint DIR=./go-concepts/...

# Or directly
go test ./...
go run ./01-types-and-zero-values
```
