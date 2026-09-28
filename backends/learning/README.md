# backends/learning

Thirteen modules, one per concept, each a Go module of its own with its own `go.mod`. They mirror the
Python repo's `backends/learning/` directory, and where a Python module has no sensible Go equivalent the
mapping is stated rather than forced.

| module | status | mirrors | what it covers |
| --- | --- | --- | --- |
| [http-tutorial/](http-tutorial/) | done | `fast-api-tutorial` | `net/http` with Go 1.22 routing, middleware, request decoding, RFC 9457 errors, graceful shutdown |
| [testing-concepts/](testing-concepts/) | done | `testing-concepts` | table-driven tests, `testing/synctest`, golden files, fuzzing, the race detector |
| [database-concepts/](database-concepts/) | done | `database-concepts` | pgx, indexes, N+1, transactions, window functions, full text, pgvector, goose |
| [backend-concepts/](backend-concepts/) | done | `backend-concepts` | pagination, rate limiting, caching, webhooks, JWT, OAuth, observability, WebSockets, Kafka |
| [grpc-concepts/](grpc-concepts/) | done | `grpc-concepts` | the four RPC patterns, status codes, metadata, interceptors |
| [graphql-concepts/](graphql-concepts/) | done | `graphql-concepts` | gqlgen, the N+1 and dataloaders, error models, complexity limiting |
| [jobs-concepts/](jobs-concepts/) | done | `celery-concepts` | asynq, retries, timeouts, scheduling, idempotency, inspection |
| [email-concepts/](email-concepts/) | done | `email-concepts` | MIME building, SMTP with TLS, two template packages, a mail catcher |
| [docker-concepts/](docker-concepts/) | done | `docker-concepts` | seven Dockerfiles measured, signals, scratch, ldflags, runtime limits |
| aws-concepts/ | not started | `aws-concepts` | the AWS SDK v2, S3, SQS, local testing |
| github-actions/ | not started | `github-actions` | the workflow this repo already runs, explained |
| [makefile-concepts/](makefile-concepts/) | done | `makefile-concepts` | rules are files, expansion, the traps, and Makefiles in a Go workspace |
| ai-concepts/ | not started | `ai-concepts` | calling models from Go, streaming, structured output |

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
