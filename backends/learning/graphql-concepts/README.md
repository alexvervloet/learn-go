# graphql-concepts

GraphQL with gqlgen: the N+1 problem it creates by construction, the dataloader that fixes it, the error model
that has no REST equivalent, and the denial-of-service vector that comes with letting clients write queries.
7,840 lines, of which 4,781 are generated.

The Python mirror is `backends/learning/graphql-concepts`, which uses Strawberry. The libraries sit on opposite
sides of the same decision and that difference is the first thing below.

## Running it

```sh
go test ./...
```

No services and no network. The store is in memory with a query counter, which is the point: this module is
about HOW MANY QUERIES ran, and a counter answers that exactly on any machine.

Regenerating after a schema change:

```sh
go run github.com/99designs/gqlgen generate
```

The generated code is committed, for the reason in the repo's [.gitignore](../../../.gitignore).

## Schema-first against code-first

gqlgen is schema-first: you write `graph/schema/*.graphqls`, run a generator, and implement the interfaces it
produces. Strawberry is code-first: you write annotated Python classes and the schema is derived.

The trade is not about taste. Schema-first makes the schema a reviewable artefact a frontend team can read, and
a breaking change is a diff in one file. Code-first has no generator and no stale generated code, and the schema
is whatever the types happen to produce, so a refactor can change the API without anything saying so.

For a public API, schema-first. For an internal one that changes weekly, code-first is less friction.

## What is here

| path | lines | what it is |
| --- | --- | --- |
| [graph/schema/](graph/schema/) | 165 | the schema, with each decision commented |
| [graph/generated/](graph/generated/) | 4,781 | generated, committed |
| [graph/model/](graph/model/) | 171 | hand-written domain types the schema binds to |
| [graph/resolvers/](graph/resolvers/) | 1,598 | the resolvers, in both the naive and the batched shape |
| [dataloader/](dataloader/) | 303 | a dataloader written out rather than imported |
| [store/](store/) | 473 | an in-memory store that counts queries |
| [gqltest/](gqltest/) | 283 | run queries against the schema without HTTP |

Coverage across the hand-written packages: 76.2%.

## The N+1 problem, measured

A resolver resolves ONE field of ONE object. `{ authors { books { title } } }` calls the authors resolver once
and the books resolver once per author, because that is what the execution model says.

| query | naive | with dataloaders |
| --- | --- | --- |
| 20 authors with their books | **21 queries** | **2** |
| 10 authors, their books, each book's author | **41 queries** | **3** |

The second row is the one that matters: each level multiplies. Three levels over lists of 10 is 1 + 10 + 100
without loaders and 3 with them, and the client that wrote the query cannot see the difference.

The row count is identical in both, which is the assertion that stops a "fix" that fetches the whole table once.

**The dataloader's cache is the other half.** Asking for `books` and `bookCount` on the same 10 authors is 20
`Load` calls, one batch and 10 cache hits: two fields asking for the same key is one fetch.

**Priming skips the batch entirely.** A parent resolver that already has the objects can put them in the
loader's cache, and the child field becomes a hit: 5 loads, 0 batches, 0 queries. It is one line in the parent
and nobody writes it.

## And the benchmark that said the opposite

The first version of `BenchmarkNPlusOne` reported the NAIVE resolver as faster than the batched one at every
latency, including 1ms per query. That was not a bug in the benchmark.

GraphQL resolves sibling fields **concurrently**. The 20 books resolvers run in 20 goroutines, so against a store
that serves 20 queries at once, 20 one-millisecond queries take one millisecond in total. The dataloader's 2ms
batching window is then pure added cost.

So an N+1 in GraphQL does not cost the REQUEST its latency. It costs the DATABASE: 21 connections instead of 2,
and 21 queries of work instead of 2. A real service has a connection pool, so the 21st query waits, and the
latency appears:

| store | naive | batched |
| --- | --- | --- |
| in memory | **373µs** | 3,513µs |
| 1ms per query, unbounded | **3.65ms** | 6.28ms |
| 1ms per query, pool of 4 | 8.61ms | **6.21ms** |
| 1ms per query, pool of 1 | 26.6ms | **6.16ms** |

The crossing point is the connection pool. Which is why every assertion in this module is on the query count
and not on a duration, and it is the same rule the rest of this repo follows for the same reason.

**The window is not free either.** A query that batches nothing still pays for the loader being there. Which is
why the window is a millisecond and not ten.

## The error model

GraphQL responses carry `data` AND `errors`, and the HTTP status is 200 whatever happened. A field that fails is
null and the rest of the response is still there.

```
errors: [{"message":"this field always fails","path":["failing"]}]
data:   {"authors":[{"id":"author-1"},{"id":"author-2"}],"failing":null}
```

A client that checks the status code and nothing else accepts that as a success, which is the single most common
GraphQL client bug.

### Null propagation destroys more than the field

A non-null field that errors cannot be null, so the error propagates UP to the nearest nullable parent. With
`authors: [Author!]!` and `books: [Book!]!`, one failing `books` field nulls the ENTIRE response. The test shows
it: `data: null` for a query that asked for two authors.

Whether a failure costs one field or everything is decided by exclamation marks in the schema, and that is not
obvious when writing them.

### Two ways to report a failure, and only one is typed

| | top-level `errors` | a result type |
| --- | --- | --- |
| shape | a message string, a path, an untyped extensions map | in the schema, typed, versioned |
| client handling | match on the text | switch on an enum |
| `data` for that field | null | present |

The rule this module follows: **expected outcomes go in the schema as typed results, and the top-level errors
array is reserved for bugs, timeouts and permission failures.** Mixing them is what makes a GraphQL client's
error handling a pile of string matching.

The tests run the same mutation both ways. The plain version returns `title must not be empty` as a message; the
result type returns two `UserError` values with a field name and a `VALIDATION` code, in a response with no
top-level errors at all, because a failed validation is a documented outcome and not an exception.

## Pagination

Relay connections are keyset pagination with different names: `edges`, `cursor`, `pageInfo`, `hasNextPage`. The
tests show two pages with no overlap, which is the property offset pagination loses.

Three things the Relay spec does not say and every connection needs:

**A maximum page size.** `first: 100000` is a denial of service with no syntax error. This schema caps at 100
and the test asks for 100,000.

**A typed cursor.** `book:book-1-1`, base64-encoded. A cursor from the authors connection handed to the books
connection is a client bug, and decoding it into a plausible book id is the worst outcome. With a prefix it is a
clear error, which the test shows.

**`hasPreviousPage` is weaker than it sounds.** The spec's answer is "a cursor was given", which means "you came
from somewhere", not "there are rows before this one". The stronger version needs a second query.

`totalCount` is in the schema and the store counts it as a query returning every row, because that is what
`count(*)` does. backend-concepts measured the same thing against Postgres: 1,428µs against 18.5µs for an
estimate.

## Complexity limiting

The vector GraphQL has and REST does not: one endpoint whose cost is decided by the client.

```graphql
{ authors(limit: 50) { books { similar(limit: 50) { similar(limit: 50) { id } } } } }
```

Unlimited, that query made **1,301 store queries and read 6,550 rows**. With a complexity limit of 1,000 it is
rejected before anything runs, with `operation has complexity 200000, which exceeds the limit of 1000`, and the
same server still serves a cheap query.

Every list argument goes through one function, `resolvers.Limit`: absent means the default, negative is an error,
and anything above the cap is the cap. The complexity functions use the same caps through `resolvers.Cost`, which
is why `similar(limit: 50)` costs 20 there. The first version validated `books(first:)` by hand and nothing else,
so `authors(limit: -1)` returned the whole table, `similar(limit: -1)` panicked, and `similar(limit: 0)` returned
one book. A negative argument can't lower the query's cost, but not because of anything here: gqlgen discards a
custom cost below 1. `TestANegativeArgumentCannotBuyComplexity` pins that.

The cost is computed from the QUERY, which is the only way: a limit applied afterwards has already paid for the
work. And the cost function is a second place to keep in sync with the schema, because **nothing in the schema
says a field is expensive**.

The same query shapes, benchmarked against one endpoint:

| query | per request |
| --- | --- |
| one field | 33.9µs |
| a list of 50 | 229µs |
| two levels | 2.98ms |
| three levels | 5.32ms |

Same endpoint, same server, **157x** between the cheapest and the most expensive, decided entirely by the
client. That is the capacity-planning difference from REST.

## Validation happens before a resolver runs

A string for an `Int` variable, a missing `ID!`, a field that does not exist: all three are rejected with HTTP
422 and a `GRAPHQL_VALIDATION_FAILED` code, before anything executes. That is the argument for GraphQL over a
REST query string, where `?limit=abc` reaches the handler and the handler decides what to do about it.

Introspection returns the whole API in one query, which is what makes the tooling work and is a complete
disclosure of every field and argument. Turning it off does not hide the schema, because every field name is in
the frontend bundle; it breaks the tools. The defensible position is on in development and behind auth in
production.

## Writing a dataloader rather than importing one

`dataloadgen` and `graph-gophers/dataloader` both do this well and either is the right choice for a service.
This one is written out because the TIMING is the part worth understanding, and it is fifty lines.

The problem: sibling fields resolve concurrently, so the 20 books resolvers call `Load` from 20 goroutines at
roughly the same moment. A loader that fires on the first `Load` batches one key. A loader that waits for a
fixed number batches correctly and hangs when there are fewer.

The answer every implementation uses is a short timer. The first `Load` starts a window, every `Load` inside it
joins the batch, and the batch fires when the window closes or the batch is full.

Three details in the implementation are the ones that bite:

- The batch runs with `context.WithoutCancel`. The timer fires on its own goroutine and the context that started
  the batch may belong to a resolver that has already returned; using it would cancel the batch for every other
  waiter.
- A key the batch function did not return is an ERROR, not a zero value. A resolver that gets a zero `Author`
  renders `{"id":"","name":""}` and nothing says the row was missing.
- `Prime` does not overwrite a cached key. A batch in flight has a future for that key, and replacing it would
  strand every waiter on a future nobody completes.

**And the loaders live in the CONTEXT, per request.** A loader on the resolver struct caches across requests,
which serves stale data and can serve one user's data to another. A missing middleware is an error here rather
than a lazily created per-call loader, because a per-call loader batches one key, looks like it is working, and
leaves the N+1 exactly where it was.

## Schema decisions worth reading

The `.graphqls` files carry these as comments.

**Input types are separate from output types**, and GraphQL enforces it. `Book` has an id and an author object;
`CreateBookInput` has neither.

**Idempotency keys go in the input, not in a header.** GraphQL has one endpoint and one HTTP request can carry
several mutations, so a header applies to all of them or none.

**`omit_slice_element_pointers: true`** in `gqlgen.yml`, so a `[Book!]!` is `[]model.Book` and not a slice of
pointers that can each be nil for something the schema says cannot be null.

**`ID` binds to a string**, because GraphQL's `ID` is "a string, or an int serialised as a string", and a client
sending `"42"` and one sending `42` must both work.

**The models are hand-written and the schema binds to them.** The generated structs are fine for a toy and have
no methods, no validation, and get rewritten by every schema change. The cost is that a field the schema
declares and the struct does not have becomes a resolver, which for `Author.books` is exactly what we want.

## Things worth stealing from here

- `dataloader.Loader`: batching, per-request caching, `Prime`, and a window that is a parameter rather than a
  constant so a test can control it.
- `store.Store`: counts queries AND rows, and `SetMaxConcurrent` models a connection pool, without which a
  benchmark of concurrent resolvers measures the wrong thing.
- `gqltest.Server`: runs queries against the executable schema with no HTTP, with `RawQuery` returning `data` and
  `errors` together, which `client.Post` cannot do.
- The `CreateBookResult` pattern: expected failures in the schema, unexpected ones in the errors array.
