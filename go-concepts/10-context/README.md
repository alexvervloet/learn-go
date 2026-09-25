# context

> 📚 [go-concepts](../README.md) · **Step 10 of 18** · [⬅ 09-sync-primitives](../09-sync-primitives/) · Next: [11-generics](../11-generics/) ➡

## What is this?

`context.Context` carries a cancellation signal, a deadline, and a small bag of
request-scoped values down a call tree. It is how a Go program says "stop, the
caller has gone" to work that may be several goroutines and three network hops
away.

The interface is four methods and you will implement none of them:

```go
type Context interface {
    Done() <-chan struct{}        // closed when this context is cancelled
    Err() error                   // why: Canceled or DeadlineExceeded
    Deadline() (time.Time, bool)  // when it will expire, if it will
    Value(key any) any            // request-scoped values
}
```

`Done()` returning a channel is the whole design. Because cancellation is a
channel, it drops straight into a `select` alongside everything else a goroutine
might be waiting for, and needs no special support from anything.

## Why it exists

A timeout without a context stops at the function that declared it:

```go
select {
case v := <-work:
    return v
case <-time.After(5 * time.Second):
    return errTimeout      // returns. The work keeps running.
}
```

Your function returned. The database query is still running, the HTTP request is
still open, the three goroutines it spawned are still going. You have a timeout
on the *caller* and none on the *work*.

A context deadline propagates *within your process*. Pass it to the query, the
HTTP request and the goroutines, and when it expires they all stop. That is the
difference between shedding load and pretending to.

### What crosses a network hop, and what does not

Worth being precise, because it is easy to assume too much:

| | Crosses an HTTP hop? |
|---|---|
| cancellation | **Yes.** The client disconnecting closes the connection and the server's `r.Context()` is cancelled. Free, automatic. |
| the deadline | **No.** HTTP defines no header for it, so the downstream handler's context has no deadline at all. |
| values | **No.** They are in-process only. |

gRPC does propagate the deadline, using a `grpc-timeout` header, which is a
concrete reason it is nicer for service-to-service calls. Over plain HTTP you
send it yourself, and `httpuse.go` shows both halves: a client that writes the
remaining budget into a header, and middleware that reads it back and clamps it
to the server's own maximum, because a header is input and input is not trusted.

The practical difference: with cancellation alone, a downstream service works
until the caller gives up and hangs up. With the deadline propagated, it knows
its budget on arrival and can decline work it cannot finish in time.

## The tree

Contexts form a tree. Cancelling a node cancels its whole subtree, never its
parent.

```
   context.Background()
          │
    WithTimeout(30s)              ← the HTTP request
          │
    ┌─────┴──────┐
    │            │
 WithCancel   WithTimeout(5s)     ← a database query
    │            │
 goroutine    goroutine
```

Cancelling the 5s node stops the query and leaves the rest alone. The 30s
deadline firing stops everything below it. A child can be *stricter* than its
parent but never more lenient: `WithTimeout(parent, time.Hour)` on a parent that
expires in 5s still expires in 5s.

## The rules

**Pass it as the first parameter, named `ctx`.** `func Fetch(ctx context.Context, url string)`. Not a struct field, not a package variable.

**Never store a Context in a struct.** A context is scoped to one call. Putting
it in a struct outlives that scope and someone will use a cancelled one. The
exception the standard library allows is `http.Request`, which is stuck with it
for compatibility and provides `WithContext`/`Context` methods to work around it.

**Never pass a nil Context.** Use `context.TODO()` if you genuinely do not have
one yet. `TODO` and `Background` are identical at runtime; the difference is
that `TODO` tells a reader, and `go vet`'s successors, that this is unfinished.

**Always `defer cancel()`.** Every `WithCancel`, `WithTimeout` and `WithDeadline`
returns a cancel function, and not calling it leaks the context and its timer
until the parent is cancelled. `go vet`'s `lostcancel` catches the obvious cases
and not the subtle ones. Calling cancel twice is explicitly safe.

**`ctx.Value` is for request-scoped data only.** A request ID, a trace span, an
authenticated user. Not configuration, not a database handle, not optional
arguments. If a function needs a thing to work, that thing is a parameter.

## Values, and their one safe idiom

`Value(key any) any` is untyped on both ends, so the key needs care:

```go
// Unexported type, so no other package can collide with it or read it.
type ctxKey struct{}

func WithRequestID(ctx context.Context, id string) context.Context {
    return context.WithValue(ctx, ctxKey{}, id)
}

func RequestID(ctx context.Context) (string, bool) {
    id, ok := ctx.Value(ctxKey{}).(string)
    return id, ok
}
```

Three things make it safe: the key type is unexported so collisions are
impossible, the setter and getter are the only access, and the getter returns a
typed value plus an ok. Using a plain `string` key is the common mistake, and it
means any package in the process can overwrite your value by accident.

## `Cause` tells you why

`ctx.Err()` returns `context.Canceled` or `context.DeadlineExceeded`, which says
what happened and not why. Since Go 1.20:

```go
ctx, cancel := context.WithCancelCause(parent)
cancel(errors.New("upstream returned 503"))

ctx.Err()            // context.Canceled
context.Cause(ctx)   // upstream returned 503
```

Use `WithCancelCause` for anything a human will debug. The cost is nothing and
the difference between "context canceled" and an actual reason in a log line is
an hour.

## Newer helpers worth knowing

| Function | Since | What it solves |
|---|---|---|
| `WithCancelCause` | 1.20 | Cancellation with a reason |
| `WithoutCancel` | 1.21 | Detach work that must outlive the request (audit logs, cleanup) |
| `AfterFunc` | 1.21 | Run a function when a context is done, without a goroutine parked on `Done()` |
| `WithDeadlineCause` | 1.21 | A deadline with a reason |

## What the files cover

| File | What it teaches |
|---|---|
| `basics.go` | `Background`/`TODO`, `Done`, `Err`, the cancel contract, `defer cancel` |
| `tree.go` | Propagation, stricter-not-looser deadlines, subtree cancellation |
| `cause.go` | `WithCancelCause`, `Cause`, `WithoutCancel`, `AfterFunc` |
| `values.go` | The typed-key idiom, collisions, what does not belong in a context |
| `httpuse.go` | Server and client integration, per-request deadlines, graceful shutdown |
| `mistakes.go` | The six context bugs, each with its fix |
| `main.go` | Runs every demo in order |
| `*_test.go` | Tests including leak checks and deadline-propagation checks |

## How to run

```bash
go run ./10-context
go test -race ./10-context
```
