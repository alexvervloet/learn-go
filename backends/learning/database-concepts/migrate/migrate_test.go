package migrate

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/stdlib"

	"github.com/alexvervloet/learn-go/backends/learning/database-concepts/dbtest"
)

// harness gives a *sql.DB, a throwaway migration directory, and a version table nobody else uses.
//
// goose works on *sql.DB rather than a pgx pool, and stdlib.OpenDBFromPool bridges them, which is the
// same trick dbtest uses. The version table is named after the test so two tests in this package cannot
// disagree about what is applied.
func harness(t *testing.T) (*sql.DB, *Dir, string) {
	t.Helper()

	pool := dbtest.Pool(t)

	db := stdlib.OpenDBFromPool(pool)
	t.Cleanup(func() { _ = db.Close() })

	table := "goose_" + strings.ToLower(strings.ReplaceAll(t.Name(), "/", "_"))

	t.Cleanup(func() {
		// The version table and anything the migrations made. A migration test cannot use a
		// rolled-back transaction, for the same reason the transactions package cannot: the
		// thing under test commits.
		for _, sql := range []string{
			"DROP TABLE IF EXISTS " + table,
			"DROP TABLE IF EXISTS mig_widgets",
			"DROP TABLE IF EXISTS mig_gadgets",
			"DROP INDEX IF EXISTS idx_mig_widgets_name",
		} {
			if _, err := db.Exec(sql); err != nil {
				t.Errorf("cleaning up with %q: %v", sql, err)
			}
		}
	})

	return db, NewDir(t.TempDir()), table
}

const firstMigration = `-- +goose Up
CREATE TABLE mig_widgets (id bigserial PRIMARY KEY, name text NOT NULL);

-- +goose Down
DROP TABLE mig_widgets;
`

func TestUpAndDownAndStatus(t *testing.T) {
	db, dir, table := harness(t)
	ctx := context.Background()

	if err := dir.Write("00001_widgets.sql", firstMigration); err != nil {
		t.Fatal(err)
	}
	if err := dir.Write("00002_gadgets.sql", `-- +goose Up
CREATE TABLE mig_gadgets (id bigserial PRIMARY KEY, widget_id bigint REFERENCES mig_widgets(id));

-- +goose Down
DROP TABLE mig_gadgets;
`); err != nil {
		t.Fatal(err)
	}

	p, err := New(db, dir.Path, table)
	if err != nil {
		t.Fatal(err)
	}

	results, err := p.Up(ctx)
	if err != nil {
		t.Fatal(err)
	}

	for _, r := range results {
		t.Logf("applied %d (%s) in %v", r.Source.Version, r.Source.Path, r.Duration)
	}

	if len(results) != 2 {
		t.Fatalf("applied %d migrations, want 2", len(results))
	}

	version, err := p.Version(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if version != 2 {
		t.Errorf("version is %d, want 2", version)
	}

	// Down rolls back ONE migration, not all of them, which surprises people who read `goose down`
	// as the opposite of `goose up`.
	if _, err := p.Down(ctx); err != nil {
		t.Fatal(err)
	}

	version, err = p.Version(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if version != 1 {
		t.Errorf("after one down the version is %d, want 1", version)
	}

	status, err := p.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}

	for _, s := range status {
		t.Logf("%d  %-20s applied=%v", s.Source.Version, s.State, !s.AppliedAt.IsZero())
	}

	// mig_widgets survives, mig_gadgets does not.
	var widgets, gadgets bool
	if err := db.QueryRow(`
		SELECT to_regclass('mig_widgets') IS NOT NULL,
		       to_regclass('mig_gadgets') IS NOT NULL`).Scan(&widgets, &gadgets); err != nil {
		t.Fatal(err)
	}

	if !widgets {
		t.Error("mig_widgets was dropped by a down that should only have touched 00002")
	}
	if gadgets {
		t.Error("mig_gadgets survived its own down migration")
	}
}

// TestAFailedMigrationLeavesNothingBehind is the Postgres argument, measured.
//
// Four statements, the third of which fails. On MySQL the first two would have committed and the schema
// would be halfway. On Postgres the whole thing rolls back.
func TestAFailedMigrationLeavesNothingBehind(t *testing.T) {
	db, dir, table := harness(t)
	ctx := context.Background()

	if err := dir.Write("00001_partly_broken.sql", `-- +goose Up
CREATE TABLE mig_widgets (id bigserial PRIMARY KEY, name text NOT NULL);
CREATE TABLE mig_gadgets (id bigserial PRIMARY KEY);
ALTER TABLE mig_widgets ADD COLUMN colour text;
-- And the statement that fails: a column that is not there.
ALTER TABLE mig_widgets DROP COLUMN does_not_exist;

-- +goose Down
DROP TABLE mig_gadgets;
DROP TABLE mig_widgets;
`); err != nil {
		t.Fatal(err)
	}

	p, err := New(db, dir.Path, table)
	if err != nil {
		t.Fatal(err)
	}

	_, err = p.Up(ctx)
	if err == nil {
		t.Fatal("the migration should have failed")
	}

	t.Logf("the migration failed as expected: %v", err)

	// Nothing was created, including the two tables whose statements succeeded.
	var widgets, gadgets bool
	if err := db.QueryRow(`
		SELECT to_regclass('mig_widgets') IS NOT NULL,
		       to_regclass('mig_gadgets') IS NOT NULL`).Scan(&widgets, &gadgets); err != nil {
		t.Fatal(err)
	}

	if widgets {
		t.Error("mig_widgets exists, so the failed migration was not rolled back")
	}
	if gadgets {
		t.Error("mig_gadgets exists, so the failed migration was not rolled back")
	}

	// And goose did not record it, so a fix plus a re-run works with no manual cleanup.
	version, err := p.Version(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if version != 0 {
		t.Errorf("version is %d after a failed migration, want 0", version)
	}

	t.Log("two of four statements succeeded and none of them persisted; on MySQL both tables " +
		"would exist and the version table would say nothing was applied")
}

// TestConcurrentIndexNeedsNoTransaction is the exception, and both halves of it.
func TestConcurrentIndexNeedsNoTransaction(t *testing.T) {
	ctx := context.Background()

	t.Run("without the annotation it fails", func(t *testing.T) {
		db, dir, table := harness(t)

		if err := dir.Write("00001_widgets.sql", firstMigration); err != nil {
			t.Fatal(err)
		}
		if err := dir.Write("00002_index.sql", `-- +goose Up
CREATE INDEX CONCURRENTLY idx_mig_widgets_name ON mig_widgets (name);

-- +goose Down
DROP INDEX CONCURRENTLY idx_mig_widgets_name;
`); err != nil {
			t.Fatal(err)
		}

		p, err := New(db, dir.Path, table)
		if err != nil {
			t.Fatal(err)
		}

		_, err = p.Up(ctx)
		if err == nil {
			t.Fatal("expected CREATE INDEX CONCURRENTLY inside a transaction to fail")
		}

		// 25001 is active_sql_transaction.
		if !strings.Contains(err.Error(), "25001") &&
			!strings.Contains(err.Error(), "cannot run inside a transaction block") {
			t.Errorf("expected a transaction-block error, got %v", err)
		}

		t.Logf("goose wrapped it in a transaction and Postgres refused: %v", err)
	})

	t.Run("with the annotation it works", func(t *testing.T) {
		db, dir, table := harness(t)

		if err := dir.Write("00001_widgets.sql", firstMigration); err != nil {
			t.Fatal(err)
		}
		if err := dir.Write("00002_index.sql", `-- +goose NO TRANSACTION
-- +goose Up
CREATE INDEX CONCURRENTLY idx_mig_widgets_name ON mig_widgets (name);

-- +goose Down
DROP INDEX CONCURRENTLY idx_mig_widgets_name;
`); err != nil {
			t.Fatal(err)
		}

		p, err := New(db, dir.Path, table)
		if err != nil {
			t.Fatal(err)
		}

		if _, err := p.Up(ctx); err != nil {
			t.Fatal(err)
		}

		var valid bool
		if err := db.QueryRow(`
			SELECT i.indisvalid FROM pg_class c
			  JOIN pg_index i ON i.indexrelid = c.oid
			 WHERE c.relname = 'idx_mig_widgets_name'`).Scan(&valid); err != nil {
			t.Fatal(err)
		}

		if !valid {
			t.Error("the index exists but is INVALID")
		}

		t.Log("the index is built and valid, and no write was blocked while it was building")
	})
}

// TestADownMigrationCanBeIrreversible is the thing "always write a down migration" does not solve.
func TestADownMigrationCanBeIrreversible(t *testing.T) {
	db, dir, table := harness(t)
	ctx := context.Background()

	if err := dir.Write("00001_widgets.sql", firstMigration); err != nil {
		t.Fatal(err)
	}

	// A perfectly reversible-looking pair. The down statement is the exact inverse of the up
	// statement, which is what a reviewer checks for.
	if err := dir.Write("00002_drop_a_column.sql", `-- +goose Up
ALTER TABLE mig_widgets DROP COLUMN name;

-- +goose Down
ALTER TABLE mig_widgets ADD COLUMN name text NOT NULL DEFAULT '';
`); err != nil {
		t.Fatal(err)
	}

	p, err := New(db, dir.Path, table)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := p.UpByOne(ctx); err != nil {
		t.Fatal(err)
	}

	if _, err := db.ExecContext(ctx,
		"INSERT INTO mig_widgets (name) VALUES ('sprocket'), ('flange')"); err != nil {
		t.Fatal(err)
	}

	if _, err := p.UpByOne(ctx); err != nil {
		t.Fatal(err)
	}

	// Roll it back. The schema is restored exactly.
	if _, err := p.Down(ctx); err != nil {
		t.Fatal(err)
	}

	var columnExists bool
	if err := db.QueryRow(`
		SELECT count(*) = 1 FROM information_schema.columns
		 WHERE table_name = 'mig_widgets' AND column_name = 'name'`).Scan(&columnExists); err != nil {
		t.Fatal(err)
	}

	if !columnExists {
		t.Fatal("the down migration did not restore the column")
	}

	// And every value is gone.
	var names []string
	rows, err := db.QueryContext(ctx, "SELECT name FROM mig_widgets ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	t.Logf("two widgets were named sprocket and flange; after up then down they are named %q", names)

	for _, name := range names {
		if name != "" {
			t.Errorf("expected every name to be empty, got %q", name)
		}
	}

	t.Log("the schema round-tripped perfectly and the DATA did not. A down migration restores " +
		"structure, never content, so a destructive up migration is one-way whatever the " +
		"down says. The safe version is two deploys: stop writing the column, then drop it " +
		"a release later.")
}

// TestAMisnamedFileIsIgnoredSilently, which is the most common "my migration did not run".
func TestAMisnamedFileIsIgnoredSilently(t *testing.T) {
	db, dir, table := harness(t)
	ctx := context.Background()

	if err := dir.Write("00001_widgets.sql", firstMigration); err != nil {
		t.Fatal(err)
	}

	// No version prefix. goose parses the version from the filename, so this is not a migration as
	// far as goose is concerned.
	if err := dir.Write("add_gadgets.sql", `-- +goose Up
CREATE TABLE mig_gadgets (id bigserial PRIMARY KEY);

-- +goose Down
DROP TABLE mig_gadgets;
`); err != nil {
		t.Fatal(err)
	}

	p, err := New(db, dir.Path, table)
	if err != nil {
		t.Fatal(err)
	}

	results, err := p.Up(ctx)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("two files in the directory, %d migration(s) applied", len(results))

	if len(results) != 1 {
		t.Errorf("applied %d, want 1", len(results))
	}

	var gadgets bool
	if err := db.QueryRow("SELECT to_regclass('mig_gadgets') IS NOT NULL").Scan(&gadgets); err != nil {
		t.Fatal(err)
	}

	if gadgets {
		t.Error("the misnamed file ran")
	}

	t.Log("no error, no warning, no mention in the status output: the file is simply not a " +
		"migration. Use `goose create` rather than writing the filename by hand.")
}
