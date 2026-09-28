// Package streaming reads a Messages response as it is generated.
//
// # Why stream at all
//
// Latency you can feel. A 500-token answer takes several seconds to generate, and a user watching a spinner for
// all of it has a worse time than a user watching words appear, even though the total is identical. Nothing
// about the cost or the content changes.
//
// The second reason is less obvious and more important for a server: a streaming request holds a connection
// open, so a client disconnecting is something you find out about immediately rather than after paying for a
// response nobody is waiting for.
//
// # The event sequence
//
//	message_start          the message, with input tokens and output_tokens: 1
//	content_block_start    one per block
//	content_block_delta    many, each carrying a fragment
//	content_block_stop
//	message_delta          the stop reason, and the REAL output token count
//	message_stop
//
// The output count is in the final message_delta. message_start reports 1, because at that point nothing has
// been generated. Code reading usage from message_start reports every response as costing one output token,
// which is the commonest bug in streaming cost accounting.
package streaming

import (
	"context"
	"errors"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
)

// Result is what a completed stream produced.
type Result struct {
	Text string

	// Events counts the deltas, which is the number that says whether streaming happened at all. A response
	// delivered in one chunk is technically a stream and practically a non-stream.
	Deltas int

	StopReason anthropic.StopReason
	Usage      anthropic.Usage

	// FirstDeltaAfter is how many events arrived before the first text. It is a useful diagnostic: a large
	// number means the model is emitting something else first, which for a tool call it is.
	FirstDeltaAfter int
}

// Collect reads a stream to the end, calling onText for each fragment.
//
// # The accumulator is doing real work
//
// anthropic.Message.Accumulate applies each event to a Message, so at the end you have exactly the object a
// non-streaming call would have returned: every block assembled, the stop reason set, the usage filled in from
// the right event. Doing that by hand means reassembling partial JSON for tool inputs, which arrive as
// input_json_delta fragments that are not individually valid JSON.
//
// That last part is worth stating plainly. A tool call's arguments are streamed as a string, in pieces, and a
// piece is usually not parseable. Anything that tries to json.Unmarshal each delta fails on the first one.
func Collect(ctx context.Context, client anthropic.Client, params anthropic.MessageNewParams, onText func(string)) (*Result, error) {
	stream := client.Messages.NewStreaming(ctx, params)

	var (
		message anthropic.Message
		result  Result
		events  int
	)

	for stream.Next() {
		event := stream.Current()
		events++

		if err := message.Accumulate(event); err != nil {
			return nil, err
		}

		delta := event.AsContentBlockDelta()
		if delta.Delta.Text == "" {
			continue
		}

		if result.Deltas == 0 {
			result.FirstDeltaAfter = events - 1
		}

		result.Deltas++

		if onText != nil {
			onText(delta.Delta.Text)
		}
	}

	// # Checking Err() is not optional
	//
	// The loop ends on the last event AND on an error, and the two are indistinguishable from inside it. A
	// stream that dies mid-response returns a partial message and a nil error unless you ask. This is the
	// same shape as bufio.Scanner, and it is forgotten for the same reason.
	if err := stream.Err(); err != nil {
		return nil, err
	}

	result.Text = textOf(&message)
	result.StopReason = message.StopReason
	result.Usage = message.Usage

	return &result, nil
}

// ErrNoText is returned by FirstSentence when the stream produced none.
var ErrNoText = errors.New("streaming: the stream carried no text")

// FirstSentence stops reading as soon as a sentence is complete.
//
// # Stopping early does not stop the billing
//
// The model has already been asked for the tokens. Closing the stream stops the bytes arriving and does not
// refund anything already generated, so this is a latency optimisation and not a cost one. The way to spend
// less is max_tokens or a stop sequence, both of which the server enforces.
//
// Closing matters anyway: an abandoned stream is an open connection.
func FirstSentence(ctx context.Context, client anthropic.Client, params anthropic.MessageNewParams) (string, error) {
	stream := client.Messages.NewStreaming(ctx, params)
	defer func() { _ = stream.Close() }()

	var b strings.Builder

	for stream.Next() {
		delta := stream.Current().AsContentBlockDelta()
		if delta.Delta.Text == "" {
			continue
		}

		b.WriteString(delta.Delta.Text)

		if i := strings.IndexAny(b.String(), ".!?"); i >= 0 {
			return strings.TrimSpace(b.String()[:i+1]), nil
		}
	}

	if err := stream.Err(); err != nil {
		return "", err
	}

	if b.Len() == 0 {
		return "", ErrNoText
	}

	return strings.TrimSpace(b.String()), nil
}

// textOf joins a message's text blocks.
func textOf(msg *anthropic.Message) string {
	var b strings.Builder

	for _, block := range msg.Content {
		if text := block.AsText(); text.Text != "" {
			b.WriteString(text.Text)
		}
	}

	return b.String()
}

// EventNames returns the event types in order, for a test or a log.
//
// Useful because the sequence is a contract: a client that assumes content_block_delta arrives before
// content_block_start works by accident.
func EventNames(ctx context.Context, client anthropic.Client, params anthropic.MessageNewParams) ([]string, error) {
	stream := client.Messages.NewStreaming(ctx, params)

	var names []string

	for stream.Next() {
		names = append(names, stream.Current().Type)
	}

	if err := stream.Err(); err != nil {
		return names, err
	}

	return names, nil
}
