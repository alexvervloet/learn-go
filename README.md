# Learning Backend Engineering with Go

[![CI](https://github.com/alexvervloet/learn-go/actions/workflows/ci.yml/badge.svg)](https://github.com/alexvervloet/learn-go/actions/workflows/ci.yml)

A public, open learning resource for backend engineering with Go. It covers the
language itself, CS fundamentals, HTTP services, databases, auth, messaging and
AI integration, up to full capstone projects. Every module is concept-focused,
self-contained, and runnable. Clone it, work through it at your own pace, learn.

This is a companion to [learning-python-backends](https://github.com/alexvervloet/learning-python-backends),
which covers the same ground in Python. The two are deliberately parallel where
the concept is shared, and deliberately different where Go's design pushes the
answer somewhere else. Go's concurrency model, error handling, interfaces and
zero values have no Python equivalent, so `go-concepts/` exists to teach them
before anything else starts.

## Status

This repo is being built out module by module. The table below is honest about
what is finished and what is not. Anything marked ✅ builds, vets, lints and
tests clean on Go 1.27.

| Area | Status |
|---|---|
| [go-concepts/](go-concepts/) | 🟡 13 of 18 lessons |
| dsa/ | ⬜ not started |
| backends/learning/ | ⬜ not started |
| backends/ capstones | ⬜ not started |

## Setup

```bash
# Go 1.27 or newer
brew install go          # macOS
go version

# Developer tooling (golangci-lint)
make tools

# Everything CI runs
make check
```

No virtualenv, no `pip install`, no `requirements.txt`. `go test` downloads what
a module needs on first run and caches it globally.

### Dependencies are per-module, by design

There is **no single `go.mod` for the whole repo**. Each area is its own Go
module with its own dependencies, and a root [`go.work`](go.work) ties them into
one workspace so `make test` covers everything and an editor resolves imports
across module boundaries.

This mirrors the Python repo's per-folder `requirements.txt` rule, and for the
same reason: the capstone that needs pgx, Redis and asynq should not force those
downloads on someone working through the language lessons. It also means
`go-concepts/` and `dsa/` have **zero third-party dependencies**, so they run on
a fresh clone with no network.

Adding a module:

```bash
mkdir backends/learning/whatever && cd $_
go mod init github.com/alexvervloet/learn-go/backends/learning/whatever
cd ../../.. && go work use ./backends/learning/whatever
```

## Structure

| Folder | What's in it |
|---|---|
| [go-concepts/](go-concepts/) | The language itself: goroutines, channels, interfaces, errors, generics, context, and the rest of what Python has no analogue for |
| `dsa/` | Data structures, sorting, searching, P vs NP, and the interview-pattern families |
| `backends/learning/` | Concept-focused modules: HTTP, testing, databases, auth, caching, gRPC, GraphQL, jobs |
| `backends/` | Capstone projects that put it together |

## Suggested learning path

1. **[go-concepts/](go-concepts/)** — the language. Start here even if you know
   another language well, because Go's concurrency and error models are not
   transferable. 🟢
2. **dsa/** — CS fundamentals, rewritten with generics. 🟢
3. **backends/learning/http-tutorial/** — your first Go HTTP service. 🟢
4. **backends/learning/testing-concepts/** — table-driven tests, `httptest`, fuzzing. 🟢
5. **backends/learning/database-concepts/** — pgx, sqlc, migrations, transactions. 🐘
6. **backends/learning/backend-concepts/** — auth, caching, rate limiting, real-time. 🟢/🔴
7. **Specialized topics, as needed** — gRPC, GraphQL, jobs, AWS, Docker, CI.
8. **Capstones** — url-shortener first, then bookmark-manager. 🐘🔴

### What each module needs to run

| Icon | Meaning |
|---|---|
| 🟢 | No infrastructure — pure Go, SQLite, or in-memory |
| 🐘 | PostgreSQL |
| 🔴 | Redis |
| 🐳 | Docker / Docker Compose |
| ☁️ | An external or cloud service (a paid LLM API, LocalStack, GitHub OAuth, SMTP) |

Each module's README states exactly what it needs and how to start it.

## The stack, and why

Stdlib-first. Where the standard library does the job, this repo uses it and
explains the mechanics, rather than reaching for a framework that hides them.

| Concern | Choice | Why not the obvious alternative |
|---|---|---|
| HTTP routing | `net/http` (Go 1.22 patterns) | Gin and Echo are fine; the stdlib now does method and wildcard routing, and learning it means learning what the frameworks wrap |
| Middleware | `chi` where composition gets repetitive | Still `http.Handler` underneath, so nothing is hidden |
| Database | `pgx/v5` + `sqlc` | GORM generates SQL you cannot see; sqlc generates Go from SQL you wrote |
| Migrations | `goose` | Plain SQL files, same model as Alembic's, without the Python |
| Testing | stdlib `testing`, `testify/require` in the backend modules | `go-concepts/` and `dsa/` stay dependency-free on purpose |
| Background jobs | `asynq` | The Celery analogue, Redis-backed |
| GraphQL | `gqlgen` | Schema-first with generated resolvers |
| gRPC | `grpc-go` | Go is the reference implementation |
| Linting | `golangci-lint` | The ruff analogue; config in [.golangci.yml](.golangci.yml) |

## A note on the coverage number

`make cover-summary` reports about 40% total, and that number is not worth
chasing. Every lesson has a `main.go` plus a `demo*` function per topic, which
exist so `go run ./04-errors` prints a guided tour. They are output formatting,
they are never called from a test, and they are a large share of the statements.

The number that means something is coverage of the functions that teach
something: **97.6% across 154 functions, with none at zero**. What remains is
unreachable branches, mostly error paths on operations that cannot fail in a
test.

This is why the CI coverage job prints the total and does not enforce a
threshold. A gate here would be satisfied by calling `demoErrors()` from a test
and asserting nothing, which would raise the number and test nothing.

## Development

```bash
make check         # fmt-check, vet, lint, test — what CI runs
make test          # every test in every module
make test-race     # under the race detector
make bench         # benchmarks with allocation counts
make cover         # coverage report in a browser
make lint-fix      # apply what the linter can fix
make help          # every target
```

Scope any target to one module with `DIR`:

```bash
make test DIR=./go-concepts/04-errors/...
```

## Conventions

- **Folder names use hyphens.** Go package names drop them (`04-errors` is
  `package main`). Go permits hyphens in directory names but not identifiers.
- **Step files carry a name, not a number**, inside a numbered lesson folder.
  Go needs one package per directory, so a lesson is a folder of related files
  rather than a sequence of standalone scripts. The lesson's `main.go` runs
  every demo in order.
- **"The why" lives in `README.md`.** Code files carry a package doc comment for
  "the what", plus ASCII diagrams where a picture is quicker than a paragraph.
- **Every claim in a README has a test.** If the prose says a nil map write
  panics, there is a test that recovers from it.
- **Suppressions state a reason.** A bare `//nolint` is never correct here; see
  [LESSONS.md](LESSONS.md) for why the linter keeps catching the deliberately
  broken examples.

## Also here

- [LESSONS.md](LESSONS.md) — everything that did not go to plan while building
  this, written down when it happened. Mostly about tooling disagreeing with
  teaching code, which turned out to be the recurring theme.
- [LICENSE](LICENSE) — MIT.
