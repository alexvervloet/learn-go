# Benchmarks and pprof

> 📚 [go-concepts](../README.md) · **Step 17 of 18** · [⬅ 16-race-detector](../16-race-detector/) · Next: [18-memory-and-escape-analysis](../18-memory-and-escape-analysis/) ➡

## What is this?

Measuring, rather than guessing. Go ships a benchmark runner, a CPU profiler, a
heap profiler, a mutex profiler, a block profiler and an execution tracer, all
in the standard library, all usable from one command.

This repo has leaned on them throughout, and the record is in
[LESSONS.md](../../LESSONS.md): a benchmark that measured two different
workloads, a `sync.Map` claim that was out of date, and a hand-written JSON
encoder that lost to `encoding/json` because it reached for `fmt`. Every one of
those was a plausible belief that measurement contradicted.

## Writing a benchmark

```go
func BenchmarkSum(b *testing.B) {
    data := makeData(1000)   // setup, not measured
    b.ResetTimer()

    for b.Loop() {
        _ = sum(data)
    }
}
```

`b.Loop()` (Go 1.24) replaced `for i := 0; i < b.N; i++`. It is better in two
ways that matter: the loop body cannot be optimised away, because the compiler
treats `b.Loop` as opaque, and setup before the loop is excluded automatically,
so `b.ResetTimer()` is usually unnecessary.

Run them:

```bash
go test -bench .                    # every benchmark
go test -bench Sum -benchmem        # one, with allocation counts
go test -bench . -benchtime=10x     # exactly 10 iterations
go test -bench . -benchtime=5s      # at least 5 seconds each
go test -bench . -count=10          # 10 samples, for benchstat
```

`-benchmem` is not optional. `ns/op` alone hides the thing most often
responsible for it, and `allocs/op` is the number that usually points at the
fix.

## Reading the output

```
BenchmarkSum-12    4152705    291.6 ns/op    0 B/op    0 allocs/op
              │          │          │            │            │
              │          │          │            │            └─ allocations per iteration
              │          │          │            └─ bytes allocated per iteration
              │          │          └─ time per iteration
              │          └─ iterations run
              └─ GOMAXPROCS
```

## The three ways a benchmark lies

**It measures different work on each side.** Two implementations that do not
produce identical output are not comparable. Assert that they agree, in a test,
next to the benchmark.

Identical output is necessary and **not sufficient**, which this lesson found
out about itself. Comparing a hand-written presized `strings.Builder` against
`strings.Join`:

| | ns/op | Verdict |
|---|---|---|
| hand-written, no separator handling | 373 | "1.9x faster than the stdlib" |
| `strings.Join(parts, "")` | 710 | |
| hand-written, **with** separator handling | 675 | |
| `strings.Join(parts, ",")` | 707 | 1.05x, which is noise |

The output was identical for an empty separator, so the agreement test passed.
The work was not identical: `strings.Join` runs a separator branch per element
and the hand-written version did not. A 1.9x win became a rounding error once
both sides did the same job.

So the check is not "do they agree?" but "do they do the same work?", and the
second question is harder because nothing automates it.

**The compiler deletes the work.** A result nobody uses can be optimised away
entirely, giving an impossible 0.3 ns/op. Assign to a package-level variable, or
use `b.Loop`, which is opaque by construction.

**One sample is not a measurement.** Machines are noisy. `-count=10` plus
`benchstat` gives a median and a confidence interval, and turns "8% faster" into
either a real result or noise.

```bash
go test -bench . -count=10 > old.txt
# make the change
go test -bench . -count=10 > new.txt
benchstat old.txt new.txt
```

## Profiling

| Profile | What it shows | Flag |
|---|---|---|
| CPU | where time goes | `-cpuprofile` |
| heap | what is allocated and still reachable | `-memprofile` |
| allocs | every allocation ever made | `-memprofile` with `-memprofilerate=1` |
| block | time blocked on channels and locks | `-blockprofile` |
| mutex | lock contention | `-mutexprofile` |
| trace | scheduler, GC and goroutine timeline | `-trace` |

```bash
go test -bench . -cpuprofile=cpu.out -memprofile=mem.out
go tool pprof -http=:8080 cpu.out     # flame graph in a browser
go tool pprof -top -nodecount=10 cpu.out
```

A running service exposes the same thing over HTTP:

```go
import _ "net/http/pprof"   // registers handlers on DefaultServeMux

go func() { log.Println(http.ListenAndServe("localhost:6060", nil)) }()
```

```bash
go tool pprof http://localhost:6060/debug/pprof/profile?seconds=30
go tool pprof http://localhost:6060/debug/pprof/heap
curl http://localhost:6060/debug/pprof/goroutine?debug=2   # every stack
```

**Never expose that on a public port.** The handlers are unauthenticated and
reveal the source layout, and the CPU profile pauses nothing but the heap dump
is not free. Bind to localhost, or put it behind an authenticated admin route.

## `flat` versus `cum`

The distinction that makes a CPU profile readable:

- **flat** — time in that function's own code.
- **cum** — time in it and everything it called.

A `main` with high `cum` and near-zero `flat` is normal and uninteresting. The
function you want has high `flat`. The one to fix is often its caller.

## The optimisation order

1. **Measure.** A profile, not a hunch.
2. **A better algorithm.** O(n²) to O(n log n) beats any constant factor.
3. **Fewer allocations.** Usually the largest remaining win in Go: pre-size
   slices and maps, reuse buffers, avoid `fmt` in hot paths.
4. **Then micro-optimisations,** and measure again, because they often lose.

Steps 1 and 3 account for most real wins. Measured here, on an M2 Max:

| Change | Before | After | Gain |
|---|---|---|---|
| `+=` to `strings.Builder` | 2349 ns, 99 allocs | 383 ns, 1 alloc | **6.1x** |
| `fmt.Sprintf` to `strconv.Itoa` | 41360 ns, 1735 allocs | 12400 ns, 901 allocs | **3.3x** |
| `make(map)` to `make(map, n)` | 46409 ns, 112 KB | 20231 ns, 58 KB | **2.3x** |
| summing via `any` vs `[]int` | 491 ns | 284 ns | 1.7x |
| returning `*T` vs `T` | 7.9 ns, 1 alloc | 1.8 ns, 0 allocs | 4.4x |
| `append` to `make([]T, 0, n)` | 14770 ns, 38 KB | 12545 ns, 19 KB | 1.18x |

Every one of those is a one-line change. The two largest are both "stop
allocating in a loop", which is the pattern worth internalising.

One detail worth noticing in the numbers: boxing 1000 integers into `[]any`
costs **745** allocations, not 1000. The runtime keeps a static array of small
integers (0-255), so boxing those allocates nothing. Exactly the sort of thing
you learn from `-benchmem` and would never guess.

## What the files cover

| File | What it teaches |
|---|---|
| `writing.go` | Benchmark shapes, `b.Loop`, setup, sub-benchmarks, parallel |
| `lies.go` | The three ways a benchmark misleads, each demonstrated |
| `allocations.go` | Where allocations come from, and the fixes, measured |
| `profiling.go` | Writing profiles from code, reading them, `flat` vs `cum` |
| `httppprof.go` | `net/http/pprof`, and how to expose it safely |
| `main.go` | Runs every demo in order |
| `*_test.go` | The benchmarks themselves, plus tests keeping the pairs honest |

## How to run

```bash
go run ./17-benchmarks-and-pprof
go test ./17-benchmarks-and-pprof
go test -bench . -benchmem -run '^$' ./17-benchmarks-and-pprof

# Write and read a real profile
go test -bench BenchmarkAlloc -cpuprofile=/tmp/cpu.out -run '^$' ./17-benchmarks-and-pprof
go tool pprof -top -nodecount=10 /tmp/cpu.out
```
