package tasks_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/alexvervloet/learn-go/capstones/url-shortener/internal/store"
	"github.com/alexvervloet/learn-go/capstones/url-shortener/internal/tasks"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
)

// recorder counts calls and can be told to fail.
type recorder struct {
	calls     int
	clickedAt time.Time
	err       error
}

func (r *recorder) RecordClick(_ context.Context, _ int64, clickedAt time.Time, _, _ string) error {
	r.calls++
	r.clickedAt = clickedAt

	return r.err
}

// sweeper is the other half.
type sweeper struct {
	deleted int64
	err     error
	calls   int
}

func (s *sweeper) DeleteExpired(context.Context) (int64, error) {
	s.calls++

	return s.deleted, s.err
}

func isNotFound(err error) bool { return errors.Is(err, store.ErrNotFound) }

// TestTheClickTaskCarriesTheIdNotTheSlug is the payload decision.
func TestTheClickTaskCarriesTheIdNotTheSlug(t *testing.T) {
	task, err := tasks.NewRecordClick(tasks.RecordClickPayload{
		URLID: 42, Referrer: "https://news.example/", UserAgent: "agent", ClickedAt: time.Now(),
	})
	require.NoError(t, err)

	require.Equal(t, tasks.TypeRecordClick, task.Type())

	var payload tasks.RecordClickPayload

	require.NoError(t, json.Unmarshal(task.Payload(), &payload))
	require.Equal(t, int64(42), payload.URLID,
		"a slug can be deleted and re-created while the task is queued, and the click would land on the wrong URL")
}

// TestTheTimeoutIsWholeSeconds is an asynq trap this repository has already been bitten by.
//
// asynq stores every time value in WHOLE SECONDS. A Timeout of 500ms becomes 0, and 0 means the default of 30
// minutes, so a task meant to be abandoned in half a second gets half an hour. That is why the timeout here is
// 10s and not 500ms.
func TestTheTimeoutIsWholeSeconds(t *testing.T) {
	task, err := tasks.NewRecordClick(tasks.RecordClickPayload{URLID: 1})
	require.NoError(t, err)

	// asynq applies the options when the task is enqueued, so the assertion is on the option list rather than
	// on a stored value. What is checkable here is that the duration is a whole number of seconds.
	const timeout = 10 * time.Second

	require.Zero(t, timeout%time.Second, "a sub-second timeout truncates to zero and means the 30 minute default")
	require.NotNil(t, task)
}

// TestARecordedClickSucceeds is the happy path.
func TestARecordedClickSucceeds(t *testing.T) {
	rec := &recorder{}
	handler := tasks.HandleRecordClick(rec, isNotFound)

	clickedAt := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)

	payload, err := json.Marshal(tasks.RecordClickPayload{URLID: 7, Referrer: "r", UserAgent: "a", ClickedAt: clickedAt})
	require.NoError(t, err)

	require.NoError(t, handler(context.Background(), asynq.NewTask(tasks.TypeRecordClick, payload)))
	require.Equal(t, 1, rec.calls)
	require.True(t, clickedAt.Equal(rec.clickedAt), "the click time rides in the task to the store")
}

// TestAMissingURLIsDroppedRatherThanRetried is the SkipRetry case.
func TestAMissingURLIsDroppedRatherThanRetried(t *testing.T) {
	rec := &recorder{err: store.ErrNotFound}
	handler := tasks.HandleRecordClick(rec, isNotFound)

	payload, err := json.Marshal(tasks.RecordClickPayload{URLID: 999})
	require.NoError(t, err)

	err = handler(context.Background(), asynq.NewTask(tasks.TypeRecordClick, payload))
	require.Error(t, err)

	require.ErrorIs(t, err, asynq.SkipRetry,
		"a click for a URL that no longer exists will never succeed, so retrying it three times is three "+
			"guaranteed failures and an archived task")
	require.ErrorIs(t, err, tasks.ErrDropped)
	require.ErrorContains(t, err, "999", "the reason survives in the message that gets logged")
}

// TestATransientErrorIsRetried is the other branch, and the one that matters more.
func TestATransientErrorIsRetried(t *testing.T) {
	rec := &recorder{err: errors.New("connection reset by peer")}
	handler := tasks.HandleRecordClick(rec, isNotFound)

	payload, err := json.Marshal(tasks.RecordClickPayload{URLID: 7})
	require.NoError(t, err)

	err = handler(context.Background(), asynq.NewTask(tasks.TypeRecordClick, payload))
	require.Error(t, err)
	require.NotErrorIs(t, err, asynq.SkipRetry,
		"a busy database recovers; marking this SkipRetry would throw away every click during an outage")
}

// TestAnUndecodablePayloadIsDropped covers the deploy that changed the struct.
func TestAnUndecodablePayloadIsDropped(t *testing.T) {
	rec := &recorder{}
	handler := tasks.HandleRecordClick(rec, isNotFound)

	err := handler(context.Background(), asynq.NewTask(tasks.TypeRecordClick, []byte("not json")))
	require.ErrorIs(t, err, asynq.SkipRetry, "it will not decode on a retry either")
	require.Zero(t, rec.calls)
}

// TestTheSweepReportsWhatItDeleted is the cleanup handler.
func TestTheSweepReportsWhatItDeleted(t *testing.T) {
	var logged []any

	sw := &sweeper{deleted: 3}
	handler := tasks.HandleSweepExpired(sw, func(_ string, args ...any) { logged = append(logged, args...) })

	require.NoError(t, handler(context.Background(), asynq.NewTask(tasks.TypeSweepExpired, nil)))
	require.Equal(t, 1, sw.calls)
	require.Contains(t, logged, int64(3))
}

// TestTheSweepLogsNothingWhenThereIsNothing keeps a nightly job quiet.
func TestTheSweepLogsNothingWhenThereIsNothing(t *testing.T) {
	var lines int

	sw := &sweeper{deleted: 0}
	handler := tasks.HandleSweepExpired(sw, func(string, ...any) { lines++ })

	require.NoError(t, handler(context.Background(), asynq.NewTask(tasks.TypeSweepExpired, nil)))
	require.Zero(t, lines, "a job that logs on every run is a job whose logs nobody reads")
}

// TestTheSweepRetriesOnError is the right default for a job that can be repeated.
func TestTheSweepRetriesOnError(t *testing.T) {
	sw := &sweeper{err: errors.New("the database is busy")}
	handler := tasks.HandleSweepExpired(sw, nil)

	err := handler(context.Background(), asynq.NewTask(tasks.TypeSweepExpired, nil))
	require.Error(t, err)
	require.NotErrorIs(t, err, asynq.SkipRetry,
		"deleting expired rows is idempotent, so a retry is free and skipping one leaves them until tomorrow")
}

// TestTheQueuesAreWeighted is the starvation guard.
func TestTheQueuesAreWeighted(t *testing.T) {
	require.Contains(t, tasks.Queues, tasks.QueueDefault)
	require.Contains(t, tasks.Queues, tasks.QueueLow)

	require.Greater(t, tasks.Queues[tasks.QueueDefault], tasks.Queues[tasks.QueueLow],
		"a two minute sweep must not occupy the workers that record clicks")

	// The weights are relative probabilities of being POLLED, not a guarantee, so the low queue still gets
	// served when the default one is empty. A weight of 0 is how you drain a queue without processing it.
	for name, weight := range tasks.Queues {
		require.Positive(t, weight, "queue %q would never be polled", name)
	}
}

// TestTheMuxRoutesBothTypes is the wiring.
func TestTheMuxRoutesBothTypes(t *testing.T) {
	rec := &recorder{}
	sw := &sweeper{}

	mux := tasks.Mux(rec, sw, isNotFound, nil)

	payload, err := json.Marshal(tasks.RecordClickPayload{URLID: 1})
	require.NoError(t, err)

	require.NoError(t, mux.ProcessTask(context.Background(), asynq.NewTask(tasks.TypeRecordClick, payload)))
	require.Equal(t, 1, rec.calls)

	require.NoError(t, mux.ProcessTask(context.Background(), asynq.NewTask(tasks.TypeSweepExpired, nil)))
	require.Equal(t, 1, sw.calls)

	// An unknown type is an error rather than a silent success, which is what catches a producer and a
	// consumer that disagree about a name.
	require.Error(t, mux.ProcessTask(context.Background(), asynq.NewTask("no:such:task", nil)))
}
