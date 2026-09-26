# Linked list

A linked list stores each element in its own node, with a pointer to the next
one. Where a slice is a contiguous block you index into, a list is a chain you
walk.

```
head                                          tail
 │                                             │
 ▼                                             ▼
┌────┬───┐   ┌────┬───┐   ┌────┬───┐   ┌────┬─────┐
│ 10 │ ●─┼──▶│ 20 │ ●─┼──▶│ 30 │ ●─┼──▶│ 40 │ nil │
└────┴───┘   └────┴───┘   └────┴───┘   └────┴─────┘
```

This one keeps a **tail pointer** as well as a head, which is what makes
`AddToTail` O(1) instead of O(n). Without it, appending means walking the whole
chain to find the end.

## What it is actually for

Almost nothing, in Go. A slice beats a linked list for nearly every use, and by
a wide margin: contiguous memory means the CPU prefetcher works, and `append` is
amortised O(1). A list's nodes are scattered, so every step is a potential cache
miss.

| | Slice | This list |
|---|---|---|
| index | O(1) | O(n) |
| append | O(1) amortised | O(1) |
| prepend | O(n) | **O(1)** |
| remove from front | O(n) | **O(1)** |
| memory per element | the element | the element + a pointer |
| cache behaviour | excellent | poor |

Measured, 1000 elements, M2 Max:

| | Slice | This list |
|---|---|---|
| append | 1971 ns, 0 allocs | 7368 ns, 1000 allocs |
| prepend | 411247 ns, 4.3 MB | **7088 ns, 16 KB** |

The slice is 3.7x faster to append and the list is **58x** faster to prepend.
One allocation per node against none at all is the list's structural cost; the
slice's prepend re-copies the whole thing every time, which is the list's
structural win.

The two rows in bold are the whole case for it. A queue that pushes at one end
and pops at the other, or an LRU cache that moves nodes to the front, genuinely
wants a list. Everything else wants a slice.

`container/list` in the standard library is a doubly-linked list and predates
generics, so it stores `any`. This one is generic, which is the version you
would write today.

## Operations

| Method | What it does | Time |
|---|---|---|
| `AddToHead(v)` | Insert at the front | O(1) |
| `AddToTail(v)` | Append | O(1) |
| `RemoveFromHead()` | Remove and return the front | O(1) |
| `RemoveFromTail()` | Remove and return the back | **O(n)** |
| `Len()` | Element count | O(1) |
| `All()` | An iterator over the values | O(n) |
| `String()` | `10 -> 20 -> 30` | O(n) |

`RemoveFromTail` is O(n) and that is not an oversight: removing the last node
requires the one before it, and a singly linked list cannot walk backwards. Fix
it by making the list doubly linked, at the cost of a second pointer per node.
The asymmetry is the lesson.

## The Go-specific parts

**The zero value works.** `var l LinkedList[int]` is an empty list, no
constructor needed. That is lesson 01's rule applied to a container.

**`All()` returns an `iter.Seq[T]`,** so `for v := range l.All()` works. That is
Go 1.23's range-over-function, covered in
[go-concepts/11-generics](../../go-concepts/11-generics/).

**Removal zeroes the node's pointer.** A removed node holding a `next` pointer
would keep the rest of the chain reachable, so removing the head of a
thousand-element list would free nothing. Lesson 02's delete-leaks-the-tail
problem, in a different shape.

## How to run

```bash
go test ./linkedlist
go test -v -run TestRemoveFromHead ./linkedlist
go doc -all ./linkedlist
```
