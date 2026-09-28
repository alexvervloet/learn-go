# URL shortener

The first capstone. It is the learning modules put together: `net/http` with Go 1.22 routing, pgx against a real
Postgres, JWT auth, a Redis read cache, asynq for the work that happens after the redirect, goose migrations,
and a distroless image.

```bash
docker compose up -d

DATABASE_URL="postgres://postgres:postgres@localhost:5435/learn_go_db?sslmode=disable" \
  REDIS_ADDR=localhost:6382 \
  go test ./...
```

With a local Postgres and Redis on their usual ports, `go test ./...` on its own works. Without either, the
tests that need them skip and say how to start them.

108 tests, 5,330 lines of Go.

## The shape

```
cmd/api       the HTTP server
cmd/worker    the job runner and the scheduler

internal/
  config      environment, validated at startup
  shortener   base62, and a reversible scramble so slugs are not sequential
  auth        bcrypt and JWT
  store       every query, with the SQL written out
  cache       cache-aside on Redis, with singleflight
  tasks       the asynq task types and handlers
  api         routing, middleware, handlers
  apitest     the test harness

migrations/   goose, one file
```

## The decisions

### Slugs are the row's id, scrambled

Three schemes get proposed and only one avoids a collision check.

A **random string** needs a uniqueness check, which is a round trip that can fail and has to retry. The code to
handle the retry has to exist, be correct and be tested, and it is the code nobody tests. A **hash of the URL**
needs the same check because a truncated hash collides, and it makes two users share a row when they wanted
separate analytics.

**Encoding the id** needs nothing: the database already guarantees it is unique. The slug is the shortest
possible for the number of URLs that exist, and 62^5 is 916 million of them in five characters.

The cost is that /1, /2, /3 are the first three and anyone can walk the whole service. `Obfuscate` multiplies by
a large coprime before encoding, so consecutive ids give unrelated slugs. That defeats casual enumeration and is
**not security**: two known pairs recover the multiplier.

Two bugs the tests found while writing it:

- `id * Multiplier` overflows int64 near the capacity limit. Shrinking the multiplier until the forward product
  fits is not enough, because the inverse is a different and larger number. `math/bits.Mul64` and `Div64` do the
  multiplication in 128 bits and remove the constraint.
- The recursive extended Euclid returns one coefficient and reconstructs the other by division. I reconstructed
  it backwards, the modular inverse came out wrong, and every slug decoded to a different id. The iterative form
  carries both coefficients and has nothing to reconstruct.

### A redirect writes nothing

`handleRedirect` reads the cache, sends a 302, and enqueues a click. It does not touch Postgres on a cache hit,
does not wait for the click to be recorded, and does not fail when the enqueue fails.

That last one is a test: `TestAFailedEnqueueDoesNotBreakTheRedirect`. A missing click is a wrong statistic; a
failed redirect is a broken link.

**302, not 301.** A permanent redirect is cached by browsers, sometimes forever, so a shortener that issues 301s
cannot change or delete a link. It also means the counter only ever sees the first visit.

### 404 and 410 are different answers

`URLBySlug` deliberately does not filter expired rows in its `WHERE` clause. If it did, "never existed" and
"expired" would be indistinguishable, and they deserve a 404 and a 410. A crawler treats them differently and a
person gets a better message.

### Ownership failures are 404s

Asking for someone else's slug returns 404, not 403. A 403 confirms the slug exists, which is an enumeration
oracle one request at a time. The same applies to delete, where the ownership check is in the `WHERE` clause
rather than a separate `SELECT`: one statement is atomic and the rows-affected count says what happened.

### Keyset pagination, and the tuple

`WHERE (created_at, id) < ($3, $4)` rather than `OFFSET`. OFFSET 10000 reads and discards ten thousand rows, and
a row inserted between two page requests shifts the window so a client sees one twice.

The tuple matters. `created_at` alone is not unique, and ten URLs created in one transaction share a timestamp
to the microsecond because `now()` is fixed for a transaction. `TestTheTupleComparisonHandlesATie` inserts six
rows with an identical `created_at` and pages through them.

### The cache deletes, it does not update

Cache-aside: read through, and on a write **delete** the key. An update has a race a delete does not, because
two writers can reach the cache in the opposite order to the database and leave it holding the older value
permanently. A delete makes the next reader fetch, which cannot be stale.

**Negative entries are cached too**, for a much shorter time. Without them a scanner probing random slugs sends
every probe to Postgres, which is a denial of service that costs the attacker nothing. The asymmetry in TTL
follows the asymmetry in consequence: a wrong "it exists" is a redirect to the wrong place, a wrong "it does not
exist" is a 404 that fixes itself.

Two interactions the tests pin. Creating a slug **deletes** its negative entry, or a slug someone probed stays
404 after it is created. And an entry never outlives its URL's expiry, or the expiry is advisory for up to one
cache TTL.

A `singleflight.Group` collapses concurrent misses. The test asserts a **bound** rather than exactly one call,
because singleflight deduplicates the callers that are in flight, and one arriving just after the first fetch
returns starts a new one.

### Passwords

bcrypt at cost 12, which is about 250ms. The cost lives **inside the hash**, so raising it does not invalidate
anything: old hashes keep verifying at their old cost and the right moment to re-hash is the next login.

Tests use `auth.TestCost`, which is bcrypt's minimum. At cost 12 a suite with thirty registrations spends eight
seconds hashing, and the pressure to "fix" that lands on the production constant.

bcrypt truncates at **72 bytes**, and that is bytes: a password of emoji hits it at 18 characters. x/crypto
returns an error rather than truncating, which is the better behaviour, so the length check is in validation.

Login failures are byte-identical whether the email is unknown or the password is wrong.
`TestLoginFailuresAreIndistinguishable` compares the two bodies. The timing still differs, because the
unknown-email path does no bcrypt work, and the README says so rather than leaving the reader to wonder.

### Tokens

HS256, with `jwt.WithValidMethods` pinning the algorithm. That one option is the whole defence against the
textbook JWT attacks: `alg: none`, and algorithm confusion where an attacker rewrites the header to HS256 and
signs with the RSA public key. `TestAlgorithmConfusionIsRejected` shows the same token being accepted by a
parser without the pin.

A JWT is **signed, not encrypted**. `TestTokenPayloadIsReadableByAnyone` base64-decodes the payload and finds
the email in it.

### The target scheme is an allow-list

A shortener that accepts any string becomes a redirector for `javascript:` URLs that run in whoever follows the
link, `data:` URLs, and internal addresses that turn the service into an SSRF probe.

It has to be an allow-list. A deny-list of "javascript" misses `JaVaScRiPt:`, `vbscript:` and whatever a browser
adds next. `TestTargetSchemeIsAnAllowList` sends eight of them.

### What is not in the middleware stack

`chi/middleware.RealIP`. staticcheck flagged it as deprecated with three advisories: it rewrites
`r.RemoteAddr` from `X-Forwarded-For`, `True-Client-IP` or `X-Real-IP` whether or not the infrastructure sets
them, so a client can set its own address. This service does not use the client address, so the answer is to not
have it rather than to configure it.

### Jobs

Click recording is at-least-once, so a repeat is possible and tolerated: two clicks for one visit is a wrong
number and not a broken system. A payment would need an idempotency key, and saying that is the point.

The payload carries the **URL id, not the slug**. A slug can be deleted and re-created while the task is queued,
and the click would land on the wrong URL.

`asynq.SkipRetry` for the two cases that can never succeed: the row is gone, and the payload does not decode. A
plain error puts them back on the queue to fail three more times.

Two queues with a 6:1 weighting, so a two-minute expiry sweep cannot starve click recording.

## The tests

| Package | What it needs | Tests |
|---|---|---|
| `shortener` | nothing | round trip, injectivity, capacity, reserved slugs |
| `auth` | nothing | salting, cost in the hash, alg=none, algorithm confusion, the 72-byte ceiling |
| `config` | nothing | every problem at once, no default secret, both duration forms |
| `tasks` | nothing | SkipRetry against retry, queue weights, the mux |
| `store` | Postgres | constraints, cascades, concurrent increments, keyset ties |
| `cache` | Postgres + Redis | TTL asymmetry, stampede collapse, invalidation on write |
| `api` | Postgres | the whole surface, through a real HTTP server |

The harness gives each test binary **its own database**, named from `os.Args[0]`, and truncates between tests.
`go test ./...` runs packages concurrently, and two packages truncating one database is a deadlock in one and a
wrong query plan in the other.

`apitest.Do` returns bytes rather than an open `*http.Response`, so no test can leak a connection. That was a
refactor: the first version handed every caller an obligation and the linter found the six that forgot.

## Running it

```bash
docker compose up -d

export DATABASE_URL="postgres://postgres:postgres@localhost:5435/learn_go_db?sslmode=disable"
export REDIS_ADDR=localhost:6382
export JWT_SECRET="a-secret-that-is-at-least-thirty-two-bytes"
export BASE_URL="http://localhost:8080"

go run ./cmd/api &
go run ./cmd/worker &

curl -s -X POST localhost:8080/api/v1/register \
  -H 'content-type: application/json' \
  -d '{"email":"you@example.com","password":"correct horse battery staple"}'
```

The response carries a token. With it:

```bash
TOKEN=...

curl -s -X POST localhost:8080/api/v1/urls \
  -H "authorization: Bearer $TOKEN" -H 'content-type: application/json' \
  -d '{"target":"https://go.dev/doc/effective_go"}'

curl -si localhost:8080/<slug>        # 302
curl -s localhost:8080/api/v1/urls -H "authorization: Bearer $TOKEN"
```

`BASE_URL` is configuration and not derived from the request, because behind a proxy the request's `Host` is
whatever the proxy sends and the scheme is `http` even when the client used `https`.

## The image

Multi-stage, `CGO_ENABLED=0`, `gcr.io/distroless/static-debian12:nonroot`, exec-form `ENTRYPOINT`.

The reasoning for each of those is worked through in
[docker-concepts](../../backends/learning/docker-concepts/), where seven Dockerfiles are built and measured.
The short version: distroless has CA certificates and `/etc/passwd`, which scratch does not and this service
needs; and the shell form of `ENTRYPOINT` makes `sh` PID 1, which does not forward SIGTERM, so every graceful
shutdown in `cmd/api/main.go` would do nothing.
