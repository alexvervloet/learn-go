# Lessons

Things that did not go according to plan while building this repo, written down
when they happened. The counterpart to `PLAN.md`, which is scratch and never
committed. This file is committed and stays.

## 2026-09-25 — Two pieces of escape-analysis folklore, both wrong

**Expected:** lesson 18's table of what escapes was going to be the standard
list, which I could write from memory: returning a pointer, boxing into an
interface, `make` with a non-constant size, capturing in a closure. Then I
added `testing.AllocsPerRun` assertions to prove each one.

**What happened:** two of them failed.

**"`make` with a non-constant size escapes."** Measured:

```
makeWithVariableSize(64)      0 allocations
makeWithVariableSize(100000)  1 allocation
```

Same function, same source line. Escape analysis runs *after* inlining, so the
compiler sees the caller's actual argument and keeps the slice in the frame
when it can prove the size is small. `-gcflags=-l` makes both escape.

**"Passing a value to an interface allocates."** This one took four attempts.

1. First measurement: the `any` path allocated 1, the concrete path allocated
   **2**. The two functions were doing different work, one using `fmt.Sprint`
   and the other `strings.Repeat`. Lesson 17's first lie, committed in lesson
   18 by the person who had just written lesson 17.
2. Fixed so both only read a field: both allocated **0**. The callee inlined,
   the compiler devirtualised the type assertion, and the boxing vanished.
3. Added `//go:noinline`: still **0**. The box was real, and escape analysis
   proved it never left the callee's frame, so it went on the stack.
4. Made the box actually escape: **1**. There it is.

And a fifth thing found on the way: `User{ID: 2}` has only constant fields, so
the compiler emits it as a static value and nothing allocates in ANY of the
four cases. Every measurement came back zero until the value was derived from
a variable.

**Next time:** the useful statement is not a list of things that escape. It is:

> Escape analysis is a property of a CALL, not of a function.

`testing.AllocsPerRun` asks about a call, which is why it belongs in the test
suite next to any claim about allocation. Reading `-gcflags=-m` tells you what
the compiler decided for the code as written; it does not tell you what happens
at a different call site.

Five corrections in this file now have the same root: I wrote down what I
believed, then measured, and the measurement disagreed. The rule stands and I
keep needing it — **write the demo, run it, then write the paragraph.**

## 2026-09-25 — The lesson about benchmarks lying contained a lying benchmark

**Expected:** lesson 17 documents three ways a benchmark misleads, the first
being "the two sides do different work". I wrote that section, wrote the
defence (a test asserting both sides produce identical output), and benchmarked
four string-joining implementations against `strings.Join`.

**What happened:** the hand-written presized `strings.Builder` came out at
**373 ns** against `strings.Join`'s **710 ns**. A 1.9x win for four lines of
code over the standard library, which is the sort of result that should be
suspicious and which I nearly wrote up as a finding.

The agreement test passed, because with an empty separator the output IS
identical. The work was not: `strings.Join` runs a separator branch on every
element, and my version had no separator at all.

Adding one that handles a separator, and comparing like for like:

| | ns/op |
|---|---|
| hand-written, no separator | 373 |
| `strings.Join(parts, "")` | 710 |
| hand-written, with separator | 675 |
| `strings.Join(parts, ",")` | 707 |

**1.05x. Noise.** The entire result was the missing feature.

**Next time:** the defence I had written down is not strong enough, and I now
know why because it failed on me. "Assert the two sides produce identical
output" catches the crude version of this lie and misses the interesting one:
two functions can agree on every input in the benchmark and still do different
amounts of work, because one handles a case the input never exercises.

The better question is **"do these two do the same job?"**, and nothing
automates it. What does help: treating a large win over the standard library as
a hypothesis rather than a result. The stdlib is not always fastest, and when a
four-line replacement beats it by 1.9x the first suspect is the benchmark.

Four for four now: every time this repo has claimed a performance result
without a fair comparison, the comparison was the problem. `appendGrowing`, the
buffered channel counter, `fmt` vs `strconv`, and now this.

## 2026-09-25 — I wrote "nothing catches this", and the linter caught it in the same file

**Expected:** lesson 14 warns about `// go:embed` with a space, which makes the
directive an ordinary comment and leaves the variable empty. I wrote that
nothing reports it: "not the compiler, not vet, not the linter", and built the
lesson's defence around a test asserting non-empty content.

**What happened:** `golangci-lint` failed the package, pointing at my own
**package doc comment**, which happened to contain the string
`// go:embed puts files into the binary`:

```
embedding.go:3:1: SA9009: ineffectual compiler directive due to extraneous
space: "// go:embed puts files into the binary at compile time:" (staticcheck)
```

The linter I had just written off caught the trap inside the paragraph claiming
it could not.

I checked it properly rather than assuming, with a three-line program:

| | Result |
|---|---|
| `go build` | compiles fine |
| `go vet` | silent |
| `golangci-lint` | **catches it** (SA9009) |
| at runtime | the variable is `""` |

So the claim was two-thirds right and wrong where it mattered.

**Next time:** this is the fourth entry in this file where running the tools
corrected the prose, after `sync.Map`, the HTTP deadline and the range-over-func
contract. The rule I wrote down at the third one still applies and I still did
not follow it here: **write the demo, run the tools, then write the paragraph.**

Worth noting what made this one findable: I had written the bad directive into
the prose, so the linter had something to point at. Had I only described it, the
claim would have shipped. There is an argument for putting the broken form in
the code deliberately, precisely so the tooling gets a chance to disagree.

## 2026-09-25 — Two build-tag files looked exhaustive and were not

**Expected:** lesson 14 demonstrates platform-specific builds with
`platform_unix.go` (`//go:build unix`) and `platform_windows.go`
(`//go:build windows`). Unix or Windows covers everything, so cross-compiling
the lesson to a few targets would be a formality.

**What happened:**

```
$ GOOS=js GOARCH=wasm go build ./14-embed-and-build-tags
platform.go:51:32: undefined: currentPlatform
```

Go builds for `js/wasm`, `wasip1/wasm` and `plan9`, none of which are `unix` and
none of which are `windows`. Neither file was compiled, so the variable both of
them declare did not exist.

The error message is the worst part. `undefined: currentPlatform` points at the
USE, and the declaration exists in two files sitting right there in the
directory. Nothing indicates that a build constraint excluded both.

**Next time:** a constraint set needs a fallback, or an explicit loud failure:

```go
//go:build !unix && !windows

func init() { panic("unsupported platform: " + runtime.GOOS) }
```

Nothing tells you the set is incomplete. Not the compiler, not vet, not the
linter, and not a CI matrix of Linux, macOS and Windows, which is exactly the
matrix this repo runs and which would never have caught it. The only thing that
finds it is building for the uncovered target, and `go tool dist list` plus a
loop is about thirty seconds.

With the fallback added, all seven targets build from one macOS machine, which
is the actual selling point the lesson was trying to make and nearly got wrong.

## 2026-09-25 — sync.Pool.Get is not guaranteed to return what you just Put

**Expected:** the `sync.Pool` demo in lesson 09 puts a buffer back without
resetting it, gets one, and shows the first caller's data still in it. A test
asserted that leak directly. It passed locally, repeatedly, with and without
`-race`.

**What happened:** it failed on CI, on the race job only, seven of eight jobs
green:

```
--- FAIL: TestForgettingResetLeaksData
    pools_test.go:80: second = "user-2-data", want it to still contain
                      the first caller's data
```

My first guess was that `-race` disables pool reuse. I wrote a twenty-line
program to check and the guess was wrong: it reuses fine under `-race`.

The actual reason is the pool's design. `sync.Pool` is a **per-P cache**. `Put`
stores into the current processor's slot; `Get` reads from the current
processor's slot. If the goroutine is rescheduled onto a different P between the
two, or a GC runs, `Get` misses and calls `New`. Race instrumentation changes
the timing enough to make that likely.

**Next time:** the test was asserting the scheduling, not the behaviour. The
behaviour is "a buffer that comes back from the pool still has its old
contents"; whether it comes back at all is the runtime's business. The fix
retries until reuse is actually observed and then asserts the consequence,
skipping with a note if 200 attempts never produce one.

This is the same shape as the map-iteration and parallel-speedup entries above:
**assert what the API guarantees, and treat everything else as an observation.**
Three times now, which suggests it is the default mistake rather than an
occasional one.

## 2026-09-25 — encoding/json beat my hand-written encoder

**Expected:** lesson 12's README said reflection costs roughly an order of
magnitude, and that `encoding/json` is "far slower than a hand-written
encoder". Standard knowledge, and I wrote a hand-written encoder to prove it.

**What happened:** `encoding/json` won. 309 ns against 330 ns for my version.

The cause is embarrassing and useful: my hand-written encoder used `fmt.Sprint`
for the numeric fields, and `fmt` is reflective and not fast. Meanwhile
`encoding/json` caches a field map per type on first use, so after the first
call it is doing indexed access, not name lookup.

The field-access numbers told the same story more precisely:

| Operation | ns/op | vs direct |
|---|---|---|
| direct | 26.9 | 1.0x |
| reflect by index | 30.3 | **1.1x** |
| reflect by name | 86.2 | 3.2x |

Reflection is not slow. `FieldByName` is slow. Once a library resolves names to
indexes the overhead is about ten percent, which is nothing like the order of
magnitude I had written.

**Next time:** two things.

When benchmarking "hand-written must be faster", check what the hand-written
version actually calls. Mine reached for `fmt` out of habit and inherited the
cost it was supposed to be avoiding. Worth noting which tool caught it: not the
benchmark, which was perfectly happy reporting a wrong conclusion, but
`staticcheck`'s QF1012 suggesting `fmt.Fprint` over `WriteString(fmt.Sprint())`.
The linter found a performance bug in a performance comparison.

And "X is slow" deserves the follow-up question "which part of X". The useful
advice that came out of this is not "avoid reflection", it is "avoid
`FieldByName` in a loop, and cache the field map per type", which is actionable
and which the vague version would never have produced.

The one measurement that did confirm the folklore: the tag-driven validator is
**99x** slower than the hand-written equivalent, 982 ns against 10 ns. Still the
right trade at once per request, and worth knowing the size of.

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

### Follow-up, same day: best-of-N was not enough, and two more surfaced

The best-of-5 fix did not work. The same runner reported **0.63x**, essentially
unchanged. That is the informative part: if averaging away noise does not help,
it was not noise. A shared runner advertising 4 CPUs does not deliver 4 CPUs in
parallel, so spreading the work costs more in scheduling than it recovers. The
test now logs the measurement and skips the assertion when `CI` is set, because
asserting something the environment cannot provide is not a test, it is a
coin flip. Locally, on real cores, it still asserts and reports 7.6-8.9x.

Two others came out of the same run, both the same mistake in different
costumes: **asserting more than the language guarantees.**

`TestManyGoroutines` asserted `peak >= before+n` exactly, and CI reported 10004
against an expectation of 10005. `before` is a snapshot of a number that moves.

`TestUnbufferedIsAHandshake` asserted a four-line event sequence. Only ONE
ordering is guaranteed: the receive completes before the send returns. Which
goroutine logs its "about to" line first is a race, and a 20ms sleep makes one
outcome likely rather than certain. Under `-race` on a loaded runner the other
one happened. The test now asserts the memory-model guarantee and nothing else.

Three failures, one root cause. When a concurrency test fails only on CI, the
question to ask first is not "what is different about that machine" but **"what
exactly does the spec promise here, and am I asserting more than that?"**

### A fourth, from lesson 18, on Windows

```
--- FAIL: TestMeasureGCImpactTakesTheBest
    pressure_test.go:62: measured 0s, want a positive duration
```

The test asserted that timing a garbage collection returns a duration greater
than zero. On Windows the clock resolution is coarser than a collection of 100
items, so `time.Since` returns exactly `0s`.

A monotonic clock promises that time does not go backwards. It promises nothing
about granularity, so "faster than the clock can measure" is a legitimate
result, not a failure. The test now asserts `>= 0` and logs the value, with a
second test at a million items where the duration is comfortably measurable on
any platform.

Same root cause as the other three, and the fourth time it has been the answer.
The pattern is now the first thing to check whenever a test fails only on one
platform.

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

## The famous slice-queue leak is not a leak

**Expected.** Writing the queue package I set out the standard three-way
comparison: `q = q[1:]` on pop grows memory without bound, `copy(q, q[1:])` is
O(n) per pop, and a ring buffer fixes both. I wrote that README before writing
the benchmark.

**What happened.** Two of the three claims were wrong, and the benchmark said so
immediately. `q = q[1:]` shrinks `cap` along with `len`, so `append` runs out of
capacity, reallocates, and lets the old array be collected. It is amortised O(1)
in time and O(n) in space, and it measured the same nanosecond count as the ring
buffer at every window size from 100 to 1,000,000 (4.7 to 5.5 ns/op for both). It
also *won* the fill-and-drain benchmark outright, 62 µs against the ring buffer's
111 µs, because it does not pay for modulo arithmetic or for zeroing the slot it
vacates. Only the shift-down version was as bad as advertised: 20x slower in
steady state, 83x on a drain.

The ring buffer's real advantage turned out to be the column I had not been
looking at. 0 B/op against a permanent 14 to 39 B/op, so at a million operations
a second the reslice version hands the collector tens of megabytes a second of
garbage and a growing live array to scan. There is also one genuine retention bug
in the reslice version, which is not the one everyone repeats: a slice keeps its
*entire* backing array alive, so a queue filled to a million and drained to one
element reports `cap` 1 while the million-element allocation is still live, and
the vacated slots are unreachable from any slice you hold so you cannot clear
them.

**Next time.** This is the same rule as four earlier entries, so it is clearly
not learned yet: write the demo, run the tools, then write the paragraph. The
specific trap here is that the claim was *received wisdom*, which made it feel
verified when it was not. A number I got from a blog post is not a measurement.
Worth adding: when a benchmark contradicts the story, the interesting write-up is
the contradiction, not a quietly deleted paragraph.

## A measurement tool needs a known-answer test before you trust a number

**Expected.** The hash map package needed two instruments: `Measure`, which scores
how evenly a hash function spreads keys, and `CollidingKeys`, which generates keys
that deliberately collide so the hash-flooding attack can be priced. Both are
twenty lines. I wrote them, wrote the tests that use them, and read the numbers.

**What happened.** Both instruments were wrong, and neither failure looked like a
failure.

`Measure` reported a chi-square ratio of `0.000` for a good hash and `0.0` for a
terrible one. I had normalised the raw crowding score inside `Measure` and then
divided by the key count again in `Ratio()`. The correct answers were 1.005 and
45.5, so the bug did not just scale the numbers, it destroyed the entire signal
the function existed to produce.

`CollidingKeys` built keys by moving a `b` around strings of *growing* length.
Under a byte-sum hash the sum is `length * 97`, so keys of different lengths do
not collide with each other. Instead of one chain of 2,000 keys it produced a
dozen short chains, and priced the attack at 25 probes per insertion. With all
keys the same length it is 1,000 probes per insertion, so the instrument
understated the thing it was built to demonstrate by 40x.

The same shape a third time, in the map itself. `resize` used
`slotsFor(count*2)`, but `slotsFor` already divides by the load factor, so the
doubling was applied twice and the table grew 4x per resize. Nothing failed. The
map was correct, the tests passed, and 100 keys sat in 512 slots at a load of
0.20. An example asserting the printed capacity is what caught it.

**Next time.** Give every measurement function a test with an answer known in
advance, before using it to measure anything. For `Measure` that is a hash that is
provably uniform and one that is provably terrible, asserted against 1.0 and
"much greater than 1.0". For `CollidingKeys` it is the one-line invariant that
every key hashes the same, which I did eventually write, and which would have
caught it in the first minute. And when a tool reports that something is fine,
sanity-check the magnitude: the reason all three of these survived is that
`0.000`, `25` and `512` all looked plausible enough not to question.

## I optimised the wrong 4%

**Expected.** The trie's `collect` built a fresh `strings.Builder` for every child
node, and I left a comment saying the faster alternative was "less clear" and that
`Complete` was "not in anyone's hot path". When the benchmark showed
`Complete("pre")` taking 515 µs and doing 10,473 allocations for 2,359 results, I
rewrote it to reuse one `[]rune` buffer and expected the time to fall with the
allocations.

**What happened.** Allocations fell 4.4x, to 2,373, and bytes fell from 309 KB to
137 KB. The time went from 515 µs to 492 µs, which is 4%. I had written "18x" into
the code comment as the cost of the old version before measuring the new one.

Breaking the call down: walking the subtree is 292 µs, sorting the results is
174 µs, and collecting them into a slice is 18 µs. String building was never the
problem. The walk is slow because it iterates a `map[rune]*node` at every one of
about 5,000 nodes, and the sort exists only because map iteration order is random.
Both costs come from the same decision, and swapping to `[26]*node` children makes
lookups 8x faster *and* removes the need to sort, because array order is already
alphabetical.

The bigger miss was not benchmarking the competition. A sorted slice with
`slices.BinarySearch` does the same query in 28.6 µs, 17x faster than the trie,
and allocates 13 times instead of 2,373 because it returns strings it already
holds. I had written most of a README arguing for the trie before finding that out.

**Next time.** Two rules, and neither is new.

Profile before optimising, even on something this small. A 20-line function still
has a distribution, and mine was 60/36/4 with my attention on the 4.

Benchmark the boring alternative before writing the paragraph that says why the
clever thing is better. The trie still earns its place, on `Count` at 10 ns
against 172 µs, on wildcard matching, and on `LongestPrefixOf`. But the argument I
was about to make, that it is the right tool for autocomplete, was wrong, and the
only reason it did not ship is that a sorted slice took four lines to add to the
benchmark.

## Three wrong benchmarks before one right one

**Expected.** The BST's in-order walk should use an explicit stack rather than
recursion, because a degenerate tree is a chain and recursing down 100,000 nodes
would blow the stack. I wrote that in a comment, wrote the explicit-stack version,
and benchmarked it against a recursive one.

**What happened.** The premise was wrong and each of the first three measurements
was wrong in a different way.

The premise: Go grows a goroutine stack on demand up to 1 GB. A chain deep enough
to overflow it needs tens of millions of nodes, and a BST that degenerate is
already unusable for every other reason. The stack was never the risk.

Measurement one had the explicit stack 2x *slower*, which I nearly wrote up as
"recursion wins". It was slower because I sized the stack with
`make([]*node, 0, max(t.Height(), 1))`, and `Height()` walks the entire tree. Every
call to `All()` was doing an extra O(n) pass and allocating 400 KB on a deep tree.
Removing that one line took sorted iteration from 1.83 ms to 997 µs.

Measurement two had the explicit stack 3x *faster*. The recursive version went
through `iter.Seq2` and the stack version took a plain `visit func(K, V)`
callback, so I was pricing range-over-func rather than the traversal. Running both
through `iter.Seq2` closed it to 5%.

Measurement three used only one degenerate shape. Ascending keys make a chain of
right children, which is the explicit stack's *best* case: it never holds more
than one node. Adding the descending case, a chain of left children, reversed the
result completely, at 766 µs and 2.2 MB against recursion's 459 µs and zero.

The final answer is recursion, within 5% on balanced trees, never allocating, and
only beaten on one of the two degenerate shapes.

**Next time.** Three separate rules, all of which I already knew.

Check the premise before optimising for it. "Recursion will blow the stack" is a C
habit, and Go's stacks are not C's.

When two implementations are compared, diff their call paths, not just their
bodies. One went through an iterator and one did not, and that was the entire
3x.

One pathological input is not the pathological input. Ascending and descending
keys produce mirror-image trees with opposite performance, and I had measured one
of them.

Also worth keeping: the honest ranking that came out of the same benchmark. A
sorted slice with `slices.BinarySearch` beats this tree at lookup, iteration and
range queries. The tree earns its place only because a sorted slice costs O(n) per
insertion. That belongs at the top of the README, not buried.

## A benchmark found an algorithmic bug three tests could not

**Expected.** The quicksort in `sorting/` had the three things pdqsort has: a
median-of-three pivot so sorted input splits evenly, three-way partitioning so
duplicates cost O(n), and a depth limit that bails out to heapsort. Tests covered
sorted, reversed, all-equal, three-valued and organ-pipe input at 50,000 elements,
plus 10 million sorted elements to check the stack. All green.

**What happened.** The benchmark showed quicksort taking 16.6 ms on sorted input
and 11.4 ms on random input. Sorted input should be the *easy* case for a
median-of-three pivot, so the 46% was the only sign that anything was wrong, and it
turned out to be hiding two separate bugs.

The first was the depth limit. It read `if depth > 2*ilog2(len(s))`, recomputed from
the current subslice, so the allowance *tightened* as the recursion descended while
the depth grew. A 13-element subslice allows 6 partitions and a balanced quicksort
reaches 13-element subslices at depth 13, so the limit fired on every input:
1,909 unintended heapsort calls on 100,000 random elements. The fix is to compute it
once from the original length.

The second was worse, and finding it took an instrumented copy of the function and
then a printed trace of the partition sizes. Three-way partitioning moves large
elements to the tail by swapping them with the shrinking right boundary. On sorted
input that rotation leaves the right half sorted ascending *except that the smallest
element is now last*. Median-of-three samples the first, middle and last of that, so
one of its three samples is the minimum and the median of the three is the
second-smallest value in the subarray. The split is 1/1/497, the same thing repeats
at every level, and the recursion depth goes from log(n) to **√n**: 105 partitions
for 10,000 sorted elements against 18 for random ones.

The answer was correct throughout. The sort was right, the tests were right, and the
algorithm was quietly quadratic-ish on the most common shape real data has. Tukey's
ninther fixed it, taking the deepest path from 105 partitions to 14.

**Next time.** Four things.

A correctness test suite cannot find a complexity bug. Both of these produced correct
output on every input. If an algorithm has a performance contract, the contract needs
a test, and the test has to measure something structural rather than time. The one I
added counts how often the fallback fires and asserts zero on natural input, which
would have caught the first bug in a second.

Make the thing you want to observe a parameter. `quickSort` now takes its fallback as
a `func` argument instead of calling `HeapFunc` directly, which is what makes the
count testable. That is a one-line design change that turns an invisible property
into an assertable one.

When a benchmark result is the wrong way round, stop and find out why. My first
instinct was that sorted input must somehow be slower for cache reasons and to write
that down. Two of the three hours this cost were spent because I went looking for a
plausible story before going looking for the cause.

Received algorithmic wisdom has preconditions. "Median-of-three makes sorted input
safe" is true, and it is true for a two-way partition. I combined it with a
three-way partition and the interaction between them broke it. Bentley and McIlroy
use the ninther in their engineered quicksort for exactly this reason, and the
reason is in their paper, which I had not read.

## The Go rebuttal to the binary-search overflow is wrong

**Expected.** Writing `mid := lo + (hi-lo)/2` in the searching package, I started to
add the usual comment: that `(lo+hi)/2` overflows, but that in Go it cannot, because a
slice long enough would need exabytes of memory. That is the standard rebuttal and I
have repeated it before.

**What happened.** I checked it before writing it down, and it is false. A slice of
**zero-size elements** needs no memory at all, and `make([]struct{}, 3<<61)` is legal:
6.9 quintillion elements, allocated instantly, costing nothing. With that length
`(lo+hi)/2` wraps to `-4035225266123964416` and indexing panics with
`index out of range [-4035225266123964416]`.

So the bug is reachable in Go. You will never have such a slice, and the habit is now
defensible on its own terms rather than inherited from Java.

**Next time.** Two things. A one-file scratch program took ninety seconds and turned a
piece of folklore I was about to pass on into a test that demonstrates the opposite.
Cheap to check, and the check is now `TestMidpointOverflowIsReachable` rather than a
paragraph asserting something.

The other: `struct{}` having zero size is the kind of language detail that makes
"this cannot happen" claims unsafe. Any argument of the form "the length cannot get
that large because of memory" needs to account for element types that occupy none.
