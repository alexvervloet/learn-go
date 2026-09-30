package resolvers_test

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/alexvervloet/learn-go/backends/learning/graphql-concepts/gqltest"
	"github.com/alexvervloet/learn-go/backends/learning/graphql-concepts/graph/resolvers"
)

// TestErrorsArePartialSuccess is the thing REST has no equivalent for.
//
// A GraphQL response carries `data` AND `errors`. A field that fails is null and the rest of the response is
// still there, and the HTTP status is 200 whatever happened. A client that checks the status code and nothing
// else silently accepts a response where half the fields are null.
func TestErrorsArePartialSuccess(t *testing.T) {
	srv := gqltest.New(t, gqltest.Options{Authors: 3, BooksPerAuthor: 2})

	// One field that works and one that fails, in the same query.
	resp := srv.RawQuery(t, `{
		authors(limit: 2) { id name }
		failing
	}`, nil)

	t.Logf("%s", resp)

	if !resp.HasError() {
		t.Fatal("the failing field produced no error")
	}

	var data struct {
		Authors []struct {
			ID   string
			Name string
		}
		Failing *string
	}

	if err := json.Unmarshal(resp.Data, &data); err != nil {
		t.Fatal(err)
	}

	// The authors are THERE, next to the error.
	if len(data.Authors) != 2 {
		t.Errorf("got %d authors alongside the error", len(data.Authors))
	}
	if data.Failing != nil {
		t.Errorf("the failing field is %v, want null", *data.Failing)
	}

	t.Logf("%d authors returned successfully, and `failing` is null with an entry in `errors`",
		len(data.Authors))

	t.Log("HTTP 200 with half the response missing. A client that checks the status code and " +
		"nothing else accepts this as a success, which is the single most common GraphQL " +
		"client bug.")
}

// TestNullPropagation is the rule that decides how much of a response a failure destroys.
//
// A non-null field that resolves to an error becomes null, which is illegal, so the error propagates UP to the
// nearest nullable parent. If there is none, the whole `data` becomes null.
//
// Which means a single failing field deep inside a response can wipe out the entire response, and whether it
// does is decided by an exclamation mark somewhere in the schema.
func TestNullPropagation(t *testing.T) {
	srv := gqltest.New(t, gqltest.Options{Authors: 3, BooksPerAuthor: 2})

	// `failing: String` is NULLABLE, so the error stops there.
	nullable := srv.RawQuery(t, `{ authors(limit: 1) { id } failing }`, nil)

	t.Logf("a nullable failing field: %s", nullable)

	var data struct {
		Authors []struct{ ID string }
		Failing *string
	}

	if err := json.Unmarshal(nullable.Data, &data); err != nil {
		t.Fatal(err)
	}

	if len(data.Authors) != 1 {
		t.Error("the sibling field was destroyed by a nullable failure")
	}

	// An error inside a NON-NULL field propagates further. `author.books` is `[Book!]!` and the
	// middleware is missing, so the error cannot be contained at `books` and takes the author with
	// it, and `authors` is `[Author!]!` so it takes the list, and `data` becomes null.
	loaded := gqltest.New(t, gqltest.Options{
		Authors: 3, BooksPerAuthor: 2, UseLoaders: true,
	})

	bare := gqltest.WithoutLoaders(t, loaded)

	nonNull := bare.RawQuery(t, `{ authors(limit: 2) { id books { id } } }`, nil)

	t.Logf("an error inside a non-null chain: %s", nonNull)

	if string(nonNull.Data) != "null" {
		t.Errorf("data is %s; a failure inside [Author!]! should have nulled the whole "+
			"response", nonNull.Data)
	}

	t.Log("one failing field nulled the entire response, because every type between it and the " +
		"root is non-null. Whether a failure costs one field or everything is decided by " +
		"exclamation marks in the schema, and that is not obvious when writing them.")
}

// TestTwoErrorModels compares the top-level errors array with a typed result.
func TestTwoErrorModels(t *testing.T) {
	srv := gqltest.New(t, gqltest.Options{Authors: 3, BooksPerAuthor: 2})

	t.Run("plain error", func(t *testing.T) {
		resp := srv.RawQuery(t, `mutation {
			createBookOrError(input: {title: "", authorId: "author-1", priceCents: 100}) {
				id
			}
		}`, nil)

		t.Logf("%s", resp)

		if !resp.HasError() {
			t.Fatal("expected an error")
		}

		// The client got a message string. To tell a validation failure from a database outage it
		// has to match on the text, and the text is not in the schema, not versioned and not
		// typed.
		if !strings.Contains(resp.ErrorMessage, "title must not be empty") {
			t.Errorf("the message is %q", resp.ErrorMessage)
		}

		t.Log("a message string in the errors array. Untyped, unversioned, and the client has " +
			"to match on the text to know what happened.")
	})

	t.Run("typed result", func(t *testing.T) {
		var resp struct {
			CreateBook struct {
				Book   *struct{ ID string }
				Errors []struct {
					Field   *string
					Message string
					Code    string
				}
			}
		}

		srv.Query(t, `mutation {
			createBook(input: {title: "", authorId: "author-1", priceCents: -5}) {
				book { id }
				errors { field message code }
			}
		}`, &resp)

		t.Logf("book: %v, %d errors", resp.CreateBook.Book, len(resp.CreateBook.Errors))
		for _, e := range resp.CreateBook.Errors {
			t.Logf("  %-12s %-12s %s", deref(e.Field), e.Code, e.Message)
		}

		// No top-level error at all: this is a SUCCESSFUL response describing a failed operation.
		if len(resp.CreateBook.Errors) != 2 {
			t.Errorf("got %d errors, want 2", len(resp.CreateBook.Errors))
		}
		if resp.CreateBook.Book != nil {
			t.Error("a book was created despite the validation failures")
		}

		codes := map[string]bool{}
		for _, e := range resp.CreateBook.Errors {
			codes[e.Code] = true
		}

		if !codes["VALIDATION"] {
			t.Error("no VALIDATION code")
		}

		t.Log("typed, in the schema, with a field name and an enum code. A client can switch on " +
			"the code and render the message against the field, and a new error kind is a " +
			"schema change a client can see coming.")
	})

	t.Run("unexpected failures still use the errors array", func(t *testing.T) {
		// A missing author is a NOT_FOUND in the result type, because it is an expected outcome.
		var resp struct {
			CreateBook struct {
				Book   *struct{ ID string }
				Errors []struct {
					Code    string
					Message string
				}
			}
		}

		srv.Query(t, `mutation {
			createBook(input: {title: "Valid", authorId: "nope", priceCents: 100}) {
				book { id }
				errors { code message }
			}
		}`, &resp)

		if len(resp.CreateBook.Errors) != 1 {
			t.Fatalf("got %d errors", len(resp.CreateBook.Errors))
		}

		t.Logf("a missing author: %s %q", resp.CreateBook.Errors[0].Code,
			resp.CreateBook.Errors[0].Message)

		if resp.CreateBook.Errors[0].Code != "NOT_FOUND" {
			t.Errorf("code is %s", resp.CreateBook.Errors[0].Code)
		}

		t.Log("the rule: expected outcomes go in the schema as typed results, and the " +
			"top-level errors array is reserved for bugs, timeouts and permission " +
			"failures. Mixing them is what makes a GraphQL client's error handling a " +
			"pile of string matching.")
	})
}

// TestRelayPagination.
func TestRelayPagination(t *testing.T) {
	srv := gqltest.New(t, gqltest.Options{Authors: 4, BooksPerAuthor: 5})

	type page struct {
		Books struct {
			Edges []struct {
				Cursor string
				Node   struct {
					ID    string
					Title string
				}
			}
			PageInfo struct {
				HasNextPage     bool
				HasPreviousPage bool
				StartCursor     *string
				EndCursor       *string
			}
			TotalCount int
		}
	}

	query := `query($first: Int, $after: String) {
		books(first: $first, after: $after) {
			edges { cursor node { id title } }
			pageInfo { hasNextPage hasPreviousPage startCursor endCursor }
			totalCount
		}
	}`

	var first page

	srv.Query(t, query, &first, gqltest.Var("first", 5))

	t.Logf("page 1: %d edges, hasNext=%v, total=%d",
		len(first.Books.Edges), first.Books.PageInfo.HasNextPage, first.Books.TotalCount)

	for _, e := range first.Books.Edges {
		t.Logf("  %-14s %s", e.Node.ID, e.Cursor)
	}

	if len(first.Books.Edges) != 5 {
		t.Fatalf("got %d edges", len(first.Books.Edges))
	}
	if !first.Books.PageInfo.HasNextPage {
		t.Error("hasNextPage is false with 20 books and a page of 5")
	}
	if first.Books.PageInfo.HasPreviousPage {
		t.Error("hasPreviousPage is true on the first page")
	}
	if first.Books.TotalCount != 20 {
		t.Errorf("totalCount is %d, want 20", first.Books.TotalCount)
	}

	// The cursor is opaque and prefixed, so one from another connection cannot be mistaken for one
	// of these.
	decoded, err := base64.RawURLEncoding.DecodeString(first.Books.Edges[0].Cursor)
	if err != nil {
		t.Fatalf("the cursor is not base64: %v", err)
	}

	t.Logf("a decoded cursor: %q", decoded)

	if !strings.HasPrefix(string(decoded), "book:") {
		t.Errorf("the cursor has no type prefix: %q", decoded)
	}

	// Page 2, from the end cursor.
	var second page

	srv.Query(t, query, &second,
		gqltest.Var("first", 5),
		gqltest.Var("after", *first.Books.PageInfo.EndCursor))

	t.Logf("page 2: %d edges, hasPrevious=%v", len(second.Books.Edges),
		second.Books.PageInfo.HasPreviousPage)

	if len(second.Books.Edges) != 5 {
		t.Fatalf("got %d edges on page 2", len(second.Books.Edges))
	}
	if !second.Books.PageInfo.HasPreviousPage {
		t.Error("hasPreviousPage is false on page 2")
	}

	// No overlap between the pages, which is the property offset pagination loses.
	seen := map[string]bool{}
	for _, e := range first.Books.Edges {
		seen[e.Node.ID] = true
	}

	for _, e := range second.Books.Edges {
		if seen[e.Node.ID] {
			t.Errorf("%s appeared on both pages", e.Node.ID)
		}
	}

	t.Log("no overlap. The cursor names a position in the data, so a row inserted before it does " +
		"not shift the page, which is the bug offset pagination has and this does not.")

	// A bad cursor is a clear error rather than a wrong page.
	bad := srv.RawQuery(t, query, map[string]any{"first": 5, "after": "not-a-cursor"})

	t.Logf("a malformed cursor: %v", bad.Err)

	if !bad.HasError() {
		t.Error("a malformed cursor was accepted")
	}

	// A cursor from another type, which the prefix catches.
	foreign := base64.RawURLEncoding.EncodeToString([]byte("author:author-1"))

	wrongType := srv.RawQuery(t, query, map[string]any{"first": 5, "after": foreign})

	t.Logf("a cursor from another connection: %v", wrongType.Err)

	if !wrongType.HasError() {
		t.Error("a cursor from another connection was accepted, which would return a " +
			"plausible and wrong page")
	}
}

// TestPageSizeIsCapped, because `first: 100000` is a denial of service with no syntax error.
func TestPageSizeIsCapped(t *testing.T) {
	srv := gqltest.New(t, gqltest.Options{Authors: 20, BooksPerAuthor: 20})

	var resp struct {
		Books struct {
			Edges []struct {
				Node struct{ ID string }
			}
		}
	}

	srv.Query(t, `query { books(first: 100000) { edges { node { id } } } }`, &resp)

	t.Logf("asked for 100000, got %d", len(resp.Books.Edges))

	if len(resp.Books.Edges) > 100 {
		t.Errorf("returned %d edges; the cap is not applied", len(resp.Books.Edges))
	}

	// A negative first is an error rather than a silent empty page.
	negative := srv.RawQuery(t, `{ books(first: -1) { edges { node { id } } } }`, nil)

	t.Logf("first: -1 -> %v", negative.Err)

	if !negative.HasError() {
		t.Error("a negative page size was accepted")
	}

	t.Log("the Relay spec says nothing about a maximum page size, so every connection needs one " +
		"and every schema that does not have one has an endpoint that returns the table")
}

// TestEveryListArgumentIsValidated, not only books(first:).
//
// The first version validated `first` on books and nothing else. `authors(limit: -1)` returned every author,
// because the store reads "not positive" as "no limit"; `similar(limit: -1)` panicked in make() and came back
// as "internal system error"; and `similar(limit: 0)` returned one book, because the length check ran after
// the append.
func TestEveryListArgumentIsValidated(t *testing.T) {
	srv := gqltest.New(t, gqltest.Options{Authors: 20, BooksPerAuthor: 5})

	for _, q := range []string{
		`{ authors(limit: -1) { id } }`,
		`{ books(first: 1) { edges { node { similar(limit: -1) { id } } } } }`,
	} {
		resp := srv.RawQuery(t, q, nil)

		t.Logf("%s -> %v", q, resp.Err)

		if !resp.HasError() {
			t.Errorf("%s was accepted", q)
		}
		if strings.Contains(resp.ErrorMessage, "internal system error") {
			t.Errorf("%s panicked rather than being rejected", q)
		}
	}

	var zero struct {
		Books struct {
			Edges []struct {
				Node struct {
					Similar []struct{ ID string }
				}
			}
		}
	}

	srv.Query(t, `{ books(first: 1) { edges { node { similar(limit: 0) { id } } } } }`, &zero)

	if got := len(zero.Books.Edges[0].Node.Similar); got != 0 {
		t.Errorf("similar(limit: 0) returned %d books", got)
	}

	var many struct{ Authors []struct{ ID string } }

	srv.Query(t, `{ authors(limit: 100000) { id } }`, &many)

	if len(many.Authors) > resolvers.MaxAuthors {
		t.Errorf("authors(limit: 100000) returned %d; the cap is %d", len(many.Authors), resolvers.MaxAuthors)
	}
}

// TestANegativeArgumentCannotBuyComplexity checks a bypass that looks real and isn't, so it stays that way.
//
// A list field's cost is its children's cost times the argument, and the first version of the cost functions
// multiplied by a negative number without complaint. That reads like a bypass: a negative field cost would
// subtract from the total and pay for an expensive sibling. An audit reported it as one. This test was written
// to prove it and passed against the unfixed code, because gqlgen discards any custom cost below 1 and its
// saturating add ignores negative operands. It pins that behaviour, so a gqlgen upgrade that changes it fails
// here rather than in production.
func TestANegativeArgumentCannotBuyComplexity(t *testing.T) {
	limited := gqltest.New(t, gqltest.Options{Authors: 50, BooksPerAuthor: 5, ComplexityLimit: 1000})

	resp := limited.RawQuery(t, `{
		discount: books(first: -1000000) { edges { node { id } } }
		authors(limit: 50) { books { similar(limit: 50) { similar(limit: 50) { id } } } }
	}`, nil)

	t.Logf("an expensive query with a negative sibling: %v", resp.Err)

	if !strings.Contains(resp.ErrorMessage, "complexity") {
		t.Errorf("the expensive half ran: a negative argument lowered the query's cost (%v)", resp.Err)
	}
}

// TestComplexityLimiting is the denial-of-service vector GraphQL has and REST does not.
func TestComplexityLimiting(t *testing.T) {
	// A nested query that costs far more than it looks.
	expensive := `{
		authors(limit: 50) {
			books {
				similar(limit: 50) {
					similar(limit: 50) {
						id title
					}
				}
			}
		}
	}`

	unlimited := gqltest.New(t, gqltest.Options{Authors: 50, BooksPerAuthor: 5})

	unlimited.Store.Reset()

	var sink struct {
		Authors []struct {
			Books []struct {
				Similar []struct {
					Similar []struct {
						ID    string
						Title string
					}
				}
			}
		}
	}

	unlimited.Query(t, expensive, &sink)

	t.Logf("with no limit: %d store queries, %d rows",
		unlimited.Store.Queries(), unlimited.Store.Rows())

	// The same query against a server with a complexity limit.
	limited := gqltest.New(t, gqltest.Options{
		Authors: 50, BooksPerAuthor: 5, ComplexityLimit: 1000,
	})

	resp := limited.RawQuery(t, expensive, nil)

	t.Logf("with a complexity limit of 1000: %v", resp.Err)

	if !resp.HasError() {
		t.Fatal("the expensive query was allowed")
	}

	if !strings.Contains(resp.ErrorMessage, "complexity") {
		t.Errorf("the rejection does not mention complexity: %v", resp.Err)
	}

	// A small query still works.
	var small struct {
		Authors []struct {
			ID string
		}
	}

	limited.Query(t, `{ authors(limit: 2) { id } }`, &small)

	if len(small.Authors) != 2 {
		t.Errorf("a cheap query was rejected: %d authors", len(small.Authors))
	}

	t.Logf("the same server served a cheap query: %d authors", len(small.Authors))

	t.Log("the cost is computed from the QUERY before anything runs, which is the only way: a " +
		"limit applied afterwards has already paid for the work. And the cost function is a " +
		"second place to keep in sync with the schema, because nothing in the schema says a " +
		"field is expensive.")
}

// TestIntrospectionIsTheSchema, which is a feature and a disclosure.
func TestIntrospectionIsTheSchema(t *testing.T) {
	srv := gqltest.New(t, gqltest.Options{Authors: 2, BooksPerAuthor: 2})

	var resp struct {
		Schema struct {
			Types []struct {
				Name   string
				Kind   string
				Fields []struct {
					Name string
				}
			}
		} `json:"__schema"`
	}

	srv.Query(t, `{ __schema { types { name kind fields { name } } } }`, &resp)

	byName := map[string][]string{}

	for _, ty := range resp.Schema.Types {
		if strings.HasPrefix(ty.Name, "__") {
			continue
		}

		names := make([]string, 0, len(ty.Fields))
		for _, f := range ty.Fields {
			names = append(names, f.Name)
		}

		byName[ty.Name] = names
	}

	t.Logf("the schema has %d types, of which %d are not introspection types",
		len(resp.Schema.Types), len(byName))

	for _, name := range []string{"Author", "Book", "Query", "Mutation"} {
		if _, ok := byName[name]; !ok {
			t.Errorf("introspection did not return %s", name)
		}
	}

	t.Logf("Author: %v", byName["Author"])
	t.Logf("Mutation: %v", byName["Mutation"])

	t.Log("one query returns the whole API. That is what makes the tooling work and it is also " +
		"a complete disclosure of every field and argument. Turning introspection off does " +
		"not hide the schema, because every field name is in the frontend bundle; it just " +
		"breaks the tools.")
}

// TestVariablesAreTypeChecked, which is the thing REST query strings are not.
func TestVariablesAreTypeChecked(t *testing.T) {
	srv := gqltest.New(t, gqltest.Options{Authors: 3, BooksPerAuthor: 2})

	// A String where an Int is declared.
	resp := srv.RawQuery(t, `query($limit: Int) { authors(limit: $limit) { id } }`,
		map[string]any{"limit": "not a number"})

	t.Logf("a string for an Int variable: %v", resp.Err)

	if !resp.HasError() {
		t.Error("a type mismatch was accepted")
	}

	// A missing required variable.
	missing := srv.RawQuery(t, `query($id: ID!) { author(id: $id) { id } }`, nil)

	t.Logf("a missing required variable: %v", missing.Err)

	if !missing.HasError() {
		t.Error("a missing required variable was accepted")
	}

	// A field that does not exist, caught before anything runs.
	unknown := srv.RawQuery(t, `{ authors(limit: 1) { id nonexistent } }`, nil)

	t.Logf("an unknown field: %v", unknown.Err)

	if !unknown.HasError() {
		t.Error("an unknown field was accepted")
	}

	t.Log("all three are caught by validation, before a resolver runs. That is the argument " +
		"for GraphQL over a REST query string, where ?limit=abc reaches the handler and the " +
		"handler decides what to do about it.")
}

func deref(s *string) string {
	if s == nil {
		return "(none)"
	}
	return *s
}

// TestTotalCountCostsAQueryOnlyWhenAskedFor is the count over every row. A page that does not select totalCount
// must not pay for it.
func TestTotalCountCostsAQueryOnlyWhenAskedFor(t *testing.T) {
	for _, tc := range []struct {
		selection string
		queries   int64
	}{
		{"edges { node { id } }", 1},
		{"edges { node { id } } totalCount", 2},
	} {
		srv := gqltest.New(t, gqltest.Options{Authors: 2, BooksPerAuthor: 3})

		var out map[string]any

		srv.Query(t, `{ books(first: 2) { `+tc.selection+` } }`, &out)

		if got := srv.Store.Queries(); got != tc.queries {
			t.Errorf("selecting %q ran %d store queries, want %d", tc.selection, got, tc.queries)
		}
	}
}
