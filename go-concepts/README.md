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
| 09 | sync-primitives | `Mutex`, `RWMutex`, `WaitGroup`, `Once`, `atomic`, `errgroup` | ⬜ |
| 10 | context | Cancellation, deadlines, values, the propagation rules | ⬜ |
| 11 | generics | Type parameters, constraints, inference, when not to use them | ⬜ |
| 12 | struct-tags-and-reflection | JSON round-trips, tag parsing, the cost of `reflect` | ⬜ |
| 13 | io-composition | `Reader`/`Writer` composition, pipes, `io.Copy`'s fast paths | ⬜ |
| 14 | embed-and-build-tags | `go:embed`, build constraints, cross-compilation | ⬜ |
| 15 | modules-and-workspaces | Semantic import versioning, `go.work`, vendoring, MVS | ⬜ |
| 16 | race-detector | A real data race, caught and fixed | ⬜ |
| 17 | benchmarks-and-pprof | `testing.B`, `b.Loop`, allocation counts, CPU and heap profiles | ⬜ |
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
