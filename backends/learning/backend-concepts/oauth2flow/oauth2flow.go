// Package oauth2flow is the authorization code flow with PKCE, and the four parameters that are the whole of
// its security.
//
// # The flow, in the order it happens
//
//  1. the app sends the user to the provider with client_id, redirect_uri, scope, state and
//     code_challenge
//  2. the user authenticates at the provider and approves
//  3. the provider redirects back to redirect_uri with code and state
//  4. the app checks state, then exchanges code plus code_verifier for tokens, server to server
//  5. the app uses the access token, and refreshes it with the refresh token when it expires
//
// Step 4 is the point of the whole design: the tokens never pass through the browser. The code does, and a code
// is useless without the client secret or the code verifier.
//
// # The four parameters
//
//	state           CSRF protection for the callback. Without it, an attacker can complete their
//	                own authorization and redirect the victim's browser to the callback with THEIR
//	                code, linking the victim's session to the attacker's account. Must be random,
//	                stored server-side or in a signed cookie, and compared on return.
//	code_verifier   PKCE. A random secret the app keeps; the provider only ever sees its SHA-256
//	                hash on the way out. An attacker who steals the code cannot use it.
//	redirect_uri    matched EXACTLY by the provider, including the trailing slash and the port.
//	                Not a prefix match, because a prefix match plus an open redirect on the app is
//	                a code exfiltration.
//	nonce           OIDC only, and the same idea as state for the ID token.
//
// # What is dead and why it still appears in tutorials
//
// The IMPLICIT flow (response_type=token) returned the access token in the URL fragment, so it ended up in
// browser history, in Referer headers and in server logs. It existed because browsers could not make
// cross-origin requests to the token endpoint. CORS solved that a decade ago, and OAuth 2.1 removes implicit
// entirely.
//
// The PASSWORD grant sent the user's actual password to the app. Also removed in 2.1, and still the first
// example in a lot of documentation.
//
// PKCE was originally for mobile apps that could not keep a secret. OAuth 2.1 requires it for EVERY client,
// including confidential ones with a secret, because it also closes code injection.
package oauth2flow

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
)

// Errors a caller distinguishes.
var (
	ErrNoState        = errors.New("no state in the callback")
	ErrStateMismatch  = errors.New("state does not match")
	ErrStateExpired   = errors.New("state expired")
	ErrNoCode         = errors.New("no code in the callback")
	ErrProviderDenied = errors.New("the provider returned an error")
)

// PKCE is a code verifier and its challenge.
//
// # The sizes are from the RFC and are not arbitrary
//
// RFC 7636 says the verifier is 43 to 128 characters from an unreserved alphabet. 43 characters of base64url is
// 32 bytes of entropy, which is the minimum, and this uses exactly that: more is allowed and buys nothing,
// because 256 bits is already beyond brute force.
type PKCE struct {
	Verifier  string
	Challenge string
	Method    string
}

// NewPKCE generates a verifier and its S256 challenge.
//
// S256, never "plain". The plain method sends the verifier itself as the challenge, which means an attacker who
// can see the authorization request has the verifier, which is the thing PKCE exists to keep secret. It is in
// the RFC only for clients that cannot compute SHA-256, and every Go client can.
func NewPKCE() (PKCE, error) {
	b := make([]byte, 32)

	if _, err := rand.Read(b); err != nil {
		return PKCE{}, fmt.Errorf("generating a code verifier: %w", err)
	}

	// RawURLEncoding: base64url with no padding. The padding character is not in the RFC's
	// unreserved alphabet, so StdEncoding produces a verifier a strict provider rejects, and a
	// lenient one accepts, which is worse because it works until you change provider.
	verifier := base64.RawURLEncoding.EncodeToString(b)

	sum := sha256.Sum256([]byte(verifier))

	return PKCE{
		Verifier: verifier,

		// The challenge is the base64url of the SHA-256 of the ASCII of the verifier. Hashing
		// the DECODED bytes instead is the classic implementation bug: it produces a challenge
		// the provider cannot match, and the error at the token endpoint says only
		// "invalid_grant".
		Challenge: base64.RawURLEncoding.EncodeToString(sum[:]),
		Method:    "S256",
	}, nil
}

// Verify checks a verifier against a challenge, which is what the provider does.
//
// Here so the tests can play the provider's part, and because seeing the check is what makes the asymmetry
// obvious: the provider stores the challenge and never learns the verifier until the exchange.
func (p PKCE) Verify(verifier string) bool {
	sum := sha256.Sum256([]byte(verifier))
	expected := base64.RawURLEncoding.EncodeToString(sum[:])

	// Constant time, for the same reason as the webhook signature: the comparison must not leak how
	// much of the value was right.
	return subtle.ConstantTimeCompare([]byte(expected), []byte(p.Challenge)) == 1
}

// State is the CSRF token for the callback, with the PKCE verifier attached.
//
// They travel together because they are needed together and have the same lifetime. Storing them separately is
// two places to expire, two places to clean up, and one more way for a callback to find half of what it needs.
type State struct {
	Value     string
	Verifier  string
	CreatedAt time.Time

	// ReturnTo is where to send the user after login. It goes in the server-side state rather than
	// in a URL parameter, and that is a decision worth stating: a `?next=` parameter that is not
	// validated is an open redirect, and validating it correctly is harder than not having it.
	ReturnTo string
}

// StateStore holds pending authorizations.
//
// An interface, because the in-memory implementation here is wrong for more than one replica: a user who starts
// login on replica A and is redirected back to replica B finds no state and is told their login failed. The
// real implementations are a signed cookie (no server state at all) or Redis.
type StateStore interface {
	Put(ctx context.Context, s State) error
	Take(ctx context.Context, value string) (State, error)
}

// Flow ties the pieces together.
type Flow struct {
	Config *oauth2.Config
	Store  StateStore

	// TTL is how long a pending authorization is valid. Short: this is the time between the
	// redirect and the callback, which is however long the user takes to type a password. Ten
	// minutes is generous and an hour is a wide replay window.
	TTL time.Duration

	// Now is injectable for the expiry tests.
	Now func() time.Time
}

func (f *Flow) now() time.Time {
	if f.Now == nil {
		return time.Now()
	}
	return f.Now()
}

// Start generates the state and PKCE, stores them, and returns the URL to send the user to.
func (f *Flow) Start(ctx context.Context, returnTo string) (string, State, error) {
	pkce, err := NewPKCE()
	if err != nil {
		return "", State{}, err
	}

	value, err := randomToken()
	if err != nil {
		return "", State{}, fmt.Errorf("generating state: %w", err)
	}

	s := State{
		Value:     value,
		Verifier:  pkce.Verifier,
		CreatedAt: f.now(),
		ReturnTo:  returnTo,
	}

	if err := f.Store.Put(ctx, s); err != nil {
		return "", State{}, fmt.Errorf("storing state: %w", err)
	}

	// AuthCodeURL takes the state and any extra parameters. The PKCE ones have helpers in
	// x/oauth2, and spelling them out shows what is on the wire.
	authURL := f.Config.AuthCodeURL(value,
		oauth2.SetAuthURLParam("code_challenge", pkce.Challenge),
		oauth2.SetAuthURLParam("code_challenge_method", pkce.Method),

		// AccessTypeOffline is Google's spelling of "give me a refresh token". Other providers
		// use scope=offline_access. There is no portable way to ask, which is the first thing
		// that breaks when swapping provider.
		oauth2.AccessTypeOffline,
	)

	return authURL, s, nil
}

// Callback validates the callback and exchanges the code for tokens.
//
// The order is the point: EVERY check happens before the exchange. An implementation that exchanges first and
// validates state afterwards has already spent the code, and the CSRF protection is decoration.
func (f *Flow) Callback(ctx context.Context, r *http.Request) (*oauth2.Token, State, error) {
	q := r.URL.Query()

	// The provider's own error comes back as a query parameter, not an HTTP error. An
	// implementation that only looks for `code` reports "no code" when the real answer is
	// "the user clicked Deny", and the support ticket is unanswerable.
	if e := q.Get("error"); e != "" {
		return nil, State{}, fmt.Errorf("%w: %s: %s", ErrProviderDenied, e,
			q.Get("error_description"))
	}

	value := q.Get("state")
	if value == "" {
		return nil, State{}, ErrNoState
	}

	// Take removes it, so a code cannot be replayed with the same state. Get-then-delete would
	// leave a window; the store's contract is that Take is atomic.
	s, err := f.Store.Take(ctx, value)
	if err != nil {
		return nil, State{}, err
	}

	if f.TTL > 0 && f.now().Sub(s.CreatedAt) > f.TTL {
		return nil, State{}, fmt.Errorf("%w: %v old, TTL is %v",
			ErrStateExpired, f.now().Sub(s.CreatedAt).Round(time.Second), f.TTL)
	}

	// Constant-time, even though the state was just looked up BY this value. The comparison is
	// cheap and the habit is what matters: a store that does a prefix match, or one that returns
	// the nearest entry, would otherwise pass.
	if subtle.ConstantTimeCompare([]byte(s.Value), []byte(value)) != 1 {
		return nil, State{}, ErrStateMismatch
	}

	code := q.Get("code")
	if code == "" {
		return nil, State{}, ErrNoCode
	}

	// The exchange, server to server, carrying the verifier. This is the request the browser never
	// sees and the reason the tokens never touch it.
	token, err := f.Config.Exchange(ctx, code,
		oauth2.SetAuthURLParam("code_verifier", s.Verifier))
	if err != nil {
		return nil, s, fmt.Errorf("exchanging the code: %w", err)
	}

	return token, s, nil
}

// MemoryStore is a StateStore for one process.
//
// Documented as insufficient rather than presented as an implementation: see StateStore.
type MemoryStore struct {
	mu     sync.Mutex
	states map[string]State

	now func() time.Time
	ttl time.Duration
}

// NewMemoryStore builds one.
func NewMemoryStore(ttl time.Duration) *MemoryStore {
	return &MemoryStore{
		states: make(map[string]State),
		now:    time.Now,
		ttl:    ttl,
	}
}

// Put stores a pending authorization.
func (m *MemoryStore) Put(_ context.Context, s State) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.sweep()

	m.states[s.Value] = s

	return nil
}

// Take removes and returns one.
//
// Removing is what makes a code single-use from the app's side. The provider also refuses a reused code, and
// relying on that alone means a replay reaches the provider and shows up in its logs as suspicious activity
// from you.
func (m *MemoryStore) Take(_ context.Context, value string) (State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.states[value]
	if !ok {
		return State{}, ErrStateMismatch
	}

	delete(m.states, value)

	return s, nil
}

// sweep drops expired entries. Called with the mutex held.
//
// Without it, every abandoned login leaks an entry: a user who opens the login page and closes the tab leaves
// a State nobody will ever take.
func (m *MemoryStore) sweep() {
	if m.ttl <= 0 {
		return
	}

	cutoff := m.now().Add(-m.ttl)

	for value, s := range m.states {
		if s.CreatedAt.Before(cutoff) {
			delete(m.states, value)
		}
	}
}

// Len reports how many pending authorizations are held.
func (m *MemoryStore) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	return len(m.states)
}

// ValidateRedirectURI checks an exact match against an allowlist.
//
// # Why exact and not a prefix
//
// A provider that prefix-matches lets `https://app.example/callback` also match
// `https://app.example/callback/../../anything`, and combined with any open redirect on the app, an attacker
// gets the code. Every provider worth using matches exactly, and an app that registers
// `https://app.example/callback` and sends `https://app.example/callback/` gets `redirect_uri_mismatch`, which
// is the most common first-time OAuth error there is.
func ValidateRedirectURI(candidate string, allowed []string) error {
	for _, a := range allowed {
		if candidate == a {
			return nil
		}
	}

	// The error names the near-misses, because the difference is usually a trailing slash, a port,
	// or http against https, and none of those are visible in a log line that says only "mismatch".
	var hints []string

	for _, a := range allowed {
		if strings.EqualFold(strings.TrimRight(candidate, "/"), strings.TrimRight(a, "/")) {
			hints = append(hints, "trailing slash: registered "+a)
		}
	}

	if u, err := url.Parse(candidate); err == nil {
		for _, a := range allowed {
			if au, err := url.Parse(a); err == nil && au.Host == u.Host && au.Scheme != u.Scheme {
				hints = append(hints, "scheme: registered "+au.Scheme)
			}
		}
	}

	if len(hints) > 0 {
		return fmt.Errorf("redirect_uri %q does not match exactly (%s)",
			candidate, strings.Join(hints, "; "))
	}

	return fmt.Errorf("redirect_uri %q is not registered", candidate)
}

func randomToken() (string, error) {
	b := make([]byte, 32)

	if _, err := rand.Read(b); err != nil {
		return "", err
	}

	return base64.RawURLEncoding.EncodeToString(b), nil
}
