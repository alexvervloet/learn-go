# Memory and escape analysis

> 📚 [go-concepts](../README.md) · **Step 18 of 18** · [⬅ 17-benchmarks-and-pprof](../17-benchmarks-and-pprof/) · The end of the language material

## What is this?

Where your values actually live, why it matters, and how to find out.

Go has no `new` versus `malloc` decision to make. The compiler decides, per
value, whether it can live on the stack (free, reclaimed when the function
returns) or must go on the heap (allocated, and eventually collected). That
decision is **escape analysis**, and it is the largest single lever on Go
performance that costs no readability.

Lesson 17 measured the consequence: returning `*T` from a constructor cost
7.9 ns and one allocation; returning `T` cost 1.8 ns and none.

## Stack or heap

The rule, roughly: **a value escapes to the heap when the compiler cannot prove
its lifetime ends with the function.**

| Escapes | Does not |
|---|---|
| returning `&x` | returning `x` |
| storing a pointer in a longer-lived structure | a local that stays local |
| boxing into an interface **that escapes** | boxing into one that stays put |
| capturing a variable in a closure that escapes | a closure called immediately |
| `make([]T, n)` where `n` cannot be shown small | `make([]T, 4)`, or a small `n` after inlining |
| sending a pointer on a channel | sending a small value |

Two rows of that table say something narrower than the usual advice, and both
corrections came from measuring rather than reading:

**"`make` with a non-constant size escapes" is wrong.** Escape analysis runs
*after* inlining, so the compiler sees the caller's actual argument.
`makeWithVariableSize(64)` allocates **nothing**; `makeWithVariableSize(100000)`
allocates once. Same function, same source line, different call sites.

**"Passing a value to an interface allocates" is wrong.** Boxing allocates only
when the interface value itself escapes. Measured:

| | Allocations |
|---|---|
| boxed, callee inlined and devirtualised | 0 |
| boxed, callee not inlined, box stays in its frame | 0 |
| boxed and stored where it outlives the call | **1** |
| never boxed at all | 0 |

So escape analysis is not a property of a function. It is a property of a
**call**, and `testing.AllocsPerRun` is how you ask about one.

One trap underneath both: a composite literal whose fields are all constants,
like `User{ID: 2}`, can be emitted as a static value, and then nothing
allocates anywhere. Measuring allocation needs a value the compiler cannot
fold.

Stack allocation is genuinely free: the frame is bumped on entry and popped on
return, with no bookkeeping, no collector involvement, and excellent cache
locality.

## Seeing the decision

```bash
go build -gcflags=-m ./...        # one level of detail
go build -gcflags='-m -m' ./...   # why, in more depth
```

```
./escapes.go:12:6: can inline newUser
./escapes.go:18:9: &User{...} escapes to heap
./escapes.go:24:16: make([]int, n) escapes to heap
./escapes.go:30:7: moved to heap: u
```

Two lines that read alike and mean different things:

- **`escapes to heap`** — the value is heap-allocated at that expression.
- **`moved to heap: x`** — the *variable* `x` was going to be on the stack, and
  something took its address in a way that outlives the frame.

`does not escape` is the one you are trying to see.

## Inlining, and why it matters here

The compiler inlines small functions, and inlining changes escape analysis: a
pointer passed to an inlined function may no longer escape, because the call
that would have outlived the value is gone.

```bash
go build -gcflags='-m' ./...          # reports inlining decisions too
go build -gcflags='-l' ./...          # disable inlining, to compare
```

`//go:noinline` forces a function out of line, which is occasionally useful in
a benchmark and almost never useful in real code.

## Stack growth

Goroutine stacks start at **8KB** and grow by copying: the runtime allocates a
bigger stack, copies the frames, and rewrites the pointers into them. The
maximum is 1GB on 64-bit by default.

That is why lesson 05's recursive parser handled a million levels of nesting
where Python raises at 1000 and C segfaults at 8MB. It also means a deep call
tree costs a copy occasionally rather than a crash.

## The garbage collector

Go's collector is a **concurrent, tricolour mark-and-sweep**, non-generational,
non-compacting, with a write barrier. Non-compacting is the part with visible
consequences: pointers are stable, so `unsafe.Pointer` arithmetic works, and
memory can fragment.

It is tuned for **latency**, not throughput. Pauses are sub-millisecond, and the
price is that it does more total work than a stop-the-world collector would.

| Knob | Default | What it does |
|---|---|---|
| `GOGC` | 100 | Collect when the heap has grown by this percentage since the last cycle |
| `GOMEMLIMIT` | off | A soft limit; the collector works harder as you approach it (Go 1.19) |

`GOGC=off` with a `GOMEMLIMIT` is a real configuration: no proportional
collection, just a ceiling. `GOMEMLIMIT` is the answer to a container OOM-kill,
because the collector is otherwise unaware of the cgroup limit.

## Reducing GC pressure

In order of how much they usually buy:

1. **Allocate less.** Lesson 17's table: pre-size, use `strings.Builder`,
   `strconv` over `fmt`.
2. **Reuse buffers** with `sync.Pool` in a genuinely hot path (lesson 09).
3. **Fewer pointers.** A `[]Item` with no pointer fields is one object to scan;
   a `[]*Item` is n+1. The collector's work is proportional to the number of
   pointers, not to bytes.

   Measured, holding 1,000,000 items alive and taking the fastest of five
   forced collections:

   | | Mark time | vs `[]Item` |
   |---|---|---|
   | `[]Item` (0 pointers each) | 1.20 ms | 1x |
   | `[]*Item` (1 pointer each) | 3.31 ms | 2.8x |
   | `[]ItemWithPointers` (2 each) | 3.98 ms | 3.3x |

   In a fresh process with nothing else live the gap is much wider: 155 µs
   against 3.71 ms, **24x**, rising to 43x at five million items. The lesson's
   own demo shows the smaller ratio because sections 1 to 4 have already filled
   the heap, and the baseline scan is not free. Both numbers are real; which
   one you see depends on what else your program is holding.
4. **Raise `GOGC`,** which trades memory for CPU, when the profile says the
   collector is the bottleneck.

## What the files cover

| File | What it teaches |
|---|---|
| `escapes.go` | The cases that escape and the ones that do not, each isolated |
| `analysis.go` | Running `-gcflags=-m` from a test and reading it |
| `stacks.go` | Stack growth, deep recursion, the 1GB ceiling |
| `collector.go` | `MemStats`, `GOGC`, `GOMEMLIMIT`, forcing a cycle |
| `pressure.go` | Pointer density, and why `[]T` beats `[]*T` for the collector |
| `main.go` | Runs every demo in order |
| `*_test.go` | Tests including `testing.AllocsPerRun`, which asserts escapes |

## How to run

```bash
go run ./18-memory-and-escape-analysis
go test ./18-memory-and-escape-analysis
go test -bench . -benchmem -run '^$' ./18-memory-and-escape-analysis

# See the compiler's decisions
go build -gcflags=-m ./18-memory-and-escape-analysis 2>&1 | grep -E 'escapes|moved to heap'
```
