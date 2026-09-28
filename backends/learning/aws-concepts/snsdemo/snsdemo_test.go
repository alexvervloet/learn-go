package snsdemo

import (
	"context"
	"testing"
	"time"

	"github.com/alexvervloet/learn-go/backends/learning/aws-concepts/awstest"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/stretchr/testify/require"
)

// fanout is a topic with n queues attached, cleaned up at the end of the test.
type fanout struct {
	sns      *sns.Client
	sqs      *sqs.Client
	topicARN string
	queues   []string // URLs
}

// newFanout builds a topic and subscribes n queues to it.
//
// raw and filters are per-subscription, so one call can set up a topic whose subscribers disagree about both.
// label distinguishes two fanouts in ONE test. awstest.Name is derived from the test name, and CreateTopic is
// idempotent on the name, so calling newFanout twice without a label returns the SAME topic. The second call
// then tries to subscribe the same queue ARN to it with different attributes and SNS refuses with
// "Subscription already exists with different attributes", which is a confusing way to learn that two variables
// were pointing at one topic.
func newFanout(t *testing.T, cfg aws.Config, label string, n int, raw bool, filters []map[string]any) *fanout {
	t.Helper()

	f := &fanout{sns: New(cfg), sqs: sqs.NewFromConfig(cfg)}

	ctx := context.Background()

	arn, err := CreateTopic(ctx, f.sns, awstest.Name(t, "topic")+"-"+label)
	require.NoError(t, err)

	f.topicARN = arn

	t.Cleanup(func() { _ = DeleteTopic(context.Background(), f.sns, arn) })

	for i := range n {
		name := awstest.Name(t, "q") + "-" + label + "-" + string(rune('a'+i))

		out, err := f.sqs.CreateQueue(ctx, &sqs.CreateQueueInput{QueueName: aws.String(name)})
		require.NoError(t, err)

		url := aws.ToString(out.QueueUrl)
		f.queues = append(f.queues, url)

		t.Cleanup(func() {
			_, _ = f.sqs.DeleteQueue(context.Background(), &sqs.DeleteQueueInput{QueueUrl: aws.String(url)})
		})

		attrs, err := f.sqs.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
			QueueUrl:       aws.String(url),
			AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameQueueArn},
		})
		require.NoError(t, err)

		queueARN := attrs.Attributes[string(sqstypes.QueueAttributeNameQueueArn)]
		require.NotEmpty(t, queueARN)

		require.NoError(t, AllowTopicToSendToQueue(ctx, f.sqs, url, queueARN, arn))

		var filter map[string]any
		if i < len(filters) {
			filter = filters[i]
		}

		_, err = SubscribeQueue(ctx, f.sns, arn, queueARN, raw, filter)
		require.NoError(t, err)
	}

	return f
}

// drain reads everything currently on a queue, with one long poll.
func (f *fanout) drain(t *testing.T, i int) []string {
	t.Helper()

	var bodies []string

	ctx := context.Background()

	// One long poll of 2 seconds. Long enough for a fan-out that has already been published, short enough that
	// an empty queue does not stall the test.
	for {
		out, err := f.sqs.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
			QueueUrl:            aws.String(f.queues[i]),
			MaxNumberOfMessages: 10,
			WaitTimeSeconds:     2,
		})
		require.NoError(t, err)

		if len(out.Messages) == 0 {
			return bodies
		}

		for _, m := range out.Messages {
			bodies = append(bodies, aws.ToString(m.Body))

			_, err := f.sqs.DeleteMessage(ctx, &sqs.DeleteMessageInput{
				QueueUrl:      aws.String(f.queues[i]),
				ReceiptHandle: m.ReceiptHandle,
			})
			require.NoError(t, err)
		}
	}
}

// TestOnePublishReachesEverySubscriber is the fan-out.
func TestOnePublishReachesEverySubscriber(t *testing.T) {
	cfg, counter := awstest.WithCounter(awstest.Config(t))

	f := newFanout(t, cfg, "main", 3, true, nil)

	counter.Reset()

	_, err := Publish(context.Background(), f.sns, f.topicARN, "order placed", nil)
	require.NoError(t, err)

	// One request from the publisher's side. The three deliveries happen inside AWS, which is the whole point:
	// the publisher does not know how many subscribers there are and does not pay a round trip per subscriber.
	require.Equal(t, 1, counter.Total(), "one Publish, whatever the subscriber count: %v", counter.Operations())

	for i := range 3 {
		bodies := f.drain(t, i)
		require.Equal(t, []string{"order placed"}, bodies, "queue %d", i)
	}
}

// TestSubscribersAddedAfterAPublishMissIt is the "SNS stores nothing" claim.
//
// This is the reason a topic is not a queue and the reason you cannot replay from one. Whatever was published
// before a subscription existed is gone.
func TestSubscribersAddedAfterAPublishMissIt(t *testing.T) {
	cfg := awstest.Config(t)

	f := newFanout(t, cfg, "main", 1, true, nil)
	ctx := context.Background()

	_, err := Publish(ctx, f.sns, f.topicARN, "first", nil)
	require.NoError(t, err)

	// A second queue joins now.
	out, err := f.sqs.CreateQueue(ctx, &sqs.CreateQueueInput{QueueName: aws.String(awstest.Name(t, "q") + "-late")})
	require.NoError(t, err)

	lateURL := aws.ToString(out.QueueUrl)

	t.Cleanup(func() {
		_, _ = f.sqs.DeleteQueue(context.Background(), &sqs.DeleteQueueInput{QueueUrl: aws.String(lateURL)})
	})

	attrs, err := f.sqs.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
		QueueUrl:       aws.String(lateURL),
		AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameQueueArn},
	})
	require.NoError(t, err)

	lateARN := attrs.Attributes[string(sqstypes.QueueAttributeNameQueueArn)]

	require.NoError(t, AllowTopicToSendToQueue(ctx, f.sqs, lateURL, lateARN, f.topicARN))

	_, err = SubscribeQueue(ctx, f.sns, f.topicARN, lateARN, true, nil)
	require.NoError(t, err)

	_, err = Publish(ctx, f.sns, f.topicARN, "second", nil)
	require.NoError(t, err)

	f.queues = append(f.queues, lateURL)

	require.Equal(t, []string{"first", "second"}, f.drain(t, 0), "the original saw both")
	require.Equal(t, []string{"second"}, f.drain(t, 1), "the latecomer saw only what came after it subscribed")
}

// TestFilterPolicyDropsBeforeDelivery is the routing that saves a consumer the work.
func TestFilterPolicyDropsBeforeDelivery(t *testing.T) {
	cfg := awstest.Config(t)

	// Queue 0 wants paid orders only. Queue 1 wants everything from the EU. Queue 2 has no filter.
	f := newFanout(t, cfg, "main", 3, true, []map[string]any{
		{"status": []string{"paid"}},
		{"region": []string{"eu-west-1", "eu-central-1"}},
		nil,
	})

	ctx := context.Background()

	messages := []struct {
		body  string
		attrs map[string]string
	}{
		{"order-1", map[string]string{"status": "paid", "region": "us-east-1"}},
		{"order-2", map[string]string{"status": "pending", "region": "eu-west-1"}},
		{"order-3", map[string]string{"status": "paid", "region": "eu-central-1"}},
		{"order-4", map[string]string{"status": "cancelled", "region": "ap-south-1"}},
	}

	for _, m := range messages {
		_, err := Publish(ctx, f.sns, f.topicARN, m.body, m.attrs)
		require.NoError(t, err)
	}

	require.ElementsMatch(t, []string{"order-1", "order-3"}, f.drain(t, 0), "status=paid")
	require.ElementsMatch(t, []string{"order-2", "order-3"}, f.drain(t, 1), "region in the EU list")
	require.ElementsMatch(t, []string{"order-1", "order-2", "order-3", "order-4"}, f.drain(t, 2), "no filter")
}

// TestFilterMatchesAttributesNotTheBody is the trap in filter policies.
//
// A publisher that puts its fields in the JSON body and a subscriber that filters on them agree on the field
// names and still match nothing, because the default filter scope never looks at the body.
func TestFilterMatchesAttributesNotTheBody(t *testing.T) {
	cfg := awstest.Config(t)

	f := newFanout(t, cfg, "main", 1, true, []map[string]any{
		{"status": []string{"paid"}},
	})

	ctx := context.Background()

	// status is in the body. There are no message attributes at all.
	_, err := Publish(ctx, f.sns, f.topicARN, `{"status":"paid","id":42}`, nil)
	require.NoError(t, err)

	require.Empty(t, f.drain(t, 0),
		"the filter looks at message attributes; a body that happens to contain the field is not consulted")

	// The same value, as an attribute, is delivered.
	_, err = Publish(ctx, f.sns, f.topicARN, `{"status":"paid","id":42}`, map[string]string{"status": "paid"})
	require.NoError(t, err)

	require.Len(t, f.drain(t, 0), 1)
}

// TestRawDeliveryChangesWhatTheConsumerSees is the flag that breaks a consumer when it is wrong.
func TestRawDeliveryChangesWhatTheConsumerSees(t *testing.T) {
	cfg := awstest.Config(t)

	wrapped := newFanout(t, cfg, "wrapped", 1, false, nil)
	ctx := context.Background()

	_, err := Publish(ctx, wrapped.sns, wrapped.topicARN, "the payload", nil)
	require.NoError(t, err)

	bodies := wrapped.drain(t, 0)
	require.Len(t, bodies, 1)

	// Without raw delivery, what arrives is JSON the consumer has to unwrap.
	require.NotEqual(t, "the payload", bodies[0])

	env, err := ParseEnvelope(bodies[0])
	require.NoError(t, err)
	require.Equal(t, "Notification", env.Type)
	require.Equal(t, "the payload", env.Message, "the real payload is a string field inside the envelope")
	require.Equal(t, wrapped.topicARN, env.TopicArn)
	require.NotEmpty(t, env.MessageID)

	// With raw delivery the consumer sees the bytes that were published, and a consumer written for a direct
	// SQS send keeps working when a topic is put in front of the queue.
	raw := newFanout(t, cfg, "raw", 1, true, nil)

	_, err = Publish(ctx, raw.sns, raw.topicARN, "the payload", nil)
	require.NoError(t, err)

	rawBodies := raw.drain(t, 0)
	require.Equal(t, []string{"the payload"}, rawBodies)

	_, err = ParseEnvelope(rawBodies[0])
	require.Error(t, err, "there is no envelope to parse")
}

// TestCreateTopicIsIdempotent is the one place AWS makes creation safe to repeat.
func TestCreateTopicIsIdempotent(t *testing.T) {
	client := New(awstest.Config(t))
	ctx := context.Background()

	name := awstest.Name(t, "topic")

	first, err := CreateTopic(ctx, client, name)
	require.NoError(t, err)

	t.Cleanup(func() { _ = DeleteTopic(context.Background(), client, first) })

	second, err := CreateTopic(ctx, client, name)
	require.NoError(t, err)
	require.Equal(t, first, second, "same name, same ARN, no error, so this is safe in startup code")
}

// TestPublishToAMissingTopicIsTyped pins the error contract.
func TestPublishToAMissingTopicIsTyped(t *testing.T) {
	client := New(awstest.Config(t))

	// A well-formed ARN for a topic that does not exist. A malformed one gives a different code, which is the
	// distinction worth having: InvalidParameter means you built the string wrong, NotFound means you looked
	// in the wrong account or region.
	_, err := Publish(context.Background(), client,
		"arn:aws:sns:"+awstest.Region+":000000000000:no-such-topic-"+time.Now().Format("150405"),
		"body", nil)

	require.Error(t, err)
	require.Equal(t, "NotFound", APIErrorCode(err))
}
