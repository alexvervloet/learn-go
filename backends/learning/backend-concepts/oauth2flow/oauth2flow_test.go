package oauth2flow

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

// fakeProvider is an authorization server that implements exactly the parts the flow depends on.
//
// Writing one rather than mocking x/oauth2 is what makes these tests worth having: the real library talks to it
// over HTTP, so the request it actually sends is what is being asserted on, including the parameters this
// package adds by hand.
type fakeProvider struct {
	*httptest.Server

	mu sync.Mutex

	// codes maps an issued code to the challenge it was issued against, which is what PKCE means
	// from the provider's side: store the challenge, verify the verifier at the exchange.
	codes map[string]issuedCode

	// Records of what was received, for assertions.
	authRequests  []url.Values
	tokenRequests []url.Values

	// Knobs for the failure cases.
	rejectVerifier bool
}

type issuedCode struct {
	challenge string
	method    string
	used      bool
}

func newFakeProvider(t *testing.T) *fakeProvider {
	t.Helper()

	p := &fakeProvider{codes: make(map[string]issuedCode)}

	mux := http.NewServeMux()

	// The authorization endpoint. A real one shows a login page; this issues a code immediately.
	mux.HandleFunc("GET /authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()

		p.mu.Lock()
		p.authRequests = append(p.authRequests, q)

		code := "code-" + q.Get("state")[:8]
		p.codes[code] = issuedCode{
			challenge: q.Get("code_challenge"),
			method:    q.Get("code_challenge_method"),
		}
		p.mu.Unlock()

		redirect, _ := url.Parse(q.Get("redirect_uri"))

		rq := redirect.Query()
		rq.Set("code", code)
		rq.Set("state", q.Get("state"))
		redirect.RawQuery = rq.Encode()

		http.Redirect(w, r, redirect.String(), http.StatusFound)
	})

	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}

		p.mu.Lock()
		p.tokenRequests = append(p.tokenRequests, r.PostForm)

		code := r.PostFormValue("code")
		issued, ok := p.codes[code]

		switch {
		case !ok:
			p.mu.Unlock()
			writeOAuthError(w, "invalid_grant", "unknown code")
			return

		case issued.used:
			// A reused code. Real providers revoke every token issued for it, on the
			// assumption that a second use means the first one was stolen.
			p.mu.Unlock()
			writeOAuthError(w, "invalid_grant", "code already used")
			return
		}

		reject := p.rejectVerifier
		p.mu.Unlock()

		verifier := r.PostFormValue("code_verifier")

		if issued.challenge != "" {
			if verifier == "" {
				writeOAuthError(w, "invalid_request", "code_verifier required")
				return
			}

			sum := sha256.Sum256([]byte(verifier))
			expected := base64.RawURLEncoding.EncodeToString(sum[:])

			if reject || expected != issued.challenge {
				writeOAuthError(w, "invalid_grant", "code_verifier does not match")
				return
			}
		}

		// Burned only on SUCCESS. Marking it used before the verifier check made a failed
		// attempt mask the next one's real error: the test for a stolen code reported
		// "code already used" where it meant to report "code_verifier does not match".
		//
		// Real providers differ on this, and the ones that burn on any attempt are arguably
		// safer. For a fixture, saying the true thing about each attempt matters more.
		p.mu.Lock()
		issued.used = true
		p.codes[code] = issued
		p.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "access-" + code,
			"refresh_token": "refresh-" + code,
			"token_type":    "Bearer",
			"expires_in":    3600,
		})
	})

	p.Server = httptest.NewServer(mux)
	t.Cleanup(p.Close)

	return p
}

func writeOAuthError(w http.ResponseWriter, code, description string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)

	_ = json.NewEncoder(w).Encode(map[string]string{
		"error":             code,
		"error_description": description,
	})
}

func (p *fakeProvider) lastAuthRequest(t *testing.T) url.Values {
	t.Helper()

	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.authRequests) == 0 {
		t.Fatal("no authorization requests received")
	}

	return p.authRequests[len(p.authRequests)-1]
}

func (p *fakeProvider) lastTokenRequest(t *testing.T) url.Values {
	t.Helper()

	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.tokenRequests) == 0 {
		t.Fatal("no token requests received")
	}

	return p.tokenRequests[len(p.tokenRequests)-1]
}

func newFlow(t *testing.T, p *fakeProvider) *Flow {
	t.Helper()

	return &Flow{
		Config: &oauth2.Config{
			ClientID:     "test-client",
			ClientSecret: "test-secret",
			RedirectURL:  "https://app.example/callback",
			Scopes:       []string{"openid", "email"},
			Endpoint: oauth2.Endpoint{
				AuthURL:  p.URL + "/authorize",
				TokenURL: p.URL + "/token",
			},
		},
		Store: NewMemoryStore(10 * time.Minute),
		TTL:   10 * time.Minute,
	}
}

// walkTheFlow does what a browser would: follow the redirect, collect the callback.
func walkTheFlow(t *testing.T, authURL string) *http.Request {
	t.Helper()

	client := &http.Client{
		// Do not follow the final redirect to the app, which does not exist. Stopping here is
		// what gives the test the callback URL.
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	resp, err := client.Get(authURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	location := resp.Header.Get("Location")
	if location == "" {
		t.Fatalf("the provider did not redirect: %d", resp.StatusCode)
	}

	callback, err := url.Parse(location)
	if err != nil {
		t.Fatal(err)
	}

	return httptest.NewRequest("GET", callback.String(), nil)
}

func TestTheWholeFlow(t *testing.T) {
	p := newFakeProvider(t)
	f := newFlow(t, p)

	ctx := context.Background()

	authURL, state, err := f.Start(ctx, "/dashboard")
	if err != nil {
		t.Fatal(err)
	}

	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatal(err)
	}

	q := parsed.Query()

	t.Logf("redirecting the user to %s", parsed.Path)
	for _, key := range []string{"client_id", "response_type", "scope", "state",
		"code_challenge_method", "access_type"} {
		t.Logf("  %-22s %s", key, truncate(q.Get(key)))
	}
	t.Logf("  %-22s %s", "code_challenge", truncate(q.Get("code_challenge")))

	if q.Get("response_type") != "code" {
		t.Errorf("response_type is %q; anything else is a dead flow", q.Get("response_type"))
	}
	if q.Get("code_challenge_method") != "S256" {
		t.Errorf("code_challenge_method is %q, want S256", q.Get("code_challenge_method"))
	}
	if q.Get("code_challenge") == state.Verifier {
		t.Error("the challenge equals the verifier, so this is the plain method and PKCE is " +
			"doing nothing")
	}

	// The verifier is NOT in the authorization request. That is the whole of PKCE.
	if strings.Contains(authURL, state.Verifier) {
		t.Error("the code verifier appears in the authorization URL")
	}

	callback := walkTheFlow(t, authURL)

	// What the provider actually received, which is the assertion that survives a refactor of how
	// the URL is built. Asserting on the URL alone would pass if AuthCodeURL stopped sending a
	// parameter the provider needs.
	received := p.lastAuthRequest(t)

	if received.Get("code_challenge") != q.Get("code_challenge") {
		t.Errorf("the provider received challenge %q and the URL carried %q",
			received.Get("code_challenge"), q.Get("code_challenge"))
	}
	if received.Get("client_id") != f.Config.ClientID {
		t.Errorf("the provider received client_id %q", received.Get("client_id"))
	}

	token, got, err := f.Callback(ctx, callback)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("exchanged for an access token expiring in %v",
		time.Until(token.Expiry).Round(time.Minute))

	if token.AccessToken == "" {
		t.Error("no access token")
	}
	if token.RefreshToken == "" {
		t.Error("no refresh token; access_type=offline was not honoured")
	}
	if got.ReturnTo != "/dashboard" {
		t.Errorf("ReturnTo is %q", got.ReturnTo)
	}

	// The verifier WAS in the token request, which never touched the browser.
	tokenReq := p.lastTokenRequest(t)

	if tokenReq.Get("code_verifier") != state.Verifier {
		t.Error("the token request did not carry the verifier")
	}
	if tokenReq.Get("grant_type") != "authorization_code" {
		t.Errorf("grant_type is %q", tokenReq.Get("grant_type"))
	}

	t.Log("the code went through the browser and the verifier did not, so a stolen code is " +
		"useless without the verifier the app kept")

	// The state was consumed, so the same callback cannot be replayed.
	_, _, err = f.Callback(ctx, callback)

	if !errors.Is(err, ErrStateMismatch) {
		t.Errorf("replaying the callback gave %v, want ErrStateMismatch", err)
	}

	t.Logf("replaying the same callback: %v", err)
}

// TestPKCEStopsAStolenCode is the attack, carried out.
func TestPKCEStopsAStolenCode(t *testing.T) {
	p := newFakeProvider(t)
	f := newFlow(t, p)

	ctx := context.Background()

	authURL, _, err := f.Start(ctx, "/")
	if err != nil {
		t.Fatal(err)
	}

	callback := walkTheFlow(t, authURL)

	code := callback.URL.Query().Get("code")

	t.Logf("the attacker intercepted the code: %s", code)

	// The attacker exchanges it with their own (wrong) verifier, or none at all.
	for _, verifier := range []string{"", "the-attackers-own-verifier-32-bytes-long"} {
		form := url.Values{
			"grant_type":   {"authorization_code"},
			"code":         {code},
			"redirect_uri": {f.Config.RedirectURL},
			"client_id":    {f.Config.ClientID},
		}

		if verifier != "" {
			form.Set("code_verifier", verifier)
		}

		resp, err := http.PostForm(p.URL+"/token", form)
		if err != nil {
			t.Fatal(err)
		}

		var body map[string]string
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()

		t.Logf("exchange with verifier %q: %d %s (%s)",
			truncate(verifier), resp.StatusCode, body["error"], body["error_description"])

		if resp.StatusCode == http.StatusOK {
			t.Errorf("the attacker exchanged a stolen code with verifier %q", verifier)
		}
	}

	t.Log("without PKCE the stolen code exchanges successfully, because the only other thing " +
		"required is the client_id, which is public")
}

// TestStateIsCheckedBeforeTheExchange, which is the ordering that makes CSRF protection real.
func TestStateIsCheckedBeforeTheExchange(t *testing.T) {
	p := newFakeProvider(t)
	f := newFlow(t, p)

	ctx := context.Background()

	authURL, _, err := f.Start(ctx, "/")
	if err != nil {
		t.Fatal(err)
	}

	callback := walkTheFlow(t, authURL)

	// The attacker's version: a valid code with a state the app never issued. This is session
	// fixation, where the victim ends up logged into the attacker's account.
	tampered := *callback.URL
	q := tampered.Query()
	q.Set("state", "the-attackers-state")
	tampered.RawQuery = q.Encode()

	p.mu.Lock()
	before := len(p.tokenRequests)
	p.mu.Unlock()

	_, _, err = f.Callback(ctx, httptest.NewRequest("GET", tampered.String(), nil))

	if !errors.Is(err, ErrStateMismatch) {
		t.Errorf("got %v, want ErrStateMismatch", err)
	}

	p.mu.Lock()
	after := len(p.tokenRequests)
	p.mu.Unlock()

	t.Logf("a callback with an unknown state: %v", err)
	t.Logf("token requests before %d, after %d", before, after)

	if after != before {
		t.Error("the code was exchanged before the state was checked, so the CSRF protection " +
			"is decoration: the code is already spent")
	}

	// And a callback with no state at all.
	noState := *callback.URL
	q = noState.Query()
	q.Del("state")
	noState.RawQuery = q.Encode()

	_, _, err = f.Callback(ctx, httptest.NewRequest("GET", noState.String(), nil))

	if !errors.Is(err, ErrNoState) {
		t.Errorf("got %v, want ErrNoState", err)
	}
}

// TestProviderErrorsAreNotMissingCodes.
func TestProviderErrorsAreNotMissingCodes(t *testing.T) {
	p := newFakeProvider(t)
	f := newFlow(t, p)

	ctx := context.Background()

	// What the provider sends when the user clicks Deny.
	callback := httptest.NewRequest("GET",
		"https://app.example/callback?error=access_denied&error_description=The+user+denied+the+request",
		nil)

	_, _, err := f.Callback(ctx, callback)

	if !errors.Is(err, ErrProviderDenied) {
		t.Errorf("got %v, want ErrProviderDenied", err)
	}

	t.Logf("the user clicked Deny: %v", err)

	if !strings.Contains(err.Error(), "access_denied") {
		t.Error("the error does not say which OAuth error it was")
	}

	t.Log("an implementation that only looks for `code` reports 'no code' here, and the " +
		"support ticket is unanswerable")
}

// TestStateExpires, so an abandoned login cannot be resumed a week later.
func TestStateExpires(t *testing.T) {
	p := newFakeProvider(t)
	f := newFlow(t, p)

	clock := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)
	f.Now = func() time.Time { return clock }
	f.TTL = 10 * time.Minute

	ctx := context.Background()

	authURL, _, err := f.Start(ctx, "/")
	if err != nil {
		t.Fatal(err)
	}

	callback := walkTheFlow(t, authURL)

	clock = clock.Add(11 * time.Minute)

	_, _, err = f.Callback(ctx, callback)

	if !errors.Is(err, ErrStateExpired) {
		t.Errorf("got %v, want ErrStateExpired", err)
	}

	t.Logf("a callback 11 minutes after a 10 minute TTL: %v", err)
}

// TestMemoryStoreSweeps, because every abandoned login leaks an entry.
func TestMemoryStoreSweeps(t *testing.T) {
	clock := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)

	store := NewMemoryStore(10 * time.Minute)
	store.now = func() time.Time { return clock }

	ctx := context.Background()

	for i := range 100 {
		if err := store.Put(ctx, State{
			Value:     "state-" + itoa(i),
			CreatedAt: clock,
		}); err != nil {
			t.Fatal(err)
		}
	}

	t.Logf("100 logins started: %d pending", store.Len())

	if store.Len() != 100 {
		t.Errorf("holding %d", store.Len())
	}

	// Nobody completes them. Eleven minutes later, one more login sweeps the rest.
	clock = clock.Add(11 * time.Minute)

	if err := store.Put(ctx, State{Value: "fresh", CreatedAt: clock}); err != nil {
		t.Fatal(err)
	}

	t.Logf("11 minutes later, after one more login: %d pending", store.Len())

	if store.Len() != 1 {
		t.Errorf("holding %d after the sweep, want 1", store.Len())
	}

	t.Log("a user who opens the login page and closes the tab leaves a State nobody will ever " +
		"take, so the sweep is not optional")
}

// TestPKCEVerification, both directions.
func TestPKCEVerification(t *testing.T) {
	p, err := NewPKCE()
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("verifier:  %s (%d chars)", p.Verifier, len(p.Verifier))
	t.Logf("challenge: %s", p.Challenge)

	// 32 bytes of entropy is 43 base64url characters, which is the RFC's minimum.
	if len(p.Verifier) != 43 {
		t.Errorf("the verifier is %d characters, want 43", len(p.Verifier))
	}

	// No padding, because '=' is not in the RFC's unreserved alphabet.
	if strings.ContainsAny(p.Verifier, "=+/") {
		t.Errorf("the verifier %q is not base64url without padding", p.Verifier)
	}
	if strings.ContainsAny(p.Challenge, "=+/") {
		t.Errorf("the challenge %q is not base64url without padding", p.Challenge)
	}

	if !p.Verify(p.Verifier) {
		t.Error("a verifier did not verify against its own challenge")
	}

	for _, wrong := range []string{"", p.Verifier + "x", p.Verifier[:42], strings.ToUpper(p.Verifier)} {
		if p.Verify(wrong) {
			t.Errorf("%q verified", truncate(wrong))
		}
	}

	// The classic bug: hashing the DECODED bytes rather than the ASCII of the verifier.
	decoded, err := base64.RawURLEncoding.DecodeString(p.Verifier)
	if err != nil {
		t.Fatal(err)
	}

	sum := sha256.Sum256(decoded)
	wrongChallenge := base64.RawURLEncoding.EncodeToString(sum[:])

	t.Logf("hashing the decoded bytes gives %s", wrongChallenge)

	if wrongChallenge == p.Challenge {
		t.Error("hashing the decoded bytes produced the same challenge, which cannot be right")
	}

	t.Log("the challenge is SHA-256 of the ASCII of the verifier, not of its decoded bytes. " +
		"The wrong one fails at the token endpoint with only 'invalid_grant' to go on.")

	// Two calls never produce the same verifier.
	second, err := NewPKCE()
	if err != nil {
		t.Fatal(err)
	}

	if second.Verifier == p.Verifier {
		t.Error("two calls produced the same verifier")
	}
}

// TestCodeIsSingleUse, which the provider enforces and the app should too.
func TestCodeIsSingleUse(t *testing.T) {
	p := newFakeProvider(t)
	f := newFlow(t, p)

	ctx := context.Background()

	authURL, state, err := f.Start(ctx, "/")
	if err != nil {
		t.Fatal(err)
	}

	callback := walkTheFlow(t, authURL)

	if _, _, err := f.Callback(ctx, callback); err != nil {
		t.Fatal(err)
	}

	// The app's own store already refuses, which is what stops a replay reaching the provider at
	// all. Put the state back to see what the provider does.
	if err := f.Store.Put(ctx, State{
		Value:     state.Value,
		Verifier:  state.Verifier,
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	_, _, err = f.Callback(ctx, callback)

	if err == nil {
		t.Fatal("the provider accepted a reused code")
	}

	t.Logf("reusing a code at the provider: %v", err)

	if !strings.Contains(err.Error(), "invalid_grant") {
		t.Errorf("the error does not mention invalid_grant: %v", err)
	}

	t.Log("a real provider revokes every token issued for a reused code, on the assumption " +
		"that a second use means the first was stolen. Consuming the state in the app is " +
		"what keeps that from ever being triggered by your own retry logic.")
}

// TestValidateRedirectURI, including the near-misses that produce the most common OAuth error.
func TestValidateRedirectURI(t *testing.T) {
	allowed := []string{
		"https://app.example/callback",
		"http://localhost:8080/callback",
	}

	for _, tc := range []struct {
		candidate string
		ok        bool
		hint      string
	}{
		{"https://app.example/callback", true, ""},
		{"http://localhost:8080/callback", true, ""},
		{"https://app.example/callback/", false, "trailing slash"},
		{"http://app.example/callback", false, "scheme"},
		{"https://app.example/callback?x=1", false, "query string"},
		{"http://localhost:3000/callback", false, "port"},
		{"https://evil.example/callback", false, "not registered"},
		{"", false, "empty"},
	} {
		err := ValidateRedirectURI(tc.candidate, allowed)

		if tc.ok {
			if err != nil {
				t.Errorf("%q: %v", tc.candidate, err)
			}
			continue
		}

		if err == nil {
			t.Errorf("%q was accepted", tc.candidate)
			continue
		}

		t.Logf("%-38s %v", tc.candidate, err)
	}

	t.Log("exact matching, including the trailing slash and the port. A prefix match plus any " +
		"open redirect on the app is a code exfiltration.")
}

func truncate(s string) string {
	if len(s) <= 24 {
		return s
	}
	return s[:24] + "..."
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}

	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
