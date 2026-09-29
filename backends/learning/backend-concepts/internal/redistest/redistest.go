// Package redistest is the Redis half of this module's test harness.
//
// Same decisions as internal/pgtest, same reasons: skip when there is no server rather than fail, and give
// each test binary its own namespace so packages running concurrently do not collide.
//
// The namespace here is a Redis DATABASE number rather than a key prefix, because SCAN and FLUSHDB are much
// easier to reason about per database, and because a prefix that one test forgets to apply silently reads
// another test's keys. Redis has 16 databases by default and this module needs a handful.
package redistest

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// DefaultAddr is a local Redis on its usual port.
const DefaultAddr = "localhost:6379"

// Addr returns the address from the environment or the default.
func Addr() string {
	if addr := os.Getenv("REDIS_ADDR"); addr != "" {
		return addr
	}
	return DefaultAddr
}

var (
	once      sync.Once
	client    *redis.Client
	clientErr error
)

// Client returns a shared client, or skips the test.
func Client(t testing.TB) *redis.Client {
	t.Helper()

	once.Do(func() { client, clientErr = connect() })

	if clientErr != nil {
		t.Skipf("no Redis available (%v)\n"+
			"  docker compose up -d, or: brew services start redis\n"+
			"  or point it elsewhere: REDIS_ADDR=host:6379 go test ./...",
			clientErr)
	}

	return client
}

func connect() (*redis.Client, error) {
	c := redis.NewClient(&redis.Options{
		Addr: Addr(),
		DB:   databaseFor(os.Args[0]),

		// Short timeouts, because a test suite waiting 30 seconds for a dead Redis is a test
		// suite that gets run with -short forever.
		DialTimeout:  2 * time.Second,
		ReadTimeout:  2 * time.Second,
		WriteTimeout: 2 * time.Second,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := c.Ping(ctx).Err(); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("pinging %s: %w", Addr(), err)
	}

	return c, nil
}

// databaseFor picks a Redis database number from the test binary's name.
//
// Databases 1 to 15, leaving 0 alone: a developer poking at Redis by hand is on 0, and a test suite that
// flushes 0 deletes whatever they were looking at.
//
// A hash rather than a counter, because the number has to be stable across runs of the same package and
// there is nothing shared between the binaries to count with. Collisions are possible with more than 15
// packages and the consequence is two packages sharing a database, which is the situation without this at all.
func databaseFor(binary string) int {
	base := filepath.Base(binary)
	base = strings.TrimSuffix(strings.TrimSuffix(base, ".exe"), ".test")

	h := fnv.New32a()
	_, _ = h.Write([]byte(base))

	return int(h.Sum32()%15) + 1
}

// Flush empties this test binary's database, if the database is this harness's to empty.
//
// FLUSHDB rather than deleting keys by pattern, because it is one round trip and it cannot miss a key.
//
// # Why it checks first
//
// The first version flushed unconditionally and justified it by staying off database 0. That protects a
// developer poking at Redis by hand and nobody else: a local app can use any of the 16 databases, and the
// default address is the developer's own Redis on 6379. `go test ./...` emptied whichever database the hash
// picked, without asking. See flushOwned.
func Flush(t testing.TB) {
	t.Helper()

	c := Client(t)

	if err := flushOwned(context.Background(), c); err != nil {
		t.Fatalf("%v\n"+
			"  point REDIS_ADDR at a Redis you don't mind emptying (docker compose up -d serves one on\n"+
			"  localhost:6380), or empty that database yourself if its contents don't matter", err)
	}
}

// ErrForeignDatabase means the database holds keys this harness did not write, so it will not flush it.
var ErrForeignDatabase = errors.New("refusing to FLUSHDB a database this test harness does not own")

// MarkerKey is written after every flush. A database that holds keys but not this one was filled by
// something else.
const MarkerKey = "learn-go:test-harness"

// flushOwned empties the database if it is empty already or carries MarkerKey, and refuses otherwise.
//
// An empty database is safe to claim: nothing is lost. A database with the marker was last flushed by this
// harness, so whatever is in it was written by tests. Anything else might be someone's data, and a test
// suite has no business deleting it.
func flushOwned(ctx context.Context, c *redis.Client) error {
	n, err := c.DBSize(ctx).Result()
	if err != nil {
		return fmt.Errorf("sizing the test database: %w", err)
	}

	if n > 0 {
		owned, err := c.Exists(ctx, MarkerKey).Result()
		if err != nil {
			return fmt.Errorf("checking the test database: %w", err)
		}

		if owned == 0 {
			return fmt.Errorf("%w: database %d at %s holds %d key(s) and no %q marker",
				ErrForeignDatabase, c.Options().DB, c.Options().Addr, n, MarkerKey)
		}
	}

	if err := c.FlushDB(ctx).Err(); err != nil {
		return fmt.Errorf("flushing the test database: %w", err)
	}

	return c.Set(ctx, MarkerKey, "emptied by this repository's test harness; safe to delete", 0).Err()
}

// Database reports which database this binary is using, for a log line.
func Database() int { return databaseFor(os.Args[0]) }
