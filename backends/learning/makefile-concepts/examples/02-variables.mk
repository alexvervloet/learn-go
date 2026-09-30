# Variables, and the assignment operator that catches everyone.
#
#   make -f examples/02-variables.mk <target>

# ---------------------------------------------------------------------------
# = against := : the difference that costs a build minute
# ---------------------------------------------------------------------------
#
#   =   RECURSIVE. The right-hand side is expanded every time the variable is USED.
#   :=  SIMPLE. The right-hand side is expanded once, here, when the line is read.
#
# So a variable holding a $(shell ...) with `=` runs that shell command on EVERY reference. A Makefile with
# `VERSION = $(shell git describe)` used in five recipes runs git five times, and one used inside a loop runs it
# per iteration.
#
# This is the most expensive Makefile mistake there is and it is one character.

RECURSIVE = $(shell echo "expanded at $$(date +%s.%N)")
SIMPLE := $(shell echo "expanded at $$(date +%s.%N)")

.PHONY: expansion
expansion:
	@echo "simple, reference 1:    $(SIMPLE)"
	@echo "simple, reference 2:    $(SIMPLE)"
	@echo "recursive, reference 1: $(RECURSIVE)"
	@echo "recursive, reference 2: $(RECURSIVE)"
	@echo
	@echo "the two simple values are identical and the two recursive ones are not."
	@echo "a := variable runs its shell command once; an = variable runs it per use."

# ---------------------------------------------------------------------------
# ?= sets a default, += appends
# ---------------------------------------------------------------------------
#
# ?= assigns only if the variable is not already set, INCLUDING from the environment or the command line. That is
# what makes `make test DIR=./foo` work without the Makefile knowing about it.
#
# The subtlety: a variable set to the EMPTY STRING counts as set, so `FOO= make target` does not get the default.

TAG ?= dev
FLAGS := -trimpath
FLAGS += -ldflags="-s -w"

.PHONY: defaults
defaults:
	@echo "TAG=$(TAG)  (override with: make -f examples/02-variables.mk defaults TAG=v1.0)"
	@echo "FLAGS=$(FLAGS)"

# ---------------------------------------------------------------------------
# A command-line variable BEATS an assignment in the file
# ---------------------------------------------------------------------------
#
# `make FOO=bar` overrides `FOO := baz` in the Makefile, silently. That is usually what you want and it means a
# Makefile cannot force a value without `override`.

FORCED := from-the-file

.PHONY: override-demo
override-demo:
	@echo "FORCED=$(FORCED)"
	@echo "try: make -f examples/02-variables.mk override-demo FORCED=from-the-command-line"

# ---------------------------------------------------------------------------
# The automatic variables
# ---------------------------------------------------------------------------
#
#   $@  the target
#   $<  the FIRST prerequisite
#   $^  ALL prerequisites, deduplicated
#   $+  all prerequisites, with duplicates
#   $*  the stem of a pattern rule match
#
# $< against $^ is the pair to know: a compile rule wants $< (one source file) and a link rule wants $^ (every
# object). Using $^ where $< belongs passes every header to the compiler.

a.txt:
	@echo a > $@

b.txt:
	@echo b > $@

combined.txt: a.txt b.txt
	@echo "target        \$$@ = $@"
	@echo "first prereq  \$$< = $<"
	@echo "all prereqs   \$$^ = $^"
	@cat $^ > $@

# ---------------------------------------------------------------------------
# A pattern rule
# ---------------------------------------------------------------------------
#
# %.upper is produced from %.txt for any stem. $* is the stem, which is the only way to get at it.

%.upper: %.txt
	@echo "stem \$$* = $*"
	@tr 'a-z' 'A-Z' < $< > $@

.PHONY: patterns
patterns: a.upper b.upper
	@echo "built: $^"
	@cat $^

.PHONY: clean
clean:
	@rm -f a.txt b.txt combined.txt *.upper
