# Interfaces

> 📚 [go-concepts](../README.md) · **Step 3 of 18** · [⬅ 02-slices-and-maps](../02-slices-and-maps/) · Next: [04-errors](../04-errors/) ➡

## What is this?

Python has duck typing: if it has `.read()`, it is close enough to a file. The
check happens when you call the method, and it fails at runtime.

Java has nominal typing: a class declares `implements Readable`, and the compiler
checks it. The check happens at compile time, and the class has to know about the
interface in advance.

Go takes the useful half of each. A type satisfies an interface by having the
right methods, with no declaration anywhere. The compiler checks it. So you can
write an interface today that a type from a library published three years ago
already satisfies, and the build will confirm it.

This inverts where interfaces live. In Java, the interface ships with the
implementation, in the same package, written by the same author. In Go, the
interface belongs to the **consumer**. The package that needs a thing defines the
shape of the thing it needs, and every existing type that fits is already a
candidate.

## Define interfaces where you use them, not where you implement them

This is the single most load-bearing convention in Go design, and it follows
directly from structural typing:

```go
// In the package that NEEDS to store users. It names the two methods it
// actually calls, and nothing else.
type UserStore interface {
    GetUser(ctx context.Context, id int64) (*User, error)
    SaveUser(ctx context.Context, u *User) error
}

func NewHandler(store UserStore) *Handler { ... }
```

`*postgres.DB` satisfies this without knowing the interface exists. So does a
test fake you write in ten lines. Neither of them imports the other.

The corollary is the proverb **accept interfaces, return structs**. Take the
narrowest interface you can at the boundary, so callers have the widest choice
of what to pass, and return the concrete type, so callers keep every method
rather than the subset you guessed they would want.

## Keep them small

Go's most-used interfaces have one method:

```go
type Reader interface { Read(p []byte) (n int, err error) }
type Writer interface { Write(p []byte) (n int, err error) }
type Stringer interface { String() string }
type error interface { Error() string }
```

The payoff compounds. Because `io.Writer` is one method, a file, a network
connection, an HTTP response, a gzip compressor, a hash, a byte buffer and
`os.Stdout` all satisfy it, and every function that writes works with all of
them. A five-method interface would have excluded most of that list.

> The bigger the interface, the weaker the abstraction. — Rob Pike

## An interface value is a pair

This is the mechanical fact behind the trap in the next section. An interface
value holds two words:

```
   var w io.Writer = os.Stdout

   ┌──────────────┬──────────────┐
   │ type: *os.File│ value: 0x...│
   └──────────────┴──────────────┘
```

`w == nil` is true only when **both** words are zero. A nil interface has no
type at all. An interface holding a nil pointer has a type, and is therefore
not nil.

## The typed-nil trap

Here is the bug. It is subtle, it is common, and it has bitten every Go
codebase of any size:

```go
type MyError struct{ Code int }
func (e *MyError) Error() string { return "boom" }

func doWork() error {
    var err *MyError = nil   // a nil POINTER
    return err               // returned as an ERROR interface
}

if doWork() != nil {
    // This branch RUNS. The caller sees a non-nil error.
}
```

Returning `err` boxes the nil `*MyError` into an `error` interface. The type word
becomes `*MyError`, the value word stays nil, and the pair is not nil. The caller
checks `err != nil`, sees true, and reports a failure that never happened. If it
then calls `err.Error()` on a method that dereferences the receiver, it panics.

Two rules avoid it completely:

1. **Never declare a concrete error type as a variable you might return.** Return
   the literal `nil`, not a nil-valued variable of a concrete type.
2. **Never give a function a concrete error return type.** `func f() *MyError` is
   the shape that invites the bug at every call site.

And one tool catches it, which matters more than either rule. staticcheck's
**SA4023** reports "function never returns a nil interface value" on
`brokenValidate`, and marks every downstream `err != nil` as always true. The
demo in `typednil.go` carries `//nolint:staticcheck` comments purely so the file
can keep demonstrating the bug; take those off in your own code and let the
linter do the remembering. `golangci-lint` runs staticcheck by default, so this
is free the moment it is in CI.

## Method sets, and why the compiler sometimes refuses

A method with a **value receiver** is in the method set of both `T` and `*T`. A
method with a **pointer receiver** is only in the method set of `*T`.

```go
func (c Counter) String() string { ... }  // Counter and *Counter have it
func (c *Counter) Add(s string)  { ... }  // only *Counter has it
```

So if an interface requires `Add`, a `Counter` value does not satisfy it and a
`*Counter` does. The reason is that a value stored in an interface is not
addressable, so Go could not produce the pointer the method needs.

The practical rule: pick one receiver kind per type and use it everywhere. If
any method needs a pointer receiver, give them all pointer receivers, and pass
`*T` around. Mixed receivers are how you end up reading this section again.

## Type assertions and type switches

Going from an interface back to a concrete type is a type assertion, and it has
a comma-ok form for the same reason map lookup does:

```go
s, ok := v.(fmt.Stringer)   // safe: ok reports whether it fit
s := v.(fmt.Stringer)       // panics if it did not
```

A type switch handles several possibilities at once, and is the right tool when
you are decoding `any` from JSON or walking a syntax tree. It is the wrong tool
when it lists your own types: that is a method waiting to be written.

## What the files cover

| File | What it teaches |
|---|---|
| `satisfaction.go` | Structural typing, small interfaces, consumer-defined interfaces, compile-time assertions |
| `typednil.go` | The typed-nil trap, both directions, and how to detect it |
| `methodsets.go` | Value vs pointer receivers, what satisfies what, addressability |
| `assertions.go` | Type assertions, comma-ok, type switches, `any`, embedding |
| `writers.go` | `io.Writer` in practice: one function, six destinations |
| `main.go` | Runs every demo in order |
| `*_test.go` | Table-driven tests, including a test that pins the typed-nil behaviour |

## How to run

```bash
go run ./03-interfaces
go test ./03-interfaces
go test -v -run TestTypedNil ./03-interfaces
```
