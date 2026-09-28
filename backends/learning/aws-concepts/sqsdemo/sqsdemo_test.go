package sqsdemo

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/alexvervloet/learn-go/backends/learning/aws-concepts/awstest"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/stretchr/testify/require"
)

// queue creates a standard queue for one test.
func queue(t *testing.T, attrs map[string]string) (*sqs.Client, string) {
	t.Helper()

	return queueWith(t, awstest.Config(t), attrs, "")
}

// queueWith creates a queue from a given config, so a test can bring a counter.
func queueWith(t *testing.T, cfg aws.Config, attrs map[string]string, suffix string) (*sqs.Client, string) {
	t.Helper()

	client := New(cfg)
	name := awstest.Name(t, "q") + suffix

	// A FIFO queue's NAME has to end in .fifo. The attribute alone is a validation error, which is one of the
	// friendlier bits of this API: it fails at creation rather than behaving like a standard queue.
	if attrs["FifoQueue"] == "true" {
		name += ".fifo"
	}

	ctx := context.Background()

	url, err := CreateQueue(ctx, client, name, attrs)
	require.NoError(t, err)

	t.Cleanup(func() {
		if err := DeleteQueue(context.Background(), client, url); err != nil {
			t.Errorf("clean up queue %s: %v", name, err)
		}
	})

	return client, url
}

// TestReceiveHidesRatherThanRemoves is the central fact about SQS.
func TestReceiveHidesRatherThanRemoves(t *testing.T) {
	// A two second timeout, so the test can wait it out without being slow.
	client, url := queue(t, map[string]string{"VisibilityTimeout": "2"})
	ctx := context.Background()

	_, err := Send(ctx, client, url, "work item")
	require.NoError(t, err)

	first, err := Receive(ctx, client, url, 1, 2*time.Second)
	require.NoError(t, err)
	require.Len(t, first, 1)
	require.Equal(t, 1, ReceiveCount(first[0]))

	// Still there, just invisible. The queue's own counters say so.
	visible, hidden, err := ApproximateMessages(ctx, client, url)
	require.NoError(t, err)
	require.Equal(t, 0, visible, "nothing is available to another consumer")
	require.Equal(t, 1, hidden, "but the message has not gone anywhere")

	// A second consumer polling right now gets nothing.
	none, err := Receive(ctx, client, url, 1, 0)
	require.NoError(t, err)
	require.Empty(t, none)

	// Wait out the timeout, and it comes back with the count incremented.
	time.Sleep(3 * time.Second)

	again, err := Receive(ctx, client, url, 1, 5*time.Second)
	require.NoError(t, err)
	require.Len(t, again, 1)
	require.Equal(t, 2, ReceiveCount(again[0]),
		"this is the at-least-once guarantee, and it is why handlers must be idempotent")

	// Delete is what actually removes it.
	require.NoError(t, Delete(ctx, client, url, again[0]))

	time.Sleep(1 * time.Second)

	gone, err := Receive(ctx, client, url, 1, 2*time.Second)
	require.NoError(t, err)
	require.Empty(t, gone)
}

// TestReceiptHandleChangesEveryReceive is the trap in the delete API.
func TestReceiptHandleChangesEveryReceive(t *testing.T) {
	client, url := queue(t, map[string]string{"VisibilityTimeout": "1"})
	ctx := context.Background()

	_, err := Send(ctx, client, url, "item")
	require.NoError(t, err)

	first, err := Receive(ctx, client, url, 1, 3*time.Second)
	require.NoError(t, err)
	require.Len(t, first, 1)

	time.Sleep(2 * time.Second)

	second, err := Receive(ctx, client, url, 1, 3*time.Second)
	require.NoError(t, err)
	require.Len(t, second, 1)

	require.Equal(t, *first[0].MessageId, *second[0].MessageId, "the same message")
	require.NotEqual(t, *first[0].ReceiptHandle, *second[0].ReceiptHandle,
		"a receipt handle identifies one receipt, not the message")
}

// TestVisibilityCanBeExtendedAndReturned covers the heartbeat and the early give-back.
func TestVisibilityCanBeExtendedAndReturned(t *testing.T) {
	client, url := queue(t, map[string]string{"VisibilityTimeout": "2"})
	ctx := context.Background()

	_, err := Send(ctx, client, url, "slow work")
	require.NoError(t, err)

	m, err := Receive(ctx, client, url, 1, 3*time.Second)
	require.NoError(t, err)
	require.Len(t, m, 1)

	// The handler is still working, so it buys more time. Without this the message would reappear at 2s and a
	// second consumer would start the same work.
	require.NoError(t, ExtendVisibility(ctx, client, url, m[0], 30*time.Second))

	time.Sleep(3 * time.Second)

	none, err := Receive(ctx, client, url, 1, 1*time.Second)
	require.NoError(t, err)
	require.Empty(t, none, "the extension held past the original timeout")

	// And the other direction: a handler that cannot do the work hands the message back at once, rather than
	// making the next consumer wait out the timeout.
	require.NoError(t, ExtendVisibility(ctx, client, url, m[0], 0))

	back, err := Receive(ctx, client, url, 1, 5*time.Second)
	require.NoError(t, err)
	require.Len(t, back, 1, "setting the visibility to zero releases it immediately")
}

// TestLongPollingCostsFewerRequests is the number that justifies the setting.
//
// Both loops wait the same five seconds for a message that arrives late. One does it in a single request and
// the other in dozens, and on a real account every one of those is billed.
func TestLongPollingCostsFewerRequests(t *testing.T) {
	cfg, counter := awstest.WithCounter(awstest.Config(t))

	client, url := queueWith(t, cfg, nil, "")
	ctx := context.Background()

	// A message that arrives 1 second into the wait.
	go func() {
		time.Sleep(1 * time.Second)

		_, _ = Send(ctx, client, url, "late arrival")
	}()

	counter.Reset()

	shortStart := time.Now()

	shortPolls := 0

	var found bool

	for time.Since(shortStart) < 2*time.Second {
		msgs, err := Receive(ctx, client, url, 1, 0)
		require.NoError(t, err)

		shortPolls++

		if len(msgs) > 0 {
			require.NoError(t, Delete(ctx, client, url, msgs[0]))

			found = true

			break
		}
	}

	shortElapsed := time.Since(shortStart)

	require.True(t, found, "the message arrived within the window")
	require.Greater(t, shortPolls, 3, "short polling means a spin: %d requests in %v", shortPolls, shortElapsed)

	// Now the same thing with long polling. One request covers the whole wait.
	go func() {
		time.Sleep(1 * time.Second)

		_, _ = Send(ctx, client, url, "late arrival")
	}()

	counter.Reset()

	longStart := time.Now()

	msgs, err := Receive(ctx, client, url, 1, 5*time.Second)
	require.NoError(t, err)

	longElapsed := time.Since(longStart)

	require.Len(t, msgs, 1)
	require.Equal(t, 1, counter.Count("ReceiveMessage"),
		"long polling holds the connection: one request, not %d: %v", shortPolls, counter.Operations())

	t.Logf("short polling: %d requests over %v; long polling: 1 request over %v", shortPolls, shortElapsed, longElapsed)
}

// TestBatchSendCountsRequests shows the ten-message limit.
func TestBatchSendCountsRequests(t *testing.T) {
	cfg, counter := awstest.WithCounter(awstest.Config(t))

	client, url := queueWith(t, cfg, nil, "")
	ctx := context.Background()

	bodies := make([]string, 25)
	for i := range bodies {
		bodies[i] = fmt.Sprintf("message %d", i)
	}

	counter.Reset()

	failed, err := SendBatch(ctx, client, url, bodies)
	require.NoError(t, err)
	require.Zero(t, failed, "the Failed slice is the part careless code drops")

	require.Equal(t, 3, counter.Count("SendMessageBatch"),
		"25 messages at 10 per request: %v", counter.Operations())
}

// TestDeadLetterQueueCatchesAPoisonMessage is the redrive policy doing its job.
func TestDeadLetterQueueCatchesAPoisonMessage(t *testing.T) {
	cfg := awstest.Config(t)

	// The dead letter queue is an ordinary queue. Nothing about it is special; it is a queue that another
	// queue's policy points at, and it can have its own redrive policy pointing somewhere else.
	dlqClient, dlqURL := queueWith(t, cfg, nil, "-dlq")

	ctx := context.Background()

	arn, err := QueueARN(ctx, dlqClient, dlqURL)
	require.NoError(t, err)

	client, url := queueWith(t, cfg, map[string]string{
		"VisibilityTimeout": "1",
		// After 3 receives without a delete, SQS moves the message here. maxReceiveCount is a JSON string,
		// which is the usual mistake in this document.
		"RedrivePolicy": RedrivePolicy(arn, 3),
	}, "-main")

	_, err = Send(ctx, client, url, "poison")
	require.NoError(t, err)

	// Receive it three times without deleting, the way a handler that keeps failing would.
	for attempt := 1; attempt <= 3; attempt++ {
		msgs, err := Receive(ctx, client, url, 1, 3*time.Second)
		require.NoError(t, err)
		require.Len(t, msgs, 1, "attempt %d", attempt)
		require.Equal(t, attempt, ReceiveCount(msgs[0]))

		time.Sleep(1200 * time.Millisecond)
	}

	// The fourth receive on the main queue finds nothing, because the move happens on delivery.
	main, err := Receive(ctx, client, url, 1, 3*time.Second)
	require.NoError(t, err)
	require.Empty(t, main, "the message hit maxReceiveCount and left")

	dead, err := Receive(ctx, dlqClient, dlqURL, 1, 10*time.Second)
	require.NoError(t, err)
	require.Len(t, dead, 1, "it is in the dead letter queue, body intact, ready to inspect or replay")
	require.Equal(t, "poison", *dead[0].Body)
}

// TestFIFOOrdersWithinAGroupAndNotAcross is the ordering guarantee, stated precisely.
func TestFIFOOrdersWithinAGroupAndNotAcross(t *testing.T) {
	client, url := queue(t, map[string]string{
		"FifoQueue":                 "true",
		"ContentBasedDeduplication": "false",
		"VisibilityTimeout":         "30",
	})

	ctx := context.Background()

	// Two groups interleaved on the wire.
	for i := range 5 {
		_, err := SendFIFO(ctx, client, url, fmt.Sprintf("a%d", i), "group-a", fmt.Sprintf("a%d", i))
		require.NoError(t, err)

		_, err = SendFIFO(ctx, client, url, fmt.Sprintf("b%d", i), "group-b", fmt.Sprintf("b%d", i))
		require.NoError(t, err)
	}

	var received []string

	for len(received) < 10 {
		msgs, err := Receive(ctx, client, url, 10, 5*time.Second)
		require.NoError(t, err)
		require.NotEmpty(t, msgs, "got %d of 10 so far", len(received))

		for _, m := range msgs {
			received = append(received, *m.Body)
			require.NoError(t, Delete(ctx, client, url, m))
		}
	}

	// The assertion is per group. Across groups the order is not defined, and asserting on it would be
	// asserting on an implementation detail that AWS is free to change.
	var a, b []string

	for _, body := range received {
		if body[0] == 'a' {
			a = append(a, body)
		} else {
			b = append(b, body)
		}
	}

	require.Equal(t, []string{"a0", "a1", "a2", "a3", "a4"}, a, "group-a arrived in order")
	require.Equal(t, []string{"b0", "b1", "b2", "b3", "b4"}, b, "group-b arrived in order")
}

// TestFIFODeduplicationDropsARetry is what makes a send idempotent.
func TestFIFODeduplicationDropsARetry(t *testing.T) {
	client, url := queue(t, map[string]string{
		"FifoQueue":         "true",
		"VisibilityTimeout": "30",
	})

	ctx := context.Background()

	// The same deduplication id twice, which is what a client retry after a timeout looks like.
	firstID, err := SendFIFO(ctx, client, url, "charge order 42", "orders", "order-42")
	require.NoError(t, err)

	secondID, err := SendFIFO(ctx, client, url, "charge order 42", "orders", "order-42")
	require.NoError(t, err, "the duplicate is accepted, not rejected")
	require.Equal(t, firstID, secondID, "SQS returns the ORIGINAL message id, so the caller cannot tell")

	msgs, err := Receive(ctx, client, url, 10, 5*time.Second)
	require.NoError(t, err)
	require.Len(t, msgs, 1, "one message was delivered, inside the five minute dedup window")

	require.NoError(t, Delete(ctx, client, url, msgs[0]))

	// A different id sends a second message with identical content, which is how you retry deliberately.
	_, err = SendFIFO(ctx, client, url, "charge order 42", "orders", "order-42-retry")
	require.NoError(t, err)

	again, err := Receive(ctx, client, url, 10, 5*time.Second)
	require.NoError(t, err)
	require.Len(t, again, 1)
}
