// Package dynamodemo is DynamoDB: the key design that decides everything, and the four operations.
//
// # The one thing to carry away
//
// DynamoDB has no query planner. There is no optimiser to rescue a bad schema, no index it can pick for you,
// and no join. What you can read cheaply is fixed by the key you chose when you made the table, and changing
// your mind later means a new table or a new index.
//
// That sounds like a limitation and it is the product. Because every access path is declared, every access path
// is O(1) in the size of the table, and the latency does not move as the data grows. A relational database will
// happily let you write a query that was fast at ten thousand rows and is a table scan at ten million.
//
// # The key
//
// A partition key alone is a hash map: one item per key, and a Query returns at most that item. A partition key
// plus a sort key is a hash map of sorted lists, and that is where the power is. Every item with the same
// partition key lives together, ordered by sort key, and a Query can take a range of them in one read.
//
// Nearly every DynamoDB design question is "what goes in the sort key".
package dynamodemo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/expression"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/smithy-go"
)

// New builds a DynamoDB client.
func New(cfg aws.Config) *dynamodb.Client {
	return dynamodb.NewFromConfig(cfg)
}

// Event is one item in the table.
//
// # The dynamodbav tags
//
// attributevalue marshals a struct the way encoding/json does, with its own tag. Without a tag the Go field
// name is the attribute name, so a rename in Go silently renames the attribute and old items stop matching.
// Tagging every field is the same discipline as tagging every JSON field, for the same reason, and it matters
// more here because there is no schema to fail against.
//
// `omitempty` is a trap worth naming: DynamoDB cannot store an empty STRING as a key attribute, and until 2020
// it could not store one at all. omitempty drops the attribute entirely, so a struct round-trips to something
// missing a field rather than holding "".
type Event struct {
	DeviceID string    `dynamodbav:"device_id"` // partition key
	At       time.Time `dynamodbav:"at"`        // sort key, written by SortKey so it sorts as a string
	Kind     string    `dynamodbav:"kind"`      // partition key of the by-kind index
	Reading  float64   `dynamodbav:"reading"`
	Version  int       `dynamodbav:"version"`
	Payload  string    `dynamodbav:"payload"`
}

// SortKeyLayout is RFC 3339 in UTC with all nine fractional digits, always.
//
// # Why not the default
//
// attributevalue writes a time.Time as RFC3339Nano, which trims trailing zeros and keeps the zone. Both break a
// sort key, because DynamoDB compares S keys byte by byte. A whole second is "12:00:00Z" and a tenth later is
// "12:00:00.1Z", and 'Z' sorts after '.', so the earlier event sorts last. A range from the whole second to
// half a second later has a lower bound above its upper bound, and DynamoDB rejects the query outright. A time
// carrying +01:00 is an hour away in string order from the same instant in UTC.
//
// Fixed width and one zone make string order and time order the same thing.
const SortKeyLayout = "2006-01-02T15:04:05.000000000Z"

// SortKey formats t for the "at" attribute.
func SortKey(t time.Time) string {
	return t.UTC().Format(SortKeyLayout)
}

// marshalEvent is attributevalue.MarshalMap with the sort key written by SortKey. Every write goes through it,
// because one write in the default format puts that item out of order.
func marshalEvent(e Event) (map[string]types.AttributeValue, error) {
	item, err := attributevalue.MarshalMap(e)
	if err != nil {
		return nil, err
	}

	item["at"] = &types.AttributeValueMemberS{Value: SortKey(e.At)}

	return item, nil
}

// Two constraints on key attributes that only bite once an index exists, both found by the tests here:
//
//   - A key attribute cannot be an EMPTY STRING. Ordinary attributes have allowed one since 2020; keys never
//     have. Because `kind` is the by-kind index's partition key, an Event with no Kind is rejected outright,
//     and the error names the index rather than the field you forgot to set.
//   - An index partition key is capped at 2048 bytes and a sort key at 1024. The item itself can be 400 KB, so
//     a large value is fine everywhere except in a key. Payload exists for that reason: it holds the bulk in
//     the page-size test, where putting it in Kind failed with `Size limit exceeded`.
//
// The general shape: adding a GSI adds constraints to the BASE table's items. An attribute that was optional
// and unbounded becomes required and bounded for every item, retroactively.

// CreateTable makes the events table and waits for it to be usable.
//
// # Why the wait is not optional
//
// CreateTable returns as soon as the request is accepted, with the table in CREATING. A PutItem against a
// CREATING table fails. The SDK ships waiters for exactly this, and using one is better than a sleep because it
// polls and has a deadline.
//
// # Why on-demand billing
//
// PAY_PER_REQUEST means no capacity to provision and no throttling to tune. Provisioned mode is cheaper at
// steady high volume and is a capacity-planning exercise; on-demand is the right default and certainly the
// right thing for a test.
func CreateTable(ctx context.Context, client *dynamodb.Client, table string) error {
	_, err := client.CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName: aws.String(table),

		// AttributeDefinitions lists only the attributes used in a KEY, not every attribute an item has.
		// That is the schemaless part: two items in one table can have completely different fields, and the
		// only thing the table knows about is its keys.
		AttributeDefinitions: []types.AttributeDefinition{
			{AttributeName: aws.String("device_id"), AttributeType: types.ScalarAttributeTypeS},
			{AttributeName: aws.String("at"), AttributeType: types.ScalarAttributeTypeS},
			{AttributeName: aws.String("kind"), AttributeType: types.ScalarAttributeTypeS},
		},
		KeySchema: []types.KeySchemaElement{
			{AttributeName: aws.String("device_id"), KeyType: types.KeyTypeHash},
			{AttributeName: aws.String("at"), KeyType: types.KeyTypeRange},
		},

		// A global secondary index is a second table, maintained for you, with its own key. "Global" means its
		// partition key is unrelated to the base table's, so this one groups by kind across every device.
		//
		// Two things it is not: consistent (a GSI is eventually consistent, always, and a read straight after
		// a write can miss) and free (every write to the base table writes to the index too, and you pay for
		// both).
		GlobalSecondaryIndexes: []types.GlobalSecondaryIndex{{
			IndexName: aws.String("by-kind"),
			KeySchema: []types.KeySchemaElement{
				{AttributeName: aws.String("kind"), KeyType: types.KeyTypeHash},
				{AttributeName: aws.String("at"), KeyType: types.KeyTypeRange},
			},
			// KEYS_ONLY copies only the keys into the index, so a query on it gives you enough to fetch the
			// item and nothing more. ALL copies every attribute and costs storage twice. INCLUDE is the middle.
			// The choice is a storage-versus-second-read trade and it cannot be changed without rebuilding.
			Projection: &types.Projection{ProjectionType: types.ProjectionTypeAll},
		}},

		BillingMode: types.BillingModePayPerRequest,
	})
	if err != nil {
		return err
	}

	waiter := dynamodb.NewTableExistsWaiter(client)

	return waiter.Wait(ctx, &dynamodb.DescribeTableInput{TableName: aws.String(table)}, 2*time.Minute)
}

// DeleteTable removes a table.
func DeleteTable(ctx context.Context, client *dynamodb.Client, table string) error {
	_, err := client.DeleteTable(ctx, &dynamodb.DeleteTableInput{TableName: aws.String(table)})

	return err
}

// Put writes an item, overwriting whatever was there.
//
// PutItem replaces the WHOLE item. An item with five attributes, put again with three, has three. That is the
// difference from UpdateItem and the source of a data-loss bug in every codebase that read an item, modified a
// field, and put it back while another writer did the same.
func Put(ctx context.Context, client *dynamodb.Client, table string, e Event) error {
	item, err := marshalEvent(e)
	if err != nil {
		return err
	}

	_, err = client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(table),
		Item:      item,
	})

	return err
}

// ErrNotFound is returned when a key has no item.
var ErrNotFound = errors.New("dynamodemo: item not found")

// Get reads one item by its full key.
//
// # GetItem returns no error for a missing item
//
// The response has an empty Item map and a 200. That is consistent with the API being a key-value store (asking
// for a key that is not there is not an error) and it catches everyone, because `err == nil` is not the same as
// "I have an item". This wraps it into an error, which is what calling code usually wants.
func Get(ctx context.Context, client *dynamodb.Client, table, deviceID string, at time.Time) (Event, error) {
	out, err := client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(table),
		Key:       key(deviceID, at),

		// Strongly consistent. The default is eventually consistent, which is half the price and can return a
		// value from before a write that has already returned success. For a test asserting on a write that
		// just happened, the default is a flake generator.
		ConsistentRead: aws.Bool(true),
	})
	if err != nil {
		return Event{}, err
	}

	if len(out.Item) == 0 {
		return Event{}, fmt.Errorf("%w: %s/%s", ErrNotFound, deviceID, at.Format(time.RFC3339))
	}

	var e Event
	if err := attributevalue.UnmarshalMap(out.Item, &e); err != nil {
		return Event{}, err
	}

	return e, nil
}

// key builds the primary key map.
func key(deviceID string, at time.Time) map[string]types.AttributeValue {
	return map[string]types.AttributeValue{
		"device_id": &types.AttributeValueMemberS{Value: deviceID},
		"at":        &types.AttributeValueMemberS{Value: SortKey(at)},
	}
}

// QueryResult carries what a read cost as well as what it found.
//
// ScannedCount is the number the whole module exists to make visible: it is how many items the database READ,
// while Count is how many it returned. A Query with a key condition has them equal. A Scan with a filter can
// read a million to return three, and the bill is for the million.
type QueryResult struct {
	Events   []Event
	Count    int32
	Scanned  int32
	Requests int
}

// QueryDevice reads one device's events in a time range.
//
// # This is the operation the key was designed for
//
// The key condition names the partition and a range on the sort key, so DynamoDB goes straight to one partition
// and reads a contiguous run. ScannedCount equals Count: nothing is read and thrown away.
//
// The expression builder is used rather than hand-written strings because DynamoDB reserves a long list of words
// ("name", "status", "timestamp", "size" among them) and an expression using one fails with a message that does
// not say which word it objected to. The builder emits #n0 placeholders and a names map, so a reserved word
// never reaches the server.
func QueryDevice(ctx context.Context, client *dynamodb.Client, table, deviceID string, from, to time.Time) (QueryResult, error) {
	cond := expression.Key("device_id").Equal(expression.Value(deviceID)).
		And(expression.Key("at").Between(
			expression.Value(SortKey(from)),
			expression.Value(SortKey(to)),
		))

	expr, err := expression.NewBuilder().WithKeyCondition(cond).Build()
	if err != nil {
		return QueryResult{}, err
	}

	var result QueryResult

	p := dynamodb.NewQueryPaginator(client, &dynamodb.QueryInput{
		TableName:                 aws.String(table),
		KeyConditionExpression:    expr.KeyCondition(),
		ExpressionAttributeNames:  expr.Names(),
		ExpressionAttributeValues: expr.Values(),
		ConsistentRead:            aws.Bool(true),
	})

	for p.HasMorePages() {
		out, err := p.NextPage(ctx)
		if err != nil {
			return QueryResult{}, err
		}

		result.Requests++
		result.Count += out.Count
		result.Scanned += out.ScannedCount

		var page []Event
		if err := attributevalue.UnmarshalListOfMaps(out.Items, &page); err != nil {
			return QueryResult{}, err
		}

		result.Events = append(result.Events, page...)
	}

	return result, nil
}

// ScanForKind reads the whole table and filters.
//
// # The operation to avoid, shown so the cost is visible
//
// A filter expression runs AFTER the read. DynamoDB reads items, charges for them, then discards the ones that
// do not match. So this returns the same events QueryByKind does and reads every item in the table to do it.
//
// The test asserts on the difference. That is the entire argument for spending time on key design.
func ScanForKind(ctx context.Context, client *dynamodb.Client, table, kind string) (QueryResult, error) {
	filter := expression.Name("kind").Equal(expression.Value(kind))

	expr, err := expression.NewBuilder().WithFilter(filter).Build()
	if err != nil {
		return QueryResult{}, err
	}

	var result QueryResult

	p := dynamodb.NewScanPaginator(client, &dynamodb.ScanInput{
		TableName:                 aws.String(table),
		FilterExpression:          expr.Filter(),
		ExpressionAttributeNames:  expr.Names(),
		ExpressionAttributeValues: expr.Values(),
	})

	for p.HasMorePages() {
		out, err := p.NextPage(ctx)
		if err != nil {
			return QueryResult{}, err
		}

		result.Requests++
		result.Count += out.Count
		result.Scanned += out.ScannedCount

		var page []Event
		if err := attributevalue.UnmarshalListOfMaps(out.Items, &page); err != nil {
			return QueryResult{}, err
		}

		result.Events = append(result.Events, page...)
	}

	return result, nil
}

// QueryByKind reads through the global secondary index.
//
// Same answer as ScanForKind, one partition instead of the table. The index exists because "all events of this
// kind" is a question the base table's key cannot answer, and adding it was a decision made when the table was
// designed rather than a query the database worked out.
func QueryByKind(ctx context.Context, client *dynamodb.Client, table, kind string) (QueryResult, error) {
	cond := expression.Key("kind").Equal(expression.Value(kind))

	expr, err := expression.NewBuilder().WithKeyCondition(cond).Build()
	if err != nil {
		return QueryResult{}, err
	}

	var result QueryResult

	p := dynamodb.NewQueryPaginator(client, &dynamodb.QueryInput{
		TableName:                 aws.String(table),
		IndexName:                 aws.String("by-kind"),
		KeyConditionExpression:    expr.KeyCondition(),
		ExpressionAttributeNames:  expr.Names(),
		ExpressionAttributeValues: expr.Values(),
		// No ConsistentRead: a GSI cannot do one. The API rejects it. That is the price of the second access
		// path and it is why an index is wrong for a read-after-write check.
	})

	for p.HasMorePages() {
		out, err := p.NextPage(ctx)
		if err != nil {
			return QueryResult{}, err
		}

		result.Requests++
		result.Count += out.Count
		result.Scanned += out.ScannedCount

		var page []Event
		if err := attributevalue.UnmarshalListOfMaps(out.Items, &page); err != nil {
			return QueryResult{}, err
		}

		result.Events = append(result.Events, page...)
	}

	return result, nil
}

// PutIfAbsent writes an item only when the key is free.
//
// # The condition expression is the whole concurrency story
//
// This needs no transaction. A condition expression is evaluated on the partition holding the item, atomically
// with the write, so two writers racing produce one success and one ConditionalCheckFailedException.
//
// DynamoDB does have transactions, TransactWriteItems and TransactGetItems, for the case a condition cannot
// cover: several items that must all change or none. Each costs twice the capacity of the plain operation, so
// they are for that case, not for making one conditional write feel safer.
//
// attribute_not_exists(device_id) reads oddly. It is not asking whether the attribute is missing from the item
// you are writing; it asks whether the ITEM AT THIS KEY already has it, and an item that does not exist has no
// attributes. So "the partition key attribute does not exist" is the idiom for "this key is free".
func PutIfAbsent(ctx context.Context, client *dynamodb.Client, table string, e Event) (wrote bool, err error) {
	item, err := marshalEvent(e)
	if err != nil {
		return false, err
	}

	cond := expression.AttributeNotExists(expression.Name("device_id"))

	expr, err := expression.NewBuilder().WithCondition(cond).Build()
	if err != nil {
		return false, err
	}

	_, err = client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName:                aws.String(table),
		Item:                     item,
		ConditionExpression:      expr.Condition(),
		ExpressionAttributeNames: expr.Names(),
	})
	if err == nil {
		return true, nil
	}

	var failed *types.ConditionalCheckFailedException
	if errors.As(err, &failed) {
		return false, nil
	}

	return false, err
}

// UpdateReading changes one attribute if the version matches, and bumps the version.
//
// # Optimistic locking in one request
//
// Read-modify-write over a network is a lost update waiting to happen. The fix is to make the write conditional
// on the version you read, so a writer whose version is stale is rejected rather than silently overwriting.
//
// Note what this does NOT do: it does not re-read, it does not lock, and it does not retry. One request, and
// the caller decides what a rejection means. UpdateItem also touches only the named attributes, so the other
// fields are untouched whatever else happened to them.
func UpdateReading(ctx context.Context, client *dynamodb.Client, table, deviceID string, at time.Time, reading float64, expectedVersion int) (ok bool, err error) {
	update := expression.Set(expression.Name("reading"), expression.Value(reading)).
		Set(expression.Name("version"), expression.Value(expectedVersion+1))

	cond := expression.Name("version").Equal(expression.Value(expectedVersion))

	expr, err := expression.NewBuilder().WithUpdate(update).WithCondition(cond).Build()
	if err != nil {
		return false, err
	}

	_, err = client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:                 aws.String(table),
		Key:                       key(deviceID, at),
		UpdateExpression:          expr.Update(),
		ConditionExpression:       expr.Condition(),
		ExpressionAttributeNames:  expr.Names(),
		ExpressionAttributeValues: expr.Values(),
	})
	if err == nil {
		return true, nil
	}

	var failed *types.ConditionalCheckFailedException
	if errors.As(err, &failed) {
		return false, nil
	}

	return false, err
}

// BatchPut writes many items and reports what the service refused.
//
// # UnprocessedItems is not an error
//
// BatchWriteItem takes up to 25 items and can partially succeed. The response carries the ones it did not write,
// with a 200 status and a nil error, and a caller that ignores the field loses data silently. This is the single
// most common DynamoDB bug and the API shape invites it.
//
// The right handling is to retry the unprocessed set with backoff, because the usual cause is throttling. This
// returns them instead, so a test can show they exist and a reader has to decide.
func BatchPut(ctx context.Context, client *dynamodb.Client, table string, events []Event) (unprocessed int, err error) {
	const maxBatch = 25 // a hard API limit, not a tuning knob

	for start := 0; start < len(events); start += maxBatch {
		end := min(start+maxBatch, len(events))

		requests := make([]types.WriteRequest, 0, end-start)

		for _, e := range events[start:end] {
			item, err := marshalEvent(e)
			if err != nil {
				return 0, err
			}

			requests = append(requests, types.WriteRequest{
				PutRequest: &types.PutRequest{Item: item},
			})
		}

		out, err := client.BatchWriteItem(ctx, &dynamodb.BatchWriteItemInput{
			RequestItems: map[string][]types.WriteRequest{table: requests},
		})
		if err != nil {
			return 0, err
		}

		unprocessed += len(out.UnprocessedItems[table])
	}

	return unprocessed, nil
}

// APIErrorCode returns the AWS error code, or "".
func APIErrorCode(err error) string {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		return apiErr.ErrorCode()
	}

	return ""
}
