// Package pgxdemo is the connection pool: how it is configured, how it runs out, and the four ways pgx
// can send a query.
//
// # Why there is no async layer
//
// Go needs no async database machinery: a goroutine blocking on a socket costs nothing and there is no
// event loop to starve. What matters is the part that actually causes outages: the pool. Its size, what
// happens when it is empty, and the fact that "concurrent" in Go is bounded by MaxConns and not by
// GOMAXPROCS.
//
// # Pool sizing, which is not "more is better"
//
// Postgres runs one backend PROCESS per connection, each with its own memory. A pool of 500 against a
// database sized for 100 does not go 5x faster, it goes slower and then falls over, because every
// backend competes for the same work_mem and the same shared buffers.
//
// The useful starting point is small. The often-quoted formula is connections = cores * 2 + effective
// spindles, which for a modern 8-core server on SSD lands around 20. Then the arithmetic that matters:
// with 20 connections and queries averaging 2ms, the pool can serve 10,000 queries a second. Most
// services do not need that and reach for 200 connections anyway.
//
// # The five query modes
//
// pgx can send a query five ways, and the default is not the obvious one:
//
//	QueryExecModeCacheStatement  the default. Prepares the statement, caches it per connection
//	                             by SQL text, reuses it. Fastest for repeated queries, and the
//	                             reason a cached bad plan can outlive a deploy.
//	QueryExecModeCacheDescribe   caches the DESCRIBE but does not name a prepared statement.
//	                             Needed in front of PgBouncer in transaction mode, where a
//	                             prepared statement may land on a different server connection.
//	QueryExecModeDescribeExec    describes and executes every time. Two round trips, no cache.
//	QueryExecModeExec            one round trip, no prepare, no describe. pgx sends the
//	                             parameters as text and lets Postgres infer the types, which is
//	                             occasionally wrong and always simple.
//	QueryExecModeSimpleProtocol  interpolates the arguments into the SQL string. pgx quotes them
//	                             correctly, so it is not an injection hole, but there are no
//	                             parameters on the wire at all and no type checking.
//
// The one to know about is the first, because "my query got slow and a restart fixed it" is usually a
// cached generic plan. The nplusone package measures that happening.
package pgxdemo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Config is the pool settings worth naming, with the defaults stated.
type Config struct {
	// MaxConns caps concurrency. pgx defaults to max(4, runtime.NumCPU()), which is a
	// developer-laptop default and is usually wrong in both directions: too small for a service
	// doing I/O-bound work, too large if a hundred replicas share one database.
	MaxConns int32

	// MinConns keeps idle connections open so a burst does not pay for handshakes. pgx defaults
	// to 0, meaning the first request after a quiet period pays the TCP plus TLS plus
	// authentication cost, which on a managed database is tens of milliseconds.
	MinConns int32

	// MaxConnLifetime retires a connection after this long even if it is healthy. This is what
	// lets a rolling database upgrade or a failover drain gracefully, and what stops a connection
	// accumulating server-side state forever. pgx defaults to an hour.
	MaxConnLifetime time.Duration

	// MaxConnLifetimeJitter spreads the retirements out. Without it, connections created together
	// at startup all expire together and the pool reconnects in lockstep, which is the same
	// thundering-herd shape as an unjittered retry.
	MaxConnLifetimeJitter time.Duration

	// MaxConnIdleTime closes connections nobody is using. pgx defaults to 30 minutes.
	MaxConnIdleTime time.Duration

	// HealthCheckPeriod is how often the pool prunes and tops up. pgx defaults to a minute.
	HealthCheckPeriod time.Duration
}

// Small is a deliberately tiny pool, for demonstrating exhaustion.
func Small() Config {
	return Config{
		MaxConns:              2,
		MinConns:              1,
		MaxConnLifetime:       time.Minute,
		MaxConnLifetimeJitter: 5 * time.Second,
		MaxConnIdleTime:       30 * time.Second,
		HealthCheckPeriod:     10 * time.Second,
	}
}

// Sensible is a defensible starting point for a service, with the reasoning in the field comments.
func Sensible() Config {
	return Config{
		MaxConns:              20,
		MinConns:              2,
		MaxConnLifetime:       30 * time.Minute,
		MaxConnLifetimeJitter: 5 * time.Minute,
		MaxConnIdleTime:       5 * time.Minute,
		HealthCheckPeriod:     time.Minute,
	}
}

// Open builds a pool from a URL and a Config.
//
// Ping before returning, because pgxpool.New is lazy and a bad password or a missing database otherwise
// surfaces inside the first request rather than at startup. A service that starts successfully and then
// 500s on every request is much harder to diagnose than one that refuses to start.
func Open(ctx context.Context, url string, cfg Config) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parsing the connection string: %w", err)
	}

	poolCfg.MaxConns = cfg.MaxConns
	poolCfg.MinConns = cfg.MinConns
	poolCfg.MaxConnLifetime = cfg.MaxConnLifetime
	poolCfg.MaxConnLifetimeJitter = cfg.MaxConnLifetimeJitter
	poolCfg.MaxConnIdleTime = cfg.MaxConnIdleTime
	poolCfg.HealthCheckPeriod = cfg.HealthCheckPeriod

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("creating the pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pinging: %w", err)
	}

	return pool, nil
}

// ErrPoolExhausted is what a caller wants to distinguish, because it means "shed load", not "the
// database is broken".
//
// pgx does not give it a distinct error: Acquire returns the context's error, so an exhausted pool and
// a slow query look identical from the outside. That is worth knowing, and this wrapper is the reason
// AcquireWithTimeout exists below.
var ErrPoolExhausted = errors.New("connection pool exhausted")

// AcquireWithTimeout takes a connection, or gives up after d and says why.
//
// The distinction matters for what you do next. A timeout waiting for a CONNECTION means the pool is
// the bottleneck and the answer is a 503 with a Retry-After, or a bigger pool. A timeout waiting for a
// QUERY means the database is the bottleneck and a bigger pool makes it worse.
//
// The two are indistinguishable without this, and getting them the wrong way round is how a team
// responds to a slow database by raising MaxConns.
func AcquireWithTimeout(ctx context.Context, pool *pgxpool.Pool, d time.Duration) (*pgxpool.Conn, error) {
	acquireCtx, cancel := context.WithTimeout(ctx, d)
	defer cancel()

	conn, err := pool.Acquire(acquireCtx)
	if err == nil {
		return conn, nil
	}

	// The parent context is fine and only the acquire deadline fired, so the pool is the
	// problem. If the parent is also done, the caller gave up and that is not exhaustion.
	if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
		stat := pool.Stat()
		return nil, fmt.Errorf("%w: %d of %d in use, %d waiting for %v",
			ErrPoolExhausted, stat.AcquiredConns(), stat.MaxConns(),
			stat.EmptyAcquireCount(), d)
	}

	return nil, fmt.Errorf("acquiring a connection: %w", err)
}

// Stats is the pool's state, named so a metrics exporter has something to range over.
//
// These six numbers are what to put on a dashboard. AcquiredConns against MaxConns is saturation;
// EmptyAcquireCount rising is the leading indicator of the outage, because it counts the times a
// caller had to WAIT, which happens long before anything times out.
type Stats struct {
	Acquired          int32
	Idle              int32
	Total             int32
	Max               int32
	EmptyAcquireCount int64
	CanceledAcquire   int64
}

// Snapshot reads the pool's counters.
func Snapshot(pool *pgxpool.Pool) Stats {
	s := pool.Stat()

	return Stats{
		Acquired:          s.AcquiredConns(),
		Idle:              s.IdleConns(),
		Total:             s.TotalConns(),
		Max:               s.MaxConns(),
		EmptyAcquireCount: s.EmptyAcquireCount(),
		CanceledAcquire:   s.CanceledAcquireCount(),
	}
}

// String formats the stats for a log line.
func (s Stats) String() string {
	return fmt.Sprintf("%d/%d acquired, %d idle, %d total; %d waits, %d cancelled",
		s.Acquired, s.Max, s.Idle, s.Total, s.EmptyAcquireCount, s.CanceledAcquire)
}

// Saturation is the number to alert on: how much of the pool is in use right now.
func (s Stats) Saturation() float64 {
	if s.Max == 0 {
		return 0
	}
	return float64(s.Acquired) / float64(s.Max)
}

// CountBooks is a trivial query, used to compare the query modes.
func CountBooks(ctx context.Context, q interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}, authorID int64) (int, error) {
	var n int

	if err := q.QueryRow(ctx, "SELECT count(*) FROM books WHERE author_id = $1", authorID).
		Scan(&n); err != nil {
		return 0, fmt.Errorf("counting books for author %d: %w", authorID, err)
	}

	return n, nil
}

// Modes is every query mode with its name, so a test or a benchmark can range over them.
//
// The names are not in pgx: QueryExecMode has no String method that prints these, so a table here is
// the only way to label the results.
func Modes() []struct {
	Name string
	Mode pgx.QueryExecMode
} {
	return []struct {
		Name string
		Mode pgx.QueryExecMode
	}{
		{"CacheStatement (default)", pgx.QueryExecModeCacheStatement},
		{"CacheDescribe", pgx.QueryExecModeCacheDescribe},
		{"DescribeExec", pgx.QueryExecModeDescribeExec},
		{"Exec", pgx.QueryExecModeExec},
		{"SimpleProtocol", pgx.QueryExecModeSimpleProtocol},
	}
}

// LeakyQuery is the bug this package exists to make visible: a connection acquired and never released.
//
// One leaked connection is invisible. A handler that leaks one per request takes MaxConns requests to
// bring the service down, and the symptom is every request timing out with no errors in the database
// log, because nothing is wrong with the database.
//
// The fix is always the same shape and always one line: defer conn.Release() immediately after the
// error check. The reason it gets missed is that the code works perfectly until the pool is empty.
func LeakyQuery(ctx context.Context, pool *pgxpool.Pool) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquiring: %w", err)
	}

	// The missing line:
	//
	//	defer conn.Release()

	var n int
	if err := conn.QueryRow(ctx, "SELECT 1").Scan(&n); err != nil {
		return fmt.Errorf("querying: %w", err)
	}

	return nil
}

// CorrectQuery is the same function with the one line.
func CorrectQuery(ctx context.Context, pool *pgxpool.Pool) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquiring: %w", err)
	}
	defer conn.Release()

	var n int
	if err := conn.QueryRow(ctx, "SELECT 1").Scan(&n); err != nil {
		return fmt.Errorf("querying: %w", err)
	}

	return nil
}
