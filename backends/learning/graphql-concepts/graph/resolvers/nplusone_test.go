package resolvers_test

import (
	"testing"
	"time"

	"github.com/alexvervloet/learn-go/backends/learning/graphql-concepts/gqltest"
	"github.com/alexvervloet/learn-go/backends/learning/graphql-concepts/graph/resolvers"
	"github.com/alexvervloet/learn-go/backends/learning/graphql-concepts/store"
)

// TestNPlusOneIsTheDefault is the measurement this module exists for.
//
// The query asks for 20 authors and their books. That is 1 query for the authors and 20 for the books, and
// nothing in the schema or the resolver says so.
func TestNPlusOneIsTheDefault(t *testing.T) {
	const authors = 20

	srv := gqltest.New(t, gqltest.Options{
		Authors:        authors,
		BooksPerAuthor: 3,
		UseLoaders:     false,
	})

	query := `{ authors(limit: 20) { id name books { id title } } }`

	var resp struct {
		Authors []struct {
			ID    string
			Name  string
			Books []struct {
				ID    string
				Title string
			}
		}
	}

	srv.Store.Reset()

	srv.Query(t, query, &resp)

	queries := srv.Store.Queries()
	rows := srv.Store.Rows()

	t.Logf("%d authors, %d books each", len(resp.Authors), len(resp.Authors[0].Books))
	t.Logf("%d queries, %d rows", queries, rows)

	if len(resp.Authors) != authors {
		t.Fatalf("got %d authors", len(resp.Authors))
	}

	// One for the list plus one per author.
	if queries != authors+1 {
		t.Errorf("made %d queries, want %d (1 + %d)", queries, authors+1, authors)
	}

	t.Logf("%d queries for one request, and the resolver that caused it is four lines with no "+
		"loop in it", queries)
}

// TestDataloaderCollapsesIt is the fix, measured against the same store.
func TestDataloaderCollapsesIt(t *testing.T) {
	const authors = 20

	srv := gqltest.New(t, gqltest.Options{
		Authors:        authors,
		BooksPerAuthor: 3,
		UseLoaders:     true,
		LoaderWait:     5 * time.Millisecond,
	})

	query := `{ authors(limit: 20) { id name books { id title } } }`

	// Every field the query selects has to be in this struct. gqlgen's test client decodes STRICTLY
	// and reports "has invalid keys: name" for a field the query asked for and the struct does not
	// have, which is the opposite of encoding/json's behaviour and catches a query and a struct
	// drifting apart.
	var resp struct {
		Authors []struct {
			ID    string
			Name  string
			Books []struct {
				ID    string
				Title string
			}
		}
	}

	srv.Store.Reset()

	srv.Query(t, query, &resp)

	queries := srv.Store.Queries()
	rows := srv.Store.Rows()

	t.Logf("%d queries, %d rows", queries, rows)
	t.Logf("loader: %s", srv.Loaders.BooksByAuthor.Stats())

	if len(resp.Authors) != authors {
		t.Fatalf("got %d authors", len(resp.Authors))
	}

	// One for the list, one for every author's books.
	if queries != 2 {
		t.Errorf("made %d queries, want 2", queries)
	}

	stats := srv.Loaders.BooksByAuthor.Stats()

	if stats.Loads != authors {
		t.Errorf("the loader saw %d Load calls, want %d", stats.Loads, authors)
	}
	if stats.Batches != 1 {
		t.Errorf("the loader made %d batches, want 1", stats.Batches)
	}

	// The ROW count is unchanged, which is the assertion that stops a "fix" that fetches the whole
	// table once.
	if rows != int64(authors+authors*3) {
		t.Errorf("returned %d rows, want %d", rows, authors+authors*3)
	}

	t.Logf("%d resolver calls became %d batch, %d queries instead of %d, and the same %d rows",
		stats.Loads, stats.Batches, queries, authors+1, rows)
}

// TestTheLoaderCachesWithinARequest is the second half of what a dataloader does.
func TestTheLoaderCachesWithinARequest(t *testing.T) {
	srv := gqltest.New(t, gqltest.Options{
		Authors:        10,
		BooksPerAuthor: 3,
		UseLoaders:     true,
		LoaderWait:     5 * time.Millisecond,
	})

	// books AND bookCount, which both resolve through the same loader key.
	query := `{ authors(limit: 10) { id books { id } bookCount } }`

	var resp struct {
		Authors []struct {
			ID        string
			Books     []struct{ ID string }
			BookCount int
		}
	}

	srv.Store.Reset()

	srv.Query(t, query, &resp)

	stats := srv.Loaders.BooksByAuthor.Stats()

	t.Logf("%d queries; loader: %s", srv.Store.Queries(), stats)

	for _, a := range resp.Authors {
		if a.BookCount != len(a.Books) {
			t.Errorf("author %s: bookCount %d, %d books", a.ID, a.BookCount, len(a.Books))
		}
	}

	// 20 Load calls (books and bookCount for each of 10 authors), one batch, and 10 cache hits.
	if stats.Loads != 20 {
		t.Errorf("%d loads, want 20", stats.Loads)
	}
	if stats.Batches != 1 {
		t.Errorf("%d batches, want 1", stats.Batches)
	}
	if stats.Hits != 10 {
		t.Errorf("%d cache hits, want 10", stats.Hits)
	}

	if srv.Store.Queries() != 2 {
		t.Errorf("%d queries, want 2", srv.Store.Queries())
	}

	t.Log("two fields asking for the same key is one fetch. Without the cache it would be two " +
		"batches, because the second field's Load arrives after the first batch has fired.")
}

// TestTheNPlusOneCompoundsWithDepth, which is what makes it worse in GraphQL than in a REST loop.
func TestNPlusOneCompoundsWithDepth(t *testing.T) {
	const authors = 10

	// authors -> books -> author: three levels, and the third resolves a field on every book.
	query := `{ authors(limit: 10) { id books { id author { id name } } } }`

	naive := gqltest.New(t, gqltest.Options{
		Authors: authors, BooksPerAuthor: 3, UseLoaders: false,
	})

	var resp struct {
		Authors []struct {
			ID    string
			Books []struct {
				ID     string
				Author struct {
					ID   string
					Name string
				}
			}
		}
	}

	naive.Store.Reset()
	naive.Query(t, query, &resp)

	naiveQueries := naive.Store.Queries()

	batched := gqltest.New(t, gqltest.Options{
		Authors: authors, BooksPerAuthor: 3, UseLoaders: true, LoaderWait: 5 * time.Millisecond,
	})

	batched.Store.Reset()
	batched.Query(t, query, &resp)

	batchedQueries := batched.Store.Queries()

	t.Logf("10 authors, 3 books each, asking for each book's author")
	t.Logf("  naive:   %d queries", naiveQueries)
	t.Logf("  batched: %d queries", batchedQueries)
	t.Logf("  %.0fx", float64(naiveQueries)/float64(batchedQueries))

	// 1 for the authors, 10 for their books, 30 for the books' authors.
	if naiveQueries != 1+authors+authors*3 {
		t.Errorf("naive made %d queries, want %d", naiveQueries, 1+authors+authors*3)
	}

	// 1 for the authors, 1 batch for the books, 1 batch for the authors.
	if batchedQueries != 3 {
		t.Errorf("batched made %d queries, want 3", batchedQueries)
	}

	t.Log("each level multiplies. A query three levels deep over lists of 10 is 1 + 10 + 100 " +
		"queries without loaders and 3 with them, and the client that wrote it cannot see " +
		"the difference.")
}

// TestPrimingSkipsABatch, the optimisation nobody uses.
//
// Driven against the loader directly rather than through a query, because priming belongs INSIDE the parent
// resolver and this schema's resolvers do not do it. Showing the mechanism is more honest than a test that
// primes from the outside and claims the resolver did it.
func TestPrimingSkipsABatch(t *testing.T) {
	ctx := gqltest.Context(t, 5*time.Second)

	s := store.New(5, 2)

	loaders := resolvers.NewLoaders(s, 5*time.Millisecond)

	// Without priming: a Load is a batch.
	s.Reset()

	if _, err := loaders.AuthorByID.Load(ctx, "author-1"); err != nil {
		t.Fatal(err)
	}

	unprimedQueries := s.Queries()
	unprimedStats := loaders.AuthorByID.Stats()

	t.Logf("without priming: %d store queries, loader %s", unprimedQueries, unprimedStats)

	// With priming: the parent resolver already had the authors, so it puts them in the cache and the
	// child field is a hit.
	primed := resolvers.NewLoaders(s, 5*time.Millisecond)

	authors, err := s.Authors(ctx, 5)
	if err != nil {
		t.Fatal(err)
	}

	for _, a := range authors {
		primed.AuthorByID.Prime(a.ID, a)
	}

	s.Reset()

	for _, a := range authors {
		got, err := primed.AuthorByID.Load(ctx, a.ID)
		if err != nil {
			t.Fatal(err)
		}

		if got.Name != a.Name {
			t.Errorf("primed value for %s is %q", a.ID, got.Name)
		}
	}

	primedQueries := s.Queries()
	primedStats := primed.AuthorByID.Stats()

	t.Logf("with priming: %d store queries for 5 loads, loader %s", primedQueries, primedStats)

	if unprimedQueries != 1 {
		t.Errorf("an unprimed Load made %d queries, want 1", unprimedQueries)
	}
	if primedQueries != 0 {
		t.Errorf("five primed Loads made %d queries, want 0", primedQueries)
	}
	if primedStats.Batches != 0 {
		t.Errorf("the primed loader made %d batches, want 0", primedStats.Batches)
	}
	if primedStats.Hits != 5 {
		t.Errorf("%d cache hits, want 5", primedStats.Hits)
	}

	// Priming an already-cached key does NOT overwrite it, which is the right choice: a batch in
	// flight has a future for that key and replacing it would leave waiters on a future nobody
	// completes.
	primed.AuthorByID.Prime("author-1", store.Author{ID: "author-1", Name: "OVERWRITTEN"})

	got, err := primed.AuthorByID.Load(ctx, "author-1")
	if err != nil {
		t.Fatal(err)
	}

	if got.Name == "OVERWRITTEN" {
		t.Error("Prime overwrote a cached value, which would strand any waiter on the " +
			"in-flight future for that key")
	}

	t.Log("a resolver that already has the objects can Prime the loader with them, and the child " +
		"field becomes a cache hit rather than a batch: 5 loads, 0 queries, 0 batches")
}

// TestNoLoadersIsAnError rather than a silent per-call loader.
func TestNoLoadersIsAnError(t *testing.T) {
	// UseLoaders on, but the harness only installs them when UseLoaders is set, so this builds a
	// server whose resolvers expect loaders and gets none by driving the schema without the hook.
	srv := gqltest.New(t, gqltest.Options{
		Authors: 3, BooksPerAuthor: 2, UseLoaders: true, LoaderWait: time.Millisecond,
	})

	// Replace the client with one that does NOT install loaders, which is what a forgotten middleware
	// looks like.
	bare := gqltest.WithoutLoaders(t, srv)

	resp := bare.RawQuery(t, `{ authors(limit: 3) { books { id } } }`, nil)

	t.Logf("with the middleware missing: %s", resp)

	if !resp.HasError() {
		t.Fatal("a missing dataloader middleware produced no error")
	}

	if !containsAny(resp.ErrorMessage, "middleware did not run") {
		t.Errorf("the error does not say what is wrong: %v", resp.Err)
	}

	t.Log("an error rather than a lazily created per-call loader. A per-call loader batches one " +
		"key and looks like it is working, so the N+1 is still there and the code says " +
		"dataloader.")
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if len(sub) > 0 && len(s) >= len(sub) {
			for i := 0; i+len(sub) <= len(s); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
		}
	}
	return false
}
