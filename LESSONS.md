# Lessons

Things that did not go according to plan while building this repo, written down
when they happened. The counterpart to `PLAN.md`, which is scratch and never
committed. This file is committed and stays.

## 2026-09-25 — Two of my tests were flaky, and only CI could tell me

**Expected:** `make test` and `make test-race` passing locally, repeatedly,
meant the suite was stable. Lessons 06 and 09 both went in green.

**What happened:** two failures that only ever appeared on GitHub's runners.

**`TestParallelIsFasterThanSequential`**, on a 4-CPU runner:

```
sequential 12ms, parallel 17ms, speedup 0.7x on 4 CPUs
```

Parallel was SLOWER. The test timed one run of each and asserted `par < seq`.
On a shared host with four cores, one goroutine being descheduled for longer
than the whole measurement is entirely normal. My machine has twelve idle cores
and never showed it.

**`TestAddInsideTheGoroutineIsUnreliable`**, on macOS:

```
panic: sync: WaitGroup is reused before previous Wait has returned
```

The test drove a deliberate race and expected a short count. The race can also
PANIC, which is the same bug with a different symptom, and the test had no
recover.

**Next time, two rules.**

For timing assertions on hardware you do not control: **best-of-N, and a
threshold with real slack.** A slow run means something interfered; a fast run
is closer to the truth. Five runs, take the minimum of each side, and assert
"at least 20% faster" rather than "faster". Locally that now reports a stable
7.6-8.9x and it will not flip on a contended runner.

For tests that drive a deliberate bug: **accept every symptom the bug can
produce.** The test now treats a short count OR a panic as evidence, and only
fails if the code turns out to be reliably correct, which would mean the example
had stopped demonstrating anything.

The wider point: a green local run on a quiet 12-core machine is weak evidence
about a 4-core shared runner. The test matrix exists for this, and both bugs
were mine rather than Go's.

## 2026-09-25 — The range-over-func contract is enforced, not just documented

**Expected:** writing lesson 11's iterator section, I described a producer that
ignores `yield`'s return value as a silent waste: the loop would still exit
because the compiler's yield returns false forever, but the producer would keep
computing elements nobody wanted. I wrote that into a doc comment and built a
demo to count the wasted work.

**What happened:** the demo crashed the program.

```
panic: runtime error: range function continued iteration after
       function for loop body returned false
```

The compiler rewrites a `for ... range f` body into a yield function that tracks
whether the loop has exited, and the generated code panics on the second call
after it returned false. So the contract is checked at runtime, every time, and
a badly written iterator fails loudly at its first mistake instead of quietly
doing extra work.

**Next time:** this is the third time in this build that running the demo
corrected the prose (see the `sync.Map` and HTTP-deadline entries). The pattern
is now unambiguous enough to state as a rule for the rest of the repo:

**Write the demo before the paragraph.** Not after it as an illustration. The
paragraph is a guess until something has executed, and a guess that sounds
plausible is exactly the kind that survives review.

The corrected version is better material anyway. "The runtime catches this and
tells you precisely what you did" is more useful to a reader than "be careful to
return when yield says false", and it is the sort of thing you only find by
getting it wrong in front of a compiler.

## 2026-09-25 — Context deadlines do not cross an HTTP hop; only cancellation does

**Expected:** I wrote lesson 10's README claiming that chaining `r.Context()`
into an outbound `http.NewRequestWithContext` propagates the caller's deadline,
so "the caller's remaining budget becomes the callee's budget". Then I wrote a
demo, `budgetPropagatesAcrossAHop`, to show it.

**What happened:** the demo reported the downstream service had been handed
`~0s`. I assumed a bug in my measurement and wrote a twenty-line standalone
program to check. It was not a bug:

```
CLIENT: has deadline=true,  2026-09-25 15:57:20 ...
SERVER: has deadline=false, 0001-01-01 00:00:00 UTC
```

HTTP has no standard header for a deadline, so nothing carries it. What DOES
cross is cancellation: the client aborting closes the connection and the
server's request context is cancelled. Those are different guarantees. With
cancellation alone a downstream service works at full cost until the caller
hangs up; with the deadline it knows its budget on arrival and can refuse.

gRPC propagates it with a `grpc-timeout` header, which is a concrete, specific
reason gRPC is nicer for service-to-service calls than plain HTTP, and I now
have a demo rather than a claim.

**Next time:** the README sentence came from general knowledge and felt obviously
true. The thing that caught it was writing a demo that printed a NUMBER rather
than a pass/fail. A test asserting "the downstream had a deadline" would have
failed and I would have debugged the test. Printing `~0s` next to an expectation
of `~198ms` pointed straight at the claim instead.

The lesson's HTTP section now shows both: the plain hop losing the deadline, and
a header-based propagation with the server clamping the caller's claim to its
own maximum, because a header is untrusted input.

## 2026-09-25 — A buffered channel turned a counter benchmark into a queue benchmark

**Expected:** `channelCounter` gave its increment channel a buffer of 64,
because a buffer is obviously better than no buffer. `TestAllThreeCountersAgree`
compared it against the atomic and mutex versions after a `WaitGroup.Wait`.

**What happened:** an intermittent failure with a message that read like a
compiler bug:

```
counters disagree: atomic=2000 mutex=2000 channel=2000, want 2000
```

All four numbers equal, and the assertion still fired. The `if` had read
`cc.Value()` as something less than 2000; by the time `Errorf` formatted its
arguments and re-read it, the owning goroutine had caught up.

The cause: with a buffered channel, `Inc` returns as soon as the value is
QUEUED. So `wg.Wait()` returned while increments were still sitting in the
buffer, and `Value()` could observe a count that was legitimately stale.

**Next time, two separate lessons.**

The correctness one: a buffered channel changes a synchronous operation into an
asynchronous one, and that is a semantic change, not a tuning knob. `Inc`
looked like the atomic and mutex versions and no longer meant the same thing.

The measurement one, which is worse: the benchmark was comparing the cost of
*enqueuing* an increment against the cost of *performing* one. With the buffer
removed so all three versions complete the same work, the channel version went
from 290 ns/op to **507 ns/op** — the published number was understating the gap
by three quarters. I had already written 290 into the README table.

A benchmark comparing two implementations has to be checked for whether they
still do the same amount of work, and "it got faster when I added a buffer" is
the exact shape of result that deserves the suspicion. Same failure mode as the
`appendGrowing`/`preallocated` pair earlier in this file, arriving from a
different direction.

Worth noting what caught it: a test asserting agreement between three
implementations, not the benchmark. The benchmark was perfectly happy.

## 2026-09-25 — I wrote down the standard sync.Map advice, then measured it and it was wrong

**Expected:** lesson 09's `sync.Map` section would say what everyone says: it is
a specialist for write-once-read-many and for disjoint key sets, and a plain map
behind a `RWMutex` is usually faster otherwise. I wrote that paragraph first and
added benchmarks to illustrate it.

**What happened:** the benchmarks contradicted it. On Go 1.27, an M2 Max, 12
cores:

| Workload | sync.Map | RWMutex map |
|---|---|---|
| sequential reads | 16.5 ns | 14.9 ns |
| parallel reads | **1.7 ns** | 116.8 ns |
| parallel 50/50 | 22.0 ns | 91.4 ns |
| parallel writes | 40.9 ns | 237.6 ns |

`RWMutex` wins only with zero contention, by ten percent. With real concurrency
it loses by up to 68x, because every `RLock`/`RUnlock` pair contends on one
cacheline across all twelve cores.

The likely cause is that Go 1.24 rewrote `sync.Map` on a hash-trie. The advice
was accurate when it was written and has quietly expired.

**Next time, two things.**

First, my initial benchmark pair used `b.RunParallel` for everything, which is
`sync.Map`'s best case. Had I stopped there I would have flipped the conclusion
in the other direction and been equally wrong. The sequential pair is what makes
the table honest, and adding it was an afterthought rather than a plan. A
benchmark that only measures one contention level measures almost nothing.

Second, the README now recommends the `RWMutex` map anyway, and says why: types.
`sync.Map` stores `any`, has no `Len`, and its `Range` is not a snapshot. That
reason survives whichever way the numbers go next release, which is what makes
it worth writing down at all. Performance advice has a shelf life; API-shape
advice mostly does not.

## 2026-09-25 — A repo that teaches data races cannot run its own tests under -race

**Expected:** lesson 09 needed an unsynchronised counter so the README's claim
about lost updates had something to point at. I added `racyCounter`, a test
asserting it loses increments, and expected `make test-race` to stay green
because the race was in a demo rather than in real code.

**What happened:** `go test -race` reported it immediately and failed the whole
package. Correctly. The detector cannot tell a deliberate race from an accident,
and it should not try.

This is a real structural problem rather than a one-off. Several lessons
demonstrate races on purpose, and lesson 16 is about nothing else. The options
were to delete the examples, or to drop the `-race` job from CI. Both are worse
than the problem.

**Next time:** Go defines a `race` build tag when the detector is on, and a
two-file pair is the standard way to branch on it:

```go
//go:build race
const raceDetectorEnabled = true

//go:build !race
const raceDetectorEnabled = false
```

The standard library does exactly this in `internal/race`. Tests that exercise a
deliberate race now call `t.Skip` when the detector is watching, so `-race` stays
meaningful for the other 99% of the code, and the demonstrations survive.

Worth knowing before designing the test layout, not after: any codebase with
intentionally-broken example code needs this escape hatch, and it is much easier
to add in lesson 9 than in lesson 16 with eight lessons of tests already written.

## 2026-09-25 — The race detector found a race in the lesson about races

**Expected:** `mainDoesNotWait` in lesson 06 was already careful. Every
goroutine incremented the counter with `atomic.AddInt32`, and the function read
it with `atomic.LoadInt32`. Every access went through an atomic operation, so
`go test -race` would be clean.

**What happened:**

```
WARNING: DATA RACE
Write at 0x00c0002a211c by goroutine 2321:
  sync/atomic.AddInt32()
Previous write at 0x00c0002a211c by goroutine 2224:
  ...mainDoesNotWait()
      starting.go:71
```

Line 71 was `return atomic.LoadInt32(&finished)`. The signature was
`func mainDoesNotWait(n int) (finished int32)` — a **named** return. `return x`
against a named return is `finished = x; return`, and that assignment is a plain
non-atomic write to the very variable the goroutines are still incrementing.

The atomic calls were never the problem. The return statement was, and it does
not look like an access at all.

**Next time:** two things.

- **Atomics protect the accesses that go through them, and nothing else.** One
  ordinary read or write of the same variable reintroduces the race. A named
  return is an ordinary write hiding inside `return`.
- **Use `atomic.Int32` rather than `atomic.AddInt32(&x, 1)`.** The method form
  keeps the underlying integer unexported, so there is no way to touch it
  non-atomically by accident. The free functions take a `*int32` that anything
  can also read directly, which is exactly how this happened. Both files now use
  the method form.

The honest note: I wrote this function, reviewed it, ran it, and shipped it into
a lesson whose README says data races are undefined behaviour. `-race` caught it
in under a second. That is the argument for the race job in CI, made better than
any paragraph I could write.

## 2026-09-25 — CI found three things a green local run could not

**Expected:** the first push would be green. `make check` passed locally:
gofmt clean, `go vet` clean, `golangci-lint` 0 issues, all tests passing, race
detector clean.

**What happened:** every test job passed, on all five OS and Go-version
combinations including Windows, plus race and coverage. The **Lint** job failed,
and for three separate reasons stacked on top of each other:

1. `golangci/golangci-lint-action@v6` with `version: latest` installs the
   **v1** line, currently v1.64.8. This repo's `.golangci.yml` is v2 format,
   so it failed with `can't load config`. The action's major version gates the
   linter's major version, which is not obvious from `version: latest`.
2. That prebuilt binary is compiled with go1.24, and refused to run against a
   module targeting a newer Go: *"the Go language version (go1.24) used to
   build golangci-lint is lower than the targeted Go version (1.27.1)"*.
3. `go.mod` said `go 1.27.1`. The `go` directive is a LANGUAGE version and
   should be `go 1.27`; the patch belongs in a `toolchain` line if anywhere.
   `go mod init` writes the patch version by default, which is how it got there.

And a fourth, which would have broken the job even after fixing the other
three: `args: --timeout=5m $(go list -m -f '{{.Dir}}/...')` inside an action's
`with:` block is **not** expanded by a shell. It is passed through literally.
Command substitution only works in a `run:` step.

**Next time:** two rules came out of this.

- **CI should run the Makefile target, not a reimplementation of it.** The lint
  job now runs `make fmt-check`, `make vet`, `make tools`, `make lint`, which
  are the same four commands anyone runs locally. The bug existed only because
  CI was doing the same job a different way.
- **Pin the linter.** `@latest` means a release nobody asked for can turn a
  green branch red. `GOLANGCI_VERSION := v2.14.0` in the Makefile, bumped
  deliberately and on its own commit.

Worth saying plainly: the test matrix passing on Windows and on the older Go
first time out was the part I was least confident about, and the part that gave
no trouble. The failure was entirely in the tooling around the code.

## 2026-09-25 — Go's growable stacks make the recursion-depth worry misplaced

**Expected:** the recursive-descent parser in lesson 05 would need a depth cap
before it could be called safe, and the fuzz target would find a stack overflow
given enough deeply nested input. I was ready to write a "always bound your
recursion" section.

**What happened:** 25 seconds of fuzzing, 5.3 million executions, nothing
escaped. Probing depth directly: 100, 1000, 10,000, 100,000 and **1,000,000**
levels of nested parentheses all parsed correctly, the last in 0.23s. Go starts
each goroutine on an 8KB stack and grows it by copying, to a 1GB default
maximum. Python's default recursion limit is 1000 and C's stack is fixed at 8MB;
neither intuition transfers.

**Next time:** check the runtime's actual limits before writing the warning. The
section that went into the README is more useful than the one I was going to
write, because it says where the real risk is: a million-level parse is a denial
of service long before it is a crash, and `fatal error: stack overflow` at the
1GB ceiling is not recoverable. The cap is worth having for time, not for
safety, and saying that precisely is worth more than "bound your recursion".

Also worth recording: the fuzz target was the thing that made this checkable at
all. `_, _ = Eval(input)` with no assertion beyond "this returns" is a complete
test of a boundary whose only contract is that panics do not escape it.

## 2026-09-25 — `./...` matches nothing from a Go workspace root

**Expected:** `go vet ./...` from the repo root would cover every module,
the way `pytest` from the Python repo root covers every folder.

**What happened:**

```
pattern ./...: directory prefix . does not contain modules listed in
go.work or their selected dependencies
```

The root of a workspace is not itself a module. `./...` is resolved against
modules, so with `go.work` listing only `./go-concepts`, the pattern starting at
`.` matches nothing at all. It is not an empty result either, it is an error,
which at least fails loudly.

**Next time:** a workspace Makefile has to ask the workspace what is in it:

```make
MODULES := $(shell go list -m -f '{{.Dir}}/...' 2>/dev/null)
DIR ?= $(MODULES)
```

That expands to an absolute path per module and keeps working as modules are
added to `go.work`, which matters here because this repo will end up with
roughly thirty of them. The alternative, hardcoding `./go-concepts/... ./dsa/...
./backends/...`, would need editing every time a module lands.

Worth teaching directly in lesson 15 rather than only fixing in the Makefile:
anyone adopting workspaces for a multi-module repo hits this within an hour.

## 2026-09-25 — The linter already knows about the typed-nil trap

**Expected:** lesson 03's typed-nil demo would need prose and a test, because
the trap is famously invisible to tooling. I had written the README section as
"two rules avoid it completely", implying vigilance was the only defence.

**What happened:** staticcheck flagged it immediately, by name. `SA4023:
brokenValidate never returns a nil interface value`, plus a marker on every
downstream comparison reading "this comparison is always true". It found both
shapes: the function returning a nil-valued concrete variable, and the one whose
concrete return type springs the trap at the call site instead.

**Next time:** before writing a "be careful about X" section, run the linters
over the broken example. If a tool catches X, the section should lead with the
tool and treat the rule as a fallback. The README now does, and the suppressions
in `typednil.go` exist only so the file can keep demonstrating the bug.

Two mechanical notes for future suppressions:

- `SA4023` anchors its "related information" diagnostics on the ASSIGNMENT line,
  not the comparison that is actually always-true. A `//nolint` above the
  comparison does nothing; it has to sit on the line the diagnostic names.
- `reflect.Ptr` is a deprecated alias and `go vet`'s inline analyzer now flags
  it. `reflect.Pointer` is the current spelling.

This is the same finding as the staticcheck entry below, arriving a second time
in a stronger form. The pattern is now clear enough to state as a rule for the
rest of this build: **write the broken example, run the linter, then write the
prose.**

## 2026-09-25 — Go's map randomisation is a rotation, not a shuffle

**Expected:** writing `TestMapIterationOrderIsRandomised` over a 5-key map, I
assumed 200 range passes would sample broadly from the 120 possible orderings,
and drafted a log line saying "N distinct orders out of 120 possible".

**What happened:** 200 passes produced 5 distinct orders. Every time. The
runtime randomises the starting bucket and the offset inside it, then walks
normally from there. A map small enough to live in one bucket therefore yields
rotations of a single walk, and 5 keys means 5 rotations.

**Next time:** the README had already been written claiming "a random start
offset per range", which is correct, and the test comment I wrote next to it
claimed something stronger that the same sentence contradicted. Writing an
assertion forced the arithmetic that caught it. The assertion stayed at
`>= 2 distinct orders`, because that is the actual language guarantee. Asserting
5 would have encoded a bucket-layout detail and broken on the next map
implementation change.

The wider lesson: a test that logs the real number is worth more than a test
that only passes. The log line is what exposed the gap.

## 2026-09-25 — A benchmark pair that measured two different workloads

**Expected:** `BenchmarkAppendGrowing` vs `BenchmarkAppendPreallocated` would
show the cost of letting a slice grow.

**What happened:** the growing side called `growthReallocates`, which appends to
*two* slices per iteration (the values, plus a second slice recording capacity
after each step). It was doing roughly twice the work, so the comparison
overstated the penalty.

**Next time:** benchmark pairs need to be built as pairs. The fix was a
dedicated `appendGrowing` that is `preallocated` with the `make` removed and
nothing else changed, plus a test asserting the two return identical slices so
they cannot drift apart later. Honest result on an M2 Max: 2513 ns and 12 allocs
growing, 529 ns and 0 allocs preallocated.

The zero there is worth a second look and is not the preallocation's doing: the
benchmark discards the result, so escape analysis keeps the backing array on the
stack. Revisit in lesson 18.

## 2026-09-25 — staticcheck flags the teaching examples, and it is right

**Expected:** a repo of deliberately-illustrative code would need a broad linter
exclusion for `go-concepts/`, since half of it demonstrates mistakes on purpose.
I pre-wrote one for `errcheck` in `.golangci.yml` on that assumption.

**What happened:** `errcheck` never fired. `staticcheck` fired six times, and
every hit was a real detection of the exact thing the file was teaching:

- `SA5000: assignment to nil map` caught the nil-map write in
  `01-types-and-zero-values/zerovalues.go`, which is the panic the README
  describes three paragraphs above it.
- `QF1011`/`ST1023` wanted `var i int = factor` reduced to `i := factor`. That
  would have deleted the demonstration: the whole point is one untyped constant
  landing in `int`, `float64` and `time.Duration` at three explicit types.

**Next time:** don't pre-write linter exclusions from a guess about which linter
will complain. Run it first, then suppress per line with a reason attached. A
blanket `path: go-concepts/` exclusion would have switched the check off across
eighteen lessons to quiet two files, and would have hidden real bugs in the
sixteen where the same pattern is not deliberate. Four `//nolint:staticcheck`
comments naming why beat one directory-wide rule.

There is a second, better outcome here: a linter that flags the teaching example
is evidence the example is realistic. Worth keeping the hits visible in the
README rather than silently suppressed.
