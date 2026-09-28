package auth

import (
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
)

var secret = []byte("a-test-secret-that-is-long-enough-for-hs256")

// TestHashIsSaltedPerCall is what stops a rainbow table.
func TestHashIsSaltedPerCall(t *testing.T) {
	const password = "correct horse battery staple"

	first, err := Hash(password, TestCost)
	require.NoError(t, err)

	second, err := Hash(password, TestCost)
	require.NoError(t, err)

	require.NotEqual(t, first, second,
		"the same password hashes differently every time, because the salt is generated per call")

	// And both verify, because the salt travels inside the hash. There is no salt column for that reason.
	require.NoError(t, Verify(first, password))
	require.NoError(t, Verify(second, password))
}

// TestTheCostIsInTheHash is why raising the cost does not invalidate anything.
func TestTheCostIsInTheHash(t *testing.T) {
	cheap, err := Hash("correct horse battery staple", TestCost)
	require.NoError(t, err)

	got, err := CostOf(cheap)
	require.NoError(t, err)
	require.Equal(t, TestCost, got)

	// A hash made at a higher cost still verifies, and still reports its own cost. That is what makes
	// "re-hash on next login" possible rather than "log everyone out".
	dearer, err := Hash("correct horse battery staple", TestCost+2)
	require.NoError(t, err)

	got, err = CostOf(dearer)
	require.NoError(t, err)
	require.Equal(t, TestCost+2, got)

	require.NoError(t, Verify(dearer, "correct horse battery staple"))
	require.NoError(t, Verify(cheap, "correct horse battery staple"))
}

// TestVerifyGivesOneErrorForEveryFailure is the enumeration defence.
func TestVerifyGivesOneErrorForEveryFailure(t *testing.T) {
	hash, err := Hash("correct horse battery staple", TestCost)
	require.NoError(t, err)

	require.ErrorIs(t, Verify(hash, "wrong password entirely"), ErrWrongPassword)
	require.ErrorIs(t, Verify("not even a hash", "anything"), ErrWrongPassword,
		"a malformed hash and a wrong password give the same error, so a caller cannot tell them apart")
}

// TestPasswordValidation covers the rules and the bcrypt ceiling.
func TestPasswordValidation(t *testing.T) {
	tests := []struct {
		name     string
		password string
		wantErr  bool
	}{
		{"a passphrase", "correct horse battery staple", false},
		{"twelve characters", "abcdefghijkl", false},
		{"eleven characters", "abcdefghijk", true},
		{"one repeated character", strings.Repeat("a", 20), true},
		{"three distinct characters", strings.Repeat("abc", 8), true},
		{"whitespace only", strings.Repeat(" ", 20), true},
		{"seventy-two bytes", strings.Repeat("abcd", 18), false},
		{"seventy-three bytes", strings.Repeat("abcd", 18) + "e", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidatePassword(tc.password)

			if tc.wantErr {
				require.ErrorIs(t, err, ErrWeakPassword)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// TestTheByteLimitIsBytes is the emoji case.
//
// A user picking a password of emoji hits bcrypt's 72-byte ceiling at 18 characters, and a length check written
// with len([]rune(...)) would let it through and then silently truncate.
func TestTheByteLimitIsBytes(t *testing.T) {
	password := strings.Repeat("🔑", 19) // 19 runes, 76 bytes

	require.Len(t, []rune(password), 19)
	require.Equal(t, 76, len(password))

	require.ErrorIs(t, ValidatePassword(password), ErrWeakPassword)

	// And the library agrees: it errors rather than truncating.
	_, err := Hash(password, TestCost)
	require.Error(t, err)
}

// TestTokenRoundTrip is the happy path.
func TestTokenRoundTrip(t *testing.T) {
	token, err := Issue(secret, 42, "alex@example.com", time.Hour)
	require.NoError(t, err)

	claims, err := Parse(secret, token)
	require.NoError(t, err)

	id, err := claims.UserID()
	require.NoError(t, err)
	require.Equal(t, int64(42), id)
	require.Equal(t, "alex@example.com", claims.Email)
	require.Equal(t, Issuer, claims.Issuer)
}

// TestAWrongSecretIsRejected is the basic guarantee.
func TestAWrongSecretIsRejected(t *testing.T) {
	token, err := Issue(secret, 1, "a@b.c", time.Hour)
	require.NoError(t, err)

	_, err = Parse([]byte("a different secret entirely, also long"), token)
	require.ErrorIs(t, err, ErrInvalidToken)
}

// TestAnExpiredTokenIsRejected covers the TTL.
func TestAnExpiredTokenIsRejected(t *testing.T) {
	token, err := Issue(secret, 1, "a@b.c", -time.Minute)
	require.NoError(t, err)

	_, err = Parse(secret, token)
	require.ErrorIs(t, err, ErrInvalidToken)
	require.ErrorIs(t, err, jwt.ErrTokenExpired)
}

// TestAlgNoneIsRejected is the textbook attack.
//
// A JWT names its own algorithm. A token with alg "none" and no signature is a token an attacker wrote, and a
// parser that trusts the header accepts it.
func TestAlgNoneIsRejected(t *testing.T) {
	unsigned, err := jwt.NewWithClaims(jwt.SigningMethodNone, Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "1",
			Issuer:    Issuer,
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
		Email: "attacker@example.com",
	}).SignedString(jwt.UnsafeAllowNoneSignatureType)
	require.NoError(t, err)

	require.True(t, strings.HasSuffix(unsigned, "."),
		"an alg=none token ends with an empty signature segment: %s", unsigned)
	require.Len(t, strings.Split(unsigned, "."), 3, "three segments, the last one empty")

	_, err = Parse(secret, unsigned)
	require.ErrorIs(t, err, ErrInvalidToken)
}

// TestAlgorithmConfusionIsRejected is the subtler one.
//
// An attacker takes a token, rewrites the header to say HS256, and signs it with the RSA PUBLIC key, which is
// public. A parser that picks its verification method from the header verifies an HMAC using a key everyone
// has. WithValidMethods is what stops it, and this asserts the pin is really there.
func TestAlgorithmConfusionIsRejected(t *testing.T) {
	// Build a token claiming HS512 rather than HS256. The signature is genuinely correct for HS512 with the
	// right secret, so the ONLY thing that can reject it is the method pin.
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS512, Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "1",
			Issuer:    Issuer,
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}).SignedString(secret)
	require.NoError(t, err)

	_, err = Parse(secret, token)
	require.ErrorIs(t, err, ErrInvalidToken, "a correctly signed token in the wrong algorithm is still rejected")

	// The point: without WithValidMethods this token parses fine, because the secret is the same and the
	// signature is valid. The pin is not about this token; it is about the RSA case where the "secret" is
	// public knowledge.
	_, err = jwt.ParseWithClaims(token, &Claims{}, func(*jwt.Token) (any, error) { return secret, nil })
	require.NoError(t, err, "without the method pin, the same token is accepted")
}

// TestAWrongIssuerIsRejected covers a secret shared between environments.
func TestAWrongIssuerIsRejected(t *testing.T) {
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "1",
			Issuer:    "some-other-service",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}).SignedString(secret)
	require.NoError(t, err)

	_, err = Parse(secret, token)
	require.ErrorIs(t, err, ErrInvalidToken)
}

// TestATokenWithNoExpiryIsRejected is the one people leave off.
func TestATokenWithNoExpiryIsRejected(t *testing.T) {
	forever, err := jwt.NewWithClaims(jwt.SigningMethodHS256, Claims{
		RegisteredClaims: jwt.RegisteredClaims{Subject: "1", Issuer: Issuer},
	}).SignedString(secret)
	require.NoError(t, err)

	_, err = Parse(secret, forever)
	require.ErrorIs(t, err, ErrInvalidToken, "WithExpirationRequired turns a missing exp into a rejection")
}

// TestASubjectThatIsNotANumberIsCaught covers the claim conversion.
func TestASubjectThatIsNotANumberIsCaught(t *testing.T) {
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "not-a-number",
			Issuer:    Issuer,
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}).SignedString(secret)
	require.NoError(t, err)

	claims, err := Parse(secret, token)
	require.NoError(t, err, "the token is valid; its subject is just not a user id")

	_, err = claims.UserID()
	require.ErrorContains(t, err, "not a user id")
}

// TestTokenPayloadIsReadableByAnyone is the fact people are surprised by.
//
// A JWT is signed, not encrypted. The payload is base64url, not ciphertext, and anyone holding the token can
// read every claim in it. That is why the email is fine to include and a password reset code would not be.
func TestTokenPayloadIsReadableByAnyone(t *testing.T) {
	token, err := Issue(secret, 7, "alex@example.com", time.Hour)
	require.NoError(t, err)

	parts := strings.Split(token, ".")
	require.Len(t, parts, 3, "header.payload.signature")

	decoded, err := jwt.NewParser().DecodeSegment(parts[1])
	require.NoError(t, err)

	require.Contains(t, string(decoded), "alex@example.com",
		"signed is not encrypted: put nothing in a token you would not print")
}
