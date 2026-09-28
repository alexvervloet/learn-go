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

## Two hypotheses about performance, both wrong, both cheap to test

**Expected.** Writing the generic heap in `patterns/heap`, I asserted in the package doc
that it would be faster than `container/heap`, because `container/heap` deals in `any`
and needs a type assertion per pop while a generic version does not. Separately, when
the k-way `MergeSorted` lost to concatenate-and-sort from k=10, I reached for cache
thrashing across k memory streams as the explanation.

**What happened.** Both were wrong, and each took one benchmark to disprove.

`container/heap` is **1.46x faster**: 1.39 ms against 2.04 ms for 10,000 pushes and
pops. It calls `Less` on a concrete type, which the compiler devirtualises and inlines
to `h[i] < h[j]`. My generic heap calls `h.compare(a, b)` through a func field, which it
cannot. That is the same 1.48x the sorting package had already measured between
`slices.Sort` and `slices.SortFunc`, and I had written that measurement up a few hours
earlier without connecting it.

The cache hypothesis was testable in one extra benchmark: shrink the total to 1,024 ints
so everything fits in L1. If cache were the mechanism, the heap should close the gap.
It went the other way: 8.5x slower at k=100 on 8 KB of data, against 1.75x on 32 MB.
The real cause was the comparison again, with the heap paying an indirect call plus a
double slice index per comparison against an inlined `<`, roughly 7x, putting the
break-even at k around 6.

**Next time.** The specific lesson is that **an indirect call per comparison is the
dominant cost in every comparison-driven structure in Go**, and it is now the third
place this repo has measured it at about 1.5x. That should be the first hypothesis, not
the last.

The general lesson is better: a hypothesis about performance that can be tested by
changing one parameter should be tested before it is written down. The cache theory was
plausible, well-formed, and disprovable by one number, and I had already typed it into a
doc comment as fact. Writing "my first guess was X, and that is wrong because Y" is
worth more to a reader than the correct explanation alone, because the wrong guess is
the one they will also have.

## A solver that only checks its own moves cannot reject an impossible input

**Expected.** The sudoku solver in `patterns/backtracking` validates every digit before
placing it, so I wrote a test handing it a grid with two 5s already in the same row and
expected an instant `false`.

**What happened.** The whole package test run hit a 110-second timeout, and bisecting it
pointed at that one test. The solver only ever validates digits **it places itself**. The
conflicting 5s were givens, so nothing ever looked at them, and the solver set off to
explore the 79 remaining empty cells of a grid with no solution. It would have finished
eventually. Not in this decade.

The fix is one pass over the givens before starting, which is 81 cells times 9 digits and
costs nothing against a search that can run for seconds.

**Next time.** The general shape: **a constraint checker that only sees the decisions the
algorithm makes has a blind spot exactly where the input is.** The same gap exists in any
solver, parser or state machine that trusts its starting state, and the symptom is not a
wrong answer, it is a hang, which is why it survived every other test in the file.

Worth noting how it was found. The test was written to assert a behaviour I was confident
about, and the assertion never ran. A test that hangs is worse than one that fails, because
the failure mode looks like a slow machine rather than a bug, and my first instinct was to
go looking for an accidentally huge benchmark.

Two smaller things from the same session, both found by randomised tests over inputs with
duplicates, where the hand-written table used distinct values because the textbook statement
of the problem does: `CombinationSum` returned the same multiset twice given duplicate
candidates, and two hand-traced paths through a 3x4 word-search grid were both wrong. The
rule that keeps earning its place: if a function takes a collection, generate test inputs
with repeats in them, because the examples in the problem statement never do.

## Fixing one overflow uncovered two more in the same function

**Expected.** `searching.SearchAnswer` bisects an inclusive `[lo, hi]` range. It worked, it
had tests, and it had been used by the binary-search-on-the-answer pattern package without
complaint. Then a test asked for `IntegerSquareRoot(math.MaxInt)`.

**What happened.** It returned `math.MaxInt`, confidently, with no error. Three separate
overflows, each hidden behind the one before it.

The **sentinel**. The signature returned `hi+1` to mean "nothing found", which wraps to
`math.MinInt` when `hi` is `math.MaxInt`. Changed to `(int, bool)`.

The **range width**. The implementation shifted `[lo, hi]` into `[0, n)` by computing
`hi - lo + 1` so it could reuse `Partition`. That overflows for any range wider than
`MaxInt`, and the guard `if n <= 0` then returned the sentinel, so the function reported "no
answer" for a range containing every non-negative integer. Rewritten to bisect `[lo, hi]`
directly.

The **midpoint**. With those two fixed, the test suite hung. `lo + (hi-lo)/2` is the
canonical safe midpoint, the one I had written a whole README section about earlier in this
same repo, and it is not safe over an arbitrary signed range: for `lo = math.MinInt` and
`hi = 0` the difference is 2^63, which does not fit in an `int`, so `mid` lands outside the
range and the loop never shrinks. The fix is to subtract in unsigned arithmetic,
`int(uint(lo) + (uint(hi)-uint(lo))/2)`, where the difference always fits.

**Next time.** Two things.

**A safe idiom is safe for a domain, not in general.** `lo + (hi-lo)/2` is safe for slice
indices, which are non-negative, and that is the context everyone learns it in. Moving the
same loop from "index into an array" to "any integer" changed the domain and silently
invalidated the idiom. I had the earlier lesson about the midpoint overflow being reachable
in Go written down two days of work ago, and still applied the fix from that lesson to a
case it does not cover.

**Sentinel returns are a source of this whole family of bug.** `hi+1`, `-1`, `len(s)`: each
is a value that must not collide with a real answer, and at the extremes of a type they
always can. `(value, bool)` costs one identifier at the call site and removes the question
entirely.

Also worth keeping: the second and third bugs were only reachable because I fixed the first.
Each fix moved the failure one layer down. That is normal for overflow work and it is worth
expecting, rather than treating the first green test run as the end.

## The container API was the wrong shape, and it cost more than the algorithm saved

**Expected.** `patterns/trie`'s word search walks a grid and a trie at the same time, so a
grid path that is not a prefix of any dictionary word is abandoned at the first character.
That is the whole pattern, and it is meant to beat running a separate single-word search per
dictionary word by a wide margin. I built it on `dsa/trie`, the package that already had a
tested prefix API.

**What happened.** It lost. At every dictionary size, on every grid, the trie version was
slower than just searching for each word separately: 72 µs against 9 µs for ten words, 1.65 ms
against 361 µs for a thousand.

The cause was the API, not the algorithm. `dsa/trie` deliberately exposes no nodes, so the
search had to carry the prefix **string** and call `HasPrefix` and `Contains` on it. Each of
those re-walks the prefix from the root, so a path of length k cost O(k) map lookups per cell
instead of one. Adding a twelve-line node-based trie inside the pattern package and carrying
a `*node` made it 6.2x faster.

Then the second finding, which only appeared once the first was fixed: even done properly,
the trie version **loses on small grids with short words** and wins by about 2x on large ones.
A per-word search also abandons a path on the first mismatched character, and it stops at the
first occurrence while the trie version explores every path. The order-of-magnitude win the
pattern is usually sold with is not there.

**Next time.** Two things, and the second is the one I keep relearning.

**A good API for a container is not automatically a good API for an algorithm.** `dsa/trie`
is right to hide its nodes: it is a container, and exposing internals to make one consumer
faster is how containers rot. The right answer was a second, smaller trie inside the package
that needs it, not a worse `dsa/trie`. Twelve duplicated lines beat a leaked abstraction.

**Benchmark the regime where the pattern loses, not just the one where it wins.** My first
configuration was an 8x8 grid with short words, chosen because it was the example in the
problem statement, and it happens to be the regime where the trie is the wrong choice. Had I
only run the large configuration I would have reported a clean 2x win and never learned there
was a crossover. The benchmark now runs both on purpose, because one showing only the winning
case is an argument rather than a measurement.

## I wrote the correct version of a bug thirty lines from where I imported the bug

**Expected.** The `http-tutorial` module picks chi's middleware with the standard library's
router, on the reasoning that chi's router has been largely redundant since Go 1.22 and its
middleware has not. `Production()` assembled a chain from both.

**What happened.** Two of chi's middleware had to be replaced, and neither was found by reading
the code.

`chimw.CleanPath` **panics** with a nil pointer dereference on every request when there is no chi
router in the chain, because it writes the cleaned path into `chi.RouteContext` and there is no
route context without chi's router. A benchmark crashed. I then tested all seventeen of chi's
middleware against a bare stdlib handler, and it is the only one that does this, which made the
finding worth a test of its own rather than a quiet workaround.

`chimw.RealIP` is **deprecated as IP-spoofable**, with three GitHub advisories against it. It
takes the *leftmost* `X-Forwarded-For` value, and a proxy appends, so the leftmost entry is
whatever the client sent. `staticcheck` found it.

The part worth writing down: I had already written the correct version of that exact logic in the
same module. `request.ClientIP` takes the rightmost entry and has a paragraph explaining why,
because a proxy appends and an attacker prepends. I wrote that, then imported `chimw.RealIP`
into the production chain thirty lines later without noticing they were the same decision made
two different ways.

**Next time.** Two things.

**A dependency's function does not inherit the reasoning I applied to my own.** I had the
argument, in writing, in the same package. What I did not do was ask whether the library
function I reached for made the same choice. That is a specific habit to build: when I write a
careful version of something and then also use a library's version, check they agree.

**`staticcheck`'s SA1019 is a security tool, not a tidiness one.** I have been treating
deprecation warnings as noise to clear at the end of a module. This one carried three CVEs and a
one-paragraph explanation of the vulnerability in the deprecation notice itself. Reading the
message rather than just satisfying the linter is what turned it into a finding.

## My own worker pool deadlocked, and the arithmetic was 2n not n

**Expected.** `testing-concepts/concurrency` needed a worker pool with enough moving parts to test:
shared state for the race detector, owned goroutines for the leak check. I wrote one with a bounded
results channel, then a test that submits twenty jobs to a pool of four and drains afterwards.

**What happened.** The test hung until the 120-second timeout. The bounded results channel holds
four, so once it fills the workers block trying to deliver, nothing reads from the jobs channel, and
`Submit` blocks forever. The obvious usage order — submit, then read — deadlocks.

Then I wrote a test to pin the contract down and got the arithmetic wrong. With n workers and an
n-slot buffer, `2n` submits succeed before the next one blocks: n results fill the buffer, and n more
are picked up by workers that then get stuck trying to deliver. I asserted it wedged after n, and the
test failed by reporting success.

**Next time.** The design lesson first: **a pool whose obvious usage order deadlocks is a bad API**,
and the honest response is not a comment. I shipped both — `TestPoolDeadlocksIfResultsAreNotDrained`
so the contract cannot be rediscovered, and `Collect`, which starts the drain before submitting so
there is nothing to get wrong. Documenting a trap and providing the version without it are different
amounts of help.

The smaller lesson is about counting buffer capacity. "How many sends fit before this blocks" is
capacity **plus** the number of receivers currently able to take one, because a receiver that has
taken a value and is blocked delivering it has consumed a slot from the sender's point of view. I
have got that wrong before with a `chan` handoff between stages and will again; the fix is to write
the blocking test rather than reason about it.

Two smaller things from the same module, both the same shape as earlier entries. I wrote five
coverage percentages into the README before measuring, and all five were wrong — `basics` was 100.0%
and I guessed 76.9%. And `golangci-lint` flagged `buggyDecode` as unused, because its only caller is
behind a build tag; the fix was a documented `//nolint`, but the interesting part is that a coverage
target would have pushed me to delete the most instructive file in the package to get the number up.

## A test that mixes a DDL transaction with a pool query deadlocks against itself

**Expected.** `dbtest.Tx` gives a transaction that rolls back, and `dbtest.Pool` gives the shared
pool. A test can use both: the transaction for changes it wants undone, the pool for read-only
queries that do not care.

**What happened.** `TestPartialIndexIsSmaller` ran `REINDEX INDEX idx_orders_pending` inside the
transaction, then ran an `EXPLAIN` of a query on `orders` through the pool. It hung until the 120s
timeout. `pg_stat_activity` showed it exactly: one backend `active`, waiting on `Lock`/`relation`,
and one `idle in transaction`. The REINDEX holds `ACCESS EXCLUSIVE` on the index until the
transaction ends, which is when the test ends, and planning a query on `orders` needs `ACCESS SHARE`
on the same index. So the pool query waited for the transaction, and the transaction waited for the
test, which was waiting for the pool query.

**Next time.** Once a test opens a transaction that does DDL, every statement in that test goes
through the transaction. A transaction is not just an isolation trick, it holds locks, and the other
connection in the same test is as much a stranger as another process. When a database test hangs,
`SELECT pid, state, wait_event_type, wait_event, query FROM pg_stat_activity` names the culprit in
one query.

## An index built during a bulk load is 43% slack

**Expected.** The partial index `WHERE status = 'pending'` covers 1/7 of the orders, so it should be
roughly 1/7 the size of the equivalent full index.

**What happened.** It was 47%, not 14%. The reason is not the partial predicate. `CREATE INDEX` sorts
the entries and packs the leaf pages to about 90% fill, while an index that already exists during a
bulk load grows one row at a time and splits pages as it goes. `REINDEX` took it from 57344 bytes to
32768 for 680 entries. 27% of the full index, and the remaining gap over 14% is the metapage and
root, which every index pays whatever its size.

**Next time.** Drop the indexes before a bulk load and create them after, which is faster to load and
gives a smaller index. When comparing two index sizes, check they were built the same way first.

## `go test ./...` runs packages concurrently, and one shared database is not enough

**Expected.** Rollback isolation through `dbtest.Tx` handles test isolation, so the packages in the
module can all point at `learn_go_db`.

**What happened.** `go test ./...` passed for each package alone and failed when run together. `seed`
reported `deadlock detected` on its TRUNCATE, and `indexes` reported a Seq Scan where an index scan
had been verified minutes earlier. Both are the same cause: `go test` builds one binary per package
and runs up to GOMAXPROCS of them at once, and two packages that truncate and reload the same tables
are two processes editing one file.

The second symptom is the dangerous one. The deadlock is loud. A query plan that silently changes
because another package emptied the table is a test that reports a wrong fact about Postgres, which is
worse than a failure.

**Next time.** A database test harness needs isolation at two levels, not one: a transaction per test,
and a database per test binary. `dbtest` now derives a name from `os.Args[0]` (which ends in
`<pkg>.test`) and creates `learn_go_db_<pkg>` on first use. `CREATE DATABASE` has no `IF NOT EXISTS`
and cannot run in a transaction, so it is a check-then-create, and two packages starting together both
try, so `42P04 duplicate_database` counts as success. It costs about 120ms per package and it keeps
`-p` at its default.

## `-benchtime 200x` on a benchmark that does I/O measures nothing

**Expected.** A fixed iteration count keeps a slow benchmark suite short, and 200 iterations of a
1ms operation is 200ms of samples, which felt like plenty.

**What happened.** `BenchmarkFanOut/Join/1` reported 468µs at `-benchtime 200x` and 90µs at
`-benchtime 2000x`. I had already started writing the conclusion that a join is 7x slower than two
queries at low fan-out, from the 200x number, which was noise.

**Next time.** Use the default `-benchtime 1s` and `-count=3` and read the spread, which is what the
default is for. Fixed iteration counts are for making a benchmark reproducible once you know the
variance, not for making the suite finish sooner.

## A cached prepared statement made the same query 6x slower, and it was Postgres, not Go

**Expected.** `BenchmarkFanOut/Join/1` should report the same number wherever it runs in the suite.

**What happened.** 93µs on its own, 552µs when it ran after `BenchmarkStrategies`. Same process, same
data, same code. `GOGC=800` changed nothing, so it was not the garbage collector. Adding
`plan_cache_mode=force_custom_plan` to the connection string removed it completely: 94.6µs against
573µs.

The cause is Postgres's plan cache. pgx prepares every statement and caches it per connection by SQL
text. Postgres plans a prepared statement with the real parameter values for five executions, then
compares their average cost against a GENERIC plan built without the parameters, and if the generic
plan looks no worse it switches permanently. `BenchmarkStrategies` ran the join with `LIMIT 50`, which
made Postgres adopt a generic plan; `BenchmarkFanOut` ran the identical SQL with `LIMIT 1` and got
that plan.

`EXPLAIN EXECUTE` under both modes shows exactly what changed. The custom plan is a Nested Loop with
a Bitmap Index Scan on `idx_books_author`, 0.033ms. The generic plan is a Hash Join with a **Seq
Scan** on `books`, 0.638ms, 19.3x slower, because without the parameter Postgres guesses the `LIMIT`
will return 1000 rows where the real answer is 38.

**Next time.** Two things. A benchmark that shares a connection pool with other benchmarks is not
isolated, so measure each one alone before believing a comparison. And this is a production failure
mode, not a benchmark artefact: it is the shape of "one endpoint got slow after a deploy and recovered
when we restarted the pods". Parameter-dependent selectivity plus a prepared statement is the
ingredient list, and a parameterised `LIMIT` or a tenant ID with wildly uneven row counts is the
trigger. `nplusone.TestGenericPlanRegression` pins it.

## Two things about transactions that only turned up by writing the test

**Write skew needs each individual write to be legal.** My first attempt had two transactions each
withdraw 600 from an account holding 500, with a `CHECK (balance_cents >= 0)` on the table. Both failed
with 23514 at every isolation level, and the anomaly never appeared. Write skew is precisely the case
where nothing individually breaks: each transaction reads a set of rows, checks a rule about the set,
writes a row nobody else wrote, and the rule ends up broken. The working version has each transaction
withdraw 300 from its own account while checking that the pair still holds 600 between them. At
REPEATABLE READ both commit and the pair ends at 400. At SERIALIZABLE one gets `40001 could not
serialize access due to read/write dependencies among transactions`.

**Committing an aborted transaction is not an error in Postgres, and pgx makes it one.** After a failed
statement, every later statement in the transaction returns `25P02`. If the code then calls COMMIT,
Postgres accepts it and performs a ROLLBACK instead, silently. pgx notices and returns
`pgx.ErrTxCommitRollback`. So a handler that swallows one statement's error and commits at the end does
get told, which is better than the alternative and is not behaviour I would have assumed.

**What it cost to know.** `pgx.Tx` has `Begin`, not `BeginTx`, so one `WithTx` helper cannot serve both
the pool and the savepoint case. That is not an oversight: a savepoint has no isolation level of its
own, because the isolation level belongs to the whole transaction. The signature is telling you
something true.

## "Do it in the database" did not survive being measured

**Expected.** A running total computed by a window function beats fetching the rows and looping in Go,
and the gap widens with the row count. That is the advice everywhere, and I wrote it into the package
doc before measuring.

**What happened.** The Go loop is faster at every size: 1.5x at 10 customers, 2.1x at 100, 1.6x at
1000. Both return identical results. The window function sends four extra columns per row, and on a
unix socket that costs more than the accumulation saves.

So I built the case it was supposed to win, where the window function FILTERS and fewer rows cross the
wire: top 3 orders per customer, 2,830 rows instead of 5,000. Still slower. 3.39ms against 2.35ms. What
it does win is memory: 932 KB and 5,679 allocations against 1.92 MB and 10,021.

**Next time.** The advice is a claim about a REMOTE database, where rows on the wire set the latency,
and it does not transfer to a local socket. Same trap as the N+1 measurement in the same module, from
the opposite direction: locally, network cost is near zero, so anything justified by network cost looks
wrong and anything justified by CPU looks right.

The rule that comes out of both: for a database comparison, assert the machine-independent quantity
(round trips, rows transferred, plan node types) and log the timing next to it. Then the test says
something true on a laptop and in CI and on a managed database, and the reader can do the arithmetic for
their own latency.

## A GIN index on 10,000 rows is never used, and the planner is right

**Expected.** The books table has a generated `tsvector` column and a GIN index on it, so
`search @@ websearch_to_tsquery(...)` uses the index. Assert that and move on.

**What happened.** Three versions of the test failed. With `LIMIT 20` the planner scanned, because
'quick river' matches 137 of 10,000 titles and a scan finds 20 of them after reading 1,320 rows. With
`count(*)`, where every match must be found, it still scanned: the table is 1,632 kB, which is a few
hundred page reads.

`SET LOCAL enable_seqscan = off` forces the index path so both can be priced. The estimate says the
index costs 534 against the scan's 329, a 1.6x penalty. The clock says 0.79ms against 0.81ms, which is
a tie.

**Next time.** Two things worth keeping. An index test needs a table big enough for the index to win,
and 10,000 rows is not it for GIN. And cost units are not milliseconds: they come from a model whose
constants default to spinning-disk assumptions, `random_page_cost` at 4.0 where an SSD is nearer 1.1.
That one setting is the most common planner tuning change there is, and this is what it looks like from
the inside.

The measurement that did survive: reading the stored generated column is 18x faster than recomputing
`to_tsvector(title)` per row, 0.81ms against 14.68ms, and that gap has nothing to do with any index.

## `pgxpool.Pool.Close` blocks until every connection is released

**Expected.** A test that deliberately leaks connections, asserts the pool is exhausted, and closes the
pool in `t.Cleanup` is a clean test.

**What happened.** Every assertion passed and then the run sat there for 156 seconds until I killed it.
`Close` waits for acquired connections, the leaked ones are never released, and the connection is
reachable only from the pool so nothing in the test can release it. From the outside it looks exactly
like the test hanging rather than the cleanup.

That is the right behaviour, not a bug: `Close` waiting is what drains in-flight queries during a
graceful shutdown, the same decision `http.Server.Shutdown` makes. The consequence is the part worth
keeping. A leaked connection does not only exhaust the pool, it also blocks shutdown, so a service with
one leak has to be killed rather than stopped, and "we had to SIGKILL the pods" is a symptom of a
missing `defer conn.Release()`.

**Next time.** When a database test hangs after its assertions pass, suspect the cleanup before the
test body. `pgxdemo.TestClosingAPoolWithLeakedConnectionsBlocks` now pins the behaviour deliberately,
with a 200ms timeout instead of a hang.

**Also measured, and larger than expected.** `MinConns: 0` against `MinConns: 5`, five acquires: 8.5 to
16.3ms cold against 1.3 to 2.0ms warm, over a unix socket with no TLS. I assumed handshake cost would be
invisible locally. It is five to ten times, and on a managed database over TLS it is the first request
after a quiet period showing up as a p99 spike that gets blamed on the database.

## Four things pgvector does that nothing warns you about

**`hnsw.ef_search` below the `LIMIT` silently returns fewer rows.** `LIMIT 50` with `ef_search = 10`
returns 10 rows. Not 50 worse rows: 10 rows, no error, no warning. `ef_search` is the size of the
candidate list the graph walk keeps, and the index cannot return more than it kept. pgvector's default is
40, so any query asking for more than 40 results is quietly truncated until someone raises it.

**Turning `ef_search` up far enough turns the index off.** At 400 the planner's cost estimate for the
index exceeded a sequential scan, so it scanned. Recall read 100% and the query got 25x slower. "Raise
ef_search until recall is acceptable" can silently stop using the index, and the plan is the only place
that says so.

**Recall by ID is meaningless when the data has ties.** My first measurement read 20%, 74%, 56%, 100% as
`ef_search` rose, which is not a tradeoff curve. The seeded titles come from 8 adjectives and 8 nouns, so
the exact top 50 held 4 distinct distances and "the exact top 50" was an arbitrary 50 of hundreds of
equidistant rows. Comparing against the distance of the worst row in the exact answer, with a 1e-6
tolerance for float32 non-associativity, gives 20%, 80%, 100%, 100%.

Ties also explain why L2 and cosine returned different first rows while agreeing on every distance. Third
time in this module that a missing tiebreaker caused a surprise, after keyset pagination and the ROWS
window frame. The difference: vector search cannot have one, because adding `, id` to the `ORDER BY`
stops the expression matching the index.

**An IVFFlat index built before the data is twice as big and twice as slow, and recall does not show
it.** I expected the one-cluster index to be smaller, having no centres to store. 33.3 MB against 16.9,
and 0.33ms against 0.17ms per query. It was grown one INSERT at a time rather than built, so it carries
the same page-split slack as the partial index earlier in this module, and a probe walks all of it
because there is nothing to skip. Recall was 100% both ways.

**And a savepoint does not isolate the parent from the child.** The first version of that comparison used
`tx.Begin` to build the second index, and `tx` saw the savepoint's changes, so both halves queried the
same index and reported identical numbers. Two states on one connection have to be measured sequentially.

## A header helper that calls `time.Now` cannot be tested

**Expected.** `Decision{ResetAt: time.Time}` and a `WriteHeaders` method that computes the
`RateLimit-Reset` header with `time.Until(d.ResetAt)`. Obvious, and it reads well.

**What happened.** The middleware test injects a fake clock set to 2025-06-01 and the real clock says
2026-09-27, so `time.Until` on the limiter's `ResetAt` produced `RateLimit-Reset: -41721007`. Two clocks in
one subtraction.

**Next time.** Whichever component knows what time it is does the subtraction. `Decision` now carries
`ResetIn time.Duration`, set by the limiter, and `WriteHeaders` only formats. A formatter that reads a clock
has a hidden input, and a hidden input is either untestable or wrong.

## `rate.Limiter` refuses before the deadline arrives, so joining `ctx.Err()` joins nothing

**Expected.** `TokenBucket.Wait` wraps `rate.Limiter.Wait`, and on a context deadline the returned error
matches `context.DeadlineExceeded` via `errors.Is`, either directly or by joining `ctx.Err()`.

**What happened.** Two surprises in one test. `rate.Limiter.Wait` returns
`rate: Wait(n=1) would exceed context deadline`, which is a plain error and does not wrap
`context.DeadlineExceeded`, so `errors.Is` is false. And `ctx.Err()` is `nil` at that point, because
`rate.Limiter` does not wait and then give up: it works out up front that the token cannot arrive in time and
returns immediately, measured at 0ns of fake time under `synctest`.

That is better behaviour than waiting. It also means the obvious fix joins nothing, and the test failed twice
with the same message before I read it properly.

**Next time.** The fix is to check `ctx.Deadline()` rather than `ctx.Err()`: a context with a deadline plus a
refusal from a limiter with a positive burst means the refusal was about the deadline. And the general
lesson, which has now come up with pgx and with `rate`: whether a library's error is matchable with
`errors.Is` is a fact to verify in a test, never to assume from the error's wording.

## The sliding log is the fastest limiter, not the slowest

**Expected.** Keeping a timestamp per request is the expensive algorithm, so it should be the slowest as well
as the hungriest.

**What happened.** 63.6ns per Allow, the fastest of the four, against 69.6 for a fixed window, 74.4 for a
sliding counter and 132.5 for `x/time/rate`'s token bucket. In the steady state it reslices a prefix and
appends, with no allocation.

Its cost is memory, and only memory: one timestamp per request inside the window, per key. At 100,000 keys
and a limit of 1,000 that is 100 million timestamps. Which means for a LOW limit on a hot endpoint, a login
form at 10 per minute, it is both exactly correct and the cheapest thing available, and the usual advice to
avoid it does not apply there.

And the number that decides the real question: the Redis limiter is 32.1µs against the in-memory fixed
window's 69.6ns, **461x**. A PING alone is 20.2µs of that, so the Lua script costs about 12µs. That 461x is
what someone is implicitly choosing when they leave a per-process limiter in a service that scales out, where
five replicas with a limit of 10 enforce a limit of 50.

## An API that means different things per algorithm signed tokens with the wrong key

**Expected.** `AddVerifyKey(kid, key)` then `SetActiveKID(kid)` is a clean way to express JWT key rotation:
register the new key, switch to it.

**What happened.** `TestKeyRotation` failed with "the new token failed during the overlap: signature is
invalid", on a token the service had just minted. `SetActiveKID` changed which kid went into the header and did
not change the key used to sign, so every new token was signed with the old secret and labelled with the new
one.

The tempting fix is for `SetActiveKID` to look the key up in the verify map and sign with it. That works for
HS256, where both are the same shared secret, and cannot work for RS256, where the verify key is public and the
signing key is private with no way from one to the other. An API that works for one algorithm and silently
signs with the wrong key for the other is worse than an extra argument.

**Next time.** `Rotate(kid, signKey, verifyKey)` takes both, and the asymmetry between HS256 and RS256 is
visible at the call site. When one method has to mean different things depending on a field set in the
constructor, that is the signal to split the argument out rather than to branch inside.

## RS256 verification costs more than the Redis lookup it was supposed to replace

**Expected.** "A JWT saves you a database round trip per request" is the standard argument, and I wrote it into
a benchmark comment before running the benchmark.

**What happened.** Measured on an M2 Max:

| | mint | verify |
| --- | --- | --- |
| HS256 | 2.94µs | 4.12µs |
| RS256-2048 | 890µs | 31.95µs |
| RS256-4096 | 5.54ms | 148.67µs |

A Redis round trip is 20.2µs, from `BenchmarkRedisAllow` in the same module. So HS256 verification at 4.1µs
saves about 16µs per request, and RS256-2048 verification at 32µs costs MORE than the session lookup it
replaces.

**Next time.** The reason to choose RS256 is that many services can verify without any of them being able to
mint. That is an authority argument. Stating it as a performance win is a claim that does not survive a
benchmark, and the same goes for "stateless scales better" when the stateful alternative is one 20µs lookup.

Two smaller things from the same run. HS256 **verify** (4.12µs) is slower than HS256 **mint** (2.94µs), because
the HMAC is not the expensive part: verification also parses three base64 segments, unmarshals the claims and
validates exp, nbf, iss and aud. And RSA signing is 303x HMAC signing at 2048 bits while verification is only
7.8x, because signing exponentiates by a 2048-bit private exponent and verification uses the public exponent
65537.

## `time.Duration` runs out at 292 years

**Expected.** `975 * 365 * 24 * time.Hour` as a test fixture for "a timestamp from the year 3000".

**What happened.** It does not compile: "constant 30747600000000000000 of int64 type time.Duration overflows
int64". A `time.Duration` is an int64 count of nanoseconds, so the maximum is about 292 years.

**Next time.** Express a far-future instant as a `time.Time`, not as an offset. Relevant beyond fixtures: a
"never expire" sentinel written as a huge duration silently overflows to a negative number, and a negative
timeout usually means "already expired".

## Four findings from building the observability package

**A metrics middleware outside the mux cannot see the route.** `http.ServeMux` sets `r.Pattern` when it
matches, which happens inside the mux's `ServeHTTP`. A middleware wrapping the mux runs before that, so it sees
an empty pattern and labels every metric `unmatched`. Reading it after `next.ServeHTTP` does not help either:
the mux passes a cloned request to the handler, so the outer middleware's `r` is never the one with the pattern
set. The fix is a mutable holder in the context, written by a middleware registered inside the mux and read by
the outer one after the handler returns. That is what chi's RouteContext is for, and it is the only way to pass
information back out through a handler chain.

**A `slog.Handler` wrapper cannot add a top-level attribute inside a group.** Forwarding `WithGroup` keeps the
wrapper, and the context attributes then land at `http.request_id` rather than `request_id`. By the time
`Handle` runs, the inner handler already knows it is in a group. A handler could fix it by taking over group
handling entirely, which is a reimplementation of slog's group semantics inside a wrapper. The practical rule:
do not use `WithGroup` on a logger carrying request-scoped attributes, because a log query for `request_id` will
not match the nested path.

**A benchmark found a feature that silently did nothing.** `LevelFilter` gates on its own level and then defers
to the inner handler for a sampled request. slog's handlers default to Info, so an inner handler built without
options drops every debug line whatever the sampling says. The symptom was a benchmark: "kept because sampled"
measured 44ns, which is not the cost of writing a log line. With the inner handler at `LevelDebug` it is 842ns
against 13.4ns for a dropped line, a factor of 63, and that 63x is the whole argument for filtering in `Enabled`
rather than in `Handle`.

**Counting `dto.Metric` values understates a histogram by fifteen times.** `Registry.Gather` returns one metric
per label combination, so counting those says a histogram is one series. In the exposition format the same
histogram is one `_bucket` series per boundary plus the implicit `+Inf`, `_sum` and `_count`: 15 series for 12
buckets. Bucket count multiplies cardinality rather than adding to it.

The measured cardinality numbers, for 1,000 requests to one endpoint with 1,000 distinct ids: **28 series
labelled by route, 27,001 labelled by raw path, 964x**. And the cost is not only Prometheus's memory:
`Registry.Gather` takes 7.6µs at one label value and 7.36ms at 10,000, so the service pays for its own
cardinality on every scrape.

## `t.Log` from a server goroutine is a data race, and it reproduces one run in three

**Expected.** A `httptest.Server` handler that logs when a connection ends, with `t.Cleanup(srv.Close)` to tear
it down, is ordinary test code.

**What happened.** `go test -race` passed four times and failed on the fifth, and the report pointed inside
`testing` itself: a read in `testing.(*common).destination` racing with a write in `testing.tRunner.func1`. The
cause is that a WebSocket connection can end after the test function has returned, and `t.Logf` on a finished
test races with testing's own bookkeeping.

The fix is ordering, and `t.Cleanup` runs LIFO. Registering the log drain FIRST and `srv.Close` SECOND means
Close runs first, blocks until every handler has returned, and only then are the buffered messages logged, on
the test's own goroutine. Handler failures go into a variable rather than through `t.Errorf` for the same
reason.

**Next time.** Nothing that outlives a test may touch its `*testing.T`. That includes `t.Log`, `t.Error` and
`t.Fatal`, and it includes any goroutine a handler started. Buffer the messages, close the server first, then
report. A race that appears one run in three is the kind that gets rerun until it passes.

## A WebSocket client only answers pings while it is reading

**Expected.** A server pings an idle connection, the client's library replies automatically, and a peer that has
vanished fails the ping.

**What happened.** The test client connected and slept for 200ms without calling `Read`. The server sent one
ping, waited the whole `WriteTimeout`, got nothing, and gave up: zero successful pings. coder/websocket sends
the pong from INSIDE `Read`, so a client that is not in a read loop never answers.

**Next time.** A WebSocket client has to be in a read loop for the whole life of the connection, even if it
never expects a message. "Connect, send, sleep, send" is a client that gets disconnected, and the server log
says the peer stopped responding to pings, which sends everyone looking at the server.

Two more from the same package. `Conn.Close` dereferenced the socket unconditionally, and `Send` calls `Close`
when the overflow policy is Disconnect, so a Conn whose upgrade failed segfaulted inside whichever goroutine
was broadcasting. And a test helper that reports every accept failure through `t.Errorf` cannot be used to test
that an accept SHOULD fail, which is how the origin check test failed on its own success.

## Committing a Kafka offset with the loop's context loses it on every graceful shutdown

**Expected.** `Run(ctx, handle)` fetches with `ctx`, calls the handler, and commits with `ctx`. One context for
the whole loop is the obvious shape.

**What happened.** Four tests failed with `committing after processing: context canceled`. They cancel the
context from inside the handler once they have read enough, which is exactly what a shutdown signal does.

The consequence in production is worse than a failing test. When the context is cancelled the work for the
message in hand is already finished and the offset still has to be written. Committing with the cancelled
context fails, the offset is never stored, and the message is redelivered on the next start. So every graceful
shutdown produces a duplicate, invisible until someone asks why the same order id appears twice in the logs
every time you deploy.

**Next time.** The commit gets `context.Background()` with its own short timeout. The loop's context is the
shutdown signal, not the deadline for the work that shutdown interrupted. The timeout still matters: a commit
that hangs holds the process past whatever the orchestrator allows, and then it is a SIGKILL rather than a
clean stop.

Two smaller ones from the same package. A `t.Cleanup` that reuses a connection closed by a `defer` in the
enclosing function fails on every test with "use of closed network connection": cleanups run long after the
function returns, so they dial their own. And `kafka.Writer`'s zero value for `RequiredAcks` is `RequireNone`,
so a Writer built from an empty config returns success before the broker has the message and a broker restart
loses it silently. That is the most dangerous default in the library and it is what you get by not deciding.

## Three smaller findings from finishing backend-concepts

**`json.Marshal` HTML-escapes `<`, `>` and `&`, with no way to turn it off.** The first golden file in
`apitesting` read `"created_at": "<normalised>"`, which is correct JSON and unreadable in a diff, which
defeats the only reason to write a golden file. `json.Encoder` with `SetEscapeHTML(false)` is the only way, and it
also appends a trailing newline that `Marshal` does not.

**`httptest.NewRequest` and `http.NewRequest` produce different things and the difference is silent.**
`httptest.NewRequest` builds a SERVER request, with `RemoteAddr` set and a relative URL. `http.NewRequest` builds
a client request with an empty `RemoteAddr`. A handler that keys a rate limit or a log line on `RemoteAddr` gets
`""` from the second, so every request in the test shares one bucket and the test passes for the wrong reason.

**A service container cannot override a command.** GitHub Actions' `services:` takes an image and `options`
(docker run flags) and has no equivalent of `command`. Redpanda needs `redpanda start` with listener flags, so it
has to be a `docker run` step with a readiness poll. A fixed `sleep` instead of the poll is either too short on a
slow runner or wasted time on a fast one, and the failure mode of too short is a suite that skips every Kafka
test and reports success.

Which is the same shape as the whole skip-on-no-service design: skipping is the right behaviour locally and a
silent pass in CI, so both the database and the backend jobs grep their own output for the skip messages and fail
on them. A skip is not a pass.

## Four things gRPC does that I had to measure rather than assume

**A server-streaming interceptor DOES see the request through `RecvMsg`.** I wrote a comment saying the request
arrives before the handler runs, so a wrapped stream would count zero received messages. The test reported one.
grpc-go delivers the single request by calling `RecvMsg` on the stream the interceptor wrapped, so a
server-streaming call reports 1 received and N sent.

**`grpc.SetTrailer` returns an error.** I wrote a helper with no error return and a comment explaining that it
cannot fail, because trailers are not sent until the handler returns and so cannot be "too late". The linter
caught it. The asymmetry I was describing is real (SetHeader fails when called after the first Send, SetTrailer
has no such window) and the conclusion I drew from it was wrong.

**Interceptors are free.** No interceptors 27.9µs per unary call, one 26.4µs, six 26.0µs, all within the noise
of a 26µs call. I expected a measurable per-layer cost, as there is in HTTP middleware. There is not, because
the call itself is two orders of magnitude more expensive than a closure.

**Streaming is 22.7x faster than the equivalent unary calls.** 100 messages in one stream: 127µs and 97 KB
allocated. 100 unary calls: 2,891µs and 1,247 KB. A stream amortises the framing, the headers and the status
trailer over every message, which is the same finding as the N+1 measurement in database-concepts arrived at
from a different direction: the chatty API is the thing to fix before the transport.

And the smaller measurements that went into the README: protobuf is 4.1x smaller and 2.9x faster to encode than
`encoding/json` on the same generated struct, while `protojson` (what a gateway emits) is 7.9x slower than
protobuf and 2.8x slower than `encoding/json`.

## The generated-code question, decided

`*.pb.go` was gitignored with a comment saying "regenerate rather than commit". That is the right rule for a
private service with a build pipeline and the wrong one here.

This repo exists to be cloned and read. With the stubs ignored, `git clone && go build ./...` needs protoc,
protoc-gen-go, protoc-gen-go-grpc and versions that agree, which is four installs before the first line
compiles. It is also the Go community's convention: the standard library commits its generated files and every
gRPC project on pkg.go.dev ships its `.pb.go`, because `go get` runs no generators.

So the stubs are committed, the reasoning lives in `.gitignore` where the next person to touch it will read it,
and the drift it would have prevented is caught by a CI job that regenerates and diffs instead.

## The GraphQL N+1 benchmark said the opposite of what it was meant to, and it was right

**Expected.** Twenty authors with their books is 21 queries without a dataloader and 2 with one, so the batched
version should be faster, and more so as the store gets slower.

**What happened.** The naive resolver was FASTER at every latency, including 1ms per query: 3.65ms against
6.28ms. Not a bug in the benchmark.

GraphQL resolves sibling fields CONCURRENTLY. The 20 books resolvers run in 20 goroutines, so against a store
that will serve 20 queries at once, 20 one-millisecond queries take one millisecond in total. The dataloader's
2ms batching window is then pure added cost.

So an N+1 in GraphQL does not cost the REQUEST its latency. It costs the DATABASE: 21 connections instead of 2
and 21 queries of work instead of 2. Adding a connection-pool model to the store produced the expected result:

| store | naive | batched |
| --- | --- | --- |
| in memory | 373µs | 3,513µs |
| 1ms, unbounded | 3.65ms | 6.28ms |
| 1ms, pool of 4 | 8.61ms | **6.21ms** |
| 1ms, pool of 1 | 26.6ms | **6.16ms** |

**Next time.** The crossing point is the connection pool, and a benchmark of concurrent work against an
unbounded fake measures a system nobody has. It also vindicates the rule the rest of the repo follows: every
assertion in that package is on the QUERY COUNT, which is true everywhere, and the durations are logged beside
it.

## Three smaller things from gqlgen

**`client.Post` returns an error for a GraphQL error and does not populate the target.** Every partial-success
test failed with "unexpected end of JSON input", which is what you get decoding a target that was never
written. `RawPost` returns `data` and `errors` together, which is the whole point when testing that a response
carries both.

**The test client decodes STRICTLY.** A field the query selects and the response struct does not have is an
error: `'Authors[0]' has invalid keys: id`. That is the opposite of `encoding/json`, and it catches a query and
a struct drifting apart, which is worth the noise.

**One failing non-null field nulls the whole response.** With `authors: [Author!]!` and `books: [Book!]!`, an
error in one author's books propagates to the list, to the field, and to `data`, which becomes null. Whether a
failure costs one field or everything is decided by exclamation marks in the schema, and it is not obvious when
writing them.

## asynq stores every time value in whole seconds, and three tests found it separately

**`asynq.Timeout(300 * time.Millisecond)` becomes a 30-minute timeout.** The value is serialised as
`int64(timeout.Seconds())` in client.go, so anything under a second rounds to zero, zero means "unset", and
unset means the 30-minute default. Measured: a 600ms handler given `Timeout(300ms)` received a 30m0s budget and
ran to completion; the same handler given `Timeout(1s)` was cancelled at one second. The option is accepted and
nothing is logged.

**A scheduled task can run up to a second EARLY.** `processAt.Unix()` truncates to the second, so a task asked
for 800ms from now at wall-clock X.9 gets the score `floor(X + 1.7) = X + 1` and is promoted 100ms after the
enqueue. The first version of that test asserted "not before 700ms" and failed one run in six with the task
running after 665ms, which I took for a flaky test before reading the source.

**And it can run seconds late**, because the forwarder that promotes scheduled tasks is a poll on
`DelayedTaskCheckInterval`, five seconds by default. Nothing wakes up when a task becomes due.

**Next time.** Before asserting a timing bound against a library, find out what granularity it stores the value
at. All three of these are one decision (`Unix()` and `int64(d.Seconds())`) surfacing in three places, and none
of them is documented where you would look.

## Two of my own mistakes from the same module

**A helper that only filled in a default when the field was nil.** `setup` set the worker's queue map with
`if cfg.Queues == nil`, and `DefaultServerConfig` fills it with critical/default/low. So the worker listened on
three queues, every test enqueued to a fourth, and nine tests timed out waiting for a task nothing was going to
pick up. A test helper that is meant to control a field should set it unconditionally; "fill in if absent" is
for a constructor, not a fixture.

**A clock started after the thing being measured.** The scheduled-task test took `start := time.Now()` after an
inspector call that itself took 300ms, so an 800ms delay measured as 508ms and the test reported asynq running
tasks early. Anything measuring a delay starts its clock at the moment the delay is requested, before any
diagnostic call.

## Four email findings, three of them about the tools rather than the code

**`smtp.SendMail` sends in the clear when the server does not advertise STARTTLS, silently.** No error, no
warning. So a server that stops advertising it after a certificate expires starts sending every message and every
password in plaintext, and nothing reports it. `net/smtp` also has no timeout at all: `SendMail` against a black
hole hangs forever and there is no option for it. Both are why the sender here drives `smtp.Client` by hand over
a `net.Dialer` rather than calling the four-line convenience function.

**`textproto.MIMEHeader.Set` canonicalises the key.** `Content-ID` is stored as `Content-Id`, which is correct
(header names are case-insensitive) and makes a test grepping for the spelling it wrote fail.

**quoted-printable encodes `=` as `=3D`, always**, because `=` is its own escape character. So `src="cid:logo"`
appears in the raw message as `src=3D"cid:logo"`, and grepping raw bytes for HTML finds nothing, which looks like
the body was never written.

**Mailpit's "raw" message is not what was on the wire.** It prepends `Bcc` reconstructed from the envelope, plus
`Message-ID`, `Return-Path` and `Received`. An assertion that the raw message contains no Bcc header therefore
fails against Mailpit while the builder is right. The only place to answer "what did we send" is the builder's own
output, and the message-level test is where that assertion belongs.

**Next time.** A mail catcher tests that a message PARSES; it does not test what was transmitted, because it
rewrites what it stores. The same shape as the earlier finding about `pg_stat_activity` and the one about
`asynq`'s second granularity: when a test about a library fails, read what the library actually does before
assuming the code is wrong.

## The Docker measurements, and two things I had backwards

**Seven Dockerfiles for the same Go binary:** 1.01 GB naive, 115 MB with CGO, 16.4 MB on alpine, 8.8 MB
distroless, 7.53 MB scratch with the certificates and zone database, 6.68 MB scratch with nothing. The naive
image is **115x** the distroless one, and the whole gap between scratch and distroless is 1.3 MB of certificates
and zoneinfo.

**Shell form costs the full grace period.** `docker stop -t 10` against `ENTRYPOINT ["/server"]` exits in 0.4s
with code 0; against `CMD /app/server` it exits after 10.2s with code 137. `/bin/sh` is PID 1 in the second and
does not forward SIGTERM. Every in-flight request is dropped on every deploy and the only symptom is that deploys
are slow.

**An empty scratch image is not broken.** I expected it to fail to start; a static Go binary needs no files at
all, so it starts and serves traffic. What it gets wrong is outbound HTTPS (`x509: certificate signed by unknown
authority` for every host, which reads like the remote server's problem) and time zones.

**And the time zone case is better than I wrote.** I had commented that `time.LoadLocation` silently returns UTC
without the zone database. It returns an ERROR: `unknown time zone Europe/London`. The test corrected the comment.

**`-X` on a variable that does not exist is not an error.** The build succeeds, the variable keeps its default,
and the other `-X` flags still apply. So one typo produces one wrong field in a health endpoint and nothing else.
That is why the build variables default to `"unknown"` rather than `""`.

## A test that searched a file for the instruction its own comments discussed

**Expected.** Checking that a Dockerfile copies `go.mod` before running `go mod download` is a matter of comparing
two `strings.Index` results.

**What happened.** The Dockerfile's COMMENTS explain the caching, so "go mod download" appears in prose 150 bytes
before the `RUN` line that does it. The ordering assertion failed on a file that was correct.

**Next time.** Parse the instructions: drop comment and blank lines, join backslash continuations, then compare
indices in that list. A file whose comments discuss its own contents cannot be checked by substring position, and
this repo's files all have comments like that by design.

## `--output-sync` groups make's parallel output and does not order it

**Expected.** `make -j2 --output-sync=target` on two targets produces `a1 a2 a3 b1 b2 b3`, so a test can assert
that every a-line precedes every b-line.

**What happened.** It produced `b1 b2 b3 a1 a2 a3`, which is correctly grouped, and the test failed on correct
output. `--output-sync` guarantees that one target's lines are not interleaved with another's. Whichever target
finishes first prints first.

**Next time.** The check counts how many times the line prefix CHANGES: grouped output changes once whatever the
order, interleaved output changes on nearly every line. When asserting on a concurrency guarantee, write down
exactly what the guarantee is before writing the assertion, because "grouped" and "ordered" look the same in the
happy case.

Two smaller ones from the same module. macOS ships GNU make **3.81** from 2006 as `/usr/bin/make`, which lacks
`--output-sync` entirely, so the test detects the feature rather than the version and skips with a message that
says `brew install make`. And `gofmt -l` prints the offending files and **exits 0**, so a CI step that just runs
it passes whatever it finds; turning a non-empty output into a failure is the whole trick and every project gets
it wrong once.

## `pgxpool.Config.ConnString` returns the string it was parsed from, not the configuration

**Expected.** `dbtest.Pool(t)` hands out a pool whose config has already been pointed at this package's own
database, so a test that needs a SECOND pool can read the URL back off it with `pool.Config().ConnString()`.

**What happened.** `pgxdemo`'s tests passed on my machine and failed in CI with `relation "books" does not exist`.
`ConnString` returns the literal string the config was PARSED from. `dbtest` sets `cfg.ConnConfig.Database` after
parsing, so the mutation is invisible to `ConnString` and the second pool connected to the base database. That
database had the tables locally, left over from earlier work, and was empty on CI's fresh Postgres.

**Next time.** A getter named after an input returns the input. Anything that rewrites a parsed config has to
publish the effective value itself, so `dbtest` now exports `EffectiveURL(t)` and building a pool from
`Config().ConnString()` is wrong everywhere. The broader tell: a test that passes locally and fails on a fresh
database is almost always reading state that a previous run left behind.

## gqlgen rewrites the resolver file, so a helper at the bottom of it disappears

**Expected.** gqlgen's `follow-schema` layout keeps hand-written code in `schema.resolvers.go`. It preserves the
method bodies across regenerations, so a few small conversion helpers at the bottom of the same file are safe.

**What happened.** It preserves the method BODIES. Everything else in the file is discarded and rewritten. A
regenerate deleted `toBook`, `toBooks`, `toAuthor` and `strPtr`, and then gqlgen's own validation build of the
output failed with `undefined: toBooks` in the resolver bodies that still called them. CI found this, not me,
because the code compiled and the tests passed on the file I had by hand. Nothing locally had regenerated it since
the helpers were written.

**Next time.** Anything that is not a resolver method goes in a separate file in the same package, here
`graph/resolvers/convert.go`. The rule generalises past gqlgen: when a tool owns a file, hand-written code goes in
a file it does not own, and the check is not "does it build" but "does it build after regenerating". A generator
step in CI that regenerates and diffs is what makes that a caught error rather than a surprise months later.

## The race detector throws away one `sync.Pool.Put` in four, on purpose

**Expected.** `pool.Put(x)` followed immediately by `pool.Get()` on the same goroutine returns x. The value is in
that P's private slot and nothing has run in between.

**What happened.** `TestGCClearsThePool` failed in the `-race` CI job and nowhere else, on the assertion BEFORE
the garbage collection. `sync/pool.go` has this, guarded by `race.Enabled`: `if runtime_randn(4) == 0 { // Randomly
drop x on floor; return }`. The standard library sabotages a quarter of all Puts under the race detector,
deliberately, to break code that assumes a Pool retains anything. Measured over 400 Puts it comes out at 23% to
28%.

**Next time.** A test that fails only under `-race` is not automatically a data race. Read what the library does
under that build tag first. The fix here was to ask several times and to add a second test that MEASURES the drop
rate, so the claim is a number this repo checks rather than a sentence about someone else's source. And
`raceEnabled` is a constant in two files with opposite `//go:build race` constraints, because there is no runtime
function that answers the question.

## Generated code records the version of the generator, so CI has to pin it

**Expected.** protoc is a compiler. Identical `.proto` files give identical `.pb.go` files, so a CI job that
regenerates and diffs only fails when the schema and the committed stubs disagree. The workflow installed
Ubuntu's `protobuf-compiler` and a comment said as much.

**What happened.** Eight files differed, every one of them by a single line: `// protoc v7.36.2` locally against
`// protoc v3.21.12` in CI. The job failed for a reason it was not built to catch and said nothing about the
schema.

**Next time.** Pin the generator wherever its version reaches the output. `.protoc-version` now holds the number,
CI downloads that exact release rather than whatever apt has, and `generate.sh` warns when the local protoc does
not match. The general shape: a regenerate-and-diff job is only as useful as the reproducibility of the tool, and
every code generator that stamps its version into a header needs this.

## Three Windows CI failures, one cause each, none of them about Windows

**Expected.** A repo of pure-Go learning material builds and tests the same everywhere, so a `windows-latest`
entry in the test matrix costs nothing.

**What happened.** Six tests failed, in three groups.

The makefile-concepts tests found GNU make on the runner and ran the examples under **cmd.exe**, because make
takes its shell from COMSPEC on Windows rather than $SHELL. `cd` did not persist the way the test asserts, and
`pwd` and `grep` are not commands there. Two golden-file tests failed because git checks out text files with CRLF
on Windows, so the file on disk and the bytes the code produced differed on every line.

And the cross-compile job listed plan9 and the two wasm targets as building EVERY module. asynq's
`Server.waitForSignals` has no plan9 build and kafka-go names `syscall.ECONNREFUSED`, which plan9's syscall
package does not define. That job had never passed and never could.

**Next time.** `.gitattributes` with `* text=auto eol=lf` is not optional in a repo with golden files, and the
line for `*.sh` is worth spelling out separately, because a shebang ending in `\r` reports itself as a missing
interpreter. A test whose subject is a POSIX shell says so and skips. And a matrix entry that cannot pass is not
a strict check, it is a red X everyone learns to ignore, so the exotic targets now build the dependency-free
modules and the deployable targets build everything.

## `s.replace("", x)` inserts x between every character, and a slice from two searches can be empty

**Expected.** Replacing one job in a 613-line workflow with a scripted edit: find the start marker, find the end
marker, slice out the old block, swap in the new one.

**What happened.** The end marker was `run: go build $(go list -m -f '{{.Dir}}/...')`, which also appears in the
BUILD job 400 lines earlier. `str.index` returns the first occurrence, so the end index came before the start
index, the slice was the empty string, and `s.replace("", new)` inserted the new block between every character of
the file. 613 lines became 1,668,900, and 25 KB became 57 MB. I committed and pushed it before noticing, and
GitHub's large-file warning was the first sign.

**Next time.** Three things. Address a block by LINE INDEX with an assertion on what is at each end, not by
searching for strings that may repeat. Never pass a computed slice to `replace` without checking it is non-empty,
because the empty string is a legal argument with a pathological meaning. And look at `git diff --stat` before
committing a scripted edit; one line would have said `1 file changed, 1668287 insertions`.

Recovering it was `git show <previous-commit>:<path>`, then rebuilding the two commits on top of the last good
one and force-pushing. The 57 MB blob compressed to 440 KB in the pack, so the cost of leaving it would have been
small, but the repository is 1.9 MB rather than 12 MB now.

## An HTTP middleware that reads a request body breaks every retry above it

**Expected.** The request counter in `awstest` names each operation. DynamoDB puts it in `X-Amz-Target`; SNS uses
the query protocol and puts it in a form-encoded body, so the counter called `req.ParseForm()` to read it.

**What happened.** Every SNS operation started failing after three attempts with `ContentLength=93 with Body
length 0`. `ParseForm` consumes the body. The SDK retried, the transport found a declared Content-Length and an
empty reader, broke the connection, retried, broke it again, and gave up. The counter was observing the requests
and destroying them.

**Next time.** A client middleware that touches a request body reads it, and then puts it back:
`io.ReadAll`, then `req.Body = io.NopCloser(bytes.NewReader(body))`. The same applies to a server middleware that
logs a body before the handler runs. The tell in the error is that the byte count is right and the reader is
empty, which means something upstream already drained it.

## Two variables named the same topic, because CreateTopic is idempotent

**Expected.** Two calls to a test helper that builds a topic build two topics.

**What happened.** The topic name came from `awstest.Name(t, "topic")`, which is derived from the test name, so
both calls used the same name. SNS's CreateTopic is idempotent on the name and returned the SAME ARN, silently.
The second helper then subscribed the same queue with different attributes and SNS refused with `Subscription
already exists with different attributes`, which describes the symptom and not the cause.

**Next time.** A name derived from the test name is unique per TEST, not per CALL. Any helper a test can call
twice needs a discriminator in its signature, and the idempotent-creation APIs (SNS topics, S3 buckets in
us-east-1) hide the collision instead of reporting it.
