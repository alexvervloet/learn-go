# The four things a Makefile is, and the one that surprises people.
#
# Run these with:  make -f examples/01-basics.mk <target>
#
# # A rule is a FILE, not a command
#
# `make` is a build system for files. A rule says "this file depends on these files, and here is how to produce
# it", and make runs the recipe only when the target is older than a prerequisite.
#
# Everything else, including every target in this repo's root Makefile, is a rule whose target is a file that
# never exists. That is why they need .PHONY, and it is the single most common Makefile bug.

# ---------------------------------------------------------------------------
# A real file rule.
# ---------------------------------------------------------------------------
#
# hello.txt depends on nothing and is produced by echo. Run it twice: the second run says
# "make: 'hello.txt' is up to date." because the file exists.
hello.txt:
	@echo "building hello.txt"
	@echo "hello" > hello.txt

# A rule with a prerequisite. Touch hello.txt and greeting.txt rebuilds; touch nothing and it does not.
greeting.txt: hello.txt
	@echo "building greeting.txt from hello.txt"
	@sed 's/hello/GREETINGS/' hello.txt > greeting.txt

# ---------------------------------------------------------------------------
# The .PHONY problem, demonstrated.
# ---------------------------------------------------------------------------
#
# `test` here is a rule whose target is the FILE `test`. It has no prerequisites, so make runs it once and then
# considers it up to date... except the recipe never creates a file called `test`, so make runs it every time.
#
# Which looks fine. Until somebody adds a directory or a file called `test`, at which point `make test` says
# "'test' is up to date" and runs nothing, and the CI that was passing goes on passing without running anything.
test:
	@echo "running the tests (this is a non-phony target called 'test')"

# The same rule, declared phony. Now make never looks for a file and always runs the recipe.
.PHONY: test-phony
test-phony:
	@echo "running the tests (declared .PHONY, so the file system is irrelevant)"

# ---------------------------------------------------------------------------
# Each recipe line is its own SHELL.
# ---------------------------------------------------------------------------
#
# This is the second-most-surprising thing about make. Every line of a recipe runs in a separate shell, so a
# `cd` on one line does not affect the next and a variable set on one line is gone on the next.
.PHONY: separate-shells
separate-shells:
	@cd /tmp
	@pwd
	@FOO=bar
	@echo "FOO is '$$FOO'"

# The fix is a single line with && or a backslash continuation, or .ONESHELL.
.PHONY: one-shell
one-shell:
	@cd /tmp && pwd
	@FOO=bar; echo "FOO is '$$FOO'"

# ---------------------------------------------------------------------------
# $ is make's, $$ is the shell's.
# ---------------------------------------------------------------------------
#
# make expands $(FOO) and $FOO itself, before the shell sees the line. To pass a dollar to the shell, double it.
#
# So a recipe using a shell variable, a command substitution or an awk script needs $$ everywhere, and forgetting
# one produces an empty string rather than an error.
.PHONY: dollars
dollars:
	@echo "make expands this:  $(SHELL)"
	@echo "the shell expands:  $$HOME"
	@echo "a substitution:     $$(date +%Y)"
	@echo "an awk field:       $$(echo 'a b c' | awk '{print $$2}')"

# ---------------------------------------------------------------------------
# @ suppresses the echo, - ignores the failure.
# ---------------------------------------------------------------------------
#
# Without @, make prints each command before running it, which is useful for a build and noise for a helper.
# With -, a non-zero exit is ignored, which is almost always wrong: it is how a Makefile reports success for a
# failed build.
.PHONY: prefixes
prefixes:
	echo "this line is printed before it runs, because there is no @"
	@echo "this one is not"
	-@false
	@echo "the previous line failed and make continued, because of the -"

.PHONY: clean
clean:
	@rm -f hello.txt greeting.txt
