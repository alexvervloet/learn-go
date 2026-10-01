package api_test

import (
	"net/http"
	"testing"

	"github.com/alexvervloet/learn-go/capstones/url-shortener/internal/apitest"
	"github.com/stretchr/testify/require"
)

// login tries one email and password and returns the status.
func login(t *testing.T, h *apitest.Harness, email, password string) *apitest.Response {
	t.Helper()

	return h.Do(t, http.MethodPost, "/api/v1/login", "", map[string]string{"email": email, "password": password})
}

// TestTheCredentialEndpointsShareOneBucket is the bcrypt cap. Register and login draw on one budget, so an
// attacker told that login is full cannot move to register.
func TestTheCredentialEndpointsShareOneBucket(t *testing.T) {
	h := apitest.New(t, apitest.HarnessOptions{LoginLimit: 3})

	h.Register(t, "alex@example.com", goodPassword)

	for range 2 {
		require.Equal(t, http.StatusOK, login(t, h, "alex@example.com", goodPassword).Status)
	}

	refused := login(t, h, "alex@example.com", goodPassword)
	require.Equal(t, http.StatusTooManyRequests, refused.Status)
	require.Equal(t, "3", refused.Header.Get("RateLimit-Limit"))
	require.NotEmpty(t, refused.Header.Get("Retry-After"))
	require.NotEqual(t, "0", refused.Header.Get("Retry-After"), "a 0 tells a client to retry now")
}

// TestOneAccountsLimitDoesNotLockOutAnother is why there is a per-account bucket as well.
func TestOneAccountsLimitDoesNotLockOutAnother(t *testing.T) {
	h := apitest.New(t, apitest.HarnessOptions{AccountLimit: 3})

	h.Register(t, "victim@example.com", goodPassword)
	h.Register(t, "bystander@example.com", goodPassword)

	// Registration used one of the victim's three; two wrong guesses, in any letter case, use the rest.
	require.Equal(t, http.StatusUnauthorized, login(t, h, "victim@example.com", "not-the-password").Status)
	require.Equal(t, http.StatusUnauthorized, login(t, h, "VICTIM@example.com", "not-the-password").Status)

	require.Equal(t, http.StatusTooManyRequests, login(t, h, "victim@example.com", goodPassword).Status,
		"even the right password waits out the window")
	require.Equal(t, http.StatusOK, login(t, h, "bystander@example.com", goodPassword).Status,
		"another account is not affected")
}

// TestASuccessfulLoginClearsTheAccountsBucket is what the bucket is for: counting guesses.
func TestASuccessfulLoginClearsTheAccountsBucket(t *testing.T) {
	h := apitest.New(t, apitest.HarnessOptions{AccountLimit: 3})

	h.Register(t, "alex@example.com", goodPassword)

	// Registration and one typo use two of three; the success uses the third and clears the bucket.
	require.Equal(t, http.StatusUnauthorized, login(t, h, "alex@example.com", "not-the-password").Status)
	require.Equal(t, http.StatusOK, login(t, h, "alex@example.com", goodPassword).Status)

	// A fresh budget of three. Without the reset, the next attempt would be the fourth and refused.
	for range 3 {
		require.Equal(t, http.StatusUnauthorized, login(t, h, "alex@example.com", "not-the-password").Status)
	}

	require.Equal(t, http.StatusTooManyRequests, login(t, h, "alex@example.com", goodPassword).Status,
		"three wrong guesses still fill it")
}
