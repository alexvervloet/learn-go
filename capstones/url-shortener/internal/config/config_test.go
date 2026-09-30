package config_test

import (
	"strings"
	"testing"
	"time"

	"github.com/alexvervloet/learn-go/capstones/url-shortener/internal/config"
	"github.com/stretchr/testify/require"
)

// set puts the environment in a known state for one test.
//
// t.Setenv restores the previous value at the end and makes the test refuse to run in parallel, which is
// correct: the process environment is global and two tests changing it at once is a race the detector cannot
// see because it is in the C library.
func set(t *testing.T, kv map[string]string) {
	t.Helper()

	for _, key := range []string{"ADDR", "DATABASE_URL", "REDIS_ADDR", "JWT_SECRET", "TOKEN_TTL", "CACHE_TTL", "BASE_URL"} {
		t.Setenv(key, "")
	}

	for k, v := range kv {
		t.Setenv(k, v)
	}
}

const goodSecret = "a-secret-that-is-at-least-thirty-two-bytes"

// TestEveryProblemIsReportedAtOnce is why Load collects rather than returning the first.
func TestEveryProblemIsReportedAtOnce(t *testing.T) {
	set(t, nil)

	_, err := config.Load()
	require.ErrorIs(t, err, config.ErrMissing)

	require.Contains(t, err.Error(), "DATABASE_URL")
	require.Contains(t, err.Error(), "JWT_SECRET",
		"fixing one variable per restart is several restarts")
}

// TestAShortSecretIsRefused is a hard floor rather than a warning.
func TestAShortSecretIsRefused(t *testing.T) {
	set(t, map[string]string{
		"DATABASE_URL": "postgres:///db",
		"JWT_SECRET":   "too-short",
	})

	_, err := config.Load()
	require.ErrorContains(t, err, "needs at least 32",
		"HMAC-SHA256's security is bounded by the key length, and a warning in a log is a warning nobody reads")
}

// TestThereIsNoDefaultSecret is the decision that matters most in this package.
func TestThereIsNoDefaultSecret(t *testing.T) {
	set(t, map[string]string{"DATABASE_URL": "postgres:///db"})

	_, err := config.Load()
	require.ErrorContains(t, err, "JWT_SECRET",
		"a default secret is a secret everyone knows, and a service that starts with one is a service "+
			"anyone can mint a token for")
}

// TestDefaultsApplyWhereTheyAreSafe covers the other half.
func TestDefaultsApplyWhereTheyAreSafe(t *testing.T) {
	set(t, map[string]string{
		"DATABASE_URL": "postgres:///db",
		"JWT_SECRET":   goodSecret,
	})

	cfg, err := config.Load()
	require.NoError(t, err)

	require.Equal(t, ":8080", cfg.Addr)
	require.Equal(t, "localhost:6379", cfg.RedisAddr)
	require.Equal(t, "http://localhost:8080", cfg.BaseURL)
	require.Equal(t, time.Hour, cfg.TokenTTL)
	require.Equal(t, 5*time.Minute, cfg.CacheTTL)
}

// TestDurationsAcceptBothForms is the compose-file-from-somewhere-else case.
func TestDurationsAcceptBothForms(t *testing.T) {
	tests := []struct {
		raw  string
		want time.Duration
	}{
		{"30s", 30 * time.Second},
		{"2h", 2 * time.Hour},
		{"90", 90 * time.Second},
		{"1h30m", 90 * time.Minute},
	}

	for _, tc := range tests {
		t.Run(tc.raw, func(t *testing.T) {
			set(t, map[string]string{
				"DATABASE_URL": "postgres:///db",
				"JWT_SECRET":   goodSecret,
				"TOKEN_TTL":    tc.raw,
			})

			cfg, err := config.Load()
			require.NoError(t, err)
			require.Equal(t, tc.want, cfg.TokenTTL)
		})
	}
}

// TestATypoInADurationIsAnError rather than a silent fallback.
func TestATypoInADurationIsAnError(t *testing.T) {
	set(t, map[string]string{
		"DATABASE_URL": "postgres:///db",
		"JWT_SECRET":   goodSecret,
		"CACHE_TTL":    "5 minutes",
	})

	_, err := config.Load()
	require.ErrorContains(t, err, "CACHE_TTL",
		"a silent fallback to the default means a value nobody notices is being ignored")
}

// TestBaseURLIsNotDerivedFromTheRequest is a comment in the package, made checkable.
//
// Behind a proxy, the request's Host is whatever the proxy sends and the scheme is http even when the client
// used https. A short URL built from those is a short URL that does not work.
func TestBaseURLIsNotDerivedFromTheRequest(t *testing.T) {
	set(t, map[string]string{
		"DATABASE_URL": "postgres:///db",
		"JWT_SECRET":   goodSecret,
		"BASE_URL":     "https://sho.rt",
	})

	cfg, err := config.Load()
	require.NoError(t, err)
	require.Equal(t, "https://sho.rt", cfg.BaseURL)
	require.True(t, strings.HasPrefix(cfg.BaseURL, "https://"))
}

// TestADurationUnderASecondIsRefused moves the failure to startup. A 500ms token TTL used to load fine and fail
// on the first login, because a JWT's times are whole seconds.
func TestADurationUnderASecondIsRefused(t *testing.T) {
	for _, raw := range []string{"500ms", "0", "0s", "-5m", "-30"} {
		t.Run(raw, func(t *testing.T) {
			set(t, map[string]string{
				"DATABASE_URL": "postgres:///db", "JWT_SECRET": goodSecret, "TOKEN_TTL": raw,
			})

			_, err := config.Load()
			require.ErrorContains(t, err, "TOKEN_TTL")
		})
	}
}
