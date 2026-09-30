package auth

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
)

var secret = []byte("a-test-secret-that-is-long-enough-for-hs256")

// TestRefreshTokensAreUnpredictable is the entropy claim.
func TestRefreshTokensAreUnpredictable(t *testing.T) {
	seen := map[string]bool{}

	for range 1000 {
		token, hash, err := NewRefreshToken()
		require.NoError(t, err)

		require.False(t, seen[token], "a token repeated within 1000 draws")
		seen[token] = true

		require.Len(t, hash, 32, "SHA-256")

		raw, err := base64.RawURLEncoding.DecodeString(token)
		require.NoError(t, err, "RawURLEncoding: URL-safe and unpadded")
		require.Len(t, raw, RefreshTokenBytes)
	}

	require.Len(t, seen, 1000)
}

// TestTheTokenIsNotRecoverableFromTheHash is why the hash is what gets stored.
func TestTheTokenIsNotRecoverableFromTheHash(t *testing.T) {
	token, hash, err := NewRefreshToken()
	require.NoError(t, err)

	// The obvious thing to check: the stored bytes are not the token.
	require.NotEqual(t, []byte(token), hash)
	require.NotContains(t, string(hash), token)

	// And the hash is reproducible from the token, which is what makes the lookup work.
	again, err := HashRefreshToken(token)
	require.NoError(t, err)
	require.Equal(t, hash, again)
}

// TestHashRefreshTokenRejectsJunk covers the input validation on the refresh path.
func TestHashRefreshTokenRejectsJunk(t *testing.T) {
	for _, token := range []string{
		"",
		"not base64!!!",
		base64.RawURLEncoding.EncodeToString([]byte("too short")),
		base64.RawURLEncoding.EncodeToString(make([]byte, 64)),
	} {
		_, err := HashRefreshToken(token)
		require.ErrorIs(t, err, ErrInvalidToken, "token %q", token)
	}
}

// TestAccessTokenRoundTrip is the happy path.
func TestAccessTokenRoundTrip(t *testing.T) {
	token, err := IssueAccess(secret, 42, "alex@example.com", 15*time.Minute)
	require.NoError(t, err)

	claims, err := ParseAccess(secret, token)
	require.NoError(t, err)

	id, err := claims.UserID()
	require.NoError(t, err)
	require.Equal(t, int64(42), id)
	require.Equal(t, "alex@example.com", claims.Email)
	require.NotEmpty(t, claims.ID, "a jti, so a token is identifiable in a log without logging the token")
}

// TestAlgNoneIsRejected is the textbook attack.
func TestAlgNoneIsRejected(t *testing.T) {
	unsigned, err := jwt.NewWithClaims(jwt.SigningMethodNone, Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: "1", Issuer: Issuer, ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}).SignedString(jwt.UnsafeAllowNoneSignatureType)
	require.NoError(t, err)

	require.True(t, strings.HasSuffix(unsigned, "."), "the signature segment is empty")

	_, err = ParseAccess(secret, unsigned)
	require.ErrorIs(t, err, ErrInvalidToken)
}

// TestAlgorithmConfusionIsRejected is the subtler one, and the reason WithValidMethods is there.
func TestAlgorithmConfusionIsRejected(t *testing.T) {
	// HS512 with the correct secret, so the signature is genuinely valid. Only the method pin can reject it.
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS512, Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: "1", Issuer: Issuer, ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}).SignedString(secret)
	require.NoError(t, err)

	_, err = ParseAccess(secret, token)
	require.ErrorIs(t, err, ErrInvalidToken)

	// Without the pin the same token parses. The pin is not about this token; it is about the RSA case, where
	// an attacker rewrites the header to HS256 and signs with a key that is published.
	_, err = jwt.ParseWithClaims(token, &Claims{}, func(*jwt.Token) (any, error) { return secret, nil })
	require.NoError(t, err, "this is what the pin prevents")
}

// TestAnExpiredAccessTokenIsRejected covers the TTL.
//
// The token is built by hand rather than with a negative TTL, because IssueAccess now refuses anything below a
// second and a negative duration is below a second. Refusing it is right: a caller asking for a token that is
// already expired has a bug, and a test needing one is a test, not a caller.
func TestAnExpiredAccessTokenIsRejected(t *testing.T) {
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "1",
			Issuer:    Issuer,
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-time.Hour)),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Minute)),
		},
	}).SignedString(secret)
	require.NoError(t, err)

	_, err = ParseAccess(secret, token)
	require.ErrorIs(t, err, jwt.ErrTokenExpired)
}

// TestAMissingExpiryIsRejected is the one people leave off.
func TestAMissingExpiryIsRejected(t *testing.T) {
	forever, err := jwt.NewWithClaims(jwt.SigningMethodHS256, Claims{
		RegisteredClaims: jwt.RegisteredClaims{Subject: "1", Issuer: Issuer},
	}).SignedString(secret)
	require.NoError(t, err)

	_, err = ParseAccess(secret, forever)
	require.ErrorIs(t, err, ErrInvalidToken, "WithExpirationRequired turns a missing exp into a rejection")
}

// TestAWrongIssuerIsRejected covers a secret copied between environments.
func TestAWrongIssuerIsRejected(t *testing.T) {
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: "1", Issuer: "some-other-service",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}).SignedString(secret)
	require.NoError(t, err)

	_, err = ParseAccess(secret, token)
	require.ErrorIs(t, err, ErrInvalidToken)
}

// TestHashIsSaltedPerCall is what stops a rainbow table.
func TestHashIsSaltedPerCall(t *testing.T) {
	const password = "correct horse battery staple"

	first, err := Hash(password, TestCost)
	require.NoError(t, err)

	second, err := Hash(password, TestCost)
	require.NoError(t, err)

	require.NotEqual(t, first, second)
	require.NoError(t, Verify(first, password))
	require.NoError(t, Verify(second, password))
}

// TestVerifyGivesOneErrorForEveryFailure is the enumeration defence.
func TestVerifyGivesOneErrorForEveryFailure(t *testing.T) {
	hash, err := Hash("correct horse battery staple", TestCost)
	require.NoError(t, err)

	require.ErrorIs(t, Verify(hash, "a completely different password"), ErrWrongPassword)
	require.ErrorIs(t, Verify("not even a hash", "anything"), ErrWrongPassword)
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
		{"whitespace only", strings.Repeat(" ", 20), true},
		{"seventy-two bytes", strings.Repeat("abcd", 18), false},
		{"seventy-three bytes", strings.Repeat("abcd", 18) + "e", true},
		{"nineteen emoji, seventy-six bytes", strings.Repeat("🔑", 19), true},
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

// TestASubSecondTTLIsRefused is the truncation, made an error.
//
// jwt/v5 truncates exp and iat to jwt.TimePrecision, which is one second. A 300ms TTL makes exp equal iat, so
// the token is expired the instant it is signed, and nothing reports it: the call succeeds, the token looks
// normal, and every request with it is a 401.
func TestASubSecondTTLIsRefused(t *testing.T) {
	for _, ttl := range []time.Duration{0, time.Millisecond, 300 * time.Millisecond, 999 * time.Millisecond} {
		_, err := IssueAccess(secret, 1, "a@b.c", ttl)
		require.ErrorIs(t, err, ErrTTLTooShort, "ttl %v", ttl)
	}

	// One second is the floor and it works.
	token, err := IssueAccess(secret, 1, "a@b.c", time.Second)
	require.NoError(t, err)

	_, err = ParseAccess(secret, token)
	require.NoError(t, err)
}

// TestTheTruncationIsWhatTheLibraryDoes shows the mechanism rather than asserting the wrapper.
//
// Building the claims by hand with a sub-second gap produces a token that parses as expired, which is the
// behaviour IssueAccess now refuses to produce.
//
// # Why `now` is truncated first, and why the first version of this failed in CI
//
// `time.Now()` and `time.Now().Add(300ms)` truncate to the same second only when the current sub-second part is
// below 700ms. Land on 12:00:00.800 and they truncate to 12:00:00 and 12:00:01, which is a VALID token with a
// one-second life.
//
// So a 300ms TTL is not reliably zero. It is zero about 70% of the time and one second the rest, which is worse
// than always broken: it works on your machine and fails at random in production. CI caught it as a flake in
// this very test, which is a fair demonstration of the point.
//
// Truncating `now` to the second first makes the collision certain, so the test asserts the mechanism rather
// than the roll of a clock.
func TestTheTruncationIsWhatTheLibraryDoes(t *testing.T) {
	require.Equal(t, time.Second, jwt.TimePrecision, "if this ever changes, the guard in IssueAccess can go")

	now := time.Now().Truncate(time.Second)

	doomed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "1",
			Issuer:    Issuer,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(300 * time.Millisecond)),
		},
	}).SignedString(secret)
	require.NoError(t, err)

	_, err = ParseAccess(secret, doomed)
	require.ErrorIs(t, err, jwt.ErrTokenExpired,
		"exp and iat truncated to the same second, so the token was born expired")

	// The other half of the coin flip, made explicit: 800ms past a second boundary, a 300ms TTL crosses into
	// the next second and produces a token that lives for a whole one.
	lucky := now.Add(800 * time.Millisecond)

	valid, err := jwt.NewWithClaims(jwt.SigningMethodHS256, Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "1",
			Issuer:    Issuer,
			IssuedAt:  jwt.NewNumericDate(lucky),
			ExpiresAt: jwt.NewNumericDate(lucky.Add(300 * time.Millisecond)),
		},
	}).SignedString(secret)
	require.NoError(t, err)

	claims, err := ParseAccess(secret, valid)
	require.NoError(t, err, "the same 300ms TTL, 800ms into a second, is a one-second token")
	require.Equal(t, int64(1), claims.ExpiresAt.Unix()-claims.IssuedAt.Unix(),
		"which is why a sub-second TTL is refused rather than rounded: the result depends on the clock")
}
