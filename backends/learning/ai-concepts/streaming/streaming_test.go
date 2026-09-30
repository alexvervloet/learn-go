package streaming

import (
	"context"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/alexvervloet/learn-go/backends/learning/ai-concepts/fake"
	"github.com/alexvervloet/learn-go/backends/learning/ai-concepts/llm"
	"github.com/anthropics/anthropic-sdk-go"
	"github.com/stretchr/testify/require"
)

func client(t *testing.T, responses ...fake.Response) (anthropic.Client, *fake.Server) {
	t.Helper()

	srv := fake.New(t, responses...)

	c, err := llm.New(llm.Config{APIKey: "k", BaseURL: srv.URL, Timeout: 10 * time.Second})
	require.NoError(t, err)

	return c, srv
}

func params(prompt string) anthropic.MessageNewParams {
	return anthropic.MessageNewParams{
		Model:     llm.DefaultModel,
		MaxTokens: 256,
		Messages:  []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock(prompt))},
	}
}

// TestCollectAssemblesTheFragments is the basic claim.
func TestCollectAssemblesTheFragments(t *testing.T) {
	chunks := []string{"The ", "quick ", "brown ", "fox."}

	c, srv := client(t, fake.StreamResponse(chunks, 14, 9))

	var seen []string

	res, err := Collect(context.Background(), c, params("say a sentence"), func(s string) {
		seen = append(seen, s)
	})
	require.NoError(t, err)

	require.Equal(t, "The quick brown fox.", res.Text)
	require.Equal(t, chunks, seen, "the callback sees each fragment as it arrives")
	require.Equal(t, 4, res.Deltas)

	// And the request said so.
	require.True(t, srv.Requests()[0].Stream, "streaming is a field on the request, not a different endpoint")
}

// TestUsageComesFromTheLastEvent is the cost-accounting bug, pinned.
func TestUsageComesFromTheLastEvent(t *testing.T) {
	c, _ := client(t, fake.StreamResponse([]string{"a", "b", "c"}, 137, 42))

	res, err := Collect(context.Background(), c, params("count"), nil)
	require.NoError(t, err)

	require.Equal(t, int64(137), res.Usage.InputTokens, "input is known at message_start")
	require.Equal(t, int64(42), res.Usage.OutputTokens,
		"output is in the FINAL message_delta; message_start says 1 because nothing has been generated yet")

	require.Equal(t, anthropic.StopReasonEndTurn, res.StopReason)
}

// TestTheEventSequenceIsAContract asserts the order.
func TestTheEventSequenceIsAContract(t *testing.T) {
	c, _ := client(t, fake.StreamResponse([]string{"x", "y"}, 10, 2))

	names, err := EventNames(context.Background(), c, params("hi"))
	require.NoError(t, err)

	require.Equal(t, []string{
		"message_start",
		"content_block_start",
		"content_block_delta",
		"content_block_delta",
		"content_block_stop",
		"message_delta",
		"message_stop",
	}, names)
}

// TestAccumulateProducesTheNonStreamingShape is why the accumulator is worth using.
//
// At the end you hold exactly what a non-streaming call would have returned. That is what lets one code path
// handle both, which matters the moment tool calls are involved, because a tool input arrives as fragments of
// JSON that are not individually parseable.
func TestAccumulateProducesTheNonStreamingShape(t *testing.T) {
	c, _ := client(t, fake.StreamResponse([]string{"Hello", ", ", "world."}, 8, 4))

	res, err := Collect(context.Background(), c, params("greet"), nil)
	require.NoError(t, err)

	require.Equal(t, "Hello, world.", res.Text)
	require.Equal(t, anthropic.StopReasonEndTurn, res.StopReason)
	require.Equal(t, int64(4), res.Usage.OutputTokens)
	require.Equal(t, 2, res.FirstDeltaAfter,
		"the first text delta arrives after message_start and content_block_start, never before")
}

// TestFirstSentenceStopsEarly covers the abandon-the-stream case.
func TestFirstSentenceStopsEarly(t *testing.T) {
	c, _ := client(t, fake.StreamResponse(
		[]string{"Yes", ", ", "that ", "is ", "right", ". ", "Now ", "here ", "is ", "more."}, 20, 12))

	got, err := FirstSentence(context.Background(), c, params("answer"))
	require.NoError(t, err)
	require.Equal(t, "Yes, that is right.", got)
}

// TestStreamErrorIsNotSilent is the bufio.Scanner shape.
//
// The loop ends on the last event and on an error, and from inside it the two look the same. A response that
// is not a stream at all ends the loop immediately with a nil message and an error that only Err() reports.
func TestStreamErrorIsNotSilent(t *testing.T) {
	c, _ := client(t, fake.ErrorResponse(500, "api_error", "something broke"))

	_, err := Collect(context.Background(), c, params("hi"), nil)
	require.Error(t, err, "without the Err() check this would return an empty result and no error")
	require.Equal(t, 500, llm.StatusCode(err))
}

// TestContextCancellationEndsTheStream is the other way a stream stops.
func TestContextCancellationEndsTheStream(t *testing.T) {
	c, _ := client(t, fake.StreamResponse([]string{"a", "b"}, 5, 2))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := Collect(ctx, c, params("hi"), nil)
	require.ErrorIs(t, err, context.Canceled)
}

// closeCounter wraps every response body so a test can see whether it was closed.
type closeCounter struct {
	mu     sync.Mutex
	opened int
	closed int
}

func (c *closeCounter) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := http.DefaultTransport.RoundTrip(r)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	c.opened++
	c.mu.Unlock()

	resp.Body = &countedBody{ReadCloser: resp.Body, c: c}

	return resp, nil
}

type countedBody struct {
	io.ReadCloser
	c *closeCounter
}

func (b *countedBody) Close() error {
	b.c.mu.Lock()
	b.c.closed++
	b.c.mu.Unlock()

	return b.ReadCloser.Close()
}

// TestEveryReaderClosesTheStream is the connection leak: reading to the end is not the same as closing.
func TestEveryReaderClosesTheStream(t *testing.T) {
	readers := map[string]func(context.Context, anthropic.Client, anthropic.MessageNewParams) error{
		"Collect": func(ctx context.Context, c anthropic.Client, p anthropic.MessageNewParams) error {
			_, err := Collect(ctx, c, p, nil)
			return err
		},
		"EventNames": func(ctx context.Context, c anthropic.Client, p anthropic.MessageNewParams) error {
			_, err := EventNames(ctx, c, p)
			return err
		},
		"FirstSentence": func(ctx context.Context, c anthropic.Client, p anthropic.MessageNewParams) error {
			_, err := FirstSentence(ctx, c, p)
			return err
		},
	}

	for name, read := range readers {
		t.Run(name, func(t *testing.T) {
			srv := fake.New(t, fake.StreamResponse([]string{"One. ", "Two."}, 10, 4))
			counter := &closeCounter{}

			c, err := llm.New(llm.Config{
				APIKey: "k", BaseURL: srv.URL, Timeout: 10 * time.Second,
				HTTPClient: &http.Client{Transport: counter},
			})
			require.NoError(t, err)

			require.NoError(t, read(context.Background(), c, params("two sentences")))

			counter.mu.Lock()
			defer counter.mu.Unlock()

			require.Equal(t, 1, counter.opened)
			require.Equal(t, 1, counter.closed, "an unclosed body is a connection that is never reused or released")
		})
	}
}
