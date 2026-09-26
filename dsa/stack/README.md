# Stack

Last in, first out. A pile of plates: you add to the top and take from the top.

```
push(30)          pop() -> 30
   │                  ▲
   ▼                  │
┌──────┐          ┌──────┐
│  30  │ ◀ top    │  20  │ ◀ top
├──────┤          ├──────┤
│  20  │          │  10  │
├──────┤          └──────┘
│  10  │
└──────┘
```

## Why a slice, not a linked list

A stack only ever touches one end, and a slice's one end is its cheap end:
`append` and `s = s[:len(s)-1]` are both O(1) amortised, with contiguous memory
and no per-element allocation. A linked-list stack allocates a node per push and
scatters them across the heap.

So `Stack[T]` here is a slice with three methods and a name. That is not a
simplification, it is the right implementation.

## Operations

| Method | What it does | Time |
|---|---|---|
| `Push(v)` | Add to the top | O(1) amortised |
| `Pop()` | Remove and return the top | O(1) |
| `Peek()` | Return the top without removing | O(1) |
| `Len()` | Element count | O(1) |
| `SearchAndRemove(v)` | Find and remove a value, preserving order | O(n) |

`SearchAndRemove` is not a stack operation and is here because the Python
version has it: a stack you can reach into the middle of is not a stack. It is
included, tested, and labelled as the compromise it is.

## The Go-specific parts

**Pop zeroes the vacated slot.** Shrinking a slice leaves the old value in the
backing array, past `len`, where nothing will read it and nothing will clear it.
For a `Stack[int]` that wastes eight bytes; for a `Stack[*Request]` it pins a
request in memory for as long as the stack lives. This is lesson 02's
delete-leaks-the-tail problem, and the fix is one line.

**The zero value works.** `var s Stack[int]` is an empty stack.

**`Pop` returns `(T, bool)`.** A `Stack[int]` cannot use 0 to mean empty, so the
comma-ok form is the only correct signature. Panicking instead would be
defensible for a stack that is never empty by construction, and this one makes
no such promise.

## Balanced brackets

`IsBalanced` is the canonical stack problem and the reason stacks are taught
first: the nesting structure of the input *is* a stack, so the algorithm is
"push openers, pop and match closers, and the stack must be empty at the end".

Three ways to fail, and all three matter:

- a closer with an empty stack: `)`
- a closer that does not match the top: `(]`
- a non-empty stack at the end: `(`

## How to run

```bash
go test ./stack
go test -v -run TestIsBalanced ./stack
go doc -all ./stack
```
