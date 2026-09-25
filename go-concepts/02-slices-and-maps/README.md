# Slices and maps

> 📚 [go-concepts](../README.md) · **Step 2 of 18** · [⬅ 01-types-and-zero-values](../01-types-and-zero-values/) · Next: [03-interfaces](../03-interfaces/) ➡

## What is this?

A Python `list` is one thing. A Go slice is three things pretending to be one: a
pointer to an array, a length, and a capacity. Once you can see those three
fields, every confusing thing slices do becomes obvious. Until you can, they
behave like a list that occasionally, silently, shares memory with another list.

```
xs := []int{10, 20, 30, 40, 50}

   xs ──┐
        │  ptr ──────────┐
        │  len  5        │
        │  cap  5        ▼
                    ┌────┬────┬────┬────┬────┐
                    │ 10 │ 20 │ 30 │ 40 │ 50 │   the backing array
                    └────┴────┴────┴────┴────┘
```

Slicing does not copy. `ys := xs[1:3]` makes a second header pointing into the
**same** array:

```
   ys ──┐
        │  ptr ───────────────┐
        │  len  2             │
        │  cap  4  (to end)   ▼
                    ┌────┬────┬────┬────┬────┐
                    │ 10 │ 20 │ 30 │ 40 │ 50 │
                    └────┴────┴────┴────┴────┘
                         └─ ys ──┘
```

`ys[0] = 99` changes `xs[1]`. That part surprises people once and then they
remember it. The part that keeps surprising people is `append`.

## The append aliasing trap

`append(ys, 999)` has two completely different behaviours depending on a number
you cannot see:

**If `len(ys) < cap(ys)`** there is room in the shared array, so append writes
in place. It overwrites `xs[3]`. Both slices change.

**If `len(ys) == cap(ys)`** there is no room, so append allocates a new array,
copies, and writes there. `xs` is untouched. The slices are now independent.

So whether `append` mutates a slice you never mentioned depends on the capacity
of a slice you never looked at. Same line of code, two outcomes:

```go
xs := []int{10, 20, 30, 40, 50}
ys := xs[1:3]            // len 2, cap 4  -> room to spare
ys = append(ys, 999)     // writes into xs[3].  xs is now [10 20 30 999 50]

xs := []int{10, 20, 30, 40, 50}
zs := xs[1:3:3]          // len 2, cap 2  -> full slice expression caps it
zs = append(zs, 999)     // allocates. xs is untouched.
```

The three-index form `xs[low:high:max]` is the fix, and it exists for exactly
this reason. Set `max == high` and the returned slice has no spare capacity, so
any append is forced to allocate. Use it whenever you hand a subslice to code
you do not control.

## append and growth

`append` returning a value is not a style choice. When it reallocates, the new
pointer has to get back to you somehow, and Go has no reference parameters. This
is why ignoring append's return value is always a bug, and why `go vet` has a
check for it.

Growth is amortised: Go roughly doubles capacity for small slices and grows by a
smaller factor once a slice is large. The exact numbers are an implementation
detail and have changed across releases, so never write code that depends on
them. What you can rely on is that N appends cost O(N) total, and that
`make([]T, 0, n)` when you know `n` avoids the intermediate copies entirely.

## Deleting from a slice leaks

The idiomatic delete is `append(xs[:i], xs[i+1:]...)`. It shifts elements left
and shortens the length. The last slot still holds the old value, and the length
no longer covers it, so nothing will ever read it again and nothing will ever
clear it.

For `[]int` that is harmless. For `[]*User` it pins a `User` in memory for as
long as the slice lives. Zero the tail when the element type contains pointers.
Go 1.21's `slices.Delete` does this for you, which is the better answer.

## Maps

Maps are simpler, with three rules that catch people:

**Iteration order is randomised, deliberately.** Not "unspecified but stable in
practice". Go does this so you cannot accidentally depend on an order that was
never promised. If you need determinism, collect the keys, sort them, and
iterate the sorted slice.

Worth being precise about what the randomisation is, because it is easy to
overstate. The runtime picks a random starting bucket and a random offset within
it, then walks from there. It is not a shuffle. A small map lives in a single
bucket, so its observed orders are rotations of one walk: `go test -v -run
TestMapIterationOrderIsRandomised` over a 5-key map reports about 5 distinct
orders across 200 passes, not the 120 a real permutation would reach. The
guarantee you get is "the order varies and is not yours to rely on", and that is
exactly as much as the test asserts.

**You cannot take the address of a map element.** `&m["key"]` does not compile,
because a map may move its entries when it grows. This is why `m["k"].Field = 1`
is an error for a struct value type: read it out, modify, write it back, or store
`*T` instead of `T`.

**The comma-ok form is the only way to distinguish absent from zero.** `m["nope"]`
on a `map[string]int` returns `0`, which is also a perfectly good stored value.
`v, ok := m["nope"]` tells you which.

## What the files cover

| File | What it teaches |
|---|---|
| `slices.go` | The slice header, subslicing, the aliasing trap, the three-index fix, growth, copy, delete |
| `maps.go` | Randomised iteration, comma-ok, unaddressable elements, deterministic output, sets |
| `stdlib.go` | The Go 1.21+ `slices` and `maps` packages, which replace most hand-written loops |
| `main.go` | Runs every demo in order |
| `*_test.go` | Table-driven tests, including one that proves iteration order really does vary |

## How to run

```bash
go run ./02-slices-and-maps
go test ./02-slices-and-maps
go test -v -run TestMapIterationOrderIsRandomised ./02-slices-and-maps
```
