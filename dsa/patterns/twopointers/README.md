# Two pointers

## The tell

Two signals, pointing at two different variants.

**"A pair, triple, or subarray in a sorted array."** Two pointers converging from the
ends. Sortedness is what makes it work: if the sum of the two ends is too small, the only
way to increase it is to move the left pointer right, so one comparison eliminates an
entire row of the n×n search space.

**"Compare or partition in place."** Two pointers moving the same direction, one reading
and one writing. Every in-place filter, dedupe and partition is this shape, and it is why
those operations need no extra memory.

## Against a hash map

For "two numbers summing to k" on **unsorted** input, a hash map is O(n) and two pointers
need a sort first, at O(n log n). The map wins. Two pointers win when

- the input is already sorted, or
- O(1) extra space is required, or
- the answer needs the elements in order anyway, as `ThreeSum` does.

Reaching for two pointers on unsorted input and sorting to enable it is a common reflex
and usually the wrong call. `TwoSumUnsorted` is the map version, kept here for exactly
that comparison. It also returns the **original** indices, which a sort would have
destroyed.

## The functions

Converging:

| | |
|---|---|
| `TwoSumSorted` / `TwoSumInts` | a pair summing to target, in O(n) and O(1) space |
| `TwoSumUnsorted` | the map version, for the comparison |
| `ThreeSum` | every distinct triple summing to zero, O(n²) |
| `MaxWaterContainer` | the largest area between two walls |
| `IsPalindrome` | ignoring punctuation and case, in O(1) space |

Same direction:

| | |
|---|---|
| `Dedupe` | remove consecutive duplicates in place |
| `DedupeAllowing` | keep at most `limit` copies of each |
| `MoveZerosToEnd` | in place, keeping the order of the rest |
| `PartitionByPredicate` | survivors to the front |
| `SquaresOfSorted` | squares of a sorted slice, sorted, in O(n) |

## The parts worth reading twice

**`ThreeSum`'s duplicate handling happens in three places,** and missing any one gives
repeated triples that a small test will not reveal. Skip a repeated *first* element; after
a match, advance **both** pointers past their duplicates. Advancing only one emits the
same triple again from the other side. This is checked against a brute-force
triple-nested loop over a nine-value alphabet, where duplicates are constant.

**`MaxWaterContainer` moves the shorter wall,** always. Moving the taller one can never
help, because the area is capped by the shorter wall, so a narrower window with the same
cap is never better. That argument turns O(n²) into one pass, and it is exactly the kind
of greedy step that sounds right and needs checking, so it is verified against brute force
on 5,000 random inputs.

**`SquaresOfSorted` fills the output from the back.** The trap is that the input may hold
negatives, so the largest square is at one **end**, not in the middle. Filling forwards
would need the smallest square, which is somewhere in the middle and takes a search to
find.

**`DedupeAllowing` collapses to one comparison:** an element may be written if it differs
from the one `limit` positions back in the **output**. That works because the output is
already deduplicated to the limit, so differing from that one means fewer than `limit`
copies precede it.

**`MoveZerosToEnd` fills the tail rather than swapping as it goes.** Swapping does more
writes for the same result: a million zeros followed by one non-zero costs one write here
and a million swaps the other way.

**`PartitionByPredicate` keeps the survivors in order and scrambles the rest.** Each swap
throws a rejected element to wherever the write pointer was, so `[1 2 3 4 5 6 7 8]`
partitioned on "even" gives `[2 4 6 8]` then `[5 3 7 1]`. A fully stable partition needs
O(n) extra space or a rotation-based algorithm at O(n log n). The doc comment said both
halves kept their order until an example proved otherwise.

## The Go-specific parts

**`TwoSumSorted` takes an `add` function,** because Go has no constraint for "things that
can be added". `cmp.Ordered` covers `<`, and there is no arithmetic constraint in the
standard library; `constraints.Integer` lives in `x/exp` and this module has no
dependencies. So the options are a func parameter or a per-type wrapper, and both are here
(`TwoSumSorted` and `TwoSumInts`) so the trade is visible.

**`Dedupe` returns a count, not a slice,** matching `slices.Compact`'s contract: the caller
uses `s[:n]` and everything past `n` is unspecified. `TestDedupeMatchesSlicesCompact` uses
the standard library as the oracle.

**`IsPalindrome` is ASCII-only, deliberately,** and the limitation is stated rather than
hidden. `unicode.IsLetter` would accept `é`, and then lowercasing has to handle case
mappings that are not one rune to one rune in every script. `strings.ToLower` and
`unicode.IsLetter` are the right tools for the real job.

## How to run

```bash
go test ./patterns/twopointers
```
