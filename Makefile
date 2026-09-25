# Root Makefile. The analogue of the Python repo's per-folder pytest/ruff
# invocations, collapsed into one place because Go's tooling is uniform.
#
# Every target works across the whole workspace. To scope one to a single
# module, pass DIR:
#
#	make test DIR=./go-concepts/04-errors

# `./...` does NOT work from a workspace root: the root directory is not itself
# a module, so the pattern matches nothing and go reports "directory prefix .
# does not contain modules listed in go.work". Asking the workspace for its
# module directories and expanding each is the portable fix, and it keeps
# working as modules are added to go.work.
MODULES := $(shell go list -m -f '{{.Dir}}/...' 2>/dev/null)
DIR ?= $(MODULES)

GOBIN := $(shell go env GOPATH)/bin

.DEFAULT_GOAL := help

## help: list the targets in this file
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## /  /' | column -t -s ':'

## build: compile every package, writing no binaries
build:
	go build $(DIR)

## test: run every test
test:
	go test $(DIR)

## test-v: run every test, naming each subtest
test-v:
	go test -v $(DIR)

## test-race: run every test under the race detector (see lesson 16)
test-race:
	go test -race $(DIR)

## test-count: run tests with the cache disabled, to catch order dependence
test-count:
	go test -count=1 $(DIR)

## cover: write a coverage profile and open the HTML report
cover:
	go test -coverprofile=coverage.out $(DIR)
	go tool cover -html=coverage.out

## cover-summary: print per-function coverage without opening a browser
cover-summary:
	go test -coverprofile=coverage.out $(DIR)
	go tool cover -func=coverage.out | tail -20

## bench: run every benchmark, reporting allocations
bench:
	go test -bench . -benchmem -run '^$$' $(DIR)

## vet: run the compiler's own static checks
vet:
	go vet $(DIR)

## lint: run golangci-lint (install with `make tools`)
lint:
	$(GOBIN)/golangci-lint run $(DIR)

## lint-fix: run golangci-lint and apply what it can fix
lint-fix:
	$(GOBIN)/golangci-lint run --fix $(DIR)

## fmt: format every file in place
fmt:
	gofmt -w .

## fmt-check: fail if anything is unformatted (this is what CI runs)
fmt-check:
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "not gofmt'd:"; echo "$$unformatted"; exit 1; \
	fi
	@echo "all files are gofmt clean"

## tidy: run `go mod tidy` in every module in the workspace
tidy:
	@for mod in $$(go list -m -f '{{.Dir}}'); do \
		echo "tidy $$mod"; \
		(cd $$mod && go mod tidy); \
	done

# Pinned rather than @latest. CI installs this too, and an unpinned linter
# means a new release can turn a green branch red without anyone touching the
# code. Bump deliberately, see what it finds, commit the bump on its own.
GOLANGCI_VERSION := v2.14.0

## tools: install the developer tools this repo expects, at pinned versions
tools:
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION)

## check: everything CI runs, in CI's order
check: fmt-check vet lint test

## clean: remove build and coverage artifacts
clean:
	go clean -cache -testcache
	rm -f coverage.out coverage.html

.PHONY: help build test test-v test-race test-count cover cover-summary bench \
        vet lint lint-fix fmt fmt-check tidy tools check clean
