# Channels

> 📚 [go-concepts](../README.md) · **Step 7 of 18** · [⬅ 06-goroutines](../06-goroutines/) · Next: [08-select-and-timeouts](../08-select-and-timeouts/) ➡

## What is this?

A channel is a typed queue that also synchronises. `ch <- v` sends, `<-ch`
receives, and the blocking behaviour is what makes it more than a queue.

> Do not communicate by sharing memory; instead, share memory by communicating.

That proverb is the design goal. Instead of several goroutines locking a shared
structure, one goroutine owns the data and others send it messages. Ownership
moves with the value, so there is nothing to lock.

It is advice, not a law. A counter incremented from twenty goroutines is a
`sync/atomic` problem, and forcing it through a channel makes it slower and
harder to read. Channels are for handing work and results between goroutines.
Mutexes and atomics are for protecting state that genuinely is shared. Lesson
[09-sync-primitives](../09-sync-primitives/) covers the other half.

## Unbuffered channels are a handshake

`make(chan int)` has no capacity. A send blocks until a receiver is ready, and a
receive blocks until a sender is. Both goroutines meet at the same instant:

```
  sender                          receiver
    │                                │
    │ ch <- 1    (blocks)            │
    │ ▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓               │
    │ ▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓          v := <-ch
    │ ────────── value ───────────▶  │
    │ continues                      │ continues
```

So an unbuffered send tells you something real: when it returns, a receiver
has the value. That is a synchronisation guarantee, not just a delivery one.

## Buffered channels decouple

`make(chan int, 3)` holds three values. A send blocks only when the buffer is
full; a receive blocks only when it is empty.

```
  make(chan int, 3)

  send 1  [1    ]  returns immediately
  send 2  [1 2  ]  returns immediately
  send 3  [1 2 3]  returns immediately
  send 4  [1 2 3]  BLOCKS until someone receives
```

The buffer size is a real decision, not a performance knob:

- **0** means "I need to know this was received."
- **1** is for a one-shot result, or for a semaphore of one.
- **N** absorbs bursts up to N. Beyond that the producer blocks, which is
  backpressure and is usually what you want.
- **Very large** hides a problem. If a producer permanently outruns its
  consumer, a big buffer converts a fast failure into a slow memory leak.

There is a speed difference, and it is smaller than people expect. On an M2 Max,
moving one int between two goroutines costs **136ns unbuffered** and **43ns
through a buffer of 128**, neither of them allocating. The 3x gap is real, and it
is a rounding error next to anything involving a syscall or a lock under
contention. Choose the buffer size for the semantics; the speed follows.

Reproduce with `go test -bench 'RoundTrip' -benchmem -run '^$' ./07-channels`.

## The four operations, and what nil and closed do

This table is the whole semantics, and it is worth memorising because the
`select` behaviour in the next lesson is built on it.

| Operation | nil channel | open, empty/full | closed |
|---|---|---|---|
| send `ch <- v` | blocks forever | blocks until space | **panics** |
| receive `<-ch` | blocks forever | blocks until a value | returns zero value immediately |
| `close(ch)` | **panics** | fine | **panics** |
| `len` / `cap` | 0 | buffered count / capacity | remaining / capacity |

Three rules follow, and they cover almost every channel bug:

**Only the sender closes.** Closing is a message meaning "no more values are
coming", which only the sender knows. A receiver that closes risks a send on a
closed channel, and that panics.

**Closing is optional.** A channel is garbage collected like anything else.
Close only when receivers need to know the stream ended, which in practice means
when someone is ranging over it.

**Never close twice.** With multiple senders, no single one can know it was
last. Use a separate done channel, a `sync.Once`, or a `WaitGroup` plus one
closer goroutine.

## Receiving from a closed channel

A receive on a closed channel returns the zero value immediately, forever. So
`<-ch` returning `0` is ambiguous: it could be a real zero or a closed channel.
The comma-ok form disambiguates, exactly like a map lookup:

```go
v, ok := <-ch    // ok is false only when the channel is closed AND drained
```

`for v := range ch` handles this for you and stops when the channel closes.
A range over a channel that is never closed blocks forever, which is leak
shape 2 from the previous lesson.

## Directional types

A channel parameter can be restricted to one direction:

```go
func produce(out chan<- int)   // send only
func consume(in <-chan int)    // receive only
```

The conversion is one-way and automatic: a `chan int` converts to either, and
neither converts back. This is worth doing on every function signature that
takes a channel, because it puts the data flow in the type and makes
"a consumer accidentally closed the channel" a compile error.

## Deadlock

If every goroutine is blocked, the runtime detects it and aborts:

```
fatal error: all goroutines are asleep - deadlock!
```

This is a `fatal error`, not a panic, so `recover` cannot catch it. It only
fires when *every* goroutine is stuck. A program with one live goroutine
spinning and twenty deadlocked ones will hang instead, with no message, which is
the harder case and the reason for `go test -timeout` and
`GOTRACEBACK=all`.

## What the files cover

| File | What it teaches |
|---|---|
| `basics.go` | Unbuffered handshake, buffered capacity, blocking behaviour, `len`/`cap` |
| `closing.go` | Who closes, comma-ok, `range`, double close, the panic cases |
| `directions.go` | `chan<-` and `<-chan`, the generator pattern, compile-time safety |
| `patterns.go` | Ping-pong, semaphore, done channel, request/response, ownership transfer |
| `deadlocks.go` | The four ways to deadlock, and what the runtime does about each |
| `main.go` | Runs every demo in order |
| `*_test.go` | Tests including timeouts on every blocking operation |

## How to run

```bash
go run ./07-channels
go test -race ./07-channels
go test -bench . -benchmem -run '^$' ./07-channels
```
