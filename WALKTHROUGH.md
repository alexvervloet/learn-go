# Walkthrough

The intended path through this repository, in order, with what each step needs and what tends to go wrong.

Every command below was run before it was written down. Where a number appears, it came from running something,
not from an estimate.

## What you are looking at

18 Go modules in one workspace: 131 packages, 248 test files, about 127,400 lines of hand-written Go plus 6,550
generated. `go.work` is what ties them together, and each module has its own `go.mod` so you can clone one
directory and it still builds.

| Area | What it is |
|---|---|
| `go-concepts/` | 18 lessons on the language, in dependency order |
| `dsa/` | Structures, algorithms and interview patterns |
| `backends/learning/` | 13 modules, one per backend topic |
| `capstones/` | Two finished services |
| `utilities/` | Three standalone packages with no services |

## Before anything

```sh
go version            # needs 1.27 or newer; go.work says `go 1.27.1`
make tools            # installs golangci-lint at the pinned version
```

Docker is needed for about half the modules and nothing else is. Every test that needs a service **skips** with
a message naming the command to start it, so `make test` is green on a machine with nothing running. That is
deliberate and it is also the trap: a skip is not a pass, which is why CI starts the services and fails the
build if it sees a skip message.

**`go test ./...` does not work at the repository root**, and that is not a bug in the repository. The root is
not itself a module, so the pattern matches nothing and Go says
`directory prefix . does not contain modules listed in go.work`. Every target in the `Makefile` expands
`go list -m -f '{{.Dir}}/...'` instead, which is the list of module directories. `make test` is the root-level
command; `go test ./go-concepts/...` and friends work because those directories ARE inside a module.

## The order

The dependencies are real, not editorial. Each stage uses what the one before it established.

### 1. `go-concepts/`

```sh
go test ./go-concepts/...
```

No services. Start at `01-types-and-zero-values` and go in order: the lessons reference each other and lesson 10
on `context` assumes lessons 6 through 9 on goroutines, channels, select and the sync primitives.

Three lessons are load-bearing for everything after:

- **`06-goroutines`** has the leak examples. The counter in `leaks.go` is incremented in the *parent* before the
  `go` statement, for the same reason `WaitGroup.Add` goes there: incrementing inside leaves a window where the
  parent has returned and the goroutine has not been scheduled.
- **`09-sync-primitives`** measures what the race detector does to `sync.Pool`. Under `-race` the standard
  library **drops one `Put` in four on purpose**, and `TestPoolRetainsNothing` measures the rate at 23% to 28%
  over 400 samples. A test that fails only under `-race` is not automatically a data race.
- **`16-race-detector`** is why `make test-race` exists, and why several tests in this repository assert a
  *bound* rather than an exact count.

```sh
make test-race        # the whole repository; a few minutes
```

### 2. `dsa/`

```sh
go test ./dsa/...
```

No services, no order dependency between the structures. `dsa/patterns/` is the interview material and
`dsa/pvsnp/` is the complexity-theory one.

### 3. `backends/learning/`

Thirteen modules. The table in `backends/learning/README.md` has them all; this is the order that makes sense
and what each one needs.

| Module | Needs | Start it with |
|---|---|---|
| `http-tutorial` | nothing | |
| `testing-concepts` | nothing | |
| `database-concepts` | Postgres | `docker compose up -d` in that directory |
| `backend-concepts` | Postgres, Redis, Kafka | `docker compose up -d` |
| `grpc-concepts` | nothing | |
| `graphql-concepts` | nothing | |
| `jobs-concepts` | Redis | `docker compose up -d` |
| `email-concepts` | Mailpit | `docker compose up -d` |
| `docker-concepts` | Docker, in **Linux**-container mode | |
| `makefile-concepts` | GNU make, on a POSIX shell | |
| `aws-concepts` | LocalStack | `docker compose up -d` |
| `github-actions` | nothing | |
| `ai-concepts` | nothing (one gated test wants a key) | |

**Do `database-concepts` before `backend-concepts`.** The second uses the first's isolation trick without
re-explaining it, and the pagination and caching material assumes you know why `EXPLAIN` is in the tests.

#### The ports

Every compose file binds a host port nothing else in the repository binds, so you can have several up at once.
There is no port 5432 or 6379 anywhere, because a locally installed Postgres or Redis already has those and the
collision is invisible: Docker binds `*:5432` while a local Postgres binds `127.0.0.1:5432`, `localhost`
resolves to the second, and every test then skips with `role "postgres" does not exist` while a healthy
container sits there.

| Module | Postgres | Redis | Other |
|---|---|---|---|
| `database-concepts` | 5433 | | |
| `backend-concepts` | 5436 | 6380 | Kafka on 19092 |
| `jobs-concepts` | | 6381 | asynqmon on 8090 |
| `email-concepts` | | | Mailpit on 1026 and 8026 |
| `aws-concepts` | | | LocalStack on 4566 |
| `docker-concepts` | | | the demo app on 8081 |
| `capstones/bookmark-manager` | 5434 | 6383 | |
| `capstones/url-shortener` | 5435 | 6382 | |

Three files bound 5433 until the day this walkthrough was written. Checking is one line:

```sh
find . -name docker-compose.yml | xargs grep -ohE '"[0-9]{4}:[0-9]{4}"' | tr -d '"' | cut -d: -f1 | sort | uniq -d
```

#### Connecting to them

Each module defaults to a **locally installed** Postgres or Redis on the usual port and prints its compose URL
in the skip message. So either works:

```sh
# a local Postgres with a learn_go_db, no environment variable needed
go test ./backends/learning/database-concepts/...

# or the container
cd backends/learning/database-concepts && docker compose up -d
DATABASE_URL="postgres://postgres:postgres@localhost:5433/learn_go_db?sslmode=disable" go test ./...
```

### 4. `capstones/`

Two services that put the modules together. Read `url-shortener` first: it is the simpler shape and the
bookmark manager's README refers back to it for the decisions it makes differently.

```sh
cd capstones/url-shortener && docker compose up -d
DATABASE_URL="postgres://postgres:postgres@localhost:5435/learn_go_db?sslmode=disable" \
  REDIS_ADDR=localhost:6382 go test ./...

cd ../bookmark-manager && docker compose up -d
DATABASE_URL="postgres://postgres:postgres@localhost:5434/learn_go_db?sslmode=disable" \
  REDIS_ADDR=localhost:6383 go test ./...
```

108 tests and 93 tests respectively.

They deliberately differ:

| | url-shortener | bookmark-manager |
|---|---|---|
| Tokens | one, stateless, unrevocable | access plus rotating refresh, with reuse detection |
| Relationships | none | categories and a many-to-many tag join |
| Search | none | Postgres full text, generated column, GIN |
| Rate limiting | none | sliding window in Lua |
| Background work | asynq worker, second binary | none |

### 5. `utilities/`

```sh
go test ./utilities/...
```

No services, no order. Three packages: a CLI with `flag`, a bounded concurrent aggregator, and the channel
pipeline pattern.

## Running everything

```sh
make check            # what CI runs, in CI's order
```

That is `fmt-check`, `vet`, `lint`, `tidy-check`, `isolated-check`, then `test`. `isolated-check` is the one
worth knowing about: it builds each module with `GOWORK=off`, the way somebody cloning one directory would, so a
module that only compiles because a sibling is in the workspace fails there rather than in a stranger's terminal.

CI has 11 jobs. The ones that need explaining:

- **Services** starts Postgres, Redis, Kafka and Mailpit, runs three modules, and then **greps the log for skip
  messages and fails if it finds one**. A skip is not a pass, and three services means three ways for a
  connection string to be wrong, each of which turns a third of a module green while running none of it.
- **Generated code is current** regenerates the protobuf and gqlgen output and diffs it. `protoc` is pinned in
  `backends/learning/grpc-concepts/.protoc-version`, because every `.pb.go` records the compiler version in its
  header and Ubuntu's 3.21.12 against a developer's 36.2 is an eight-file diff that says nothing about the
  schema.
- **Cross-compile** has two tiers. The deployable targets build every module; plan9 and the two wasm targets
  build only `go-concepts` and `dsa`, because asynq calls `Server.waitForSignals` and kafka-go names
  `syscall.ECONNREFUSED`, neither of which exists on plan9. A check that can never pass is not a strict check,
  it is a red X everyone learns to ignore.

## Things that will surprise you

These are the ones that cost real time. The full list, 86 entries, is in `LESSONS.md`.

**`go test ./...` runs package binaries concurrently.** Two packages truncating one database is a deadlock in
one and a *wrong query plan* in the other. Every module that touches Postgres gives each test binary its own
database, named from `os.Args[0]`, which ends in `<pkg>.test`.

**`pgxpool.Config.ConnString()` returns the string it was parsed from.** Mutate `ConnConfig.Database` and
`ConnString` still names the old one, so migrations run against one database while the tests truncate another.
It produced `relation "books" does not exist` in CI on code that passed locally, and then produced
`relation "clicks" does not exist` an hour later in a different module, written by someone who had just fixed
the first one.

**A plan is a function of the data, not just the schema.** The bookmark manager's GIN index is used at 20,000
rows and correctly ignored at 200. Three fixtures asserted the wrong thing before one was big enough to mean
anything, and `ANALYZE` after a bulk insert is not optional: autovacuum has not run, and a planner without
statistics is guessing.

**A JWT cannot express a sub-second TTL.** `exp` and `iat` are truncated to `jwt.TimePrecision`, which is one
second, so a 300ms token is expired the instant it is signed and nothing reports it. asynq has the same trap
with whole seconds: `Timeout(300 * time.Millisecond)` becomes zero, and zero means the default of 30 minutes.

**`INSERT ... ON CONFLICT DO NOTHING RETURNING` returns only what it inserted.** A tag that already existed is
absent from the result, so a caller that trusts it silently drops every existing tag and the bug reads as "tags
disappear sometimes".

**Docker being available is not the question.** A Windows runner has a healthy daemon in Windows-container mode
that cannot build any Linux image. The check is `docker info --format "{{.OSType}}"`.

**Generated code belongs in the repository.** Cloning one module and building it has to work with nothing but a
Go toolchain, so `*.pb.go` and gqlgen's output are committed and CI regenerates and diffs them. The reasoning is
in `.gitignore`, where the entries would otherwise be.

**A test that passes standalone and fails under `make test` is the worst shape there is.** Two makefile tests
did, because `make` sets `MAKELEVEL=1`, `go test` inherits it, and the child `make` then decides it is a
sub-make and wraps its output in `Entering directory` and `Leaving directory` lines. One test counted those
words as module paths. Clearing `MAKEFLAGS` is half the job; `MAKELEVEL` is the other half. The only way to find
it is to run the command the README tells people to run.

**`go work sync` and per-module `go mod tidy` pull in opposite directions.** The first pushes every module up to
the workspace-wide maximum; the second pulls each down to what it needs. This repository checks `tidy -diff` on
every module, so per-module tidy is the invariant and `go work sync` is not run.

## Conventions

Every module follows these, and they are the reason the tests are worth reading.

**Assert the machine-independent quantity; log the timing next to it.** Query counts, HTTP round trips, plan
node types, goroutine counts, image sizes. Not durations. A duration is a property of the runner; 31 queries
against 2 is the same number everywhere.

**A claim in a comment gets a test.** Where that was not possible the comment says so. Several comments in this
repository were wrong until the test was written, and each one is in `LESSONS.md`.

**A test that needs a service skips with the command to start it**, and CI fails on the skip. Both halves are
necessary: without the first, a developer without Docker cannot run anything; without the second, a broken
connection string looks like success.

**Errors match on codes, not messages.** Postgres SQLSTATE, AWS error codes, HTTP status. A message is prose,
it is localised, and it changes.

## If something does not work

| Symptom | Cause |
|---|---|
| Every database test skips with `role "postgres" does not exist` | A local Postgres owns 5432 and is answering instead of the container. Use the module's port. |
| `port is already allocated` | Another module's compose is up. `docker compose down` in that directory. |
| `relation "..." does not exist` | Migrations ran against a different database from the tests. |
| Image tests skip on a healthy Docker | The daemon is in Windows-container mode. |
| `make lint` fails and `go vet` does not | `make tools`; golangci-lint runs checks `vet` does not. |
| A module builds in the workspace and not alone | Run `make isolated-check`, which is what CI does. |
| `pattern ./...: directory prefix . does not contain modules` | You are at the repository root. Use `make test`, or a path inside a module. |
| A test passes on its own and fails under `make test` | Something is inherited from the parent process. `MAKEFLAGS` and `MAKELEVEL` are the usual two. |
