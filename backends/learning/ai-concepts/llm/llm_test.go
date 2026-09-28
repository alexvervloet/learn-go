package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/alexvervloet/learn-go/backends/learning/ai-concepts/fake"
	"github.com/anthropics/anthropic-sdk-go"
	"github.com/stretchr/testify/require"
)

// client builds a client pointed at a fake server.
func client(t *testing.T, responses ...fake.Response) (anthropic.Client, *fake.Server) {
	t.Helper()

	srv := fake.New(t, responses...)

	c, err := New(Config{
		APIKey:  "test-key",
		BaseURL: srv.URL,
		Timeout: 10 * time.Second,
	})
	require.NoError(t, err)

	return c, srv
}

// TestNewRefusesWithoutAKey is the guard that stops a confusing 401.
func TestNewRefusesWithoutAKey(t *testing.T) {
	_, err := New(Config{})
	require.ErrorIs(t, err, ErrNoAPIKey)
}

// TestRequestShape asserts on the JSON that actually goes out.
//
// This is the test the whole fake exists for. A reader can see the request without a proxy, and it fails if the
// SDK changes how it marshals a system prompt, which it has done before.
func TestRequestShape(t *testing.T) {
	c, srv := client(t, fake.TextMessage("4", 12, 1))

	text, usage, err := Ask(context.Background(), c, "Answer with a single number.", "What is 2+2?", 64)
	require.NoError(t, err)
	require.Equal(t, "4", text)
	require.Equal(t, int64(12), usage.InputTokens)

	reqs := srv.Requests()
	require.Len(t, reqs, 1)

	req := reqs[0]
	require.Equal(t, "claude-haiku-4-5-20251001", req.Model, "the dated model id, not the alias")
	require.Equal(t, int64(64), req.MaxTokens, "max_tokens is required, and it is a cap rather than a target")
	require.Len(t, req.Messages, 1)
	require.False(t, req.Stream)

	// The system prompt is its own field. It is NOT a message with role "system", which is the shape every
	// other provider uses and the first thing people get wrong here.
	require.NotEmpty(t, req.System)

	var system []map[string]any
	require.NoError(t, json.Unmarshal(req.System, &system))
	require.Equal(t, "Answer with a single number.", system[0]["text"])

	// And the headers the SDK sets without being asked.
	require.Equal(t, "test-key", req.Headers.Get("X-Api-Key"))
	require.NotEmpty(t, req.Headers.Get("Anthropic-Version"), "the API is versioned by header, not by URL")
}

// TestTheAPIIsStateless is the fact that shapes every design on top of it.
func TestTheAPIIsStateless(t *testing.T) {
	c, srv := client(t,
		fake.TextMessage("Hello.", 10, 3),
		fake.TextMessage("You said hello.", 25, 5),
	)

	conv := &Conversation{System: "Be brief."}
	conv.User("Say hello.")

	ctx := context.Background()

	_, err := conv.Send(ctx, c, DefaultModel, 64)
	require.NoError(t, err)

	conv.User("What did I just say?")

	_, err = conv.Send(ctx, c, DefaultModel, 64)
	require.NoError(t, err)

	reqs := srv.Requests()
	require.Len(t, reqs, 2)

	require.Len(t, reqs[0].Messages, 1, "first turn: one message")
	require.Len(t, reqs[1].Messages, 3,
		"second turn: the ENTIRE history goes again, because nothing on the server remembers it")

	// Which is why input tokens grow. On a real call the second request's input count is the whole transcript.
	require.Greater(t, len(reqs[1].Raw), len(reqs[0].Raw))
	t.Logf("request bodies: %d bytes then %d bytes", len(reqs[0].Raw), len(reqs[1].Raw))
}

// TestRetriesA429 shows the SDK's own retrying, counted.
func TestRetriesA429(t *testing.T) {
	srv := fake.New(t, fake.RateLimited(0))

	c, err := New(Config{
		APIKey:     "test-key",
		BaseURL:    srv.URL,
		MaxRetries: 3,
		Timeout:    10 * time.Second,
	})
	require.NoError(t, err)

	_, _, err = Ask(context.Background(), c, "", "hello", 16)
	require.Error(t, err)

	// One original plus three retries. The count is the assertion; the backoff between them is not, because
	// it is timing.
	require.Equal(t, 4, srv.Count(), "MaxRetries is retries, not total attempts")

	require.Equal(t, http.StatusTooManyRequests, StatusCode(err))
	require.True(t, IsRetryable(err))
	require.Contains(t, Describe(err), "retryable=true")
}

// TestDoesNotRetryA400 is the other half, and the more important one.
//
// A malformed request retried three times is a malformed request three times. The SDK knows this; a hand-rolled
// retry wrapper usually does not.
func TestDoesNotRetryA400(t *testing.T) {
	srv := fake.New(t, fake.ErrorResponse(http.StatusBadRequest, "invalid_request_error", "max_tokens is required"))

	c, err := New(Config{APIKey: "k", BaseURL: srv.URL, MaxRetries: 3, Timeout: 5 * time.Second})
	require.NoError(t, err)

	_, _, err = Ask(context.Background(), c, "", "hello", 16)
	require.Error(t, err)

	require.Equal(t, 1, srv.Count(), "a 400 is terminal: your request is wrong and will stay wrong")
	require.False(t, IsRetryable(err))
	require.Equal(t, http.StatusBadRequest, StatusCode(err))
}

// TestOverloadedIsRetryable covers the status that is not in net/http.
func TestOverloadedIsRetryable(t *testing.T) {
	srv := fake.New(t, fake.ErrorResponse(529, "overloaded_error", "overloaded"))

	c, err := New(Config{APIKey: "k", BaseURL: srv.URL, MaxRetries: 1, Timeout: 5 * time.Second})
	require.NoError(t, err)

	_, _, err = Ask(context.Background(), c, "", "hello", 16)
	require.Error(t, err)
	require.Equal(t, 529, StatusCode(err))
	require.True(t, IsRetryable(err), "529 means the service is busy, not that the request is wrong")
}

// TestContextCancellationStopsTheRequest is the thing every HTTP client in this repo asserts.
func TestContextCancellationStopsTheRequest(t *testing.T) {
	c, _ := client(t, fake.TextMessage("hi", 1, 1))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, err := Ask(ctx, c, "", "hello", 16)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 0, StatusCode(err), "a cancelled request has no HTTP status because it never completed")
}

// TestTextOfIgnoresNonTextBlocks is why a response is not a string.
func TestTextOfIgnoresNonTextBlocks(t *testing.T) {
	c, _ := client(t, fake.ToolUseMessage("toolu_1", "get_weather", `{"city":"Lisbon"}`, 20, 8))

	msg, err := c.Messages.New(context.Background(), anthropic.MessageNewParams{
		Model:     DefaultModel,
		MaxTokens: 64,
		Messages:  []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock("weather?"))},
	})
	require.NoError(t, err)

	require.Empty(t, TextOf(msg), "there is no text block, only a tool_use one")
	require.Equal(t, anthropic.StopReasonToolUse, msg.StopReason,
		"the stop reason is the signal; treating the response as a string would have dropped the whole call")
}

// ---------------------------------------------------------------------------
// The live test. One call, to the cheapest model, only with a key present.
// ---------------------------------------------------------------------------

// TestLiveRoundTrip talks to the real API.
//
// # Why there is only one of these
//
// It costs money and it is not deterministic. The assertion is therefore about the SHAPE of the reply, not its
// content: a stop reason of end_turn, a non-zero input count, and an output count at or under the cap. Those
// hold for any model on any day. "The answer is 4" does not, quite, and a test that fails because a model
// phrased something differently is a test nobody keeps.
//
// max_tokens is 16, so the worst case is a handful of output tokens on the cheapest model.
func TestLiveRoundTrip(t *testing.T) {
	key, err := APIKeyFromEnv()
	if err != nil {
		t.Skip("no ANTHROPIC_API_KEY; this is the only test in the module that costs anything")
	}

	c, err := New(Config{APIKey: key, Timeout: 30 * time.Second, MaxRetries: 2})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	msg, err := c.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     DefaultModel,
		MaxTokens: 16,
		System:    []anthropic.TextBlockParam{{Text: "Reply with a single digit and nothing else."}},
		Messages:  []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock("What is 2+2?"))},
	})
	require.NoError(t, err)

	require.Equal(t, anthropic.StopReasonEndTurn, msg.StopReason)
	require.Positive(t, msg.Usage.InputTokens)
	require.Positive(t, msg.Usage.OutputTokens)
	require.LessOrEqual(t, msg.Usage.OutputTokens, int64(16), "max_tokens is a hard cap")
	require.NotEmpty(t, TextOf(msg))

	t.Logf("model said %q using %d in / %d out", TextOf(msg), msg.Usage.InputTokens, msg.Usage.OutputTokens)
}
