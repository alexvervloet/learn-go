# The traps, each one runnable.
#
#   make -f examples/04-traps.mk <target>
#
# Every one of these has cost somebody a day, and none of them produces an error message that says what happened.

# ---------------------------------------------------------------------------
# Trap 1: a target that shares a name with a file or directory
# ---------------------------------------------------------------------------
#
# `build` here is not phony. Create a directory called `build` and the target stops running, with make reporting
# success.

build:
	@echo "the build target ran"

.PHONY: trap-phony
trap-phony:
	@rm -rf build
	@echo "--- with no file called 'build' ---"
	@$(MAKE) --no-print-directory -f $(firstword $(MAKEFILE_LIST)) build
	@mkdir -p build
	@echo "--- with a DIRECTORY called 'build' ---"
	@$(MAKE) --no-print-directory -f $(firstword $(MAKEFILE_LIST)) build || true
	@rm -rf build
	@echo
	@echo "the second run printed nothing and exited 0. Every Go repo has a bin/ or a dist/,"
	@echo "and every one of them is one 'mkdir build' away from a CI job that runs nothing."

# ---------------------------------------------------------------------------
# Trap 2: a failure in the middle of a recipe line
# ---------------------------------------------------------------------------
#
# make checks the exit code of each recipe LINE. A line that is a pipeline reports the exit code of the LAST
# command, so `failing | tee log` succeeds because tee succeeded.
#
# This is exactly the shape of a CI step that captures output, and it is why every such step in this repo's
# workflow sets `set -o pipefail`.

.PHONY: trap-pipeline
trap-pipeline:
	@echo "--- without pipefail ---"
	@(exit 3) | cat; echo "make saw exit code $$?"
	@echo "--- with pipefail ---"
	@set -o pipefail; (exit 3) | cat; echo "the shell saw exit code $$?" || true

# ---------------------------------------------------------------------------
# Trap 3: a recipe that changes directory
# ---------------------------------------------------------------------------
#
# Each recipe line is its own shell, so `cd` does not persist. A loop that cds into each module has to do the cd
# and the work on the SAME line, which is why every such loop in a real Makefile is a wall of backslashes.

.PHONY: trap-cd
trap-cd:
	@echo "--- separate lines ---"
	@mkdir -p /tmp/mk-demo
	@cd /tmp/mk-demo
	@echo "  pwd is $$(pwd)"
	@echo "--- one line ---"
	@cd /tmp/mk-demo && echo "  pwd is $$(pwd)"
	@rmdir /tmp/mk-demo

# ---------------------------------------------------------------------------
# Trap 4: recursive expansion in a loop
# ---------------------------------------------------------------------------
#
# A `=` variable holding a $(shell ...) is re-run on every reference. Inside a loop over N items, that is N
# process launches for a value that never changes.

SLOW_RECURSIVE = $(shell sleep 0.05; echo value)
SLOW_SIMPLE := $(shell sleep 0.05; echo value)

.PHONY: trap-expansion
trap-expansion:
	@echo "--- := (expanded once, when the file was read) ---"
	@start=$$(date +%s%N); \
	for i in 1 2 3 4 5; do echo "$(SLOW_SIMPLE)" > /dev/null; done; \
	end=$$(date +%s%N); \
	echo "  five references took $$(( (end - start) / 1000000 ))ms"
	@echo "--- = (expanded per reference) ---"
	@start=$$(date +%s%N); \
	for i in 1 2 3 4 5; do echo "$(SLOW_RECURSIVE)" > /dev/null; done; \
	end=$$(date +%s%N); \
	echo "  five references took $$(( (end - start) / 1000000 ))ms"
	@echo
	@echo "both loops are in ONE recipe line, so make expanded the variable once per line either"
	@echo "way. The cost of = shows up when the reference is in a separate recipe line or in"
	@echo "several targets, which is the normal case."

# ---------------------------------------------------------------------------
# Trap 5: .PHONY does not make a target fast
# ---------------------------------------------------------------------------
#
# A common misreading: .PHONY is not an optimisation and not a declaration that a target is a command. It only
# tells make not to look for a file of that name.
#
# A phony target still runs its recipe every time, which is the point, and it still runs its prerequisites.

.PHONY: slow-prereq
slow-prereq:
	@echo "  the prerequisite ran"

.PHONY: trap-prereqs
trap-prereqs: slow-prereq slow-prereq slow-prereq
	@echo "three identical prerequisites, and make ran it once."
	@echo "make deduplicates prerequisites within one invocation, which is why a diamond"
	@echo "dependency does not build the shared node twice."

# ---------------------------------------------------------------------------
# Trap 6: parallel make and shared output
# ---------------------------------------------------------------------------
#
# `make -j` runs independent targets concurrently, and their output INTERLEAVES line by line. GNU make 4.0 added
# --output-sync to fix that, and it is off by default.
#
# Worse, two targets writing the same file concurrently corrupt it, and make cannot know they do.

.PHONY: parallel-a parallel-b
parallel-a:
	@for i in 1 2 3; do echo "a$$i"; sleep 0.02; done

parallel-b:
	@for i in 1 2 3; do echo "b$$i"; sleep 0.02; done

.PHONY: trap-parallel
trap-parallel:
	@echo "--- serial ---"
	@$(MAKE) --no-print-directory -f $(firstword $(MAKEFILE_LIST)) parallel-a parallel-b
	@echo "--- parallel, interleaved ---"
	@$(MAKE) --no-print-directory -j2 -f $(firstword $(MAKEFILE_LIST)) parallel-a parallel-b
	@echo "--- parallel with --output-sync=target ---"
	@$(MAKE) --no-print-directory -j2 --output-sync=target -f $(firstword $(MAKEFILE_LIST)) parallel-a parallel-b

.PHONY: all-traps
all-traps:
	@$(MAKE) --no-print-directory -f $(firstword $(MAKEFILE_LIST)) trap-phony
	@echo
	@$(MAKE) --no-print-directory -f $(firstword $(MAKEFILE_LIST)) trap-cd
	@echo
	@$(MAKE) --no-print-directory -f $(firstword $(MAKEFILE_LIST)) trap-prereqs
