// Package jwtauth is JWTs, the attacks they invite, and role-based access on top.
//
// # What a JWT is and is not
//
// Three base64url segments separated by dots: header, claims, signature. The first two are NOT ENCRYPTED. They
// are encoded, which anyone can reverse, so a JWT is a signed public document. Putting an email address in one
// is fine; putting anything you would not print in a log is not.
//
// What the signature buys is integrity: the holder cannot change the claims. That is all it buys.
//
// # The three attacks, in the order they were used in anger
//
//	alg=none          the spec has a "none" algorithm meaning unsigned. A library that honours the
//	                  TOKEN'S header will accept a forged token with the signature removed. Fixed
//	                  by naming the acceptable algorithm at the verifier, never reading it from the
//	                  token.
//	algorithm         a server that verifies RS256 with the PUBLIC key can be handed an HS256
//	  confusion       token signed WITH that public key as an HMAC secret. The public key is
//	                  public, so anyone can forge. Same fix.
//	kid traversal     the header's kid names a key. A verifier that treats it as a file path or an
//	                  SQL value gets ../../etc/passwd or a UNION. Treat kid as a lookup key in a
//	                  fixed map and nothing else.
//
// golang-jwt/jwt/v5 requires the expected methods up front, which closes the first two by construction, and
// TestForgedTokensAreRejected measures that rather than trusting it.
//
// # The thing everyone gets wrong
//
// YOU CANNOT REVOKE A JWT. The whole point is that verification needs no state, which means nothing to
// consult, which means nothing to change your mind with. A user who logs out, has their account suspended, or
// has their roles reduced keeps their working token until it expires.
//
// The three answers, in order of how much they give up:
//
//	short expiry plus a refresh token. The access token lives 5 to 15 minutes, the refresh token
//	  is a random opaque string stored in the database and can be revoked. This is the standard
//	  answer and the revocation window is the access token's lifetime.
//	a denylist of revoked jti values, in Redis, checked on every request. Works, and gives up
//	  statelessness, which was the reason for the JWT.
//	do not use JWTs. A random session id in a cookie, looked up in Redis, is simpler, revocable
//	  immediately, and one lookup slower. For a first-party web app it is usually the better
//	  choice, and saying so is not fashionable.
//
// # HS256 or RS256
//
// HS256 is one shared secret: whoever can verify can also sign. Fine when the same service issues and
// verifies. RS256 splits them: a private key signs, a public key verifies, so twelve services can verify
// tokens without any of them being able to mint one. That is the reason to pay for it, and the cost is
// measured in this package's benchmarks.
package jwtauth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Errors a caller distinguishes.
//
// Expired is separate from invalid because the client's response differs: an expired token means "refresh and
// retry", and an invalid one means "log in again". Collapsing them makes a refresh flow impossible.
var (
	ErrNoToken       = errors.New("no bearer token")
	ErrExpired       = errors.New("token expired")
	ErrInvalidToken  = errors.New("invalid token")
	ErrWrongAudience = errors.New("token was not issued for this audience")
	ErrUnknownKey    = errors.New("unknown key id")
	ErrForbidden     = errors.New("insufficient role")
)

// Claims is what this service puts in a token.
//
// jwt.RegisteredClaims is embedded rather than reimplemented, because it carries exp, nbf, iat, iss, aud, sub
// and jti with the right JSON tags and the right validation, and getting `exp` wrong by writing it as an
// RFC 3339 string is a real and common mistake: the spec says it is a NumericDate, a count of seconds.
type Claims struct {
	jwt.RegisteredClaims

	// Roles is what the RBAC checks. A slice, because a user has several, and in the token rather
	// than looked up per request because that is the point of a JWT.
	//
	// The cost of putting them here is the revocation problem above: a user demoted from admin stays
	// an admin until the token expires. For an access token measured in minutes that is usually
	// acceptable and it has to be a decision rather than an accident.
	Roles []string `json:"roles,omitempty"`

	// Email is here as an example of a claim that is fine to include and would not be if the token
	// were treated as secret. It is base64, not encryption.
	Email string `json:"email,omitempty"`
}

// HasRole reports whether the claims carry a role.
func (c Claims) HasRole(role string) bool { return slices.Contains(c.Roles, role) }

// HasAnyRole reports whether the claims carry at least one of the roles.
//
// Any rather than All, because that is what an authorisation check almost always means: "admin or owner", not
// "admin and owner". Having both functions invites picking the wrong one, so there is one, named for what it
// does.
func (c Claims) HasAnyRole(roles ...string) bool {
	for _, role := range roles {
		if c.HasRole(role) {
			return true
		}
	}
	return false
}

// Issuer mints and verifies tokens.
type Issuer struct {
	// Method is the signing algorithm, fixed at construction. This field is the whole defence
	// against alg=none and algorithm confusion: the verifier states what it accepts and never reads
	// it from the token.
	Method jwt.SigningMethod

	// signKey and verifyKeys are different types for HS256 and RS256, so they are `any` and the
	// constructors below are what keep them consistent.
	signKey any

	// verifyKeys is keyed by kid, so a rotation can accept both the old and the new key. A MAP, and
	// kid is only ever a key in it: never a file path, never interpolated into SQL.
	verifyKeys map[string]any

	// activeKID names the key new tokens are signed with.
	activeKID string

	Issuer   string
	Audience string
	TTL      time.Duration

	// Now is injectable, so the expiry tests do not sleep.
	Now func() time.Time
}

// NewHS256 builds an issuer with a shared secret.
//
// The secret must be at least 32 bytes. HS256 is HMAC-SHA256, so a short secret is brute-forceable offline:
// anyone with one token can try secrets until the signature matches, at millions per second, and "password123"
// takes no time at all. The check is here rather than in a comment because a constructor that accepts a weak
// key produces a service nobody notices is broken.
func NewHS256(secret []byte, kid string) (*Issuer, error) {
	if len(secret) < 32 {
		return nil, fmt.Errorf("jwtauth: HS256 secret is %d bytes, want at least 32", len(secret))
	}

	return &Issuer{
		Method:     jwt.SigningMethodHS256,
		signKey:    secret,
		verifyKeys: map[string]any{kid: secret},
		activeKID:  kid,
		TTL:        15 * time.Minute,
		Now:        time.Now,
	}, nil
}

// NewRS256 builds an issuer with an RSA key pair.
func NewRS256(key *rsa.PrivateKey, kid string) *Issuer {
	return &Issuer{
		Method:     jwt.SigningMethodRS256,
		signKey:    key,
		verifyKeys: map[string]any{kid: &key.PublicKey},
		activeKID:  kid,
		TTL:        15 * time.Minute,
		Now:        time.Now,
	}
}

// AddVerifyKey registers an additional key for verification only.
//
// This is how rotation works, and it is the same shape as the webhook secret list: during the overlap both
// keys verify, new tokens are signed with the active one, and the old key is removed once every token signed
// with it has expired. Which is why the removal can be scheduled: it is TTL after the switch, exactly.
func (i *Issuer) AddVerifyKey(kid string, key any) {
	if i.verifyKeys == nil {
		i.verifyKeys = map[string]any{}
	}
	i.verifyKeys[kid] = key
}

// Rotate registers a new key and starts signing with it.
//
// # Why this takes both keys and SetActiveKID does not exist
//
// The first version of this package had AddVerifyKey plus SetActiveKID, and a test caught it: SetActiveKID
// changed which kid went in the header and did not change the key used to SIGN, so every new token was signed
// with the old key and labelled with the new one. Nothing verified it, and the failure was
// "signature is invalid" on a token this very service had just minted.
//
// The fix could have been to make SetActiveKID also set the signing key from the verify map. For HS256 that
// works, because the two are the same shared secret. For RS256 it cannot: the verify key is a PUBLIC key and
// the signing key is private, and there is no way to get from one to the other. An API that works for one
// algorithm and silently signs with the wrong key for the other is worse than one extra argument.
//
// So rotation takes both, and the asymmetry between HS256 and RS256 is visible at the call site instead of
// hidden in a method that means different things depending on the algorithm.
func (i *Issuer) Rotate(kid string, signKey, verifyKey any) error {
	if kid == "" {
		return errors.New("jwtauth: a key id is required")
	}
	if signKey == nil || verifyKey == nil {
		return errors.New("jwtauth: rotation needs both a signing and a verification key")
	}

	if i.verifyKeys == nil {
		i.verifyKeys = map[string]any{}
	}

	i.verifyKeys[kid] = verifyKey
	i.signKey = signKey
	i.activeKID = kid

	return nil
}

// RemoveVerifyKey drops a key, which is safe exactly TTL after a Rotate away from it.
//
// Not "after the deploy" and not "next week": TTL, because that is the longest a token signed with the old key
// can still be valid. The arithmetic is available, so the cleanup can be scheduled rather than remembered.
func (i *Issuer) RemoveVerifyKey(kid string) error {
	if kid == i.activeKID {
		return fmt.Errorf("jwtauth: %q is the active key; rotate away from it first", kid)
	}

	if _, ok := i.verifyKeys[kid]; !ok {
		return fmt.Errorf("%w: %q", ErrUnknownKey, kid)
	}

	delete(i.verifyKeys, kid)

	return nil
}

// ActiveKID reports which key new tokens are signed with.
func (i *Issuer) ActiveKID() string { return i.activeKID }

// VerifyKIDs lists every key that will verify, for a log line at startup.
//
// Worth having: "which keys does this process accept" is the first question when a rolling deploy starts
// rejecting tokens, and the answer is otherwise only in the code.
func (i *Issuer) VerifyKIDs() []string {
	kids := make([]string, 0, len(i.verifyKeys))
	for kid := range i.verifyKeys {
		kids = append(kids, kid)
	}
	slices.Sort(kids)
	return kids
}

func (i *Issuer) now() time.Time {
	if i.Now == nil {
		return time.Now()
	}
	return i.Now()
}

// Mint issues a token.
func (i *Issuer) Mint(subject string, roles []string, email string) (string, error) {
	now := i.now()

	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   subject,
			Issuer:    i.Issuer,
			ExpiresAt: jwt.NewNumericDate(now.Add(i.TTL)),
			IssuedAt:  jwt.NewNumericDate(now),

			// NotBefore, set to now. Without it a token is valid from the beginning of time,
			// which matters for a token minted in advance.
			NotBefore: jwt.NewNumericDate(now),

			// A unique id, so a denylist is possible later even if one is not used now.
			// Adding jti after the fact means every token already in circulation has none.
			ID: randomID(),
		},
		Roles: roles,
		Email: email,
	}

	if i.Audience != "" {
		claims.Audience = jwt.ClaimStrings{i.Audience}
	}

	token := jwt.NewWithClaims(i.Method, claims)

	// The kid goes in the header, which is the one thing a verifier reads from the token before
	// verifying. It is a lookup key and nothing else.
	token.Header["kid"] = i.activeKID

	signed, err := token.SignedString(i.signKey)
	if err != nil {
		return "", fmt.Errorf("signing a token for %q: %w", subject, err)
	}

	return signed, nil
}

// Verify parses and validates a token.
//
// The four options passed to jwt.ParseWithClaims are each closing something:
//
//	WithValidMethods    the alg=none and confusion fix. Only this algorithm is accepted, whatever
//	                    the token's header says.
//	WithIssuer          so a token from a different system with the same key is rejected.
//	WithAudience        so an access token for service A cannot be replayed at service B.
//	WithExpirationRequired  a token with NO exp claim is otherwise valid forever, and v5 accepts
//	                    one by default.
//
// The last is the surprising one. jwt/v5 validates exp when present and does not require it, so a token
// missing the claim entirely passes. One option turns that into a rejection.
func (i *Issuer) Verify(token string) (Claims, error) {
	var claims Claims

	opts := []jwt.ParserOption{
		jwt.WithValidMethods([]string{i.Method.Alg()}),
		jwt.WithExpirationRequired(),
		jwt.WithTimeFunc(i.now),
	}

	if i.Issuer != "" {
		opts = append(opts, jwt.WithIssuer(i.Issuer))
	}
	if i.Audience != "" {
		opts = append(opts, jwt.WithAudience(i.Audience))
	}

	parsed, err := jwt.ParseWithClaims(token, &claims, i.keyFunc, opts...)
	if err != nil {
		// jwt/v5's errors are joinable sentinels, so errors.Is works and the expiry case can be
		// separated. That is worth checking rather than assuming, and the test does.
		switch {
		case errors.Is(err, jwt.ErrTokenExpired):
			return claims, fmt.Errorf("%w: %w", ErrExpired, err)
		case errors.Is(err, jwt.ErrTokenInvalidAudience):
			return claims, fmt.Errorf("%w: %w", ErrWrongAudience, err)
		default:
			return claims, fmt.Errorf("%w: %w", ErrInvalidToken, err)
		}
	}

	if !parsed.Valid {
		// Belt and braces: ParseWithClaims returns an error for an invalid token, so this is
		// unreachable. It is here because the v4 API could return a token with Valid false and a
		// nil error in some paths, and code carried over from v4 checked only the error.
		return claims, ErrInvalidToken
	}

	return claims, nil
}

// keyFunc looks up the verification key by kid.
//
// It is a method so it can close over the issuer's map, and the map is the only thing kid ever touches. A
// keyFunc that opens a file named by kid, or queries a database with it, is a path traversal or an injection,
// and both have been found in the wild.
func (i *Issuer) keyFunc(token *jwt.Token) (any, error) {
	kid, ok := token.Header["kid"].(string)
	if !ok {
		// No kid. Fall back to the active key, because a token minted before kid was introduced
		// still has to verify during the migration. A verifier that rejects tokens without a kid
		// is correct and cannot be deployed without logging everyone out.
		return i.verifyKeys[i.activeKID], nil
	}

	key, ok := i.verifyKeys[kid]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownKey, kid)
	}

	return key, nil
}

// BearerToken pulls the token out of an Authorization header.
//
// Case-insensitive on the scheme, because RFC 7235 says the scheme is case-insensitive and clients send
// "bearer", "Bearer" and occasionally "BEARER". A strict prefix check works until the first client that does
// not match, and then the failure is a 401 with no explanation.
func BearerToken(r *http.Request) (string, error) {
	header := r.Header.Get("Authorization")
	if header == "" {
		return "", ErrNoToken
	}

	scheme, token, ok := strings.Cut(header, " ")
	if !ok || !strings.EqualFold(scheme, "bearer") || token == "" {
		return "", fmt.Errorf("%w: %q is not a bearer token", ErrNoToken, header)
	}

	return strings.TrimSpace(token), nil
}

// claimsKey is the context key.
//
// An unexported struct{} type, so nothing outside this package can collide with it or read the claims out
// without going through FromContext. A string key would collide with any other package using the same string,
// silently.
type claimsKey struct{}

// WithClaims puts claims in a context.
func WithClaims(ctx context.Context, claims Claims) context.Context {
	return context.WithValue(ctx, claimsKey{}, claims)
}

// FromContext reads the claims a middleware put there.
//
// Returns ok rather than a zero value alone, because a handler behind the middleware can assume they are there
// and a handler that is not must not silently treat an unauthenticated request as one with no roles.
func FromContext(ctx context.Context) (Claims, bool) {
	claims, ok := ctx.Value(claimsKey{}).(Claims)
	return claims, ok
}

// Authenticate verifies the bearer token and puts the claims in the context.
//
// The WWW-Authenticate header on a 401 is not decoration: RFC 6750 defines it for bearer tokens, and the
// error and error_description parameters are what let a client tell "refresh your token" from "your token is
// nonsense". Most implementations omit it and then the client has to guess.
func (i *Issuer) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, err := BearerToken(r)
		if err != nil {
			unauthorized(w, "invalid_request", "a bearer token is required")
			return
		}

		claims, err := i.Verify(token)
		if err != nil {
			switch {
			case errors.Is(err, ErrExpired):
				// The one case a client can act on automatically.
				unauthorized(w, "invalid_token", "the access token has expired")
			default:
				unauthorized(w, "invalid_token", "the access token is not valid")
			}
			return
		}

		next.ServeHTTP(w, r.WithContext(WithClaims(r.Context(), claims)))
	})
}

// RequireRole is the authorisation half, and it is deliberately separate from Authenticate.
//
// Two middlewares rather than one with a roles argument, because authentication and authorisation fail
// differently (401 against 403) and because most routes need the first and only some need the second. Merging
// them produces a middleware that has to be configured with "no roles required", which is a value that means
// "skip half of this function".
func RequireRole(roles ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, ok := FromContext(r.Context())
			if !ok {
				// Not authenticated. A 500 rather than a 401, because reaching a
				// RequireRole with no claims in the context means the router is wired
				// wrongly: Authenticate did not run. Returning 401 would hide the bug
				// behind something that looks like a normal auth failure.
				http.Error(w, "authorisation middleware reached without authentication",
					http.StatusInternalServerError)
				return
			}

			if !claims.HasAnyRole(roles...) {
				// 403, not 401. The client authenticated fine and is not allowed, so
				// retrying with a fresh token changes nothing and the distinction is what
				// stops a client looping.
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func unauthorized(w http.ResponseWriter, code, description string) {
	w.Header().Set("WWW-Authenticate",
		fmt.Sprintf(`Bearer error=%q, error_description=%q`, code, description))

	http.Error(w, description, http.StatusUnauthorized)
}

// randomID returns a random jti.
//
// crypto/rand, not math/rand. A predictable jti lets an attacker guess the ids of tokens they have not seen,
// which matters the moment a denylist exists: guessing ids means revoking other people's sessions.
func randomID() string {
	b := make([]byte, 16)

	// crypto/rand.Read in Go 1.24+ never returns an error; it panics if the system source fails,
	// which is the right behaviour for a source of randomness that cannot fail safely. Before 1.24
	// the error had to be checked, and code that ignored it could produce all-zero ids.
	_, _ = rand.Read(b)

	return base64.RawURLEncoding.EncodeToString(b)
}
