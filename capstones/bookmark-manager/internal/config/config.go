// Package config reads the service's configuration from the environment and validates it at startup.
//
// A missing JWT secret discovered on the first login is an outage that starts when a user arrives. Discovered
// at startup it is a container that does not come up, which the orchestrator reports and the previous version
// keeps serving through.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is everything the service needs.
type Config struct {
	Addr        string
	DatabaseURL string
	RedisAddr   string

	JWTSecret []byte

	// AccessTTL is short because an access token cannot be revoked. Fifteen minutes is how long you are
	// willing to be wrong about a logout.
	AccessTTL time.Duration

	// RefreshTTL is long because the refresh token IS revocable, so a long life costs nothing.
	RefreshTTL time.Duration

	LoginLimit  int
	LoginWindow time.Duration
}

// ErrMissing is returned when required configuration is absent or wrong.
var ErrMissing = errors.New("config: required environment variable is unset or invalid")

// Load reads the configuration, returning every problem at once.
func Load() (*Config, error) {
	var problems []string

	cfg := &Config{
		Addr:        envOr("ADDR", ":8080"),
		DatabaseURL: os.Getenv("DATABASE_URL"),
		RedisAddr:   envOr("REDIS_ADDR", "localhost:6379"),
	}

	if cfg.DatabaseURL == "" {
		problems = append(problems, "DATABASE_URL")
	}

	secret := os.Getenv("JWT_SECRET")

	switch {
	case secret == "":
		problems = append(problems, "JWT_SECRET")
	case len(secret) < 32:
		problems = append(problems, fmt.Sprintf("JWT_SECRET is %d bytes, needs at least 32", len(secret)))
	default:
		cfg.JWTSecret = []byte(secret)
	}

	var err error

	if cfg.AccessTTL, err = durationOr("ACCESS_TTL", 15*time.Minute); err != nil {
		problems = append(problems, err.Error())
	}

	if cfg.RefreshTTL, err = durationOr("REFRESH_TTL", 30*24*time.Hour); err != nil {
		problems = append(problems, err.Error())
	}

	if cfg.LoginWindow, err = durationOr("LOGIN_WINDOW", time.Minute); err != nil {
		problems = append(problems, err.Error())
	}

	if cfg.LoginLimit, err = intOr("LOGIN_LIMIT", 20); err != nil {
		problems = append(problems, err.Error())
	}

	// A refresh token that expires before the access token is a configuration that logs everyone out at the
	// worst possible moment, and nothing else in the program would notice.
	if cfg.RefreshTTL > 0 && cfg.AccessTTL > 0 && cfg.RefreshTTL <= cfg.AccessTTL {
		problems = append(problems, fmt.Sprintf(
			"REFRESH_TTL (%s) must be longer than ACCESS_TTL (%s), or refreshing is pointless",
			cfg.RefreshTTL, cfg.AccessTTL))
	}

	if len(problems) > 0 {
		return nil, fmt.Errorf("%w: %s", ErrMissing, strings.Join(problems, ", "))
	}

	return cfg, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}

	return fallback
}

// durationOr accepts Go's form ("30s") and a bare number of seconds.
//
// Both exist in the wild, so a compose file copied from another stack works. A value that is neither is an
// error rather than a silent fallback, because a silently ignored setting is worse than a refusal to start.
func durationOr(key string, fallback time.Duration) (time.Duration, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback, nil
	}

	if d, err := time.ParseDuration(raw); err == nil {
		return d, nil
	}

	seconds, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s=%q is neither a duration nor a number of seconds", key, raw)
	}

	return time.Duration(seconds) * time.Second, nil
}

func intOr(key string, fallback int) (int, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback, nil
	}

	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%s=%q must be a positive number", key, raw)
	}

	return n, nil
}
