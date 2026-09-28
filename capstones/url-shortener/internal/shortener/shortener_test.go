package shortener

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestEncodeDecodeRoundTrip is the property that makes the scheme correct.
func TestEncodeDecodeRoundTrip(t *testing.T) {
	for _, id := range []int64{1, 2, 61, 62, 63, 3843, 3844, 1_000_000, 1 << 40} {
		slug, err := Encode(id)
		require.NoError(t, err)

		back, err := Decode(slug)
		require.NoError(t, err)
		require.Equal(t, id, back, "slug %q", slug)
	}
}

// TestEncodeIsTheShortestForm is why a counter beats a random string.
func TestEncodeIsTheShortestForm(t *testing.T) {
	tests := []struct {
		id   int64
		want string
	}{
		{1, "1"},
		{9, "9"},
		{10, "a"},
		{35, "z"},
		{36, "A"},
		{61, "Z"},
		{62, "10"},
		{3843, "ZZ"},
		{3844, "100"},
	}

	for _, tc := range tests {
		got, err := Encode(tc.id)
		require.NoError(t, err)
		require.Equal(t, tc.want, got, "id %d", tc.id)
	}

	// The practical consequence: 62^5 is 916 million URLs in five characters.
	slug, err := Encode(916_132_831)
	require.NoError(t, err)
	require.Len(t, slug, 5, "a billion URLs fit in five characters")
}

// TestEncodeRejectsNonPositive covers the boundary.
func TestEncodeRejectsNonPositive(t *testing.T) {
	for _, id := range []int64{0, -1, -62} {
		_, err := Encode(id)
		require.ErrorIs(t, err, ErrNotPositive, "id %d", id)
	}
}

// TestDecodeRejectsCharactersOutsideTheAlphabet is the input validation.
func TestDecodeRejectsCharactersOutsideTheAlphabet(t *testing.T) {
	for _, slug := range []string{"", "ab-cd", "hello world", "abc/def", "aä"} {
		_, err := Decode(slug)
		require.ErrorIs(t, err, ErrInvalidSlug, "slug %q", slug)
	}
}

// TestDecodeRejectsAllZeroes is the one case the round trip cannot produce.
//
// "0" decodes to 0, which is not a valid id. Encode never emits it, so a request for /0 or /000 is either a
// typo or a probe, and both deserve a 404 rather than a query for row zero.
func TestDecodeRejectsAllZeroes(t *testing.T) {
	for _, slug := range []string{"0", "00", "0000"} {
		_, err := Decode(slug)
		require.ErrorIs(t, err, ErrInvalidSlug, "slug %q", slug)
	}
}

// TestObfuscateScattersConsecutiveIds is the enumeration defence.
func TestObfuscateScattersConsecutiveIds(t *testing.T) {
	var slugs []string

	for id := int64(1); id <= 5; id++ {
		slug, err := Obfuscate(id)
		require.NoError(t, err)

		slugs = append(slugs, slug)
	}

	t.Logf("ids 1..5 obfuscate to %v", slugs)

	// No shared prefix beyond the first character, and every one is six characters rather than one. Plain
	// Encode would have given "1" "2" "3" "4" "5".
	for _, slug := range slugs {
		require.Len(t, slug, 6)
	}

	require.NotEqual(t, slugs[0][:2], slugs[1][:2], "consecutive ids do not share a prefix")
}

// TestObfuscateRoundTrips is the inverse, which is the part that can silently break.
func TestObfuscateRoundTrips(t *testing.T) {
	for _, id := range []int64{1, 2, 3, 999, 1_000_000, Modulus - 1} {
		slug, err := Obfuscate(id)
		require.NoError(t, err)

		back, err := Deobfuscate(slug)
		require.NoError(t, err)
		require.Equal(t, id, back, "id %d via %q", id, slug)
	}
}

// TestTheMultiplierIsCoprimeWithTheModulus is why the mapping is reversible at all.
//
// 62 = 2 x 31, so the multiplier has to be odd and not divisible by 31. If it were not, two ids would map to
// one slug and the inverse would not exist, and the failure would look like random redirects to the wrong URL.
func TestTheMultiplierIsCoprimeWithTheModulus(t *testing.T) {
	require.NotZero(t, Multiplier%2, "an even multiplier shares a factor of 2 with 62^n")
	require.NotZero(t, Multiplier%31, "a multiple of 31 shares a factor with 62^n")

	// And the inverse actually inverts, which is the direct statement of the same thing.
	//
	// mulMod rather than a plain multiply, for the same reason Obfuscate uses it: Multiplier times its
	// inverse is about 7e19 and the largest int64 is 9.2e18. Writing this assertion the obvious way produces
	// a wrapped product that is not 1, and the test fails on correct code.
	require.Equal(t, int64(1), mulMod(Multiplier, inverseMultiplier, Modulus))
}

// TestObfuscateIsInjective is the strong form: no two ids share a slug.
func TestObfuscateIsInjective(t *testing.T) {
	seen := make(map[string]int64, 20_000)

	for id := int64(1); id <= 20_000; id++ {
		slug, err := Obfuscate(id)
		require.NoError(t, err)

		if other, clash := seen[slug]; clash {
			t.Fatalf("ids %d and %d both encode to %q", other, id, slug)
		}

		seen[slug] = id
	}

	require.Len(t, seen, 20_000)
}

// TestObfuscateRefusesBeyondCapacity is the honest limit.
func TestObfuscateRefusesBeyondCapacity(t *testing.T) {
	_, err := Obfuscate(Modulus)
	require.ErrorContains(t, err, "capacity")

	// One below is fine, so the boundary is where the message says it is.
	_, err = Obfuscate(Modulus - 1)
	require.NoError(t, err)
}

// TestReservedSlugsAreRefused is the route-shadowing guard.
func TestReservedSlugsAreRefused(t *testing.T) {
	for _, slug := range []string{"api", "API", "health", "metrics", "admin"} {
		require.ErrorIs(t, ValidateCustom(slug), ErrReserved, "slug %q", slug)
	}
}

// TestCustomSlugRules covers the rest of the validation.
func TestCustomSlugRules(t *testing.T) {
	tests := []struct {
		slug    string
		wantErr bool
		why     string
	}{
		{"my-link", false, "hyphens are allowed in a custom slug and not in a generated one"},
		{"my_link", false, "so are underscores"},
		{"abc", false, "three characters is the floor"},
		{"ab", true, "two is below it"},
		{strings.Repeat("a", 32), false, "thirty-two is the ceiling"},
		{strings.Repeat("a", 33), true, "thirty-three is above it"},
		{"my link", true, "a space would be encoded in a URL and confuse everything downstream"},
		{"my/link", true, "a slash would create a path segment"},
		{"café", true, "a non-ASCII character needs punycode or percent-encoding"},
	}

	for _, tc := range tests {
		err := ValidateCustom(tc.slug)

		if tc.wantErr {
			require.Error(t, err, "%q: %s", tc.slug, tc.why)
		} else {
			require.NoError(t, err, "%q: %s", tc.slug, tc.why)
		}
	}
}

// TestGeneratedSlugsPassCustomValidationOnlyOnceLongEnough is a real interaction worth pinning.
//
// A generated slug for a low id is one or two characters, which ValidateCustom rejects for being too short.
// That is not a bug in either function: the rules for a slug a person chose and a slug the system derived are
// different, and conflating them would either let someone claim "a" or refuse the system's own first URL.
func TestGeneratedSlugsPassCustomValidationOnlyOnceLongEnough(t *testing.T) {
	short, err := Encode(5)
	require.NoError(t, err)
	require.Len(t, short, 1)
	require.Error(t, ValidateCustom(short), "the system's own early slugs are shorter than a person may claim")

	long, err := Obfuscate(5)
	require.NoError(t, err)
	require.NoError(t, ValidateCustom(long), "an obfuscated slug is always six characters, so it passes")
}

// BenchmarkEncode is here because the operation is on the hot path of every redirect.
//
//	go test -bench Encode -benchmem ./internal/shortener
func BenchmarkEncode(b *testing.B) {
	for i := 0; b.Loop(); i++ {
		_, _ = Encode(int64(i%1_000_000) + 1)
	}
}
