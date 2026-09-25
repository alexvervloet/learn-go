# select and timeouts

> 📚 [go-concepts](../README.md) · **Step 8 of 18** · [⬅ 07-channels](../07-channels/) · Next: [09-sync-primitives](../09-sync-primitives/) ➡

## What is this?

`select` waits on several channel operations at once and proceeds with whichever
is ready first. It is what turns channels from a queue into a coordination
primitive.

```go
select {
case v := <-results:
    handle(v)
case err := <-errors:
    return err
case <-ctx.Done():
    return ctx.Err()
case <-time.After(5 * time.Second):
    return errTimeout
}
```

Four things a goroutine might be waiting for, handled in one statement, with no
polling and no callbacks. Without `select` you cannot write a timeout, cannot
cancel a blocked receive, and cannot merge two streams.

## The rules

**If several cases are ready, one is chosen uniformly at random.** Not the first
in source order. This is deliberate, and it prevents starvation: a busy channel
listed first cannot lock out a quieter one below it.

**If none is ready and there is a `default`, `default` runs immediately.** This
turns `select` into a non-blocking try.

**If none is ready and there is no `default`, the select blocks** until one
becomes ready.

**An empty `select{}` blocks forever.** Occasionally useful as "park this
goroutine and never return"; usually a mistake.

**A `nil` channel is never ready.** Which is the most useful trick in this
lesson, and has its own section below.

## Non-blocking operations with `default`

```go
select {
case ch <- v:
    // sent
default:
    // would have blocked; drop it, or count it, or try again later
}
```

This is how you drop under load rather than building an unbounded queue, and
how you implement a "latest value wins" channel. It is also the only correct way
to ask "can I send without blocking", because `len(ch) < cap(ch)` is stale the
moment you read it.

## Timeouts, and the `time.After` leak

The obvious timeout is `time.After`:

```go
select {
case v := <-ch:
    return v
case <-time.After(time.Second):
    return errTimeout
}
```

That is correct here. But `time.After` allocates a timer that **cannot be
stopped**, and it is not collected until it fires. In a loop that runs a
thousand times a second with a 30-second timeout, you accumulate 30,000 live
timers holding 30,000 channels.

Inside a loop, create one timer and reset it:

```go
timer := time.NewTimer(timeout)
defer timer.Stop()

for {
    timer.Reset(timeout)
    select {
    case v := <-ch:
        ...
    case <-timer.C:
        ...
    }
}
```

Go 1.23 made this much less dangerous: unstopped timers became eligible for
collection as soon as they are unreachable, and `Reset` on an already-fired
timer no longer needs the old drain-the-channel dance. On Go 1.23 and later the
`time.After` leak is mostly historical. The `NewTimer` form is still better
practice and still what you will see in review.

In real code the answer is usually neither: use `context.WithTimeout` and select
on `ctx.Done()`, so the deadline propagates to everything downstream instead of
stopping at this one function. That is [lesson 10](../10-context/).

## Disabling a case with a nil channel

A `nil` channel blocks forever, so a `select` case on one is never chosen. Set a
channel variable to `nil` and that arm switches off:

```go
for a != nil || b != nil {
    select {
    case v, ok := <-a:
        if !ok {
            a = nil     // stop selecting on a; the loop condition ends it
            continue
        }
        out <- v
    case v, ok := <-b:
        if !ok {
            b = nil
            continue
        }
        out <- v
    }
}
```

Without this, a closed channel is permanently ready and returns instantly
forever, so the select spins at 100% CPU. That bug is common enough to have a
name: the **busy closed-channel loop**. It is the single most valuable thing in
this lesson.

It is worth seeing the size of it. With one channel closed immediately and
another delivering 5 values 10ms apart, the broken merge runs **568,823 select
iterations**; the fixed one runs **7**. Both finish in the same 60ms, because
both are waiting on the same slow producer. The difference is that one of them
saturates a CPU core to do nothing while it waits.

That is also why the bug survives code review and testing. It does not fail, it
does not slow anything down, and it does not appear until someone looks at a CPU
graph. `go run ./08-select-and-timeouts` prints the comparison.

## The patterns built on select

| Pattern | What it does |
|---|---|
| **fan-out** | one input, N workers, to parallelise |
| **fan-in** | N inputs, one output, to merge |
| **pipeline** | stages connected by channels, each a generator |
| **worker pool** | bounded workers pulling from a shared queue |
| **or-done** | wrap a channel so it stops on cancellation |
| **tee** | duplicate one stream into two |
| **bridge** | flatten a channel of channels |
| **first result wins** | race several sources, take the fastest |

The unifying rule is the one from lesson 06: **every goroutine needs a known way
to exit**. In a pipeline that means every stage selects on a done channel or a
context, and every stage closes its output when its input closes. Get that right
and pipelines compose; get it wrong and cancelling the consumer leaks every
stage upstream.

## What the files cover

| File | What it teaches |
|---|---|
| `basics.go` | Random choice, `default`, blocking vs non-blocking, empty select |
| `timeouts.go` | `time.After`, the timer leak, `NewTimer`/`Reset`, deadlines |
| `nilcases.go` | Disabling cases, the busy closed-channel loop and its fix |
| `pipelines.go` | Generator, stages, fan-out, fan-in, or-done, tee, bridge |
| `workerpool.go` | A bounded pool with results, errors, and cancellation |
| `main.go` | Runs every demo in order |
| `*_test.go` | Tests including a CPU-burn check for the busy-loop bug |

## How to run

```bash
go run ./08-select-and-timeouts
go test -race ./08-select-and-timeouts
go test -bench . -benchmem -run '^$' ./08-select-and-timeouts
```
