# Trie (the pattern)

The data structure lives in [dsa/trie](../../trie/), with insert, delete, prefix operations,
and the measurements arguing about when a trie is worth using at all. This package is about
**recognising when a problem wants one**, which is a different skill.

## The tell

"Prefix", "autocomplete", "dictionary of words", "starts with", "wildcard search". The signal
underneath is that many strings **share prefixes** and the question is about those shared
prefixes rather than about whole strings.

If the question is only "is this exact string in the set", it is a hash map. `dsa/trie` has
the number: a map is 5.5x faster at that.

## The four shapes

**Walk one string down the trie.** `ReplaceWords`. One pass, O(len(input)). The trie is a
lookup table for "the shortest known prefix of this".

**Collect a subtree.** `Autocompleter`. Walk to the prefix, then gather everything below. The
cost is the size of the **answer**, not of the dictionary.

**Explore the trie and something else together.** `WordSearchII`. The one worth learning.

**Bits, not letters.** `BitTrie`. The least obvious generalisation and the most useful.

## ReplaceWords: where the trie clearly wins

A 200-word sentence, as the root dictionary grows:

| roots | trie | check every root | trie is |
|---|---|---|---|
| 10 | 21 µs | 12 µs | 0.54x |
| 1,000 | 91 µs | 664 µs | **7.3x** |
| 10,000 | 468 µs | 8,801 µs | **18.8x** |

The naive version is O(words × roots × length); the trie walks each word once and stops at
the first terminal, so the dictionary size drops out entirely. At ten roots the trie
construction is not worth it, and by a thousand it is not close.

One detail: `dsa/trie` offers `LongestPrefixOf`, which is what an HTTP router wants. This
problem wants the **shortest**, and the difference is one word in the walk: stop at the first
terminal instead of remembering the last. There is no way to build one from the other.

## LongestCommonPrefix is here to say "not a trie problem"

Building a trie and walking down while each node has one child works, and it costs O(total
length) to build a structure that is walked once and thrown away. Comparing the first word
against the others character by character is O(total length) too, allocates nothing, and is
four lines.

Reaching for the structure because the word "prefix" appears is the mistake this function
exists to name.

## WordSearchII, and two honest results

### The abstraction costs 2.5x to 6.3x

`dsa/trie` deliberately exposes no nodes: it is a container with a prefix API, which is the
right shape for a container and the **wrong shape for this problem**.

The first version carried the prefix **string** and asked the trie about it. Every step calls
`HasPrefix` and `Contains`, each of which re-walks the prefix from the root, so a path of
length k costs O(k) map lookups per cell instead of one. That version loses to a per-word
search at every size tried.

Carrying a `*node` instead makes descending one cell a single map lookup:

| | node-carrying | prefix-carrying |
|---|---|---|
| 8×8, 1,000 words | **510 µs** | 1,529 µs |
| 12×12, 5,000 words | **2.40 ms** | 5.89 ms |

The lesson: the pattern needs a trie you can hold a **position** in, not one you can ask
questions of.

### And the pattern's advantage is about 2x, not an order of magnitude

Against running a single-word search once per dictionary word:

| grid, alphabet, word length, dictionary | trie | per word | trie is |
|---|---|---|---|
| 8×8, 4 letters, 3–6, 100 words | 56 µs | 49 µs | 0.86x |
| 8×8, 4 letters, 3–6, 1,000 words | 510 µs | 315 µs | **0.62x** |
| 12×12, 6 letters, 6–10, 1,000 words | **635 µs** | 950 µs | 1.50x |
| 12×12, 6 letters, 6–10, 5,000 words | **2.40 ms** | 4.84 ms | 2.02x |

The trie **loses** on small grids with short words, however large the dictionary. The reason
is that a per-word search also abandons a path on the first character that does not match,
and it stops at the **first** occurrence, while the trie version explores every path to find
every word.

The benchmark runs both configurations on purpose. One showing only the winning case would be
an argument rather than a measurement.

## A trie over bits

`BitTrie` stores integers by their binary digits, most significant first. Any sequence with a
branching factor small enough to enumerate works, and for integers it is two.

What it buys: a greedy bit-by-bit decision becomes a walk. XOR is 1 exactly when the bits
differ, and a higher bit outweighs every lower bit combined, so "find the value maximising
x XOR v" is answered by preferring the opposite branch at every level, in O(bits) instead of
O(n).

| n | bit trie | every pair |
|---|---|---|
| 100 | 39 µs | **3.6 µs** |
| 1,000 | 379 µs | 305 µs |
| 10,000 | **4.5 ms** | ~30 ms (skipped) |

The crossover is near n = 1,500. Below it, O(n²) with a tiny constant beats O(n × 32) with a
map allocation per node.

## Testing

Every function is checked against the obvious slow version:

- `ReplaceWords` against trying every root and keeping the shortest match.
- `LongestCommonPrefix` by the property, not a fixed answer: every word starts with the
  result, and one character more fails for at least one word.
- `WordSearchII` against running a separate single-word search per word, and separately
  against `WordSearchIIByPrefix`, which shares no traversal code.
- `MaxXORPair` against comparing all n²/2 pairs.

`TestWordSearchIIRestoresTheGrid` checks the grid comes back unchanged after failed searches,
since the grid doubles as the visited set.

`TestAutocompleteIsDeterministic` runs the same query 200 times, because the trie iterates in
map order and without the alphabetical tie-break the ranking would change between runs.

## How to run

```bash
go test ./patterns/trie
go test -run XXX -bench BenchmarkWordSearch -benchtime 20x ./patterns/trie
go test -run XXX -bench 'MaxXORPair|ReplaceWords' -benchtime 20x ./patterns/trie
```
