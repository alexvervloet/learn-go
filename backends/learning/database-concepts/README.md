# database-concepts

Postgres from Go, with pgx. Eleven packages, 9,068 lines, and every number in this file was measured on
the machine that wrote it rather than looked up.

The Python mirror of this module is `backends/learning/database-concepts` in the Python repo, which covers
the same ground with SQLAlchemy and psycopg. Two of its sub-modules have no Go equivalent and one Go
package has no Python equivalent, which is explained under [what did not translate](#what-did-not-translate).

## Running it

```sh
docker compose up -d
DATABASE_URL="postgres://postgres:postgres@localhost:5433/learn_go_db?sslmode=disable" go test ./...
```

Or against a local Postgres, which needs no `DATABASE_URL` at all:

```sh
createdb learn_go_db
go test ./...
```

With no database reachable, every test **skips**. It does not fail. A red suite for a missing local
Postgres trains people to ignore red, and then a real failure goes unnoticed.

The whole suite is 7 seconds wall clock, because `go test` runs the eleven packages concurrently and each
has its own database. Serially it is about 28. `transactions` is the slow one at 9s, almost all of it
waiting for Postgres's 1-second `deadlock_timeout` to fire in `TestDeadlockAndItsFix`.

## The packages

| package | lines | coverage | what it is for |
| --- | --- | --- | --- |
| [dbtest/](dbtest/) | 654 | 79.9% | the harness: skip on no database, a transaction per test, a database per package |
| [seed/](seed/) | 329 | 91.0% | a deterministic 16,200-row dataset via `CopyFrom` |
| [migrations/](migrations/) | 226 | n/a | three goose migrations: schema, indexes, pgvector |
| [normalization/](normalization/) | 645 | 87.2% | 1NF to BCNF, with each anomaly triggered rather than described |
| [indexes/](indexes/) | 756 | 92.8% | reading `EXPLAIN (ANALYZE, FORMAT JSON)`, and the four ways to lose an index |
| [nplusone/](nplusone/) | 1,062 | 81.4% | N+1 counted in round trips, plus `pgx.Batch` and `LATERAL` |
| [transactions/](transactions/) | 1,653 | 81.9% | the isolation anomalies, locking, savepoints, and the retry loop |
| [windowfuncs/](windowfuncs/) | 832 | 81.7% | window functions against the equivalent Go loop |
| [fulltext/](fulltext/) | 676 | 90.5% | `tsvector`, the four query parsers, trigrams |
| [vectors/](vectors/) | 928 | 94.6% | pgvector, and measuring the recall an approximate index costs |
| [pgxdemo/](pgxdemo/) | 740 | 77.8% | pool sizing, exhaustion, leaks, the five query modes |
| [migrate/](migrate/) | 567 | 80.6% | goose, transactional DDL, and `CREATE INDEX CONCURRENTLY` |

## The rule the whole module follows

**Assert the machine-independent quantity. Log the timing next to it.**

A test asserting "this query takes under 5ms" passes on a fast laptop with a warm cache and fails on a
loaded CI runner, gets marked flaky, and is deleted. A test asserting "this query uses an Index Scan and
not a Seq Scan", or "this returns the same data in 2 round trips rather than 51", is a fact about the code
that holds everywhere.

So the assertions here are on plan node types, round-trip counts, rows transferred, index sizes and
recall. The timings are logged, because they are what makes the lesson concrete, and asserted only where
the ratio is enormous.

Arriving at that rule cost two wrong conclusions in opposite directions, both recorded in the repo's
`LESSONS.md`.

## What the measurements said

Every row below is from a test or benchmark in this module, on an M2 Max against Postgres 17 over a unix
socket. The socket matters: a local round trip is 15µs and a managed database in the same region is 1 to
2ms, so anything justified by network cost looks wrong here and anything justified by CPU looks right.

### The N+1 problem is almost free locally, and that is the trap

| strategy | round trips | statements | time | allocations |
| --- | --- | --- | --- | --- |
| `Naive` (a query per author) | 51 | 51 | 2.43ms | 7,999 |
| `Join` (one query) | 1 | 1 | 2.97ms | 27,084 |
| `TwoQueries` (`= ANY`) | 2 | 2 | 1.65ms | 10,243 |
| `Batched` (`pgx.Batch`) | 2 | 51 | 1.41ms | 8,469 |

The single JOIN is **slower than the N+1**. Its 27,084 allocations, from repeating each author's columns
once per book, cost more than 49 local round trips do.

The arithmetic that makes it real: one round trip is 15.1µs, so 49 extra trips are 740µs. Subtract that
from `Naive` and you get 1.69ms, which is `TwoQueries` to within noise. At a 1ms network round trip
instead of 15µs, `Naive` costs 52ms and `TwoQueries` costs 3.7ms, a factor of 14. The round-trip count is
the number that travels; the millisecond figure is not.

Pipelining measured on its own: 50 trivial statements in one `pgx.Batch` take 98.5µs, and the same 50 as
separate queries take 785µs. **8x**, and 1.97µs per statement against 15.7µs.

### "Do it in the database" did not survive measurement

A running total, `PARTITION BY` with `lag` and `lead`, against the same thing computed in a Go loop:

| customers | window function | Go loop |
| --- | --- | --- |
| 10 | 116µs | 79µs |
| 100 | 519µs | 252µs |
| 1000 | 4.16ms | 2.66ms |

The Go loop wins at every size. So I built the case the window function was supposed to win, where it
filters and fewer rows cross the wire: top 3 orders per customer, 2,830 rows instead of 5,000. Still
slower, 3.39ms against 2.35ms. What it does win is memory, 932 KB against 1.92 MB.

The received wisdom is a claim about a **remote** database, where rows on the wire set the latency. It is
not a claim about CPU, and it does not transfer to a unix socket.

### Indexes

`idx_books_author` against no index, counting 54 books by one author out of 10,000: **13x**, 0.32ms to
0.02ms. A foreign key does not create an index on the referencing side, and that is the most common
missing index in any schema.

The four ways to lose an index, each confirmed by reading the plan:

| what the query does | plan |
| --- | --- |
| `WHERE author_id = 42` | Bitmap Index Scan via `idx_books_author` |
| `WHERE abs(author_id) = 42` | Seq Scan |
| `WHERE title LIKE 'The Quick%'` with `text_pattern_ops` | Bitmap Index Scan |
| `WHERE title LIKE '%Quick%'` | Seq Scan |

`text_pattern_ops` is the obscure one: a plain B-tree on a `text` column cannot serve `LIKE` at all unless
the database collation is `C`, because the default collation's ordering is not the one `LIKE` needs.

**A Seq Scan is often correct.** On a 50-row table Postgres ignores the primary key index, because reading
one page beats walking a B-tree. Forcing an index there makes things slower.

**An index built during a bulk load is 43% slack.** The partial index `WHERE status = 'pending'` came out
at 57,344 bytes for 680 entries and 32,768 after `REINDEX`. `CREATE INDEX` sorts and packs leaf pages to
about 90%; an index that already exists during a load grows one row at a time and splits pages as it goes.
Drop the indexes before a bulk load, create them after.

**Cost units are not milliseconds.** Forcing the GIN index for a full-text count, the planner's estimate
says 534 against the scan's 329, a 1.6x penalty. The clock says 0.79ms against 0.81ms, a tie. The cost
model's constants default to spinning-disk assumptions, `random_page_cost` at 4.0 where an SSD is nearer
1.1, which is why the compose file sets it.

### The strangest thing here: a cached plan made the same query 6x slower

`BenchmarkFanOut/Join/1` reports 93µs alone and 552µs when it runs after `BenchmarkStrategies`. Same
process, same data, same code. Not the garbage collector: `GOGC=800` changed nothing.

pgx prepares every statement and caches it per connection by SQL text. Postgres plans a prepared statement
with the real parameter values five times, then compares their average cost against a **generic** plan
built without the parameters, and if the generic plan looks no worse it switches permanently.
`BenchmarkStrategies` runs the join with `LIMIT 50`, Postgres adopts a generic plan, and `BenchmarkFanOut`
runs the identical SQL with `LIMIT 1` and gets that plan.

`EXPLAIN EXECUTE` under both modes, for `LIMIT 1`:

| plan | shape | time | rows estimated |
| --- | --- | --- | --- |
| custom | Nested Loop, Bitmap Index Scan on `idx_books_author` | 0.033ms | 50 |
| generic | Hash Join, **Seq Scan** on `books` | 0.638ms | 1000 |

19x, because without the parameter Postgres guesses a `LIMIT` returns 1000 rows where the real answer is
38. `nplusone.TestGenericPlanRegression` pins it.

This is the shape of "one endpoint got slow after a deploy and recovered when we restarted the pods".
Parameter-dependent selectivity plus a prepared statement is the ingredient list, and a parameterised
`LIMIT` or a tenant ID with uneven row counts is the trigger.

### The isolation anomalies, each triggered

| scenario | READ COMMITTED | REPEATABLE READ | SERIALIZABLE |
| --- | --- | --- | --- |
| read-then-write transfer | **600 appears from nothing** | 40001, one commits | one commits |
| `SELECT ... FOR UPDATE` | one commits | one commits | one commits |
| the check inside the `UPDATE` | one commits | one commits | one commits |
| write skew on a pair | n/a | **the rule breaks, 1000 to 400** | 40001, one commits |

Two things in there are not in the textbook summary.

**REPEATABLE READ catches the lost update**, with `40001 could not serialize access due to concurrent
update`. Postgres is stricter than the standard requires at every level.

**Write skew needs each individual write to be legal**, which is exactly what makes it dangerous. My first
attempt had two transactions each withdraw 600 from an account holding 500, which is an ordinary CHECK
violation and fails at every level. The working version has each withdraw 300 from its own account while
checking that the pair still holds 600 between them. SERIALIZABLE reports `40001 could not serialize
access due to read/write dependencies among transactions`, which is a different message from the
REPEATABLE READ one and means SSI rather than first-updater-wins.

**And committing an aborted transaction is not an error in Postgres.** After a failed statement every
later statement returns `25P02`; a `COMMIT` is then accepted and performs a ROLLBACK, silently. pgx
notices and returns `pgx.ErrTxCommitRollback`, which most drivers do not.

Savepoints are not free: 2,000 updates in one transaction take 289ms, and the same 2,000 each wrapped in
its own savepoint take 406ms, **1.41x**. Two costs are mixed in that number and the test does not
separate them. Each savepoint is two more statements, `SAVEPOINT` and `RELEASE`, so the second run sends
three times as many round trips. And Postgres caches 64 subtransactions per backend; past that, visibility
checks on rows a subtransaction wrote have to consult `pg_subtrans`, which can mean disk.

### `SELECT ... FOR UPDATE SKIP LOCKED` is a job queue

Six workers, 60 rows, batches of five, no coordination: all 60 claimed, none twice. This one clause is
what every Postgres-backed job queue is built on, and it is the reason a separate queue system is often
unnecessary.

`NOWAIT` turns an unbounded wait into `55P03` in 1ms, which for a user-facing request is usually what you
want.

### Full text search

Stemming is what makes it search rather than `LIKE`:

| config | input | stored as |
| --- | --- | --- |
| `english` | `The Running Dogs` | `'dog':3 'run':2` |
| `simple` | `The Running Dogs` | `'dogs':3 'running':2 'the':1` |

10,000 titles begin with "The" and a full-text search for `the` returns **zero**, because a stop word is
never stored.

One of the four query parsers is unsafe for user input. `to_tsquery('english', 'c++ &')` raises
`42601`, which is a 500 from a search box. The other three never raise, and `websearch_to_tsquery`, which
returns `'c'` here, is the one that behaves like a search engine.

**Reading the stored generated column is 18x faster than recomputing `to_tsvector(title)` per row**, 0.81ms
against 14.68ms, and that gap has nothing to do with any index. The generated column also cannot go stale:
writing to it directly is rejected with `428C9`.

A GIN index on 10,000 rows is never chosen, and the planner is right. The table is 1,632 kB. An index test
needs a table big enough for the index to win.

### pgvector: the index changes the answer

Every other index in this module is exact. A vector index is approximate, and nothing warns you.

| `hnsw.ef_search` | rows returned for `LIMIT 50` | recall | time | index used |
| --- | --- | --- | --- | --- |
| 10 | **10** | 20% | 0.03ms | yes |
| 40 | **40** | 80% | 0.06ms | yes |
| 100 | 50 | 100% | 0.08ms | yes |
| 400 | 50 | 100% | 1.51ms | **no** |

Two findings in that table.

**`ef_search` below the `LIMIT` silently returns fewer rows than asked for.** Not 50 worse rows: 10 rows,
no error. `ef_search` is the size of the candidate list the graph walk keeps, and pgvector's default is 40,
so any query asking for more than 40 results is quietly truncated.

**Turning `ef_search` up far enough turns the index off.** At 400 the cost estimate exceeded a sequential
scan, recall read 100%, and the query got 25x slower.

Recall by ID is meaningless when the data has ties. The first measurement read 20%, 74%, 56%, 100%, which
is noise, because the exact top 50 held 4 distinct distances. Comparing against the distance of the worst
row in the exact answer, with a 1e-6 tolerance for float32 non-associativity, gives the monotonic column
above.

**An IVFFlat index built before the data is twice as big and twice as slow**, 33.3 MB against 16.9 and
0.33ms against 0.17, and recall is 100% both ways. Recall does not find this bug. Never `CREATE INDEX` on
a vector column before the data exists.

Storage arithmetic, which is just multiplication and still surprises people: 10,000 `vector(384)` rows are
16.6 MB, which is **5.9x** the size of the books they describe. At 1,536 dimensions the same 10,000 rows
would be about 62 MB (59 MiB) of vectors alone.

Measure after `VACUUM FULL`, or the number includes dead rows. This paragraph first said 31.7 MB and 11.1x,
measured on a table that repeated test runs had rewritten: half of it was dead tuples.

### The connection pool

| | cold pool (`MinConns: 0`) | pre-warmed (`MinConns: 5`) |
| --- | --- | --- |
| five acquires | 8.5 to 16.3ms | 1.3 to 2.0ms |

Five to ten times, over a unix socket with no TLS. I assumed handshake cost would be invisible locally. On
a managed database over TLS this is the first request after a quiet period showing up as a p99 spike that
gets blamed on the database.

**Three leaked connections take down a pool of three**, and every one of those three requests succeeds.
No database error, no error log, nothing in the plan. The fourth request hangs until its context expires.

**And `pgxpool.Pool.Close` blocks until every connection is released**, so the leak also prevents a clean
shutdown. A service with one leak has to be killed rather than stopped. That cost 156 seconds of a test
run to discover, because the test passed and then the cleanup hung.

50 goroutines against a pool of 5, 20ms per query, took 215ms rather than 20ms. Concurrency in Go is
bounded by `MaxConns`, not by goroutines, and `EmptyAcquireCount` (49 here) is the metric to alert on
because it rises long before anything times out.

### Migrations

**Postgres wraps DDL in transactions and MySQL does not**, which is the strongest practical argument for
Postgres and rarely the one people make. A migration with four statements whose third fails leaves
*nothing* behind: both tables its first two statements created are gone, and goose records no version, so
a fix plus a re-run needs no manual cleanup.

The exception is the one that bites. `CREATE INDEX CONCURRENTLY` cannot run in a transaction, and
`CONCURRENTLY` is exactly what a migration on a live table needs. Without `-- +goose NO TRANSACTION` it
fails with `25001`; with it, the index builds and is valid.

**A down migration restores structure, never content.** `DROP COLUMN name` and
`ADD COLUMN name text NOT NULL DEFAULT ''` look like exact inverses and pass review. Up then down leaves
the schema identical and the two widgets named `""` and `""`. The safe version of a destructive change is
two deploys: stop writing the column, then drop it a release later.

And a migration file without a version prefix is ignored **silently**: no error, no warning, no mention in
the status output. That is most of "my migration did not run".

### Normalisation

Each normal form prevents one anomaly, so each test triggers one.

The update anomaly: one `UPDATE` with a `WHERE` that looks specific touches two of the customer's three
rows, and the customer now lives in two cities. Nothing complains, because the constraint that would have
complained ("one customer, one city") is not expressible on that shape.

The delete anomaly: removing one order line takes the database from two known authors to one, because the
only record of who wrote the book was a row of an order.

**And the most useful finding: the migration that adds the constraint is what tells you the data is
already broken.** Normalising the inconsistent table fails with a unique violation on `n_customers.name`
rather than silently picking a city. Adding a constraint to an existing table is a data-cleaning job
before it is a schema change.

Rebuilding the flat shape from the normalised tables is **not lossless**, which the test found and I had
not planned. Order 1 carried `vip,gift` and order 2 carried `vip`; both come back `gift,vip`. That is the
normalisation asking a question the flat shape let everyone avoid: does a tag describe the customer or the
order? Deciding what each fact is *about* is the work. The normal forms are the notation for having done
it.

## Test isolation, at two levels

Rollback isolation handles tests. `dbtest.Tx` gives each test a transaction that is never committed, so
every row it writes disappears, and `t.Cleanup` registers the rollback so a caller cannot forget it.

It does not handle packages, and finding that out cost a debugging session. `go test ./...` builds one
binary per package and runs up to `GOMAXPROCS` of them at once. Two packages that truncate and reload the
same tables produced `deadlock detected` in one and a **wrong query plan** in the other, because the
dataset one was measuring got emptied underneath it. The deadlock is loud; the wrong plan is a test
reporting a false fact about Postgres, which is worse than a failure.

So each test binary gets its own database, named from `os.Args[0]` (which ends in `<pkg>.test`):
`learn_go_db_indexes`, `learn_go_db_seed` and so on. `CREATE DATABASE` has no `IF NOT EXISTS` and cannot
run in a transaction, so it is a check-then-create, and two packages starting together both try, so
`42P04 duplicate_database` counts as success. About 120ms per package, and it keeps `-p` at its default.

What rollback isolation cannot do, which is why `transactions` and `migrate` truncate instead:

- it cannot test `COMMIT`, or anything reading from another connection
- it cannot test isolation levels or deadlocks, which need two real connections
- it does not reset sequences, so a test asserting `id = 1` passes once
- **a transaction holds locks**, so a test mixing a DDL transaction with a pool query deadlocks against
  itself. That one cost a 120-second timeout: `REINDEX` inside the transaction held `ACCESS EXCLUSIVE`
  while a pool query needed `ACCESS SHARE` to plan, and the transaction was waiting for the test that was
  waiting for the query. Once a test opens a transaction that does DDL, everything in that test goes
  through the transaction.

`SELECT pid, state, wait_event_type, wait_event, query FROM pg_stat_activity` named the culprit in one
query, and is the first thing to run when a database test hangs.

## What did not translate

**`async-sqlalchemy` has no Go equivalent.** Five Python files about async sessions, sessionmakers and
event loops. A goroutine blocking on a socket costs nothing and there is no loop to starve, so the async
half of that module simply does not exist here. What survives is the part that caused the outages: the
pool. That is `pgxdemo`.

**`pgvector-demo` needed a different fixture.** The Python version calls an embedding model. `vectors`
uses a hashed bag of words instead, because a test suite that needs an API key or a 90 MB download is a
test suite nobody runs. It is deterministic and offline, and it demonstrates every mechanical property of
vector search and nothing about embedding quality. Being clear about which half of a subject a fixture
covers is the point.

**`migrate` has no Python equivalent** in the same shape. Alembic's autogenerate does something goose
deliberately does not, and goose's `NO TRANSACTION` annotation has no Alembic counterpart because Alembic
manages the transaction itself. The transactional-DDL measurement is the same lesson either way.

## Things worth stealing from here

- `dbtest.Pool` and `dbtest.Tx`: skip on no database, a transaction per test, a database per package.
- `indexes.Explain`: decodes `EXPLAIN (ANALYZE, FORMAT JSON)` into something a test can assert on, with
  `Uses`, `UsesIndex`, `NodeTypes`, `RowsRemovedByFilter` and a one-line `Summary`.
- `nplusone.Counter`: wraps any `Querier` and counts round trips and statements, so an N+1 regression is a
  failing assertion rather than a slow endpoint.
- `transactions.Retry`: the retry loop that makes SERIALIZABLE usable, with full jitter and a context that
  bounds the retries as well as the query.
- `transactions.WithTx` and `WithSavepoint`: the correct defer-rollback shape, including why
  `pgx.ErrTxClosed` must be tolerated and why one function cannot serve both levels.
- `vectors.Recall`: tie-aware recall, which is the only version that means anything.
- `pgxdemo.AcquireWithTimeout`: distinguishes "the pool is the bottleneck" from "the database is the
  bottleneck", which pgx does not, and getting those the wrong way round is how a team responds to a slow
  database by raising `MaxConns`.
