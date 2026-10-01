package workflow

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// repoRoot walks up to the directory holding go.work.
//
// The tests here read this repository's own .github/workflows/ci.yml, which is the point of the module, and a
// relative path from the package directory would break the moment the module moves.
func repoRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	require.NoError(t, err)

	for {
		if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
			return dir
		}

		parent := filepath.Dir(dir)
		require.NotEqual(t, parent, dir, "walked to the filesystem root without finding go.work")

		dir = parent
	}
}

// example loads one of the module's example workflows.
func example(t *testing.T, name string) *Workflow {
	t.Helper()

	w, err := Parse(filepath.Join("..", "examples", name))
	require.NoError(t, err)

	return w
}

// TestOnIsNotParsedAsTrue pins the YAML version this depends on.
//
// In YAML 1.1 the bare word `on` is the boolean true, so a 1.1 parser gives a workflow a key called `true`.
// yaml.v3 implements 1.2, where only `true` and `false` are booleans. Every rule in this package that looks at
// triggers depends on that, so it is worth an assertion rather than a footnote.
func TestOnIsNotParsedAsTrue(t *testing.T) {
	w, err := ParseBytes([]byte("name: t\non: push\njobs: {}\n"))
	require.NoError(t, err)

	triggers, err := w.Triggers()
	require.NoError(t, err)
	require.Equal(t, []string{"push"}, triggers, "`on` decoded as a key, not as the boolean true")
}

// TestTriggersAcceptsAllThreeShapes covers the union that YAML cannot express.
func TestTriggersAcceptsAllThreeShapes(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want []string
	}{
		{
			name: "scalar",
			yaml: "on: push\njobs: {}\n",
			want: []string{"push"},
		},
		{
			name: "sequence",
			yaml: "on: [push, pull_request]\njobs: {}\n",
			want: []string{"push", "pull_request"},
		},
		{
			name: "mapping",
			yaml: "on:\n  push:\n    branches: [main]\n  schedule:\n    - cron: \"0 3 * * *\"\njobs: {}\n",
			want: []string{"push", "schedule"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w, err := ParseBytes([]byte(tc.yaml))
			require.NoError(t, err)

			got, err := w.Triggers()
			require.NoError(t, err)
			require.ElementsMatch(t, tc.want, got)
		})
	}
}

// TestUnknownKeysAreRejected is the misspelling check.
//
// `runs-on` spelled `runs_on` is a workflow that fails at run time with "a job must have a runs-on". Catching
// it at parse time is the difference between a linter and a document.
func TestUnknownKeysAreRejected(t *testing.T) {
	_, err := ParseBytes([]byte("on: push\njobs:\n  build:\n    runs_on: ubuntu-latest\n    steps: []\n"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "runs_on")
}

// TestScriptInjectionIsFound is the security rule, against a fixture that contains the bug.
func TestScriptInjectionIsFound(t *testing.T) {
	w := example(t, "injectable.yml")

	findings := Check(w)

	var injections []Finding

	for _, f := range findings {
		if f.Rule == "script-injection" {
			injections = append(injections, f)
		}
	}

	require.Len(t, injections, 1, "one interpolation, not two: %v", findings)
	require.Equal(t, "Say hello", injections[0].Step,
		"the vulnerable step is the one interpolating into `run`, not the one using `env`")
	require.Contains(t, injections[0].Message, "github.event.issue.title")
	require.Contains(t, injections[0].Message, "env:")

	// The point of the fixture: the SAFE step also references attacker-controlled data. It is safe because the
	// value reaches the shell through the environment instead of being substituted into the source text.
	safe := w.Jobs["greet"].Steps[1]
	require.Equal(t, "Say hello, safely", safe.Name)
	require.Contains(t, safe.Env["TITLE"], "github.event.issue.body",
		"the same class of data, in env rather than in run")
	require.NotContains(t, safe.Run, "${{", "nothing is interpolated into the script")
}

// TestUnpinnedActionsAreFound covers the three pinning cases.
func TestUnpinnedActionsAreFound(t *testing.T) {
	w := example(t, "injectable.yml")

	var unpinned []string

	for _, f := range Check(w) {
		if f.Rule == "unpinned-action" {
			unpinned = append(unpinned, f.Message)
		}
	}

	require.Len(t, unpinned, 2)
	require.Contains(t, strings.Join(unpinned, "\n"), "no version at all")
	require.Contains(t, strings.Join(unpinned, "\n"), "mutable tag")
}

// TestACommitShaCountsAsPinned is the other half of that rule.
func TestACommitShaCountsAsPinned(t *testing.T) {
	// 40 hex characters. A sha cannot be repointed, so the code that runs is the code that was reviewed.
	const sha = "11bd71901bbe5b1630ceea73d27597364c9af683"

	w, err := ParseBytes([]byte(
		"on: push\npermissions:\n  contents: read\njobs:\n  a:\n    runs-on: ubuntu-latest\n" +
			"    timeout-minutes: 5\n    steps:\n      - uses: some-org/act@" + sha + "\n"))
	require.NoError(t, err)

	require.Empty(t, Check(w), "a sha-pinned third-party action is the pinned case")

	// The same action on a tag is not.
	w, err = ParseBytes([]byte(
		"on: push\npermissions:\n  contents: read\njobs:\n  a:\n    runs-on: ubuntu-latest\n" +
			"    timeout-minutes: 5\n    steps:\n      - uses: some-org/act@v3\n"))
	require.NoError(t, err)
	require.Len(t, Check(w), 1)
}

// TestPullRequestTargetCheckoutIsFound covers the most dangerous trigger.
func TestPullRequestTargetCheckoutIsFound(t *testing.T) {
	yaml := `
on: pull_request_target
permissions:
  contents: read
jobs:
  test:
    runs-on: ubuntu-latest
    timeout-minutes: 5
    steps:
      - uses: actions/checkout@v4
        with:
          ref: ${{ github.event.pull_request.head.sha }}
      - run: go test ./...
`

	w, err := ParseBytes([]byte(yaml))
	require.NoError(t, err)

	findings := Check(w)
	require.Len(t, findings, 1)
	require.Equal(t, "pull_request_target", findings[0].Rule)
	require.Contains(t, findings[0].Message, "writable token")

	// The same workflow on `pull_request` is fine: that trigger gives a read-only token and no secrets, which
	// is exactly why it exists.
	safe, err := ParseBytes([]byte(strings.Replace(yaml, "pull_request_target", "pull_request", 1)))
	require.NoError(t, err)
	require.Empty(t, Check(safe))
}

// TestMissingTimeoutIsFound is the cheapest rule and the one that saves the most runner minutes.
func TestMissingTimeoutIsFound(t *testing.T) {
	w, err := ParseBytes([]byte(
		"on: push\npermissions:\n  contents: read\njobs:\n  a:\n    runs-on: ubuntu-latest\n    steps: []\n"))
	require.NoError(t, err)

	findings := Check(w)
	require.Len(t, findings, 1)
	require.Equal(t, "timeout", findings[0].Rule)
	require.Contains(t, findings[0].Message, "360")
}

// TestFailFastDefaultIsFlagged is the matrix rule.
func TestFailFastDefaultIsFlagged(t *testing.T) {
	yaml := `
on: push
permissions:
  contents: read
jobs:
  a:
    runs-on: ubuntu-latest
    timeout-minutes: 5
    strategy:
      matrix:
        go: ["1.26", "1.27"]
    steps: []
`

	w, err := ParseBytes([]byte(yaml))
	require.NoError(t, err)

	findings := Check(w)
	require.Len(t, findings, 1)
	require.Equal(t, "fail-fast", findings[0].Rule)

	// Saying it explicitly, either way, silences the rule. The point is the decision, not the value.
	for _, value := range []string{"true", "false"} {
		explicit, err := ParseBytes([]byte(strings.Replace(yaml,
			"    strategy:\n      matrix:",
			"    strategy:\n      fail-fast: "+value+"\n      matrix:", 1)))
		require.NoError(t, err)
		require.Empty(t, Check(explicit), "fail-fast: %s", value)
	}
}

// TestReleaseExampleIsClean is the positive case.
//
// A checker that only ever fires is a checker nobody trusts. The release example is written to pass every rule,
// so the rules are demonstrated from both sides.
func TestReleaseExampleIsClean(t *testing.T) {
	for _, name := range []string{"release.yml", "scheduled.yml"} {
		t.Run(name, func(t *testing.T) {
			w := example(t, name)

			findings := Check(w)
			require.Empty(t, findings, "%v", findings)
		})
	}
}

// TestReleaseExampleOrdersItsJobs shows what `needs` does.
func TestReleaseExampleOrdersItsJobs(t *testing.T) {
	w := example(t, "release.yml")

	order, err := Order(w)
	require.NoError(t, err)
	require.Equal(t, []string{"build", "publish"}, order)

	require.Equal(t, []string{"build"}, w.Jobs["publish"].NeedsOf())
	require.Empty(t, w.Jobs["build"].NeedsOf(), "build has no dependency, so it starts immediately")
}

// TestACycleInNeedsIsReported covers the error GitHub reports vaguely.
func TestACycleInNeedsIsReported(t *testing.T) {
	w, err := ParseBytes([]byte(`
on: push
jobs:
  a:
    runs-on: ubuntu-latest
    needs: b
    steps: []
  b:
    runs-on: ubuntu-latest
    needs: a
    steps: []
`))
	require.NoError(t, err)

	_, err = Order(w)
	require.Error(t, err)
	require.Contains(t, err.Error(), "cycle")
	require.Contains(t, err.Error(), "[a b]")
}

// TestUndeclaredNeedsIsReported catches a renamed job.
func TestUndeclaredNeedsIsReported(t *testing.T) {
	w, err := ParseBytes([]byte(`
on: push
jobs:
  test:
    runs-on: ubuntu-latest
    needs: [lint, buidl]
    steps: []
  lint:
    runs-on: ubuntu-latest
    steps: []
`))
	require.NoError(t, err)

	bad := UndeclaredNeeds(w)
	require.Len(t, bad, 1)
	require.Contains(t, bad[0], `needs "buidl"`)
}

// ---------------------------------------------------------------------------
// The repository's own workflow.
//
// These are the assertions that make the module self-checking. If someone edits ci.yml in a way that breaks one
// of the claims in this module's README, these fail.
// ---------------------------------------------------------------------------

// ownCI parses this repository's workflow.
func ownCI(t *testing.T) *Workflow {
	t.Helper()

	w, err := Parse(filepath.Join(repoRoot(t), ".github", "workflows", "ci.yml"))
	require.NoError(t, err, "the repository's own workflow has to parse with this package's model")

	return w
}

// TestOwnCIParses is the first claim: the model covers a real workflow.
func TestOwnCIParses(t *testing.T) {
	w := ownCI(t)

	require.NotEmpty(t, w.Name)
	require.NotEmpty(t, w.Jobs)

	triggers, err := w.Triggers()
	require.NoError(t, err)
	require.Contains(t, triggers, "push")
	require.Contains(t, triggers, "pull_request")

	t.Logf("%d jobs, triggered by %v", len(w.Jobs), triggers)
}

// TestOwnCIIsClean runs every rule against the files that actually run, and requires no findings at all.
//
// The TestOwnCI* tests below each check one hand-picked rule, and for a while that was all there was. ci.yml
// failed the `timeout` rule on every one of its eleven jobs, the README said this module's tests would catch
// exactly that, and nothing did, because no test asked for the whole list to be empty. A checker that its own
// repository fails is advice rather than a check.
//
// Every workflow, not only ci.yml: a second file added later is exactly the one nobody thinks to point a test at.
func TestOwnCIIsClean(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(repoRoot(t), ".github", "workflows", "*.yml"))
	require.NoError(t, err)
	require.NotEmpty(t, files)

	for _, file := range files {
		w, err := Parse(file)
		require.NoError(t, err, file)

		for _, f := range Check(w) {
			t.Errorf("%s: %v", filepath.Base(file), f)
		}
	}
}

// TestOwnCIHasNoScriptInjection is the rule that matters most, applied to the file that actually runs.
func TestOwnCIHasNoScriptInjection(t *testing.T) {
	for _, f := range Check(ownCI(t)) {
		require.NotEqual(t, "script-injection", f.Rule, "%v", f)
		require.NotEqual(t, "pull_request_target", f.Rule, "%v", f)
	}
}

// TestOwnCINeedsAreConsistent catches a rename that a passing run would hide.
//
// A job referenced by a `needs` that does not exist fails the whole workflow at start, with a message that
// names the job rather than the file.
func TestOwnCINeedsAreConsistent(t *testing.T) {
	w := ownCI(t)

	require.Empty(t, UndeclaredNeeds(w))

	order, err := Order(w)
	require.NoError(t, err)
	require.Len(t, order, len(w.Jobs))

	t.Logf("job order: %v", order)
}

// TestOwnCIJobsAreIndependent is a claim about this repository's CI specifically.
//
// Nothing uses `needs`, so every job starts at once and the wall-clock time is the slowest job rather than the
// sum. That is the right shape for a repository where the jobs check different things, and it is the reason
// `make check` locally and CI disagree about how long the same work takes.
func TestOwnCIJobsAreIndependent(t *testing.T) {
	w := ownCI(t)

	var dependent []string

	for name, job := range w.Jobs {
		if len(job.NeedsOf()) > 0 {
			dependent = append(dependent, name)
		}
	}

	require.Empty(t, dependent,
		"every job runs in parallel; if this changes, the README's claim about wall-clock time changes with it")
}

// TestOwnCIPinsEveryThirdPartyAction is a supply-chain claim about this repository.
//
// Nearly every `uses` here is actions/*, which is GitHub's own. The one that is not is
// docker/setup-buildx-action, and it carries a commit sha rather than @v3.
//
// This test is how that happened. It was written expecting no third-party actions at all, failed on the buildx
// step, and the fix was to pin it. That is the module working on the repository that contains it.
func TestOwnCIPinsEveryThirdPartyAction(t *testing.T) {
	w := ownCI(t)

	firstParty, thirdParty := 0, 0

	for name, job := range w.Jobs {
		for _, step := range job.Steps {
			if step.Uses == "" || strings.HasPrefix(step.Uses, "./") {
				continue
			}

			owner, _, _ := strings.Cut(step.Uses, "/")
			if owner == "actions" || owner == "github" {
				firstParty++
				continue
			}

			thirdParty++

			_, ref, found := strings.Cut(step.Uses, "@")
			require.True(t, found, "%s: %s has no version at all", name, step.Uses)
			require.Regexp(t, `^[0-9a-f]{40}$`, ref,
				"%s: %s is third party and pinned to a mutable tag; use a commit sha", name, step.Uses)
		}
	}

	t.Logf("%d first-party uses, %d third-party (all sha-pinned)", firstParty, thirdParty)

	// And the checker agrees, which is the point of having both.
	for _, f := range Check(w) {
		require.NotEqual(t, "unpinned-action", f.Rule, "%v", f)
	}
}

// TestOwnCIServiceContainersHaveHealthChecks is a claim about this repository's test infrastructure.
//
// A service container with no health check is available the moment the container STARTS, not when the service
// inside it is ready. Postgres takes a second or two to accept connections, and a test that connects in that
// window fails or, worse, skips.
func TestOwnCIServiceContainersHaveHealthChecks(t *testing.T) {
	w := ownCI(t)

	total := 0

	for jobName, job := range w.Jobs {
		for serviceName, service := range job.Services {
			total++

			require.True(t, service.HasHealthCheck(),
				"%s/%s (%s) has no --health-cmd, so the job starts before the service is ready",
				jobName, serviceName, service.Image)
		}
	}

	require.Positive(t, total, "this repository does run service containers")
	t.Logf("%d service containers, all with health checks", total)
}

// TestEveryExampleWorkflowParses is the floor: a broken fixture should fail loudly.
func TestEveryExampleWorkflowParses(t *testing.T) {
	entries, err := os.ReadDir("../examples")
	require.NoError(t, err)

	var names []string

	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".yml" {
			continue
		}

		names = append(names, e.Name())

		t.Run(e.Name(), func(t *testing.T) {
			_, err := Parse(filepath.Join("..", "examples", e.Name()))
			require.NoError(t, err)
		})
	}

	slices.Sort(names)
	require.Equal(t, []string{"injectable.yml", "release.yml", "scheduled.yml"}, names)
}
