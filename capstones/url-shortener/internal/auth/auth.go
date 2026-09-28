// Package auth is passwords and tokens.
//
// # The two halves
//
// A password is stored so that stealing the database does not give anyone a password. A token is issued so that
// the next request does not need the password. They are separate problems with separate failure modes and the
// only thing they share is that both are easy to get subtly wrong in a way nothing reports.
package auth

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

// Cost is bcrypt's work factor.
//
// # What the number means
//
// It is a base-2 exponent: cost 12 is 2^12 = 4096 rounds, and each increment DOUBLES the time. The default in
// x/crypto is 10. Twelve is roughly 250ms on a current machine, which is slow enough to make offline cracking
// expensive and fast enough that a login does not feel broken.
//
// The number is a moving target by design. It should be raised as hardware gets faster, and because bcrypt
// stores the cost inside the hash, raising it does not invalidate existing hashes: they keep verifying at their
// old cost, and the right time to re-hash is the next successful login, when the plaintext is in hand.
const Cost = 12

// TestCost is bcrypt's minimum, for tests.
//
// # Why a test needs this
//
// At cost 12 each hash is a quarter of a second. A test suite with thirty logins in it spends eight seconds
// hashing, and someone eventually "fixes" that by lowering the production cost. Exporting the test cost
// separately makes the fast path explicit and keeps it out of the service.
const TestCost = bcrypt.MinCost

// ErrWrongPassword is returned when a password does not match.
//
// # Why one error and not two
//
// A caller must not be able to tell "no such user" from "wrong password". The difference turns a login form
// into a way to enumerate who has an account, which is the first step of a credential-stuffing run. The store
// returns the same error for both and this is it.
var ErrWrongPassword = errors.New("auth: email or password is wrong")

// Hash produces a bcrypt hash at the given cost.
func Hash(password string, cost int) (string, error) {
	// bcrypt silently TRUNCATES at 72 bytes. A passphrase longer than that has its tail ignored, so two
	// different long passwords can verify against one hash. x/crypto returns an error rather than truncating,
	// which is the better behaviour and means the length check belongs in validation rather than here.
	b, err := bcrypt.GenerateFromPassword([]byte(password), cost)
	if err != nil {
		return "", fmt.Errorf("auth: hash: %w", err)
	}

	return string(b), nil
}

// Verify checks a password against a hash.
func Verify(hash, password string) error {
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
		return ErrWrongPassword
	}

	return nil
}

// CostOf reads the cost out of a hash.
//
// Useful for deciding whether to re-hash on login, and useful in a test to prove the cost is what the config
// said rather than the library default.
func CostOf(hash string) (int, error) {
	cost, err := bcrypt.Cost([]byte(hash))
	if err != nil {
		return 0, fmt.Errorf("auth: cost: %w", err)
	}

	return cost, nil
}

// ErrWeakPassword is returned by ValidatePassword.
var ErrWeakPassword = errors.New("auth: password is too weak")

// ValidatePassword enforces the rules.
//
// # Length first, character classes barely
//
// NIST's current guidance is to require length and to stop requiring a mix of character classes, because the
// classes push people towards Password1! and a passphrase of four words beats it by a wide margin. The one
// class rule kept here is "not all the same character", which catches aaaaaaaaaaaa without pushing anyone
// anywhere.
//
// The 72-byte ceiling is bcrypt's, and it is a BYTE limit rather than a character one: a password of emoji
// hits it at 18 characters.
func ValidatePassword(password string) error {
	const (
		minLen = 12
		maxLen = 72
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

// Claims is what a token carries.
type Claims struct {
	jwt.RegisteredClaims

	// Email is here for logging and for a UI that wants to greet someone. It is NOT the identity: the subject
	// is, because an email can change and a user id cannot.
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

// Issue mints a token.
//
// # What is in it and what is not
//
// The subject, the email, an expiry and an issued-at. No role, no permissions, no display name. Everything in a
// token is a snapshot from the moment it was issued, so anything that can change is stale for as long as the
// token lives, and a revoked admin is an admin until their token expires.
//
// That is the fundamental trade of a stateless token: no lookup per request, and no way to take it back. The
// TTL is how long you are willing to be wrong.
func Issue(secret []byte, userID int64, email string, ttl time.Duration) (string, error) {
	now := time.Now()

	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   strconv.FormatInt(userID, 10),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),

			// NotBefore, set to now, so a token cannot be used before it was issued. That sounds vacuous and
			// matters when clocks disagree: without it, a token minted on a server whose clock is ahead is
			// accepted by one whose clock is behind, which is correct, and there is no symmetric protection.
			NotBefore: jwt.NewNumericDate(now),

			Issuer: Issuer,
		},
		Email: email,
	}

	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(secret)
	if err != nil {
		return "", fmt.Errorf("auth: sign: %w", err)
	}

	return signed, nil
}

// Issuer identifies this service in a token.
//
// Checking it on parse means a token minted by a different service sharing the same secret is rejected. That
// should never happen and it does, because secrets get copied between environments.
const Issuer = "url-shortener"

// ErrInvalidToken is returned for anything that is not a valid, current token from this service.
var ErrInvalidToken = errors.New("auth: token is not valid")

// Parse validates a token and returns its claims.
//
// # The algorithm has to be pinned
//
// This is the single most important line in the package. A JWT names its own algorithm in the header, so a
// parser that trusts the header will:
//
//   - accept alg "none" and skip verification entirely, if the library allows it; or
//   - accept alg RS256 with the HMAC secret as the "public key", so an attacker who knows the public key can
//     sign tokens with it.
//
// jwt/v5 refuses the first by default. The second is what WithValidMethods prevents, and a parser without it
// is the textbook JWT vulnerability.
func Parse(secret []byte, token string) (*Claims, error) {
	parsed, err := jwt.ParseWithClaims(token, &Claims{},
		func(t *jwt.Token) (any, error) {
			return secret, nil
		},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(Issuer),

		// The library checks exp and nbf by default. Saying so explicitly documents that the check happens
		// here rather than in a handler, and gives the leeway a place to live if clock skew ever needs one.
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
