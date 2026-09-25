# Types and zero values

> 📚 [go-concepts](../README.md) · **Step 1 of 18** · Next: [02-slices-and-maps](../02-slices-and-maps/) ➡

## What is this?

Python has one universal "nothing" value: `None`. A name that hasn't been given a
real value holds `None`, and asking for an attribute on it raises at runtime.

Go has no `None`. Every type carries its own **zero value**, and a variable is born
holding it. An `int` starts at `0`, a `string` starts at `""`, a pointer starts at
`nil`. There is no third state meaning "not set yet".

Think of it as the difference between an empty glass and no glass at all. Python
hands you no glass and you find out when you try to drink. Go always hands you a
glass, and it is empty.

This trades one class of bug for another. You will never get Go's version of
`AttributeError: 'NoneType' object has no attribute 'x'` on a plain struct field.
What you get instead is a value that looks valid and is not: an empty `Config`
whose `Timeout` is `0`, which means "no timeout" to most libraries, silently.

## Zero values, by type

| Type | Zero value | Usable as-is? |
|---|---|---|
| `int`, `float64`, all numerics | `0` | Yes |
| `bool` | `false` | Yes |
| `string` | `""` | Yes |
| pointer, `func`, `chan`, `interface` | `nil` | No, dereferencing or calling panics |
| slice | `nil` | Partly. `len`, `range` and `append` work; indexing panics |
| map | `nil` | Read-only. Reading returns the zero value, **writing panics** |
| struct | every field at its own zero | Yes, recursively |
| array | every element at its own zero | Yes |

The two rows worth memorising are slice and map. A `nil` slice behaves almost
exactly like an empty one, which is why idiomatic Go declares `var items []string`
and appends without ever initialising. A `nil` map does not: writing to one is a
runtime panic, so a map always needs `make` or a literal before first write.

## The useful-zero-value rule

Go's standard library is designed so that a freshly declared value is ready to work:

```go
var buf bytes.Buffer   // ready, no New() needed
buf.WriteString("hi")

var mu sync.Mutex      // ready, unlocked
mu.Lock()

var wg sync.WaitGroup  // ready, counter at 0
```

When you design a struct, aim for the same property. If your type needs a
constructor to be safe, that is a signal, not a given, and the README or doc
comment should say so.

## Constants and `iota`

Go constants are untyped until used, which is why `const x = 1` can be assigned to
an `int`, a `float64`, or a `time.Duration` without a cast. `iota` is a counter
that resets at each `const` block and increments per line, which is how Go writes
enums:

```go
type Level int

const (
    Debug Level = iota  // 0
    Info                // 1
    Warn                // 2
    Error               // 3
)
```

Note the trap this creates: `Debug` is `0`, which is also `Level`'s zero value. A
struct with an unset `Level` field silently means `Debug`. Start at `iota + 1` when
"unset" needs to be distinguishable.

## Conversions, never coercions

Go will not convert between types for you, not even `int` to `int64`. Every
conversion is written out, and that is deliberate: `int(f)` truncates toward zero
and can overflow, and Go wants that visible at the call site rather than inferred.

The one exception is untyped constants, which adopt the type they are used as.

## What the files cover

| File | What it teaches |
|---|---|
| `zerovalues.go` | Zero value per type, the nil-map write panic, nil vs empty slice |
| `declarations.go` | `var` vs `:=`, shadowing, multiple assignment, the blank identifier |
| `constants.go` | Untyped constants, `iota` enums, the zero-value enum trap, `Stringer` |
| `conversions.go` | Numeric conversion, truncation, overflow, string/[]byte/[]rune |
| `main.go` | Runs every demo in order with section headers |
| `*_test.go` | Table-driven tests asserting each claim above |

## How to run

```bash
go run ./01-types-and-zero-values      # guided tour, prints each demo
go test ./01-types-and-zero-values     # verifies every claim the README makes
go test -v ./01-types-and-zero-values  # with each subtest named
```
