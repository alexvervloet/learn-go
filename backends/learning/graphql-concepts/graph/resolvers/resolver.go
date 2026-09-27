// Package resolvers implements the schema.
//
// # What a resolver is, and why that produces the N+1
//
// A resolver resolves ONE field of ONE object. gqlgen generates an interface per type with a method per field
// it cannot read straight off the model struct, and the execution engine calls them: the parent first, then
// each child's fields against each parent instance.
//
// So `{ authors { books { title } } }` calls Authors once and Books once PER AUTHOR. That is not a bug in the
// resolver, it is the execution model, and it is why every GraphQL server needs dataloaders and no REST server
// does.
//
// # Where the dataloaders live
//
// In the CONTEXT, per request, put there by middleware. Not on the Resolver struct, and the distinction is the
// whole of dataloader correctness: a loader caches, and a loader on the Resolver struct outlives the request, so
// it serves stale data and can serve one user's data to another.
//
// The cost of context storage is that every resolver has to fetch them and handle their absence, which is why
// For() below returns an error rather than panicking.
package resolvers

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/alexvervloet/learn-go/backends/learning/graphql-concepts/dataloader"
	"github.com/alexvervloet/learn-go/backends/learning/graphql-concepts/store"
)

// Resolver is the dependency container gqlgen hands to every resolver.
type Resolver struct {
	Store *store.Store

	// UseLoaders selects the batched or the naive path, so the tests can measure both against the
	// same store. A real service would not have this: it would use loaders always.
	UseLoaders bool
}

// Loaders holds one loader per batchable relationship, for one request.
type Loaders struct {
	AuthorByID    *dataloader.Loader[string, store.Author]
	BooksByAuthor *dataloader.Loader[string, []store.Book]
}

// NewLoaders builds a set for one request.
//
// wait is the batching window. 1ms is the usual figure: invisible next to a database round trip, and a real
// added latency on a request with nothing to batch. Making it a parameter rather than a constant is what lets a
// test drive it to zero for a deterministic single-key case.
func NewLoaders(s *store.Store, wait time.Duration) *Loaders {
	return &Loaders{
		AuthorByID: dataloader.New(
			func(ctx context.Context, ids []string) (map[string]store.Author, error) {
				return s.AuthorsByIDs(ctx, ids)
			}, wait, 1000),

		BooksByAuthor: dataloader.New(
			func(ctx context.Context, ids []string) (map[string][]store.Book, error) {
				return s.BooksByAuthors(ctx, ids)
			}, wait, 1000),
	}
}

type loadersKey struct{}

// WithLoaders puts a set of loaders in a context.
func WithLoaders(ctx context.Context, l *Loaders) context.Context {
	return context.WithValue(ctx, loadersKey{}, l)
}

// ErrNoLoaders is returned when the middleware did not run.
//
// An error rather than a panic, and rather than lazily creating a set. A lazily created per-CALL loader batches
// one key and looks like it is working, which is the worst of the three: the N+1 is still there and the code
// says dataloader.
var ErrNoLoaders = errors.New("no dataloaders in the context; the middleware did not run")

// For reads the loaders out of a context.
func For(ctx context.Context) (*Loaders, error) {
	l, ok := ctx.Value(loadersKey{}).(*Loaders)
	if !ok || l == nil {
		return nil, ErrNoLoaders
	}

	return l, nil
}

// Errors a resolver returns.
var (
	ErrNotFound = errors.New("not found")
)

// wrap turns a store error into something the schema can carry.
func wrap(op string, err error) error {
	if errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("%s: %w", op, ErrNotFound)
	}
	return fmt.Errorf("%s: %w", op, err)
}
