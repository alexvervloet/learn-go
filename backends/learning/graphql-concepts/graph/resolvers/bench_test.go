package resolvers_test

import (
	"testing"
	"time"

	"github.com/alexvervloet/learn-go/backends/learning/graphql-concepts/gqltest"
)

// BenchmarkNPlusOne prices the fix, and the first version of it measured the wrong thing.
//
// # What went wrong and what it taught
//
// The first version had no concurrency limit on the store, and it reported the NAIVE resolver as faster than
// the batched one at every latency, including 1ms per query. That was not a bug in the benchmark.
//
// GraphQL resolves sibling fields CONCURRENTLY. The 20 books resolvers run in 20 goroutines, so against a store
// that will serve 20 queries at once their 20 one-millisecond queries take one millisecond in total. The
// dataloader's 2ms batching window is then pure added cost and the naive version wins.
//
// So an N+1 in GraphQL does not cost the request its latency. It costs the DATABASE: 21 connections instead of
// 2 and 21 queries of work instead of 2. A real service has a connection pool, so the 21st query waits, and the
// latency appears. SetMaxConcurrent models that, and with a pool of 4 the expected result comes back.
//
// Which is why every assertion in this package is on the QUERY COUNT and not on a duration.
func BenchmarkNPlusOne(b *testing.B) {
	const authors = 20

	query := `{ authors(limit: 20) { id name books { id title } } }`

	var sink struct {
		Authors []struct {
			ID    string
			Name  string
			Books []struct {
				ID    string
				Title string
			}
		}
	}

	for _, tc := range []struct {
		name       string
		delay      time.Duration
		concurrent int
	}{
		{"in memory", 0, 0},
		{"1ms per query, unbounded", time.Millisecond, 0},
		{"1ms per query, pool of 4", time.Millisecond, 4},
		{"1ms per query, pool of 1", time.Millisecond, 1},
	} {
		naive := gqltest.New(b, gqltest.Options{
			Authors: authors, BooksPerAuthor: 3, UseLoaders: false,
		})
		naive.Store.Delay = tc.delay
		naive.Store.SetMaxConcurrent(tc.concurrent)

		batched := gqltest.New(b, gqltest.Options{
			Authors: authors, BooksPerAuthor: 3, UseLoaders: true,
			LoaderWait: 2 * time.Millisecond,
		})
		batched.Store.Delay = tc.delay
		batched.Store.SetMaxConcurrent(tc.concurrent)

		b.Run("naive/"+tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				naive.Query(b, query, &sink)
			}
		})

		b.Run("batched/"+tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				batched.Query(b, query, &sink)
			}
		})
	}

	b.Log("21 queries against 2. The ratio only becomes a LATENCY ratio once the database " +
		"cannot serve them all at once, which is always, because a connection pool is finite.")
}

// BenchmarkLoaderWindow measures what the batching window costs when there is nothing to batch, which is the
// dataloader's one real downside.
func BenchmarkLoaderWindow(b *testing.B) {
	query := `{ author(id: "author-1") { id name } book(id: "book-1-1") { id title } }`

	var sink struct {
		Author struct {
			ID   string
			Name string
		}
		Book struct {
			ID    string
			Title string
		}
	}

	for _, wait := range []time.Duration{0, time.Millisecond, 5 * time.Millisecond} {
		srv := gqltest.New(b, gqltest.Options{
			Authors: 5, BooksPerAuthor: 3, UseLoaders: true, LoaderWait: wait,
		})

		b.Run("wait="+wait.String(), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				srv.Query(b, query, &sink)
			}
		})
	}

	b.Log("this query resolves no batchable field, so the window is pure added latency. It is " +
		"why the window is a millisecond and not ten, and why a request that batches nothing " +
		"still pays for the loader being there.")
}

// BenchmarkQueryShapes shows that the cost of a GraphQL request is a property of the QUERY, not of the endpoint.
//
// Which is the thing that makes capacity planning different: with REST, an endpoint has a cost. With GraphQL,
// one endpoint has whatever cost the client asked for.
func BenchmarkQueryShapes(b *testing.B) {
	srv := gqltest.New(b, gqltest.Options{
		Authors: 50, BooksPerAuthor: 5, UseLoaders: true, LoaderWait: time.Millisecond,
	})

	for _, tc := range []struct {
		name  string
		query string
		sink  any
	}{
		{"one field", `{ authors(limit: 1) { id } }`, &struct {
			Authors []struct{ ID string }
		}{}},

		{"a list", `{ authors(limit: 50) { id name } }`, &struct {
			Authors []struct {
				ID   string
				Name string
			}
		}{}},

		{"two levels", `{ authors(limit: 50) { id books { id title } } }`, &struct {
			Authors []struct {
				ID    string
				Books []struct {
					ID    string
					Title string
				}
			}
		}{}},

		{"three levels", `{ authors(limit: 50) { id books { id author { id name } } } }`, &struct {
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
		}{}},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				srv.Query(b, tc.query, tc.sink)
			}
		})
	}

	b.Log("same endpoint, same server, and the cost is decided by the client. That is the " +
		"capacity-planning difference between GraphQL and REST, and it is what complexity " +
		"limiting exists to bound.")
}
