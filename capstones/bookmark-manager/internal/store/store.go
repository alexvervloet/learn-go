// Package store is every query this service makes.
//
// # The three problems worth reading
//
//   - Tags are many-to-many and user-scoped, so saving a bookmark with tags means "find or create" for each
//     tag. The obvious ON CONFLICT DO NOTHING ... RETURNING does not do what it looks like.
//   - Loading bookmarks with their tags is the N+1 problem in its most natural habitat. There are three
//     answers and they are measurably different.
//   - Full-text search is a generated tsvector column and a GIN index, and the query has to use the same text
//     search configuration the column was built with or the index is not used at all.
package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
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

// Pool exposes the pool.
func (s *Store) Pool() *pgxpool.Pool { return s.pool }

// Domain errors.
var (
	ErrNotFound   = errors.New("store: not found")
	ErrEmailTaken = errors.New("store: that email is already registered")
	ErrNameTaken  = errors.New("store: you already have one of those")
	ErrURLSaved   = errors.New("store: you have already saved that URL")
)

// User is a row of users.
type User struct {
	ID           int64
	Email        string
	PasswordHash string
	CreatedAt    time.Time
}

// CreateUser inserts a user.
func (s *Store) CreateUser(ctx context.Context, email, passwordHash string) (*User, error) {
	const q = `
		INSERT INTO users (email, password_hash)
		VALUES ($1, $2)
		RETURNING id, email, password_hash, created_at`

	return one[User](ctx, s.pool, q, map[string]error{"users_email_key": ErrEmailTaken}, email, passwordHash)
}

// UserByEmail finds a user.
func (s *Store) UserByEmail(ctx context.Context, email string) (*User, error) {
	const q = `SELECT id, email, password_hash, created_at FROM users WHERE email = $1`

	return one[User](ctx, s.pool, q, nil, email)
}

// Category is a row of categories.
type Category struct {
	ID     int64
	UserID int64
	Name   string
}

// CreateCategory inserts a category for a user.
func (s *Store) CreateCategory(ctx context.Context, userID int64, name string) (*Category, error) {
	const q = `INSERT INTO categories (user_id, name) VALUES ($1, $2) RETURNING id, user_id, name`

	return one[Category](ctx, s.pool, q, map[string]error{"categories_user_id_name_key": ErrNameTaken}, userID, name)
}

// CategoriesOf lists a user's categories.
func (s *Store) CategoriesOf(ctx context.Context, userID int64) ([]Category, error) {
	const q = `SELECT id, user_id, name FROM categories WHERE user_id = $1 ORDER BY name`

	return many[Category](ctx, s.pool, q, userID)
}

// DeleteCategory removes a category, leaving its bookmarks alone.
//
// The foreign key is ON DELETE SET NULL, so the bookmarks survive with no category. CASCADE here would be a
// data-loss bug discovered by a support ticket.
func (s *Store) DeleteCategory(ctx context.Context, userID, id int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM categories WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return err
	}

	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: category %d", ErrNotFound, id)
	}

	return nil
}

// Tag is a row of tags.
type Tag struct {
	ID     int64
	UserID int64
	Name   string
}

// EnsureTags finds or creates every named tag for a user, in one round trip.
//
// # The trap: ON CONFLICT DO NOTHING ... RETURNING returns nothing for the conflicting rows
//
// The obvious query is
//
//	INSERT INTO tags (user_id, name) SELECT $1, unnest($2::citext[])
//	ON CONFLICT DO NOTHING RETURNING id, name
//
// and it returns only the rows it actually INSERTED. A tag that already existed conflicts, does nothing, and
// is absent from RETURNING, so a caller that trusts the result silently drops every existing tag. The bookmark
// then has only its new tags, and the bug looks like "tags disappear sometimes".
//
// There are two fixes. `ON CONFLICT (user_id, name) DO UPDATE SET name = EXCLUDED.name` forces a write so
// RETURNING sees the row, at the cost of a pointless update and a bumped xmin on every existing tag, which
// bloats the table.
//
// The other is to insert and then SELECT, which is what this does. Two statements in one transaction rather
// than one clever one, and the SELECT is the source of truth for what the caller gets back.
func (s *Store) EnsureTags(ctx context.Context, userID int64, names []string) ([]Tag, error) {
	unique := uniqueTagNames(names)
	if len(unique) == 0 {
		return nil, nil
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}

	defer func() { _ = tx.Rollback(ctx) }()

	tags, err := ensureTags(ctx, tx, userID, unique)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	return tags, nil
}

// uniqueTagNames trims, drops empties and deduplicates case-insensitively, keeping the first spelling.
//
// Deduplicate before the insert. ON CONFLICT does not fire for two rows in the SAME statement: Postgres reports
// `ON CONFLICT DO UPDATE command cannot affect row a second time` and the whole statement fails. A request with
// ["go", "Go"] hits this, because the column is CITEXT.
func uniqueTagNames(names []string) []string {
	seen := make(map[string]bool, len(names))
	unique := make([]string, 0, len(names))

	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}

		key := strings.ToLower(n)
		if seen[key] {
			continue
		}

		seen[key] = true

		unique = append(unique, n)
	}

	return unique
}

// ensureTags is EnsureTags inside a transaction the caller owns, for names that are already deduplicated.
//
// CreateBookmark needs this form. With EnsureTags committing its own transaction first, a bookmark refused as a
// duplicate left behind the tags it had asked for, and "a bookmark with half its tags is worse than no
// bookmark" was only half true.
func ensureTags(ctx context.Context, tx pgx.Tx, userID int64, unique []string) ([]Tag, error) {
	// unnest turns an array parameter into rows, so this is ONE statement whatever the tag count. The
	// alternative is a VALUES list built with fmt.Sprintf, which is a placeholder-counting exercise and the
	// classic place SQL injection gets introduced.
	if _, err := tx.Exec(ctx, `
		INSERT INTO tags (user_id, name)
		SELECT $1, unnest($2::citext[])
		ON CONFLICT (user_id, name) DO NOTHING`, userID, unique); err != nil {
		return nil, err
	}

	rows, err := tx.Query(ctx, `
		SELECT id, user_id, name
		FROM tags
		WHERE user_id = $1 AND name = ANY($2::citext[])
		ORDER BY name`, userID, unique)
	if err != nil {
		return nil, err
	}

	return pgx.CollectRows(rows, pgx.RowToStructByPos[Tag])
}

// TagsOf lists a user's tags with how many bookmarks each one has.
//
// LEFT JOIN, not JOIN. An inner join drops the tags nobody has used, which are exactly the ones a user wants
// to see so they can delete them.
func (s *Store) TagsOf(ctx context.Context, userID int64) ([]TagCount, error) {
	const q = `
		SELECT t.id, t.name, count(bt.bookmark_id)
		FROM tags t
		LEFT JOIN bookmark_tags bt ON bt.tag_id = t.id
		WHERE t.user_id = $1
		GROUP BY t.id, t.name
		ORDER BY t.name`

	return many[TagCount](ctx, s.pool, q, userID)
}

// TagCount is a tag and its usage.
type TagCount struct {
	ID    int64
	Name  string
	Count int64
}

// Bookmark is a row of bookmarks, with its tags attached.
type Bookmark struct {
	ID          int64
	UserID      int64
	CategoryID  *int64
	URL         string
	Title       string
	Description string
	CreatedAt   time.Time
	UpdatedAt   time.Time

	// Tags is filled by the loader, not by the row scan. It is here rather than in a separate type because
	// every caller wants it and a "bookmark without tags" is a shape that only exists mid-query.
	Tags []string
}

// NewBookmark is what a caller supplies.
type NewBookmark struct {
	CategoryID  *int64
	URL         string
	Title       string
	Description string
	Tags        []string
}

// CreateBookmark inserts a bookmark and its tags.
//
// One transaction, the tags included, because a bookmark with half its tags is worse than no bookmark, and tags
// created for a bookmark that was then refused are clutter nobody asked for.
func (s *Store) CreateBookmark(ctx context.Context, userID int64, in NewBookmark) (*Bookmark, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}

	defer func() { _ = tx.Rollback(ctx) }()

	var tags []Tag

	if unique := uniqueTagNames(in.Tags); len(unique) > 0 {
		if tags, err = ensureTags(ctx, tx, userID, unique); err != nil {
			return nil, err
		}
	}

	const q = `
		INSERT INTO bookmarks (user_id, category_id, url, title, description)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, user_id, category_id, url, title, description, created_at, updated_at`

	rows, err := tx.Query(ctx, q, userID, in.CategoryID, in.URL, in.Title, in.Description)
	if err != nil {
		return nil, wrapUnique(err, map[string]error{"bookmarks_user_id_url_key": ErrURLSaved})
	}

	b, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[bookmarkRow])
	if err != nil {
		return nil, wrapUnique(err, map[string]error{
			"bookmarks_user_id_url_key": ErrURLSaved,
			// Migration 002's composite key. Someone else's category and a missing one fail the same
			// constraint, so the caller cannot tell them apart, which is the point.
			"bookmarks_category_owner_fkey": ErrNotFound,
		})
	}

	if len(tags) > 0 {
		ids := make([]int64, 0, len(tags))
		for _, t := range tags {
			ids = append(ids, t.ID)
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO bookmark_tags (bookmark_id, tag_id)
			SELECT $1, unnest($2::bigint[])`, b.ID, ids); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	out := b.toBookmark()

	for _, t := range tags {
		out.Tags = append(out.Tags, t.Name)
	}

	return &out, nil
}

// bookmarkRow is the row shape, without the tags.
type bookmarkRow struct {
	ID          int64
	UserID      int64
	CategoryID  *int64
	URL         string
	Title       string
	Description string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

func (r bookmarkRow) toBookmark() Bookmark {
	return Bookmark{
		ID: r.ID, UserID: r.UserID, CategoryID: r.CategoryID,
		URL: r.URL, Title: r.Title, Description: r.Description,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}

// DeleteBookmark removes a bookmark, checking ownership in the same statement.
func (s *Store) DeleteBookmark(ctx context.Context, userID, id int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM bookmarks WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return err
	}

	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: bookmark %d", ErrNotFound, id)
	}

	return nil
}

// Queries counts the round trips a loader made, so a test can assert on the N+1.
type Queries struct{ N int }

// ListNPlusOne loads bookmarks and then their tags, one query per bookmark.
//
// # The version everybody writes first
//
// It is not stupid: it is what an ORM does by default, it is readable, and for one bookmark it is correct and
// fast. The problem is that the cost is a function of the RESULT SIZE rather than the request, so it is
// invisible in development and linear in production.
//
// It is here so the alternative has something to be compared against, and the test asserts the query count
// rather than a duration, because the count is the same on every machine.
func (s *Store) ListNPlusOne(ctx context.Context, userID int64, limit int) ([]Bookmark, Queries, error) {
	var q Queries

	const listQ = `
		SELECT id, user_id, category_id, url, title, description, created_at, updated_at
		FROM bookmarks
		WHERE user_id = $1
		ORDER BY created_at DESC, id DESC
		LIMIT $2`

	rows, err := s.pool.Query(ctx, listQ, userID, limit)
	if err != nil {
		return nil, q, err
	}

	q.N++

	base, err := pgx.CollectRows(rows, pgx.RowToStructByPos[bookmarkRow])
	if err != nil {
		return nil, q, err
	}

	out := make([]Bookmark, 0, len(base))

	for _, r := range base {
		b := r.toBookmark()

		tagRows, err := s.pool.Query(ctx, `
			SELECT t.name
			FROM bookmark_tags bt
			JOIN tags t ON t.id = bt.tag_id
			WHERE bt.bookmark_id = $1
			ORDER BY t.name`, b.ID)
		if err != nil {
			return nil, q, err
		}

		q.N++

		names, err := pgx.CollectRows(tagRows, pgx.RowTo[string])
		if err != nil {
			return nil, q, err
		}

		b.Tags = names
		out = append(out, b)
	}

	return out, q, nil
}

// ListTwoQueries loads bookmarks and then all their tags at once.
//
// # The fix that does not change the shape of the result
//
// Collect the ids, fetch every tag for all of them in one query with `= ANY($1)`, group them in Go. Two
// queries whatever the page size, and it is the same thing a dataloader does: batch by key.
//
// This is usually the right answer. It keeps the row scan simple, it does not need a driver that understands
// arrays of composite types, and two round trips is not meaningfully worse than one.
func (s *Store) ListTwoQueries(ctx context.Context, userID int64, limit int) ([]Bookmark, Queries, error) {
	var q Queries

	const listQ = `
		SELECT id, user_id, category_id, url, title, description, created_at, updated_at
		FROM bookmarks
		WHERE user_id = $1
		ORDER BY created_at DESC, id DESC
		LIMIT $2`

	rows, err := s.pool.Query(ctx, listQ, userID, limit)
	if err != nil {
		return nil, q, err
	}

	q.N++

	base, err := pgx.CollectRows(rows, pgx.RowToStructByPos[bookmarkRow])
	if err != nil {
		return nil, q, err
	}

	if len(base) == 0 {
		return nil, q, nil
	}

	ids := make([]int64, 0, len(base))
	for _, r := range base {
		ids = append(ids, r.ID)
	}

	tagRows, err := s.pool.Query(ctx, `
		SELECT bt.bookmark_id, t.name
		FROM bookmark_tags bt
		JOIN tags t ON t.id = bt.tag_id
		WHERE bt.bookmark_id = ANY($1::bigint[])
		ORDER BY t.name`, ids)
	if err != nil {
		return nil, q, err
	}

	q.N++

	byBookmark := map[int64][]string{}

	for tagRows.Next() {
		var (
			id   int64
			name string
		)

		if err := tagRows.Scan(&id, &name); err != nil {
			return nil, q, err
		}

		byBookmark[id] = append(byBookmark[id], name)
	}

	// rows.Err() after the loop. The loop ends on the last row AND on an error, and from inside it they look
	// identical. This is the same shape as bufio.Scanner and it is forgotten for the same reason.
	if err := tagRows.Err(); err != nil {
		return nil, q, err
	}

	out := make([]Bookmark, 0, len(base))

	for _, r := range base {
		b := r.toBookmark()
		b.Tags = byBookmark[b.ID]

		out = append(out, b)
	}

	return out, q, nil
}

// ListOneQuery loads bookmarks and their tags in a single statement.
//
// # array_agg with a FILTER
//
// A LEFT JOIN plus array_agg collapses the join's duplicate rows back into one row per bookmark. The FILTER is
// what stops a bookmark with no tags coming back with `{NULL}`: array_agg over a LEFT JOIN's NULL produces a
// one-element array containing NULL, which in Go becomes []string{""} and is a bug nobody spots until a user
// sees an empty tag chip.
//
// # Why this is not automatically the best one
//
// It is one round trip, which matters when the database is far away. It also makes the row scan depend on the
// driver's array handling, makes the query harder to read, and does not compose: adding a second collection to
// the same query needs a second array_agg with its own FILTER and the cardinality gets subtle.
//
// The test measures all three. The honest summary is that the N+1 is the only one that is clearly wrong.
func (s *Store) ListOneQuery(ctx context.Context, userID int64, limit int) ([]Bookmark, Queries, error) {
	const q = `
		SELECT b.id, b.user_id, b.category_id, b.url, b.title, b.description, b.created_at, b.updated_at,
		       coalesce(array_agg(t.name ORDER BY t.name) FILTER (WHERE t.name IS NOT NULL), '{}') AS tags
		FROM bookmarks b
		LEFT JOIN bookmark_tags bt ON bt.bookmark_id = b.id
		LEFT JOIN tags t ON t.id = bt.tag_id
		WHERE b.user_id = $1
		GROUP BY b.id
		ORDER BY b.created_at DESC, b.id DESC
		LIMIT $2`

	rows, err := s.pool.Query(ctx, q, userID, limit)
	if err != nil {
		return nil, Queries{}, err
	}

	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[Bookmark])
	if err != nil {
		return nil, Queries{N: 1}, err
	}

	return out, Queries{N: 1}, nil
}

// SearchResult is a bookmark with its relevance.
type SearchResult struct {
	Bookmark Bookmark
	Rank     float32
}

// Search runs a full-text query against the generated tsvector.
//
// # websearch_to_tsquery, not plainto_tsquery or to_tsquery
//
// to_tsquery takes raw tsquery syntax, so a user typing an apostrophe or an ampersand gets a syntax error
// rather than results. plainto_tsquery ANDs every word and ignores everything else.
//
// websearch_to_tsquery accepts what a person types into a search box: quoted phrases, `or`, and a leading `-`
// for exclusion, and it never raises a syntax error. It is the right default and it is the newest of the
// three, which is why most examples online use one of the others.
//
// # The configuration has to match
//
// The column is built with 'english'. Querying with a different configuration, or with the default that
// depends on a server setting, produces different lexemes and the GIN index is not used: the plan falls back
// to a sequential scan with a recheck and it is slower than no index at all. The test asserts the plan.
func (s *Store) Search(ctx context.Context, userID int64, query string, limit int) ([]SearchResult, error) {
	const q = `
		SELECT b.id, b.user_id, b.category_id, b.url, b.title, b.description, b.created_at, b.updated_at,
		       coalesce(array_agg(t.name ORDER BY t.name) FILTER (WHERE t.name IS NOT NULL), '{}') AS tags,
		       ts_rank(b.search_vector, websearch_to_tsquery('english', $2)) AS rank
		FROM bookmarks b
		LEFT JOIN bookmark_tags bt ON bt.bookmark_id = b.id
		LEFT JOIN tags t ON t.id = bt.tag_id
		WHERE b.user_id = $1
		  AND b.search_vector @@ websearch_to_tsquery('english', $2)
		GROUP BY b.id
		ORDER BY rank DESC, b.created_at DESC
		LIMIT $3`

	rows, err := s.pool.Query(ctx, q, userID, query, limit)
	if err != nil {
		return nil, err
	}

	defer rows.Close()

	var out []SearchResult

	for rows.Next() {
		var (
			r    SearchResult
			tags []string
		)

		if err := rows.Scan(
			&r.Bookmark.ID, &r.Bookmark.UserID, &r.Bookmark.CategoryID,
			&r.Bookmark.URL, &r.Bookmark.Title, &r.Bookmark.Description,
			&r.Bookmark.CreatedAt, &r.Bookmark.UpdatedAt, &tags, &r.Rank,
		); err != nil {
			return nil, err
		}

		r.Bookmark.Tags = tags
		out = append(out, r)
	}

	return out, rows.Err()
}

// ExplainSearch returns the query plan for a search, as text.
//
// It exists so a test can assert on the PLAN rather than on a duration. A duration is a property of the
// machine; a plan node type is a property of the schema, the query, and the row count.
//
// The row count is the part that is easy to forget. The same query against the same schema uses the index at
// twenty thousand rows and reads the table at two hundred, and both are the right answer. A test that asserts
// one without controlling for the other is asserting the size of its own fixture.
func (s *Store) ExplainSearch(ctx context.Context, userID int64, query string) (string, error) {
	const q = `
		EXPLAIN (ANALYZE, FORMAT TEXT)
		SELECT b.id
		FROM bookmarks b
		WHERE b.user_id = $1 AND b.search_vector @@ websearch_to_tsquery('english', $2)`

	rows, err := s.pool.Query(ctx, q, userID, query)
	if err != nil {
		return "", err
	}

	lines, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return "", err
	}

	return strings.Join(lines, "\n"), nil
}

// BookmarksByTag lists a user's bookmarks carrying a tag.
func (s *Store) BookmarksByTag(ctx context.Context, userID int64, tag string, limit int) ([]Bookmark, error) {
	const q = `
		SELECT b.id, b.user_id, b.category_id, b.url, b.title, b.description, b.created_at, b.updated_at,
		       coalesce(array_agg(t2.name ORDER BY t2.name) FILTER (WHERE t2.name IS NOT NULL), '{}')
		FROM bookmarks b
		JOIN bookmark_tags bt ON bt.bookmark_id = b.id
		JOIN tags t ON t.id = bt.tag_id AND t.name = $2::citext
		LEFT JOIN bookmark_tags bt2 ON bt2.bookmark_id = b.id
		LEFT JOIN tags t2 ON t2.id = bt2.tag_id
		WHERE b.user_id = $1
		GROUP BY b.id
		ORDER BY b.created_at DESC
		LIMIT $3`

	rows, err := s.pool.Query(ctx, q, userID, tag, limit)
	if err != nil {
		return nil, err
	}

	return pgx.CollectRows(rows, pgx.RowToStructByPos[Bookmark])
}

// ---------------------------------------------------------------------------
// Refresh tokens
// ---------------------------------------------------------------------------

// RefreshToken is a row of refresh_tokens.
type RefreshToken struct {
	ID        int64
	UserID    int64
	TokenHash []byte
	IssuedAt  time.Time
	ExpiresAt time.Time
	UsedAt    *time.Time
	ParentID  *int64
	Family    string
	RevokedAt *time.Time
}

// Active reports whether the token can be exchanged.
func (r RefreshToken) Active() bool {
	return r.UsedAt == nil && r.RevokedAt == nil && r.ExpiresAt.After(time.Now())
}

// StoreRefreshToken saves a new token, starting a family or continuing one.
func (s *Store) StoreRefreshToken(ctx context.Context, userID int64, hash []byte, expiresAt time.Time, family string, parentID *int64) (*RefreshToken, error) {
	const q = `
		INSERT INTO refresh_tokens (user_id, token_hash, expires_at, family, parent_id)
		VALUES ($1, $2, $3, coalesce(nullif($4, '')::uuid, gen_random_uuid()), $5)
		RETURNING id, user_id, token_hash, issued_at, expires_at, used_at, parent_id, family::text, revoked_at`

	return one[RefreshToken](ctx, s.pool, q, nil, userID, hash, expiresAt, family, parentID)
}

// RefreshTokenByHash finds a token.
func (s *Store) RefreshTokenByHash(ctx context.Context, hash []byte) (*RefreshToken, error) {
	const q = `
		SELECT id, user_id, token_hash, issued_at, expires_at, used_at, parent_id, family::text, revoked_at
		FROM refresh_tokens
		WHERE token_hash = $1`

	return one[RefreshToken](ctx, s.pool, q, nil, hash)
}

// ErrReuse is returned when an already-used refresh token is presented.
var ErrReuse = errors.New("store: refresh token was already used")

// RotateRefreshToken consumes a token and issues its successor, atomically.
//
// # Why one transaction and a conditional UPDATE
//
// Two concurrent refreshes with the same token must not both succeed. The UPDATE carries `WHERE used_at IS
// NULL`, so exactly one of them affects a row; the other sees zero rows affected and knows it lost.
//
// Checking with a SELECT and then updating is the same race with extra steps: both read NULL, both update,
// both issue a token, and the reuse detection never fires.
func (s *Store) RotateRefreshToken(ctx context.Context, oldHash, newHash []byte, ttl time.Duration) (*RefreshToken, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}

	defer func() { _ = tx.Rollback(ctx) }()

	var (
		id     int64
		userID int64
		family string
	)

	// The conditional consume. RETURNING tells us it happened and gives the columns the next insert needs, so
	// there is no second SELECT.
	err = tx.QueryRow(ctx, `
		UPDATE refresh_tokens
		SET used_at = now()
		WHERE token_hash = $1
		  AND used_at IS NULL
		  AND revoked_at IS NULL
		  AND expires_at > now()
		RETURNING id, user_id, family::text`, oldHash).Scan(&id, &userID, &family)

	if errors.Is(err, pgx.ErrNoRows) {
		// Nothing was consumed. Either the token does not exist, or it is expired, revoked, or ALREADY USED.
		// Only the last one is an attack signal, so it needs a second look.
		existing, lookupErr := s.RefreshTokenByHash(ctx, oldHash)
		if lookupErr != nil {
			return nil, fmt.Errorf("%w: refresh token", ErrNotFound)
		}

		if existing.UsedAt != nil {
			return nil, fmt.Errorf("%w: family %s", ErrReuse, existing.Family)
		}

		return nil, fmt.Errorf("%w: refresh token", ErrNotFound)
	}

	if err != nil {
		return nil, err
	}

	const insert = `
		INSERT INTO refresh_tokens (user_id, token_hash, expires_at, family, parent_id)
		VALUES ($1, $2, $3, $4::uuid, $5)
		RETURNING id, user_id, token_hash, issued_at, expires_at, used_at, parent_id, family::text, revoked_at`

	rows, err := tx.Query(ctx, insert, userID, newHash, time.Now().Add(ttl), family, id)
	if err != nil {
		return nil, err
	}

	next, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[RefreshToken])
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	return &next, nil
}

// RevokeFamily revokes every token in a chain.
//
// This is what a reuse triggers. It logs out the session everywhere without touching the user's other
// sessions, which is why the family exists as a separate column from the user.
func (s *Store) RevokeFamily(ctx context.Context, family string) (int64, error) {
	tag, err := s.pool.Exec(ctx,
		`UPDATE refresh_tokens SET revoked_at = now() WHERE family = $1::uuid AND revoked_at IS NULL`, family)
	if err != nil {
		return 0, err
	}

	return tag.RowsAffected(), nil
}

// RevokeToken revokes one token, which is what a logout does.
func (s *Store) RevokeToken(ctx context.Context, hash []byte) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE refresh_tokens SET revoked_at = now() WHERE token_hash = $1 AND revoked_at IS NULL`, hash)
	if err != nil {
		return err
	}

	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: refresh token", ErrNotFound)
	}

	return nil
}

// DeleteExpiredTokens removes tokens nobody can use.
func (s *Store) DeleteExpiredTokens(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM refresh_tokens WHERE expires_at < now()`)
	if err != nil {
		return 0, err
	}

	return tag.RowsAffected(), nil
}

// ---------------------------------------------------------------------------
// Query helpers
// ---------------------------------------------------------------------------

// one runs a query expecting exactly one row.
func one[T any](ctx context.Context, pool *pgxpool.Pool, q string, byConstraint map[string]error, args ...any) (*T, error) {
	rows, err := pool.Query(ctx, q, args...)
	if err != nil {
		return nil, wrapUnique(err, byConstraint)
	}

	v, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[T])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}

		return nil, wrapUnique(err, byConstraint)
	}

	return &v, nil
}

// many runs a query returning a slice.
func many[T any](ctx context.Context, pool *pgxpool.Pool, q string, args ...any) ([]T, error) {
	rows, err := pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}

	return pgx.CollectRows(rows, pgx.RowToStructByPos[T])
}

// wrapUnique turns a Postgres constraint violation into a domain error.
//
// The SQLSTATE is the stable contract. 23505 is unique_violation and 23503 is foreign_key_violation, and both
// are matched by CODE rather than by message, because the message is prose and is localised.
func wrapUnique(err error, byConstraint map[string]error) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return err
	}

	if pgErr.Code != "23505" && pgErr.Code != "23503" {
		return err
	}

	if domain, ok := byConstraint[pgErr.ConstraintName]; ok {
		return fmt.Errorf("%w: %s", domain, pgErr.ConstraintName)
	}

	return err
}
