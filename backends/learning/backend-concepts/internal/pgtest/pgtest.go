// Package pgtest is a Postgres harness for this module's tests.
//
// # Why this exists when database-concepts already has one
//
// database-concepts/dbtest does the same job and is better at it. Importing it would mean this module
// depends on that one, which works inside the workspace and breaks the moment someone clones one directory
// and builds it, because a Go module path under github.com needs a tag to be resolvable and this repo does
// not tag sub-modules.
//
// So: 90 lines duplicated, deliberately, to keep every module in this repo independently buildable. The
// alternative is a shared internal module and a `replace` directive in nine go.mod files, which is worse.
//
// The decisions are the same ones dbtest makes and the reasons are in its package doc: skip when there is
// no database rather than fail, one pool per test binary, one DATABASE per test binary so packages running
// concurrently do not fight.
package pgtest

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DefaultURL is used when DATABASE_URL is unset.
const DefaultURL = "postgres:///learn_go_db?sslmode=disable"

// URL returns the connection string.
func URL() string {
	if url := os.Getenv("DATABASE_URL"); url != "" {
		return url
	}
	return DefaultURL
}

var (
	once    sync.Once
	pool    *pgxpool.Pool
	poolErr error
)

// Pool returns a shared pool, or skips the test.
func Pool(t testing.TB) *pgxpool.Pool {
	t.Helper()

	once.Do(func() { pool, poolErr = connect() })

	if poolErr != nil {
		t.Skipf("no database available (%v)\n"+
			"  docker compose up -d, then set DATABASE_URL\n"+
			"  or with a local Postgres: createdb learn_go_db && go test ./...",
			poolErr)
	}

	return pool
}

func connect() (*pgxpool.Pool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cfg, err := pgxpool.ParseConfig(URL())
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", URL(), err)
	}

	if name, err := ensureOwnDatabase(ctx, cfg); err == nil {
		cfg.ConnConfig.Database = name
	} else {
		fmt.Fprintf(os.Stderr, "pgtest: sharing %s (%v)\n", cfg.ConnConfig.Database, err)
	}

	cfg.MaxConns = 10
	cfg.MinConns = 1

	p, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("creating pool: %w", err)
	}

	if err := p.Ping(ctx); err != nil {
		p.Close()
		return nil, fmt.Errorf("connecting to %s: %w", URL(), err)
	}

	if err := applySchema(ctx, p); err != nil {
		p.Close()
		return nil, fmt.Errorf("applying the schema: %w", err)
	}

	return p, nil
}

// ensureOwnDatabase gives this test binary its own database.
func ensureOwnDatabase(ctx context.Context, cfg *pgxpool.Config) (string, error) {
	base := filepath.Base(os.Args[0])
	base = strings.TrimSuffix(strings.TrimSuffix(base, ".exe"), ".test")

	var b strings.Builder
	for _, r := range strings.ToLower(base) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}

	suffix := strings.Trim(b.String(), "_")
	if suffix == "" {
		return "", errors.New("cannot name a database for this binary")
	}

	name := cfg.ConnConfig.Database + "_bc_" + suffix
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

	if _, err := conn.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "42P04" {
			return name, nil
		}
		return "", fmt.Errorf("creating %s: %w", name, err)
	}

	return name, nil
}

// Schema is the one table this module's database tests need.
//
// Plain idempotent SQL rather than goose, and that is a deliberate difference from database-concepts. There,
// migrations ARE the subject: the ordering, the version table and the down direction all get tested. Here
// the table is a fixture, nothing tests its history, and `CREATE TABLE IF NOT EXISTS` is honest about that.
// Reaching for a migration tool to create a test fixture adds a version table, a directory and an ordering
// problem to something that needs none of them.
const Schema = `
CREATE TABLE IF NOT EXISTS events (
    id         bigserial PRIMARY KEY,
    actor      text        NOT NULL,
    kind       text        NOT NULL,
    created_at timestamptz NOT NULL,
    payload    text        NOT NULL
);

-- The index keyset pagination needs. (created_at, id) rather than created_at alone, because timestamps
-- collide and a pagination key that is not unique skips or repeats rows. The pagination package measures
-- exactly that.
CREATE INDEX IF NOT EXISTS idx_events_created_id ON events (created_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS idx_events_actor ON events (actor, created_at DESC, id DESC);
`

func applySchema(ctx context.Context, p *pgxpool.Pool) error {
	if _, err := p.Exec(ctx, Schema); err != nil {
		return fmt.Errorf("creating the events table: %w", err)
	}
	return nil
}

// Tx returns a transaction that is rolled back when the test ends.
func Tx(t testing.TB) pgx.Tx {
	t.Helper()

	p := Pool(t)
	ctx := context.Background()

	tx, err := p.Begin(ctx)
	if err != nil {
		t.Fatalf("beginning a transaction: %v", err)
	}

	t.Cleanup(func() {
		if err := tx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			t.Errorf("rolling back: %v", err)
		}
	})

	return tx
}

// Querier is what a pool and a transaction both satisfy.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}
