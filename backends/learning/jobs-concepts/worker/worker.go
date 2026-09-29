// Package worker is the handlers and the server that runs them.
//
// # Where a worker lives
//
// asynq's server runs in a Go process you write, which is unlike Celery's separate `celery worker` command. So
// a service can run its API and its worker in one binary behind a flag, or in two deployments from the same
// image, and the choice is a deployment decision rather than a framework one.
//
// One binary is simpler and couples the two: a deploy restarts both, and a worker under load competes with the
// API for CPU. Two deployments is more moving parts and lets them scale separately, which is usually the point
// of having a queue at all.
package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hibiken/asynq"

	"github.com/alexvervloet/learn-go/backends/learning/jobs-concepts/tasks"
)

// Recorder collects what the handlers did, so a test can assert on behaviour.
type Recorder struct {
	mu sync.Mutex

	Handled   []Attempt
	panics    atomic.Int64
	succeeded atomic.Int64
	failed    atomic.Int64
}

// Attempt is one handler invocation.
type Attempt struct {
	Type        string
	TaskID      string
	Retry       int
	MaxRetry    int
	Queue       string
	Err         error
	HadDeadline bool
	Deadline    time.Duration
}

func (r *Recorder) add(a Attempt) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.Handled = append(r.Handled, a)
}

// Attempts returns a copy.
func (r *Recorder) Attempts() []Attempt {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]Attempt(nil), r.Handled...)
}

// AttemptsFor returns the attempts for one task id.
func (r *Recorder) AttemptsFor(taskID string) []Attempt {
	r.mu.Lock()
	defer r.mu.Unlock()

	var out []Attempt

	for _, a := range r.Handled {
		if a.TaskID == taskID {
			out = append(out, a)
		}
	}

	return out
}

// Counts returns the successes, failures and panics.
func (r *Recorder) Counts() (succeeded, failed, panics int64) {
	return r.succeeded.Load(), r.failed.Load(), r.panics.Load()
}

// Reset clears it.
func (r *Recorder) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.Handled = nil
	r.succeeded.Store(0)
	r.failed.Store(0)
	r.panics.Store(0)
}

// Handlers holds the dependencies every handler needs.
type Handlers struct {
	Log      *slog.Logger
	Recorder *Recorder

	// SendWelcome is the work EmailWelcome does. Nil means "pretend it was sent", which is what the
	// demo needs; a test sets it to make the work fail.
	SendWelcome func(ctx context.Context, email string) error

	// Idempotent records which task ids have already succeeded, which is the consumer-side half of
	// at-least-once. Same caveat as everywhere else in this repo: a map is wrong for more than one
	// worker process, and the real one is a unique constraint in the same transaction as the work.
	idempotent sync.Map
}

// Mux builds the router.
//
// asynq.ServeMux matches on the task type with a PREFIX match, the same as http.ServeMux with a trailing slash:
// registering "email:" catches "email:welcome" and "email:digest". That is useful and it is also how a task ends
// up in the wrong handler when two prefixes overlap, so this registers exact types.
func (h *Handlers) Mux() *asynq.ServeMux {
	mux := asynq.NewServeMux()

	// Middleware, in order, outermost first. The same shape as HTTP middleware and the same rule:
	// recovery outermost, or it cannot catch a panic in anything above it.
	mux.Use(h.recover, h.logging)

	mux.HandleFunc(tasks.TypeEmailWelcome, h.EmailWelcome)
	mux.HandleFunc(tasks.TypeImageResize, h.ImageResize)
	mux.HandleFunc(tasks.TypeReportBuild, h.ReportBuild)
	mux.HandleFunc(tasks.TypeFlaky, h.Flaky)
	mux.HandleFunc(tasks.TypeSlow, h.Slow)
	mux.HandleFunc(tasks.TypePanicking, h.Panicking)

	return mux
}

// recover turns a panic into an error.
//
// # Why asynq needs this and grpc does too
//
// A panic in a handler kills the worker PROCESS, taking every other in-flight task with it. asynq does recover
// in its processor, so this is belt and braces rather than the only defence, and having it in the middleware
// chain means the recorder sees the panic and the log line has the task id.
func (h *Handlers) recover(next asynq.Handler) asynq.Handler {
	return asynq.HandlerFunc(func(ctx context.Context, t *asynq.Task) (err error) {
		defer func() {
			r := recover()
			if r == nil {
				return
			}

			h.Recorder.panics.Add(1)

			if h.Log != nil {
				h.Log.Error("task panicked",
					"type", t.Type(),
					"task_id", tasks.TaskID(ctx),
					"panic", fmt.Sprint(r))
			}

			// A plain error, so it is RETRIED. A panic is usually a bug and usually
			// deterministic, so retrying it is mostly waste; returning SkipRetry instead
			// would be defensible. Retrying is chosen here because a panic from a nil map
			// under a race is not deterministic, and the archive is where it ends up either
			// way after the retries run out.
			err = fmt.Errorf("panic: %v", r)
		}()

		return next.ProcessTask(ctx, t)
	})
}

// logging records every attempt.
func (h *Handlers) logging(next asynq.Handler) asynq.Handler {
	return asynq.HandlerFunc(func(ctx context.Context, t *asynq.Task) error {
		start := time.Now()

		// The deadline is read BEFORE the handler runs, not after. Reading it afterwards reports
		// how long is left, which for a task that timed out is zero and tells you nothing about
		// what it was given. That cost a confusing test result: "Timeout(1s): deadline 0s out".
		deadline, hasDeadline := tasks.Deadline(ctx)

		var budget time.Duration
		if hasDeadline {
			budget = time.Until(deadline)
		}

		err := next.ProcessTask(ctx, t)

		queue, _ := asynq.GetQueueName(ctx)

		attempt := Attempt{
			Type:        t.Type(),
			TaskID:      tasks.TaskID(ctx),
			Retry:       tasks.RetryCount(ctx),
			MaxRetry:    tasks.MaxRetry(ctx),
			Queue:       queue,
			Err:         err,
			HadDeadline: hasDeadline,
			Deadline:    budget,
		}

		h.Recorder.add(attempt)

		if err != nil {
			h.Recorder.failed.Add(1)
		} else {
			h.Recorder.succeeded.Add(1)
		}

		if h.Log != nil {
			h.Log.Info("task handled",
				"type", t.Type(),
				"task_id", attempt.TaskID,
				"queue", queue,
				// The retry count in every log line. A task on attempt 12 is a different
				// situation from one on attempt 1, and without this the two lines are
				// identical.
				"retry", attempt.Retry,
				"duration", time.Since(start),
				"error", err)
		}

		return err
	})
}

// EmailWelcome is a normal handler.
func (h *Handlers) EmailWelcome(ctx context.Context, t *asynq.Task) error {
	var p tasks.EmailWelcomePayload

	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		// A payload that will not decode can never decode, so retrying it is a slow way to
		// archive it. SkipRetry says so.
		return tasks.Permanent(fmt.Errorf("%w: %w", tasks.ErrBadPayload, err))
	}

	if p.Email == "" {
		return tasks.Permanent(fmt.Errorf("%w: no email address", tasks.ErrBadPayload))
	}

	// The idempotency check, keyed on the TASK id rather than on the payload. asynq's task id is
	// stable across retries, so a retry of work that already succeeded finds its own id here.
	id := tasks.TaskID(ctx)

	if _, done := h.idempotent.Load(id); done {
		// Not an error: the work is done and the queue is asking again, which is what
		// at-least-once means.
		return nil
	}

	if h.SendWelcome != nil {
		if err := h.SendWelcome(ctx, p.Email); err != nil {
			return err
		}
	}

	// Recorded AFTER the work, never before. Marking first turns a failure into a loss: the retry
	// finds the id, returns nil, and the email is never sent. The first version of this handler
	// did that, with LoadOrStore before the work; TestIdempotencyIsRecordedAfterTheWork is the
	// regression test.
	//
	// Check-then-record is not racy here, because asynq runs one task id at a time. What remains
	// is a crash between the send and the Store, and then the retry sends twice. The only fix for
	// that is making the record and the work one transaction, or giving the email provider an
	// idempotency key so the second send is a no-op on their side.
	h.idempotent.Store(id, true)

	return nil
}

// ImageResize checks the payload carries an id rather than the image.
func (h *Handlers) ImageResize(ctx context.Context, t *asynq.Task) error {
	var p tasks.ImageResizePayload

	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return tasks.Permanent(fmt.Errorf("%w: %w", tasks.ErrBadPayload, err))
	}

	if p.Width <= 0 || p.Height <= 0 {
		return tasks.Permanent(fmt.Errorf("%w: dimensions must be positive", tasks.ErrBadPayload))
	}

	return nil
}

// ReportBuild is a long-running handler that checks its deadline.
func (h *Handlers) ReportBuild(ctx context.Context, t *asynq.Task) error {
	var p tasks.ReportBuildPayload

	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return tasks.Permanent(fmt.Errorf("%w: %w", tasks.ErrBadPayload, err))
	}

	// A loop that checks the context between chunks, which is what makes a long task killable. A
	// handler that does the whole thing in one uninterruptible call cannot be stopped, and asynq
	// will report it as timed out while it keeps running.
	for i := range p.Rows {
		select {
		case <-ctx.Done():
			return fmt.Errorf("building report %s: stopped after %d of %d rows: %w",
				p.ReportID, i, p.Rows, ctx.Err())
		default:
		}

		time.Sleep(time.Microsecond)
	}

	return nil
}

// Flaky fails a set number of times before succeeding, for the retry tests.
func (h *Handlers) Flaky(ctx context.Context, t *asynq.Task) error {
	var p tasks.FlakyPayload

	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return tasks.Permanent(err)
	}

	if p.FailPermanently {
		return tasks.Permanent(errors.New("this can never succeed"))
	}

	if tasks.RetryCount(ctx) < p.FailuresWanted {
		return fmt.Errorf("%w: attempt %d of %d",
			tasks.ErrTransient, tasks.RetryCount(ctx)+1, p.FailuresWanted+1)
	}

	return nil
}

// Slow sleeps, with or without honouring the deadline.
func (h *Handlers) Slow(ctx context.Context, t *asynq.Task) error {
	var p tasks.SlowPayload

	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return tasks.Permanent(err)
	}

	d := tasks.Duration(p.Sleep, time.Second)

	if p.IgnoreContext {
		// The unkillable handler. asynq will report the task as timed out and retry it, and
		// THIS goroutine keeps running, so the worker is doing the work twice.
		time.Sleep(d)
		return nil
	}

	select {
	case <-ctx.Done():
		return fmt.Errorf("slow task %s: %w", p.ID, ctx.Err())
	case <-time.After(d):
		return nil
	}
}

// Panicking panics, for the recovery test.
func (h *Handlers) Panicking(context.Context, *asynq.Task) error {
	panic("handler exploded")
}

// ServerConfig is the worker's settings.
type ServerConfig struct {
	// Concurrency is how many tasks run at once IN THIS PROCESS. Not the same as the queue's
	// throughput: ten processes at concurrency 10 is 100 concurrent tasks, and that is the number
	// the database's connection pool has to survive.
	Concurrency int

	// Queues and their weights. asynq picks a queue in proportion to its weight rather than by
	// strict priority, which is what stops the low queue starving forever.
	Queues map[string]int

	// ShutdownTimeout is how long a graceful stop waits for in-flight tasks. Past it they are left
	// in the active set and recovered by the lease expiry, which means they RUN AGAIN.
	ShutdownTimeout time.Duration

	// HealthCheckInterval and its handler are how a worker reports that it cannot reach Redis. A
	// worker with no health check fails silently: it stops processing and nothing notices.
	HealthCheckInterval time.Duration

	// DelayedTaskCheckInterval is how often scheduled and retrying tasks are promoted to pending.
	//
	// asynq's default is 5 SECONDS, and that is the number behind "a task scheduled for 100ms from
	// now ran at 5s". It is a poll, not a timer: nothing wakes up when a task becomes due, a
	// forwarder checks on an interval.
	//
	// So "schedule" means "not before", never "at", and a queue whose tasks are latency-sensitive
	// needs this lowered, at the cost of a Redis round trip per interval per worker.
	DelayedTaskCheckInterval time.Duration

	// RetryDelay overrides the backoff. Nil uses asynq's default, which is exponential with jitter
	// starting around a second: right for production and slow for a test.
	RetryDelay asynq.RetryDelayFunc
}

// DefaultServerConfig is a starting point with the reasoning in the field comments.
func DefaultServerConfig() ServerConfig {
	return ServerConfig{
		Concurrency: 10,
		Queues: map[string]int{
			tasks.QueueCritical: 6,
			tasks.QueueDefault:  3,
			tasks.QueueLow:      1,
		},
		ShutdownTimeout:     8 * time.Second,
		HealthCheckInterval: 15 * time.Second,
	}
}

// NewServer builds an asynq server.
func NewServer(redisOpt asynq.RedisConnOpt, cfg ServerConfig, log *slog.Logger, onHealth func(error)) *asynq.Server {
	return asynq.NewServer(redisOpt, asynq.Config{
		Concurrency:              cfg.Concurrency,
		Queues:                   cfg.Queues,
		ShutdownTimeout:          cfg.ShutdownTimeout,
		HealthCheckInterval:      cfg.HealthCheckInterval,
		HealthCheckFunc:          onHealth,
		DelayedTaskCheckInterval: cfg.DelayedTaskCheckInterval,

		// RetryDelayFunc controls the backoff. asynq's default is exponential with jitter, which
		// is right for production; a test overrides it to keep the run short.
		RetryDelayFunc: retryDelay(cfg.RetryDelay),

		// ErrorHandler is called for every failed task, INCLUDING the last attempt. It is where a
		// dead-letter row or an alert goes, and without it a task that exhausts its retries is
		// archived silently.
		ErrorHandler: asynq.ErrorHandlerFunc(func(ctx context.Context, t *asynq.Task, err error) {
			if log == nil {
				return
			}

			level := slog.LevelWarn
			if tasks.IsLastAttempt(ctx) {
				// The last attempt is the one worth alerting on. Every earlier failure is
				// noise if the task eventually succeeds.
				level = slog.LevelError
			}

			log.Log(ctx, level, "task failed",
				"type", t.Type(),
				"task_id", tasks.TaskID(ctx),
				"retry", tasks.RetryCount(ctx),
				"max_retry", tasks.MaxRetry(ctx),
				"last_attempt", tasks.IsLastAttempt(ctx),
				"error", err)
		}),

		Logger: asynqSlog{log: log},
	})
}

// retryDelay returns the configured backoff or asynq's default.
func retryDelay(f asynq.RetryDelayFunc) asynq.RetryDelayFunc {
	if f != nil {
		return f
	}

	return asynq.DefaultRetryDelayFunc
}

// asynqSlog adapts slog to asynq's logger interface.
//
// asynq defines its own six-method Logger interface rather than taking an io.Writer or a slog.Handler, which
// was the normal thing to do before slog existed and is now an adapter in every project that uses it.
type asynqSlog struct{ log *slog.Logger }

// Debug implements asynq.Logger.
func (a asynqSlog) Debug(args ...any) { a.logAt(slog.LevelDebug, args...) }

// Info implements asynq.Logger.
func (a asynqSlog) Info(args ...any) { a.logAt(slog.LevelInfo, args...) }

// Warn implements asynq.Logger.
func (a asynqSlog) Warn(args ...any) { a.logAt(slog.LevelWarn, args...) }

// Error implements asynq.Logger.
func (a asynqSlog) Error(args ...any) { a.logAt(slog.LevelError, args...) }

// Fatal implements asynq.Logger.
//
// It logs at Error and does NOT exit, which is a deliberate difference from what the name promises. asynq calls
// Fatal for conditions it considers unrecoverable, and a library deciding to kill the process is not something
// to accept: the service decides that, and it may well prefer to keep serving HTTP while its worker is broken.
func (a asynqSlog) Fatal(args ...any) { a.logAt(slog.LevelError, args...) }

func (a asynqSlog) logAt(level slog.Level, args ...any) {
	if a.log == nil {
		return
	}

	a.log.Log(context.Background(), level, fmt.Sprint(args...))
}
