# defer, panic, recover

> 📚 [go-concepts](../README.md) · **Step 5 of 18** · [⬅ 04-errors](../04-errors/) · Next: [06-goroutines](../06-goroutines/) ➡

## What is this?

`defer` schedules a call to run when the surrounding **function** returns, no
matter how it returns. It is Go's answer to `finally`, and to Python's `with`.

```go
f, err := os.Open(path)
if err != nil {
    return err
}
defer f.Close()   // runs on every return path below, including a panic
```

The value is that the cleanup sits next to the acquisition. In a function with
six early returns, you write the close once instead of six times, and adding a
seventh return cannot forget it.

## Four rules, and each one bites

**1. Deferred calls run in LIFO order.** Last deferred, first run. This is what
you want: resources are released in the reverse of the order they were taken,
so a lock acquired inside a transaction is released before the transaction
commits.

**2. Arguments are evaluated at `defer` time, not at run time.** This is the
one that surprises people:

```go
i := 0
defer fmt.Println(i)   // prints 0, not 1
i++
```

`i` was copied when the `defer` statement executed. To defer something that sees
the final value, defer a closure: `defer func() { fmt.Println(i) }()`.

**3. `defer` is function-scoped, not block-scoped.** A `defer` inside a loop
does not run at the end of each iteration; it piles up until the function
returns. Open a thousand files in a loop with `defer f.Close()` and you hold a
thousand descriptors. The fix is to move the body into its own function, which
is the honest signal that the loop body was doing too much.

**4. A deferred closure can change a named return value.** Which is how you turn
a panic into an error, and how a `Close` failure gets reported:

```go
func read() (err error) {
    defer func() {
        if r := recover(); r != nil {
            err = fmt.Errorf("recovered: %v", r)
        }
    }()
    ...
}
```

Without the named return, the `err =` assignment has nothing to assign to, and
the recovered panic vanishes silently. This is the single most common way
`recover` is written wrong.

## panic

`panic` unwinds the stack, running every deferred call on the way, and then
kills the program with a stack trace. It is not an exception. There is no
`catch`, no exception hierarchy, and nothing resembling `except ValueError`.

Panic when the program has reached a state its own code says is impossible:

- An index out of range, a nil dereference, a nil map write. The runtime does
  these for you.
- A `default:` in a switch over an enum you defined and fully covered.
- `regexp.MustCompile` on a literal pattern, at package init.
- A failed invariant in a data structure you maintain.

Do **not** panic for anything a caller could have caused. Bad user input,
a missing file, a refused connection and a malformed request are all errors.
A library that panics on bad input forces every caller to wrap it in `recover`,
which is worse than the error return it was avoiding.

## recover

`recover` stops a panic, and only works when called **directly inside a deferred
function** of the frame that is unwinding. Not in a function the deferred
function calls. Not in a nested closure. The compiler will not stop you writing
either of those, and both silently fail to recover.

There are three legitimate uses, and they are all at boundaries:

**A server keeping other requests alive.** One handler panicking should return
500 for that request, not take the process down. `net/http` does this for you
already.

**A worker pool surviving one bad job.** Same shape.

**Converting a panic to an error at a package boundary.** A recursive descent
parser can panic its way out of deep recursion and recover at the top, which is
genuinely cleaner than threading an error through every level. `encoding/json`
does exactly this.

Everywhere else, recovering is how a corrupted program keeps running.

## What `recover` cannot do

It cannot catch a panic in a different goroutine. Each goroutine has its own
stack, and an unrecovered panic anywhere kills the whole process:

```go
go func() {
    panic("boom")   // no recover in main will save you
}()
```

Every goroutine that could panic needs its own deferred recover. This is the
most common way a Go service dies in production, and it comes up again in
[06-goroutines](../06-goroutines/).

### Recursion depth is not the limit you expect

A recursive parser is the classic place to worry about blowing the stack.
Python raises `RecursionError` at a default depth of 1000, and C segfaults on a
fixed 8MB stack. Go starts every goroutine with an **8KB** stack and grows it by
copying, up to 1GB by default, so `Eval` in `boundaries.go` parses a million
nested parentheses in about a fifth of a second. The test covers 100,000 levels.

That does not mean depth is free. Exceeding the 1GB maximum is
`fatal error: stack overflow`, which `recover` cannot catch, and a slow
million-level parse is a denial of service well before it crashes. Untrusted
input still wants an explicit depth cap. The point is that the cap is yours to
choose for your own reasons, not something the runtime forces on you at 1000.

### What `recover` cannot do, part two

It also cannot catch a fatal runtime error. A concurrent map write, a stack
overflow, or a deadlock detected by the runtime are `fatal error`, not `panic`,
and nothing recovers them. That is deliberate: they mean memory is already in an
unknown state.

## What the files cover

| File | What it teaches |
|---|---|
| `ordering.go` | LIFO order, argument evaluation time, the loop trap and its fix |
| `namedreturns.go` | Modifying a named return from a defer, the close-error pattern |
| `panics.go` | What panics, what the runtime panics on, `fatal error` vs panic |
| `recovery.go` | Correct and incorrect `recover` placement, the goroutine rule |
| `boundaries.go` | A panicking HTTP handler contained by middleware; a parser that panics internally |
| `main.go` | Runs every demo in order |
| `*_test.go` | Table-driven tests, including tests that assert a panic happens |

## How to run

```bash
go run ./05-defer-panic-recover
go test ./05-defer-panic-recover
go test -v -run TestRecoverOnlyWorks ./05-defer-panic-recover
```
