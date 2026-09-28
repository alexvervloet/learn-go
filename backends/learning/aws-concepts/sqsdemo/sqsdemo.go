// Package sqsdemo is SQS: a queue that gives you at-least-once delivery and asks you to do the rest.
//
// # The one thing to carry away
//
// Receiving a message does not remove it. It HIDES it, for the visibility timeout, and then it comes back. The
// only thing that removes a message is DeleteMessage, called by the consumer after the work is done.
//
// Every property of SQS follows from that. Delivery is at-least-once because a consumer that crashes after
// doing the work and before deleting will see the message again, so handlers have to be idempotent. A slow
// handler gets the message redelivered mid-flight, which is why the visibility timeout has to exceed the
// realistic worst case and why extending it is a real operation. And a message that fails forever has to be
// caught by a redrive policy, or it will be received until it expires.
//
// # Standard and FIFO
//
// A standard queue is unordered, at-least-once, and effectively unlimited in throughput. A FIFO queue gives
// ordering within a message group and exactly-once processing inside a five-minute deduplication window, at a
// lower throughput ceiling. The group id is the interesting part: ordering is per group, so throughput scales
// with the number of groups and a single group is a single lane.
package sqsdemo

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/aws/smithy-go"
)

// New builds an SQS client.
func New(cfg aws.Config) *sqs.Client {
	return sqs.NewFromConfig(cfg)
}

// CreateQueue makes a queue and returns its URL.
//
// # Everything is an attribute, and everything is a string
//
// The API has no typed configuration. Visibility timeout, retention, the redrive policy and the FIFO flag are
// all entries in a map[string]string, and a typo in a key is accepted silently while a typo in a value is a
// validation error. That asymmetry is worth knowing: `VisibilityTimout: "30"` configures nothing and reports
// success.
func CreateQueue(ctx context.Context, client *sqs.Client, name string, attrs map[string]string) (string, error) {
	out, err := client.CreateQueue(ctx, &sqs.CreateQueueInput{
		QueueName:  aws.String(name),
		Attributes: attrs,
	})
	if err != nil {
		return "", err
	}

	return aws.ToString(out.QueueUrl), nil
}

// DeleteQueue removes a queue.
//
// A deleted queue name cannot be reused for 60 seconds. Test names are unique per test for that reason among
// others.
func DeleteQueue(ctx context.Context, client *sqs.Client, url string) error {
	_, err := client.DeleteQueue(ctx, &sqs.DeleteQueueInput{QueueUrl: aws.String(url)})

	return err
}

// Send puts one message on a queue.
func Send(ctx context.Context, client *sqs.Client, url, body string) (messageID string, err error) {
	out, err := client.SendMessage(ctx, &sqs.SendMessageInput{
		QueueUrl:    aws.String(url),
		MessageBody: aws.String(body),
	})
	if err != nil {
		return "", err
	}

	return aws.ToString(out.MessageId), nil
}

// SendFIFO puts one message on a FIFO queue.
//
// # The two ids
//
// MessageGroupId is the ordering lane. Messages in one group are delivered in order and a message is not
// delivered until the one before it is deleted, so a stuck message blocks its group and nothing else.
//
// MessageDeduplicationId is what makes a retry safe. SQS remembers it for five minutes and drops a second
// message with the same id, returning success. Passing a content hash gives you idempotent sends for free;
// passing a unique id per send turns it off.
func SendFIFO(ctx context.Context, client *sqs.Client, url, body, group, dedup string) (messageID string, err error) {
	out, err := client.SendMessage(ctx, &sqs.SendMessageInput{
		QueueUrl:               aws.String(url),
		MessageBody:            aws.String(body),
		MessageGroupId:         aws.String(group),
		MessageDeduplicationId: aws.String(dedup),
	})
	if err != nil {
		return "", err
	}

	return aws.ToString(out.MessageId), nil
}

// SendBatch sends up to ten messages in one request.
//
// Ten is a hard limit, and so is 256 KB for the whole batch. Like DynamoDB's BatchWriteItem this can partially
// succeed, and the Failed slice is the part a careless caller drops on the floor.
func SendBatch(ctx context.Context, client *sqs.Client, url string, bodies []string) (failed int, err error) {
	const maxBatch = 10

	for start := 0; start < len(bodies); start += maxBatch {
		end := min(start+maxBatch, len(bodies))

		entries := make([]types.SendMessageBatchRequestEntry, 0, end-start)

		for i, body := range bodies[start:end] {
			entries = append(entries, types.SendMessageBatchRequestEntry{
				// The id is per request, not a message id. It exists so the response can tell you WHICH of
				// the ten failed, and it has to be unique within the batch.
				Id:          aws.String(strconv.Itoa(start + i)),
				MessageBody: aws.String(body),
			})
		}

		out, err := client.SendMessageBatch(ctx, &sqs.SendMessageBatchInput{
			QueueUrl: aws.String(url),
			Entries:  entries,
		})
		if err != nil {
			return 0, err
		}

		failed += len(out.Failed)
	}

	return failed, nil
}

// Receive takes up to max messages, waiting up to wait for them to arrive.
//
// # Long polling is not a tuning knob, it is the correct setting
//
// With wait at 0 the call returns immediately, usually with nothing, and a consumer loop becomes a spin: one
// billed request per iteration, and a message can sit for however long the loop sleeps. That is short polling.
//
// With wait above 0 the call holds the connection open until a message arrives or the time is up. One request
// covers twenty seconds instead of hundreds covering the same span, and a message is delivered the moment it
// lands. The tests count the requests, because that is the number that changes.
//
// The other subtlety: with a standard queue, a receive samples a SUBSET of the servers holding the queue, so
// asking for ten messages can return three when there are fifty. An empty response does not mean an empty
// queue.
func Receive(ctx context.Context, client *sqs.Client, url string, maxMessages int32, wait time.Duration) ([]types.Message, error) {
	out, err := client.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
		QueueUrl:            aws.String(url),
		MaxNumberOfMessages: maxMessages,
		WaitTimeSeconds:     int32(wait.Seconds()),

		// Without asking, the response carries no attributes at all. ApproximateReceiveCount is the one that
		// matters for a redrive policy: it is what SQS counts against maxReceiveCount.
		MessageSystemAttributeNames: []types.MessageSystemAttributeName{
			types.MessageSystemAttributeNameApproximateReceiveCount,
		},
	})
	if err != nil {
		return nil, err
	}

	return out.Messages, nil
}

// Delete removes a message, which is the only thing that does.
//
// # The receipt handle is not the message id
//
// It identifies one RECEIPT of one message and it changes every time the message is received. Storing a message
// id and trying to delete with it fails; storing a handle from an earlier receive and using it after the
// visibility timeout expired fails too. The handle is a short-lived token for the delete you are about to do.
func Delete(ctx context.Context, client *sqs.Client, url string, m types.Message) error {
	_, err := client.DeleteMessage(ctx, &sqs.DeleteMessageInput{
		QueueUrl:      aws.String(url),
		ReceiptHandle: m.ReceiptHandle,
	})

	return err
}

// ExtendVisibility gives a consumer more time on a message it is still working on.
//
// # The heartbeat a long handler needs
//
// A visibility timeout set for the worst case makes every failure slow to recover, because a crashed consumer's
// message stays hidden for that long. The alternative is a short timeout plus a goroutine that extends it while
// the work runs, which keeps the failure fast and the success safe.
//
// Setting it to 0 does the opposite and is the correct way to give a message back immediately when a handler
// decides it cannot do the work.
func ExtendVisibility(ctx context.Context, client *sqs.Client, url string, m types.Message, d time.Duration) error {
	_, err := client.ChangeMessageVisibility(ctx, &sqs.ChangeMessageVisibilityInput{
		QueueUrl:          aws.String(url),
		ReceiptHandle:     m.ReceiptHandle,
		VisibilityTimeout: int32(d.Seconds()),
	})

	return err
}

// QueueARN reads a queue's ARN, which a redrive policy needs.
func QueueARN(ctx context.Context, client *sqs.Client, url string) (string, error) {
	out, err := client.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
		QueueUrl:       aws.String(url),
		AttributeNames: []types.QueueAttributeName{types.QueueAttributeNameQueueArn},
	})
	if err != nil {
		return "", err
	}

	arn, ok := out.Attributes[string(types.QueueAttributeNameQueueArn)]
	if !ok {
		return "", errors.New("sqsdemo: the queue has no ARN attribute")
	}

	return arn, nil
}

// RedrivePolicy builds the JSON for a dead letter queue.
//
// # A JSON string inside a string map
//
// The attribute value is a JSON document, as a string, in a map of strings. There is no typed form in the SDK,
// so a malformed policy is a runtime error rather than a compile error, and maxReceiveCount has to be a JSON
// STRING and not a number. That last one is the usual mistake.
func RedrivePolicy(deadLetterARN string, maxReceiveCount int) string {
	return fmt.Sprintf(`{"deadLetterTargetArn":%q,"maxReceiveCount":"%d"}`, deadLetterARN, maxReceiveCount)
}

// ApproximateMessages reads the queue depth.
//
// Approximate is honest. The number is computed across the servers holding the queue and can be stale by
// seconds, so it is a monitoring signal and not a control-flow input. Code that loops "while depth > 0" will
// exit early or spin.
func ApproximateMessages(ctx context.Context, client *sqs.Client, url string) (visible, notVisible int, err error) {
	out, err := client.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
		QueueUrl: aws.String(url),
		AttributeNames: []types.QueueAttributeName{
			types.QueueAttributeNameApproximateNumberOfMessages,
			types.QueueAttributeNameApproximateNumberOfMessagesNotVisible,
		},
	})
	if err != nil {
		return 0, 0, err
	}

	visible, _ = strconv.Atoi(out.Attributes[string(types.QueueAttributeNameApproximateNumberOfMessages)])
	notVisible, _ = strconv.Atoi(out.Attributes[string(types.QueueAttributeNameApproximateNumberOfMessagesNotVisible)])

	return visible, notVisible, nil
}

// ReceiveCount reads how many times a message has been delivered.
func ReceiveCount(m types.Message) int {
	n, _ := strconv.Atoi(m.Attributes[string(types.MessageSystemAttributeNameApproximateReceiveCount)])

	return n
}

// APIErrorCode returns the AWS error code, or "".
func APIErrorCode(err error) string {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		return apiErr.ErrorCode()
	}

	return ""
}
