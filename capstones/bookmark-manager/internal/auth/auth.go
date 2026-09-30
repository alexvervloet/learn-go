// Package auth is the two-token scheme: a short stateless access token and a long stateful refresh token.
//
// # Why two tokens rather than one
//
// One long-lived token cannot be revoked, and one short-lived token makes a user log in every fifteen minutes.
// The split gives both: the access token is stateless so no request costs a database lookup, and the refresh
// token is a row so a logout takes effect immediately.
//
// The url-shortener capstone in this repository uses one token and says so. This one is what a service with
// real sessions needs, and the difference is worth seeing side by side.
//
// # Rotation, and what it detects
//
// Every refresh CONSUMES the token and issues a new one. So a token is valid exactly once, and a second use
// means something has a copy it should not: either the token was stolen, or the legitimate client's response
// was lost and it retried.
//
// The two are indistinguishable, so the safe response is the same for both: revoke the whole family. A user
// whose refresh was lost logs in again; an attacker with a stolen token gets one use before the real user's
// next refresh kills the session. Without rotation, a stolen refresh token is a session for weeks and nothing
// notices.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

// Cost is bcrypt's work factor in production.
const Cost = 12

// TestCost is bcrypt's minimum, for tests.
const TestCost = bcrypt.MinCost

// ErrWrongPassword is returned for any credential failure, so the caller cannot tell them apart.
var ErrWrongPassword = errors.New("auth: email or password is wrong")

// Hash produces a bcrypt hash.
func Hash(password string, cost int) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(password), cost)
	if err != nil {
		return "", fmt.Errorf("auth: hash: %w", err)
	}

	return string(b), nil
}

// Verify checks a password.
func Verify(hash, password string) error {
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
		return ErrWrongPassword
	}

	return nil
}

// ErrWeakPassword is returned by ValidatePassword.
var ErrWeakPassword = errors.New("auth: password is too weak")

// ValidatePassword enforces length rather than character classes.
func ValidatePassword(password string) error {
	const (
		minLen = 12
		maxLen = 72 // bcrypt's ceiling, in BYTES: a password of emoji reaches it at 18 characters
	)

	if len(password) < minLen {
		return fmt.Errorf("%w: needs at least %d characters, got %d", ErrWeakPassword, minLen, len(password))
	}

	if len(password) > maxLen {
		return fmt.Errorf("%w: bcrypt ignores anything past %d bytes, and this is %d",
			ErrWeakPassword, maxLen, len(password))
	}

	distinct := map[rune]bool{}
	for _, r := range password {
		distinct[r] = true
	}

	if len(distinct) < 4 {
		return fmt.Errorf("%w: only %d distinct characters", ErrWeakPassword, len(distinct))
	}

	if strings.TrimFunc(password, unicode.IsSpace) == "" {
		return fmt.Errorf("%w: whitespace only", ErrWeakPassword)
	}

	return nil
}

// Issuer identifies this service in a token.
const Issuer = "bookmark-manager"

// Claims is what an access token carries.
type Claims struct {
	jwt.RegisteredClaims

	Email string `json:"email"`
}

// UserID reads the subject as a number.
func (c Claims) UserID() (int64, error) {
	id, err := strconv.ParseInt(c.Subject, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("auth: subject %q is not a user id: %w", c.Subject, err)
	}

	return id, nil
}

// ErrTTLTooShort is returned for a TTL a JWT cannot represent.
var ErrTTLTooShort = errors.New("auth: a token TTL below one second truncates to zero")

// IssueAccess mints a short-lived access token.
//
// # A JWT cannot express a sub-second TTL
//
// `exp` and `iat` are NumericDate, and jwt/v5 truncates every date to jwt.TimePrecision, which is one SECOND.
// So `now.Add(300 * time.Millisecond)` and `now` truncate to the same value, `exp` equals `iat`, and the token
// is expired the instant it is signed.
//
// Nothing reports this. The call succeeds, the token looks normal, and every request with it is a 401. A test
// with a 300ms TTL found it here, and the same shape in production would be a service that mints tokens nobody
// can use.
//
// Refusing outright rather than silently rounding up, because a caller asking for 300ms wants something this
// format cannot give and should be told.
func IssueAccess(secret []byte, userID int64, email string, ttl time.Duration) (string, error) {
	if ttl < time.Second {
		return "", fmt.Errorf("%w: got %v", ErrTTLTooShort, ttl)
	}

	now := time.Now()

	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   strconv.FormatInt(userID, 10),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
			Issuer:    Issuer,

			// A jti, so a token is identifiable in a log without printing the whole thing. The token itself
			// is a credential and does not belong in a log line; its id does.
			ID: randomID(),
		},
		Email: email,
	}

	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(secret)
	if err != nil {
		return "", fmt.Errorf("auth: sign: %w", err)
	}

	return signed, nil
}

// ErrInvalidToken is returned for anything that is not a valid, current token from this service.
var ErrInvalidToken = errors.New("auth: token is not valid")

// ParseAccess validates an access token.
//
// WithValidMethods pins the algorithm, which is the whole defence against alg=none and algorithm confusion.
// It is one line and it is the difference between a JWT and a base64 string anyone can write.
func ParseAccess(secret []byte, token string) (*Claims, error) {
	parsed, err := jwt.ParseWithClaims(token, &Claims{},
		func(*jwt.Token) (any, error) { return secret, nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(Issuer),
		jwt.WithExpirationRequired(),
	)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidToken, err)
	}

	claims, ok := parsed.Claims.(*Claims)
	if !ok || !parsed.Valid {
		return nil, ErrInvalidToken
	}

	return claims, nil
}

// RefreshTokenBytes is how much entropy a refresh token carries.
//
// 32 bytes is 256 bits. That is not a round number chosen for looks: it is more entropy than the HMAC key, so
// guessing a valid token is not the weak link, and it is the same size as the SHA-256 it hashes to.
const RefreshTokenBytes = 32

// NewRefreshToken returns a token and its hash.
//
// # The token is never stored
//
// The caller sends the token to the client and stores the HASH. A stolen database is then a set of hashes,
// which cannot be replayed.
//
// # base64.RawURLEncoding
//
// URL-safe, because the token travels in a JSON body today and might travel in a URL tomorrow. Raw, meaning no
// `=` padding, because the padding carries no information and is a character that needs escaping in exactly
// the places a token gets put.
func NewRefreshToken() (token string, hash []byte, err error) {
	raw := make([]byte, RefreshTokenBytes)

	if _, err := rand.Read(raw); err != nil {
		return "", nil, fmt.Errorf("auth: read randomness: %w", err)
	}

	sum := sha256.Sum256(raw)

	return base64.RawURLEncoding.EncodeToString(raw), sum[:], nil
}

// HashRefreshToken recomputes the hash for a lookup.
//
// # Why the lookup needs no constant-time comparison
//
// The database compares hashes, in an index lookup, and the attacker controls the TOKEN, not the hash. A timing
// leak there would tell them how many leading bytes of SHA-256(guess) match a stored hash, and that does not
// shorten anything: extending the match by a byte means finding a new input whose hash has that longer prefix,
// which is a fresh brute-force search each time. The byte-at-a-time attack that makes constant-time comparison
// matter for a password or an HMAC does not work through a hash. What protects the token is its 256 random
// bits, and storing only the hash means a database leak does not leak live tokens.
func HashRefreshToken(token string) ([]byte, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return nil, fmt.Errorf("%w: not a refresh token", ErrInvalidToken)
	}

	if len(raw) != RefreshTokenBytes {
		return nil, fmt.Errorf("%w: refresh token is %d bytes, want %d", ErrInvalidToken, len(raw), RefreshTokenBytes)
	}

	sum := sha256.Sum256(raw)

	return sum[:], nil
}

// randomID returns a short random identifier for a jti.
func randomID() string {
	b := make([]byte, 12)

	if _, err := rand.Read(b); err != nil {
		// crypto/rand failing means the OS entropy source is gone, which is not a condition any caller can
		// do anything about. A jti is not load-bearing, so an empty one is better than a panic.
		return ""
	}

	return base64.RawURLEncoding.EncodeToString(b)
}
