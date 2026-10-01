// Package snsdemo is SNS: one publish, many independent deliveries.
//
// # The one thing to carry away
//
// SNS does not store anything. A publish is a fan-out to whatever is subscribed AT THAT MOMENT, and a
// subscriber added a second later never sees it. That is the difference from a queue, and it is why the
// standard pattern is SNS in front of SQS: the topic does the fan-out, and each queue does the durability and
// the retries for its own consumer.
//
// Saying it the other way round: a topic is a routing table, a queue is a buffer. Putting a Lambda or an HTTP
// endpoint directly on a topic means a consumer that is down loses messages, minus whatever the delivery retry
// policy recovers.
//
// # Filter policies
//
// A subscription can carry a filter, evaluated by SNS before delivery. The consumer never sees the message and
// is never billed for it. That turns "one topic per event type" into "one topic, filtered per subscriber",
// which is the difference between a dozen topics and one.
//
// The catch is where the filter looks. By default it matches message ATTRIBUTES, not the body, so a publisher
// that puts everything in the JSON body has nothing to filter on. Switching the scope to MessageBody is
// possible and means the body has to be JSON.
package snsdemo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	snstypes "github.com/aws/aws-sdk-go-v2/service/sns/types"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/aws/smithy-go"
)

// New builds an SNS client.
func New(cfg aws.Config) *sns.Client {
	return sns.NewFromConfig(cfg)
}

// CreateTopic makes a topic and returns its ARN.
//
// CreateTopic is idempotent on the name: calling it twice returns the same ARN and changes nothing. That is
// unusual for AWS and it makes topic creation safe to put in startup code.
func CreateTopic(ctx context.Context, client *sns.Client, name string) (string, error) {
	out, err := client.CreateTopic(ctx, &sns.CreateTopicInput{Name: aws.String(name)})
	if err != nil {
		return "", err
	}

	return aws.ToString(out.TopicArn), nil
}

// DeleteTopic removes a topic and its subscriptions.
func DeleteTopic(ctx context.Context, client *sns.Client, arn string) error {
	_, err := client.DeleteTopic(ctx, &sns.DeleteTopicInput{TopicArn: aws.String(arn)})

	return err
}

// SubscribeQueue attaches an SQS queue to a topic.
//
// # RawMessageDelivery is the flag you want and is off by default
//
// Without it, SNS wraps the message in a JSON envelope carrying the topic ARN, a timestamp, a signature and the
// message as a STRING field. Every consumer then has to unwrap it, and a consumer written against a direct SQS
// send breaks when a topic is put in front.
//
// With it, the queue receives exactly the bytes that were published. The cost is that the message attributes
// move from the envelope onto the SQS message, which is usually what you wanted anyway.
func SubscribeQueue(ctx context.Context, client *sns.Client, topicARN, queueARN string, raw bool, filter map[string]any) (string, error) {
	attrs := map[string]string{
		"RawMessageDelivery": fmt.Sprintf("%t", raw),
	}

	if filter != nil {
		policy, err := json.Marshal(filter)
		if err != nil {
			return "", err
		}

		attrs["FilterPolicy"] = string(policy)
	}

	out, err := client.Subscribe(ctx, &sns.SubscribeInput{
		TopicArn: aws.String(topicARN),
		Protocol: aws.String("sqs"),
		Endpoint: aws.String(queueARN),
		// This changes the response, not the subscription. Without it, a subscription that still needs
		// confirming comes back with the literal ARN "pending confirmation", which cannot be used to
		// unsubscribe. Delivery does not depend on it: an SQS queue in the same account is confirmed
		// automatically either way. The attributes, separately, only apply if they are set here or with a
		// later SetSubscriptionAttributes call.
		ReturnSubscriptionArn: true,
		Attributes:            attrs,
	})
	if err != nil {
		return "", err
	}

	return aws.ToString(out.SubscriptionArn), nil
}

// Publish sends one message to a topic with string attributes.
//
// The attributes are what a filter policy matches on by default, so they are not decoration. A message with no
// attributes cannot be filtered except by a policy that explicitly asks for an absent key.
func Publish(ctx context.Context, client *sns.Client, topicARN, body string, attrs map[string]string) (string, error) {
	msgAttrs := make(map[string]snstypes.MessageAttributeValue, len(attrs))

	for k, v := range attrs {
		msgAttrs[k] = snstypes.MessageAttributeValue{
			DataType:    aws.String("String"),
			StringValue: aws.String(v),
		}
	}

	out, err := client.Publish(ctx, &sns.PublishInput{
		TopicArn:          aws.String(topicARN),
		Message:           aws.String(body),
		MessageAttributes: msgAttrs,
	})
	if err != nil {
		return "", err
	}

	return aws.ToString(out.MessageId), nil
}

// policyDocument is the IAM policy JSON, as types. A misspelt "Efect" in a map is a policy AWS rejects at
// runtime, or worse, accepts and ignores; as a struct field it does not compile.
type policyDocument struct {
	Version   string            `json:"Version"`
	Statement []policyStatement `json:"Statement"`
}

type policyStatement struct {
	Effect    string          `json:"Effect"`
	Principal policyPrincipal `json:"Principal"`
	Action    string          `json:"Action"`
	Resource  string          `json:"Resource"`

	// Condition stays a map: its keys are condition operators (ArnEquals, StringLike, ...) and their keys
	// are condition keys, both open-ended sets that a struct would have to enumerate.
	Condition map[string]map[string]string `json:"Condition,omitempty"`
}

type policyPrincipal struct {
	Service string `json:"Service"`
}

// AllowTopicToSendToQueue gives a topic permission to write to a queue.
//
// # The step everyone forgets
//
// Subscribing succeeds without it. Publishing succeeds without it. Nothing is delivered, and nothing tells you
// why: the message is simply gone. The permission lives on the QUEUE, as a resource policy, because the queue
// is the resource being written to.
//
// LocalStack's IAM is permissive enough that this is not strictly required there, which is exactly the kind of
// difference that makes a thing work locally and vanish in production. It is set here for that reason.
func AllowTopicToSendToQueue(ctx context.Context, client *sqs.Client, queueURL, queueARN, topicARN string) error {
	policy := policyDocument{
		Version: "2012-10-17",
		Statement: []policyStatement{{
			Effect:    "Allow",
			Principal: policyPrincipal{Service: "sns.amazonaws.com"},
			Action:    "sqs:SendMessage",
			Resource:  queueARN,
			// Without the condition, ANY topic could write to this queue. The condition is what scopes the
			// grant, and leaving it out is a real finding in a real audit.
			Condition: map[string]map[string]string{
				"ArnEquals": {"aws:SourceArn": topicARN},
			},
		}},
	}

	encoded, err := json.Marshal(policy)
	if err != nil {
		return err
	}

	_, err = client.SetQueueAttributes(ctx, &sqs.SetQueueAttributesInput{
		QueueUrl: aws.String(queueURL),
		Attributes: map[string]string{
			string(sqstypes.QueueAttributeNamePolicy): string(encoded),
		},
	})

	return err
}

// Envelope is the JSON SNS wraps a message in when raw delivery is off.
//
// The fields here are the ones worth knowing about. There are more, including Signature and SigningCertURL,
// which exist so an HTTP endpoint can verify the message really came from SNS. An SQS subscriber does not need
// them, because the delivery path is internal to AWS.
type Envelope struct {
	Type      string `json:"Type"`
	MessageID string `json:"MessageId"`
	TopicArn  string `json:"TopicArn"`
	Message   string `json:"Message"`
	Timestamp string `json:"Timestamp"`
}

// ParseEnvelope reads the SNS wrapper.
func ParseEnvelope(body string) (Envelope, error) {
	var e Envelope

	if err := json.Unmarshal([]byte(body), &e); err != nil {
		return Envelope{}, err
	}

	if e.Type == "" {
		return Envelope{}, errors.New("snsdemo: not an SNS envelope")
	}

	return e, nil
}

// APIErrorCode returns the AWS error code, or "".
func APIErrorCode(err error) string {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		return apiErr.ErrorCode()
	}

	return ""
}
