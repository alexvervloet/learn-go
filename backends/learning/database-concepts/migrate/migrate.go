// Package migrate is schema migrations with goose, and the two things Postgres lets you do that most
// databases do not.
//
// # Transactional DDL
//
// Postgres wraps DDL in transactions. CREATE TABLE, ALTER TABLE, CREATE INDEX and DROP COLUMN are all
// transactional, so a migration with four statements where the third fails leaves the schema exactly as
// it was. MySQL does not do this: each DDL statement commits, so a failed migration leaves the schema
// halfway and there is no rollback, only a second migration written by hand at 3am.
//
// This is the single strongest practical argument for Postgres over MySQL and it is rarely the one
// people make. TestAFailedMigrationLeavesNothingBehind measures it.
//
// goose runs each migration in a transaction by default, so this comes for free.
//
// # And the exception, which is the one that bites
//
// CREATE INDEX CONCURRENTLY cannot run inside a transaction. Neither can DROP INDEX CONCURRENTLY, ALTER
// TYPE ... ADD VALUE before Postgres 12, VACUUM, or CREATE DATABASE. And CONCURRENTLY is exactly what a
// migration on a live table needs, because a plain CREATE INDEX takes a lock that blocks every write for
// the duration.
//
// So the migration that is safe for production is the one goose cannot wrap, and it needs the annotation:
//
//	-- +goose Up
//	-- +goose NO TRANSACTION
//	CREATE INDEX CONCURRENTLY idx_books_isbn ON books (isbn);
//
// The cost of NO TRANSACTION is that a failure leaves the work half done. A failed CREATE INDEX
// CONCURRENTLY leaves an INVALID index behind, which takes space, is not used by the planner, and has to
// be dropped by hand. TestConcurrentIndexNeedsNoTransaction shows both halves.
//
// # What goose tracks and where
//
// A table called goose_db_version, one row per applied migration, with the version number and a timestamp.
// Two consequences worth knowing:
//
//	the numbers must be unique and ordered, so two developers who both create 00004_* on their
//	  own branches produce a collision that only appears on merge. Timestamp-based versions
//	  (goose create, which uses YYYYMMDDHHMMSS) avoid it.
//	goose decides what to run from that table, not from the files, so deleting a migration file
//	  from a repo does not un-apply it. Adding one with a lower number than the current version,
//	  the merge above, makes the Provider's Up REFUSE to run: "found 1 missing (out-of-order)
//	  migration". It applies it only with goose.WithAllowOutofOrder(true). An earlier version of
//	  this comment said the file is left unapplied and silent; that was the old package-level API,
//	  not the Provider this package uses.
package migrate

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	"github.com/pressly/goose/v3"
)

// Dir is a throwaway migration directory, so a test can write migrations and run them without touching
// the module's real ones.
type Dir struct {
	Path string
}

// NewDir creates a temporary migration directory.
//
// t.TempDir is the usual way, and this takes the path instead so the type does not depend on testing. The
// tests pass t.TempDir() in.
func NewDir(path string) *Dir {
	return &Dir{Path: path}
}

// Write adds a migration file.
//
// The name must be <version>_<label>.sql, which is what goose parses the version out of. A file that does
// not match is ignored SILENTLY, which is worth knowing because "my migration did not run" is usually
// this.
func (d *Dir) Write(name, body string) error {
	path := filepath.Join(d.Path, name)

	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", name, err)
	}

	return nil
}

// Provider wraps goose's provider for one directory and one database.
//
// goose has two APIs: a package-level one with global state (goose.SetDialect, goose.Up) and a
// provider-based one. The global one cannot be used from concurrent tests, because the dialect and the
// logger are process-wide. The provider takes them as arguments, which is why this package uses it and
// dbtest, written earlier, does not.
type Provider struct {
	provider *goose.Provider
}

// New builds a Provider for a directory.
//
// The table name is a parameter because two Providers in one database must not share a version table:
// each test writes its own migrations, and sharing goose_db_version would make them disagree about what
// is applied.
func New(db *sql.DB, dir string, table string) (*Provider, error) {
	// WithTableName rather than the default goose_db_version. Out-of-order migrations stay refused,
	// goose's default; WithAllowOutofOrder(true) is the option that would change that.
	p, err := goose.NewProvider(goose.DialectPostgres, db, os.DirFS(dir),
		goose.WithTableName(table))
	if err != nil {
		return nil, fmt.Errorf("creating a goose provider for %s: %w", dir, err)
	}

	return &Provider{provider: p}, nil
}

// Up applies every pending migration.
func (p *Provider) Up(ctx context.Context) ([]*goose.MigrationResult, error) {
	results, err := p.provider.Up(ctx)
	if err != nil {
		return results, fmt.Errorf("goose up: %w", err)
	}
	return results, nil
}

// UpByOne applies the next pending migration only.
func (p *Provider) UpByOne(ctx context.Context) (*goose.MigrationResult, error) {
	result, err := p.provider.UpByOne(ctx)
	if err != nil {
		return result, fmt.Errorf("goose up-by-one: %w", err)
	}
	return result, nil
}

// Down rolls back the most recent migration.
func (p *Provider) Down(ctx context.Context) (*goose.MigrationResult, error) {
	result, err := p.provider.Down(ctx)
	if err != nil {
		return result, fmt.Errorf("goose down: %w", err)
	}
	return result, nil
}

// Version returns the current schema version.
func (p *Provider) Version(ctx context.Context) (int64, error) {
	v, err := p.provider.GetDBVersion(ctx)
	if err != nil {
		return 0, fmt.Errorf("reading the version: %w", err)
	}
	return v, nil
}

// Status lists every known migration and whether it is applied.
func (p *Provider) Status(ctx context.Context) ([]*goose.MigrationStatus, error) {
	status, err := p.provider.Status(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading the status: %w", err)
	}
	return status, nil
}
