# Data structures and algorithms

> 📚 [Repository root](../README.md) · [⬅ go-concepts](../go-concepts/) · Next: `backends/learning/`

The mirror of [learning-python-backends'
`d-structs-algos/`](https://github.com/alexvervloet/learning-python-backends/tree/main/d-structs-algos):
the structures built from scratch, the classic algorithms, the theory of
computational hardness, and the eleven interview-pattern families.

Same material, rewritten where Go's type system makes a better answer available.
Generics are the main difference: the Python version writes `list[int]` and the
Go version writes `[]T` with a constraint, so one `Stack[T]` replaces the stack
of strings you would otherwise copy-paste per type.

## How this differs from the Python version

**Generics throughout.** `Stack[T any]`, `HashMap[K comparable, V any]`,
`BST[T cmp.Ordered]`. Before Go 1.18 this material would have been a choice
between `any` with type assertions everywhere or one copy per type; see
[go-concepts/11-generics](../go-concepts/11-generics/) for what that costs.

**Tests are the interface.** The Python repo has a `run_tests.py` script and a
custom assertion harness. Go has `go test`, so there is nothing to write:

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

**One `sorting` package, not six.** The Python version has a folder per
algorithm. Go convention is one package per concern, and putting all six sorts
together means they can be benchmarked against each other in one file, which is
the more interesting comparison anyway.

**Table-driven tests.** The Go idiom, and a better fit for this material than
the Python repo's `test(func, args, expected)` harness: one slice of cases, one
loop, one `t.Run` per case so failures name themselves.

## Structures

| Package | What it is |
|---|---|
| [linkedlist/](linkedlist/) | Singly linked list with head and tail pointers, and an iterator |
| [stack/](stack/) | Slice-backed LIFO stack, plus balanced-bracket checking |
| [queue/](queue/) | Slice-backed FIFO queue, plus a matchmaking example |
| [hashmap/](hashmap/) | Open addressing with linear probing, auto-resizing at 70% load |
| [trie/](trie/) | Prefix tree with prefix search, document scanning and character substitution |
| [graph/](graph/) | Adjacency list and adjacency matrix, side by side, with BFS and DFS |
| [bst/](bst/) | Binary search tree: insert, delete, three traversals, height |
| [redblack/](redblack/) | Self-balancing BST maintaining the red-black invariants on insert |

## Algorithms

| Package | What it is |
|---|---|
| [sorting/](sorting/) | Bubble, insertion, selection, merge, quick, and the stdlib, benchmarked together |
| [searching/](searching/) | Binary search and its variants: first, last, insertion point |
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
| "rotated sorted array", "min X such that..." | **binary search on the answer** | [patterns/binarysearchanswer/](patterns/binarysearchanswer/) |
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
go test -bench . -benchmem ./sorting       # the six sorts compared
go test -run Example ./...                 # every documented example, output-checked
go doc -all ./hashmap                      # the API and its examples
```
