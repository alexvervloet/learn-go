// Package store is every query this service makes.
//
// # Why the SQL is here and not spread through the handlers
//
// Because then the queries are reviewable as a set. Every index in the migration exists for a query in this
// file, and a query added without an index is visible next to the ones that have one.
//
// It also means the handlers never hold a connection. They call a method, it returns data or an error, and the
// pool is managed in one place.
//
// # No ORM
//
// pgx directly, with the SQL written out. An ORM would save the scanning and cost the ability to see what is
// sent, which is the thing this repository's database module spends nine thousand lines on. The cost is
// boilerplate in Scan, and pgx.CollectRows removes most of it.
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store holds the pool.
type Store struct {
	pool *pgxpool.Pool
}

// New wraps a pool.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Pool exposes the pool, for a caller that needs a transaction across several methods.
func (s *Store) Pool() *pgxpool.Pool { return s.pool }

// User is a row of users.
type User struct {
	ID           int64
	Email        string
	PasswordHash string
	CreatedAt    time.Time
}

// URL is a row of urls.
type URL struct {
	ID         int64
	Slug       string
	Target     string
	UserID     int64
	CreatedAt  time.Time
	ExpiresAt  *time.Time
	ClickCount int64
}

// Expired reports whether the URL has passed its expiry.
//
// The check is in Go rather than in the query's WHERE clause, deliberately. A lookup that filters expired rows
// out cannot tell "never existed" from "expired", and those deserve different responses: a 404 and a 410.
func (u URL) Expired() bool {
	return u.ExpiresAt != nil && u.ExpiresAt.Before(time.Now())
}

// Errors the callers switch on.
var (
	ErrNotFound      = errors.New("store: not found")
	ErrEmailTaken    = errors.New("store: that email is already registered")
	ErrSlugTaken     = errors.New("store: that slug is already taken")
	ErrNotOwner      = errors.New("store: that URL belongs to someone else")
	ErrNothingToSave = errors.New("store: nothing to update")
)

// CreateUser inserts a user.
//
// # Why the unique violation is caught rather than pre-checked
//
// A SELECT before the INSERT is a race: two requests both find the email free and both insert, and one gets a
// constraint error anyway. So the constraint is the check, and the code turns its error into a domain error.
//
// 23505 is the SQLSTATE for unique_violation. Matching on the CODE rather than the message is what makes this
// survive a Postgres upgrade and a non-English locale.
func (s *Store) CreateUser(ctx context.Context, email, passwordHash string) (*User, error) {
	const q = `
		INSERT INTO users (email, password_hash)
		VALUES ($1, $2)
		RETURNING id, email, password_hash, created_at`

	rows, err := s.pool.Query(ctx, q, email, passwordHash)
	if err != nil {
		return nil, wrapUnique(err, map[string]error{"users_email_key": ErrEmailTaken})
	}

	user, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[User])
	if err != nil {
		return nil, wrapUnique(err, map[string]error{"users_email_key": ErrEmailTaken})
	}

	return &user, nil
}

// UserByEmail finds a user.
func (s *Store) UserByEmail(ctx context.Context, email string) (*User, error) {
	const q = `SELECT id, email, password_hash, created_at FROM users WHERE email = $1`

	rows, err := s.pool.Query(ctx, q, email)
	if err != nil {
		return nil, err
	}

	user, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[User])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: user %q", ErrNotFound, email)
		}

		return nil, err
	}

	return &user, nil
}

// CreateURL inserts a URL, generating a slug from the row's own id when none is given.
//
// # How a generated slug gets its id first
//
// A generated slug is derived from the id, and the id normally does not exist until the row does. nextval
// reserves one before the insert, so the slug can be computed and the row written in one INSERT.
//
// # The collision the id scheme does not prevent
//
// Generated slugs never collide with each other, because ids don't. They can collide with a CUSTOM slug: a
// person can choose, today, the exact string that id 90,000 will encode to next month. When that id comes up
// the insert fails on urls_slug_key, and without handling it the user who did not choose a slug at all gets a
// 409. So a generated slug that is taken is skipped: the next id is reserved and tried, inside a savepoint,
// because a failed INSERT aborts the whole transaction in Postgres. Skipping costs one id. A person would have
// to have taken every candidate for this to give up.
func (s *Store) CreateURL(ctx context.Context, userID int64, target, customSlug string, expiresAt *time.Time, slugFor func(int64) (string, error)) (*URL, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}

	// Rollback on every path that is not an explicit Commit. After a successful commit this is a no-op that
	// returns ErrTxClosed, which is why the error is discarded rather than logged.
	defer func() { _ = tx.Rollback(ctx) }()

	if customSlug != "" {
		url, err := insertURL(ctx, tx, userID, target, customSlug, expiresAt)
		if err != nil {
			return nil, err
		}

		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}

		return url, nil
	}

	const attempts = 5

	for range attempts {
		url, err := insertGenerated(ctx, tx, userID, target, expiresAt, slugFor)
		if errors.Is(err, ErrSlugTaken) {
			continue
		}

		if err != nil {
			return nil, err
		}

		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}

		return url, nil
	}

	return nil, fmt.Errorf("%w: %d generated slugs in a row were already taken", ErrSlugTaken, attempts)
}

// insertGenerated reserves an id, derives the slug and inserts, all inside a savepoint, so a slug that is
// already taken rolls back this attempt and leaves the transaction usable for the next.
func insertGenerated(ctx context.Context, tx pgx.Tx, userID int64, target string, expiresAt *time.Time, slugFor func(int64) (string, error)) (*URL, error) {
	sp, err := tx.Begin(ctx)
	if err != nil {
		return nil, err
	}

	defer func() { _ = sp.Rollback(ctx) }()

	var id int64
	if err := sp.QueryRow(ctx, `SELECT nextval('urls_id_seq')`).Scan(&id); err != nil {
		return nil, fmt.Errorf("store: reserve an id: %w", err)
	}

	slug, err := slugFor(id)
	if err != nil {
		return nil, fmt.Errorf("store: build a slug for id %d: %w", id, err)
	}

	const q = `
		INSERT INTO urls (id, slug, target, user_id, expires_at)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, slug, target, user_id, created_at, expires_at, click_count`

	rows, err := sp.Query(ctx, q, id, slug, target, userID, expiresAt)
	if err != nil {
		return nil, wrapUnique(err, map[string]error{"urls_slug_key": ErrSlugTaken})
	}

	url, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[URL])
	if err != nil {
		return nil, wrapUnique(err, map[string]error{"urls_slug_key": ErrSlugTaken})
	}

	// Commit on a savepoint is RELEASE SAVEPOINT: the row becomes part of the outer transaction.
	if err := sp.Commit(ctx); err != nil {
		return nil, err
	}

	return &url, nil
}

// insertURL inserts with an explicit slug.
func insertURL(ctx context.Context, tx pgx.Tx, userID int64, target, slug string, expiresAt *time.Time) (*URL, error) {
	const q = `
		INSERT INTO urls (slug, target, user_id, expires_at)
		VALUES ($1, $2, $3, $4)
		RETURNING id, slug, target, user_id, created_at, expires_at, click_count`

	rows, err := tx.Query(ctx, q, slug, target, userID, expiresAt)
	if err != nil {
		return nil, wrapUnique(err, map[string]error{"urls_slug_key": ErrSlugTaken})
	}

	url, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[URL])
	if err != nil {
		return nil, wrapUnique(err, map[string]error{"urls_slug_key": ErrSlugTaken})
	}

	return &url, nil
}

// URLBySlug is the redirect path's only query.
//
// This is the hot one. It is a single indexed lookup on a unique column, which is why the slug has its own
// index, and it is the query the cache in front of it exists to avoid.
func (s *Store) URLBySlug(ctx context.Context, slug string) (*URL, error) {
	const q = `
		SELECT id, slug, target, user_id, created_at, expires_at, click_count
		FROM urls
		WHERE slug = $1`

	rows, err := s.pool.Query(ctx, q, slug)
	if err != nil {
		return nil, err
	}

	url, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[URL])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: slug %q", ErrNotFound, slug)
		}

		return nil, err
	}

	return &url, nil
}

// URLsByUser lists a user's URLs, newest first, with keyset pagination.
//
// # Keyset rather than OFFSET
//
// OFFSET 10000 makes Postgres read and discard ten thousand rows. Worse, a row inserted between two page
// requests shifts everything, so a user paging through their URLs sees one twice and misses another.
//
// A keyset takes the last row's sort key and asks for what comes after it, which is an index seek and is stable
// under insertion. The cost is that a client cannot jump to page 50, which is a feature nobody uses and a bill
// nobody wants.
//
// The tuple comparison (created_at, id) < ($2, $3) is the correct form: created_at alone is not unique, so two
// URLs created in the same microsecond would straddle a page boundary and one would be skipped.
func (s *Store) URLsByUser(ctx context.Context, userID int64, limit int, afterCreatedAt *time.Time, afterID int64) ([]URL, error) {
	const first = `
		SELECT id, slug, target, user_id, created_at, expires_at, click_count
		FROM urls
		WHERE user_id = $1
		ORDER BY created_at DESC, id DESC
		LIMIT $2`

	const next = `
		SELECT id, slug, target, user_id, created_at, expires_at, click_count
		FROM urls
		WHERE user_id = $1 AND (created_at, id) < ($3, $4)
		ORDER BY created_at DESC, id DESC
		LIMIT $2`

	var (
		rows pgx.Rows
		err  error
	)

	if afterCreatedAt == nil {
		rows, err = s.pool.Query(ctx, first, userID, limit)
	} else {
		rows, err = s.pool.Query(ctx, next, userID, limit, *afterCreatedAt, afterID)
	}

	if err != nil {
		return nil, err
	}

	return pgx.CollectRows(rows, pgx.RowToStructByPos[URL])
}

// DeleteURL removes a URL, checking ownership.
//
// # Why the ownership check is in the WHERE clause
//
// A SELECT to check the owner followed by a DELETE is two round trips and a race. One statement with both
// conditions is atomic, and the rows-affected count says which case happened.
//
// Distinguishing "not found" from "not yours" then needs a second query, and it is worth NOT doing: telling a
// caller that a slug exists but belongs to someone else is a way to enumerate other people's slugs. Both return
// ErrNotFound.
func (s *Store) DeleteURL(ctx context.Context, userID int64, slug string) error {
	const q = `DELETE FROM urls WHERE slug = $1 AND user_id = $2`

	tag, err := s.pool.Exec(ctx, q, slug, userID)
	if err != nil {
		return err
	}

	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: slug %q", ErrNotFound, slug)
	}

	return nil
}

// RecordClick inserts a click and bumps the counter.
//
// # Why both, in one transaction
//
// The clicks table is the source of truth and the counter is a cache of it. Writing one without the other makes
// them disagree, and a transaction is how they stay consistent.
//
// # Why this is not on the redirect's critical path
//
// It is two writes. A redirect that waits for them is a redirect that is slower than it needs to be and that
// fails when the database is busy, for the sake of a number nobody is watching in real time. The worker does
// this, and the redirect returns as soon as it knows the target.
//
// # Why the UPDATE comes first
//
// Because the URL can be gone by the time the worker gets here: it was deleted between the redirect and the
// task. Updating first finds that as zero rows, which is ErrNotFound, and the worker drops the task. Inserting
// first finds it as a foreign-key violation (23503) instead, which the worker cannot tell from a real failure,
// so it retries a task that will never succeed. The update also locks the URL row, so a DELETE that arrives
// between the two statements waits for this transaction rather than removing the row under the insert.
func (s *Store) RecordClick(ctx context.Context, urlID int64, referrer, userAgent string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}

	defer func() { _ = tx.Rollback(ctx) }()

	// click_count = click_count + 1, not a read-then-write. The increment happens in the database, so two
	// concurrent clicks both count. Reading the value into Go and writing it back is the classic lost update.
	tag, err := tx.Exec(ctx, `UPDATE urls SET click_count = click_count + 1 WHERE id = $1`, urlID)
	if err != nil {
		return err
	}

	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: url %d", ErrNotFound, urlID)
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO clicks (url_id, referrer, user_agent) VALUES ($1, $2, $3)`,
		urlID, nullIfEmpty(referrer), nullIfEmpty(userAgent),
	); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// ClickCount reads the denormalised counter.
func (s *Store) ClickCount(ctx context.Context, slug string) (int64, error) {
	var n int64

	err := s.pool.QueryRow(ctx, `SELECT click_count FROM urls WHERE slug = $1`, slug).Scan(&n)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, fmt.Errorf("%w: slug %q", ErrNotFound, slug)
		}

		return 0, err
	}

	return n, nil
}

// ActualClickCount counts the clicks table, which is the source of truth.
//
// It exists so a test can prove the counter agrees with it, and so an admin can repair a counter that drifted.
func (s *Store) ActualClickCount(ctx context.Context, slug string) (int64, error) {
	const q = `
		SELECT count(*)
		FROM clicks
		JOIN urls ON urls.id = clicks.url_id
		WHERE urls.slug = $1`

	var n int64
	if err := s.pool.QueryRow(ctx, q, slug).Scan(&n); err != nil {
		return 0, err
	}

	return n, nil
}

// DeleteExpired removes URLs past their expiry and returns how many went.
//
// The partial index on expires_at is for this query. Without it, the sweep is a full table scan of a table that
// is mostly rows with a NULL expiry.
func (s *Store) DeleteExpired(ctx context.Context) (int64, error) {
	const q = `DELETE FROM urls WHERE expires_at IS NOT NULL AND expires_at < now()`

	tag, err := s.pool.Exec(ctx, q)
	if err != nil {
		return 0, err
	}

	return tag.RowsAffected(), nil
}

// wrapUnique turns a Postgres unique violation into a domain error.
func wrapUnique(err error, byConstraint map[string]error) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return err
	}

	// 23505 is unique_violation. The SQLSTATE is the stable contract; the message is prose and is localised.
	if pgErr.Code != "23505" {
		return err
	}

	if domain, ok := byConstraint[pgErr.ConstraintName]; ok {
		return fmt.Errorf("%w: %s", domain, pgErr.ConstraintName)
	}

	return err
}

// nullIfEmpty turns "" into a NULL, so an absent header is absent rather than blank.
//
// The distinction matters for the analytics: `WHERE referrer IS NULL` finds direct visits and
// `WHERE referrer = ”` finds nothing, because nothing writes an empty string once this is here.
func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}

	return &s
}
