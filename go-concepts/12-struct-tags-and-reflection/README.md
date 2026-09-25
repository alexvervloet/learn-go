# Struct tags and reflection

> 📚 [go-concepts](../README.md) · **Step 12 of 18** · [⬅ 11-generics](../11-generics/) · Next: [13-io-composition](../13-io-composition/) ➡

## What is this?

A struct tag is a string literal attached to a field. The compiler stores it and
otherwise ignores it entirely; libraries read it at runtime with `reflect`.

```go
type User struct {
    ID        int64     `json:"id" db:"user_id"`
    Email     string    `json:"email" validate:"required,email"`
    Password  string    `json:"-"`
    CreatedAt time.Time `json:"created_at,omitzero"`
}
```

This is how `encoding/json` knows to write `created_at`, how `sqlx` maps a
column, and how a validator knows a field is required. It is Go's answer to
Python decorators and Java annotations, and it is deliberately dumber than
either: a tag is just a string, with a convention about its format.

## The tag format

By convention (documented in `reflect.StructTag`), a tag is space-separated
`key:"value"` pairs:

```
`json:"email,omitempty" db:"email" validate:"required,email"`
```

Three rules that cost people time:

**Backticks, not quotes.** The tag contains double quotes, so a normal string
literal would need escaping and nobody does it right.

**No space after the colon.** `json: "email"` parses as an empty `json` key.
`go vet`'s `structtag` check catches this, which is one of the better reasons to
run vet.

**Only exported fields are visible to reflection.** A lowercase field cannot be
marshalled, unmarshalled, or validated by any library, no matter what its tag
says. This is the single most common "why is my JSON empty" question.

## JSON tags specifically

| Tag | Effect |
|---|---|
| `json:"email"` | Use `email` as the key |
| `json:"-"` | Never marshal or unmarshal this field |
| `json:"-,"` | Use the literal key `-` (the trailing comma is the escape) |
| `json:",omitempty"` | Omit when the value is empty, keeping the field name |
| `json:",omitzero"` | Omit when the value is the zero value (Go 1.24) |
| `json:",string"` | Encode a number or bool as a JSON string |

`omitempty` and `omitzero` are not the same thing, and the difference is the
reason `omitzero` was added. `omitempty` omits "empty" values: `0`, `""`,
`false`, `nil`, and empty slices and maps. It does **not** work on a struct,
which is never "empty", so `time.Time{}` has always been marshalled as
`"0001-01-01T00:00:00Z"` however much you wanted it gone. `omitzero` omits the
zero value, works on structs, and consults an `IsZero() bool` method if the type
has one.

## reflect, in three types

```go
reflect.TypeOf(v)   // *reflect.Type:  what type is this?
reflect.ValueOf(v)  // reflect.Value:  the value, manipulable
v.Kind()            // reflect.Kind:   the underlying category
```

`Type` and `Kind` are not the same. For `type Celsius float64`, the `Type` is
`main.Celsius` and the `Kind` is `float64`. Code that switches on `Kind` handles
every named type for free; code that compares `Type` does not.

## Settability

The rule that blocks everyone once:

```go
v := reflect.ValueOf(user)      // a COPY
v.Field(0).SetString("x")       // panic: using unaddressable value

v := reflect.ValueOf(&user).Elem()   // addressable
v.Field(0).SetString("x")            // works
```

A `reflect.Value` is settable only if it is **addressable** and **exported**.
Passing a value to `reflect.ValueOf` copies it, and you cannot take the address
of a copy, so you must pass a pointer and call `.Elem()`. This is why every
`Unmarshal` function in Go takes a pointer.

`CanSet()` reports it, and checking is better than recovering.

## What reflection costs

"Reflection is slow" is true and much too vague. Measured on an M2 Max, Go 1.27:

| Operation | ns/op | vs direct |
|---|---|---|
| direct field access | 26.9 | 1.0x |
| `reflect` by index | 30.3 | **1.1x** |
| `reflect` by cached index | 48.8 | 1.8x |
| `reflect` by name (`FieldByName`) | 86.2 | 3.2x |

So the cost is almost entirely in **looking the field up by name**, not in
reflection itself. Once a library has resolved names to indexes (which every
serious one does, once per type), the remaining overhead is about 10%. That is
why `FieldByName` in a loop is the thing to avoid, and why `encoding/json`
caches a field map per type on first use.

Two results that corrected what I had written here before measuring:

**A hand-written encoder beats `encoding/json` by 1.3x, not by "far".** And
whether it beats it at all depends on one detail:

| Hand-written encoder | ns/op | vs `encoding/json` (313 ns) |
|---|---|---|
| using `fmt.Sprint` for numbers | 330 | **loses** |
| using `strconv.Format*` | 240 | wins by 1.3x |

Identical output, same structure, one substitution. `fmt` is itself reflective,
so reaching for it while hand-rolling an encoder to avoid reflection gives back
more than the reflection cost in the first place. The first version I wrote used
`fmt`, out of habit, and lost.

The honest summary: `encoding/json` is within 30% of careful hand-written code.
Generated encoders (easyjson and similar) do much better, and cost a build step.

**The tag-driven validator is ~100x slower than the hand-written equivalent.**
982 ns against 10 ns. That is the real cost of this lesson's headline technique,
and it is still the right trade: 982 ns once per request is invisible next to a
database round trip, and the hand-written version is five times the code,
rewritten per type, with nothing stopping a field being forgotten.

The rule that survives all of this: **at a boundary, once per request, not once
per row.** Reproduce with `go test -bench . -benchmem -run '^$'
./12-struct-tags-and-reflection`.

## When not to use it

- **When generics will do.** Since 1.18 a great deal of old reflection code is
  better as a type parameter, checked at compile time.
- **When an interface will do.** If you are switching on `Kind` over your own
  types, that is a method.
- **In a hot path.** Measure first, but the answer is usually no.
- **To reach unexported fields.** It is possible with `unsafe` and it will break.

## What the files cover

| File | What it teaches |
|---|---|
| `tags.go` | Tag syntax, parsing with `StructTag`, the `vet` check, exported-only |
| `jsontags.go` | Every JSON tag option, `omitempty` vs `omitzero`, custom marshalling |
| `basics.go` | `Type`, `Value`, `Kind`, walking a struct, `Type` vs `Kind` |
| `settability.go` | Addressability, `CanSet`, why `Unmarshal` takes a pointer |
| `validator.go` | A tag-driven validator in about 100 lines, the real-world use |
| `costs.go` | Reflection vs direct access vs generics, measured |
| `main.go` | Runs every demo in order |
| `*_test.go` | Tests plus benchmarks for every approach |

## How to run

```bash
go run ./12-struct-tags-and-reflection
go test ./12-struct-tags-and-reflection
go test -bench . -benchmem -run '^$' ./12-struct-tags-and-reflection
```
