package config_test

import (
	"testing"
	"time"

	"github.com/alexvervloet/learn-go/capstones/bookmark-manager/internal/config"
	"github.com/stretchr/testify/require"
)

const goodSecret = "a-secret-that-is-at-least-thirty-two-bytes"

// set puts the environment in a known state.
//
// t.Setenv restores the previous value and makes the test refuse to run in parallel, which is correct: the
// process environment is global and two tests changing it at once is a race the detector cannot see, because
// it happens inside libc.
func set(t *testing.T, kv map[string]string) {
	t.Helper()

	for _, key := range []string{
		"ADDR", "DATABASE_URL", "REDIS_ADDR", "JWT_SECRET",
		"ACCESS_TTL", "REFRESH_TTL", "LOGIN_LIMIT", "LOGIN_WINDOW",
	} {
		t.Setenv(key, "")
	}

	for k, v := range kv {
		t.Setenv(k, v)
	}
}

// TestEveryProblemIsReportedAtOnce is why Load collects.
func TestEveryProblemIsReportedAtOnce(t *testing.T) {
	set(t, nil)

	_, err := config.Load()
	require.ErrorIs(t, err, config.ErrMissing)
	require.Contains(t, err.Error(), "DATABASE_URL")
	require.Contains(t, err.Error(), "JWT_SECRET", "fixing one variable per restart is several restarts")
}

// TestThereIsNoDefaultSecret is the decision that matters most here.
func TestThereIsNoDefaultSecret(t *testing.T) {
	set(t, map[string]string{"DATABASE_URL": "postgres:///db"})

	_, err := config.Load()
	require.ErrorContains(t, err, "JWT_SECRET",
		"a default secret is a secret everyone knows")
}

// TestAShortSecretIsRefused is a hard floor rather than a warning.
func TestAShortSecretIsRefused(t *testing.T) {
	set(t, map[string]string{"DATABASE_URL": "postgres:///db", "JWT_SECRET": "too-short"})

	_, err := config.Load()
	require.ErrorContains(t, err, "needs at least 32",
		"HMAC-SHA256's security is bounded by the key length, and a warning in a log is a warning nobody reads")
}

// TestARefreshTTLShorterThanTheAccessTTLIsRefused is a configuration nothing else would catch.
//
// It is syntactically fine and logically useless: the refresh token dies before the access token it is meant
// to replace, so a user is logged out at the worst possible moment and no component reports anything wrong.
func TestARefreshTTLShorterThanTheAccessTTLIsRefused(t *testing.T) {
	set(t, map[string]string{
		"DATABASE_URL": "postgres:///db",
		"JWT_SECRET":   goodSecret,
		"ACCESS_TTL":   "1h",
		"REFRESH_TTL":  "30m",
	})

	_, err := config.Load()
	require.ErrorContains(t, err, "must be longer than ACCESS_TTL")
}

// TestDefaultsApplyWhereTheyAreSafe covers the other half.
func TestDefaultsApplyWhereTheyAreSafe(t *testing.T) {
	set(t, map[string]string{"DATABASE_URL": "postgres:///db", "JWT_SECRET": goodSecret})

	cfg, err := config.Load()
	require.NoError(t, err)

	require.Equal(t, ":8080", cfg.Addr)
	require.Equal(t, "localhost:6379", cfg.RedisAddr)
	require.Equal(t, 15*time.Minute, cfg.AccessTTL)
	require.Equal(t, 30*24*time.Hour, cfg.RefreshTTL)
	require.Equal(t, 20, cfg.LoginLimit)
	require.Equal(t, time.Minute, cfg.LoginWindow)

	// The relationship the service depends on.
	require.Greater(t, cfg.RefreshTTL, cfg.AccessTTL)
}

// TestDurationsAcceptBothForms is the compose-file-from-somewhere-else case.
func TestDurationsAcceptBothForms(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want time.Duration
	}{
		{"30s", 30 * time.Second},
		{"2h", 2 * time.Hour},
		{"900", 15 * time.Minute},
		{"1h30m", 90 * time.Minute},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			set(t, map[string]string{
				"DATABASE_URL": "postgres:///db", "JWT_SECRET": goodSecret, "ACCESS_TTL": tc.raw,
			})

			cfg, err := config.Load()
			require.NoError(t, err)
			require.Equal(t, tc.want, cfg.AccessTTL)
		})
	}
}

// TestATypoIsAnErrorRatherThanADefault covers the silent-fallback trap.
func TestATypoIsAnErrorRatherThanADefault(t *testing.T) {
	set(t, map[string]string{
		"DATABASE_URL": "postgres:///db", "JWT_SECRET": goodSecret, "LOGIN_WINDOW": "1 minute",
	})

	_, err := config.Load()
	require.ErrorContains(t, err, "LOGIN_WINDOW",
		"a silently ignored setting is worse than a refusal to start")
}

// TestANonPositiveLimitIsRefused covers the other numeric field.
func TestANonPositiveLimitIsRefused(t *testing.T) {
	for _, raw := range []string{"0", "-5", "many"} {
		set(t, map[string]string{
			"DATABASE_URL": "postgres:///db", "JWT_SECRET": goodSecret, "LOGIN_LIMIT": raw,
		})

		_, err := config.Load()
		require.ErrorContains(t, err, "LOGIN_LIMIT", "LOGIN_LIMIT=%q", raw)
	}
}
