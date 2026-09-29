# backend-concepts

Ten packages, 14,433 lines, and every number in this file was measured on the machine that wrote it.

The Python mirror is `backends/learning/backend-concepts`, which covers the same ground with FastAPI, SlowAPI,
Redis and confluent-kafka. Two of its sub-modules have no Go equivalent and one Go package covers two of its
folders, which is set out under [what did not translate](#what-did-not-translate).

## Running it

```sh
docker compose up -d
DATABASE_URL="postgres://postgres:postgres@localhost:5436/learn_go_db?sslmode=disable" \
  REDIS_ADDR=localhost:6380 \
  KAFKA_BROKERS=localhost:19092 \
  go test ./...
```

Or against services you already run, which needs no environment at all if they are on their default ports:

```sh
go test ./...
```

Every test that needs a database, a Redis or a broker **skips** when it cannot reach one. It does not fail.

The whole suite is 6.0 seconds wall clock with all three running and 5.0 with none, because `go test` runs the
ten packages concurrently and the slowest ones (`websockets` at 4.2s and `ratelimit` at 4.0s) need no services at
all. Serially it is about 24 seconds.

## The packages

| package | lines | coverage | what it is for |
| --- | --- | --- | --- |
| [pagination/](pagination/) | 1,072 | 79.7% | offset against keyset, and the rows offset skips |
| [ratelimit/](ratelimit/) | 2,113 | 93.3% | four algorithms, `x/time/rate`, and one shared Redis limiter |
| [caching/](caching/) | 1,045 | 91.4% | cache-aside, stampedes, jitter, negative caching |
| [webhooks/](webhooks/) | 1,064 | 95.3% | HMAC verification, replay windows, idempotency |
| [jwtauth/](jwtauth/) | 1,525 | 93.6% | the three JWT forgeries, RBAC, key rotation |
| [oauth2flow/](oauth2flow/) | 1,088 | 93.8% | authorization code with PKCE, and the attacks it stops |
| [observability/](observability/) | 2,501 | 95.4% | slog, Prometheus, W3C trace context by hand |
| [websockets/](websockets/) | 1,269 | 93.4% | single-writer, ping, slow-client policies |
| [messaging/](messaging/) | 1,185 | 79.2% | Kafka, and the delivery guarantee nobody has |
| [apitesting/](apitesting/) | 1,030 | 89.6% | testing a service and what it sends |
| [internal/](internal/) | 541 | n/a | the Postgres, Redis and Kafka harnesses |

## The rule this module follows

**Assert the machine-independent quantity. Log the timing next to it.**

The same rule as database-concepts, arrived at the same way. A test asserting "this is 40ms faster" is a fact
about one laptop. A test asserting "this reads 20 rows rather than 45,020", "this makes 1 query rather than 500",
or "this returns 10 rows when you asked for 50" is a fact about the code.

## What the measurements said

An M2 Max, Postgres 17, Redis 7, Redpanda, over a unix socket or loopback.

### Offset pagination reads everything it skips

| offset | rows read to return 20 | time |
| --- | --- | --- |
| 0 | 20 | 39µs |
| 100 | 120 | 44µs |
| 5,000 | 5,020 | 241µs |
| 45,000 | 45,020 | **1,915µs** |

Keyset at the same depths: 20 rows read every time, 30 to 49µs. At page 2,251 that is **42.6x** faster and
**2,251x** fewer rows read.

**And the bug that matters more than the speed.** Between page 1 and page 2, one insert makes offset repeat a
row and one delete makes it skip one. The test shows both: with rows 0 to 9 and pages of 5, after an insert
`row 5` appears on both pages, and after a delete `row 5` appears on neither. Keyset returns 10 distinct rows
across the same two pages, because a cursor names a position in the data rather than in a result set.

**A non-unique pagination key brings the bug straight back.** Ten rows sharing one timestamp, cursoring on
`created_at` alone: page 1 returns 4 rows, page 2 returns 0, and six rows are unreachable while the API looks
like it reached the end. `WHERE created_at < $1 AND id < $2` has the same problem, because it excludes the
boundary rows; the row comparison `(created_at, id) < ($1, $2)` is the correct form and can use a composite
index.

**Counting is the expensive part.** `count(*)` over 50,000 rows reads all of them, 1,428µs. `reltuples` from
`pg_class` is one row, 18.5µs, **77x**, and after `ANALYZE` it was exactly right. That is why "about 50,000
results" is common on large sites and "page 7 of 2,500" is not.

### Rate limiting

| algorithm | per Allow | allocations | 20 goroutines, one key |
| --- | --- | --- | --- |
| sliding log | 63.6ns | 0 | |
| fixed window | 69.6ns | 0 | 197ns |
| sliding counter | 74.4ns | 0 | 207ns |
| token bucket (`x/time/rate`) | 132.5ns | 0 | 388ns |
| Redis sliding counter | **32,060ns** | 31 | |

The sliding log is the FASTEST, which is the opposite of the usual advice. Its cost is memory and only memory:
one timestamp per request inside the window, per key, so 100,000 keys at a limit of 1,000 is 100 million
timestamps. For a low limit on a hot endpoint, a login form at 10 per minute, it is both exactly correct and the
cheapest thing available.

The Redis limiter is **461x** the in-memory fixed window. A PING alone is 20.2µs of that, so the Lua script
costs about 12µs. That 461x is what someone is implicitly choosing when they leave a per-process limiter in a
service that scales out, and the test measures what that choice costs: **five replicas with a limit of 10
enforce a limit of 50**, and the same five sharing one Redis enforce 10.

**A fixed window allows double at the boundary.** 10 per minute, 20 requests at 11:59:59 and 12:00:00: 20
allowed, in two seconds. The sliding counter allows 10. The sliding log allows 10 by construction, which is what
makes it the correctness baseline.

The script is Lua and not `GET` then `SET`, because that read-then-write is the lost update from
database-concepts at a different layer: 200 concurrent requests against a limit of 50 allowed exactly 50, and a
non-atomic version would allow more.

### Caching

**500 concurrent readers of one expired key made 1 query.** `singleflight.Group` collapsed them, and all 500
were served in 61ms, which is about one 50ms source call. The same 500 without it made exactly **500** queries
and 25 seconds of source load.

**Jitter turns a spike into a trickle.** 50 keys written milliseconds apart, 10-minute TTL: without jitter their
expiry times span 1ms, so they all expire in the same second forever. With 10% jitter they span 1m59s.

**Negative caching turned 100 source calls into 20.** 100 requests for 20 missing ids, with a 30-second
negative TTL. Without it, every request for a missing key reaches the source, which is how an id scan becomes a
database problem. The negative TTL is shorter than the positive one on purpose: a row about to be created must
not be remembered as absent for the full cache lifetime.

With Redis unreachable every request still succeeded and the error counter recorded 6 failures. A cache outage
should be a latency problem, not an availability one, and the error count is what tells you which.

### JWTs

| | mint | verify |
| --- | --- | --- |
| HS256 | 2.94µs | 4.12µs |
| RS256-2048 | 890µs | 31.95µs |
| RS256-4096 | 5.54ms | 148.67µs |

RSA signing is **303x** HMAC signing at 2048 bits and verification is only 7.8x, because signing exponentiates
by a 2048-bit private exponent and verification uses the public exponent 65537. So the cost falls on whoever
mints, which is one service at login.

**And the claim that did not survive.** A Redis session lookup is 20.2µs, measured in `ratelimit`. HS256
verification at 4.1µs saves about 16µs per request. RS256-2048 verification at 32µs costs MORE than the lookup
it replaces. The reason to choose RS256 is that many services can verify without any of them being able to
mint. That is an authority argument, not a performance one.

HS256 **verify** is slower than HS256 **mint**, because the HMAC is not the expensive part: verification also
parses three base64 segments, unmarshals the claims and validates exp, nbf, iss and aud.

**All three forgeries are attempted and all three are rejected.** `alg=none` with the signature stripped,
algorithm confusion (an HS256 token signed with the RSA public key against an RS256 verifier), and a `kid` of
`../../../etc/passwd`. `WithValidMethods` closes the first two by construction, and treating `kid` as nothing
but a map key closes the third.

**A token with no `exp` claim is valid forever by default.** jwt/v5 validates expiry when it is present and does
not require it. `WithExpirationRequired` turns that into a rejection and it is not the default.

### OAuth 2.1

The attacks are carried out rather than described. A stolen authorization code exchanged with the attacker's own
verifier gets `invalid_grant: code_verifier does not match`; with no verifier, `invalid_request`. Without PKCE
the same exchange succeeds, because the only other thing required is the public `client_id`.

A callback carrying a valid code and an unknown `state` is rejected **before** the exchange: the token endpoint
saw zero requests. An implementation that exchanges first has already spent the code, and its CSRF protection is
decoration.

**A state the app really did issue is the harder case, and the first version got it wrong.** The attacker starts a
login of their own, which gets them a genuine state, authorizes as themselves, and sends that callback URL to the
victim. A state checked only against a server-side store is valid, so the victim's browser finishes the attacker's
login and ends up in the attacker's account. State has to be bound to the browser that started the flow: `Start`
sets it in a `__Host-` cookie with `SameSite=Lax`, and `Callback` requires the cookie and the query parameter to
match before it touches the store. `Lax` and not `Strict`, because the provider's redirect back is a cross-site
navigation and `Strict` withholds the cookie from exactly that request. `TestStateIsBoundToTheBrowser` carries
out the attack.

The verifier is 43 base64url characters, which is 32 bytes of entropy and the RFC's minimum. The challenge is
SHA-256 of the **ASCII** of the verifier, not of its decoded bytes, and getting that wrong fails at the token
endpoint with only `invalid_grant` to go on.

### Observability

| | cost |
| --- | --- |
| slog JSON handler, 3 attributes | 535ns, 0 allocations |
| with the context handler | 816ns, 1 allocation |
| a DROPPED debug line | **13.4ns**, 0 allocations |
| the same line kept | 842ns |

**63x** for filtering in `Enabled` rather than in `Handle`, because slog calls `Enabled` before evaluating the
arguments. The redaction wrapper is free: 428ns against 440ns for a plain string.

**Cardinality, measured.** 1,000 requests to one endpoint with 1,000 distinct ids: **28** time series labelled
by route, **27,001** labelled by raw path, **964x**. And the cost is not only Prometheus's memory:
`Registry.Gather` takes 7.6µs at one label value and **7.36ms** at 10,000, so the service pays for its own
cardinality on every scrape.

A histogram with 12 buckets is **15** stored series per label combination, not one, because `_bucket` per
boundary plus the implicit `+Inf` plus `_sum` and `_count`. Bucket count multiplies cardinality rather than
adding to it, and the boundaries cannot be changed later without resetting the series.

The full middleware stack costs 1,287ns per request against 5.4ns for a bare handler, of which tracing is 995ns
and metrics 290ns.

W3C trace context is implemented by hand, because the whole wire format is 55 characters and knowing that is
what makes OpenTelemetry's configuration make sense. The parser accepts an unknown version and extra fields,
which is how the format was designed to grow, and rejects an all-zero trace id, which parses as hex and is
explicitly invalid.

### WebSockets

Three of the four failures are demonstrated and the fourth is measured three ways.

A silent client is disconnected after the read deadline, 300ms in the test, which is Slowloris at a different
layer. A vanished peer is noticed within one ping interval plus one write timeout rather than by TCP's
retransmit timeout, which can be fifteen minutes. 50 goroutines sending 268 messages through one connection
produced no corrupted frames, which a direct `Write` could not do: coder/websocket panics on the second
concurrent writer.

The slow-client policies, with a buffer of 4 and 20 messages: drop-newest keeps messages 0 to 3, drop-oldest
keeps 16 to 19, disconnect closes with `StatusPolicyViolation`. There is no right default, and shipping whichever
the library chose is how a client ends up with a stream that has holes it cannot see.

**And the finding that cost a failing test.** A WebSocket client only answers pings while it is READING. The
pong is sent from inside `Read`, so "connect, send, sleep, send" is a client that gets disconnected, and the
server log says the peer stopped responding to pings.

### Kafka

**Ordering is per key, not per topic.** Three keys, ten messages each, written interleaved to a four-partition
topic: each key landed on one partition and arrived in order. The same key written through kafka-go's DEFAULT
balancer was spread over four partitions and its ordering was gone, because the default is round robin and the
Hash balancer is what makes a key mean anything.

**At-least-once redelivers and at-most-once loses.** Commit-after: a consumer that dies having processed three
messages and committed two sees the third again. Commit-before: a consumer that dies after committing and before
processing leaves message 2 processed by nothing, with no error, no lag and no dead letter. It is simply gone.

Three deliveries of one event, deduplicated on a business id from a header, produced one charge and two skips.
The id comes from the message, not from the offset: three deliveries have three offsets and one event id.

**Four consumers on two partitions: two did all the work and two did nothing.** Partition count is the scaling
decision and it is made when the topic is created.

`kafka.Writer`'s zero value for `RequiredAcks` is `RequireNone`, so a Writer built from an empty config returns
success before the broker has the message. That is the most dangerous default in the library and it is what you
get by not deciding.

### Testing an API

`httptest.NewRequest` builds a SERVER request, with `RemoteAddr` set and a relative URL. `http.NewRequest`
builds a client request with an empty `RemoteAddr`, so a handler that keys a rate limit on it gets one bucket
for every request in the test and the test passes for the wrong reason.

`json.Marshal` escapes `<`, `>` and `&` unconditionally, with no option to turn it off. The first golden file
read `"created_at": "<normalised>"`, which is correct JSON and unreadable in a diff, which defeats the
only reason to write a golden file. `json.Encoder` with `SetEscapeHTML(false)` is the only way.

## Test isolation, three ways

Each harness in `internal/` makes the same decision as database-concepts' `dbtest` and isolates differently,
because each service offers a different unit:

- **Postgres**: a database per test binary, named from `os.Args[0]`, which ends in `<pkg>.test`.
- **Redis**: a database NUMBER per test binary, hashed from the same name, from 1 to 15. Never 0, because a
  developer poking at Redis by hand is on 0 and a suite that flushes it deletes what they were looking at.
- **Kafka**: a topic per TEST, named after it plus a random suffix, and a consumer group per test RUN. A group id
  remembers its offsets in Kafka, so reusing one means the second run starts where the first left off and reads
  nothing, which looks like a broken producer.

The two hundred or so lines of `pgtest` duplicate `dbtest` deliberately. Importing it would mean this module depends on that
one, which works inside the workspace and breaks the moment someone clones one directory: a module path under
github.com needs a tag, and this repo does not tag sub-modules.

## What did not translate

**`kafka-demo` and `celery-concepts` became one package.** `messaging` covers the Kafka half. The task-queue
half belongs with asynq in the `jobs-concepts` module, because Go's answer to Celery is a job queue library and
not a Kafka consumer, and putting both here would blur the distinction the Python repo makes clearly.

**`api-testing` shrank.** Most of what the Python version covers (fixtures, parametrised tests, dependency
overrides) is in `testing-concepts` and `http-tutorial` here. What was left is the layer above: testing a whole
service, testing what it SENDS through a fake `RoundTripper`, and writing assertions that survive the code
changing. That is `apitesting`.

**`observability` grew.** The Python version is structured logging, metrics and tracing with three libraries.
Here it is slog and Prometheus with the trace context written out by hand, which is more code and less
dependency, and it makes the mechanism visible rather than configured.

## Things worth stealing from here

- `pagination.Cursor` and its codec: both columns of the sort key, nanosecond precision, base64url, and an
  `ErrBadCursor` sentinel so a handler can return 400 rather than 500.
- `ratelimit.Decision`: carries `ResetIn` as a DURATION, so `WriteHeaders` never reads a clock. The version with
  `ResetAt time.Time` produced `RateLimit-Reset: -41721007` under an injected clock.
- `ratelimit.FailOpen`: makes "what happens when Redis is down" an explicit per-endpoint decision with a
  mandatory error callback, because a limiter that fails silently is indistinguishable from one that works.
- `caching.Cache`: cache-aside with singleflight, jittered TTLs, a negative marker that cannot be confused with
  an empty value, and a decode failure that deletes and reloads rather than failing forever.
- `webhooks.Verifier`: verifies the RAW body before parsing, checks the timestamp in both directions, accepts a
  list of secrets for rotation, and returns the body even on failure so a handler can log what it rejected.
- `jwtauth.Issuer.Rotate`: takes both keys, because for RS256 neither can be derived from the other, and an API
  that took one signed tokens with the wrong key.
- `observability.Redacted[T]`: implements LogValuer, Stringer, GoStringer and Marshaler, because implementing
  three of four leaks on one verb.
- `observability.CaptureRoute`: publishes the matched route through a mutable holder in the context, which is the
  only way to get `r.Pattern` out to a middleware that wraps the mux.
- `websockets.Conn`: one writer goroutine, a deadline on every read, a ping with a pong deadline, and a bounded
  send buffer with a chosen overflow policy.
- `messaging.Consumer`: `FetchMessage` and `CommitMessages` kept separate so the delivery guarantee is a
  parameter, and the commit uses `context.Background()` so a graceful shutdown does not lose the offset.
- `apitesting.Shape`: asserts the fields and types it names and nothing else, reports every mismatch rather than
  the first, and survives a field being added while catching one being removed.
