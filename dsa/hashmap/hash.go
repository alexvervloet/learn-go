package hashmap

import "hash/maphash"

// Hash functions
// ==============
//
// A hash table is only as good as its hash function, and "good" means one
// specific thing: keys that differ in any way should land in unrelated slots.
// The functions below are here to be measured against each other, not used.

// SumBytes is the hash everybody writes first, and it is broken in a way that a
// spot check will not show. It gives the same result for every permutation of
// the same bytes, so "abc", "cba" and "bca" collide, and it produces values in
// a narrow band: no 8-character ASCII string can exceed about 1016, so a table
// with 4096 slots leaves three quarters of them permanently empty.
func SumBytes(s string) uint64 {
	var sum uint64
	for i := range len(s) {
		sum += uint64(s[i])
	}
	return sum
}

// FNV1a is what a hand-rolled hash should look like: each byte is mixed into
// the whole accumulator, so position matters and the output covers the full
// 64-bit range.
//
// The stdlib has this in hash/fnv. It is written out because the two constants
// are the entire algorithm, and seeing that they are the entire algorithm is
// the point.
func FNV1a(s string) uint64 {
	const (
		offset = 14695981039346656037 // the starting accumulator
		prime  = 1099511628211        // multiply by this after each byte
	)

	h := uint64(offset)
	for i := range len(s) {
		h ^= uint64(s[i]) // XOR first, hence the 1a
		h *= prime
	}
	return h
}

// Runtime returns the hash the runtime itself uses, seeded per call site.
//
// This is what HashMap uses by default, via maphash.Comparable. It is seeded,
// which the other two are not, and that difference is the subject of
// FloodingCost below.
func Runtime(seed maphash.Seed) func(string) uint64 {
	return func(s string) uint64 { return maphash.Comparable(seed, s) }
}

// Distribution
// ============

// Spread describes how evenly a hash function scatters keys across slots.
type Spread struct {
	Slots      int
	Keys       int
	Used       int // slots that received at least one key
	Worst      int // keys in the most crowded slot
	Collisions int // keys that landed on an already-used slot

	// ChiSquare is the raw crowding score: the sum over slots of
	// count*(count+1)/2, which is the number of probes a linear-probing table
	// would pay to insert every key. Compare it against Expected, not against
	// any absolute number.
	ChiSquare float64
}

// Expected is the crowding score a uniformly random hash would produce for this
// many keys and slots: (n/2m)(n + 2m - 1).
func (s Spread) Expected() float64 {
	n, m := float64(s.Keys), float64(s.Slots)
	if n == 0 || m == 0 {
		return 0
	}
	return n / (2 * m) * (n + 2*m - 1)
}

// Ratio reports ChiSquare against Expected. A hash indistinguishable from random
// scores about 1.0. Well under or over is suspicious; SumBytes scores in the
// hundreds because its keys pile into a handful of slots.
func (s Spread) Ratio() float64 {
	expected := s.Expected()
	if expected == 0 {
		return 0
	}
	return s.ChiSquare / expected
}

// Measure buckets keys by hash and reports how evenly they fell.
//
// The score is the standard hash-quality test: sum over slots of
// count*(count+1)/2, compared against what a uniformly random hash would give.
// The formula matters less than two properties. It punishes crowding
// quadratically, and the ratio converges on 1.0 whatever the number of keys and
// slots, so scores are comparable across runs.
func Measure(hash func(string) uint64, keys []string, slots int) Spread {
	counts := make([]int, slots)
	collisions := 0

	for _, k := range keys {
		idx := int(hash(k) % uint64(slots))
		if counts[idx] > 0 {
			collisions++
		}
		counts[idx]++
	}

	used, worst := 0, 0
	var sum float64

	for _, c := range counts {
		if c > 0 {
			used++
		}
		worst = max(worst, c)
		sum += float64(c) * float64(c+1) / 2
	}

	return Spread{
		Slots:      slots,
		Keys:       len(keys),
		Used:       used,
		Worst:      worst,
		ChiSquare:  sum,
		Collisions: collisions,
	}
}

// Hash flooding
// =============

// CollidingKeys returns n distinct strings that all hash to the same value under
// SumBytes.
//
// Finding them takes no cleverness, which is the problem. Every key is 'a'
// repeated, with one byte raised to 'b' and another lowered to '`', so the byte
// sum is always length*97 while no two keys are equal. A length of L yields
// L*(L-1) keys, so 46 characters is enough for two thousand of them.
//
// The keys must all be the SAME length. An earlier version moved a 'b' around
// strings of growing length, which only collides within each length: it produced
// a dozen short chains instead of one long one and understated the attack by 40x.
//
// Real attacks against unseeded string hashes have used this to turn an O(1)
// lookup into an O(n) scan and take a web server down with one POST body of form
// fields.
func CollidingKeys(n int) []string {
	if n <= 0 {
		return nil
	}

	length := 2
	for length*(length-1) < n {
		length++
	}

	keys := make([]string, 0, n)

	for i := range length {
		for j := range length {
			if i == j {
				continue
			}

			b := make([]byte, length)
			for k := range b {
				b[k] = 'a'
			}
			b[i] = 'b' // +1
			b[j] = '`' // -1, so the sum is unchanged

			keys = append(keys, string(b))
			if len(keys) == n {
				return keys
			}
		}
	}

	return keys
}

// FloodingCost inserts keys into a map using the given hash and returns the
// average number of probes per insertion.
//
// With a seeded runtime hash this stays near 1 no matter what the keys are, and
// an attacker cannot do better because they cannot see the seed. With SumBytes
// and the keys from CollidingKeys it grows linearly, because every insertion
// walks the whole chain built by the ones before it.
func FloodingCost(hash func(string) uint64, keys []string) float64 {
	m := NewWithHash[string, int](len(keys), hash)

	for i, k := range keys {
		m.Put(k, i)
	}

	if len(keys) == 0 {
		return 0
	}
	return float64(m.Probes()) / float64(len(keys))
}
