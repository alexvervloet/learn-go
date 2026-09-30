// Package config reads the service's configuration from the environment.
//
// # Why the environment and not a file
//
// Because the service runs in a container, and a container's configuration arrives as environment variables.
// A file would have to be mounted, which is another thing to get wrong in a compose file and another thing to
// keep out of the image.
//
// # Why it fails at startup rather than at first use
//
// A missing JWT secret discovered on the first login is an outage that starts when a user arrives. Discovered
// at startup it is a container that does not come up, which the orchestrator reports and the previous version
// keeps serving through. Load returns every problem at once, because fixing one per restart is several
// restarts.
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

	// JWTSecret signs the tokens. There is no default, on purpose: a default secret is a secret everyone
	// knows, and a service that starts with one is a service anyone can mint an admin token for.
	JWTSecret []byte

	TokenTTL time.Duration
	CacheTTL time.Duration

	// BaseURL is what a short link looks like to the outside world. It cannot be derived from the request,
	// because behind a proxy the request's Host is whatever the proxy sends and the scheme is http even when
	// the client used https.
	BaseURL string
}

// ErrMissing is returned when a required variable is unset.
var ErrMissing = errors.New("config: required environment variable is unset")

// Load reads the configuration, returning every problem at once.
func Load() (*Config, error) {
	var problems []string

	cfg := &Config{
		Addr:        envOr("ADDR", ":8080"),
		DatabaseURL: os.Getenv("DATABASE_URL"),
		RedisAddr:   envOr("REDIS_ADDR", "localhost:6379"),
		BaseURL:     envOr("BASE_URL", "http://localhost:8080"),
	}

	if cfg.DatabaseURL == "" {
		problems = append(problems, "DATABASE_URL")
	}

	secret := os.Getenv("JWT_SECRET")

	switch {
	case secret == "":
		problems = append(problems, "JWT_SECRET")
	case len(secret) < 32:
		// 32 bytes, because HMAC-SHA256's security is bounded by the key length and a short key is brute
		// forceable offline once an attacker has one token. This is a hard floor rather than a warning: a
		// warning in a log is a warning nobody reads.
		problems = append(problems, fmt.Sprintf("JWT_SECRET is %d bytes, needs at least 32", len(secret)))
	default:
		cfg.JWTSecret = []byte(secret)
	}

	var err error

	if cfg.TokenTTL, err = durationOr("TOKEN_TTL", time.Hour); err != nil {
		problems = append(problems, err.Error())
	}

	if cfg.CacheTTL, err = durationOr("CACHE_TTL", 5*time.Minute); err != nil {
		problems = append(problems, err.Error())
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

// durationOr parses a duration, accepting a bare number of seconds as well.
//
// Both forms exist in the wild: "30s" is Go's, "30" is what most other stacks use. Accepting both means a
// compose file copied from somewhere else works, and rejecting a value that is neither means a typo is an
// error rather than a silent fallback to the default.
func durationOr(key string, fallback time.Duration) (time.Duration, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback, nil
	}

	d, err := time.ParseDuration(raw)
	if err != nil {
		seconds, atoiErr := strconv.Atoi(raw)
		if atoiErr != nil {
			return 0, fmt.Errorf("%s=%q is neither a duration nor a number of seconds", key, raw)
		}

		d = time.Duration(seconds) * time.Second
	}

	// At least a second, for every duration here. Zero and negative values are always mistakes. A sub-second
	// token TTL truncates to zero in a JWT, whose times are whole seconds, and auth refuses it on the first
	// login rather than at startup. A sub-second window or cache TTL is not a setting anyone means. Refusing
	// here moves all of that to the moment the service starts.
	if d < time.Second {
		return 0, fmt.Errorf("%s=%q must be at least one second", key, raw)
	}

	return d, nil
}
