# The race detector

> 📚 [go-concepts](../README.md) · **Step 16 of 18** · [⬅ 15-modules-and-workspaces](../15-modules-and-workspaces/) · Next: [17-benchmarks-and-pprof](../17-benchmarks-and-pprof/) ➡

## What is this?

`go test -race` instruments every memory access and reports when two goroutines
touch the same address without synchronisation, with stack traces for both.

It is the most valuable tool in the language, and this repo is the evidence: it
found a race in **lesson 06, the lesson about goroutines**, in code I had
written carefully and reviewed. The write-up is in
[LESSONS.md](../../LESSONS.md).

## What a data race actually is

Two goroutines access the same memory, at least one writes, and nothing orders
them. That is the whole definition, and each clause matters:

- **Two goroutines.** One goroutine racing itself is impossible.
- **The same memory.** Distinct slice elements are distinct memory, which is
  why `results[i] = ...` from many goroutines is fine.
- **At least one write.** Concurrent reads are always safe.
- **Nothing orders them.** A mutex, a channel, a `WaitGroup`, an atomic or
  `sync.Once` all create the ordering. The Go memory model says exactly which.

A race is **undefined behaviour**, not "a wrong number sometimes". The compiler
optimises assuming races do not happen, so a racy program can do things no
interleaving of the source would explain. This is the part people underestimate:
`counter++` from two goroutines can lose an update, and it can also produce a
value neither goroutine ever computed.

## Coming from Python

CPython's GIL means only one thread runs bytecode at a time, so `counter += 1`
from ten threads usually gives the right answer. The habits that survives are
wrong in Go, where there is no GIL and goroutines genuinely run in parallel.

There is no Go equivalent of "it works because of the GIL". There is only
synchronised or racy.

## Reading a report

```
WARNING: DATA RACE
Read at 0x00c000018098 by goroutine 8:
  main.(*Counter).Value()
      counter.go:23 +0x2c

Previous write at 0x00c000018098 by goroutine 7:
  main.(*Counter).Inc()
      counter.go:18 +0x44

Goroutine 8 (running) created at:
  main.main()
      main.go:15 +0x88
```

Four parts, and the fourth is the one people skip:

1. **The address.** Same address in both halves means the same variable.
2. **The current access,** with its stack.
3. **The previous access,** with its stack. One of the two is a write.
4. **Where each goroutine was created.** This is usually what identifies the
   bug, because the access sites are often in a library and the `go` statement
   is yours.

One caveat on that fourth part, found while writing this lesson. Since Go 1.25,
`sync.WaitGroup.Go` starts the goroutine itself, so the report says:

```
Goroutine 8 (running) created at:
  sync.(*WaitGroup).Go()
      .../sync/waitgroup.go:238
  testing.tRunner()
```

and your own line is not in it. The same happens with `errgroup.Go` and any
helper that takes a func and starts it. Look one frame past the helper. A bare
`go func(){...}()` still points straight at your code.

## What it costs, and what it misses

| | Cost |
|---|---|
| CPU | 2-20x slower |
| Memory | 5-10x more |
| Goroutines | limited to 8192 simultaneously |

So it is a testing tool, not a production one, though running one canary
instance with `-race` is a real technique for a race that will not reproduce.

**It only reports races it actually observes.** It is not static analysis: code
that never runs, or an interleaving that never happens, reports nothing. A green
`-race` run means "no race in what executed", which is exactly as strong as your
test coverage of concurrent paths.

The practical consequences:

- Run the **whole** suite under `-race`, not one package.
- Concurrency tests should run the racy operation many times, and with enough
  goroutines to actually interleave.
- `-race` with `-count=10` finds things one run does not.

## What it cannot catch

**Deadlocks.** A different failure, caught by the runtime only when *every*
goroutine is blocked ([lesson 07](../07-channels/)).

**Goroutine leaks.** Not a race; nothing is racing ([lesson 06](../06-goroutines/)).

**Logical races.** Check-then-act with a mutex round each half is perfectly
synchronised and still wrong ([lesson 09](../09-sync-primitives/)). The detector
sees correct locking and says nothing.

That last one is worth repeating: **`-race` clean does not mean correct.**

## Testing code that is deliberately racy

This repo has examples that race on purpose, and the detector rightly fails the
package. The fix is a build-tag flag, which is what the standard library does in
`internal/race`:

```go
//go:build race
const raceDetectorEnabled = true

//go:build !race
const raceDetectorEnabled = false
```

Tests exercising an intentional race skip themselves when the detector is
watching, so `-race` stays meaningful everywhere else. See
`09-sync-primitives/raceflag_*.go`.

## What the files cover

| File | What it teaches |
|---|---|
| `races.go` | Five race shapes: counter, map, slice append, struct fields, closure capture |
| `fixes.go` | Each one fixed, with the memory-model rule that makes the fix work |
| `notaraces.go` | Code that looks racy and is not, and why |
| `notcaught.go` | What `-race` cannot see: leaks, deadlocks, logical races |
| `reading.go` | Anatomy of a report, and how to act on one |
| `main.go` | Runs every demo in order |
| `*_test.go` | Tests, guarded so the deliberate races skip under `-race` |

## How to run

```bash
go run ./16-race-detector
go test ./16-race-detector            # the deliberate races run, and lose updates
go test -race ./16-race-detector      # they skip; everything else is checked

# See a real report. This FAILS on purpose:
go test -race -run TestShowMeARealRaceReport -tags showrace ./16-race-detector
```
