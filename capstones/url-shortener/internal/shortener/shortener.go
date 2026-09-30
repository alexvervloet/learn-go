// Package shortener turns a number into a short slug and back.
//
// # Why base62 of a counter, and not a hash
//
// Three schemes get proposed for a URL shortener and only one of them is simple.
//
// A random string needs a uniqueness check, which is a database round trip that can fail and has to retry. At
// low volume the collision probability is tiny and the code to handle it still has to exist, be correct, and be
// tested, and it is the code nobody tests.
//
// A hash of the URL is deterministic, which sounds like a feature until two users want separate analytics for
// the same destination, or one of them wants to delete theirs. It also needs a collision check for the same
// reason, because a truncated hash collides.
//
// Encoding the row's own id is the third. The database already guarantees the id is unique, so two generated
// slugs can never collide, and the slug is the shortest possible for the number of URLs that exist.
//
// Custom slugs break the "nothing to check" part. They share the column, so a person can choose today the
// string a future id will encode to. The store handles that one case by skipping to the next id, which is a
// much smaller retry than a random scheme needs, but it is a retry, and it has a test.
//
// # What it costs
//
// The slugs are sequential, so /1, /2, /3 are the first three and anyone can enumerate every URL in the system.
// That is a real problem and the fix is not a different encoding: it is to treat a short URL as public and put
// nothing private behind one. This package also offers Obfuscate, which multiplies the id by a large coprime
// before encoding, so consecutive ids give scattered slugs. That defeats casual enumeration and is not
// security, because the multiplier is recoverable from two known pairs.
package shortener

import (
	"errors"
	"fmt"
	"math/bits"
	"strings"
)

// Alphabet is the digit set.
//
// # Why this order and these characters
//
// 62 characters: digits, then lowercase, then uppercase. No punctuation, because a slug ends up in a URL, in an
// email that some client will linkify, and in a shell command, and every punctuation character is a special case
// in one of those.
//
// The obvious refinement is to drop the visually ambiguous characters (0, O, o, 1, l, I) so a slug can be read
// aloud or copied from paper. That costs a character set of 56 and changes nothing structural, and it is the
// right call for a code a human types. It is left in here because a URL is copied, not retyped, and because 62
// keeps the arithmetic recognisable.
const Alphabet = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"

// Base is the number of digits.
const Base = int64(len(Alphabet))

// ErrInvalidSlug is returned for a slug containing a character outside the alphabet.
var ErrInvalidSlug = errors.New("shortener: slug contains a character outside the alphabet")

// ErrNotPositive is returned for an id a slug cannot represent.
var ErrNotPositive = errors.New("shortener: id must be positive")

// Encode turns a positive id into a slug.
//
// The loop is the standard change-of-base: take the remainder, prepend the digit, divide. Prepending rather than
// appending-and-reversing is the same work and one fewer place to make a mistake.
func Encode(id int64) (string, error) {
	if id <= 0 {
		return "", fmt.Errorf("%w: got %d", ErrNotPositive, id)
	}

	var b []byte

	for id > 0 {
		b = append([]byte{Alphabet[id%Base]}, b...)
		id /= Base
	}

	return string(b), nil
}

// Decode turns a slug back into an id.
//
// # Why decoding exists at all
//
// It does not have to. A lookup by slug is an indexed query on a string column and that is what this service
// does, because it keeps the slug independent of the id and lets a custom slug live in the same column.
//
// Decode is here because it makes Encode testable as a round trip, which is the only way to be confident the
// digit order is right, and because it is the version a service that skipped the slug column entirely would use.
func Decode(slug string) (int64, error) {
	if slug == "" {
		return 0, fmt.Errorf("%w: empty", ErrInvalidSlug)
	}

	var id int64

	for _, r := range slug {
		digit := strings.IndexRune(Alphabet, r)
		if digit < 0 {
			return 0, fmt.Errorf("%w: %q", ErrInvalidSlug, r)
		}

		// Overflow is not checked, deliberately: an int64 id is 11 base62 digits, and a slug longer than that
		// is not something this service produced. Validate the length before calling if the input is untrusted.
		id = id*Base + int64(digit)
	}

	if id <= 0 {
		return 0, fmt.Errorf("%w: %q decodes to %d", ErrInvalidSlug, slug, id)
	}

	return id, nil
}

// Multiplier scatters consecutive ids.
//
// # Why this specific number
//
// It has to be coprime with 62^n for the mapping to be reversible, which means odd and not divisible by 31,
// because 62 = 2 x 31. This one is a prime, which satisfies both and is easy to check. It is also large enough
// that small ids produce long slugs, which is the point: id 1 should not encode to a single character.
const Multiplier = int64(1_580_030_173)

// The first version of this package computed `(id * Multiplier) % Modulus` directly, and the test found the
// bug immediately: id 56,800,235,583 times 1,580,030,173 is about 9.0e19, and the largest int64 is 9.2e18. The
// product wrapped, went negative, and Encode rejected it as "id must be positive".
//
// Shrinking the multiplier until the forward product fits is not enough either, because the INVERSE is a
// different number and can be much larger: for the largest multiplier that fits, the inverse is 4.5e10 and
// multiplying that by the modulus overflows again.
//
// mulMod below does the multiplication in 128 bits, which removes the constraint entirely and lets the
// multiplier be chosen for its scattering rather than for its size.

// Modulus bounds the obfuscated space.
//
// 62^6, so every obfuscated slug is at most 6 characters and the service supports 56 billion URLs. Beyond that
// the multiplication wraps and two ids share a slug, so this is a capacity decision rather than a constant.
const Modulus = int64(56_800_235_584) // 62^6

// inverseMultiplier undoes Multiplier modulo Modulus.
//
// Computed with the extended Euclidean algorithm rather than hard-coded, because a hard-coded inverse and a
// changed multiplier is a silent data-corruption bug: every existing slug decodes to the wrong id.
var inverseMultiplier = modularInverse(Multiplier, Modulus)

// Obfuscate encodes an id so that consecutive ids give unrelated slugs.
func Obfuscate(id int64) (string, error) {
	if id <= 0 {
		return "", fmt.Errorf("%w: got %d", ErrNotPositive, id)
	}

	if id >= Modulus {
		return "", fmt.Errorf("shortener: id %d is beyond the %d-URL capacity of the obfuscated space", id, Modulus)
	}

	return Encode(mulMod(id, Multiplier, Modulus))
}

// Deobfuscate reverses Obfuscate.
func Deobfuscate(slug string) (int64, error) {
	scrambled, err := Decode(slug)
	if err != nil {
		return 0, err
	}

	return mulMod(scrambled, inverseMultiplier, Modulus), nil
}

// mulMod computes (a * b) mod m without overflowing.
//
// # How 128-bit arithmetic is available in Go
//
// math/bits.Mul64 returns the full 128-bit product as a high and a low word, and Div64 divides a 128-bit
// numerator by a 64-bit divisor. Both compile to single instructions on amd64 and arm64, so this is not slower
// than the wrong version, it is the same speed and correct.
//
// Div64 PANICS if the quotient would not fit in 64 bits, which happens when hi >= m. Here a and b are both
// below m and m is below 2^36, so the product is below 2^72 and hi is below 2^8, which is comfortably under m.
// The precondition holds by construction rather than by luck, and that is worth stating because a caller
// passing an unreduced a would break it.
func mulMod(a, b, m int64) int64 {
	hi, lo := bits.Mul64(uint64(a), uint64(b))
	_, rem := bits.Div64(hi%uint64(m), lo, uint64(m))

	return int64(rem)
}

// modularInverse finds x such that (a*x) mod m == 1.
//
// # The iterative extended Euclid, and why not the recursive one
//
// The recursive form returns one coefficient and reconstructs the other by division, which needs a special
// case when the remainder is zero and is easy to get backwards. I got it backwards: the inverse came out
// wrong, every obfuscated slug decoded to a different id, and the round-trip test caught it on id 1.
//
// The iterative form carries the coefficient along with the remainder, so there is nothing to reconstruct. Both
// stay bounded by m, so nothing overflows either.
func modularInverse(a, m int64) int64 {
	oldR, r := a%m, m
	oldS, s := int64(1), int64(0)

	for r != 0 {
		q := oldR / r
		oldR, r = r, oldR-q*r
		oldS, s = s, oldS-q*s
	}

	if oldR != 1 {
		panic(fmt.Sprintf("shortener: %d is not coprime with %d, so the mapping is not reversible", a, m))
	}

	return ((oldS % m) + m) % m
}

// Reserved slugs cannot be handed out, because they would shadow a route.
//
// # The bug this prevents
//
// A shortener that serves redirects from the root and an API from /api has to make sure no slug is "api", or
// that slug's redirect is unreachable and the API path is ambiguous. With sequential ids this happens at a
// predictable point, and the fix is a deny list checked at creation rather than a route ordering trick.
var Reserved = map[string]bool{
	"api":     true,
	"health":  true,
	"healthz": true,
	"metrics": true,
	"admin":   true,
	"login":   true,
	"static":  true,
	"favicon": true,
	"robots":  true,
}

// ErrReserved is returned for a slug that would shadow a route.
var ErrReserved = errors.New("shortener: that slug is reserved")

// ValidateCustom checks a user-supplied slug.
//
// A custom slug is a different feature from a generated one and it needs its own rules: it is chosen by a
// person, it can collide with an existing one, and it can be an attempt to impersonate a route.
func ValidateCustom(slug string) error {
	const (
		minLen = 3
		maxLen = 32
	)

	if len(slug) < minLen || len(slug) > maxLen {
		return fmt.Errorf("shortener: a custom slug must be %d to %d characters, got %d", minLen, maxLen, len(slug))
	}

	for _, r := range slug {
		if !strings.ContainsRune(Alphabet, r) && r != '-' && r != '_' {
			return fmt.Errorf("%w: %q", ErrInvalidSlug, r)
		}
	}

	if Reserved[strings.ToLower(slug)] {
		return fmt.Errorf("%w: %q", ErrReserved, slug)
	}

	return nil
}
