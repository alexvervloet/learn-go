package jwtauth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var (
	secret    = []byte("a-test-secret-that-is-long-enough-32")
	fixedTime = time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)
)

func newHS256(t *testing.T) *Issuer {
	t.Helper()

	i, err := NewHS256(secret, "k1")
	if err != nil {
		t.Fatal(err)
	}

	i.Issuer = "learn-go"
	i.Audience = "api"
	i.Now = func() time.Time { return fixedTime }

	return i
}

func newRS256(t *testing.T) (*Issuer, *rsa.PrivateKey) {
	t.Helper()

	// 2048 bits, generated per test. Generation is the slow part (tens of milliseconds), which is
	// fine here and is why the benchmarks generate once.
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}

	i := NewRS256(key, "k1")
	i.Issuer = "learn-go"
	i.Audience = "api"
	i.Now = func() time.Time { return fixedTime }

	return i, key
}

func TestMintAndVerify(t *testing.T) {
	for _, tc := range []struct {
		name string
		make func(*testing.T) *Issuer
	}{
		{"HS256", newHS256},
		{"RS256", func(t *testing.T) *Issuer { i, _ := newRS256(t); return i }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			i := tc.make(t)

			token, err := i.Mint("user-42", []string{"admin", "billing"}, "a@example.test")
			if err != nil {
				t.Fatal(err)
			}

			t.Logf("token is %d bytes, %d segments", len(token), len(strings.Split(token, ".")))

			claims, err := i.Verify(token)
			if err != nil {
				t.Fatal(err)
			}

			if claims.Subject != "user-42" {
				t.Errorf("subject is %q", claims.Subject)
			}
			if !claims.HasRole("admin") || !claims.HasRole("billing") {
				t.Errorf("roles are %v", claims.Roles)
			}
			if claims.HasRole("nope") {
				t.Error("HasRole found a role that is not there")
			}
			if !claims.HasAnyRole("nope", "billing") {
				t.Error("HasAnyRole missed a role that is there")
			}
			if claims.ID == "" {
				t.Error("no jti")
			}
			if claims.ExpiresAt.Time.Sub(fixedTime) != 15*time.Minute {
				t.Errorf("expiry is %v after now, want 15m",
					claims.ExpiresAt.Time.Sub(fixedTime))
			}
		})
	}
}

// TestTheClaimsAreReadableByAnyone is the property people get wrong.
func TestTheClaimsAreReadableByAnyone(t *testing.T) {
	i := newHS256(t)

	token, err := i.Mint("user-42", []string{"admin"}, "secret@example.test")
	if err != nil {
		t.Fatal(err)
	}

	// No key, no library, no verification. Just base64.
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("expected 3 segments, got %d", len(parts))
	}

	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("the claims, decoded with no key at all: %s", raw)

	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}

	if decoded["email"] != "secret@example.test" {
		t.Errorf("the email did not decode: %v", decoded["email"])
	}

	// And the header, which is where the kid and alg live.
	raw, err = base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("the header: %s", raw)

	t.Log("a JWT is signed, not encrypted. Anything in the claims is public, so the rule is " +
		"simple: nothing in a token that you would not put in a log line.")
}

// TestForgedTokensAreRejected is the three attacks, each attempted.
func TestForgedTokensAreRejected(t *testing.T) {
	i := newHS256(t)

	valid, err := i.Mint("user-42", []string{"user"}, "")
	if err != nil {
		t.Fatal(err)
	}

	t.Run("alg=none", func(t *testing.T) {
		// The classic. Take a real token, rewrite the header to alg=none, escalate the role, and
		// drop the signature.
		parts := strings.Split(valid, ".")

		header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))

		claimsJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
		if err != nil {
			t.Fatal(err)
		}

		escalated := strings.Replace(string(claimsJSON), `"roles":["user"]`,
			`"roles":["admin"]`, 1)

		if escalated == string(claimsJSON) {
			t.Fatalf("the role rewrite did not apply to %s", claimsJSON)
		}

		forged := header + "." +
			base64.RawURLEncoding.EncodeToString([]byte(escalated)) + "."

		t.Logf("forged token: %s...", forged[:40])

		claims, err := i.Verify(forged)

		if err == nil {
			t.Fatalf("an unsigned token verified, with roles %v", claims.Roles)
		}

		t.Logf("rejected: %v", err)

		if !errors.Is(err, ErrInvalidToken) {
			t.Errorf("got %v, want ErrInvalidToken", err)
		}
	})

	t.Run("algorithm confusion", func(t *testing.T) {
		// The RS256 version: a server verifying RS256 with a public key is handed an HS256 token
		// signed with that public key as the HMAC secret. The public key is public.
		rs, key := newRS256(t)

		// The attacker's material: the public key, in the form a server would publish it.
		publicKeyPEM := publicKeyBytes(t, &key.PublicKey)

		// An HS256 token signed with the public key bytes as the secret.
		attacker := jwt.NewWithClaims(jwt.SigningMethodHS256, Claims{
			RegisteredClaims: jwt.RegisteredClaims{
				Subject:   "attacker",
				Issuer:    "learn-go",
				Audience:  jwt.ClaimStrings{"api"},
				ExpiresAt: jwt.NewNumericDate(fixedTime.Add(time.Hour)),
			},
			Roles: []string{"admin"},
		})
		attacker.Header["kid"] = "k1"

		forged, err := attacker.SignedString(publicKeyPEM)
		if err != nil {
			t.Fatal(err)
		}

		claims, err := rs.Verify(forged)

		if err == nil {
			t.Fatalf("an HS256 token verified against an RS256 verifier, roles %v", claims.Roles)
		}

		t.Logf("rejected: %v", err)
		t.Log("WithValidMethods is what closes this: the verifier states RS256 and never reads " +
			"alg from the token")
	})

	t.Run("kid is only a map key", func(t *testing.T) {
		// A kid that would be a path traversal or an injection if it were used as anything but a
		// lookup key.
		for _, kid := range []string{
			"../../../etc/passwd",
			"k1' OR '1'='1",
			"k1\x00k2",
			strings.Repeat("k", 10_000),
		} {
			token := jwt.NewWithClaims(jwt.SigningMethodHS256, Claims{
				RegisteredClaims: jwt.RegisteredClaims{
					Subject:   "attacker",
					Issuer:    "learn-go",
					Audience:  jwt.ClaimStrings{"api"},
					ExpiresAt: jwt.NewNumericDate(fixedTime.Add(time.Hour)),
				},
				Roles: []string{"admin"},
			})
			token.Header["kid"] = kid

			signed, err := token.SignedString(secret)
			if err != nil {
				t.Fatal(err)
			}

			_, err = i.Verify(signed)

			if err == nil {
				t.Errorf("a token with kid %q verified", truncate(kid))
				continue
			}

			if !errors.Is(err, ErrInvalidToken) {
				t.Errorf("kid %q gave %v", truncate(kid), err)
			}
		}

		t.Log("every one is a map miss, which is the whole defence. A keyFunc that opens a " +
			"file or runs a query with kid is where these become exploits.")
	})

	t.Run("tampered claims", func(t *testing.T) {
		parts := strings.Split(valid, ".")

		claimsJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
		if err != nil {
			t.Fatal(err)
		}

		escalated := strings.Replace(string(claimsJSON), `"roles":["user"]`,
			`"roles":["admin"]`, 1)

		// The original signature, over the original claims.
		forged := parts[0] + "." +
			base64.RawURLEncoding.EncodeToString([]byte(escalated)) + "." + parts[2]

		if _, err := i.Verify(forged); err == nil {
			t.Error("tampered claims verified with the original signature")
		} else {
			t.Logf("rejected: %v", err)
		}
	})
}

// TestExpiryIsRequiredNotJustValidated is the jwt/v5 default that surprised me.
func TestExpiryIsRequiredNotJustValidated(t *testing.T) {
	i := newHS256(t)

	// A token with NO exp claim.
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:  "forever",
			Issuer:   "learn-go",
			Audience: jwt.ClaimStrings{"api"},
		},
		Roles: []string{"admin"},
	})
	token.Header["kid"] = "k1"

	signed, err := token.SignedString(secret)
	if err != nil {
		t.Fatal(err)
	}

	// With WithExpirationRequired, which Verify passes: rejected.
	if _, err := i.Verify(signed); err == nil {
		t.Error("a token with no exp claim verified")
	} else {
		t.Logf("with WithExpirationRequired: %v", err)
	}

	// Without it, which is jwt/v5's DEFAULT: accepted, and valid forever.
	var claims Claims

	parsed, err := jwt.ParseWithClaims(signed, &claims,
		func(*jwt.Token) (any, error) { return secret, nil },
		jwt.WithValidMethods([]string{"HS256"}))

	if err != nil {
		t.Fatalf("the default parser rejected it, so this lesson is out of date: %v", err)
	}
	if !parsed.Valid {
		t.Fatal("the default parser marked it invalid")
	}

	t.Logf("without the option, the same token parses as VALID with roles %v and no expiry",
		claims.Roles)
	t.Log("jwt/v5 validates exp when it is present and does not require it. One option turns a " +
		"token that never expires into a rejection, and it is not the default.")
}

// TestExpiryAndNotBefore, both directions.
func TestExpiryAndNotBefore(t *testing.T) {
	i := newHS256(t)
	i.TTL = 15 * time.Minute

	token, err := i.Mint("user-42", []string{"user"}, "")
	if err != nil {
		t.Fatal(err)
	}

	clock := fixedTime
	i.Now = func() time.Time { return clock }

	for _, tc := range []struct {
		name   string
		offset time.Duration
		want   error
	}{
		{"immediately", 0, nil},
		{"14 minutes later", 14 * time.Minute, nil},
		{"16 minutes later", 16 * time.Minute, ErrExpired},
		{"a day later", 24 * time.Hour, ErrExpired},
		{"a minute before it was issued", -time.Minute, ErrInvalidToken},
	} {
		clock = fixedTime.Add(tc.offset)

		_, err := i.Verify(token)

		switch {
		case tc.want == nil && err != nil:
			t.Errorf("%s: %v", tc.name, err)
		case tc.want != nil && !errors.Is(err, tc.want):
			t.Errorf("%s: got %v, want %v", tc.name, err, tc.want)
		default:
			t.Logf("%-30s %s", tc.name, resultOf(err))
		}
	}

	t.Log("the last case is nbf: without it a token minted in advance is valid from the " +
		"beginning of time")

	// The expiry case has to be distinguishable, because a client's response differs: refresh and
	// retry, against log in again.
	clock = fixedTime.Add(time.Hour)

	_, err = i.Verify(token)

	if !errors.Is(err, ErrExpired) {
		t.Errorf("an expired token gave %v", err)
	}
	if errors.Is(err, ErrWrongAudience) {
		t.Error("an expired token matched ErrWrongAudience too, so the sentinels overlap")
	}
}

// TestAudienceStopsCrossServiceReplay.
func TestAudienceStopsCrossServiceReplay(t *testing.T) {
	// Two services sharing a secret, which is common and is exactly when audience matters.
	api, err := NewHS256(secret, "k1")
	if err != nil {
		t.Fatal(err)
	}
	api.Issuer = "learn-go"
	api.Audience = "api"
	api.Now = func() time.Time { return fixedTime }

	admin, err := NewHS256(secret, "k1")
	if err != nil {
		t.Fatal(err)
	}
	admin.Issuer = "learn-go"
	admin.Audience = "admin"
	admin.Now = func() time.Time { return fixedTime }

	token, err := api.Mint("user-42", []string{"user"}, "")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := api.Verify(token); err != nil {
		t.Fatalf("the issuing service rejected its own token: %v", err)
	}

	_, err = admin.Verify(token)

	if !errors.Is(err, ErrWrongAudience) {
		t.Errorf("the admin service accepted an api token: %v", err)
	}

	t.Logf("the admin service rejects it: %v", err)
	t.Log("same secret, same issuer, different audience. Without the aud check, any token " +
		"signed by anything sharing the key works everywhere.")
}

// TestKeyRotation is the same shape as the webhook secret rotation, and it caught an API bug.
//
// The first version of this package had AddVerifyKey plus SetActiveKID. This test failed with
// "the new token failed during the overlap: signature is invalid", on a token the service had just minted:
// SetActiveKID changed the kid in the header without changing the signing key. Rotate takes both, and the
// reason it has to is that for RS256 the verify key cannot be derived from the signing key or the reverse.
func TestKeyRotation(t *testing.T) {
	i := newHS256(t)

	oldToken, err := i.Mint("user-42", []string{"user"}, "")
	if err != nil {
		t.Fatal(err)
	}

	if got := i.VerifyKIDs(); len(got) != 1 || got[0] != "k1" {
		t.Errorf("VerifyKIDs is %v, want [k1]", got)
	}

	// Rotate: a new key, registered and made active for signing in one call.
	newSecret := []byte("the-second-secret-also-32-bytes!!")

	if err := i.Rotate("k2", newSecret, newSecret); err != nil {
		t.Fatal(err)
	}

	if i.ActiveKID() != "k2" {
		t.Errorf("the active kid is %q, want k2", i.ActiveKID())
	}
	if got := i.VerifyKIDs(); len(got) != 2 {
		t.Errorf("VerifyKIDs is %v, want both keys", got)
	}

	newToken, err := i.Mint("user-42", []string{"user"}, "")
	if err != nil {
		t.Fatal(err)
	}

	for name, token := range map[string]string{"old": oldToken, "new": newToken} {
		if _, err := i.Verify(token); err != nil {
			t.Errorf("the %s token failed during the overlap: %v", name, err)
		}
	}

	t.Logf("both verify during the overlap, accepting %v", i.VerifyKIDs())

	// The active key cannot be removed, because removing it would make every new token unverifiable.
	if err := i.RemoveVerifyKey("k2"); err == nil {
		t.Error("the active key was removed")
	} else {
		t.Logf("removing the active key is refused: %v", err)
	}

	// The old one can be, TTL after the switch.
	if err := i.RemoveVerifyKey("k1"); err != nil {
		t.Fatal(err)
	}

	if _, err := i.Verify(oldToken); err == nil {
		t.Error("the old token still verifies after its key was removed")
	}
	if _, err := i.Verify(newToken); err != nil {
		t.Errorf("the new token stopped verifying: %v", err)
	}

	t.Logf("k1 removed %v after the switch, which is the longest a token signed with it can live",
		i.TTL)

	// Rotate rejects the states that would produce the original bug.
	if err := i.Rotate("", newSecret, newSecret); err == nil {
		t.Error("Rotate accepted an empty kid")
	}
	if err := i.Rotate("k3", nil, newSecret); err == nil {
		t.Error("Rotate accepted a nil signing key")
	}
	if err := i.Rotate("k3", newSecret, nil); err == nil {
		t.Error("Rotate accepted a nil verification key")
	}
}

// TestRS256RotationCannotDeriveOneKeyFromTheOther is why Rotate takes two arguments.
func TestRS256RotationCannotDeriveOneKeyFromTheOther(t *testing.T) {
	i, first := newRS256(t)

	oldToken, err := i.Mint("user-42", []string{"user"}, "")
	if err != nil {
		t.Fatal(err)
	}

	second, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}

	// The private key signs, the public key verifies, and they are different objects. An API that
	// took one key could not do this.
	if err := i.Rotate("k2", second, &second.PublicKey); err != nil {
		t.Fatal(err)
	}

	newToken, err := i.Mint("user-42", []string{"user"}, "")
	if err != nil {
		t.Fatal(err)
	}

	for name, token := range map[string]string{"old": oldToken, "new": newToken} {
		if _, err := i.Verify(token); err != nil {
			t.Errorf("the %s token failed: %v", name, err)
		}
	}

	t.Logf("RS256 rotation with two distinct key pairs, both verifying: %v", i.VerifyKIDs())

	// The point, stated as an assertion: the two moduli differ, so nothing could have derived one
	// from the other.
	if first.PublicKey.N.Cmp(second.PublicKey.N) == 0 {
		t.Fatal("the two generated keys are identical, which cannot happen")
	}

	t.Log("this is the case a SetActiveKID that copied from the verify map would have signed " +
		"tokens with an RSA public key, or panicked")
}

// TestShortSecretsAreRefused, because HS256 with a weak secret is brute-forceable offline.
func TestShortSecretsAreRefused(t *testing.T) {
	for _, s := range []string{"", "secret", "password123", strings.Repeat("a", 31)} {
		if _, err := NewHS256([]byte(s), "k1"); err == nil {
			t.Errorf("a %d-byte secret was accepted", len(s))
		}
	}

	if _, err := NewHS256([]byte(strings.Repeat("a", 32)), "k1"); err != nil {
		t.Errorf("a 32-byte secret was refused: %v", err)
	}

	t.Log("HS256 is HMAC-SHA256, so anyone with one token can try secrets offline at millions " +
		"per second. The length check is the only thing between a service and that.")
}

// TestBearerTokenParsing, because the scheme is case-insensitive and clients disagree about it.
func TestBearerTokenParsing(t *testing.T) {
	for _, tc := range []struct {
		header string
		want   string
		ok     bool
	}{
		{"Bearer abc.def.ghi", "abc.def.ghi", true},
		{"bearer abc.def.ghi", "abc.def.ghi", true},
		{"BEARER abc.def.ghi", "abc.def.ghi", true},
		{"Bearer  abc.def.ghi ", "abc.def.ghi", true},
		{"", "", false},
		{"abc.def.ghi", "", false},
		{"Basic dXNlcjpwYXNz", "", false},
		{"Bearer", "", false},
		{"Bearer ", "", false},
	} {
		r := httptest.NewRequest("GET", "/", nil)
		if tc.header != "" {
			r.Header.Set("Authorization", tc.header)
		}

		got, err := BearerToken(r)

		if tc.ok {
			if err != nil {
				t.Errorf("%q: %v", tc.header, err)
			}
			if got != tc.want {
				t.Errorf("%q gave %q, want %q", tc.header, got, tc.want)
			}
		} else if err == nil {
			t.Errorf("%q was accepted as %q", tc.header, got)
		}
	}
}

// TestMiddlewareStatusCodes: 401 against 403 is the distinction that stops a client looping.
func TestMiddlewareStatusCodes(t *testing.T) {
	i := newHS256(t)

	var seen Claims

	protected := i.Authenticate(RequireRole("admin")(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			seen, _ = FromContext(r.Context())
			w.WriteHeader(http.StatusOK)
		})))

	adminToken, err := i.Mint("admin-1", []string{"admin"}, "")
	if err != nil {
		t.Fatal(err)
	}

	userToken, err := i.Mint("user-1", []string{"user"}, "")
	if err != nil {
		t.Fatal(err)
	}

	expired, err := func() (string, error) {
		past, err := NewHS256(secret, "k1")
		if err != nil {
			return "", err
		}
		past.Issuer, past.Audience = "learn-go", "api"
		past.TTL = time.Minute
		past.Now = func() time.Time { return fixedTime.Add(-time.Hour) }
		return past.Mint("user-1", []string{"admin"}, "")
	}()
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name   string
		header string
		want   int
	}{
		{"no header", "", http.StatusUnauthorized},
		{"nonsense", "Bearer not-a-token", http.StatusUnauthorized},
		{"expired", "Bearer " + expired, http.StatusUnauthorized},
		{"wrong role", "Bearer " + userToken, http.StatusForbidden},
		{"admin", "Bearer " + adminToken, http.StatusOK},
	} {
		r := httptest.NewRequest("GET", "/admin", nil)
		if tc.header != "" {
			r.Header.Set("Authorization", tc.header)
		}

		rec := httptest.NewRecorder()
		protected.ServeHTTP(rec, r)

		challenge := rec.Header().Get("WWW-Authenticate")

		t.Logf("%-12s %d  %s", tc.name, rec.Code, challenge)

		if rec.Code != tc.want {
			t.Errorf("%s: got %d, want %d", tc.name, rec.Code, tc.want)
		}

		if rec.Code == http.StatusUnauthorized && challenge == "" {
			t.Errorf("%s: a 401 with no WWW-Authenticate leaves the client guessing", tc.name)
		}
		if rec.Code == http.StatusForbidden && challenge != "" {
			t.Errorf("%s: a 403 should not send a challenge; the client is authenticated",
				tc.name)
		}
	}

	if seen.Subject != "admin-1" {
		t.Errorf("the handler saw subject %q", seen.Subject)
	}

	// The expired case has to say WHY, so a client knows to refresh rather than to log the user out.
	r := httptest.NewRequest("GET", "/admin", nil)
	r.Header.Set("Authorization", "Bearer "+expired)

	rec := httptest.NewRecorder()
	protected.ServeHTTP(rec, r)

	if !strings.Contains(rec.Header().Get("WWW-Authenticate"), "expired") {
		t.Errorf("the expiry challenge does not mention it: %q",
			rec.Header().Get("WWW-Authenticate"))
	}
}

// TestRequireRoleWithoutAuthenticateIs500, because that is a wiring bug and not an auth failure.
func TestRequireRoleWithoutAuthenticateIs500(t *testing.T) {
	// RequireRole on its own, which is the mistake: the route forgot Authenticate.
	handler := RequireRole("admin")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/admin", nil))

	t.Logf("RequireRole with no claims in the context: %d", rec.Code)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("got %d, want 500", rec.Code)
	}

	t.Log("500 rather than 401, because a missing Authenticate is a routing bug and a 401 " +
		"would hide it behind something that looks like a normal auth failure")
}

// TestContextKeyCannotCollide, because a string key would.
func TestContextKeyCannotCollide(t *testing.T) {
	i := newHS256(t)

	token, err := i.Mint("user-42", []string{"user"}, "")
	if err != nil {
		t.Fatal(err)
	}

	claims, err := i.Verify(token)
	if err != nil {
		t.Fatal(err)
	}

	ctx := WithClaims(t.Context(), claims)

	got, ok := FromContext(ctx)
	if !ok {
		t.Fatal("the claims were not in the context")
	}
	if got.Subject != "user-42" {
		t.Errorf("subject %q", got.Subject)
	}

	// Nothing in a bare context.
	if _, ok := FromContext(t.Context()); ok {
		t.Error("FromContext found claims in an empty context")
	}

	// And a string key with the obvious name does not reach them, which is the point of an
	// unexported struct type.
	//nolint:staticcheck // the whole point is that a string key is the wrong thing
	if v := ctx.Value("claims"); v != nil {
		t.Errorf(`ctx.Value("claims") returned %v`, v)
	}
}

// TestJWTErrorsAreMatchable verifies the assumption Verify's switch rests on.
func TestJWTErrorsAreMatchable(t *testing.T) {
	i := newHS256(t)
	i.TTL = time.Minute

	token, err := i.Mint("user-42", nil, "")
	if err != nil {
		t.Fatal(err)
	}

	i.Now = func() time.Time { return fixedTime.Add(time.Hour) }

	_, err = i.Verify(token)
	if err == nil {
		t.Fatal("expected an expiry error")
	}

	// Both the package's sentinel and the library's, because Verify joins them with %w twice and
	// that only works if both survive.
	if !errors.Is(err, ErrExpired) {
		t.Errorf("does not match ErrExpired: %v", err)
	}
	if !errors.Is(err, jwt.ErrTokenExpired) {
		t.Errorf("does not match jwt.ErrTokenExpired: %v", err)
	}

	t.Logf("both sentinels match, so a caller can use either: %v", err)
	t.Log("worth checking rather than assuming. pgx and x/time/rate both have errors that do " +
		"NOT wrap what you would expect, and both cost a failing test to find.")
}

func publicKeyBytes(t *testing.T, key *rsa.PublicKey) []byte {
	t.Helper()

	// The signing method for HS256 takes a []byte secret, so the attack uses the public key's bytes.
	// Any encoding of it works as long as the attacker and the server agree, and in the real attack
	// the attacker uses whatever the server publishes, usually PEM.
	der, err := x509.MarshalPKIXPublicKey(key)
	if err != nil {
		t.Fatal(err)
	}

	return der
}

func truncate(s string) string {
	if len(s) <= 24 {
		return s
	}
	return s[:24] + "..."
}

func resultOf(err error) string {
	if err == nil {
		return "accepted"
	}
	return err.Error()
}

// A sanity check that the HMAC in this package's tests matches crypto/hmac, so a library upgrade that changed
// the signing bytes would be caught.
func TestHS256MatchesCryptoHMAC(t *testing.T) {
	i := newHS256(t)

	token, err := i.Mint("user-42", nil, "")
	if err != nil {
		t.Fatal(err)
	}

	parts := strings.Split(token, ".")

	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(parts[0] + "." + parts[1]))

	want := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	if parts[2] != want {
		t.Errorf("signature is\n  %s\nwant\n  %s", parts[2], want)
	}
}
