# backends/learning

Thirteen modules, one per concept, each a Go module of its own with its own `go.mod`.

| module | what it covers |
| --- | --- |
| [http-tutorial/](http-tutorial/) | `net/http` with Go 1.22 routing, middleware, request decoding, RFC 9457 errors, graceful shutdown |
| [testing-concepts/](testing-concepts/) | table-driven tests, `testing/synctest`, golden files, fuzzing, the race detector |
| [database-concepts/](database-concepts/) | pgx, indexes, N+1, transactions, window functions, full text, pgvector, goose |
| [backend-concepts/](backend-concepts/) | pagination, rate limiting, caching, webhooks, JWT, OAuth, observability, WebSockets, Kafka |
| [grpc-concepts/](grpc-concepts/) | the four RPC patterns, status codes, metadata, interceptors |
| [graphql-concepts/](graphql-concepts/) | gqlgen, the N+1 and dataloaders, error models, complexity limiting |
| [jobs-concepts/](jobs-concepts/) | asynq, retries, timeouts, scheduling, idempotency, inspection |
| [email-concepts/](email-concepts/) | MIME building, SMTP with TLS, two template packages, a mail catcher |
| [docker-concepts/](docker-concepts/) | seven Dockerfiles measured, signals, scratch, ldflags, runtime limits |
| [aws-concepts/](aws-concepts/) | S3, DynamoDB, SQS, SNS against LocalStack, with the request counts measured |
| [github-actions/](github-actions/) | a workflow checker in Go, pointed at this repo's own CI |
| [makefile-concepts/](makefile-concepts/) | rules are files, expansion, the traps, and Makefiles in a Go workspace |
| [ai-concepts/](ai-concepts/) | the request shape, the tool loop, streaming, retrieval, and the cost of each |

## What every module here has

- its own `go.mod`, added to the workspace with `go work use`, so a reader can clone one directory and
  build it
- a package doc explaining the decision each file makes, not what the code does
- tests that assert on something machine-independent, with the timings logged beside them
- a `README.md` with every number measured rather than looked up
- benchmarks where a claim about cost is being made

## The convention that took a while to settle

Numbers in prose are written **after** the measurement, never before. The repo's `LESSONS.md` has six
separate entries where a figure written from memory turned out to be wrong, and two where the measurement
contradicted advice that is repeated everywhere. That is now the rule: write the demo, run the tools, then
write the paragraph.
