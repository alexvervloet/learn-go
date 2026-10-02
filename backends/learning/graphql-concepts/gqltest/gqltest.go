// Package gqltest runs GraphQL queries against a schema without HTTP.
//
// # Why not an HTTP client
//
// gqlgen's handler package can be wrapped in httptest and driven with JSON, and gqlgen ships client.New for
// exactly that. It works and it puts a JSON encode, an HTTP round trip and a JSON decode between the test and
// the thing being tested, so a failure could be in any of them.
//
// Executing against the executable schema directly tests the resolvers, the dataloaders and the execution order,
// which is what this module is about. A couple of tests go through HTTP to cover the parts that only exist there:
// the status code for a malformed query, and the middleware that installs the dataloaders.
package gqltest

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/99designs/gqlgen/client"
	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/extension"
	"github.com/99designs/gqlgen/graphql/handler/transport"

	"github.com/alexvervloet/learn-go/backends/learning/graphql-concepts/graph/generated"
	"github.com/alexvervloet/learn-go/backends/learning/graphql-concepts/graph/resolvers"
	"github.com/alexvervloet/learn-go/backends/learning/graphql-concepts/store"
)

// Server is a schema plus the store behind it.
type Server struct {
	Client *client.Client
	Store  *store.Store

	// Loaders is the set installed for the NEXT query, so a test can inspect their stats afterwards.
	Loaders *resolvers.Loaders
}

// Options configures a test server.
type Options struct {
	// Authors and BooksPerAuthor size the store.
	Authors        int
	BooksPerAuthor int

	// UseLoaders selects the batched resolver path.
	UseLoaders bool

	// LoaderWait is the batching window. Small in tests and not zero: a zero window fires the batch
	// on a timer with no delay, which races the other resolvers and batches whatever happened to
	// arrive first.
	LoaderWait time.Duration

	// ComplexityLimit rejects queries above a cost. Zero disables it. It also covers deep nesting, since
	// each level multiplies the cost, which is why there is no separate depth limit.
	ComplexityLimit int

	// MaxDepth has no effect: nothing ever read it.
	//
	// Deprecated: use ComplexityLimit, which bounds nesting too. Kept so code that sets it compiles.
	MaxDepth int
}

// New builds a test server.
func New(t testing.TB, opts Options) *Server {
	t.Helper()

	if opts.Authors == 0 {
		opts.Authors = 5
	}
	if opts.BooksPerAuthor == 0 {
		opts.BooksPerAuthor = 3
	}
	if opts.LoaderWait == 0 {
		opts.LoaderWait = time.Millisecond
	}

	s := store.New(opts.Authors, opts.BooksPerAuthor)

	srv := &Server{Store: s}

	resolver := &resolvers.Resolver{Store: s, UseLoaders: opts.UseLoaders}

	cfg := generated.Config{Resolvers: resolver}

	// The complexity of the expensive fields, declared. Nothing in the schema says `similar` costs
	// more than `title`, so the cost function is where that knowledge lives, and it is a second place
	// to keep in sync with the schema.
	cfg.Complexity.Book.Similar = func(childComplexity int, limit *int) int {
		// The same default and cap as the resolver, from the same function. See resolvers.Cost.
		n := resolvers.Cost(limit, 5, resolvers.MaxSimilar)

		// childComplexity times the number of items, which is the whole idea: a list field's cost is
		// its children's cost times how many there will be. Without the multiplication, `first: 1000`
		// costs the same as `first: 1`.
		return childComplexity * n
	}

	cfg.Complexity.Author.Books = func(childComplexity int) int {
		// No argument to multiply by, so this is a guess at the fan-out. A real schema would not
		// allow an unbounded list field at all, and the fact that this one does is the reason the
		// complexity number is a guess.
		return childComplexity * opts.BooksPerAuthor
	}

	cfg.Complexity.Query.Books = func(childComplexity int, first *int, after *string, last *int, before *string) int {
		return childComplexity * resolvers.Cost(first, 10, resolvers.MaxPageSize)
	}

	cfg.Complexity.Query.Authors = func(childComplexity int, limit *int) int {
		return childComplexity * resolvers.Cost(limit, 10, resolvers.MaxAuthors)
	}

	es := generated.NewExecutableSchema(cfg)

	h := handler.New(es)

	h.AddTransport(transport.POST{})

	// Introspection, on. Whether it should be on in production is a real argument: off makes a schema
	// harder to explore and does not hide it, because every field name is in the frontend bundle. Off
	// also breaks every tool. The defensible position is on in development and behind auth in
	// production.
	h.Use(extension.Introspection{})

	if opts.ComplexityLimit > 0 {
		h.Use(extension.FixedComplexityLimit(opts.ComplexityLimit))
	}

	srv.Client = client.New(h, func(bd *client.Request) {
		if !opts.UseLoaders {
			return
		}

		// The loaders go in the context per REQUEST. Building them here, in the per-request hook,
		// is what makes that true; building them once outside would cache across requests.
		srv.Loaders = resolvers.NewLoaders(s, opts.LoaderWait)

		bd.HTTP = bd.HTTP.WithContext(resolvers.WithLoaders(bd.HTTP.Context(), srv.Loaders))
	})

	return srv
}

// Var is a query variable, re-exported so a test does not have to import gqlgen's client package for one
// function.
func Var(name string, value any) client.Option { return client.Var(name, value) }

// Query runs a query and decodes the data into v.
//
// Fails the test on a GraphQL error, which is what a test asserting on a successful query wants. RawQuery is for
// the tests that expect one.
func (s *Server) Query(t testing.TB, query string, v any, opts ...client.Option) {
	t.Helper()

	if err := s.Client.Post(query, v, opts...); err != nil {
		t.Fatalf("query failed: %v\nquery:\n%s", err, query)
	}
}

// RawQuery runs a query and returns the whole response, errors included.
//
// # Why not client.Post
//
// Post returns a Go error when the response carries GraphQL errors, and it does NOT populate the target: the
// caller gets an error and an empty struct. That is right for a test asserting a query succeeded and wrong for
// one asserting on PARTIAL SUCCESS, where the whole point is that `data` and `errors` arrive together.
//
// RawPost returns both. The first version of this helper used Post and every partial-success test failed with
// "unexpected end of JSON input", which is the error you get from decoding a target that was never written.
func (s *Server) RawQuery(t testing.TB, query string, variables map[string]any) Response {
	t.Helper()

	opts := make([]client.Option, 0, len(variables))

	for k, v := range variables {
		opts = append(opts, client.Var(k, v))
	}

	raw, err := s.Client.RawPost(query, opts...)

	resp := Response{}

	if err != nil {
		// A TRANSPORT failure: a malformed query rejected during validation, which gqlgen answers
		// with HTTP 422 and no parseable body. Distinct from a GraphQL error, and the distinction
		// matters: one means the query was never executed and the other means it was.
		resp.Err = err
		resp.ErrorMessage = err.Error()

		return resp
	}

	if raw.Data != nil {
		encoded, err := json.Marshal(raw.Data)
		if err != nil {
			t.Fatalf("re-encoding the data: %v", err)
		}

		resp.Data = encoded
	} else {
		resp.Data = json.RawMessage("null")
	}

	if len(raw.Errors) > 0 && string(raw.Errors) != "null" {
		resp.Errors = raw.Errors
		resp.ErrorMessage = string(raw.Errors)
		resp.Err = fmt.Errorf("graphql: %s", raw.Errors)
	}

	return resp
}

// Response is a raw GraphQL response.
type Response struct {
	// Data is the `data` field, which is present even when `errors` is.
	Data json.RawMessage

	// Errors is the `errors` array, or nil.
	Errors json.RawMessage

	// Err is non-nil when the response carried errors OR the transport failed.
	Err error

	ErrorMessage string
}

// HasError reports whether the response carried errors.
func (r Response) HasError() bool { return r.Err != nil }

// String renders it for a failure message.
func (r Response) String() string {
	if r.Err != nil {
		return fmt.Sprintf("errors: %s\ndata: %s", r.ErrorMessage, r.Data)
	}

	return fmt.Sprintf("data: %s", r.Data)
}

// Context returns a context with a deadline.
func Context(t testing.TB, d time.Duration) context.Context {
	t.Helper()

	if d <= 0 {
		d = 10 * time.Second
	}

	ctx, cancel := context.WithTimeout(context.Background(), d)
	t.Cleanup(cancel)

	return ctx
}

// WithoutLoaders returns a server whose client does NOT install dataloaders, for the test that shows what a
// forgotten middleware does.
//
// A separate constructor rather than an Options field, because "build a server that is wired wrongly" is a
// testing concern and putting it in the normal configuration invites someone to set it.
func WithoutLoaders(t testing.TB, base *Server) *Server {
	t.Helper()

	resolver := &resolvers.Resolver{Store: base.Store, UseLoaders: true}

	es := generated.NewExecutableSchema(generated.Config{Resolvers: resolver})

	h := handler.New(es)
	h.AddTransport(transport.POST{})
	h.Use(extension.Introspection{})

	return &Server{
		Client: client.New(h),
		Store:  base.Store,
	}
}
