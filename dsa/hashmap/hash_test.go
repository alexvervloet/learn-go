package hashmap

import (
	"fmt"
	"hash/maphash"
	"math"
	"testing"
)

// wordish keys, the shape a real table sees: short, similar, sharing prefixes.
func testKeys(n int) []string {
	keys := make([]string, n)
	for i := range keys {
		keys[i] = fmt.Sprintf("user_%d", i)
	}
	return keys
}

func TestSumBytesIsOrderBlind(t *testing.T) {
	// The failure a spot check misses. Any permutation hashes the same.
	for _, pair := range [][2]string{{"abc", "cba"}, {"ab", "ba"}, {"listen", "silent"}} {
		if SumBytes(pair[0]) != SumBytes(pair[1]) {
			t.Errorf("SumBytes(%q) != SumBytes(%q), expected them to collide", pair[0], pair[1])
		}
		if FNV1a(pair[0]) == FNV1a(pair[1]) {
			t.Errorf("FNV1a(%q) == FNV1a(%q), so position is being ignored", pair[0], pair[1])
		}
	}
}

// TestSumBytesUsesAlmostNoneOfTheRange is the other half of why it fails: its
// output is bounded by 255 times the key length, so a table with many slots
// cannot use most of them however good the modulo is.
func TestSumBytesUsesAlmostNoneOfTheRange(t *testing.T) {
	const slots = 4096

	spread := Measure(SumBytes, testKeys(4000), slots)
	if spread.Used > slots/8 {
		t.Errorf("SumBytes reached %d of %d slots; expected it to be confined", spread.Used, slots)
	}
	t.Logf("SumBytes: %d/%d slots used, worst slot %d keys, chi ratio %.1f",
		spread.Used, slots, spread.Worst, spread.Ratio())
}

func TestGoodHashesSpreadEvenly(t *testing.T) {
	keys := testKeys(4000)
	seed := maphash.MakeSeed()

	for _, tc := range []struct {
		name string
		hash func(string) uint64
	}{
		{"FNV1a", FNV1a},
		{"runtime", Runtime(seed)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spread := Measure(tc.hash, keys, 4096)

			t.Logf("%d/%d slots used, worst slot %d keys, %d collisions, chi ratio %.3f",
				spread.Used, spread.Slots, spread.Worst, spread.Collisions, spread.Ratio())

			// A chi-square ratio near 1.0 is what "indistinguishable from
			// random" means. The band is wide because 4000 keys is a small
			// sample and this must not be flaky; SumBytes scores in the
			// hundreds, so there is no risk of confusing the two.
			if math.Abs(spread.Ratio()-1.0) > 0.15 {
				t.Errorf("chi ratio %.3f, want within 0.15 of 1.0", spread.Ratio())
			}
			if spread.Used < 2500 {
				t.Errorf("only %d of 4096 slots used", spread.Used)
			}
		})
	}
}

func TestCollidingKeysAreDistinctAndCollide(t *testing.T) {
	keys := CollidingKeys(50)

	if len(keys) != 50 {
		t.Fatalf("got %d keys, want 50", len(keys))
	}

	seen := map[string]bool{}
	for _, k := range keys {
		if seen[k] {
			t.Errorf("duplicate key %q", k)
		}
		seen[k] = true
	}

	want := SumBytes(keys[0])
	for _, k := range keys[1:] {
		if SumBytes(k) != want {
			t.Errorf("SumBytes(%q) = %d, want %d", k, SumBytes(k), want)
		}
	}
}

// TestFloodingCost is the security argument for seeding, and the numbers are the
// point. Keys chosen to collide turn every insertion into a walk down the chain
// the earlier insertions built, which is O(n) per operation and O(n^2) overall.
//
// The same keys against the seeded runtime hash cost about one probe each,
// because the attacker cannot compute collisions for a seed they cannot see.
func TestFloodingCost(t *testing.T) {
	const n = 2000

	attack := CollidingKeys(n)
	seed := maphash.MakeSeed()

	naive := FloodingCost(SumBytes, attack)
	seeded := FloodingCost(Runtime(seed), attack)

	t.Logf("%d hand-picked keys: SumBytes %.0f probes/insert, runtime hash %.2f probes/insert (%.0fx)",
		n, naive, seeded, naive/seeded)

	if naive < float64(n)/20 {
		t.Errorf("SumBytes cost %.1f probes per insertion; expected the attack to bite", naive)
	}
	if seeded > 2 {
		t.Errorf("the seeded hash cost %.2f probes per insertion, want under 2", seeded)
	}
}

// TestFloodingIsNotJustABadModulo: the attack works on the hash, not on the
// table. FNV1a is unseeded too, but the keys were chosen against SumBytes, so
// they mean nothing to it. An attacker who knew the table used FNV1a could
// compute a fresh set, which is why "use a better hash" is not the fix. Seeding
// is the fix.
func TestFloodingIsNotJustABadModulo(t *testing.T) {
	attack := CollidingKeys(2000)

	if cost := FloodingCost(FNV1a, attack); cost > 2 {
		t.Errorf("FNV1a cost %.2f probes per insertion on keys not chosen for it", cost)
	}
}
