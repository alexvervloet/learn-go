# Data structures and algorithms

> 📚 [Repository root](../README.md) · [⬅ go-concepts](../go-concepts/) · Next: `backends/learning/`

The structures built from scratch, the classic algorithms, the theory of
computational hardness, and the eleven interview-pattern families, written the
way Go's type system allows: one generic `Stack[T]` rather than a stack of
strings copied per type.

## How this is put together

**Generics throughout.** `Stack[T any]`, `HashMap[K comparable, V any]`,
`bst.Tree[K cmp.Ordered, V any]`. Before Go 1.18 this material would have been a choice
between `any` with type assertions everywhere or one copy per type; see
[go-concepts/11-generics](../go-concepts/11-generics/) for what that costs.

**Tests are the interface.** `go test` is the harness, so there is nothing to
write:

```bash
go test ./...                    # every package
go test ./patterns/...           # the eleven pattern families
go test -v -run TestStack ./stack
go test -bench . -benchmem ./sorting
```

**Examples are documentation.** Every package has `Example` functions, which
are compiled, run and output-checked by `go test`, and appear in `go doc`. A
Go doc comment that shows usage cannot rot, because the output is asserted:

```bash
go doc -all ./stack              # the API plus the examples
go test -run Example ./...       # verify every example's output
```

**One `sorting` package, not six.** Go convention is one package per concern,
and putting all six sorts together means they can be benchmarked against each
other in one file, which is the more interesting comparison anyway.

**Table-driven tests.** The Go idiom: one slice of cases, one loop, one `t.Run`
per case so failures name themselves.

## Structures

| Package | What it is |
|---|---|
| [linkedlist/](linkedlist/) | Singly linked list with head and tail pointers, and an iterator |
| [stack/](stack/) | Slice-backed LIFO stack, plus balanced-bracket checking |
| [queue/](queue/) | Slice-backed FIFO queue, plus a matchmaking example |
| [hashmap/](hashmap/) | Open addressing with linear probing, auto-resizing at 70% load |
| [trie/](trie/) | Rune-keyed prefix tree with subtree counts, wildcard match and longest-prefix routing |
| [graph/](graph/) | Adjacency list and matrix side by side, with BFS, DFS, Dijkstra and topological sort |
| [bst/](bst/) | Binary search tree: insert, delete, range queries, and what it degenerates into |
| [redblack/](redblack/) | Left-leaning red-black tree, with the invariants checked after every insert and delete |

## Algorithms

| Package | What it is |
|---|---|
| [sorting/](sorting/) | Six sorts and the stdlib, benchmarked together, with every tuning decision priced |
| [searching/](searching/) | One partition primitive, and every binary search built on it |
| [pvsnp/](pvsnp/) | Subset sum and the travelling salesman: exponential to solve, polynomial to verify |

## Interview patterns

Eleven families. The skill is not the implementations, it is **recognising which
pattern a problem is asking for**.

| You hear... | Reach for | Package |
|---|---|---|
| "k largest / k most frequent / k closest" | a **heap**, never a full sort | [patterns/heap/](patterns/heap/) |
| "longest/shortest **contiguous** subarray that..." | a **sliding window** | [patterns/slidingwindow/](patterns/slidingwindow/) |
| "pair in a **sorted** array", "from both ends" | **two pointers** | [patterns/twopointers/](patterns/twopointers/) |
| "number of ways to...", "minimum cost to..." | **dynamic programming** | [patterns/dynamicprogramming/](patterns/dynamicprogramming/) |
| "all subsets / permutations / combinations" | **backtracking** | [patterns/backtracking/](patterns/backtracking/) |
| a **matrix** + "connected regions / fewest steps" | **grid BFS/DFS** | [patterns/grid/](patterns/grid/) |
| a list of **`[start, end]`** pairs | **intervals**, sort first | [patterns/intervals/](patterns/intervals/) |
| "minimise the maximum", "maximise the minimum" | **binary search on the answer** | [patterns/binarysearchanswer/](patterns/binarysearchanswer/) |
| "next greater element", "largest rectangle" | **monotonic stack** | [patterns/monotonicstack/](patterns/monotonicstack/) |
| "connected components" as edges arrive | **union-find** | [patterns/unionfind/](patterns/unionfind/) |
| "prefix / autocomplete / wildcard search" | **trie** | [patterns/trie/](patterns/trie/) |

## How to practise with these

Reading solutions does not build the skill. The loop that works:

1. Read the package README: the pattern, not the solutions.
2. For each function, read only its doc comment, then **write your own
   implementation** in a scratch file against the same tests.
3. Compare with the reference. If yours differs, decide which is better and why.
   Sometimes yours is.
4. Come back a week later and re-derive the two you found hardest.

`go doc ./patterns/slidingwindow` prints the contracts without the bodies, which
is exactly what step 2 needs.

## What the measurements said

Every package benchmarks itself against the obvious alternative, and the results are in the
package READMEs. The ones that changed how the code was written:

| Finding | Where |
|---|---|
| `q = q[1:]` is **not** the unbounded leak everyone says it is; the ring buffer wins on allocations, not time | [queue/](queue/) |
| A sorted slice with `slices.BinarySearch` beats a trie at autocomplete by **17x** | [trie/](trie/) |
| Balancing buys **7%** on random input and **163x** on sorted input | [redblack/](redblack/) |
| An adjacency matrix wins exactly one operation and costs **909x** the memory | [graph/](graph/) |
| A func-value comparison costs **~1.5x** against an inlined operator, in three separate packages | [sorting/](sorting/), [patterns/heap/](patterns/heap/) |
| `container/heap` is **1.46x faster** than a generic heap, because the compiler can devirtualise its `Less` | [patterns/heap/](patterns/heap/) |
| Union-find without both optimisations is **893x** more pointer hops | [patterns/unionfind/](patterns/unionfind/) |
| The trie word search beats a per-word search by **2x**, not the order of magnitude it is sold with | [patterns/trie/](patterns/trie/) |

Four bugs were found by benchmarks rather than tests, and they are written up in
[LESSONS.md](../LESSONS.md): a quicksort that was quietly O(√n)-deep on sorted input, a
depth budget that fired on every input, three stacked integer overflows in one search
function, and a sudoku solver that could not reject an impossible puzzle.

## Complexity, in one table

| Operation | Slice | Linked list | Hash map | BST (balanced) | Heap |
|---|---|---|---|---|---|
| index | O(1) | O(n) | — | — | — |
| search | O(n) | O(n) | O(1) avg | O(log n) | O(n) |
| insert at end | O(1) amortised | O(1) | O(1) avg | O(log n) | O(log n) |
| insert at front | O(n) | O(1) | — | — | — |
| delete | O(n) | O(1) given the node | O(1) avg | O(log n) | O(log n) |
| min / max | O(n) | O(n) | O(n) | O(log n) | O(1) |
| ordered traversal | O(n log n) | O(n log n) | O(n log n) | **O(n)** | O(n log n) |

The last row is why a BST still exists in a world with hash maps: it is the only
one of the five that can walk its contents in order without sorting them first.

## How to run

```bash
go test ./...                              # every package
go test -race ./...                        # the graph and queue tests use goroutines
go test ./patterns/...                     # the eleven pattern families
go test -bench . -benchmem ./sorting       # the six sorts compared
go test -run Example ./...                 # every documented example, output-checked
go doc -all ./hashmap                      # the API and its examples
```
