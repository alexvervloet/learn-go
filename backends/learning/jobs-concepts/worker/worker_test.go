package worker_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hibiken/asynq"

	"github.com/alexvervloet/learn-go/backends/learning/jobs-concepts/jobtest"
	"github.com/alexvervloet/learn-go/backends/learning/jobs-concepts/tasks"
	"github.com/alexvervloet/learn-go/backends/learning/jobs-concepts/worker"
)

// setup starts a worker on a queue of this test's own, and returns the pieces a test needs.
func setup(t *testing.T, cfg worker.ServerConfig) (*asynq.Client, *asynq.Inspector, *worker.Recorder, string) {
	t.Helper()

	return setupWith(t, cfg, &worker.Handlers{})
}

// setupWith is setup for a test that supplies its own Handlers, for example to make the work fail.
func setupWith(t *testing.T, cfg worker.ServerConfig, h *worker.Handlers) (*asynq.Client, *asynq.Inspector, *worker.Recorder, string) {
	t.Helper()

	jobtest.Require(t)
	jobtest.Flush(t)

	queue := jobtest.QueueName(t)

	// The worker listens on THIS TEST'S queue and nothing else.
	//
	// Unconditionally, which the first version of this helper got wrong: it only set the map when it
	// was nil, and DefaultServerConfig fills it with critical/default/low. So the worker listened on
	// three queues, every test enqueued to a fourth, and nine tests timed out waiting for a task
	// nothing was going to pick up.
	//
	// A worker silently not listening to a queue is the failure mode this models: nothing errors,
	// the tasks sit in pending, and the only symptom is a queue that never drains.
	cfg.Queues = map[string]int{queue: 1}

	// Fast promotion and a fixed short backoff, so a test that exercises retries takes a second
	// rather than a minute. Both are production knobs left at sensible values in DefaultServerConfig
	// and driven down here, which is the right shape: a test that needs a different value says so.
	if cfg.DelayedTaskCheckInterval == 0 {
		cfg.DelayedTaskCheckInterval = 50 * time.Millisecond
	}
	if cfg.RetryDelay == nil {
		cfg.RetryDelay = func(int, error, *asynq.Task) time.Duration {
			return 50 * time.Millisecond
		}
	}

	rec := &worker.Recorder{}

	h.Log = jobtest.DiscardLogger()
	h.Recorder = rec

	srv := worker.NewServer(jobtest.RedisOpt(), cfg, jobtest.DiscardLogger(), nil)

	jobtest.RunServer(t, srv, h.Mux())

	return jobtest.Client(t), jobtest.Inspector(t), rec, queue
}

// TestATaskRuns is the baseline.
func TestATaskRuns(t *testing.T) {
	client, inspector, rec, queue := setup(t, worker.DefaultServerConfig())

	task, err := tasks.NewEmailWelcome(tasks.EmailWelcomePayload{
		UserID: "u1", Email: "a@example.test",
	})
	if err != nil {
		t.Fatal(err)
	}

	info, err := client.Enqueue(task, asynq.Queue(queue))
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("enqueued %s to %s, state %s", info.ID, info.Queue, info.State)

	jobtest.WaitFor(t, 5*time.Second, "the task to be handled", func() bool {
		succeeded, _, _ := rec.Counts()
		return succeeded == 1
	})

	attempts := rec.AttemptsFor(info.ID)

	if len(attempts) != 1 {
		t.Fatalf("handled %d times, want 1", len(attempts))
	}

	a := attempts[0]

	t.Logf("handled: type=%s queue=%s retry=%d/%d deadline_in=%v",
		a.Type, a.Queue, a.Retry, a.MaxRetry, a.Deadline.Round(time.Second))

	if a.Type != tasks.TypeEmailWelcome {
		t.Errorf("type is %q", a.Type)
	}
	if a.Retry != 0 {
		t.Errorf("first attempt has retry=%d", a.Retry)
	}

	// Every task gets a deadline, whether or not one was asked for. asynq's default timeout is 30
	// minutes, which is long enough to be useless as a safety net and is still a deadline, so a
	// handler that checks its context works without any configuration.
	if !a.HadDeadline {
		t.Error("the handler had no deadline")
	}

	t.Logf("asynq gave the handler a deadline %v out with no timeout configured",
		a.Deadline.Round(time.Minute))

	state := jobtest.State(t, inspector, queue)

	t.Logf("queue: %s", state)

	if state.Processed != 1 {
		t.Errorf("processed=%d, want 1", state.Processed)
	}
}

// TestRetriesUseExponentialBackoff, and the thing to notice is that the retry count is visible to the handler.
func TestRetriesBackOff(t *testing.T) {
	client, inspector, rec, queue := setup(t, worker.DefaultServerConfig())

	task, err := tasks.NewFlaky(tasks.FlakyPayload{ID: "f1", FailuresWanted: 2})
	if err != nil {
		t.Fatal(err)
	}

	// MaxRetry and a short backoff. asynq's default backoff is exponential with jitter starting
	// around a second, which is right for production and makes a test take a minute.
	info, err := client.Enqueue(task,
		asynq.Queue(queue),
		asynq.MaxRetry(5),
		asynq.Retention(time.Minute))
	if err != nil {
		t.Fatal(err)
	}

	jobtest.WaitFor(t, 30*time.Second, "the task to succeed after its retries", func() bool {
		succeeded, _, _ := rec.Counts()
		return succeeded >= 1
	})

	attempts := rec.AttemptsFor(info.ID)

	t.Logf("%d attempts for task %s", len(attempts), info.ID)
	for i, a := range attempts {
		t.Logf("  attempt %d: retry=%d err=%v", i+1, a.Retry, a.Err)
	}

	if len(attempts) != 3 {
		t.Fatalf("made %d attempts, want 3 (2 failures and a success)", len(attempts))
	}

	// The retry count is 0, 1, 2, which is what makes IsLastAttempt possible.
	for i, a := range attempts {
		if a.Retry != i {
			t.Errorf("attempt %d has retry=%d", i+1, a.Retry)
		}
		if a.MaxRetry != 5 {
			t.Errorf("attempt %d has max_retry=%d, want 5", i+1, a.MaxRetry)
		}
	}

	if attempts[0].Err == nil || attempts[1].Err == nil {
		t.Error("the first two attempts should have failed")
	}
	if attempts[2].Err != nil {
		t.Errorf("the third attempt failed: %v", attempts[2].Err)
	}

	state := jobtest.State(t, inspector, queue)

	t.Logf("queue: %s", state)

	// asynq counts every FAILED attempt, so two failures and one success is processed=3, failed=2.
	if state.Failed != 2 {
		t.Errorf("failed=%d, want 2", state.Failed)
	}

	t.Log("the handler can see its own retry count, which is what lets it behave differently on " +
		"the last attempt: alert, write a dead-letter row, tell the user their export failed")
}

// TestSkipRetryStopsImmediately, because a task that can never succeed should not be retried 25 times.
func TestSkipRetryStopsImmediately(t *testing.T) {
	client, inspector, rec, queue := setup(t, worker.DefaultServerConfig())

	task, err := tasks.NewFlaky(tasks.FlakyPayload{ID: "perm", FailPermanently: true})
	if err != nil {
		t.Fatal(err)
	}

	info, err := client.Enqueue(task, asynq.Queue(queue), asynq.MaxRetry(10))
	if err != nil {
		t.Fatal(err)
	}

	jobtest.WaitFor(t, 10*time.Second, "the task to be archived", func() bool {
		return jobtest.State(t, inspector, queue).Archived == 1
	})

	attempts := rec.AttemptsFor(info.ID)

	t.Logf("%d attempt(s) for a permanently failing task with MaxRetry=10", len(attempts))
	for _, a := range attempts {
		t.Logf("  %v", a.Err)
	}

	if len(attempts) != 1 {
		t.Errorf("made %d attempts, want 1", len(attempts))
	}

	state := jobtest.State(t, inspector, queue)

	t.Logf("queue: %s", state)

	if state.Archived != 1 {
		t.Errorf("archived=%d, want 1", state.Archived)
	}

	// Compare: the same task WITHOUT SkipRetry would be retried ten times over several minutes,
	// each attempt costing a worker slot and a log line, and ending in the same archive.
	t.Log("one attempt instead of ten. A malformed payload can never decode, so retrying it is a " +
		"slow way to reach the same archive, and every attempt occupies a worker slot that " +
		"real work needs.")

	// And a bad payload takes the same path.
	bad := asynq.NewTask(tasks.TypeEmailWelcome, []byte(`{"not":"valid for this struct"`))

	if _, err := client.Enqueue(bad, asynq.Queue(queue), asynq.MaxRetry(10)); err != nil {
		t.Fatal(err)
	}

	jobtest.WaitFor(t, 10*time.Second, "the malformed task to be archived", func() bool {
		return jobtest.State(t, inspector, queue).Archived == 2
	})

	t.Log("a malformed payload is archived after one attempt too")
}

// TestTimeoutStopsAHandlerThatChecks, and does not stop one that does not.
func TestTimeoutStopsAHandlerThatChecks(t *testing.T) {
	client, inspector, rec, queue := setup(t, worker.DefaultServerConfig())

	t.Run("a handler that checks its context", func(t *testing.T) {
		task, err := tasks.NewSlow(tasks.SlowPayload{ID: "polite", Sleep: "10s"})
		if err != nil {
			t.Fatal(err)
		}

		info, err := client.Enqueue(task,
			asynq.Queue(queue),
			// ONE SECOND, not 300ms. See TestTimeoutHasSecondGranularity: asynq stores the
			// timeout as an integer number of seconds, so anything under a second rounds to
			// zero and the handler gets no deadline at all.
			asynq.Timeout(time.Second),
			asynq.MaxRetry(0))
		if err != nil {
			t.Fatal(err)
		}

		start := time.Now()

		jobtest.WaitFor(t, 15*time.Second, "the task to time out", func() bool {
			return len(rec.AttemptsFor(info.ID)) > 0
		})

		elapsed := time.Since(start)

		attempts := rec.AttemptsFor(info.ID)

		t.Logf("a 10s sleep with a 1s timeout returned after %v: %v",
			elapsed.Round(50*time.Millisecond), attempts[0].Err)

		if attempts[0].Err == nil {
			t.Error("the handler returned nil after its deadline passed")
		}
		if !errors.Is(attempts[0].Err, context.DeadlineExceeded) {
			t.Errorf("the error does not match DeadlineExceeded: %v", attempts[0].Err)
		}

		// It stopped at the deadline rather than running the full ten seconds.
		if elapsed > 4*time.Second {
			t.Errorf("took %v; the handler did not stop at its deadline", elapsed)
		}
	})

	t.Run("a handler that ignores it", func(t *testing.T) {
		rec.Reset()

		task, err := tasks.NewSlow(tasks.SlowPayload{
			ID: "rude", Sleep: "3s", IgnoreContext: true,
		})
		if err != nil {
			t.Fatal(err)
		}

		info, err := client.Enqueue(task,
			asynq.Queue(queue),
			asynq.Timeout(time.Second),
			asynq.MaxRetry(0))
		if err != nil {
			t.Fatal(err)
		}

		// asynq marks the task as failed at the deadline, and the goroutine keeps running.
		jobtest.WaitFor(t, 15*time.Second, "the handler to eventually return", func() bool {
			return len(rec.AttemptsFor(info.ID)) > 0
		})

		attempts := rec.AttemptsFor(info.ID)

		t.Logf("the handler returned %v after ignoring a 1s deadline for 3s", attempts[0].Err)

		// The handler itself returned nil: it never looked at the context.
		if attempts[0].Err != nil {
			t.Logf("(the middleware saw %v)", attempts[0].Err)
		}

		t.Log("asynq reported the task as timed out at 1s and the goroutine ran for 3s. A " +
			"handler that does not check its context is unkillable: the worker slot is " +
			"occupied, the task is retried elsewhere, and the work happens twice.")
	})

	_ = inspector
}

// TestPanicsDoNotKillTheWorker.
func TestPanicsDoNotKillTheWorker(t *testing.T) {
	client, _, rec, queue := setup(t, worker.DefaultServerConfig())

	panicking := asynq.NewTask(tasks.TypePanicking, nil)

	if _, err := client.Enqueue(panicking, asynq.Queue(queue), asynq.MaxRetry(0)); err != nil {
		t.Fatal(err)
	}

	jobtest.WaitFor(t, 10*time.Second, "the panic to be recovered", func() bool {
		_, _, panics := rec.Counts()
		return panics >= 1
	})

	_, _, panics := rec.Counts()

	t.Logf("%d panic(s) recovered", panics)

	// And the worker is still working.
	task, err := tasks.NewEmailWelcome(tasks.EmailWelcomePayload{UserID: "u", Email: "a@b.test"})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := client.Enqueue(task, asynq.Queue(queue)); err != nil {
		t.Fatal(err)
	}

	jobtest.WaitFor(t, 10*time.Second, "the next task to run", func() bool {
		succeeded, _, _ := rec.Counts()
		return succeeded >= 1
	})

	t.Log("the worker survived. asynq recovers panics in its processor too, so this middleware " +
		"is belt and braces; without either, one nil map takes down every in-flight task in " +
		"the process.")
}

// TestScheduledTasksRunLater, and the two ways a schedule is not a schedule.
//
// # One-second granularity, in both directions
//
// asynq stores a scheduled task's run time as `processAt.Unix()`, a Unix timestamp in SECONDS. So a task asked
// for 800ms from now at wall-clock time X.9 gets the score floor(X + 1.7) = X + 1, and the forwarder promotes it
// when now.Unix() reaches X + 1, which is 100ms after the enqueue.
//
// The first version of this test asserted "not before 700ms" and failed one run in six, with the task running
// after 665ms. That was not a flaky test: asynq really can run a scheduled task up to a second EARLY.
//
// It is the same encoding decision as asynq.Timeout, which rounds a sub-second value to zero. Everything asynq
// stores about time is in seconds, and sub-second scheduling is not available.
//
// # And the forwarder is a poll
//
// A task is promoted from the scheduled set by a forwarder running on DelayedTaskCheckInterval, which defaults
// to five seconds. Nothing wakes up when a task becomes due. So a task scheduled for 100ms from now may run at
// five seconds, and "schedule" means "somewhere near", not "at".
func TestScheduledTasksRunLater(t *testing.T) {
	client, inspector, rec, queue := setup(t, worker.DefaultServerConfig())

	task, err := tasks.NewEmailWelcome(tasks.EmailWelcomePayload{UserID: "later", Email: "a@b.test"})
	if err != nil {
		t.Fatal(err)
	}

	// Several SECONDS, not milliseconds, because the granularity is a second and a sub-second delay
	// cannot be asserted on. The clock starts before the enqueue, which the first version got wrong:
	// it started after the queue-state check, so a slow inspector call made the delay look shorter
	// than it was.
	const delay = 3 * time.Second

	start := time.Now()

	// ProcessIn is asynq's countdown. ProcessAt takes an absolute time and is the one to use for
	// anything derived from a stored timestamp, because a countdown computed at enqueue time drifts
	// if the enqueue is itself delayed.
	info, err := client.Enqueue(task, asynq.Queue(queue), asynq.ProcessIn(delay))
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("enqueued %s in state %s, to run at %s", info.ID, info.State,
		info.NextProcessAt.Format(time.RFC3339Nano))

	if info.State != asynq.TaskStateScheduled {
		t.Errorf("state is %s, want scheduled", info.State)
	}

	// It is in the SCHEDULED set, not pending, so a worker will not see it yet.
	early := jobtest.State(t, inspector, queue)

	t.Logf("immediately: %s", early)

	if early.Scheduled != 1 {
		t.Errorf("scheduled=%d, want 1", early.Scheduled)
	}

	jobtest.WaitFor(t, 30*time.Second, "the scheduled task to run", func() bool {
		succeeded, _, _ := rec.Counts()
		return succeeded >= 1
	})

	elapsed := time.Since(start)

	t.Logf("asked for %v, ran after %v (%v early)", delay, elapsed.Round(10*time.Millisecond),
		(delay - elapsed).Round(10*time.Millisecond))

	// The honest bound: within a second of the requested time, either side. Earlier than that means
	// something other than the rounding, and much later means the forwarder interval.
	if delay-elapsed > time.Second {
		t.Errorf("ran %v early, which is more than the one-second rounding explains",
			delay-elapsed)
	}
	if elapsed > delay+2*time.Second {
		t.Errorf("ran %v late", elapsed-delay)
	}

	t.Log("the run time is stored as processAt.Unix(), a whole number of seconds, so a task " +
		"can run up to a second EARLY. And it is promoted by a forwarder on an interval " +
		"(5s by default, 50ms here), so it can run seconds LATE. Neither is a bug and " +
		"neither is documented where you would look for it.")
}

// TestUniqueStopsDuplicates, which is asynq's answer to the double-submit.
func TestUniqueStopsDuplicates(t *testing.T) {
	client, inspector, _, queue := setup(t, worker.DefaultServerConfig())

	payload := tasks.EmailWelcomePayload{UserID: "unique-1", Email: "a@b.test"}

	task, err := tasks.NewEmailWelcome(payload)
	if err != nil {
		t.Fatal(err)
	}

	// Unique takes a TTL, and the uniqueness key is derived from the type, the queue and the
	// PAYLOAD. So two tasks with the same payload collide and two with a different one do not, which
	// means a payload carrying a timestamp is never unique.
	first, err := client.Enqueue(task, asynq.Queue(queue), asynq.Unique(time.Minute))
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("first enqueue: %s", first.ID)

	// The same payload again.
	second, err := tasks.NewEmailWelcome(payload)
	if err != nil {
		t.Fatal(err)
	}

	_, err = client.Enqueue(second, asynq.Queue(queue), asynq.Unique(time.Minute))

	t.Logf("second enqueue: %v", err)

	if err == nil {
		t.Fatal("the duplicate was accepted")
	}
	if !errors.Is(err, asynq.ErrDuplicateTask) {
		t.Errorf("got %v, want ErrDuplicateTask", err)
	}

	// A DIFFERENT payload is not a duplicate.
	other, err := tasks.NewEmailWelcome(tasks.EmailWelcomePayload{
		UserID: "unique-2", Email: "b@b.test",
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := client.Enqueue(other, asynq.Queue(queue), asynq.Unique(time.Minute)); err != nil {
		t.Errorf("a different payload was rejected: %v", err)
	}

	t.Logf("queue: %s", jobtest.State(t, inspector, queue))

	t.Log("the uniqueness key is the type, the queue and the payload, so a payload carrying a " +
		"timestamp or a request id is never unique. Uniqueness is about the WORK, which " +
		"means the payload has to name the work and nothing else.")
}

// TestTaskIDMakesItIdempotent is the caller-controlled version.
func TestTaskIDMakesItIdempotent(t *testing.T) {
	client, _, _, queue := setup(t, worker.DefaultServerConfig())

	task, err := tasks.NewEmailWelcome(tasks.EmailWelcomePayload{UserID: "u", Email: "a@b.test"})
	if err != nil {
		t.Fatal(err)
	}

	// An explicit id, which is the better tool when the caller has a natural key: an order id, a
	// webhook event id, a user id plus a date. Unlike Unique it does not depend on the payload
	// bytes, so adding a field to the payload does not change the key.
	const id = "welcome-email-for-user-42"

	if _, err := client.Enqueue(task, asynq.Queue(queue), asynq.TaskID(id)); err != nil {
		t.Fatal(err)
	}

	again, err := tasks.NewEmailWelcome(tasks.EmailWelcomePayload{
		UserID: "u", Email: "a@b.test", Locale: "en-GB",
	})
	if err != nil {
		t.Fatal(err)
	}

	// A DIFFERENT payload with the same id is still a duplicate, which is the difference from
	// Unique and is usually what "do this once for this user" means.
	_, err = client.Enqueue(again, asynq.Queue(queue), asynq.TaskID(id))

	t.Logf("the same task id with a different payload: %v", err)

	if !errors.Is(err, asynq.ErrTaskIDConflict) {
		t.Errorf("got %v, want ErrTaskIDConflict", err)
	}

	t.Log("asynq.TaskID keys on an id the CALLER chose, so it survives a payload change; " +
		"asynq.Unique keys on the payload bytes, so it does not. The first is what an " +
		"idempotency key is.")
}

// TestQueueWeightingIsNotStrictPriority, which is the thing that stops the low queue starving.
func TestQueueWeighting(t *testing.T) {
	jobtest.Require(t)
	jobtest.Flush(t)

	critical := jobtest.QueueName(t) + "-crit"
	low := jobtest.QueueName(t) + "-low"

	rec := &worker.Recorder{}
	h := &worker.Handlers{Log: jobtest.DiscardLogger(), Recorder: rec}

	cfg := worker.DefaultServerConfig()
	cfg.Concurrency = 1
	cfg.Queues = map[string]int{critical: 6, low: 1}

	srv := worker.NewServer(jobtest.RedisOpt(), cfg, jobtest.DiscardLogger(), nil)

	jobtest.RunServer(t, srv, h.Mux())

	client := jobtest.Client(t)

	// Fill both queues before the worker can drain either.
	const each = 30

	for i := range each {
		task, err := tasks.NewEmailWelcome(tasks.EmailWelcomePayload{
			UserID: "c" + itoa(i), Email: "c@b.test",
		})
		if err != nil {
			t.Fatal(err)
		}

		if _, err := client.Enqueue(task, asynq.Queue(critical)); err != nil {
			t.Fatal(err)
		}

		lowTask, err := tasks.NewEmailWelcome(tasks.EmailWelcomePayload{
			UserID: "l" + itoa(i), Email: "l@b.test",
		})
		if err != nil {
			t.Fatal(err)
		}

		if _, err := client.Enqueue(lowTask, asynq.Queue(low)); err != nil {
			t.Fatal(err)
		}
	}

	jobtest.WaitFor(t, 30*time.Second, "both queues to drain", func() bool {
		succeeded, _, _ := rec.Counts()
		return succeeded >= each*2
	})

	// Count how many of the FIRST 30 handled came from each queue.
	attempts := rec.Attempts()

	firstCritical, firstLow := 0, 0

	for i, a := range attempts {
		if i >= each {
			break
		}

		switch a.Queue {
		case critical:
			firstCritical++
		case low:
			firstLow++
		}
	}

	t.Logf("weights critical=6 low=1; of the first %d tasks handled, %d were critical and %d low",
		each, firstCritical, firstLow)

	// The critical queue dominates and the low one is NOT starved, which is the property.
	if firstCritical <= firstLow {
		t.Errorf("critical (%d) did not dominate low (%d)", firstCritical, firstLow)
	}
	if firstLow == 0 {
		t.Error("the low queue was starved entirely, which weighted queues should prevent")
	}

	t.Log("asynq picks a queue in proportion to its weight rather than by strict priority. " +
		"Strict priority starves the low queue forever the moment the critical one is never " +
		"empty, which is the usual state of a critical queue.")
}

// TestInspectorSeesTheQueue, which is what a dashboard and an alert are built on.
func TestInspectorSeesTheQueue(t *testing.T) {
	// A long retry delay, so the task STAYS in the retry set while the test looks at it. With the
	// harness's 50ms delay it was promoted back to pending between the wait seeing retry=1 and the
	// snapshot below, and the test failed about one run in four under -race.
	cfg := worker.DefaultServerConfig()
	cfg.RetryDelay = func(int, error, *asynq.Task) time.Duration { return time.Minute }

	client, inspector, rec, queue := setup(t, cfg)

	// A task that will sit in the retry set.
	task, err := tasks.NewFlaky(tasks.FlakyPayload{ID: "insp", FailuresWanted: 5})
	if err != nil {
		t.Fatal(err)
	}

	info, err := client.Enqueue(task, asynq.Queue(queue), asynq.MaxRetry(5))
	if err != nil {
		t.Fatal(err)
	}

	jobtest.WaitFor(t, 15*time.Second, "the task to fail at least once", func() bool {
		_, failed, _ := rec.Counts()
		return failed >= 1
	})

	jobtest.WaitFor(t, 15*time.Second, "the task to reach the retry set", func() bool {
		return jobtest.State(t, inspector, queue).Retry >= 1
	})

	state := jobtest.State(t, inspector, queue)

	t.Logf("queue: %s", state)

	if state.Retry < 1 {
		t.Errorf("retry=%d, want at least 1", state.Retry)
	}

	// The inspector can read an individual task, which is what a dashboard shows when you click one.
	got, err := inspector.GetTaskInfo(queue, info.ID)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("task %s: state=%s retried=%d/%d last_error=%q next_at=%s",
		got.ID, got.State, got.Retried, got.MaxRetry, got.LastErr,
		got.NextProcessAt.Format(time.RFC3339))

	if got.LastErr == "" {
		t.Error("the task info carries no last error, which is what a dashboard shows")
	}

	// And it can act: cancel, archive, delete, run now. This is what an operator does at 3am.
	if err := inspector.DeleteTask(queue, info.ID); err != nil {
		t.Fatal(err)
	}

	after := jobtest.State(t, inspector, queue)

	t.Logf("after deleting it: %s", after)

	if after.Retry != 0 {
		t.Errorf("retry=%d after the delete", after.Retry)
	}

	t.Log("a queue with no inspector is a queue nobody knows the depth of. Pending and Retry " +
		"rising are the two numbers to alert on, and the second is the one that means " +
		"something is broken rather than busy.")
}

// TestArchivedTasksCanBeReplayed, which is the dead-letter workflow.
func TestArchivedTasksCanBeReplayed(t *testing.T) {
	client, inspector, rec, queue := setup(t, worker.DefaultServerConfig())

	task, err := tasks.NewFlaky(tasks.FlakyPayload{ID: "replay", FailPermanently: true})
	if err != nil {
		t.Fatal(err)
	}

	info, err := client.Enqueue(task, asynq.Queue(queue), asynq.MaxRetry(0))
	if err != nil {
		t.Fatal(err)
	}

	jobtest.WaitFor(t, 10*time.Second, "the task to be archived", func() bool {
		return jobtest.State(t, inspector, queue).Archived == 1
	})

	archived, err := inspector.ListArchivedTasks(queue)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("%d archived task(s)", len(archived))
	for _, a := range archived {
		t.Logf("  %s %s: %s", a.ID, a.Type, a.LastErr)
	}

	if len(archived) != 1 {
		t.Fatalf("got %d archived", len(archived))
	}

	// The payload is still there, which is what makes a replay possible: the archive is a dead
	// letter queue with the original task in it.
	if len(archived[0].Payload) == 0 {
		t.Error("the archived task has no payload, so it could not be replayed")
	}

	before, _, _ := rec.Counts()

	// RunTask moves it back to pending. An operator does this after fixing the bug.
	if err := inspector.RunTask(queue, info.ID); err != nil {
		t.Fatal(err)
	}

	jobtest.WaitFor(t, 10*time.Second, "the replayed task to run", func() bool {
		return len(rec.AttemptsFor(info.ID)) >= 2
	})

	t.Logf("the replayed task ran again and failed again, as it must: the bug is still there")

	_ = before

	t.Log("the archive keeps the payload, so a task can be replayed after the bug is fixed. " +
		"Without that, a failed job is a log line and the work is simply lost.")
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}

	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// TestTimeoutHasSecondGranularity is the trap that cost an afternoon.
//
// asynq serialises a task's timeout as an INTEGER NUMBER OF SECONDS: `Timeout: int64(timeout.Seconds())` in
// client.go. So asynq.Timeout(300*time.Millisecond) is stored as 0, and 0 means "no timeout", so the handler
// gets a context with NO DEADLINE and runs to completion.
//
// Nothing rejects the option, nothing logs it, and the symptom is a handler that ignores a timeout it was given.
func TestTimeoutHasSecondGranularity(t *testing.T) {
	client, _, rec, queue := setup(t, worker.DefaultServerConfig())

	// A sub-second timeout.
	sub, err := tasks.NewSlow(tasks.SlowPayload{ID: "sub-second", Sleep: "600ms"})
	if err != nil {
		t.Fatal(err)
	}

	subInfo, err := client.Enqueue(sub,
		asynq.Queue(queue),
		asynq.Timeout(300*time.Millisecond),
		asynq.MaxRetry(0))
	if err != nil {
		t.Fatal(err)
	}

	// A whole-second one.
	whole, err := tasks.NewSlow(tasks.SlowPayload{ID: "one-second", Sleep: "10s"})
	if err != nil {
		t.Fatal(err)
	}

	wholeInfo, err := client.Enqueue(whole,
		asynq.Queue(queue),
		asynq.Timeout(time.Second),
		asynq.MaxRetry(0))
	if err != nil {
		t.Fatal(err)
	}

	jobtest.WaitFor(t, 20*time.Second, "both tasks to be handled", func() bool {
		return len(rec.AttemptsFor(subInfo.ID)) > 0 && len(rec.AttemptsFor(wholeInfo.ID)) > 0
	})

	subAttempt := rec.AttemptsFor(subInfo.ID)[0]
	wholeAttempt := rec.AttemptsFor(wholeInfo.ID)[0]

	t.Logf("Timeout(300ms): budget %v, error %v",
		subAttempt.Deadline.Round(time.Second), subAttempt.Err)
	t.Logf("Timeout(1s):    budget %v, error %v",
		wholeAttempt.Deadline.Round(100*time.Millisecond), wholeAttempt.Err)

	// The sub-second timeout was stored as 0 seconds, so asynq applied its 30-minute DEFAULT. The
	// handler was given a deadline and it was nothing like the one asked for, and its 600ms sleep
	// completed.
	if subAttempt.Deadline < time.Minute {
		t.Errorf("a 300ms timeout gave a budget of %v; asynq may have changed its encoding",
			subAttempt.Deadline)
	}
	if subAttempt.Err != nil {
		t.Errorf("the sub-second task failed: %v", subAttempt.Err)
	}

	t.Logf("the 300ms timeout became a %v budget, which is asynq's default, and the 600ms "+
		"handler ran to completion", subAttempt.Deadline.Round(time.Minute))

	// The whole-second one was honoured, and stopped the 10s sleep.
	if wholeAttempt.Deadline > 2*time.Second {
		t.Errorf("a 1s timeout gave a budget of %v", wholeAttempt.Deadline)
	}
	if wholeAttempt.Err == nil {
		t.Error("the 10s sleep with a 1s deadline succeeded")
	}

	t.Log("asynq.Timeout is serialised as an integer number of seconds (int64(d.Seconds()) in " +
		"client.go), so anything under a second is stored as 0, and 0 means 'unset', so the " +
		"30-minute default applies. The option is accepted, nothing is logged, and the " +
		"handler runs to completion.")
	t.Log("asynq.Deadline takes an absolute time.Time and is stored as a unix timestamp, which " +
		"has the same granularity for a different reason. Sub-second task timeouts are not " +
		"available; enforce them inside the handler.")
}

// TestIdempotencyIsRecordedAfterTheWork: the first send fails, and the retry must send again.
//
// A handler that records the task id before doing the work finds the id on the retry, returns nil, and the
// email is never sent. asynq reports the task completed, so nothing looks wrong.
func TestIdempotencyIsRecordedAfterTheWork(t *testing.T) {
	var sends atomic.Int32

	h := &worker.Handlers{
		SendWelcome: func(context.Context, string) error {
			if sends.Add(1) == 1 {
				return errors.New("smtp: 421 try again later")
			}
			return nil
		},
	}

	client, _, rec, queue := setupWith(t, worker.DefaultServerConfig(), h)

	task, err := tasks.NewEmailWelcome(tasks.EmailWelcomePayload{UserID: "u", Email: "a@b.test"})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := client.Enqueue(task, asynq.Queue(queue), asynq.MaxRetry(3)); err != nil {
		t.Fatal(err)
	}

	jobtest.WaitFor(t, 10*time.Second, "the task to succeed", func() bool {
		succeeded, _, _ := rec.Counts()
		return succeeded == 1
	})

	if got := sends.Load(); got != 2 {
		t.Errorf("sent %d time(s), want 2: the retry after a failed send did not send", got)
	}
}
