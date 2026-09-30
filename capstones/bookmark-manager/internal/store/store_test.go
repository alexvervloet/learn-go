package store_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/alexvervloet/learn-go/capstones/bookmark-manager/internal/apitest"
	"github.com/alexvervloet/learn-go/capstones/bookmark-manager/internal/store"
	"github.com/stretchr/testify/require"
)

func newStore(t *testing.T) (*store.Store, context.Context) {
	t.Helper()

	return apitest.Store(t), context.Background()
}

func user(t *testing.T, s *store.Store, ctx context.Context, email string) *store.User {
	t.Helper()

	u, err := s.CreateUser(ctx, email, "$2a$04$averylonglookinghashthatislongenoughtopassforone12345")
	require.NoError(t, err)

	return u
}

// TestEnsureTagsReturnsExistingOnesToo is the ON CONFLICT trap.
//
// `INSERT ... ON CONFLICT DO NOTHING RETURNING` returns only the rows it INSERTED. A tag that already existed
// conflicts, does nothing, and is missing from RETURNING, so a caller that trusts the result silently drops
// every existing tag and the bug reads as "tags disappear sometimes".
func TestEnsureTagsReturnsExistingOnesToo(t *testing.T) {
	s, ctx := newStore(t)

	u := user(t, s, ctx, "alex@example.com")

	first, err := s.EnsureTags(ctx, u.ID, []string{"go", "databases"})
	require.NoError(t, err)
	require.Len(t, first, 2)

	// Two that exist and one that does not. The naive query would return one row.
	second, err := s.EnsureTags(ctx, u.ID, []string{"go", "databases", "testing"})
	require.NoError(t, err)
	require.Len(t, second, 3, "existing tags come back too, or a bookmark loses them")

	names := make([]string, 0, len(second))
	for _, tag := range second {
		names = append(names, tag.Name)
	}

	require.ElementsMatch(t, []string{"go", "databases", "testing"}, names)

	// And nothing was duplicated: the ids of the two that already existed are unchanged.
	require.Equal(t, first[0].ID, findTag(t, second, first[0].Name).ID)
	require.Equal(t, first[1].ID, findTag(t, second, first[1].Name).ID)
}

func findTag(t *testing.T, tags []store.Tag, name string) store.Tag {
	t.Helper()

	for _, tag := range tags {
		if strings.EqualFold(tag.Name, name) {
			return tag
		}
	}

	t.Fatalf("tag %q not in %v", name, tags)

	return store.Tag{}
}

// TestEnsureTagsDeduplicatesBeforeInserting is the other half of the same statement.
//
// ON CONFLICT does not fire for two rows in the SAME statement. Postgres reports
// `ON CONFLICT DO UPDATE command cannot affect row a second time` and the whole statement fails. Because the
// column is CITEXT, a request with ["go", "Go"] is exactly that case.
func TestEnsureTagsDeduplicatesBeforeInserting(t *testing.T) {
	s, ctx := newStore(t)

	u := user(t, s, ctx, "alex@example.com")

	tags, err := s.EnsureTags(ctx, u.ID, []string{"go", "Go", "GO", "  go  "})
	require.NoError(t, err)
	require.Len(t, tags, 1, "CITEXT makes these one tag, and the statement would fail without deduplicating")
}

// TestTagsAreScopedToAUser is the composite unique doing its job.
func TestTagsAreScopedToAUser(t *testing.T) {
	s, ctx := newStore(t)

	alex := user(t, s, ctx, "alex@example.com")
	sam := user(t, s, ctx, "sam@example.com")

	alexTags, err := s.EnsureTags(ctx, alex.ID, []string{"reading"})
	require.NoError(t, err)

	samTags, err := s.EnsureTags(ctx, sam.ID, []string{"reading"})
	require.NoError(t, err)

	require.NotEqual(t, alexTags[0].ID, samTags[0].ID,
		"two people can both have a 'reading' tag; a global UNIQUE (name) would be a land grab")
}

// TestDeletingACategoryKeepsItsBookmarks is the foreign key clause that matters.
func TestDeletingACategoryKeepsItsBookmarks(t *testing.T) {
	s, ctx := newStore(t)

	u := user(t, s, ctx, "alex@example.com")

	category, err := s.CreateCategory(ctx, u.ID, "Reading")
	require.NoError(t, err)

	b, err := s.CreateBookmark(ctx, u.ID, store.NewBookmark{
		CategoryID: &category.ID,
		URL:        "https://go.dev/doc/effective_go",
		Title:      "Effective Go",
	})
	require.NoError(t, err)
	require.Equal(t, category.ID, *b.CategoryID)

	require.NoError(t, s.DeleteCategory(ctx, u.ID, category.ID))

	after, _, err := s.ListTwoQueries(ctx, u.ID, 10)
	require.NoError(t, err)
	require.Len(t, after, 1, "ON DELETE SET NULL, not CASCADE: the bookmark survives")
	require.Nil(t, after[0].CategoryID)
}

// TestDeletingAUserCascadesEverything is the other direction.
func TestDeletingAUserCascadesEverything(t *testing.T) {
	s, ctx := newStore(t)

	u := user(t, s, ctx, "alex@example.com")

	_, err := s.CreateBookmark(ctx, u.ID, store.NewBookmark{
		URL: "https://go.dev/", Title: "Go", Tags: []string{"go"},
	})
	require.NoError(t, err)

	_, err = s.Pool().Exec(ctx, `DELETE FROM users WHERE id = $1`, u.ID)
	require.NoError(t, err)

	for _, table := range []string{"bookmarks", "tags", "bookmark_tags"} {
		var n int64

		require.NoError(t, s.Pool().QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&n))
		require.Zero(t, n, "%s still has rows", table)
	}
}

// TestTheSameURLTwiceIsAConflictPerUser is the composite unique on bookmarks.
func TestTheSameURLTwiceIsAConflictPerUser(t *testing.T) {
	s, ctx := newStore(t)

	alex := user(t, s, ctx, "alex@example.com")
	sam := user(t, s, ctx, "sam@example.com")

	_, err := s.CreateBookmark(ctx, alex.ID, store.NewBookmark{URL: "https://go.dev/", Title: "Go"})
	require.NoError(t, err)

	_, err = s.CreateBookmark(ctx, alex.ID, store.NewBookmark{URL: "https://go.dev/", Title: "Go again"})
	require.ErrorIs(t, err, store.ErrURLSaved)

	_, err = s.CreateBookmark(ctx, sam.ID, store.NewBookmark{URL: "https://go.dev/", Title: "Go"})
	require.NoError(t, err, "a different user saving the same URL is not a conflict")
}

// seedBookmarks creates n bookmarks each with two tags.
func seedBookmarks(t *testing.T, s *store.Store, ctx context.Context, userID int64, n int) {
	t.Helper()

	for i := range n {
		_, err := s.CreateBookmark(ctx, userID, store.NewBookmark{
			URL:         fmt.Sprintf("https://example.com/%d", i),
			Title:       fmt.Sprintf("Article %d", i),
			Description: "something about programming",
			Tags:        []string{fmt.Sprintf("tag%d", i%5), "shared"},
		})
		require.NoError(t, err)
	}
}

// TestTheThreeLoadersAgreeAndCostDifferently is the N+1, measured.
//
// All three return the same bookmarks with the same tags. The query COUNT is the number that differs, and it is
// the same on every machine, which is why it is the assertion and the durations are a log line.
func TestTheThreeLoadersAgreeAndCostDifferently(t *testing.T) {
	s, ctx := newStore(t)

	u := user(t, s, ctx, "alex@example.com")

	const n = 30

	seedBookmarks(t, s, ctx, u.ID, n)

	start := time.Now()
	nPlusOne, qA, err := s.ListNPlusOne(ctx, u.ID, n)
	require.NoError(t, err)
	elapsedA := time.Since(start)

	start = time.Now()
	two, qB, err := s.ListTwoQueries(ctx, u.ID, n)
	require.NoError(t, err)
	elapsedB := time.Since(start)

	start = time.Now()
	one, qC, err := s.ListOneQuery(ctx, u.ID, n)
	require.NoError(t, err)
	elapsedC := time.Since(start)

	require.Len(t, nPlusOne, n)
	require.Len(t, two, n)
	require.Len(t, one, n)

	// The same answer, in the same order, with the same tags.
	for i := range n {
		require.Equal(t, nPlusOne[i].ID, two[i].ID, "row %d", i)
		require.Equal(t, nPlusOne[i].ID, one[i].ID, "row %d", i)
		require.ElementsMatch(t, nPlusOne[i].Tags, two[i].Tags, "row %d", i)
		require.ElementsMatch(t, nPlusOne[i].Tags, one[i].Tags, "row %d", i)
	}

	// THE assertion. 31 against 2 against 1, and the 31 grows with the page size while the others do not.
	require.Equal(t, n+1, qA.N, "one query for the list plus one per bookmark")
	require.Equal(t, 2, qB.N, "one for the list, one for every tag of every bookmark")
	require.Equal(t, 1, qC.N, "array_agg over a LEFT JOIN")

	t.Logf("N+1: %d queries in %v", qA.N, elapsedA)
	t.Logf("two: %d queries in %v", qB.N, elapsedB)
	t.Logf("one: %d queries in %v", qC.N, elapsedC)
	t.Logf("the counts are the claim; the durations are against a Postgres on this machine and mean little")
}

// TestArrayAggFiltersOutTheNulls is the bug the FILTER prevents.
//
// array_agg over a LEFT JOIN that matched nothing produces a one-element array containing NULL, which arrives
// in Go as []string{""} and shows up as an empty tag in a UI. The FILTER is what makes it an empty array.
func TestArrayAggFiltersOutTheNulls(t *testing.T) {
	s, ctx := newStore(t)

	u := user(t, s, ctx, "alex@example.com")

	_, err := s.CreateBookmark(ctx, u.ID, store.NewBookmark{URL: "https://go.dev/", Title: "No tags here"})
	require.NoError(t, err)

	one, _, err := s.ListOneQuery(ctx, u.ID, 10)
	require.NoError(t, err)
	require.Len(t, one, 1)
	require.Empty(t, one[0].Tags, "an empty array, not a one-element array holding an empty string")

	// Without the FILTER this would be []string{""} with length 1. Asserting the length rather than just
	// emptiness makes that explicit.
	require.Len(t, one[0].Tags, 0)
}

// TestSearchRanksTitleAboveDescription is what setweight buys.
func TestSearchRanksTitleAboveDescription(t *testing.T) {
	s, ctx := newStore(t)

	u := user(t, s, ctx, "alex@example.com")

	_, err := s.CreateBookmark(ctx, u.ID, store.NewBookmark{
		URL: "https://a.example/", Title: "An introduction to goroutines", Description: "unrelated text",
	})
	require.NoError(t, err)

	_, err = s.CreateBookmark(ctx, u.ID, store.NewBookmark{
		URL: "https://b.example/", Title: "Unrelated title", Description: "this one mentions goroutines briefly",
	})
	require.NoError(t, err)

	results, err := s.Search(ctx, u.ID, "goroutines", 10)
	require.NoError(t, err)
	require.Len(t, results, 2)

	require.Equal(t, "An introduction to goroutines", results[0].Bookmark.Title,
		"a title match outranks a description match, because setweight marks them A and B")
	require.Greater(t, results[0].Rank, results[1].Rank)

	t.Logf("ranks: %.6f (title) against %.6f (description)", results[0].Rank, results[1].Rank)
}

// TestSearchStemsAndIgnoresStopWords is what the english configuration does.
func TestSearchStemsAndIgnoresStopWords(t *testing.T) {
	s, ctx := newStore(t)

	u := user(t, s, ctx, "alex@example.com")

	_, err := s.CreateBookmark(ctx, u.ID, store.NewBookmark{
		URL: "https://a.example/", Title: "Running tests in parallel",
	})
	require.NoError(t, err)

	// "run" finds "Running": the english configuration stems both to "run". A LIKE '%run%' would too, and
	// would also match "runtime" and "overrun" and could not use an index.
	stemmed, err := s.Search(ctx, u.ID, "run", 10)
	require.NoError(t, err)
	require.Len(t, stemmed, 1, "the english stemmer reduces Running and run to the same lexeme")

	// "the" finds nothing, because it is a stop word and is not in the vector at all.
	stopWord, err := s.Search(ctx, u.ID, "the", 10)
	require.NoError(t, err)
	require.Empty(t, stopWord)
}

// TestWebsearchSyntaxIsAcceptedWithoutErrors is why websearch_to_tsquery.
//
// to_tsquery raises a syntax error on anything that is not tsquery syntax, so a user typing an apostrophe gets
// a 500. plainto_tsquery ANDs every word and ignores quotes and negation. websearch_to_tsquery accepts what a
// person types into a search box and never raises.
func TestWebsearchSyntaxIsAcceptedWithoutErrors(t *testing.T) {
	s, ctx := newStore(t)

	u := user(t, s, ctx, "alex@example.com")

	_, err := s.CreateBookmark(ctx, u.ID, store.NewBookmark{
		URL: "https://a.example/", Title: "Testing concurrent Go programs",
	})
	require.NoError(t, err)

	tests := []struct {
		query string
		want  int
		why   string
	}{
		{`concurrent`, 1, "a plain word"},
		{`"concurrent go"`, 1, "a quoted phrase, which plainto_tsquery would ignore the quotes of"},
		{`concurrent -python`, 1, "a leading minus excludes"},
		{`concurrent -go`, 0, "and the exclusion applies"},
		{`rust or concurrent`, 1, "`or` is a word here, not an operator a user has to know"},
		{`it's & | ! ( )`, 0, "punctuation that to_tsquery would reject with a syntax error"},
		{`concurrent!!!`, 1, "and trailing punctuation is ignored"},
	}

	for _, tc := range tests {
		t.Run(tc.query, func(t *testing.T) {
			results, err := s.Search(ctx, u.ID, tc.query, 10)
			require.NoError(t, err, tc.why)
			require.Len(t, results, tc.want, tc.why)
		})
	}
}

// TestSearchUsesTheGinIndexOnceItIsWorthIt asserts on the plan, with a fixture that means something.
//
// # Three wrong fixtures before this one
//
// A plan depends on the schema, the query AND the row count, and the third is the one that is easy to forget.
// Earlier versions of this test asserted the GIN index was used and failed on a correct schema:
//
//   - 500 bookmarks for one user: the planner read them through the btree on (user_id, created_at, id) and
//     filtered by the tsvector. Cheaper, and correct.
//   - 20 users with 500 each: the same plan, because what matters is how many rows `user_id = $1` selects.
//   - 5,000 for one user with every title identical: a sequential scan beat both indexes, partly because the
//     search term had no statistics to be selective against.
//
// Twenty thousand rows with varied text is where the GIN scan wins on its own, with no hint and no setting.
// That is also roughly where search stops being a feature nobody needs.
func TestSearchUsesTheGinIndexOnceItIsWorthIt(t *testing.T) {
	s, ctx := newStore(t)

	u := user(t, s, ctx, "alex@example.com")

	// One statement rather than twenty thousand round trips. The titles and descriptions VARY, so the column
	// has real statistics: with every row carrying identical text, a rare term has nothing to be rare
	// against and the planner's selectivity estimate is a default.
	_, err := s.Pool().Exec(ctx, `
		INSERT INTO bookmarks (user_id, url, title, description)
		SELECT $1,
		       'https://example.com/' || i,
		       'Article ' || i || ' about topic' || (i % 500),
		       'body text number ' || i || ' discussing subject' || (i % 300)
		FROM generate_series(1, 20000) AS i`, u.ID)
	require.NoError(t, err)

	_, err = s.CreateBookmark(ctx, u.ID, store.NewBookmark{
		URL: "https://needle.example/", Title: "A treatise on marmalade",
	})
	require.NoError(t, err)

	// ANALYZE, or the planner works from defaults. A bulk insert does not update the statistics and
	// autovacuum has not run yet, so without this the plan is a guess and the test asserts on the guess.
	_, err = s.Pool().Exec(ctx, `ANALYZE bookmarks`)
	require.NoError(t, err)

	plan, err := s.ExplainSearch(ctx, u.ID, "marmalade")
	require.NoError(t, err)

	t.Logf("20,001 rows:\n%s", plan)

	require.Contains(t, plan, "bookmarks_search_idx",
		"a rare term over twenty thousand rows is what the GIN index is for")
	require.NotContains(t, plan, "Seq Scan on bookmarks")

	// And it returns the one row, which is the point of all of it.
	results, err := s.Search(ctx, u.ID, "marmalade", 10)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, "A treatise on marmalade", results[0].Bookmark.Title)
}

// TestOnASmallTableTheScanWins is the other half of the same fact.
//
// Not a test of Postgres: a test of the CLAIM in the migration and the README that whether an index is used
// depends on the row count. In this repository a claim gets checked, and this one also stops somebody
// "simplifying" the fixture above by shrinking it.
func TestOnASmallTableTheScanWins(t *testing.T) {
	s, ctx := newStore(t)

	u := user(t, s, ctx, "alex@example.com")

	_, err := s.Pool().Exec(ctx, `
		INSERT INTO bookmarks (user_id, url, title)
		SELECT $1, 'https://example.com/' || i, 'Article ' || i || ' about topic' || (i % 20)
		FROM generate_series(1, 200) AS i`, u.ID)
	require.NoError(t, err)

	_, err = s.Pool().Exec(ctx, `ANALYZE bookmarks`)
	require.NoError(t, err)

	plan, err := s.ExplainSearch(ctx, u.ID, "marmalade")
	require.NoError(t, err)

	require.NotContains(t, plan, "bookmarks_search_idx",
		"with 200 rows, reading them is cheaper than any index, and the planner is right:\n%s", plan)

	t.Logf("200 rows:\n%s", plan)
}

// TestBookmarksByTagReturnsEveryTag is the shape of the tag filter.
//
// Filtering by one tag must not hide the others. The query joins twice for that reason: once to filter, once
// to collect, and getting it wrong returns bookmarks that appear to have exactly one tag.
func TestBookmarksByTagReturnsEveryTag(t *testing.T) {
	s, ctx := newStore(t)

	u := user(t, s, ctx, "alex@example.com")

	_, err := s.CreateBookmark(ctx, u.ID, store.NewBookmark{
		URL: "https://a.example/", Title: "A", Tags: []string{"go", "testing", "concurrency"},
	})
	require.NoError(t, err)

	_, err = s.CreateBookmark(ctx, u.ID, store.NewBookmark{
		URL: "https://b.example/", Title: "B", Tags: []string{"rust"},
	})
	require.NoError(t, err)

	found, err := s.BookmarksByTag(ctx, u.ID, "testing", 10)
	require.NoError(t, err)
	require.Len(t, found, 1)
	require.ElementsMatch(t, []string{"go", "testing", "concurrency"}, found[0].Tags,
		"the filter picks the bookmark; it must not truncate its tags")

	// CITEXT, so the lookup is case-insensitive without the caller lowercasing.
	upper, err := s.BookmarksByTag(ctx, u.ID, "TESTING", 10)
	require.NoError(t, err)
	require.Len(t, upper, 1)
}

// TestTagsOfIncludesUnusedTags is the LEFT JOIN.
func TestTagsOfIncludesUnusedTags(t *testing.T) {
	s, ctx := newStore(t)

	u := user(t, s, ctx, "alex@example.com")

	_, err := s.EnsureTags(ctx, u.ID, []string{"orphan"})
	require.NoError(t, err)

	_, err = s.CreateBookmark(ctx, u.ID, store.NewBookmark{
		URL: "https://a.example/", Title: "A", Tags: []string{"used"},
	})
	require.NoError(t, err)

	tags, err := s.TagsOf(ctx, u.ID)
	require.NoError(t, err)
	require.Len(t, tags, 2, "an inner join would drop the unused one, which is the one a user wants to delete")

	counts := map[string]int64{}
	for _, tag := range tags {
		counts[tag.Name] = tag.Count
	}

	require.Equal(t, int64(0), counts["orphan"])
	require.Equal(t, int64(1), counts["used"])
}

// TestARejectedBookmarkLeavesNoTagsBehind is the transaction boundary. A bookmark refused as a duplicate must
// not leave the tags it asked for: they were created for it, and it does not exist.
func TestARejectedBookmarkLeavesNoTagsBehind(t *testing.T) {
	s, ctx := newStore(t)

	u := user(t, s, ctx, "alex@example.com")

	_, err := s.CreateBookmark(ctx, u.ID, store.NewBookmark{URL: "https://a.example/", Title: "A"})
	require.NoError(t, err)

	_, err = s.CreateBookmark(ctx, u.ID, store.NewBookmark{
		URL: "https://a.example/", Title: "A again", Tags: []string{"only-for-the-duplicate"},
	})
	require.ErrorIs(t, err, store.ErrURLSaved)

	tags, err := s.TagsOf(ctx, u.ID)
	require.NoError(t, err)
	require.Empty(t, tags, "the tags were created for a bookmark that was never saved")
}
