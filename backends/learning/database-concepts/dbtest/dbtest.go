// Package dbtest is the test harness every other package in this module uses.
//
// # Three problems, three answers
//
// A test that needs a database has to decide what to do when there is not one, how to stop tests
// from seeing each other's rows, and how to stop PACKAGES from seeing each other's rows.
//
// ON A MISSING DATABASE, SKIP. Never fail. A failing test for a missing local Postgres trains
// people to ignore red, and then a real failure goes unnoticed. Skip with a message saying what to
// set, and make CI set it.
//
// FOR ISOLATION, ROLL BACK. Each test gets a transaction that is never committed, so every row it
// writes disappears when it ends. This is the technique the Python mirror uses too, and in Go it is
// four lines because t.Cleanup exists.
//
// # What rollback isolation cannot do
//
// Worth knowing before relying on it:
//
//	it cannot test COMMIT itself, or anything reading from another connection, because the
//	  uncommitted rows are invisible outside the transaction
//	it cannot test isolation levels or deadlocks, which need two real connections
//	a savepoint is needed if the code under test also opens a transaction, because Postgres
//	  has no true nested transactions
//	it does not reset sequences, so an ID is never the same twice and a test asserting id = 1
//	  passes once
//
// The transactions package uses Pool directly for exactly those reasons, and truncates instead.
//
// # Between packages, a separate database
//
// Rollback isolation does nothing about the other kind of collision. `go test ./...` builds one test
// binary per package and runs them CONCURRENTLY, up to GOMAXPROCS of them. Two packages that both
// truncate and reload the same tables then fight, and the symptom is not a clear failure: it is
// `deadlock detected` in one package and a wrong query plan in another, because the dataset one
// package was measuring got emptied underneath it by another.
//
// So each test binary gets its own database, named after itself. It costs one CREATE DATABASE and one
// migration run per package, which is about 120ms, and it buys back the parallelism. The alternative
// is `go test -p 1 ./...` forever, which serialises every package in the module including the ones
// that never touch Postgres.
package dbtest

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// DefaultURL is used when DATABASE_URL is unset, so a developer with a local Postgres needs no
// setup at all.
//
// Pointing at a dedicated database rather than `postgres` is deliberate: the migrations in this
// module drop and recreate tables, and running them against whatever database happens to be default
// is how someone loses work.
const DefaultURL = "postgres:///learn_go_db?sslmode=disable"

// URL returns the connection string, from the environment or the default.
func URL() string {
	if url := os.Getenv("DATABASE_URL"); url != "" {
		return url
	}
	return DefaultURL
}

var (
	poolOnce sync.Once
	pool     *pgxpool.Pool
	poolErr  error
)

// Pool returns a shared connection pool, or skips the test if there is no reachable database.
//
// The pool is created once for the whole test binary, and the migrations run once with it. Creating
// one per test would work and would spend most of the suite's time on TCP handshakes; a pool is
// safe for concurrent use, which is the point of having one.
//
// The context timeout is what turns "no database" into a skip rather than a hang. Without it, a
// DATABASE_URL pointing at a firewalled host makes the suite sit there until the test timeout.
func Pool(t testing.TB) *pgxpool.Pool {
	t.Helper()

	poolOnce.Do(func() { pool, poolErr = connectAndMigrate() })

	if poolErr != nil {
		t.Skipf("no database available (%v)\n"+
			"  start one with: docker compose up -d\n"+
			"  or point DATABASE_URL at your own: DATABASE_URL=postgres:///mydb go test ./...",
			poolErr)
	}

	return pool
}

func connectAndMigrate() (*pgxpool.Pool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cfg, err := pgxpool.ParseConfig(URL())
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", URL(), err)
	}

	// Point the pool at this package's own database, creating it if it is not there. On a
	// database the test user may not create databases in, this logs and stays on the base
	// database, because a skipped suite is worse than a serialised one.
	if name, err := ensureOwnDatabase(ctx, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "dbtest: sharing %s (%v); run with -p 1 if packages collide\n",
			cfg.ConnConfig.Database, err)
	} else {
		cfg.ConnConfig.Database = name
	}

	// A small pool: the transactions package needs several real connections at once, and
	// anything above a handful just hides a leak.
	cfg.MaxConns = 10
	cfg.MinConns = 1

	p, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("creating pool: %w", err)
	}

	// NewWithConfig is lazy: it does not connect until the first query, so without an explicit
	// Ping a missing database is discovered inside the first test rather than here.
	if err := p.Ping(ctx); err != nil {
		p.Close()
		return nil, fmt.Errorf("connecting to %s: %w", URL(), err)
	}

	if err := migrate(ctx, p); err != nil {
		p.Close()
		return nil, fmt.Errorf("migrating: %w", err)
	}

	return p, nil
}

// ensureOwnDatabase creates this test binary's database if it does not exist and returns its name.
//
// Three details in here are each a small trap.
//
// CREATE DATABASE cannot run inside a transaction and has no IF NOT EXISTS, so it needs the
// check-then-create that is normally a race. Two packages starting at the same moment can both see
// "absent" and both try, so duplicate_database is caught and treated as success.
//
// The check runs on the `postgres` maintenance database, not on the target, because you cannot
// connect to a database to ask whether it exists.
//
// The name comes from the test binary, which is the only thing at runtime that knows which package
// is executing. runtime.Caller would name this file in every package.
func ensureOwnDatabase(ctx context.Context, cfg *pgxpool.Config) (string, error) {
	suffix := binarySuffix()
	if suffix == "" {
		return "", errors.New("cannot name a database for this binary")
	}

	name := cfg.ConnConfig.Database + "_" + suffix

	// Postgres truncates identifiers at 63 bytes, and a silently truncated name that collides
	// with another package's is exactly the bug this function exists to prevent.
	if len(name) > 63 {
		return "", fmt.Errorf("database name %q is longer than 63 bytes", name)
	}

	admin := cfg.ConnConfig.Copy()
	admin.Database = "postgres"

	conn, err := pgx.ConnectConfig(ctx, admin)
	if err != nil {
		return "", fmt.Errorf("connecting to the postgres database: %w", err)
	}
	defer func() { _ = conn.Close(ctx) }()

	var exists bool
	if err := conn.QueryRow(ctx,
		"SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)", name).Scan(&exists); err != nil {
		return "", fmt.Errorf("checking for %s: %w", name, err)
	}

	if exists {
		return name, nil
	}

	// Sanitize rather than %s: the name is derived from a filename, and quoting it is one line.
	if _, err := conn.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "42P04" {
			// duplicate_database: another package won the race, which is the outcome
			// this wanted anyway.
			return name, nil
		}
		return "", fmt.Errorf("creating %s: %w", name, err)
	}

	return name, nil
}

// binarySuffix turns the test binary's path into something usable as an identifier.
//
// `go test` compiles each package to <pkg>.test in a build directory, so os.Args[0] ends in
// "indexes.test" or "seed.test". Anything outside [a-z0-9_] becomes an underscore, because the name
// goes into an identifier and a package directory can contain a hyphen.
func binarySuffix() string {
	base := filepath.Base(os.Args[0])
	base = strings.TrimSuffix(base, ".exe")
	base = strings.TrimSuffix(base, ".test")

	var b strings.Builder
	for _, r := range strings.ToLower(base) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}

	return strings.Trim(b.String(), "_")
}

// migrate applies the goose migrations.
//
// goose works on *sql.DB, and pgx exposes a database/sql driver through stdlib.OpenDBFromPool, so
// the same pool serves both. The alternative is a second connection just for migrations, which
// works and is one more thing to configure.
func migrate(ctx context.Context, p *pgxpool.Pool) error {
	db := stdlib.OpenDBFromPool(p)
	defer func() { _ = db.Close() }()

	goose.SetBaseFS(nil)
	goose.SetLogger(goose.NopLogger())

	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("setting dialect: %w", err)
	}

	dir, err := migrationsDir()
	if err != nil {
		return err
	}

	if err := goose.UpContext(ctx, db, dir); err != nil {
		return fmt.Errorf("goose up: %w", err)
	}

	return nil
}

// migrationsDir finds the migrations directory relative to THIS source file.
//
// runtime.Caller rather than os.Getwd, because `go test ./...` runs each package's tests with that
// package's directory as the working directory, so a relative path is wrong from every package
// except this one. This is the standard trick for locating testdata shared across packages.
func migrationsDir() (string, error) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return "", errors.New("dbtest: cannot determine the source path")
	}

	dir := filepath.Join(filepath.Dir(filepath.Dir(thisFile)), "migrations")

	if _, err := os.Stat(dir); err != nil {
		return "", fmt.Errorf("migrations directory: %w", err)
	}

	return dir, nil
}

// Tx returns a transaction that is rolled back when the test ends.
//
// This is the isolation technique. Every write the test makes is invisible to anything else and
// disappears afterwards, so tests can run in any order and, with a big enough pool, in parallel.
//
// The rollback is registered with t.Cleanup rather than deferred by the caller, which matters: a
// caller that forgets the defer leaks a transaction and eventually exhausts the pool, and the
// failure appears in an unrelated test as a timeout.
func Tx(t testing.TB) pgx.Tx {
	t.Helper()

	p := Pool(t)

	ctx := context.Background()

	tx, err := p.Begin(ctx)
	if err != nil {
		t.Fatalf("beginning a transaction: %v", err)
	}

	t.Cleanup(func() {
		// pgx.ErrTxClosed means the test committed or rolled back itself, which is fine
		// and not worth a failure.
		if err := tx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			t.Errorf("rolling back: %v", err)
		}
	})

	return tx
}

// Truncate empties the given tables and resets their sequences.
//
// For the tests rollback isolation cannot serve: anything needing two real connections, a real
// COMMIT, or a predictable sequence value.
//
// TRUNCATE rather than DELETE: it does not scan, it resets the sequence with RESTART IDENTITY, and
// CASCADE follows the foreign keys so the caller does not have to get the order right. It does take
// an ACCESS EXCLUSIVE lock, so tests using it cannot run in parallel with each other.
func Truncate(t testing.TB, tables ...string) {
	t.Helper()

	if len(tables) == 0 {
		return
	}

	p := Pool(t)

	list := ""
	for i, table := range tables {
		if i > 0 {
			list += ", "
		}
		// The tables are compile-time constants from this module's own tests, so string
		// concatenation is safe here. Anywhere a table name could come from a request,
		// this needs pgx.Identifier{}.Sanitize().
		list += table
	}

	if _, err := p.Exec(context.Background(),
		"TRUNCATE "+list+" RESTART IDENTITY CASCADE"); err != nil {
		t.Fatalf("truncating %s: %v", list, err)
	}
}

// Querier is what both a pool and a transaction satisfy, so a helper can take either.
//
// pgx does not declare this interface, and declaring it here rather than importing one is the same
// decision as in testing-concepts/fakes: the consumer names the methods it needs. Three methods
// cover every query in this module.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}
