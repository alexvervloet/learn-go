# jobs-concepts

Background jobs with [asynq](https://github.com/hibiken/asynq): retries, timeouts, scheduling, idempotency and
the inspection API a dashboard is built on. 1,844 lines, and three of the findings below came from a test
failing rather than from the documentation.

asynq and Celery, the library many people arrive from, make one decision very differently, and it is the first
thing below.

## Running it

```sh
docker compose up -d
REDIS_ADDR=localhost:6381 go test ./...
```

Or against a Redis you already run, which needs no environment at all:

```sh
go test ./...
```

Without a Redis every test **skips**. The suite is about 35 seconds, almost all of it waiting for asynq's own
timers: retry backoffs, a three-second scheduled task, and a handler deliberately ignoring its deadline.

## asynq against Celery

Celery is a framework: a decorator makes a function a task, a separate `celery worker` process runs it, and the
result comes back through a "result backend". asynq is a library: a task is a name and a JSON payload, and a
worker is an `asynq.Server` started inside your own binary.

asynq can store a result: a handler writes it with `t.ResultWriter()`, it is kept for as long as the task's
`asynq.Retention` option says, and `Inspector.GetTaskInfo` returns it in `TaskInfo.Result`. (An earlier version
of this section said asynq has no result backend at all; it has had one since v0.22.) What it does not have is
the blocking wait, and that is the important difference. Celery lets you write `result = add.delay(2, 2); result.get()`, which
looks like a function call and is a distributed system pretending to be one: it blocks a web worker on a
background worker, and if the background worker is down it blocks forever. asynq does not offer it. Having to
write down where the result goes is the better default.

The other consequence is deployment. An asynq worker runs in a Go binary you wrote, so a service can run its API
and its worker in one process behind a flag, or in two deployments from the same image. That is a deployment
decision rather than a framework one.

## What is here

| path | lines | what it is |
| --- | --- | --- |
| [tasks/](tasks/) | 271 | task types, payloads, and the accessors a handler needs |
| [worker/](worker/) | 1,293 | handlers, middleware, the server, and the tests |
| [jobtest/](jobtest/) | 280 | a real worker against a real Redis, isolated per test |

Coverage: 87.4%.

## Three things asynq does that nothing warns you about

Each one was found by a test failing, and each is the same root cause: **asynq stores every time value as a
whole number of seconds.**

### `asynq.Timeout(300 * time.Millisecond)` becomes a 30-minute timeout

The timeout is serialised as `int64(timeout.Seconds())` in `client.go`. Anything under a second rounds to zero,
zero means "unset", and unset means the 30-minute default.

Measured:

| option | budget the handler received | a 600ms handler |
| --- | --- | --- |
| `Timeout(300ms)` | **30m0s** | completed |
| `Timeout(1s)` | 1s | cancelled |

The option is accepted, nothing is logged, and the symptom is a handler that ignores a timeout it was given.
`asynq.Deadline` takes an absolute `time.Time` and is stored as a Unix timestamp, so it has the same
granularity. **Sub-second task timeouts are not available**; enforce them inside the handler.

### A scheduled task can run up to a second EARLY

`processAt.Unix()` truncates to the second. A task asked for 800ms from now at wall-clock time X.9 gets the
score `floor(X + 1.7) = X + 1`, and the forwarder promotes it when `now.Unix()` reaches X + 1, which is 100ms
after the enqueue.

The first version of that test asserted "not before 700ms" and failed one run in six, with the task running
after 665ms. That was not a flaky test.

### And it can run seconds LATE, because the forwarder is a poll

A task is promoted from the scheduled set by a forwarder running on `DelayedTaskCheckInterval`, which defaults
to **five seconds**. Nothing wakes up when a task becomes due.

So a task scheduled for 100ms from now may run at five seconds. "Schedule" means "somewhere near", never "at",
and a latency-sensitive queue needs that interval lowered at the cost of a Redis round trip per interval per
worker.

## Retries

Measured on a task that fails twice then succeeds, with `MaxRetry(5)`:

```
attempt 1: retry=0 err=transient failure: attempt 1 of 3
attempt 2: retry=1 err=transient failure: attempt 2 of 3
attempt 3: retry=2 err=<nil>
```

The retry count is visible to the HANDLER, through `asynq.GetRetryCount(ctx)`, which is what makes
`IsLastAttempt` possible: alert, write a dead-letter row, tell the user their export failed. Without it a task
fails silently into the archive.

**`asynq.SkipRetry` stops a task that can never succeed.** A malformed payload archived after **one** attempt
rather than ten, each of which would cost a worker slot and a log line and reach the same archive. Every
`json.Unmarshal` failure in this module returns it.

**The archive keeps the payload**, so a task can be replayed with `inspector.RunTask` after the bug is fixed.
Without that, a failed job is a log line and the work is lost.

## Timeouts, and the handler that cannot be stopped

| handler | outcome |
| --- | --- |
| selects on `ctx.Done()` | returns at the deadline with `context.DeadlineExceeded` |
| does not | asynq reports it timed out, and the goroutine keeps running |

The second is the one to understand. asynq marks the task failed at its deadline and **retries it**, while the
original goroutine carries on and still does the work. So the work happens twice. And asynq has already given
the slot back: its bookkeeping goroutine releases the concurrency token at the deadline, while the handler's
goroutine keeps running, so the process is quietly running MORE tasks than its concurrency limit, not fewer. An
earlier version of this paragraph said the limit drops by one; asynq's `processor.exec` does the opposite.

A long handler has to check its context between chunks. `ReportBuild` in this module does, and reports how far
it got.

## Idempotency: two mechanisms that are not the same

| | keys on | survives a payload change |
| --- | --- | --- |
| `asynq.Unique(ttl)` | the type, the queue and the PAYLOAD BYTES | no |
| `asynq.TaskID(id)` | an id the CALLER chose | yes |

`Unique` returns `ErrDuplicateTask` for an identical payload and accepts a different one, so **a payload
carrying a timestamp or a request id is never unique**. Uniqueness is about the WORK, which means the payload
has to name the work and nothing else.

`TaskID` returns `ErrTaskIDConflict` even for a different payload with the same id, which is usually what "do
this once for this user" means. It is what an idempotency key is.

And at the handler end: asynq's task id is stable across retries, so it is the right key for a
"have I already done this" check. The check reads before the work and records **after** it succeeds. The
first version recorded first, so a send that failed left the id behind, the retry found it and returned
nil, and the email was never sent while asynq reported the task completed.
`TestIdempotencyIsRecordedAfterTheWork` fails the first send and requires a second one.

Same caveat as everywhere else in this repo: an in-memory map is wrong for more than one worker, and a crash
between the work and the record still repeats the work. The real fix is a unique constraint in the same
transaction as the work, or an idempotency key the downstream service honours.

## Queues are weighted, not prioritised

With `critical: 6` and `low: 1`, of the first 30 tasks handled from two full queues, **29 were critical and 1
was low**.

The low queue is not starved, which is the property. Strict priority starves it forever the moment the critical
queue is never empty, and a critical queue is never empty.

## Inspection

`asynq.Inspector` is the API behind asynqmon, and it is what a test asserts on:

```
queue: pending=0 active=0 scheduled=0 retry=1 archived=0 completed=0 processed=1 failed=1
task 9c1e...: state=retry retried=1/5 last_error="transient failure: attempt 1 of 6" next_at=...
```

It also acts: cancel, archive, delete, run now. That is what an operator does at 3am, and it is why a queue
without a dashboard is a queue nobody knows the depth of.

**The two numbers to alert on** are Pending rising, which means the workers cannot keep up, and Retry rising,
which means something is broken rather than busy.

## Payload rules

**A payload carries an IDENTIFIER, not the data.** It goes in Redis, is copied on every retry, and is held in
memory by the worker, so a 5 MB image in a payload is 5 MB of Redis per queued job and a queue of a thousand is
5 GB. This is the most common job-queue mistake and it is invisible until the queue backs up.

**A payload is a wire format with no schema.** A task enqueued now may be handled by a worker running the code
from before the last deploy, because a rolling deploy has both versions running and the queue is shared. Adding
an optional field is safe; removing, renaming or retyping one is not, and `encoding/json` silently drops an
unknown field rather than reporting it. When a real break is needed, add a new task TYPE and drain the old one.

**The task type name is a routing key**, so renaming one strands every task already queued: they are dequeued,
no handler matches, and asynq retries them until they are archived.

## Things worth stealing from here

- `tasks.IsLastAttempt`: the hook a handler needs to behave differently on the final try, which is where an
  alert or a dead-letter row belongs.
- `worker.Handlers.logging`: reads the deadline BEFORE calling the handler, because reading it afterwards
  reports how much is left, which for a timed-out task is zero and tells you nothing about what it was given.
- `worker.NewServer`'s `ErrorHandler`: logs at Warn for a retryable failure and Error for the last attempt,
  because every earlier failure is noise if the task eventually succeeds.
- `jobtest.WaitFor`: polls rather than sleeps, because the thing being waited for is a state asynq will reach
  on its own schedule and there is nothing to signal on.
- `jobtest.QueueName`: a queue per TEST, not per binary. asynq keys its state on the queue name, so two tests
  sharing one see each other's tasks.
