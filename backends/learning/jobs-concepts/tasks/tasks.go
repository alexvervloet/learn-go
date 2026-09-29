// Package tasks is background jobs with asynq, and the four things a job queue has to get right.
//
// # What asynq is, against Celery
//
// The Python mirror of this module uses Celery, which is a framework: a decorator makes a function a task, a
// separate process runs the worker, and the result comes back through a "result backend". asynq is a library:
// a task is a name and a JSON payload, and a worker is a `asynq.Server` you start in your own binary. asynq can
// store a result (Task.ResultWriter, kept for the task's Retention), but it has no blocking wait for one.
//
// That last difference is the important one. Celery lets you write `result = add.delay(2, 2); result.get()`,
// which looks like a function call and is a distributed system pretending to be one: it blocks a web worker on
// a background worker, and if the background worker is down it blocks forever. asynq does not offer it, and
// having to write down where the result goes is the better default.
//
// # The four things
//
//	SERIALISATION  a task's payload crosses a process boundary, so it is bytes. A struct that
//	               serialises fine today and gains a field tomorrow has to decode against a worker
//	               running yesterday's code, and that is a schema problem with no schema.
//	RETRIES        a job that fails is retried, so every handler must be idempotent. The same rule
//	               as at-least-once delivery in messaging, for the same reason.
//	TIMEOUTS       a handler that hangs keeps a goroutine busy forever. asynq gives each task a
//	               deadline through the context, and a handler that ignores it is unkillable:
//	               asynq frees the slot and retries, and the old goroutine runs on regardless.
//	VISIBILITY     a queue with no dashboard is a queue nobody knows the depth of. asynq's Inspector
//	               is the API behind its web UI and it is what a test uses to assert on state.
//
// # What asynq guarantees
//
// At-least-once, by the same mechanism as everything else that says so: a task is moved to an "active" set when
// a worker picks it up and removed when the handler returns nil. A worker that dies mid-task leaves it in the
// active set, and after its lease expires asynq's recoverer retries it (or archives it, out of retries). It
// counts as a failed attempt, not a return to pending, so a crash costs a retry.
//
// So a handler that runs twice is normal traffic, not an incident.
package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/hibiken/asynq"
)

// Task type names.
//
// # Why a constant and why the colon
//
// The type name is the routing key: it is how a worker decides which handler runs. So it is a wire contract,
// and renaming one strands every task already in the queue: they will be dequeued, no handler will match, and
// asynq will retry them until they are archived.
//
// The `domain:action` convention is asynq's and is worth keeping. It groups related tasks in the dashboard and
// it makes a prefix match useful.
const (
	TypeEmailWelcome = "email:welcome"
	TypeEmailDigest  = "email:digest"
	TypeImageResize  = "image:resize"
	TypeReportBuild  = "report:build"
	TypeFlaky        = "test:flaky"
	TypeSlow         = "test:slow"
	TypePanicking    = "test:panicking"
	TypeUnregistered = "test:unregistered"
)

// Queue names, in priority order.
//
// asynq's queues are weighted rather than strictly ordered: with critical=6, default=3, low=1 a worker picks
// critical 60% of the time, not always. That is deliberate and it is the right default, because strict priority
// starves the low queue forever the moment the critical one is never empty.
const (
	QueueCritical = "critical"
	QueueDefault  = "default"
	QueueLow      = "low"
)

// EmailWelcomePayload is a task's arguments.
//
// # The versioning problem nobody mentions
//
// This struct is a wire format. A task enqueued now may be handled by a worker running the code from before the
// last deploy, because a rolling deploy has both versions running at once and the queue is shared.
//
// So the same rules as any schema: adding an optional field is safe, removing one is not, renaming one is not,
// and changing a type is not. The difference from an HTTP API is that nothing negotiates a version and nothing
// reports a mismatch: an unknown field is silently dropped by encoding/json, which is the failure mode that
// takes longest to find.
//
// The answer is the same as for protobuf: add fields, never remove or repurpose them, and when a real break is
// needed introduce a new task TYPE and drain the old one.
type EmailWelcomePayload struct {
	UserID string `json:"user_id"`
	Email  string `json:"email"`

	// Locale was added later, which is why it has an omitempty and a sensible zero value. A worker
	// running older code ignores it; a worker running newer code against an older payload gets "".
	Locale string `json:"locale,omitempty"`
}

// ImageResizePayload carries an id, not the image.
//
// # The rule that matters most about payloads
//
// A payload goes in Redis, is copied on every retry, and is held in memory by the worker. So it carries an
// IDENTIFIER and not the data: a 5 MB image in a task payload is 5 MB of Redis per queued job, and a queue of
// a thousand is 5 GB.
//
// This is the single most common job-queue mistake and it is invisible until the queue backs up.
type ImageResizePayload struct {
	ImageID string `json:"image_id"`
	Width   int    `json:"width"`
	Height  int    `json:"height"`
}

// ReportBuildPayload is a long-running task.
type ReportBuildPayload struct {
	ReportID string `json:"report_id"`
	Rows     int    `json:"rows"`
}

// FlakyPayload fails a given number of times before succeeding, for the retry tests.
type FlakyPayload struct {
	ID              string `json:"id"`
	FailuresWanted  int    `json:"failures_wanted"`
	FailPermanently bool   `json:"fail_permanently"`
}

// SlowPayload sleeps, for the timeout tests.
type SlowPayload struct {
	ID    string `json:"id"`
	Sleep string `json:"sleep"`

	// IgnoreContext makes the handler NOT check its deadline, which is what an unkillable task
	// looks like.
	IgnoreContext bool `json:"ignore_context"`
}

// NewEmailWelcome builds a task.
//
// Returning (*asynq.Task, error) rather than panicking on a marshal failure, because a payload that will not
// marshal is a programming error the caller can report with context. json.Marshal fails for a channel, a
// function, or a NaN, and all three reach here from a struct somebody changed.
func NewEmailWelcome(p EmailWelcomePayload) (*asynq.Task, error) {
	payload, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("marshalling %s: %w", TypeEmailWelcome, err)
	}

	return asynq.NewTask(TypeEmailWelcome, payload), nil
}

// NewImageResize builds a resize task.
func NewImageResize(p ImageResizePayload) (*asynq.Task, error) {
	payload, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("marshalling %s: %w", TypeImageResize, err)
	}

	return asynq.NewTask(TypeImageResize, payload), nil
}

// NewReportBuild builds a report task.
func NewReportBuild(p ReportBuildPayload) (*asynq.Task, error) {
	payload, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("marshalling %s: %w", TypeReportBuild, err)
	}

	return asynq.NewTask(TypeReportBuild, payload), nil
}

// NewFlaky builds a task that fails a set number of times.
func NewFlaky(p FlakyPayload) (*asynq.Task, error) {
	payload, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("marshalling %s: %w", TypeFlaky, err)
	}

	return asynq.NewTask(TypeFlaky, payload), nil
}

// NewSlow builds a task that sleeps.
func NewSlow(p SlowPayload) (*asynq.Task, error) {
	payload, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("marshalling %s: %w", TypeSlow, err)
	}

	return asynq.NewTask(TypeSlow, payload), nil
}

// ErrPermanent marks a failure that must NOT be retried.
//
// # Why this exists
//
// A job queue retries on error, which is right for a network blip and wrong for a malformed payload: a task
// that can never succeed is retried until it is archived, and every attempt costs a worker slot and a log line.
//
// asynq has asynq.SkipRetry for exactly this. Wrapping it in a package sentinel means a handler can return
// `fmt.Errorf("bad payload: %w", ErrPermanent)` and keep its own message, and errors.Is finds it.
var ErrPermanent = asynq.SkipRetry

// Permanent wraps an error so it will not be retried.
func Permanent(err error) error {
	return fmt.Errorf("%w (%w)", ErrPermanent, err)
}

// Errors a handler distinguishes.
var (
	ErrBadPayload = errors.New("malformed payload")
	ErrTransient  = errors.New("transient failure")
)

// Duration parses a payload's duration field.
//
// A string in the payload rather than a time.Duration, because a Duration marshals as an integer number of
// nanoseconds and a payload that reads `"sleep": 5000000000` is unreadable in a dashboard. The cost is a parse
// and a possible error, which is a reasonable trade for a value a human will look at.
func Duration(s string, fallback time.Duration) time.Duration {
	if s == "" {
		return fallback
	}

	d, err := time.ParseDuration(s)
	if err != nil {
		return fallback
	}

	return d
}

// Deadline reports the task's deadline and whether it has one.
//
// asynq sets it from the task's Timeout or Deadline option, and a handler that does not look at it is a handler
// that cannot be stopped.
func Deadline(ctx context.Context) (time.Time, bool) {
	return ctx.Deadline()
}

// TaskID reads asynq's task id out of the context.
//
// The id is what identifies an ATTEMPT's task across retries: it is stable, so it is the right key for an
// idempotency check. asynq.GetTaskID is the accessor, and a handler that wants to be idempotent needs it.
func TaskID(ctx context.Context) string {
	id, _ := asynq.GetTaskID(ctx)
	return id
}

// RetryCount reads how many times this task has already been retried.
//
// Zero on the first attempt. Worth having in every handler's log line: a task on attempt 12 is a different
// situation from one on attempt 1 and the log line looks identical without it.
func RetryCount(ctx context.Context) int {
	n, _ := asynq.GetRetryCount(ctx)
	return n
}

// MaxRetry reads the limit for this task.
func MaxRetry(ctx context.Context) int {
	n, _ := asynq.GetMaxRetry(ctx)
	return n
}

// IsLastAttempt reports whether this is the final try.
//
// The hook a handler needs to do something different on the last attempt: send an alert, write a dead-letter
// row, notify the user that their export failed. Without it, a task fails silently into the archive.
func IsLastAttempt(ctx context.Context) bool {
	return RetryCount(ctx) >= MaxRetry(ctx)
}
