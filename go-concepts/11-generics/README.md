# Generics

> 📚 [go-concepts](../README.md) · **Step 11 of 18** · [⬅ 10-context](../10-context/) · Next: [12-struct-tags-and-reflection](../12-struct-tags-and-reflection/) ➡

## What is this?

Type parameters, added in Go 1.18 after a decade of argument about whether to
have them at all. One function, many types, checked at compile time.

```go
func Max[T cmp.Ordered](a, b T) T {
    if a > b {
        return a
    }
    return b
}

Max(3, 5)        // T inferred as int
Max("a", "b")    // T inferred as string
Max(1.5, 2.5)    // T inferred as float64
```

Before this, that function existed four times, or once with `any` and a type
switch, or not at all. Python needs none of it because duck typing does the job
at runtime; Go checks at compile time and needed a way to say "any type that
supports `>`".

## Constraints

A constraint is an interface used as a type bound. It can list methods, types,
or both.

```go
any                  // no constraint at all
comparable           // supports == and != (map keys, slices.Contains)
cmp.Ordered          // supports < <= > >=  (numbers and strings)
fmt.Stringer         // has String() string
```

Writing your own uses a **type set**, which is an interface listing concrete
types rather than methods:

```go
type Number interface {
    ~int | ~int8 | ~int16 | ~int32 | ~int64 |
    ~float32 | ~float64
}
```

The `|` is union. The `~` matters: `int` means exactly `int`, while `~int` means
"any type whose underlying type is `int`", which includes `type Celsius int`.
Almost always use `~`, or your generic function silently rejects every named
type in the codebase.

An interface containing a type set can only be used as a constraint. It cannot
be a variable's type, and trying is a compile error with a clear message.

## Inference, and when it gives up

Go infers type arguments from the function's arguments. It cannot infer from the
return type alone:

```go
Max(3, 5)                    // fine
var x float64 = Max(3, 5)    // still int, then a compile error
Max[float64](3, 5)           // explicit, fine

Zero[int]()                  // required: nothing to infer from
```

When inference fails, the fix is to write the type argument. There is no
penalty for being explicit, and it is often clearer.

## Methods cannot have type parameters

This is the limitation people hit first:

```go
func (s *Stack[T]) Map[U any](f func(T) U) *Stack[U]   // DOES NOT COMPILE
```

A **type** can have type parameters; a **method** cannot add its own. The reason
is that it would make interface satisfaction undecidable, since an interface
would need to match an infinite family of methods.

The workaround is a plain function:

```go
func MapStack[T, U any](s *Stack[T], f func(T) U) *Stack[U]
```

This is why `slices.Map` does not exist as a method and why most of the generic
standard library is functions rather than methods.

## When not to use generics

The Go team's own guidance, which is unusually direct: write the concrete
version first, and generalise when you have written it three times.

**Do not** use a type parameter that appears exactly once in the signature.
`func Print[T any](v T)` is `func Print(v any)` with extra steps.

**Do not** reach for generics when an interface fits. If the behaviour varies by
type, that is an interface. If only the *type* varies and the code is identical,
that is a type parameter.

**Do not** write `Map`, `Filter` and `Reduce` and use them everywhere. Go has no
method chaining, so `Reduce(Filter(Map(xs, f), g), h, 0)` reads inside out. A
`for` loop is three lines and everyone can read it.

Good uses are containers (`Set[T]`, `Cache[K,V]`, `Queue[T]`), algorithms over
ordered or comparable things, and removing genuine copy-paste between types.

## How it compiles, and what it costs

Go uses **GC shape stenciling**: one compiled copy per "gcshape", which groups
types by memory layout and pointer positions. Every pointer type shares one
shape, so `[]*User` and `[]*Order` use the same instantiation, with a hidden
**dictionary** argument carrying the type-specific details.

The usual claim is that this makes generic code over pointer types slower,
because method calls go through the dictionary rather than being inlined. I
benchmarked it rather than repeating it, and on Go 1.27, M2 Max:

| Comparison | generic | alternative | Difference |
|---|---|---|---|
| sum 1000 ints | 291.6 ns | 296.3 ns concrete | none |
| sum 1000 ints | 291.6 ns | 574.9 ns via `any` | **`any` is 2x slower** |
| join 100 items with a method call | 7445 ns | 7274 ns via interface | 2%, noise |
| build and query a 1000-element set | 13881 ns | 13442 ns hand-written map | 3% |
| stack of pointers vs stack of ints | 451.7 ns | 468.9 ns | none |

So on these workloads the cost is not measurable. The generic sum is
indistinguishable from the hand-written one; the container is within 3% of the
map it wraps; and the pointer case shows no penalty at all, because a `Stack[T]`
does not call methods on T and so never touches the dictionary.

The one clear result is the comparison people forget: **`any` plus a type switch
is twice as slow** as the generic version, and it moves a compile-time error to
runtime. If you are choosing between generics and `any`, generics win on both
counts.

Reproduce with `go test -bench . -benchmem -run '^$' ./11-generics`. Where the
dictionary does cost something is a generic function calling methods through a
method constraint in a hot loop, and the honest summary is that you will not
find it without a profiler telling you to look.

## Iterators: `iter.Seq`

Go 1.23 added range-over-function, which is generics plus a compiler rewrite:

```go
type Seq[V any] func(yield func(V) bool)

for v := range Values(m) { ... }
```

`yield` returning `false` means the consumer broke out of the loop, and the
producer must stop. This is what `maps.Keys` and `slices.Values` return, and
writing one yourself is about six lines.

The contract is enforced, which I did not expect and is worth knowing. A
producer that calls `yield` again after it has returned `false` does not
silently keep going; the runtime panics:

```
panic: runtime error: range function continued iteration after
       function for loop body returned false
```

The compiler rewrites the loop body into a `yield` function that tracks whether
the loop has exited, and the generated code checks on every call. So the failure
mode of a badly written iterator is an immediate crash naming the problem,
rather than an iterator that keeps reading files after its consumer stopped.
`CountBroken` in `iterators.go` demonstrates it.

The one place the contract is still yours to honour is `iter.Pull`: it runs the
producer on a goroutine, and the `stop` function it returns **must** be called
or that goroutine leaks. `defer stop()`, every time.

## What the files cover

| File | What it teaches |
|---|---|
| `basics.go` | Type parameters, inference, explicit arguments, the single-use smell |
| `constraints.go` | `comparable`, `cmp.Ordered`, type sets, `~`, method constraints |
| `types.go` | Generic structs, why methods cannot add parameters, the workaround |
| `containers.go` | `Set[T]`, `Result[T]`, `Optional[T]`, `Cache[K,V]` |
| `iterators.go` | `iter.Seq`, `iter.Seq2`, writing and consuming one, early exit |
| `costs.go` | GC shape stenciling, dictionaries, and when generic is slower |
| `main.go` | Runs every demo in order |
| `*_test.go` | Tests plus benchmarks comparing generic, concrete and `any` |

## How to run

```bash
go run ./11-generics
go test ./11-generics
go test -bench . -benchmem -run '^$' ./11-generics
```
