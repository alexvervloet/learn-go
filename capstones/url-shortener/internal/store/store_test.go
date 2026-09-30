package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/alexvervloet/learn-go/capstones/url-shortener/internal/apitest"
	"github.com/alexvervloet/learn-go/capstones/url-shortener/internal/shortener"
	"github.com/alexvervloet/learn-go/capstones/url-shortener/internal/store"
	"github.com/stretchr/testify/require"
)

func newStore(t *testing.T) (*store.Store, context.Context) {
	t.Helper()

	return apitest.Store(t), context.Background()
}

func user(t *testing.T, s *store.Store, ctx context.Context, email string) *store.User {
	t.Helper()

	u, err := s.CreateUser(ctx, email, "$2a$04$notarealhashbutlongenoughtolooklikeone1234567890abcdefg")
	require.NoError(t, err)

	return u
}

// TestTheUniqueConstraintIsTheCheck is why there is no SELECT before the INSERT.
func TestTheUniqueConstraintIsTheCheck(t *testing.T) {
	s, ctx := newStore(t)

	user(t, s, ctx, "alex@example.com")

	_, err := s.CreateUser(ctx, "alex@example.com", "another-hash")
	require.ErrorIs(t, err, store.ErrEmailTaken,
		"the constraint catches it, so two concurrent registrations cannot both succeed")

	// And the error names the constraint, which is how the code knows WHICH unique index fired. A table with
	// two unique columns needs that; matching on the message would not survive a locale change.
	require.ErrorContains(t, err, "users_email_key")
}

// TestCITEXTMakesTheEmailCaseInsensitive is the column type doing the work.
func TestCITEXTMakesTheEmailCaseInsensitive(t *testing.T) {
	s, ctx := newStore(t)

	created := user(t, s, ctx, "Alex@Example.COM")

	found, err := s.UserByEmail(ctx, "alex@example.com")
	require.NoError(t, err, "no query has to remember to lowercase, because the type does it")
	require.Equal(t, created.ID, found.ID)

	// The stored value keeps the case the user typed, which is what a greeting wants.
	require.Equal(t, "Alex@Example.COM", found.Email)
}

// TestCreateURLDerivesTheSlugFromTheId is the transaction in CreateURL.
func TestCreateURLDerivesTheSlugFromTheId(t *testing.T) {
	s, ctx := newStore(t)

	u := user(t, s, ctx, "alex@example.com")

	first, err := s.CreateURL(ctx, u.ID, "https://example.com/1", "", nil, shortener.Obfuscate)
	require.NoError(t, err)

	// Truncate RESTART IDENTITY means the first URL is id 1 in every test, so the slug is deterministic.
	require.Equal(t, int64(1), first.ID)

	expected, err := shortener.Obfuscate(1)
	require.NoError(t, err)
	require.Equal(t, expected, first.Slug)

	second, err := s.CreateURL(ctx, u.ID, "https://example.com/2", "", nil, shortener.Obfuscate)
	require.NoError(t, err)
	require.Equal(t, int64(2), second.ID)
	require.NotEqual(t, first.Slug, second.Slug)
}

// TestACustomSlugTakesADifferentPath covers the other branch.
func TestACustomSlugTakesADifferentPath(t *testing.T) {
	s, ctx := newStore(t)

	u := user(t, s, ctx, "alex@example.com")

	url, err := s.CreateURL(ctx, u.ID, "https://example.com/docs", "my-docs", nil, shortener.Obfuscate)
	require.NoError(t, err)
	require.Equal(t, "my-docs", url.Slug)

	_, err = s.CreateURL(ctx, u.ID, "https://elsewhere.example/", "my-docs", nil, shortener.Obfuscate)
	require.ErrorIs(t, err, store.ErrSlugTaken)
	require.ErrorContains(t, err, "urls_slug_key")
}

// TestDeleteChecksOwnershipInOneStatement is the race the two-query version has.
func TestDeleteChecksOwnershipInOneStatement(t *testing.T) {
	s, ctx := newStore(t)

	alex := user(t, s, ctx, "alex@example.com")
	sam := user(t, s, ctx, "sam@example.com")

	url, err := s.CreateURL(ctx, alex.ID, "https://example.com/", "", nil, shortener.Obfuscate)
	require.NoError(t, err)

	require.ErrorIs(t, s.DeleteURL(ctx, sam.ID, url.Slug), store.ErrNotFound,
		"the same error as a missing slug, so a caller cannot learn that someone else's slug exists")

	// It is still there.
	found, err := s.URLBySlug(ctx, url.Slug)
	require.NoError(t, err)
	require.Equal(t, url.ID, found.ID)

	require.NoError(t, s.DeleteURL(ctx, alex.ID, url.Slug))

	_, err = s.URLBySlug(ctx, url.Slug)
	require.ErrorIs(t, err, store.ErrNotFound)
}

// TestDeletingAUserCascades is the foreign key's ON DELETE clause.
func TestDeletingAUserCascades(t *testing.T) {
	s, ctx := newStore(t)

	u := user(t, s, ctx, "alex@example.com")

	url, err := s.CreateURL(ctx, u.ID, "https://example.com/", "", nil, shortener.Obfuscate)
	require.NoError(t, err)

	require.NoError(t, s.RecordClick(ctx, url.ID, "https://news.example/", "agent"))

	_, err = s.Pool().Exec(ctx, `DELETE FROM users WHERE id = $1`, u.ID)
	require.NoError(t, err, "with ON DELETE NO ACTION, the default, this would fail")

	_, err = s.URLBySlug(ctx, url.Slug)
	require.ErrorIs(t, err, store.ErrNotFound, "the URL went with the user")

	// And so did the click, through a second cascade.
	var clicks int64
	require.NoError(t, s.Pool().QueryRow(ctx, `SELECT count(*) FROM clicks`).Scan(&clicks))
	require.Zero(t, clicks)
}

// TestRecordClickIsAtomic is the two writes in one transaction.
func TestRecordClickIsAtomic(t *testing.T) {
	s, ctx := newStore(t)

	u := user(t, s, ctx, "alex@example.com")

	url, err := s.CreateURL(ctx, u.ID, "https://example.com/", "", nil, shortener.Obfuscate)
	require.NoError(t, err)

	for range 5 {
		require.NoError(t, s.RecordClick(ctx, url.ID, "https://news.example/", "agent"))
	}

	counter, err := s.ClickCount(ctx, url.Slug)
	require.NoError(t, err)
	require.Equal(t, int64(5), counter)

	actual, err := s.ActualClickCount(ctx, url.Slug)
	require.NoError(t, err)
	require.Equal(t, counter, actual, "the counter is a cache of the clicks table and they agree")
}

// TestConcurrentClicksAllCount is why the increment is in SQL.
//
// click_count = click_count + 1 happens in the database. Reading the value into Go, adding one and writing it
// back is the lost update: two goroutines both read 5, both write 6, and one click vanishes.
func TestConcurrentClicksAllCount(t *testing.T) {
	s, ctx := newStore(t)

	u := user(t, s, ctx, "alex@example.com")

	url, err := s.CreateURL(ctx, u.ID, "https://example.com/", "", nil, shortener.Obfuscate)
	require.NoError(t, err)

	const clicks = 50

	errs := make(chan error, clicks)

	for range clicks {
		go func() { errs <- s.RecordClick(ctx, url.ID, "", "") }()
	}

	for range clicks {
		require.NoError(t, <-errs)
	}

	counter, err := s.ClickCount(ctx, url.Slug)
	require.NoError(t, err)
	require.Equal(t, int64(clicks), counter, "every concurrent increment landed")

	actual, err := s.ActualClickCount(ctx, url.Slug)
	require.NoError(t, err)
	require.Equal(t, int64(clicks), actual)
}

// TestAClickForAMissingURLIsNotFound is what tells the worker to drop a task. A click is recorded
// asynchronously, so the URL can be deleted between the redirect and the worker. The worker drops the task only
// on ErrNotFound, so a foreign-key error here (which is what inserting the click first produced) would be
// retried until it was archived.
func TestAClickForAMissingURLIsNotFound(t *testing.T) {
	s, ctx := newStore(t)

	err := s.RecordClick(ctx, 999_999, "", "")
	require.ErrorIs(t, err, store.ErrNotFound)
}

// TestAnEmptyReferrerIsNULL is the distinction the analytics needs.
func TestAnEmptyReferrerIsNULL(t *testing.T) {
	s, ctx := newStore(t)

	u := user(t, s, ctx, "alex@example.com")

	url, err := s.CreateURL(ctx, u.ID, "https://example.com/", "", nil, shortener.Obfuscate)
	require.NoError(t, err)

	require.NoError(t, s.RecordClick(ctx, url.ID, "", ""))
	require.NoError(t, s.RecordClick(ctx, url.ID, "https://news.example/", "agent"))

	var direct int64
	require.NoError(t, s.Pool().QueryRow(ctx,
		`SELECT count(*) FROM clicks WHERE referrer IS NULL`).Scan(&direct))
	require.Equal(t, int64(1), direct, "WHERE referrer IS NULL finds direct visits; = '' would find none")
}

// TestKeysetPaginationIsStable is the pagination property, at the store layer.
func TestKeysetPaginationIsStable(t *testing.T) {
	s, ctx := newStore(t)

	u := user(t, s, ctx, "alex@example.com")

	for range 10 {
		_, err := s.CreateURL(ctx, u.ID, "https://example.com/", "", nil, shortener.Obfuscate)
		require.NoError(t, err)
	}

	first, err := s.URLsByUser(ctx, u.ID, 4, nil, 0)
	require.NoError(t, err)
	require.Len(t, first, 4)

	last := first[len(first)-1]

	second, err := s.URLsByUser(ctx, u.ID, 4, &last.CreatedAt, last.ID)
	require.NoError(t, err)
	require.Len(t, second, 4)

	for _, a := range first {
		for _, b := range second {
			require.NotEqual(t, a.ID, b.ID, "no row appears on two pages")
		}
	}
}

// TestTheTupleComparisonHandlesATie is why (created_at, id) and not created_at alone.
//
// Ten URLs created in the same transaction share a created_at to the microsecond, because now() is fixed for a
// transaction. A cursor on created_at alone would return the same page forever or skip the whole tie.
func TestTheTupleComparisonHandlesATie(t *testing.T) {
	s, ctx := newStore(t)

	u := user(t, s, ctx, "alex@example.com")

	// Inserted directly with one timestamp, so every row ties.
	same := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)

	for i := 1; i <= 6; i++ {
		_, err := s.Pool().Exec(ctx,
			`INSERT INTO urls (slug, target, user_id, created_at) VALUES ($1, $2, $3, $4)`,
			"tie"+string(rune('a'+i)), "https://example.com/", u.ID, same)
		require.NoError(t, err)
	}

	var seen []int64

	var (
		cursor *time.Time
		lastID int64
	)

	for range 5 {
		page, err := s.URLsByUser(ctx, u.ID, 2, cursor, lastID)
		require.NoError(t, err)

		if len(page) == 0 {
			break
		}

		for _, url := range page {
			seen = append(seen, url.ID)
		}

		last := page[len(page)-1]
		cursor, lastID = &last.CreatedAt, last.ID
	}

	require.Len(t, seen, 6, "every row was returned exactly once despite the tie")

	unique := map[int64]bool{}
	for _, id := range seen {
		require.False(t, unique[id], "id %d appeared twice", id)
		unique[id] = true
	}
}

// TestDeleteExpiredRemovesOnlyThePastOnes is the sweep.
func TestDeleteExpiredRemovesOnlyThePastOnes(t *testing.T) {
	s, ctx := newStore(t)

	u := user(t, s, ctx, "alex@example.com")

	future := time.Now().Add(time.Hour)
	past := time.Now().Add(-time.Hour)

	never, err := s.CreateURL(ctx, u.ID, "https://example.com/never", "", nil, shortener.Obfuscate)
	require.NoError(t, err)

	later, err := s.CreateURL(ctx, u.ID, "https://example.com/later", "", &future, shortener.Obfuscate)
	require.NoError(t, err)

	expired, err := s.CreateURL(ctx, u.ID, "https://example.com/expired", "", &past, shortener.Obfuscate)
	require.NoError(t, err)

	deleted, err := s.DeleteExpired(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(1), deleted)

	_, err = s.URLBySlug(ctx, expired.Slug)
	require.ErrorIs(t, err, store.ErrNotFound)

	for _, url := range []*store.URL{never, later} {
		_, err := s.URLBySlug(ctx, url.Slug)
		require.NoError(t, err, "slug %s survived", url.Slug)
	}
}

// TestExpiredIsCheckedInGoNotInTheQuery is why a lookup returns an expired row.
//
// Filtering expired rows out of URLBySlug would make "never existed" and "expired" indistinguishable, and those
// deserve a 404 and a 410 respectively.
func TestExpiredIsCheckedInGoNotInTheQuery(t *testing.T) {
	s, ctx := newStore(t)

	u := user(t, s, ctx, "alex@example.com")

	past := time.Now().Add(-time.Hour)

	url, err := s.CreateURL(ctx, u.ID, "https://example.com/", "", &past, shortener.Obfuscate)
	require.NoError(t, err)

	found, err := s.URLBySlug(ctx, url.Slug)
	require.NoError(t, err, "the row comes back, so the handler can tell expired from absent")
	require.True(t, found.Expired())
}

// TestAGeneratedSlugSkipsOneAPersonTook is the collision between the two kinds of slug. Someone chose, as a
// custom slug, the string the next id encodes to. The next user, who chose no slug at all, must still get a URL
// rather than a 409 for a slug they never asked for.
func TestAGeneratedSlugSkipsOneAPersonTook(t *testing.T) {
	s, ctx := newStore(t)

	u := user(t, s, ctx, "alex@example.com")

	_, err := s.CreateURL(ctx, u.ID, "https://example.com/mine", "squatted", nil, shortener.Obfuscate)
	require.NoError(t, err)

	// The slug function lands on the squatted slug first, the way id 90,000 would next month.
	calls := 0
	slugFor := func(id int64) (string, error) {
		calls++
		if calls == 1 {
			return "squatted", nil
		}

		return shortener.Obfuscate(id)
	}

	url, err := s.CreateURL(ctx, u.ID, "https://example.com/theirs", "", nil, slugFor)
	require.NoError(t, err)
	require.NotEqual(t, "squatted", url.Slug)
	require.Equal(t, 2, calls, "one taken slug costs one extra id, not the request")
}
