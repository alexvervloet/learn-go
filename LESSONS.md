# Lessons

Things that did not go according to plan while building this repo, written down
when they happened. The counterpart to `PLAN.md`, which is scratch and never
committed. This file is committed and stays.

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
