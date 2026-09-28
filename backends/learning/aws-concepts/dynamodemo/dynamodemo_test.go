package dynamodemo

import (
	"context"
	"testing"
	"time"

	"github.com/alexvervloet/learn-go/backends/learning/aws-concepts/awstest"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/expression"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/stretchr/testify/require"
)

// base is the instant every test's timestamps are offset from, so a sort key is predictable.
var base = time.Date(2025, 3, 1, 12, 0, 0, 0, time.UTC)

// table creates the events table for one test and removes it afterwards.
func table(t *testing.T) (*dynamodb.Client, string) {
	t.Helper()

	client := New(awstest.Config(t))
	name := awstest.Name(t, "events")

	ctx := context.Background()

	require.NoError(t, CreateTable(ctx, client, name))

	t.Cleanup(func() {
		if err := DeleteTable(context.Background(), client, name); err != nil {
			t.Errorf("clean up table %s: %v", name, err)
		}
	})

	return client, name
}

// seed writes n events for a device, one per minute.
func seed(t *testing.T, client *dynamodb.Client, name, device, kind string, n int) []Event {
	t.Helper()

	events := make([]Event, 0, n)

	for i := range n {
		events = append(events, Event{
			DeviceID: device,
			At:       base.Add(time.Duration(i) * time.Minute),
			Kind:     kind,
			Reading:  float64(i),
			Version:  1,
		})
	}

	unprocessed, err := BatchPut(context.Background(), client, name, events)
	require.NoError(t, err)
	require.Zero(t, unprocessed, "nothing was throttled, so every item went in")

	return events
}

// TestPutGetRoundTrip is the key-value half of the API.
func TestPutGetRoundTrip(t *testing.T) {
	client, name := table(t)
	ctx := context.Background()

	want := Event{DeviceID: "sensor-1", At: base, Kind: "temperature", Reading: 21.5, Version: 1}
	require.NoError(t, Put(ctx, client, name, want))

	got, err := Get(ctx, client, name, "sensor-1", base)
	require.NoError(t, err)
	require.Equal(t, want.DeviceID, got.DeviceID)
	require.Equal(t, want.Kind, got.Kind)
	require.InDelta(t, want.Reading, got.Reading, 1e-9)

	// time.Time round-trips through RFC3339Nano in a string attribute. DynamoDB has no date type at all:
	// everything is S, N, B, BOOL, NULL, L, M or a set. Storing a time means choosing an encoding, and the
	// choice has to sort correctly because it is the sort key.
	require.True(t, want.At.Equal(got.At), "want %v, got %v", want.At, got.At)
}

// TestGetMissingItemIsNotAnError is the shape everyone trips over.
func TestGetMissingItemIsNotAnError(t *testing.T) {
	client, name := table(t)
	ctx := context.Background()

	// The wrapper turns it into an error. Underneath, the raw call succeeds.
	_, err := Get(ctx, client, name, "no-such-device", base)
	require.ErrorIs(t, err, ErrNotFound)

	raw, err := client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(name),
		Key:       key("no-such-device", base),
	})
	require.NoError(t, err, "GetItem on a missing key is a successful request")
	require.Empty(t, raw.Item, "the only signal is an empty Item map")
}

// TestPutReplacesTheWholeItem is the PutItem-versus-UpdateItem distinction, made expensive.
func TestPutReplacesTheWholeItem(t *testing.T) {
	client, name := table(t)
	ctx := context.Background()

	full := Event{DeviceID: "sensor-1", At: base, Kind: "temperature", Reading: 21.5, Version: 7}
	require.NoError(t, Put(ctx, client, name, full))

	// A caller who only knows about the reading writes what it knows. The item map is written out rather than
	// marshalled from the struct, because a struct always has every field: leaving Version at its zero value
	// would store 0, not nothing, and the test would pass for the wrong reason.
	//
	// kind is still here because it is the by-kind index's partition key and a key attribute cannot be an
	// empty string. That constraint came from adding the index, and it applies to every item in the table.
	_, err := client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(name),
		Item: map[string]types.AttributeValue{
			"device_id": &types.AttributeValueMemberS{Value: "sensor-1"},
			"at":        &types.AttributeValueMemberS{Value: base.Format(time.RFC3339Nano)},
			"kind":      &types.AttributeValueMemberS{Value: "temperature"},
			"reading":   &types.AttributeValueMemberN{Value: "22"},
		},
	})
	require.NoError(t, err)

	raw, err := client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName:      aws.String(name),
		Key:            key("sensor-1", base),
		ConsistentRead: aws.Bool(true),
	})
	require.NoError(t, err)

	require.Contains(t, raw.Item, "reading")
	require.NotContains(t, raw.Item, "version",
		"PutItem replaced the whole item, so the attribute it did not mention no longer exists")

	// Unmarshalling hides it: a missing attribute becomes the Go zero value, which is indistinguishable from a
	// stored 0. That is why the assertion above is on the attribute map.
	got, err := Get(ctx, client, name, "sensor-1", base)
	require.NoError(t, err)
	require.Zero(t, got.Version)
}

// TestQueryReadsExactlyWhatItReturns is the payoff of the key design.
func TestQueryReadsExactlyWhatItReturns(t *testing.T) {
	client, name := table(t)

	seed(t, client, name, "sensor-1", "temperature", 30)
	seed(t, client, name, "sensor-2", "temperature", 30)
	seed(t, client, name, "sensor-3", "humidity", 30)

	from := base.Add(5 * time.Minute)
	to := base.Add(9 * time.Minute)

	start := time.Now()

	res, err := QueryDevice(context.Background(), client, name, "sensor-1", from, to)
	require.NoError(t, err)

	elapsed := time.Since(start)

	require.Len(t, res.Events, 5, "minutes 5 through 9 inclusive, Between is closed at both ends")

	// The assertion that matters. 90 items in the table, 5 read.
	require.Equal(t, int32(5), res.Scanned,
		"a key condition reads only the range it asked for; ScannedCount == Count")
	require.Equal(t, res.Count, res.Scanned)

	// And they come back in sort-key order without an ORDER BY, because that is how they are stored.
	for i := 1; i < len(res.Events); i++ {
		require.True(t, res.Events[i-1].At.Before(res.Events[i].At),
			"items arrive in sort-key order: %v then %v", res.Events[i-1].At, res.Events[i].At)
	}

	t.Logf("query read %d of 90 items in %v", res.Scanned, elapsed)
}

// TestScanReadsEverythingToReturnALittle is the cost of not having the right key.
//
// This is the assertion the whole package builds towards. Both calls return the same 30 events. One reads 30
// items and one reads 90, and on a real table the second number is however many items you have.
func TestScanReadsEverythingToReturnALittle(t *testing.T) {
	client, name := table(t)
	ctx := context.Background()

	seed(t, client, name, "sensor-1", "temperature", 30)
	seed(t, client, name, "sensor-2", "temperature", 30)
	seed(t, client, name, "sensor-3", "humidity", 30)

	scanStart := time.Now()

	scan, err := ScanForKind(ctx, client, name, "humidity")
	require.NoError(t, err)

	scanElapsed := time.Since(scanStart)

	indexStart := time.Now()

	index, err := QueryByKind(ctx, client, name, "humidity")
	require.NoError(t, err)

	indexElapsed := time.Since(indexStart)

	require.Len(t, scan.Events, 30)
	require.Len(t, index.Events, 30, "the index returns the same answer")

	require.Equal(t, int32(90), scan.Scanned,
		"a filter runs after the read: every item in the table was read and 60 were thrown away")
	require.Equal(t, int32(30), index.Scanned,
		"the index has its own partition, so it reads only what it returns")

	// Three times the reads for the same answer, and the ratio grows with the table. That is the number to
	// remember; the durations are here because LocalStack in one process makes them nearly identical and it is
	// worth seeing that a local emulator cannot show you a cost that is about IO and billing.
	require.Equal(t, int32(3), scan.Scanned/index.Scanned)
	t.Logf("scan read %d items in %v; index read %d in %v",
		scan.Scanned, scanElapsed, index.Scanned, indexElapsed)
}

// TestQueryPagesAtOneMegabyte measures the paging a caller cannot turn off.
//
// DynamoDB stops a Query or Scan at 1 MB of read data and returns a LastEvaluatedKey, whatever Limit says. The
// paginator hides the loop; a hand-written one that returns after the first response silently returns part of
// the answer, and the bug does not appear until the data grows.
func TestQueryPagesAtOneMegabyte(t *testing.T) {
	client, name := table(t)
	ctx := context.Background()

	// 400 items with a 4 KB payload each is about 1.6 MB, so the read has to span more than one page.
	//
	// The padding is in Payload rather than Kind. Kind is the by-kind index's partition key and an index
	// partition key is capped at 2048 bytes, so a 4 KB Kind is rejected with `Size limit exceeded` even though
	// the item is nowhere near the 400 KB item limit.
	padding := make([]byte, 4000)
	for i := range padding {
		padding[i] = 'x'
	}

	const items = 400

	events := make([]Event, 0, items)

	for i := range items {
		events = append(events, Event{
			DeviceID: "bulky",
			At:       base.Add(time.Duration(i) * time.Second),
			Kind:     "bulk",
			Payload:  string(padding),
			Reading:  float64(i),
			Version:  1,
		})
	}

	unprocessed, err := BatchPut(ctx, client, name, events)
	require.NoError(t, err)
	require.Zero(t, unprocessed)

	res, err := QueryDevice(ctx, client, name, "bulky", base, base.Add(time.Hour))
	require.NoError(t, err)

	require.Equal(t, int32(items), res.Count, "the paginator returned everything")
	require.Greater(t, res.Requests, 1,
		"1.6 MB of items cannot come back in one response, whatever the caller asked for: %d requests", res.Requests)

	t.Logf("%d items (~%d KB) took %d requests", items, items*4, res.Requests)
}

// TestConditionalPutIsAnAtomicClaim shows the write half of the concurrency story.
func TestConditionalPutIsAnAtomicClaim(t *testing.T) {
	client, name := table(t)
	ctx := context.Background()

	e := Event{DeviceID: "sensor-1", At: base, Kind: "temperature", Reading: 1, Version: 1}

	first, err := PutIfAbsent(ctx, client, name, e)
	require.NoError(t, err)
	require.True(t, first)

	e.Reading = 999

	second, err := PutIfAbsent(ctx, client, name, e)
	require.NoError(t, err)
	require.False(t, second, "the key is taken, so the condition failed and nothing was written")

	got, err := Get(ctx, client, name, "sensor-1", base)
	require.NoError(t, err)
	require.InDelta(t, 1.0, got.Reading, 1e-9, "the original survived")
}

// TestOptimisticLockingRejectsAStaleWriter is the lost-update fix.
func TestOptimisticLockingRejectsAStaleWriter(t *testing.T) {
	client, name := table(t)
	ctx := context.Background()

	require.NoError(t, Put(ctx, client, name, Event{
		DeviceID: "sensor-1", At: base, Kind: "temperature", Reading: 10, Version: 1,
	}))

	// Two readers see version 1.
	a, err := Get(ctx, client, name, "sensor-1", base)
	require.NoError(t, err)

	b, err := Get(ctx, client, name, "sensor-1", base)
	require.NoError(t, err)
	require.Equal(t, a.Version, b.Version)

	// A writes, and the version moves to 2.
	okA, err := UpdateReading(ctx, client, name, "sensor-1", base, 11, a.Version)
	require.NoError(t, err)
	require.True(t, okA)

	// B writes with the version it read. Without the condition this would overwrite A's value and A would
	// never know. With it, B is told.
	okB, err := UpdateReading(ctx, client, name, "sensor-1", base, 12, b.Version)
	require.NoError(t, err)
	require.False(t, okB, "B's version is stale, so the write is refused")

	got, err := Get(ctx, client, name, "sensor-1", base)
	require.NoError(t, err)
	require.InDelta(t, 11.0, got.Reading, 1e-9, "A's write stands")
	require.Equal(t, 2, got.Version)

	// And B's recovery is to re-read and try again, which now succeeds.
	fresh, err := Get(ctx, client, name, "sensor-1", base)
	require.NoError(t, err)

	retried, err := UpdateReading(ctx, client, name, "sensor-1", base, 12, fresh.Version)
	require.NoError(t, err)
	require.True(t, retried)
}

// TestUpdateTouchesOnlyNamedAttributes is the contrast with PutItem.
func TestUpdateTouchesOnlyNamedAttributes(t *testing.T) {
	client, name := table(t)
	ctx := context.Background()

	require.NoError(t, Put(ctx, client, name, Event{
		DeviceID: "sensor-1", At: base, Kind: "temperature", Reading: 10, Version: 1,
	}))

	ok, err := UpdateReading(ctx, client, name, "sensor-1", base, 42, 1)
	require.NoError(t, err)
	require.True(t, ok)

	got, err := Get(ctx, client, name, "sensor-1", base)
	require.NoError(t, err)
	require.InDelta(t, 42.0, got.Reading, 1e-9)
	require.Equal(t, "temperature", got.Kind, "UpdateItem left the attributes it was not told about alone")
}

// TestBatchWriteCountsRequests shows the 25-item limit as round trips.
func TestBatchWriteCountsRequests(t *testing.T) {
	cfg, counter := awstest.WithCounter(awstest.Config(t))

	client := New(cfg)
	name := awstest.Name(t, "events")

	ctx := context.Background()

	require.NoError(t, CreateTable(ctx, client, name))

	t.Cleanup(func() { _ = DeleteTable(context.Background(), client, name) })

	events := make([]Event, 0, 60)
	for i := range 60 {
		events = append(events, Event{
			DeviceID: "bulk", At: base.Add(time.Duration(i) * time.Second), Kind: "k", Reading: float64(i), Version: 1,
		})
	}

	counter.Reset()

	unprocessed, err := BatchPut(ctx, client, name, events)
	require.NoError(t, err)
	require.Zero(t, unprocessed)

	require.Equal(t, 3, counter.Count("BatchWriteItem"),
		"60 items at 25 per request is 25 + 25 + 10: %v", counter.Operations())

	// The comparison: one PutItem each would be 60 requests. Batching is a round-trip saving and NOT a cost
	// saving, because DynamoDB charges per item written either way.
	counter.Reset()

	for _, e := range events[:10] {
		require.NoError(t, Put(ctx, client, name, e))
	}

	require.Equal(t, 10, counter.Count("PutItem"), "one request per item: %v", counter.Operations())
}

// TestReservedWordsNeedPlaceholders is why the expression builder is not optional.
//
// DynamoDB reserves several hundred words, and they are ordinary ones: name, status, timestamp, size, count,
// year, hash, key, value, data. An expression that names an attribute called any of them is rejected, and the
// error does not name the word.
func TestReservedWordsNeedPlaceholders(t *testing.T) {
	client, name := table(t)
	ctx := context.Background()

	require.NoError(t, Put(ctx, client, name, Event{
		DeviceID: "sensor-1", At: base, Kind: "temperature", Reading: 1, Version: 1,
	}))

	// A hand-written expression naming a reserved word directly.
	//
	// Real DynamoDB rejects this with a ValidationException. LocalStack ACCEPTS it, which is the clearest
	// example in this module of an emulator being more permissive than the service: a test suite that only
	// ever runs here would ship the bug. So the assertion below is on the builder's output, which is computed
	// in this process and true everywhere, and the server call is logged rather than asserted.
	_, err := client.Scan(ctx, &dynamodb.ScanInput{
		TableName:        aws.String(name),
		FilterExpression: aws.String("size = :v"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":v": &types.AttributeValueMemberN{Value: "1"},
		},
	})
	if err == nil {
		t.Log("LocalStack accepted a filter naming the reserved word `size`; real DynamoDB returns ValidationException")
	} else {
		t.Logf("rejected, as the real service does: %v", err)
		require.Equal(t, "ValidationException", APIErrorCode(err))
	}

	// The same filter through the builder. It emits #0 with a names map, so "size" never reaches the parser
	// as an identifier. This is the whole reason to use the builder rather than fmt.Sprintf.
	expr, err := expression.NewBuilder().
		WithFilter(expression.Name("size").Equal(expression.Value(1))).
		Build()
	require.NoError(t, err)

	require.NotContains(t, aws.ToString(expr.Filter()), "size", "the attribute name is a placeholder: %s", aws.ToString(expr.Filter()))
	require.Contains(t, expr.Names(), "#0")
	require.Equal(t, "size", expr.Names()["#0"])

	out, err := client.Scan(ctx, &dynamodb.ScanInput{
		TableName:                 aws.String(name),
		FilterExpression:          expr.Filter(),
		ExpressionAttributeNames:  expr.Names(),
		ExpressionAttributeValues: expr.Values(),
	})
	require.NoError(t, err, "the placeholder form is accepted")
	require.Equal(t, int32(0), out.Count, "no item has a size attribute, which is a different thing from being rejected")
}
