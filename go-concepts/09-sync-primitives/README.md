# sync primitives

> 📚 [go-concepts](../README.md) · **Step 9 of 18** · [⬅ 08-select-and-timeouts](../08-select-and-timeouts/) · Next: [10-context](../10-context/) ➡

## What is this?

Channels move data between goroutines. `sync` and `sync/atomic` protect data
that several goroutines genuinely share. Both are idiomatic, and the Go team
says so explicitly in the memory model documentation.

The proverb "share memory by communicating" is often quoted as if mutexes were
a failure of nerve. They are not. The actual guidance, from the Go wiki:

> Use whichever is most expressive and/or most simple.

A useful split:

**Reach for a channel** when you are passing ownership of data, distributing
work, or signalling that something happened.

**Reach for a mutex** when you are protecting a cache, a counter, a connection
pool, or any struct whose fields several goroutines read and write.

Matching the tool to the shape is most of the skill. Measured here, incrementing
one shared counter from 12 cores:

| Approach | ns/op | Relative |
|---|---|---|
| `atomic.Int64` | 57.3 | 1.0x |
| `sync.Mutex` | 131.5 | 2.3x |
| channel to an owning goroutine | 507.4 | 8.9x |

All three are correct. The channel version is also three times the code and
needs its own shutdown path. For a counter, the atomic is simply the right
answer, and routing it through a goroutine to honour a proverb makes the program
worse.

The reverse holds too: a work queue built from a mutex and a slice is a worse
channel. Reproduce with `go test -bench BenchmarkCounter -benchmem -run '^$'
./09-sync-primitives`.

## The primitives

| Type | Use it for |
|---|---|
| `sync.Mutex` | Mutual exclusion. One holder at a time. |
| `sync.RWMutex` | Many readers **or** one writer. Only pays off when reads dominate and are slow. |
| `sync.WaitGroup` | Wait for N goroutines to finish. |
| `sync.Once` | Run something exactly once, however many goroutines ask. |
| `sync.Map` | A map tuned for two specific access patterns. Rarely the right answer. |
| `sync.Pool` | Reuse allocations between goroutines. A GC optimisation, not a resource pool. |
| `sync/atomic` | Lock-free operations on a single word. |
| `golang.org/x/sync/errgroup` | A WaitGroup that collects the first error and cancels the rest. |

## Mutex rules

**The zero value is ready.** `var mu sync.Mutex` works. Never `new`, never
initialise.

**Always unlock with `defer`.** A panic or an early return between `Lock` and a
bare `Unlock` deadlocks the program permanently.

```go
mu.Lock()
defer mu.Unlock()
```

**Never copy a mutex.** Copying one copies its lock state, and the copy protects
nothing. This is why a type with a mutex field must be used through a pointer,
and why every method on it takes a pointer receiver. `go vet`'s `copylocks`
catches most of these, and it is one of the best reasons to run vet.

**Keep the critical section small,** but not so small that you have to lock
twice. Two consecutive locked sections are not atomic together, which is the
check-then-act bug:

```go
mu.Lock(); v := cache[k]; mu.Unlock()   // another goroutine can write here
mu.Lock(); cache[k] = v + 1; mu.Unlock()
```

## RWMutex is not a free upgrade

`RWMutex` lets many readers hold the lock at once, but each `RLock` is more
expensive than a plain `Lock`, and writers can be starved by a steady stream of
readers. It wins when reads massively outnumber writes **and** the critical
section is long enough for the parallelism to pay for the overhead.

For a short read of one field, a plain `Mutex` is usually faster. Measured on an
M2 Max with `b.RunParallel` across 12 cores:

| Critical section | `RWMutex` | `Mutex` | Winner |
|---|---|---|---|
| one map lookup | 134.6 ns | 122.7 ns | **Mutex**, by 10% |
| a loop over the value | 364.7 ns | 3987 ns | **RWMutex**, by 10.9x |

So the rule of thumb is real but narrow. When the critical section is a single
field read, `RWMutex` costs about 10% more than a plain `Mutex` and buys
nothing, because the bookkeeping for shared acquisition exceeds the work being
protected. Make the critical section substantial and the same code is an order
of magnitude faster.

Reproduce both with:

```bash
go test -bench 'BenchmarkCacheReadHeavy|BenchmarkLongCriticalSection' \
        -benchmem -run '^$' ./09-sync-primitives
```

Measure before switching. "Reads outnumber writes" is not sufficient on its own.

## `sync.Once`

```go
var once sync.Once
once.Do(func() { conn = connect() })
```

Exactly one call to `Do` runs the function; every other blocks until it
finishes, then returns. That second part matters: `Do` is not "skip if already
running", it is "wait until done".

If the function panics, `Once` still counts as done and will never run it again.
For fallible initialisation you want `sync.OnceValue` or `OnceValues` (Go 1.21),
which cache a result, or a hand-rolled version that can retry.

## `sync.Map`, and some advice that has gone stale

The standard advice, repeated everywhere including in earlier drafts of this
file, is that `sync.Map` is a specialist for two cases (write-once-read-many,
and disjoint key sets per goroutine) and that a plain `map` behind a `RWMutex`
is usually faster otherwise.

I benchmarked it rather than repeating it, and on Go 1.27 that is no longer
true. Measured on an M2 Max, 12 cores:

| Workload | `sync.Map` | `RWMutex` map | Winner |
|---|---|---|---|
| sequential reads, 1 goroutine | 16.5 ns | 14.9 ns | RWMutex, by 10% |
| parallel reads, 12 cores | **1.7 ns** | 116.8 ns | sync.Map, by 68x |
| parallel 50/50 read-write | 22.0 ns | 91.4 ns | sync.Map, by 4x |
| parallel writes | 40.9 ns | 237.6 ns | sync.Map, by 5.8x |

`RWMutex` only wins when there is **no contention at all**, and then by ten
percent. Add real concurrency and it loses badly, because every `RLock` and
`RUnlock` touches one shared cacheline that all twelve cores fight over, while
`sync.Map` serves reads from a per-processor structure that needs no
coordination.

The likely reason the old advice no longer holds: Go 1.24 replaced `sync.Map`'s
internals with a hash-trie implementation, and it got much faster across the
board. Guidance written against the pre-1.24 version was accurate when written.

So what should you actually do?

**Start with a `RWMutex` map anyway.** Not for speed: for types. `sync.Map`
stores `any`, so every read needs a type assertion, storing the wrong type under
a key compiles fine and panics at runtime, and there is no `Len` because
maintaining one would need the synchronisation the type exists to avoid.
Counting means `Range`, which is O(n) and is not a consistent snapshot.

**Switch when a profile shows the lock is hot,** and wrap it in a generic type
to get the compile-time safety back. `typedSyncMap` in `maps.go` is that wrapper
in twenty lines.

The broader point is the one worth taking away: performance advice has a
shelf life, and the benchmark takes ten minutes. Run
`go test -bench BenchmarkMap -benchmem -run '^$' ./09-sync-primitives` on your
own hardware before trusting either the table above or the folklore.

## `sync.Pool` is not a resource pool

It reuses allocations to reduce GC pressure. Items can be evicted at any time,
including immediately, so it cannot hold anything that must not be lost. It is
not for database connections; it is for byte buffers in a hot path.

Whether it is worth it depends entirely on how much of the work is the
allocation:

| Workload | pooled | unpooled | Gain |
|---|---|---|---|
| small buffer, ~90 bytes written | 125.5 ns, 6 allocs | 151.7 ns, 8 allocs | 17% |
| 64KB buffer, 17KB written | 319.5 ns, **0 allocs** | 7471 ns, 65537 B | **23x** |

For a small buffer the pool is barely worth the two extra lines. For a large
one it removes the allocation entirely and the function gets 23 times faster.
Profile first: a pool added to a path that was not allocation-bound is pure
complexity, and one that forgets `Reset` is a data leak between requests.

One property that catches people writing tests: **`Get` is not guaranteed to
return what you just `Put`.** A `sync.Pool` is a per-processor cache. If the
goroutine moves to another P between the `Put` and the `Get`, or a GC runs in
between, `Get` misses and calls `New`. A test of mine assumed reuse, passed
locally and on seven of eight CI jobs, and failed on the race job where the
scheduling differs. `leakIsObservable` in `pools.go` retries until reuse
actually happens, which is the honest way to test something probabilistic.

One rule the type system will not enforce for you: **`Reset` before `Put`,
always.** A buffer returned with content in it hands that content to the next
caller. In a web service that is one user's data in another user's response,
and `forgettingResetLeaksData` in `pools.go` shows it happening.

## What the files cover

| File | What it teaches |
|---|---|
| `mutexes.go` | `Mutex`, the defer rule, copying, check-then-act, `RWMutex` |
| `waitgroups.go` | `WaitGroup` rules, the Add-inside-goroutine bug, `WaitGroup.Go` |
| `once.go` | `Once`, panics, `OnceValue`/`OnceValues`, retryable initialisation |
| `atomics.go` | `atomic.Int64`, compare-and-swap, `atomic.Value`, the counter benchmark |
| `maps.go` | `sync.Map` against a `RWMutex` map, with numbers |
| `pools.go` | `sync.Pool` for buffer reuse, and what it must never hold |
| `errgroups.go` | `errgroup` for "run these concurrently, stop on the first error" |
| `main.go` | Runs every demo in order |
| `*_test.go` | Tests plus benchmarks comparing every approach |

## How to run

```bash
go run ./09-sync-primitives
go test -race ./09-sync-primitives
go test -bench . -benchmem -run '^$' ./09-sync-primitives
```
