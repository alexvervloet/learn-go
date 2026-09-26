# Hash map

A hash table with open addressing and linear probing. Go's builtin `map` written
out longhand, so that when a profile blames a map, or an interviewer asks what
happens on a collision, the answer is something you have built.

**Use the builtin map.** This one is 2.1x slower on a hit and 2.5x slower on a
miss. The numbers are further down.

## Three decisions make a hash table

| Decision | This table | Go's builtin map |
|---|---|---|
| Where a key goes | `hash(key) & (capacity-1)` | same, on a bucket of 8 |
| What happens on a collision | linear probing: try the next slot | fill the bucket, then chain to an overflow bucket |
| When to grow | load factor 0.7, double | load factor 0.8125 (13/16) |

## Linear probing

Collide, and you walk forward until you find a free slot.

```
  hash("bo") & 7 == 2, and slot 2 is taken by "ada"

  ┌────┬────┬─────┬────┬────┬────┬────┬────┐
  │    │    │ ada │ bo │    │    │    │    │
  └────┴────┴─────┴────┴────┴────┴────┴────┘
                ▲     ▲
              wanted  landed here instead
```

Looking "bo" up repeats the walk and stops at the first **never-used** slot,
because that is proof the key was never inserted.

Walking forward looks worse than keeping a linked list per slot, and it is
faster, because the next slot is almost always in the cache line you have already
paid to load. A chained table follows a pointer to somewhere else in memory, and a
cache miss costs more than a hundred sequential comparisons.

The price is clustering. Probe counts for an unsuccessful lookup grow roughly as
`(1 + 1/(1-load)²)/2`:

| load | probes on a miss |
|---|---|
| 0.5 | ~2.5 |
| 0.7 | ~6 |
| 0.9 | ~50 |

Hence a load factor of 0.7. At 0.9 the table still works and is unusable.

## Deletion is the hard part

This is the part people get wrong, and it fails silently.

Three keys hash to slot 5 and land in 5, 6, 7. Delete the one in slot 6 and mark
the slot empty, and the lookup for the key in slot 7 probes 5, probes 6, sees an
empty slot, and reports that the key is absent. The table has lost a value with
no error anywhere.

So a deleted slot becomes a **tombstone**: a marker that says "empty for
insertion, occupied for probing". `slotState` has three values for exactly this
reason, and `TestDeleteKeepsTheProbeChain` is built to fail if `Delete` ever sets
a slot back to `empty`.

Tombstones then need their own handling:

**Insertion reuses the first tombstone in the chain,** so adding and removing the
same key a thousand times does not lengthen anything. `TestTombstoneIsReused`
holds that down.

**The load factor counts tombstones,** because a table full of them probes as
slowly as a table full of keys.

**A rebuild is the only way to reclaim them,** and `resize` sizes the new table
from the key count rather than from the old capacity. That covers both cases in
one line: a table full of keys doubles, and a table full of tombstones rebuilds
small. `TestResizeSizesFromCount` checks that a table holding 10 keys in 256
slots shrinks.

## The hash function is most of the quality

4,000 keys of the form `user_N` into 4,096 slots:

| hash | slots reached | worst slot | chi-square ratio |
|---|---|---|---|
| `SumBytes` | **85** of 4096 | **223 keys** | 45.5 |
| `FNV1a` | 2502 | 5 | 1.005 |
| runtime (`maphash.Comparable`) | 2536 | 6 | 1.005 |

The ratio compares the measured crowding against what a uniformly random hash
would produce, so 1.0 is the target and 45.5 means every operation pays 45 times
what it should.

`SumBytes` is the hash everybody writes first, and it fails twice over. It adds
the bytes, so every permutation of the same bytes collides: `listen` and
`silent`, `abc` and `cba`. And its output is bounded by 255 times the key length,
so no short string can reach a high slot number, which is why it touched 85 slots
out of 4,096.

It is also not faster. All three hashes cost about the same per call:

| | ns/op |
|---|---|
| `SumBytes` | 4.92 |
| `FNV1a` | 5.66 |
| runtime | 5.04 |

A bad hash buys nothing.

## Why the seed exists

`SumBytes` is deterministic, so anyone can compute keys that collide under it.
`CollidingKeys` does: every key is `a` repeated with one byte raised to `b` and
another lowered to `` ` ``, so the byte sum never changes and no two keys are
equal. Forty-six characters give 2,070 of them, and finding them takes no
cleverness at all.

Inserting 2,000 of those keys:

| hash | probes per insertion |
|---|---|
| `SumBytes` | **1000** |
| runtime, seeded | 1.55 |

644x, and quadratic in the number of keys, from a single request body full of
form fields. This is a real attack that has taken real servers down.

"Use a better hash" is not the fix. `FNV1a` handles those keys fine, but only
because they were picked for `SumBytes`; an attacker who knows the table uses
`FNV1a` computes a fresh set. `TestFloodingIsNotJustABadModulo` makes that point
explicitly.

The fix is the **seed**. `maphash.MakeSeed` gives each map a hash function the
attacker cannot see, so there are no keys they can choose in advance. Go's
builtin map does the same, and goes further by randomising the iteration start
point, so no code can come to depend on an order that was never promised.

## maphash.Comparable

Go 1.24 added:

```go
func maphash.Comparable[T comparable](seed Seed, v T) uint64
```

It hashes any comparable type using the runtime's own hash functions. Before it,
a generic hash table in Go had three options: make the caller supply a hash
function, restrict the key type to strings and integers, or use reflection. This
table is one line because of it:

```go
return maphash.Comparable(m.seed, key)
```

`maphash` panics on an uninitialised `Seed` rather than treating zero as valid,
which is why `Put` seeds the map on first use. That is what keeps the zero value
usable.

## Against the builtin map

10,000 string keys. Apple M2 Max, Go 1.27:

| | this table | builtin | builtin is |
|---|---|---|---|
| `Get`, hit | 20.9 ns | 9.7 ns | 2.2x faster |
| `Get`, miss | 28.5 ns | 11.5 ns | 2.5x faster |
| build, presized | 259 µs | 138 µs | 1.9x faster |
| build, from empty | 523 µs | 403 µs | 1.3x faster |

The hash accounts for about 5 ns of that 20.9, so the gap is in the probe loop,
not in the hashing. What the runtime has and this does not: buckets of 8 keys with
a one-byte tag per key, so a bucket is scanned with a few comparisons before any
key is touched; hash functions in assembly with AES instructions where the CPU has
them; generated code per map type rather than a generic function; and incremental
growth that spreads a resize across many operations instead of stopping the world
for one O(n) rebuild.

Two places where this table does better, both for the same reason:

**Misses cost more here, relatively, than hits.** Linear probing on a miss walks
to the end of the chain; a bucketed table rejects a whole bucket on eight tag
bytes.

**Fewer, larger allocations.** 2 allocations to build presized against the
builtin's 33, because one flat slice is one allocation and a bucketed map is
many. It uses more bytes in total (524 KB against 437 KB) since every slot
carries a key, a value and a state byte with padding.

Also worth noting: fixing the growth factor from 4x to 2x made building from
empty 19% *slower* (439 µs to 523 µs) while cutting peak memory by 25%. More
resizes, each O(n). That is the trade, and neither direction is free.

## Operations

| Method | Time | Notes |
|---|---|---|
| `Get(k)` | O(1) expected | comma-ok, no sentinel |
| `Put(k, v)` | O(1) amortised | may trigger an O(n) rebuild |
| `Delete(k)` | O(1) expected | leaves a tombstone |
| `Len()`, `Cap()`, `Load()` | O(1) | |
| `All()` | O(capacity) | `iter.Seq2`, arbitrary order |
| `Probes()` | O(1) | cumulative, for the measurements above |

## The Go-specific parts

**The zero value works.** No `New`, no `make`. The first `Put` allocates the
slots and seeds the hash.

**`Delete` clears the key and value, not just the state.** Only `state` means
anything to the probe loop, so a `HashMap[string, *Session]` would otherwise pin
every session it had ever held.

**`All` returns `iter.Seq2[K, V]`,** so `for k, v := range m.All()` works and a
`break` in the caller becomes a `false` from `yield`.

**The capacity is always a power of two,** so `hash % capacity` becomes
`hash & (capacity-1)`. The cost of that choice is that only the low bits of the
hash are used, so a hash with weak low bits clusters where a prime modulus would
have hidden it. `Measure` shows exactly that.

**Iteration order is arbitrary and differs between two maps holding the same
keys,** because the seed differs. `String` sorts before printing, and having to
sort is the demonstration.

## How to run

```bash
go test ./hashmap
go test -v -run 'Flooding|Spread' ./hashmap    # the measurements, logged
go test -bench . -benchmem ./hashmap
```
