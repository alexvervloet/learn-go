// Package jobtest runs a real asynq worker against a real Redis for a test.
//
// # Why not a fake
//
// asynq's behaviour IS the subject: the retry backoff, the lease recovery, the queue weighting and the
// scheduled-task promotion all live in its Redis scripts. A fake would test the handlers, which are the least
// interesting part.
//
// So these tests need Redis, and they skip without one, the same decision every other module in this repo makes.
//
// # Isolation
//
// Each test binary gets a Redis DATABASE, and each test gets a FLUSH plus its own queue names. The database
// alone is not enough: asynq stores its state under fixed key prefixes, so two tests in one binary share a
// queue unless the queue names differ.
package jobtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
)

// DefaultAddr is a local Redis.
const DefaultAddr = "localhost:6379"

// Addr returns the address from the environment or the default.
func Addr() string {
	if addr := os.Getenv("REDIS_ADDR"); addr != "" {
		return addr
	}
	return DefaultAddr
}

var (
	once     sync.Once
	ok       bool
	checkErr error
)

// database picks a Redis database from the test binary's name, leaving 0 alone.
func database() int {
	base := filepath.Base(os.Args[0])
	base = strings.TrimSuffix(strings.TrimSuffix(base, ".exe"), ".test")

	h := fnv.New32a()
	_, _ = h.Write([]byte("jobs-" + base))

	return int(h.Sum32()%15) + 1
}

// RedisOpt returns the connection options asynq needs.
func RedisOpt() asynq.RedisClientOpt {
	return asynq.RedisClientOpt{Addr: Addr(), DB: database()}
}

// Require skips the test unless Redis is reachable.
func Require(t testing.TB) {
	t.Helper()

	once.Do(func() { ok, checkErr = check() })

	if !ok {
		t.Skipf("no Redis at %s (%v)\n"+
			"  docker compose up -d, or: brew services start redis\n"+
			"  or point it elsewhere: REDIS_ADDR=host:6379 go test ./...",
			Addr(), checkErr)
	}
}

func check() (bool, error) {
	c := redis.NewClient(&redis.Options{
		Addr:        Addr(),
		DB:          database(),
		DialTimeout: 2 * time.Second,
	})
	defer func() { _ = c.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := c.Ping(ctx).Err(); err != nil {
		return false, err
	}

	return true, nil
}

// Flush empties this binary's database, if the database is this harness's to empty.
//
// The first version flushed unconditionally. The default address is the developer's own Redis, and staying
// off database 0 protects someone poking at Redis by hand and nobody else: a local app can use any of the 16
// databases, and `go test ./...` emptied whichever one the hash picked. See flushOwned.
func Flush(t testing.TB) {
	t.Helper()

	Require(t)

	c := redis.NewClient(&redis.Options{Addr: Addr(), DB: database()})
	defer func() { _ = c.Close() }()

	if err := flushOwned(context.Background(), c); err != nil {
		t.Fatalf("%v\n"+
			"  point REDIS_ADDR at a Redis you don't mind emptying (docker compose up -d serves one on\n"+
			"  localhost:6381), or empty that database yourself if its contents don't matter", err)
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
// harness, so whatever is in it was written by tests. Anything else might be someone's data.
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

// QueueName returns a queue name unique to this test.
//
// Per TEST, not per binary. asynq keys its state on the queue name, so two tests in one binary sharing a queue
// see each other's tasks, and the second one's assertions are about the first one's leftovers.
func QueueName(t testing.TB) string {
	t.Helper()

	suffix := make([]byte, 4)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatalf("generating a queue suffix: %v", err)
	}

	name := sanitise(t.Name()) + "-" + hex.EncodeToString(suffix)

	if len(name) > 60 {
		name = name[:60]
	}

	return name
}

func sanitise(name string) string {
	var b strings.Builder

	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}

	return strings.Trim(b.String(), "-")
}

// DiscardLogger is a logger that writes nowhere, for the tests that do not read the output.
func DiscardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// Client returns an asynq client that is closed when the test ends.
func Client(t testing.TB) *asynq.Client {
	t.Helper()

	Require(t)

	c := asynq.NewClient(RedisOpt())

	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Errorf("closing the client: %v", err)
		}
	})

	return c
}

// Inspector returns an asynq inspector, which is the API behind the web dashboard.
func Inspector(t testing.TB) *asynq.Inspector {
	t.Helper()

	Require(t)

	i := asynq.NewInspector(RedisOpt())

	t.Cleanup(func() {
		if err := i.Close(); err != nil {
			t.Errorf("closing the inspector: %v", err)
		}
	})

	return i
}

// RunServer starts a worker and stops it when the test ends.
//
// # Start against Run
//
// asynq.Server has Run (blocks, installs signal handlers) and Start (returns immediately). Run is right for a
// binary and wrong for a test: it catches SIGINT, so a test that used it would swallow ctrl-C.
func RunServer(t testing.TB, srv *asynq.Server, mux *asynq.ServeMux) {
	t.Helper()

	if err := srv.Start(mux); err != nil {
		t.Fatalf("starting the worker: %v", err)
	}

	t.Cleanup(func() {
		// Shutdown waits for in-flight tasks up to the server's ShutdownTimeout. Stop only stops
		// PULLING new ones, so a test that called Stop and asserted immediately would race a
		// handler still running.
		srv.Shutdown()
	})
}

// WaitFor polls until cond is true or the deadline passes.
//
// # Why polling rather than a channel
//
// The thing being waited for is a state in Redis that asynq will reach on its own schedule: a retry after a
// backoff, a scheduled task promoted by the forwarder, a lease recovered after it expires. There is nothing to
// signal on, so the test polls.
//
// The alternative is a sleep long enough to be safe, which is either flaky or slow. Polling at 10ms is neither.
func WaitFor(t testing.TB, timeout time.Duration, what string, cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		if cond() {
			return
		}

		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("timed out after %v waiting for %s", timeout, what)
}

// QueueState is a snapshot of a queue, for assertions and log lines.
type QueueState struct {
	Pending   int
	Active    int
	Scheduled int
	Retry     int
	Archived  int
	Completed int
	Processed int
	Failed    int
}

// String formats it.
func (q QueueState) String() string {
	return fmt.Sprintf("pending=%d active=%d scheduled=%d retry=%d archived=%d completed=%d "+
		"processed=%d failed=%d",
		q.Pending, q.Active, q.Scheduled, q.Retry, q.Archived, q.Completed,
		q.Processed, q.Failed)
}

// State reads a queue's counts.
//
// Returns a zero QueueState for a queue asynq has never seen, rather than an error. A queue only exists once
// something is enqueued to it, so "not found" is the normal state at the start of a test and treating it as a
// failure would make every test start with a workaround.
func State(t testing.TB, inspector *asynq.Inspector, queue string) QueueState {
	t.Helper()

	info, err := inspector.GetQueueInfo(queue)
	if err != nil {
		return QueueState{}
	}

	return QueueState{
		Pending:   info.Pending,
		Active:    info.Active,
		Scheduled: info.Scheduled,
		Retry:     info.Retry,
		Archived:  info.Archived,
		Completed: info.Completed,
		Processed: info.Processed,
		Failed:    info.Failed,
	}
}
