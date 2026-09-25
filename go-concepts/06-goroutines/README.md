# Goroutines

> 📚 [go-concepts](../README.md) · **Step 6 of 18** · [⬅ 05-defer-panic-recover](../05-defer-panic-recover/) · Next: [07-channels](../07-channels/) ➡

## What is this?

`go f()` runs `f` concurrently and returns immediately. That is the whole
syntax. There is no `async def`, no `await`, no event loop to start, and no
separate set of libraries for the concurrent version of things.

```go
go doWork()        // starts now, this line does not block
doSomethingElse()  // runs while doWork is still going
```

The thing to understand first is what a goroutine costs, because the answer
changes what designs are reasonable. A goroutine starts with an **8KB** stack
that grows by copying as needed. An OS thread starts with 1MB or more, fixed.
Creating a goroutine is a few hundred nanoseconds and a small allocation;
creating a thread is a syscall. A Go program running 100,000 goroutines is
ordinary. A program running 100,000 threads is not a program, it is an outage.

So in Go you do not pool goroutines to avoid creating them. You create one per
unit of work and let the runtime deal with it.

## No GIL, and why that is the dangerous part

This is the difference that catches Python developers, and it catches them
silently.

CPython's global interpreter lock means only one thread executes bytecode at a
time. That makes threading useless for CPU work, and it also makes a lot of
sloppy concurrent code accidentally correct: `counter += 1` from ten threads
usually produces the right answer, because the lock serialises the interpreter.

Go has no GIL. Goroutines run genuinely in parallel across cores. `counter++`
from ten goroutines is a **data race**, which in Go is undefined behaviour, not
"probably fine". The result can be wrong, and the compiler is entitled to assume
races do not happen when it optimises.

The detector earns its keep on code that already looks careful. While writing
this lesson, `mainDoesNotWait` used `atomic.AddInt32` in every goroutine and
`atomic.LoadInt32` to read the result. Every access went through an atomic
operation and it still raced, because the function used a **named return**:
`return atomic.LoadInt32(&finished)` compiles to `finished = ...`, a plain
non-atomic write to the variable the goroutines were still incrementing. The
full write-up is in [LESSONS.md](../../LESSONS.md).

Two rules came out of it. Atomics protect the accesses that go through them and
nothing else, so one ordinary read or write of the same variable brings the race
back. And prefer `atomic.Int32` over `atomic.AddInt32(&x, 1)`: the method form
keeps the integer unexported, so a non-atomic access is not expressible.

The good news is that Go ships a detector. `go test -race` instruments every
memory access and reports the two goroutines involved, with stack traces. It is
the single most valuable tool in the language, it belongs in CI from day one, and
[16-race-detector](../16-race-detector/) is about nothing else.

```
WARNING: DATA RACE
Read at 0x00c000018098 by goroutine 8:
  main.increment()
      counter.go:14 +0x2c
Previous write at 0x00c000018098 by goroutine 7:
  main.increment()
      counter.go:14 +0x44
```

## The scheduler, in one diagram

Go multiplexes many goroutines onto few OS threads. The model has three parts,
and knowing the names makes the runtime docs and stack traces readable:

```
   G   goroutine     the unit of work. Thousands of these.
   M   machine       an OS thread. Roughly GOMAXPROCS of these doing Go work.
   P   processor     a scheduling context holding a run queue. Exactly GOMAXPROCS.

        P0                    P1
   ┌──────────┐          ┌──────────┐
   │ runq:    │          │ runq:    │        global run queue
   │ G1 G2 G3 │          │ G4 G5    │        ┌──────────┐
   └────┬─────┘          └────┬─────┘        │ G9 G10   │
        │                     │              └──────────┘
       M0                    M1
        │                     │
     CPU core              CPU core
```

A `P` runs goroutines from its own queue. When that empties, it steals from
another `P` or takes from the global queue. When a goroutine blocks on a
syscall, its `M` blocks with it and the `P` detaches and finds another `M`, so
one blocking file read does not stop the other goroutines in that queue.

`GOMAXPROCS` is the number of `P`s, and therefore the maximum number of
goroutines running Go code simultaneously. It defaults to the number of CPUs
visible to the process. Since Go 1.25 it is also container-aware, reading the
cgroup CPU limit, which fixed a long-standing problem where a pod limited to 2
CPUs on a 64-core node would still create 64 `P`s.

## Preemption

Before Go 1.14, the scheduler could only switch goroutines at function calls,
so a tight loop with no calls in it would occupy its `P` forever and could hang
the whole program at a garbage collection. Go 1.14 added asynchronous
preemption using signals, so a goroutine is interrupted after about 10ms
regardless of what it is doing.

This matters mostly as history, and as the explanation for why old Go advice
tells you to sprinkle `runtime.Gosched()` into loops. You do not need to.

## Goroutine leaks are the real bug

A goroutine that blocks forever is never collected. It holds its stack, and
everything its stack references, for the lifetime of the process. Nothing warns
you. The garbage collector cannot help, because a blocked goroutine is
reachable by definition.

Three leaks cover most real cases:

**Sending on a channel nobody reads.** The send blocks forever.

**Receiving from a channel nobody closes.** The receive blocks forever.

**Ignoring cancellation.** The caller gave up thirty seconds ago and the
goroutine is still working, because nothing told it to stop.

The fix is a rule: **every goroutine needs a known way to exit**. Usually that
is a `context.Context` ([10-context](../10-context/)) or a `done` channel
([08-select-and-timeouts](../08-select-and-timeouts/)). If you cannot say in one
sentence how a goroutine terminates, it leaks.

`runtime.NumGoroutine()` counts them, which makes leaks testable: record the
count, do the work, wait, and check it came back down. `goroutines_test.go` does
exactly that, and [uber-go/goleak](https://github.com/uber-go/goleak) does it
properly for a whole test suite.

## The loop variable, and why old code looks wrong

Before Go 1.22, `for i := range xs` reused one variable across iterations, so
every goroutine closing over `i` saw whatever value it held when they ran,
usually the last one. The workaround, `i := i` at the top of the body, is all
over older codebases.

Go 1.22 changed loop variables to be per-iteration. The bug is gone, the
workaround is now a no-op, and `copyloopvar` in `.golangci.yml` flags it. A
module only gets the new behaviour if its `go.mod` declares `go 1.22` or later,
which is a rare case of a language change gated on the module's declared
version.

## What the files cover

| File | What it teaches |
|---|---|
| `starting.go` | `go` statements, cost, ordering guarantees, the unstarted-goroutine trap |
| `scheduling.go` | `GOMAXPROCS`, parallelism vs concurrency, work stealing, preemption |
| `leaks.go` | The three leak shapes, detecting them with `NumGoroutine`, and the fixes |
| `loopvar.go` | Per-iteration loop variables, and what the pre-1.22 bug looked like |
| `main.go` | Runs every demo in order |
| `*_test.go` | Tests including a parallel-speedup benchmark and a leak check |

## How to run

```bash
go run ./06-goroutines
go test ./06-goroutines
go test -race ./06-goroutines       # the important one
go test -bench . -benchmem -run '^$' ./06-goroutines
```
