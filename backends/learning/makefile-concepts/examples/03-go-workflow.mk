# A Makefile for a Go workspace, which is where the interesting problems are.
#
#   make -f examples/03-go-workflow.mk help

# ---------------------------------------------------------------------------
# ./... does NOT work from a workspace root
# ---------------------------------------------------------------------------
#
# This is the first thing that breaks in a multi-module repo and the error does not say why:
#
#   go: directory prefix . does not contain modules listed in go.work
#
# The root of a workspace is not itself a module, so `./...` matches nothing. Asking the workspace for its module
# directories and expanding each one is the portable fix, and it keeps working as modules are added.
#
# := and not =, because this shells out and the variable is used in every target.
MODULES := $(shell go list -m -f '{{.Dir}}/...' 2>/dev/null)

# DIR lets a caller scope any target to one module: make -f ... test DIR=./go-concepts/04-errors
#
# ?= so the command line wins, and defaulting to every module so the bare target does the obvious thing.
DIR ?= $(MODULES)

# GOBIN, resolved once, because `go env` is a process launch.
GOBIN := $(shell go env GOPATH)/bin

# The tools this Makefile installs, with their versions PINNED.
#
# @latest in a Makefile is how a build becomes unreproducible: a tool that changes its output breaks CI on a day
# nobody touched the repo. Pinning means an upgrade is a commit.
GOLANGCI_VERSION := v2.6.2

# .DEFAULT_GOAL, so a bare `make` prints the help rather than running the first target.
#
# Without it, `make` runs whatever happens to be first, which for most Makefiles is a build and for some is a
# deploy.
.DEFAULT_GOAL := help

# ---------------------------------------------------------------------------
# A self-documenting help target
# ---------------------------------------------------------------------------
#
# The convention: a `## target: description` comment above each target, and a help target that greps them. It
# means the documentation is next to the thing it documents and cannot drift.
#
# $(MAKEFILE_LIST) rather than a hard-coded filename, so it works when the file is included from another.
## help: list the targets in this file
.PHONY: help
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## /  /' | column -t -s ':'

## build: compile every package, writing no binaries
.PHONY: build
build:
	go build $(DIR)

## test: run every test
.PHONY: test
test:
	go test $(DIR)

## test-race: run every test under the race detector
.PHONY: test-race
test-race:
	go test -race $(DIR)

## cover: run the tests with coverage and report the total
.PHONY: cover
cover:
	go test -coverprofile=coverage.out $(DIR)
	@go tool cover -func=coverage.out | tail -1

## fmt: format every file and report which ones changed
.PHONY: fmt
fmt:
	@gofmt -l -w $(shell go list -m -f '{{.Dir}}' 2>/dev/null)

## fmt-check: fail if anything is unformatted, for CI
#
# `gofmt -l` prints the offending files and exits 0, so a CI step that runs it passes whatever it finds. Turning
# a non-empty output into a failure is the whole trick, and every project gets it wrong once.
.PHONY: fmt-check
fmt-check:
	@unformatted=$$(gofmt -l $(shell go list -m -f '{{.Dir}}' 2>/dev/null)); \
	if [ -n "$$unformatted" ]; then \
		echo "these files are not formatted:"; \
		echo "$$unformatted" | sed 's/^/  /'; \
		exit 1; \
	fi; \
	echo "every file is formatted"

## vet: run go vet
.PHONY: vet
vet:
	go vet $(DIR)

## tools: install the pinned development tools
#
# Into $(GOBIN), which is on PATH for anyone who has set it up. The alternative, for a single-module repo, is the
# go.mod `tool` directive (Go 1.24): `go get -tool github.com/.../golangci-lint@vX` records the version in go.mod
# and `go tool golangci-lint` runs it, with no install step at all. It replaced the older tools.go file of blank
# imports. In a workspace it is a poorer fit, because one module's tool dependencies raise the shared versions
# every module builds with.
.PHONY: tools
tools:
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION)

# The version stamp the lint target depends on; see the $(TOOLS_STAMP) rule below. Defined BEFORE the rule that
# names it, because make expands a prerequisite list when it reads the rule.
TOOLS_STAMP := .tools/golangci-lint-$(GOLANGCI_VERSION)

## lint: run golangci-lint over every module
#
# golangci-lint has no workspace mode, so it runs once per module directory. A `for` loop in a recipe needs the
# whole thing on one logical line, because each recipe LINE is its own shell.
.PHONY: lint
lint: $(TOOLS_STAMP)
	@for dir in $(shell go list -m -f '{{.Dir}}' 2>/dev/null); do \
		echo "linting $$dir"; \
		(cd $$dir && $(GOBIN)/golangci-lint run ./...) || exit 1; \
	done

# A FILE target for the tool, so `make lint` installs it only when it is missing.
#
# This is the one place a real file rule earns its keep in a Go Makefile: the target is a file that either exists
# or does not, which is exactly what make is for. But the file has to name the VERSION. The first version of this
# rule targeted $(GOBIN)/golangci-lint itself, which exists after any install of any version, so bumping
# GOLANGCI_VERSION never reinstalled anything and the "pin" pinned nothing. A stamp file per version fixes it: a
# new version is a new file name, so make sees it missing.
$(TOOLS_STAMP):
	@echo "installing golangci-lint $(GOLANGCI_VERSION)"
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION)
	@mkdir -p $(dir $@) && touch $@

## ci: everything CI runs, in the order it runs it
#
# The order matters: formatting and vet are fast and catch the most, so they go first and a failure saves the
# slower steps.
#
# Listing them as prerequisites rather than as recipe lines means make could run them in parallel with -j, which
# is wrong here because they have an order. `.NOTPARALLEL:` or a recipe with && is the fix, and a recipe is
# clearer.
.PHONY: ci
ci:
	$(MAKE) -f $(firstword $(MAKEFILE_LIST)) fmt-check
	$(MAKE) -f $(firstword $(MAKEFILE_LIST)) vet
	$(MAKE) -f $(firstword $(MAKEFILE_LIST)) lint
	$(MAKE) -f $(firstword $(MAKEFILE_LIST)) test-race

## modules: list the workspace's modules, to see what DIR expands to
.PHONY: modules
modules:
	@echo "$(MODULES)" | tr ' ' '\n'

## clean: remove build and coverage artifacts
.PHONY: clean
clean:
	@rm -f coverage.out coverage.html
	@go clean -cache -testcache 2>/dev/null || true
