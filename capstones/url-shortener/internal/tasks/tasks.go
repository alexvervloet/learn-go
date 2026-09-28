// Package tasks is the work that happens after the redirect has been sent.
//
// # Why click tracking is a job and not a write
//
// A redirect's job is to return 302 with a Location header. Everything else is overhead on a request a person
// is waiting for. Recording a click is two writes in a transaction, and doing them inline means:
//
//   - the redirect is as slow as the slowest of three round trips rather than one cache read;
//   - the redirect FAILS when the database is busy, for a statistic nobody is watching in real time.
//
// Enqueueing is one Redis write that the handler does not wait to confirm beyond the enqueue itself, and the
// worker does the rest on its own time.
//
// # At-least-once, so the handler must tolerate a repeat
//
// asynq redelivers a task whose worker died mid-flight. Two clicks recorded for one visit is a wrong number and
// not a broken system, which is the right trade here: the alternative is an idempotency key per click, and a
// click has no natural one.
//
// That is a decision worth stating rather than discovering. A payment would need the key.
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
// Namespaced with a colon, which is asynq's convention and is also what lets the Inspector and the web UI group
// them. A bare name works and stops being readable at about six task types.
const (
	TypeRecordClick  = "click:record"
	TypeSweepExpired = "url:sweep-expired"
)

// RecordClickPayload is what a click task carries.
//
// The URL id, not the slug.
//
// A slug would need a lookup in the worker, and worse, a slug can be deleted and re-created while the task is
// in the queue, so the click would land on the wrong URL. The id is stable, and if the row is gone the worker
// has an unambiguous answer: drop the task.
type RecordClickPayload struct {
	URLID     int64     `json:"url_id"`
	Referrer  string    `json:"referrer"`
	UserAgent string    `json:"user_agent"`
	ClickedAt time.Time `json:"clicked_at"`
}

// NewRecordClick builds the task.
//
// # The options are the interesting part
//
//   - MaxRetry(3): a click is not worth retrying for a day. The default is 25, which for this payload means a
//     dead database produces a queue of tasks retrying for hours.
//   - Timeout: bounds one attempt. asynq stores every time value in WHOLE SECONDS, so a sub-second timeout
//     becomes zero and zero means the default of 30 minutes. That is a real trap and the reason this is 10s
//     rather than 500ms.
//   - Retention: keeps the task visible in the Inspector after it succeeds, which is how you answer "did this
//     actually run" without adding a log line per click.
func NewRecordClick(p RecordClickPayload) (*asynq.Task, error) {
	payload, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("tasks: encode click: %w", err)
	}

	return asynq.NewTask(TypeRecordClick, payload,
		asynq.MaxRetry(3),
		asynq.Timeout(10*time.Second),
		asynq.Retention(1*time.Hour),
		asynq.Queue(QueueDefault),
	), nil
}

// NewSweepExpired builds the cleanup task.
//
// # Unique, not TaskID
//
// Unique(ttl) refuses a second task with the same type and payload while one is pending, and the refusal is an
// error the caller can ignore. TaskID(id) refuses a duplicate id forever, which for a periodic task means the
// second run never happens.
//
// For a sweep that runs on a schedule, Unique with a TTL a little longer than the interval is the shape: a
// scheduler that fires twice because of a restart enqueues one task.
func NewSweepExpired() *asynq.Task {
	return asynq.NewTask(TypeSweepExpired, nil,
		asynq.MaxRetry(2),
		asynq.Timeout(2*time.Minute),
		asynq.Unique(10*time.Minute),
		asynq.Queue(QueueLow),
	)
}

// Queue names.
//
// # Why two
//
// So a sweep that takes two minutes cannot starve click recording. asynq's weights decide how often a worker
// looks at each queue, and without separate queues a slow task type occupies workers that a fast one needs.
const (
	QueueDefault = "default"
	QueueLow     = "low"
)

// Queues is the weighting.
//
// Six to one. The weights are RELATIVE probabilities of being polled, not a guarantee, so the low queue still
// gets served when the default one is empty. Setting a queue to weight 0 is how you drain one without
// processing it.
var Queues = map[string]int{
	QueueDefault: 6,
	QueueLow:     1,
}

// ClickRecorder is what the worker needs from the store.
//
// An interface here and not a *store.Store, so the worker package does not import the store package and a test
// can supply a counter. One method, defined where it is used, which is the Go shape.
type ClickRecorder interface {
	RecordClick(ctx context.Context, urlID int64, referrer, userAgent string) error
}

// ExpirySweeper is the other half.
type ExpirySweeper interface {
	DeleteExpired(ctx context.Context) (int64, error)
}

// ErrDropped wraps a task that should not be retried.
var ErrDropped = errors.New("tasks: dropped")

// HandleRecordClick returns the handler.
//
// # SkipRetry is the important line
//
// A click for a URL that no longer exists will never succeed, however many times it is tried. Returning a plain
// error puts it back on the queue to fail again three times and then sit in the archive.
//
// asynq.SkipRetry, joined to the error, tells the server to archive it immediately. The joined error is still
// what gets logged, so the reason survives.
func HandleRecordClick(recorder ClickRecorder, notFound func(error) bool) asynq.HandlerFunc {
	return func(ctx context.Context, t *asynq.Task) error {
		var p RecordClickPayload
		if err := json.Unmarshal(t.Payload(), &p); err != nil {
			// A payload that does not decode will not decode on a retry either. This is the other SkipRetry
			// case, and it is the one that happens on a deploy that changed the struct.
			return fmt.Errorf("%w: decode: %w: %w", ErrDropped, err, asynq.SkipRetry)
		}

		if err := recorder.RecordClick(ctx, p.URLID, p.Referrer, p.UserAgent); err != nil {
			if notFound(err) {
				return fmt.Errorf("%w: url %d is gone: %w", ErrDropped, p.URLID, asynq.SkipRetry)
			}

			// Anything else is retried: a busy database, a closed connection, a timeout.
			return fmt.Errorf("tasks: record click for url %d: %w", p.URLID, err)
		}

		return nil
	}
}

// HandleSweepExpired returns the cleanup handler.
func HandleSweepExpired(sweeper ExpirySweeper, log func(string, ...any)) asynq.HandlerFunc {
	return func(ctx context.Context, _ *asynq.Task) error {
		deleted, err := sweeper.DeleteExpired(ctx)
		if err != nil {
			return fmt.Errorf("tasks: sweep expired: %w", err)
		}

		if deleted > 0 && log != nil {
			log("swept expired urls", "deleted", deleted)
		}

		return nil
	}
}

// Mux wires the handlers.
func Mux(recorder ClickRecorder, sweeper ExpirySweeper, notFound func(error) bool, log func(string, ...any)) *asynq.ServeMux {
	mux := asynq.NewServeMux()
	mux.Handle(TypeRecordClick, HandleRecordClick(recorder, notFound))
	mux.Handle(TypeSweepExpired, HandleSweepExpired(sweeper, log))

	return mux
}
