# Errors

> 📚 [go-concepts](../README.md) · **Step 4 of 18** · [⬅ 03-interfaces](../03-interfaces/) · Next: [05-defer-panic-recover](../05-defer-panic-recover/) ➡

## What is this?

In Python, a function that fails raises, and the failure travels up the stack on
its own until something catches it. You can write a hundred lines of happy path
and handle every failure in one `try` at the top.

In Go, a function that fails returns an error, and the error goes nowhere unless
you move it. Every call that can fail is followed by three lines deciding what to
do about it.

```go
f, err := os.Open(path)
if err != nil {
    return fmt.Errorf("open config: %w", err)
}
defer f.Close()
```

This is the most-complained-about thing in the language and the design is
deliberate. An exception is invisible control flow: reading a function body
tells you nothing about where it can leave from. In Go, every exit is written
down. The cost is verbosity. What you buy is that failure handling is ordinary
code, reviewed like ordinary code, and impossible to forget silently, because
an unused variable is a compile error.

## `error` is just an interface

There is nothing special about it. One method, no runtime machinery:

```go
type error interface {
    Error() string
}
```

Which means errors are values. You can compare them, store them in a struct,
put them in a slice, pass them to a function, and return them from one. Most of
what follows is a consequence of that.

## Four ways to create one, in order of preference

**`errors.New`** for a fixed message with no variables.

**`fmt.Errorf` with `%w`** to wrap another error, adding context while keeping
the original reachable. This is the default. The `%w` is what makes the wrapped
error findable later; `%v` would flatten it to text and lose it.

**A sentinel** (`var ErrNotFound = errors.New("not found")`) for a condition
callers need to branch on. Exported sentinels are part of your API, so adding
one is a promise and changing one is a breaking change.

**A custom type** when the caller needs *data* about the failure, not just its
identity: which field was invalid, which line of the file, how long to wait
before retrying.

## Wrapping, and the message convention

Each layer adds what it knows and passes the rest along:

```
open config: read /etc/app.toml: permission denied
└─ handler    └─ loader          └─ syscall
```

Three rules make those messages read well:

- **No capital letter, no trailing punctuation.** Errors get concatenated, and
  `Failed to open file.: permission denied` is what happens otherwise.
- **No "failed to" or "error".** The word `error` is already in the variable
  name and the reader already knows it failed. `open config` beats
  `failed to open config file`.
- **Say what you were doing, not what went wrong.** The layer below already
  said what went wrong. Add the operation and its subject.

## Checking: `errors.Is` and `errors.As`

Never compare with `==` once wrapping is in play, because the value you have is
a wrapper around the value you are looking for.

**`errors.Is(err, target)`** walks the chain asking "is any error in here this
one?". Use it for sentinels.

**`errors.As(err, &target)`** walks the chain asking "is any error in here of
this type?", and if so assigns it, so you can read its fields. Use it for custom
types. The second argument must be a pointer to the type you want, which is the
part everyone gets wrong on the first try.

```go
if errors.Is(err, ErrNotFound) { ... }

var verr *ValidationError
if errors.As(err, &verr) {
    log.Printf("field %s was invalid", verr.Field)
}
```

## Joining

`errors.Join(errs...)` combines several failures into one error, and `errors.Is`
searches all of them. This is what a validator returns when four fields are
wrong and reporting only the first would waste a round trip.

## When not to return an error

- **Programmer error, not runtime error.** An index out of range or a nil map
  write is a bug in the code, not a condition to handle. Let it panic.
- **Impossible states in your own package.** If a switch covers every case of an
  enum you defined, `default: panic("unreachable")` is honest.
- **Package initialisation.** `regexp.MustCompile` panics because a bad regex
  literal is a typo that should never reach production, and handling it at every
  call site would be noise.

Everything else returns an error. In particular, a library must never panic
across its API boundary for something a caller could have caused.

## What the files cover

| File | What it teaches |
|---|---|
| `creating.go` | `errors.New`, `fmt.Errorf`, `%w` vs `%v`, the message convention |
| `sentinels.go` | Sentinel errors, `errors.Is`, why `==` breaks once wrapped |
| `custom.go` | Custom error types, `errors.As`, `Unwrap`, carrying data |
| `joining.go` | `errors.Join`, multi-error validation, `Is` over a joined error |
| `patterns.go` | Retry with a retryable error, error groups, the `defer`-close idiom |
| `main.go` | Runs every demo in order |
| `*_test.go` | Table-driven tests over every helper here |

## How to run

```bash
go run ./04-errors
go test ./04-errors
go test -v -run TestErrorsIs ./04-errors
```
