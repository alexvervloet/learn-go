# Lessons

Things that did not go according to plan while building this repo, written down
when they happened. The counterpart to `PLAN.md`, which is scratch and never
committed. This file is committed and stays.

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
