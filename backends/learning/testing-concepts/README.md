# testing-concepts

> 📚 [Repository root](../../../README.md) · **Core path · step 2 of 4** · [⬅ http-tutorial](../http-tutorial/) · Next: [database-concepts ➡](../database-concepts/)

Five subjects. The one where Go differs most from dynamic languages is fakes: a dependency is swapped through an
interface the code already accepts, never by patching a module at runtime.

```
basics/       table-driven tests, subtests, TestMain, t.Cleanup, t.Parallel
fakes/        interfaces instead of mock.patch, and when testify/mock earns its keep
golden/       golden files, -update, and the two things that make them flaky
fuzzing/      go test -fuzz, and the bug it found in 0.04 seconds
concurrency/  -race, testing/synctest, goroutine leaks
```

## The mapping from pytest

| pytest | go test |
|---|---|
| discovery by name | files `*_test.go`, functions `TestXxx` |
| `@pytest.mark.parametrize` | a table and a loop |
| fixtures | ordinary functions taking `*testing.T` |
| `yield` fixtures | `t.Cleanup` |
| `scope="session"` | `TestMain` |
| `@pytest.mark.slow` + `-m` | `testing.Short()` + `-short`, or a build tag |
| `conftest.py` | nothing; there is no implicit sharing |
| `assert a == b` | `if a != b { t.Errorf(...) }` |
| `pytest -k` | `go test -run` |
| `pytest -x` | `go test -failfast` |
| `unittest.mock.patch` | an interface parameter |
| `pytest-asyncio` | nothing needed; plus `testing/synctest` |
| `pytest-cov` | `go test -cover`, built in |

**There is no fixture injection, and that is mostly a relief.** pytest resolves fixtures by
parameter *name*, so a test's dependencies are declared by spelling and finding one means searching
every `conftest.py` up the tree. In Go a test that needs a cart calls `newCart(t)` and the definition
is one jump away. What is genuinely lost is fixture caching across a scope; `t.Cleanup` covers the
teardown, the caching has to be written.

## basics: the parts that are not obvious

**`t.Helper()` is one line and it changes every failure message.** Without it, a failure inside a
helper reports the helper's file and line, so every failure in the suite points at the same place.
`basics/failing_test.go` is behind a build tag so you can see both:

```
go test -tags demo_failure -run Deliberate ./basics

failing_test.go:32: Add failed: ...   # with t.Helper() — the call site
failing_test.go:27: Add failed: ...   # without        — inside the helper
```

**`TestMain` must not defer its teardown.** `os.Exit` skips deferred functions, so `defer teardown()`
in `TestMain` never runs. The teardown goes between `m.Run()` and `os.Exit`.

**A parallel subtest's body runs after its parent function returns.** So a `defer` in the parent
fires *before* the subtests do, and only `t.Cleanup` waits. `TestParallelCleanupOrdering` measures
the ordering and it is the single most common parallel-test bug: set up a resource, `defer` its
teardown, run parallel subtests against it, and every subtest sees a closed resource.

**The `tt := tt` line is dead.** Go 1.22 made the loop variable per-iteration, so the copy every
parallel table test used to need is now noise. `TestLoopVarIsPerIteration` proves it.

**`t.Setenv` forbids `t.Parallel` in the same test**, and the error is a panic rather than a compile
failure, because the environment is process-global. That restriction is a good reason to pass
configuration as an argument instead.

## fakes: there is nothing to patch

`mock.patch` works by replacing an attribute on a module at runtime. It needs no cooperation from the
code under test, which is its strength and its weakness: a patch targeting the wrong import path
silently does nothing.

Go's interfaces are satisfied *implicitly*, so the implementation does not know the interface exists.
The consumer declares the narrow interface it wants, the real type satisfies it unchanged, and a fake
satisfies it too. There is no patching because the dependency arrives as an argument.

**Define the interface where it is used, not where it is implemented.** A `Notifier` interface next
to the email client is a Java habit. It belongs next to the service, listing only the methods that
service calls — usually one or two. A fake for a two-method interface is fifteen lines; a fake for
the email client's full surface is two hundred, and that difference is the whole reason people reach
for a mock library.

### Three ways to stand in for a dependency

| | lines | when |
|---|---|---|
| function-field stub | ~5 | the default. Per-test behaviour, and a nil field panics on an unexpected call, which is a useful assertion for free |
| hand-written fake | ~15 | when the test wants to assert on **state** afterwards rather than on calls |
| `testify/mock` | ~10 + a dependency | when the assertion **is** about the call: "must not charge twice", "the audit log must record this" |

All three appear against the same service in `fakes_test.go` so the trade is visible.

`require` versus `assert` is the testify distinction people get wrong: `require` stops the test like
`t.Fatalf`, `assert` continues like `t.Errorf`. Using `assert` for a nil check and then dereferencing
reports a panic instead of the assertion that should have stopped it.

### Fake time, and the thing Go has that Python does not

`testing/synctest` (standard library since Go 1.25, behind `GOEXPERIMENT=synctest` in 1.24) runs a function in a bubble where the `time`
package uses a **fake clock** that advances only when every goroutine in the bubble is durably
blocked. A test of exponential backoff with 1s, 2s and 4s waits runs instantly and assertions are
*exact*:

```go
synctest.Test(t, func(t *testing.T) {
    start := time.Now()               // always midnight UTC 2000-01-01
    _, err := retrying.Run(ctx, "o_1")
    if elapsed := time.Since(start); elapsed != 15*time.Second {
        t.Errorf("elapsed = %v, want exactly 15s", elapsed)
    }
})
```

Fifteen seconds of backoff, measured to the nanosecond, in microseconds of real time. The Python
mirror makes the backoff configurable, sets it to a millisecond, and accepts that the test is
timing-dependent and occasionally flaky on a loaded runner.

**The limitation:** everything inside a bubble must block on a channel, a mutex, or the `time`
package. A CPU-bound loop does not advance the clock, because the goroutine is *runnable* rather
than blocked, and the test hangs. Network and external processes break it too, which is why the
`synctest` docs list that first.

## golden: two things make them flaky, and both are in the renderer

```
go test ./golden           compare
go test ./golden -update   rewrite
```

The technique is fifteen lines. What kills golden tests in practice is non-determinism, and there are
exactly two usual causes:

- **Map iteration order.** Go randomises it, so an unsorted render differs between runs.
  `Render` sorts the keys, and `TestRenderIsDeterministic` renders the same invoice a hundred times
  and compares. That test is the one people forget, and its failure is far more informative than a
  golden mismatch: it names determinism as the problem rather than looking like a content change.
- **Timestamps and timezones.** `time.Time`'s default `String()` includes a monotonic reading that
  differs every run. `Render` uses a fixed layout in UTC, and `TestRenderNormalisesTheTimezone`
  checks the same instant in two zones renders identically — because a golden file generated on a
  laptop has to match one generated in CI.

The `-update` flag is also the risk: a flag that rewrites the test's own expectations makes accepting
a regression a single command. The discipline is that its output goes in a diff and gets read, which
is why the files are committed.

## fuzzing: it found the bug in 0.04 seconds

`fuzzing/buggy.go` is the first version of a length-prefixed record decoder — what a careful person
writes in five minutes. It has three bugs and only one is a crash.

```
go test -tags demo_fuzz -fuzz FuzzBuggyDecode ./fuzzing

fuzz: minimizing 56-byte failing input file
--- FAIL: FuzzBuggyDecode (0.04s)
    panic: runtime error: slice bounds out of range [:808464432] with capacity 10
```

The minimised crasher is **six ASCII zeros**. `0x3030` is 12,336, so it claims 12,336 records from a
four-byte remainder. The fixed version rejects it cleanly:
`too many records: claimed 12336`.

| bug | found by |
|---|---|
| a claimed length not checked against the remaining input | the fuzzer, instantly |
| a claimed record count used to preallocate, so 2 bytes allocate 65,535 strings | reading the code afterwards |
| trailing bytes ignored, making the format ambiguous | a round-trip **property**, not a crash |

That is the honest limit of fuzzing: **it finds crashes**. The other two needed a property to check
against, which is what `FuzzDecodeDoesNotPanic` does by requiring that `Encode(Decode(x)) == x` for
every input that decodes. The ambiguity bug fails that property immediately.

The fixed version survives 15 seconds and ~9 million executions per target:

| target | execs in 15s | new interesting inputs |
|---|---|---|
| `FuzzDecodeDoesNotPanic` | 9,251,446 | 17 |
| `FuzzEncodeDecodeRoundTrip` | 5,939,044 | 6 |
| `FuzzDecodeStrictAgreesWithDecode` | 8,767,893 | 52 |

Two practices worth copying. **A crasher belongs in an ordinary table test as well as the corpus** —
a file called `66498f377f38b53e` tells a reader nothing, and `TestDecodeRejectsTheFuzzerCrashers`
names each one. And **a property that two functions agree needs a companion example**, because
"`DecodeStrict` agrees with `Decode` except on invalid UTF-8" is satisfied by a `DecodeStrict` that is
identical to `Decode`.

## concurrency: two failure modes, two tools

**A data race**, which `-race` finds and ordinary testing will not. `TestUnsafeCounterLosesIncrements`
runs 200,000 unsynchronised increments and reports how many were lost — and sometimes loses none,
which is exactly why the detector exists: the race is real whether or not it produces a wrong answer.

The detector's report names both accesses, both goroutines, and where each was created. Worth seeing
once:

```
go test -tags demo_race -race -run TestDeliberateRace ./concurrency
```

**A leaked goroutine**, which nothing finds unless you look. `LeakyFetch` sends on an unbuffered
channel and returns early on cancellation, so the goroutine blocks forever. One per request takes a
service down over a day.

`checkNoLeaks` is a hand-rolled `uber-go/goleak` in fifteen lines. Two details make it work rather
than flake: it **retries**, because a goroutine that is finishing needs a moment to be reaped and a
single `NumGoroutine` reading is a coin flip; and it uses `runtime.Gosched` rather than only
sleeping. What it cannot do is *name* the leaked goroutine, which is what `goleak` parses a stack
dump for and is worth the dependency on a real service.

`TestLeakyFetchLeaks` asserts the leak **happens**, deliberately. If `LeakyFetch` is ever fixed the
test fails and says so, rather than silently becoming a test of nothing.

### The Pool's API had a deadlock, and the test found it

`TestPool` hung until the 120-second timeout on its first run. The cause is a design wart worth
keeping: results arrive on a **bounded** channel, so submitting more jobs than the buffer holds
without draining wedges the pool — the workers block sending, so nothing reads from `jobs`, so
`Submit` blocks forever.

The arithmetic is `2n`, not `n`, and I got that wrong too: with n workers and an n-slot buffer, n
results fill the buffer and n more are picked up and then stuck trying to deliver.

Two fixes, both shipped. `TestPoolDeadlocksIfResultsAreNotDrained` pins the contract down so it
cannot be rediscovered, and `Collect` starts the drain before submitting so there is nothing to get
wrong. A pool whose obvious usage order deadlocks is a bad API, and the honest response is to
provide the one that does not and document why both exist.

## Coverage

```
basics       100.0%
concurrency   95.9%
fakes         92.9%
golden        98.4%
fuzzing       65.8%
```

Worth saying what that measures: **which lines ran**, and nothing else. A test calling every function
and asserting nothing scores 100%, which makes `basics`'s 100% the least meaningful number here.

`fuzzing` is lowest for a reason worth knowing: `buggy.go` exists only to be fuzzed under a build
tag, so its lines never run in a normal `go test` and drag the figure down by about 20 points. A
coverage target would have pushed me to delete the most instructive file in the package, which is
the argument against coverage targets in one sentence.

## How to run

```bash
go test ./...                    # everything, ~4s
go test -race ./...              # the only way to find a data race
go test -short ./...             # skips the slow ones
go test -cover ./...

go test ./golden -update         # regenerate the golden files, then read the diff
go test -fuzz FuzzDecodeDoesNotPanic -fuzztime 30s ./fuzzing

# the three deliberate failures, each behind a build tag
go test -tags demo_failure -run Deliberate ./basics
go test -tags demo_fuzz -fuzz FuzzBuggyDecode ./fuzzing
go test -tags demo_race -race -run TestDeliberateRace ./concurrency
```

## What Go does not have

| | |
|---|---|
| fixture injection by name | pass arguments |
| fixture caching across a scope | `TestMain`, or a package-level `sync.Once` |
| a marker system (`-m "not slow"`) | `testing.Short()` or a build tag |
| parametrised fixtures | a table of constructors |
| `pytest-xdist` (distributed runs) | `go test` already parallelises per package; `t.Parallel` within one |
| plugins | nothing, and nothing to hook |

And what it has that pytest does not: a race detector in the toolchain, a fuzzer in the toolchain, a
fake clock in the standard library, and a goroutine dump on deadlock that says what every goroutine
was waiting for.
