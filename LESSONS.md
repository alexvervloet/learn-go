# Lessons

Things that did not go according to plan while building this repo, written down
when they happened. The counterpart to `PLAN.md`, which is scratch and never
committed. This file is committed and stays.

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
