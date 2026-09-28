# AWS concepts

S3, DynamoDB, SQS and SNS, against LocalStack, with the cost of each decision measured rather than described.

```bash
docker compose up -d
go test ./...
```

Without LocalStack every test skips and says how to start it.

## What is here

| Package | Subject |
|---|---|
| [`awstest/`](awstest/) | The harness: LocalStack config, a request counter, per-test resource names |
| [`s3demo/`](s3demo/) | Buckets, the flat keyspace, presigned URLs, conditional writes, multipart |
| [`dynamodemo/`](dynamodemo/) | Key design, Query against Scan, conditions, optimistic locking, batching |
| [`sqsdemo/`](sqsdemo/) | Visibility timeouts, long polling, dead letter queues, FIFO |
| [`snsdemo/`](snsdemo/) | Fan-out, filter policies, raw delivery, the queue policy everyone forgets |

## The number each package asserts on

Every module in this repo follows one rule: assert the machine-independent quantity, log the timing next to it.
A duration on a laptop running LocalStack in Docker tells you nothing about a service call across a region. A
request count tells you the same thing on every machine, and it is also what AWS bills for.

| Claim | The number the test asserts |
|---|---|
| Listing a bucket is a loop, not a call | 7 keys at `MaxKeys=3` is exactly 3 HTTP requests |
| Presigning is local HMAC | 0 requests, for a bucket that does not exist |
| Multipart trades round trips for retry granularity | 4 parts is 6 requests: create, four uploads, complete |
| A DynamoDB key condition reads only what it returns | `ScannedCount == Count == 5`, from a table holding 90 |
| A filter expression runs after the read | scan reads 90 to return 30; the index reads 30 |
| DynamoDB pages at 1 MB whatever you ask for | 400 items of 4 KB come back in 2 responses |
| Batching is a round-trip saving | 60 items is 3 `BatchWriteItem` calls; 10 items one at a time is 10 `PutItem` calls |
| Long polling is not a tuning knob | 940 requests against 1, for the same one-second wait |
| SNS fan-out costs the publisher nothing | 1 `Publish`, 3 subscribers, 1 HTTP request |

That long-polling number is the one to remember. Both loops waited the same second for the same message.

## S3

S3 is not a filesystem. It is a flat map from key to bytes with a strongly consistent single-key read and a
conditional write, and everything else follows.

There are no directories. `TestKeysWithSlashesAreNotDirectories` lists the same four keys twice, once flat and
once with a delimiter, and the "folders" appear in the second listing only. They are computed per request.

There is no rename, so `CopyObject` exists and a move is a copy plus a delete with a window in between where
both exist.

The interesting recent addition is `If-None-Match: *` on a PUT, which makes the write conditional on the key
being free. That is enough to build a lock with no second service, and `TestPutIfAbsentIsALock` shows the second
writer being refused where a plain `PutObject` overwrites in silence.

Two things the tests found that the documentation states quietly:

- In **us-east-1 only**, re-creating a bucket you already own returns 200 OK instead of
  `BucketAlreadyOwnedByYou`. So the "did I create it" boolean is truthful in every region except the default one.
- The SDK now sends a **CRC32 checksum** with every upload. For multipart that has to be declared on
  `CreateMultipartUpload`, or the parts and the server disagree about the checksum type and the upload fails.

## DynamoDB

DynamoDB has no query planner. What you can read cheaply is fixed by the key you chose when you made the table.
That is the product, not a limitation: every access path is declared, so every access path is O(1) in the size of
the table.

`TestScanReadsEverythingToReturnALittle` is the argument for spending time on key design. Two calls return the
same 30 events. One reads 30 items and one reads 90, and the 90 is however many items you have.

Three shapes worth internalising:

- **`GetItem` on a missing key is not an error.** It is a 200 with an empty `Item` map. `err == nil` does not
  mean you have an item.
- **`PutItem` replaces the whole item.** An attribute the new item does not mention is gone. `UpdateItem`
  touches only what it names, which is why read-modify-write over a network belongs in a condition expression
  rather than a `PutItem`.
- **`BatchWriteItem` can partially succeed** with a 200 and a nil error. `UnprocessedItems` is where the data
  goes when nobody looks.

Adding the `by-kind` index added constraints to the base table, retroactively: a key attribute cannot be an empty
string, and an index partition key is capped at 2048 bytes while an item can be 400 KB. Both were found by tests
that were about something else.

## SQS

Receiving a message does not remove it. It hides it for the visibility timeout and then it comes back. The only
thing that removes a message is `DeleteMessage`.

Everything else follows. Delivery is at-least-once, so handlers are idempotent. A slow handler needs
`ChangeMessageVisibility` as a heartbeat, or a short timeout plus extensions, which keeps failures fast and
successes safe. Setting the visibility to 0 hands a message back immediately, which is the right move for a
handler that knows it cannot do the work.

A receipt handle identifies one RECEIPT, not the message, and changes every time. A message id cannot be used to
delete.

`TestDeadLetterQueueCatchesAPoisonMessage` receives a message three times without deleting it and then finds it
in the dead letter queue. The redrive policy is a JSON document inside a string map, and `maxReceiveCount` has to
be a JSON string.

FIFO ordering is **per message group**, so the tests assert order within `group-a` and within `group-b` and say
nothing about the two together. Deduplication is per id within five minutes, and a duplicate send returns the
ORIGINAL message id with no error, so the sender cannot tell it was dropped.

## SNS

SNS stores nothing. A publish is a fan-out to whatever is subscribed at that moment, which
`TestSubscribersAddedAfterAPublishMissIt` demonstrates by adding a queue between two publishes.

That is why the standard pattern is SNS in front of SQS. The topic routes, the queue buffers and retries.

Two settings that decide whether it works:

- **The queue's resource policy.** Subscribing succeeds without it, publishing succeeds without it, and nothing
  is delivered. The permission lives on the queue because the queue is what is being written to. LocalStack is
  permissive enough not to need it, which is exactly why the code sets it.
- **`RawMessageDelivery`.** Off by default, which wraps the message in a JSON envelope carrying the topic ARN, a
  timestamp and the payload as a string field. On, the queue receives the published bytes, and a consumer
  written against a direct SQS send keeps working when a topic is put in front of it.

Filter policies match **message attributes**, not the body. `TestFilterMatchesAttributesNotTheBody` publishes
`{"status":"paid"}` to a subscriber filtering on `status` and nothing is delivered.

## Where LocalStack is not AWS

Worth knowing, because a test suite that only runs here can ship a bug.

| Behaviour | LocalStack | Real AWS |
|---|---|---|
| Reserved words in an expression | accepted | `ValidationException` |
| Presigned URL expiry | not checked unless `S3_SKIP_SIGNATURE_VALIDATION=0` | always checked |
| IAM | not enforced on the Community tier | enforced |
| Multipart checksum combination | stricter than S3 about the declared type | more forgiving |

The compose file sets `S3_SKIP_SIGNATURE_VALIDATION=0` so the expiry test tests something.

The reserved-word case is handled by asserting on what the expression builder produces, which is computed in
this process and true everywhere, and logging what the server did.

## The harness

`awstest.Config(t)` pins three things: the endpoint, static credentials, and the region. The credentials matter
most. Without them the SDK walks its provider chain, finds a real profile in `~/.aws/credentials`, and sends the
request to real AWS with real billing attached.

`awstest.WithCounter` wraps the HTTP client, so a test can count what actually went on the wire. One SDK call is
not one HTTP request: a retry is a second request, a paginator makes one per page, a multipart upload makes one
per part plus two.

That counter had a bug worth repeating. It read the request body with `ParseForm` to name the operation, which
consumed it, and every retried SNS request failed with `ContentLength=93 with Body length 0`. A middleware that
reads a request body has to put it back.

## Cost

Everything here runs against LocalStack. No AWS account, no credentials, no bill.

If you point it at real AWS by setting `AWS_ENDPOINT_URL` and supplying credentials, the tests create and delete
buckets, tables, queues and topics. The DynamoDB tables are on-demand billing and the data is small, so the cost
is cents, but it is not zero, and `TestQueryPagesAtOneMegabyte` writes 1.6 MB.
