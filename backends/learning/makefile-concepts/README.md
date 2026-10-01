# makefile-concepts

Four example Makefiles and a Go test suite that runs them, because a claim about make in a comment is either true
of the make on your machine or it is not.

## Running it

```sh
go test ./...
```

The tests need **GNU** make and skip without it. BSD make (which is `make` on FreeBSD) has different syntax for
conditionals, no `$(shell ...)`, and no `.PHONY` in the same sense.

macOS ships GNU make **3.81**, from 2006, as `/usr/bin/make`. That is old enough to lack `--output-sync`, so one
test skips on a stock Mac and runs after `brew install make`, which provides `gmake`. The test harness prefers
`gmake` and says which it found.

You can also run any example directly:

```sh
make -f examples/04-traps.mk trap-phony
```

## What is here

| file | what it covers |
| --- | --- |
| [examples/01-basics.mk](examples/01-basics.mk) | rules are files, `.PHONY`, one shell per line, `$` against `$$` |
| [examples/02-variables.mk](examples/02-variables.mk) | `=` against `:=`, `?=`, `+=`, the automatic variables, pattern rules |
| [examples/03-go-workflow.mk](examples/03-go-workflow.mk) | a real Makefile for a Go workspace |
| [examples/04-traps.mk](examples/04-traps.mk) | six traps, each runnable |
| [makefiles/](makefiles/) | the tests that run them and assert on the output |

## A rule is a FILE

`make` is a build system for files. A rule says "this file depends on these files, and here is how to produce
it", and make runs the recipe only when the target is older than a prerequisite.

Everything in a modern Makefile is a rule whose target is a file that never exists. That is why they need
`.PHONY`, and it is the most common Makefile bug:

```
--- with no file called 'build' ---
the build target ran
--- with a DIRECTORY called 'build' ---
make[1]: `build' is up to date.
```

The recipe ran **once** across two invocations. With a directory called `build` present, make reported success
and ran nothing. Every Go repo has a `bin/` or a `dist/`, and every one is one `mkdir` away from a CI job that
runs nothing and passes.

`.PHONY` is not an optimisation and does not declare that a target is a command. It only tells make not to look
for a file of that name.

## Each recipe LINE is its own shell

```
--- separate lines ---
  pwd is /Users/alex/.../makefile-concepts     # the cd did not persist
  FOO is ''                                    # nor did the variable
--- one line ---
  pwd is /tmp
  FOO is 'bar'
```

That is why every loop over directories in a real Makefile is a wall of backslash continuations, and why the
`lint` target in `03-go-workflow.mk` looks the way it does.

## `=` against `:=`, which is one character and the most expensive mistake

```
simple, reference 1:    expanded at 1790556277.830014000
simple, reference 2:    expanded at 1790556277.830014000     # identical
recursive, reference 1: expanded at 1790556277.834580000
recursive, reference 2: expanded at 1790556277.838943000     # different
```

`:=` expands the right-hand side **once**, when the line is read. `=` expands it on **every reference**.

So `VERSION = $(shell git describe)` used in five recipes runs `git` five times, and one referenced inside a loop
runs it per iteration. Nothing warns you and the only symptom is a build that is slower than it should be.

`?=` assigns only if the variable is not already set, including from the environment or the command line, which
is what makes `make test DIR=./foo` work without the Makefile knowing about it. The subtlety: a variable set to
the EMPTY STRING counts as set, so `FOO= make target` does not get the default.

## The automatic variables

```
target        $@ = combined.txt
first prereq  $< = a.txt
all prereqs   $^ = a.txt b.txt
stem          $* = a
```

`$<` against `$^` is the pair to know. A compile rule wants `$<` (one source file) and a link rule wants `$^`
(every object). Using `$^` where `$<` belongs passes every header to the compiler.

## `$` is make's, `$$` is the shell's

make expands `$(FOO)` and `$FOO` itself, before the shell sees the line. To pass a dollar to the shell, double
it. So a recipe using a shell variable, a command substitution or an awk script needs `$$` everywhere, and
forgetting one produces an empty string rather than an error:

```
make expands this:  /bin/sh
the shell expands:  /Users/alex
a substitution:     2026
an awk field:       b          # from $$(echo 'a b c' | awk '{print $$2}')
```

## `make -j` interleaves output, and `--output-sync` does not order it

```
serial:   "a1 a2 a3 b1 b2 b3"
parallel: "a1 b1 a2 b2 b3 a3"
synced:   "b1 b2 b3 a1 a2 a3"
```

Parallel output interleaves line by line and nothing warns you. `--output-sync=target` groups each target's
output and is off by default. GNU make 4.0 added it, which is why macOS does not have it.

And note the third line: **`--output-sync` guarantees grouping, not order.** Whichever target finishes first
prints first. The test's first version asserted a-then-b and failed on correct output, which is how the helper
came to count prefix changes instead.

## `gofmt -l` exits 0 whatever it finds

```
gofmt -l printed "/tmp/.../bad.go" and exited with <nil>
```

A CI step that runs `gofmt -l` passes whatever it finds. Turning a non-empty output into a failure is the whole
trick:

```make
fmt-check:
	@unformatted=$$(gofmt -l $(DIRS)); \
	if [ -n "$$unformatted" ]; then \
		echo "these files are not formatted:"; \
		echo "$$unformatted" | sed 's/^/  /'; \
		exit 1; \
	fi
```

The same shape catches people with `grep` in a pipeline: `make` checks the exit code of each recipe LINE, and a
pipeline reports the exit code of the LAST command, so `failing | tee log` succeeds. That is why every capturing
step in this repo's CI workflow sets `set -o pipefail`.

## The Go workspace problem

```
$ go build ./...
pattern ./...: directory prefix . does not contain modules listed in go.work
               or their selected dependencies
```

The root of a workspace is **not itself a module**, so `./...` matches nothing. The fix:

```make
MODULES := $(shell go list -m -f '{{.Dir}}/...' 2>/dev/null)
DIR ?= $(MODULES)
```

`:=` because it shells out and every target uses it. `?=` on DIR so a caller can scope any target to one module
with `make test DIR=./go-concepts/04-errors`. It expands to every module in this workspace (eighteen at the time of writing) and keeps
working as modules are added.

Two more things the Go workflow file does:

**`.DEFAULT_GOAL := help`**, so a bare `make` prints the targets rather than running the first one. Without it,
`make` runs whatever happens to be first, which for most Makefiles is a build and for some is a deploy.

**A file target for a tool.** A tool install is the one place a real file rule earns its keep in a Go Makefile:
the target is a file that either exists or does not, which is exactly what make is for. A version is pinned in the
Makefile rather than `@latest`, because `@latest` in a build is how CI breaks on a day nobody touched the repo.

The first version targeted the binary, `$(GOBIN)/golangci-lint`, and that made the pin decorative: the binary
exists after ANY version is installed, so bumping `GOLANGCI_VERSION` never reinstalled anything. The target is
now a stamp file with the version in its name, `.tools/golangci-lint-v2.6.2`, so a new version is a new file make
has never seen.

## The self-documenting help target

```make
## help: list the targets in this file
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## /  /' | column -t -s ':'
```

A `## target: description` comment above each target and a help target that greps them. The documentation is next
to the thing it documents and cannot drift, and the test checks that every documented target appears in the
output: **13 of 13**.

`$(MAKEFILE_LIST)` rather than a hard-coded filename, so it still works when the file is included from another.

## Things worth stealing from here

- The `MODULES := $(shell go list -m ...)` plus `DIR ?=` pair, which is the whole answer to Makefiles in a Go
  workspace.
- The `fmt-check` recipe, which turns `gofmt -l`'s output into an exit code.
- The `$(GOBIN)/<tool>` file target, so `make lint` installs the tool only when it is missing.
- The `## target: description` convention and the three-line help target.
- `makefiles/makefiles_test.go`, which runs each example and asserts on its output, because a Makefile that
  nobody checks is a Makefile that has drifted.
