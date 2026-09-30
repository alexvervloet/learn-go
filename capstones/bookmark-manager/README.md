# Bookmark manager

The second capstone. It covers what the url-shortener deliberately does not: rotating refresh tokens with reuse
detection, a many-to-many relationship, Postgres full-text search, and a rate limiter that is atomic.

```bash
docker compose up -d

DATABASE_URL="postgres://postgres:postgres@localhost:5434/learn_go_db?sslmode=disable" \
  REDIS_ADDR=localhost:6383 \
  go test ./...
```

With a local Postgres and Redis on their usual ports, `go test ./...` works on its own. Without either, the
tests that need them skip and say how to start them.

92 tests, 4,880 lines of Go, 174 lines of SQL.

## The shape

```
cmd/api       the HTTP server

internal/
  config      environment, validated at startup
  auth        bcrypt, access tokens, refresh tokens
  store       every query, with the SQL written out
  ratelimit   a sliding window in Lua
  api         routing, middleware, handlers
  apitest     the test harness

migrations/   goose, applied in order and never edited once run
```

## Two tokens

The url-shortener issues one token and lives with not being able to revoke it. That is a defensible choice for a
service whose sessions are short. This one has real sessions, so:

| | Access token | Refresh token |
|---|---|---|
| Format | JWT, HS256 | 32 random bytes, base64url |
| Verified by | a signature check | a database lookup |
| Lifetime | 15 minutes | 30 days |
| Revocable | no | yes, immediately |
| Stored | nowhere | as a **SHA-256 hash** |

The access token is stateless so no request costs a lookup. The refresh token is a row so a logout takes effect.

Only the hash is stored, so a stolen database is a set of hashes that cannot be replayed. SHA-256 rather than
bcrypt because the token is 32 bytes of CSPRNG output: there is nothing to brute force, and a lookup on every
refresh should not cost 250ms.

### Rotation and reuse detection

Every refresh **consumes** the token and issues a new one, so a refresh token is valid exactly once. A second
use means a copy exists somewhere it should not, because the legitimate client threw its copy away when it got
the replacement.

Theft and a retried request whose response was lost are indistinguishable from the server, so both get the safe
answer: **revoke the whole family**. The honest user logs in again; an attacker gets one request.

`TestReuseRevokesTheWholeFamily` plays it out: register, refresh once, refresh again, then replay the original
token. The replay is refused *and* the legitimate client's current token stops working, because the chain went
with it. `TestRevokingAFamilyDoesNotTouchOtherSessions` shows a second device unaffected, which is why the
family is a column of its own rather than being the user id.

The rotation is one conditional `UPDATE`:

```sql
UPDATE refresh_tokens SET used_at = now()
WHERE token_hash = $1 AND used_at IS NULL AND revoked_at IS NULL AND expires_at > now()
RETURNING id, user_id, family
```

Exactly one of two concurrent refreshes affects a row. A `SELECT` to check and then an `UPDATE` is the same race
with extra steps: both read NULL, both update, and the reuse detection never fires.

### A JWT cannot express a sub-second TTL

`exp` and `iat` are NumericDate, and jwt/v5 truncates every date to `jwt.TimePrecision`, which is **one second**.
A 300ms TTL makes `exp == iat`, so the token is expired the instant it is signed, and nothing reports it:
`SignedString` succeeds, the token looks normal, and every request with it is a 401.

Two versions of one test failed on this before I checked the library. `auth.IssueAccess` now returns an error
below one second, and `TestTheTruncationIsWhatTheLibraryDoes` asserts the mechanism rather than the wrapper.

## Tags, and the ON CONFLICT trap

Saving a bookmark with tags means "find or create" for each one. The obvious query is:

```sql
INSERT INTO tags (user_id, name) SELECT $1, unnest($2::citext[])
ON CONFLICT DO NOTHING RETURNING id, name
```

It returns **only the rows it inserted**. A tag that already existed conflicts, does nothing, and is absent from
`RETURNING`, so a caller that trusts the result silently drops every existing tag. The bookmark ends up with
only its new tags and the bug reads as "tags disappear sometimes".

`EnsureTags` inserts and then selects: two statements in one transaction rather than one clever one, with the
`SELECT` as the source of truth. The alternative, `DO UPDATE SET name = EXCLUDED.name`, forces a write so
`RETURNING` sees the row, at the cost of a pointless update and a bumped `xmin` on every existing tag.

It also **deduplicates before inserting**, because `ON CONFLICT` does not fire for two rows in the *same*
statement. Postgres reports `ON CONFLICT DO UPDATE command cannot affect row a second time` and the whole
statement fails, and since the column is `CITEXT`, a request with `["go", "Go"]` is exactly that case.

## The N+1, measured

`internal/store` has three loaders that return identical results:

| Loader | Queries for 30 bookmarks |
|---|---|
| `ListNPlusOne` | **31** |
| `ListTwoQueries` | **2** |
| `ListOneQuery` | **1** |

The count is the assertion because it is the same on every machine. The durations are a log line.

The N+1 is what an ORM does by default, and it is not stupid: it is readable and for one bookmark it is
correct and fast. The problem is that the cost is a function of the **result size** rather than the request, so
it is invisible in development and linear in production.

The service uses `ListTwoQueries`. One query with `array_agg` is one round trip and makes the row scan depend on
the driver's array handling, does not compose (a second collection needs a second `array_agg` with its own
`FILTER` and the cardinality gets subtle), and is harder to read. The honest summary is that the N+1 is the only
one that is clearly wrong.

The `FILTER (WHERE t.name IS NOT NULL)` on the `array_agg` is not decoration. Without it, a bookmark with no
tags comes back as `{NULL}`, which in Go is `[]string{""}` and shows up as an empty tag chip in a UI.

## Full-text search

The search vector is a **generated column**, not a trigger:

```sql
search_vector TSVECTOR GENERATED ALWAYS AS (
    setweight(to_tsvector('english', coalesce(title, '')), 'A') ||
    setweight(to_tsvector('english', coalesce(description, '')), 'B')
) STORED
```

A trigger is the classic answer and it is code that can be forgotten on an insert path, disabled by a bulk load,
or written against a different text search configuration from the query. A generated column cannot drift.

`setweight` is what makes a title match outrank a description match: `TestSearchRanksTitleAboveDescription`
measures 0.61 against 0.24. And `coalesce` is not optional, because `to_tsvector(NULL)` is NULL and
`NULL || anything` is NULL, so one NULL field makes the whole row unsearchable.

The query uses **`websearch_to_tsquery`**, not `to_tsquery` or `plainto_tsquery`. `to_tsquery` raises a syntax
error on anything that is not tsquery syntax, so a user typing an apostrophe gets a 500. `plainto_tsquery` ANDs
every word and ignores quotes and negation. `websearch_to_tsquery` accepts what a person types into a search box
and never raises: `TestWebsearchSyntaxIsAcceptedWithoutErrors` sends quoted phrases, `or`, a leading `-`, and
`it's & | ! ( )`.

### "Is the index used" has no answer without a row count

The index is GIN on `(user_id, search_vector)`, which needs the `btree_gin` extension, because there is no GIN
operator class for `bigint`.

Whether the planner uses it depends on the data, and three fixtures got that wrong before this one:

| Fixture | Plan | Correct? |
|---|---|---|
| 500 rows, one user | btree on `(user_id, …)`, filter by tsvector | yes, 500 rows is nothing |
| 20 users × 500 | the same | yes, `user_id = $1` still selects 500 |
| 5,000 rows, identical titles | sequential scan | yes, a term with no selectivity |
| **20,000 rows, varied text** | **GIN on `bookmarks_search_idx`** | this is what the index is for |

`TestOnASmallTableTheScanWins` asserts the opposite on 200 rows, because "the plan depends on the row count" is
a claim and claims get checked here. Both tests run `ANALYZE` first: a bulk insert updates no statistics,
autovacuum has not run, and a planner without statistics is guessing.

## Rate limiting

A **sliding window log** in Redis: a sorted set of request timestamps, trimmed to the window, counted.

A fixed window counts per calendar minute and allows twice the limit across a boundary. 100 requests at 11:59:59
and 100 more at 12:00:00 is 200 in one second, all within the rules, and being wrong by 2x at exactly the moment
traffic spikes is the wrong direction to be wrong in.

**The whole check is one Lua script**, because the four operations have to be atomic. As separate commands, two
concurrent requests both trim, both count 99, and both are allowed past a limit of 100. A `MULTI`/`EXEC`
transaction does not help, because the decision depends on the count and a transaction cannot branch.
`TestConcurrentRequestsCannotExceedTheLimit` races 500 goroutines against a limit of 50 and asserts **exactly
50**.

Two details in the script that are easy to get wrong:

- **The member must be unique per request.** A sorted set is a set, so two `ZADD`s with the same member are one
  member, and a member derived from the clock silently merges concurrent requests.
- **The `PEXPIRE` is reset on every call**, so a key for a client that visited once disappears on its own.

`ResetIn` points at the **oldest surviving entry**, not at the end of the window, so `Retry-After` tells a client
the truth rather than over-telling it to wait.

### One bucket, not one per path

The first version keyed on `r.URL.Path`, so `/register` and `/login` each got the limit. That is two limits that
each look right and together allow twice the traffic: both run bcrypt, and an attacker told that login is full
moves to register. There is one `CredentialsKey` now.

### Two limits: one for bcrypt, one per account

The shared bucket protects the CPU, and every user is in it, so it is sized to what bcrypt can afford
(`LOGIN_LIMIT`, 120 a minute: at cost 12 a hash took 225ms here, so a full bucket is about half a core). It
used to be 20 a minute, and at 20 one script logging in on a loop locked every user out of the service.

Guessing one person's password is a different attack and gets a different bucket: `ACCOUNT_LIMIT` attempts per
lower-cased email per `ACCOUNT_WINDOW` (10 per 15 minutes). It refuses the account under attack and nobody
else, which the shared bucket cannot do. `TestOneAccountsLimitDoesNotLockOutAnother` covers it. It only shows
itself on a refusal: the `RateLimit-*` headers on a success describe the shared bucket, because telling a
caller how many attempts remain on an account is telling a guesser their budget.

It **fails closed**: if Redis is unreachable, credential endpoints refuse. On a login endpoint the limiter is
the only thing between a credential-stuffing run and bcrypt, so failing open converts a Redis outage into an
open door. Reads are not limited at all, which is a decision the handler makes rather than the limiter.

## Paging

`GET /api/v1/bookmarks?limit=20` returns a page and, when there is more, a `next` cursor. Pass it back as
`&after=<next>` for the following page; the last page has no `next`. `?tag=` pages the same way.

The cursor is the last row's `(created_at, id)`, not an offset. `OFFSET 1000` reads and throws away a thousand
rows to return twenty, and a bookmark saved while someone pages shifts every later page by one, so an item shows
twice or not at all. A cursor is one seek on the `(user_id, created_at DESC, id DESC)` index, whichever page it
is. The `id` is in it because two bookmarks can share a timestamp, and `TestPagesDoNotSkipRowsWithTheSameTimestamp`
is what a cursor without it gets wrong. To learn whether there is a next page, the API asks for one row more than
the limit, rather than running a `count(*)`.

## Schema decisions

- `categories` and `tags` are unique **per user**, not globally. Two people can both have a "Reading" category,
  and a global `UNIQUE (name)` would be first-come-first-served.
- A bookmark's category must belong to the bookmark's owner, and the database enforces it with a composite
  foreign key, `(user_id, category_id) -> categories(user_id, id)`. The first version only had
  `REFERENCES categories(id)`, which proves the category exists and says nothing about whose it is: any user
  could file bookmarks under anyone's category. Migration 002 fixes it forward, and
  `TestCannotFileIntoAnotherUsersCategory` is the test that was missing. Scoping every read by `user_id` is not
  enough; the write path needs the same check.
- Deleting a category is `ON DELETE SET NULL (category_id)`, so the bookmarks survive. `CASCADE` is the kind of
  mistake discovered by a support ticket. The column list matters on a composite key: without it Postgres would
  null `user_id` too, and that column is `NOT NULL`.
- `bookmark_tags` has a composite primary key and no surrogate id. A join table with its own `BIGSERIAL` and a
  separate unique index is a column and an index nothing reads.
- Ownership failures are **404, not 403**. A 403 confirms the row exists, one request at a time.

## Running it

```bash
docker compose up -d

export DATABASE_URL="postgres://postgres:postgres@localhost:5434/learn_go_db?sslmode=disable"
export REDIS_ADDR=localhost:6383
export JWT_SECRET="a-secret-that-is-at-least-thirty-two-bytes"

# The schema. Only the test harness migrates on its own, so a fresh database needs this once.
# The goose version matches go.mod.
go run github.com/pressly/goose/v3/cmd/goose@v3.28.0 -dir migrations postgres "$DATABASE_URL" up

go run ./cmd/api

curl -s -X POST localhost:8080/api/v1/register \
  -H 'content-type: application/json' \
  -d '{"email":"you@example.com","password":"correct horse battery staple"}'
```

That returns an access token and a refresh token. With the access token:

```bash
curl -s -X POST localhost:8080/api/v1/bookmarks \
  -H "authorization: Bearer $ACCESS" -H 'content-type: application/json' \
  -d '{"url":"https://go.dev/doc/effective_go","title":"Effective Go","tags":["go","style"]}'

curl -s "localhost:8080/api/v1/search?q=effective" -H "authorization: Bearer $ACCESS"
curl -s "localhost:8080/api/v1/bookmarks?tag=go" -H "authorization: Bearer $ACCESS"
curl -s localhost:8080/api/v1/tags -H "authorization: Bearer $ACCESS"
```

And when the access token expires:

```bash
curl -s -X POST localhost:8080/api/v1/refresh \
  -H 'content-type: application/json' -d "{\"refresh_token\":\"$REFRESH\"}"
```

The old refresh token is dead the moment that returns. Sending it again revokes the session.

## The tests

| Package | What it needs | Subject |
|---|---|---|
| `auth` | nothing | salting, alg=none, algorithm confusion, the NumericDate floor |
| `config` | nothing | every problem at once, no default secret, TTLs that contradict |
| `ratelimit` | Redis | exactness under concurrency, the sliding window, key expiry |
| `store` | Postgres | the ON CONFLICT trap, cascades, the N+1, search plans |
| `api` | both | rotation, reuse detection, ownership, the limiter through HTTP |

Each test binary gets **its own database**, named from `os.Args[0]`, and truncates between tests, because
`go test ./...` runs package binaries concurrently.

## The image

Multi-stage, `CGO_ENABLED=0`, `gcr.io/distroless/static-debian12:nonroot`, exec-form `ENTRYPOINT`. 14 MB.

The reasoning is in [docker-concepts](../../backends/learning/docker-concepts/), where seven Dockerfiles are
built and measured.
