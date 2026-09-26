# Queue

First in, first out. A ticket line: you join at the back and are served from the
front.

```
        Push(40) ──▶ ┌────┬────┬────┬────┐ ──▶ Pop() -> 10
                     │ 40 │ 30 │ 20 │ 10 │
          back ──────┘    └────┴────┘    └────── front
```

A stack pops from the cheap end of a slice. A queue does not, so the
implementation is the interesting part.

## Three ways to pop the front, and what each actually costs

**Move the slice header forward.** The version everyone writes first:

```go
func (q *naiveReslice[T]) Pop() (T, bool) {
    v := q.items[0]
    q.items = q.items[1:]
    return v, true
}
```

**Shift everything down.** The version people write when they hear the first one
is bad:

```go
copy(q.items, q.items[1:])
q.items = q.items[:len(q.items)-1]
```

**A ring buffer.** A fixed slice with a head index, a tail index, and a count.
Both ends move around the array rather than the data moving through it.

```
  capacity 8, 4 elements, head=6, tail=2

  ┌────┬────┬────┬────┬────┬────┬────┬────┐
  │ 30 │ 40 │    │    │    │    │ 10 │ 20 │
  └────┴────┴────┴────┴────┴────┴────┴────┘
            ▲                     ▲
          tail                   head
```

Push writes at `tail` and advances it modulo capacity. Pop reads at `head` and
advances that. When the count reaches capacity the buffer doubles and the
elements are copied into order once, which also un-wraps them.

## The measurements

One push and one pop per iteration, on a queue already holding `window`
elements. Apple M2 Max, Go 1.27, `go test -bench . -benchmem ./queue`:

| window | ring buffer | reslice | shift down |
|---|---|---|---|
| 100 | 4.98 ns, 0 B | 4.67 ns, 14 B | |
| 1,000 | 4.93 ns, 0 B | 5.46 ns, 22 B | 97.6 ns, 0 B |
| 10,000 | 4.90 ns, 0 B | 5.33 ns, 32 B | |
| 1,000,000 | 5.19 ns, 0 B | 5.46 ns, 39 B | |

Fill 10,000 then drain, per full cycle:

| | ring buffer | reslice | shift down |
|---|---|---|---|
| time | 111 µs | **62 µs** | 5,108 µs |

Two of those numbers are not what the textbook version of this section says.

**`q = q[1:]` is not quadratic, and it does not grow without bound.** Reslicing
shrinks `cap` along with `len`, so `append` runs out of capacity and reallocates,
and the old array is then collected. The cost is amortised across the window, so
it comes out at the same nanosecond count as the ring buffer at every size I
measured, and it wins the drain benchmark outright by not doing the ring
buffer's modulo and slot-zeroing work.

**Shifting down is the genuinely bad one.** 20x slower at a window of 1,000, and
83x on a drain, because every pop copies the whole queue. This is the O(n²) that
gets misattributed to the reslice version.

## So why a ring buffer

Not for throughput. The reason is in the `B/op` column: **14 to 39 bytes of
garbage per operation, forever, versus zero.** A queue running at a million
operations a second produces tens of megabytes a second of garbage on the
reslice version, and hands the collector a growing live array to scan every
cycle. The ring buffer reaches its steady-state size and then stops allocating
completely.

The second reason is the shape of the latency. Both versions pay an O(n) copy
when they grow, but the ring buffer pays it a bounded number of times and then
never again, while the reslice version pays it every `window` operations for as
long as the process runs.

The third is retention, and it is the one real memory bug in the reslice version:
**a slice keeps its entire backing array alive, not just the part it points at.**
Fill a queue to a million and drain it to one element, and `cap` reads 1 while
the million-element allocation is still live, because the slice header points
into the middle of it. Nothing is reachable from your code and nothing can be
collected. `TestResliceRetainsPoppedValues` shows the vacated slot still holding
its pointer.

## Operations

| Method | What it does | Time |
|---|---|---|
| `Push(v)` | Join the back | O(1) amortised |
| `Pop()` | Leave the front | O(1) |
| `Peek()` | Look at the front | O(1) |
| `Len()` | Element count | O(1) |
| `SearchAndRemove(v, equal)` | Find and remove, preserving order | O(n) |

## Matchmaking

`Matchmake` is the Python version's example, kept because it shows what a queue
is for and that a real one is rarely a plain FIFO. Players join the back, and
the matchmaker pairs the front player with the first *compatible* player behind
them, who may not be the second in line.

Two decisions carry the whole example:

**An unmatched player goes back to the front, not the back.** Being unmatchable
is not their fault, and on a busy server sending them to the back would starve
them forever. That is why `Matchmake` takes the queue by pointer and mutates it
rather than being pure, and why the queue has an unexported `pushFront`.

**`MatchAll` stops after a full pass with no match.** That condition is the
whole difficulty of the function. Without it, a queue holding one bronze and one
gold player rotates forever, and the test hangs rather than failing.

## The Go-specific parts

**The zero value works,** with a nil slice that grows on first push.

**Pop zeroes the vacated slot,** so a `Queue[*Request]` does not pin a request.
The same problem as lesson 02, and a ring buffer makes it easier to forget than
a stack does, because the slot is not past `len`. It sits in the middle of a live
slice and looks like it still belongs to someone.

**`SearchAndRemove` takes an equality function** rather than constraining `T` to
`comparable`, because constraining it would forbid a `Queue[[]byte]` or a
`Queue[func()]`. `Comparable[T comparable]` embeds `Queue[T]` and adds `Remove`, so
the common case stays short and gets every other method by promotion.

**A full ring buffer has `head == tail`,** so the indices alone cannot tell you
whether it wrapped. The test checks the backing array directly instead.

## How to run

```bash
go test ./queue
go test -bench . -benchmem ./queue      # the table above
go test -bench . -benchmem -run XXX ./queue
```
