# Interview patterns

The rest of `dsa/` builds data structures. This half is the other skill:
**recognising which pattern a problem is asking for**. Most coding-interview
questions are one of a small number of patterns in a costume, and the work is
spotting the signal in the problem statement.

| You hear… | Reach for | Package |
|---|---|---|
| "k largest / k most frequent / k closest" | a **heap**, never a full sort | [heap/](heap/) |
| "longest or shortest **contiguous** substring or subarray that…" | a **sliding window** | [slidingwindow/](slidingwindow/) |
| "pair or triple in a **sorted** array", "compare from both ends" | **two pointers** | [twopointers/](twopointers/) |
| "number of ways to…", "minimum cost to reach…", "longest common…" | **dynamic programming** | [dynamicprogramming/](dynamicprogramming/) |
| "all subsets / permutations / combinations", "every way to…" | **backtracking** | [backtracking/](backtracking/) |
| a **matrix** plus "connected regions / reachable / fewest steps" | **grid BFS or DFS** | [grid/](grid/) |
| a list of **`[start, end]`** pairs plus "overlap / merge / schedule" | **intervals**, sort first | [intervals/](intervals/) |
| "minimum or maximum X such that a condition holds" | **binary search on the answer** | [binarysearchanswer/](binarysearchanswer/) |
| "next or previous greater element", "largest rectangle" | a **monotonic stack** | [monotonicstack/](monotonicstack/) |
| "connected components / merge groups / detect a cycle" as edges arrive | **union-find** | [unionfind/](unionfind/) |
| "prefix / autocomplete / dictionary of words", wildcard search | a **trie** | [trie/](trie/) |

## The tells, more precisely

Some of those signals are stronger than others, and a few are easy to confuse.

**Contiguous** is the word that separates sliding window from everything else. "Longest
substring with no repeats" is a window. "Longest *subsequence*" is dynamic programming,
because a subsequence can skip elements and a window cannot.

**Sorted input** points at two pointers, and if the input is not sorted, ask whether
sorting it first is allowed. For "two numbers that sum to k" a hash map is O(n) and
beats two pointers on unsorted input; two pointers wins when the array is already
sorted or when you need O(1) extra space.

**"Minimum maximum"** or **"maximum minimum"** almost always means binary search on
the answer. "Minimise the largest sum of any part" is the shape.

**"Next greater"** in any form is a monotonic stack, and so is anything about
histograms or spans.

**Union-find against graph traversal**: use union-find when edges arrive one at a time
and you need the answer after each, or when the question is only about connectivity.
Use BFS or DFS when you need the actual path.

## How to practise with these

Reading solutions does not build the skill. The loop that works:

1. Read this file and the package README. The pattern, not the solutions.
2. For each problem, read only the doc comment's contract, then **write your own
   implementation** in a scratch file against the same tests.
3. Compare with the version here. If yours differs, decide which is better and why.
   Sometimes yours is.
4. Come back a week later and re-derive the two you found hardest.

Every package here has a table-driven `_test.go` you can point your own implementation
at, and output-checked `Example` functions that double as documentation.

## What these packages are not

They are not a library. The functions are named after the problems rather than after
what they compute, they take and return plain slices, and several of them solve the
same sub-problem in two different ways on purpose so the two can be compared.

Where a pattern has a genuinely reusable core, that core is factored out and tested on
its own: `heap.Heap`, `unionfind.Sets`, and the `Partition` primitive over in
[searching/](../searching/).
