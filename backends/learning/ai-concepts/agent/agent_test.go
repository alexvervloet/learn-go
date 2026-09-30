package agent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/alexvervloet/learn-go/backends/learning/ai-concepts/fake"
	"github.com/alexvervloet/learn-go/backends/learning/ai-concepts/llm"
	"github.com/anthropics/anthropic-sdk-go"
	"github.com/stretchr/testify/require"
)

// weather is a tool that always succeeds.
func weather(calls *int) Tool {
	return Tool{
		Name: "get_weather",
		Description: "Returns the current temperature in Celsius for a city. Use for conditions right now, " +
			"not for a forecast.",
		Schema: ObjectSchema(map[string]any{
			"city": map[string]any{
				"type":        "string",
				"description": "The city name, for example \"Lisbon\".",
			},
		}, "city"),
		Run: func(_ context.Context, input json.RawMessage) (string, error) {
			*calls++

			var args struct {
				City string `json:"city"`
			}

			if err := json.Unmarshal(input, &args); err != nil {
				return "", err
			}

			if args.City == "" {
				return "", errors.New("city is required")
			}

			return "18C and clear in " + args.City, nil
		},
	}
}

// client builds a client against a fake server.
func client(t *testing.T, responses ...fake.Response) (anthropic.Client, *fake.Server) {
	t.Helper()

	srv := fake.New(t, responses...)

	c, err := llm.New(llm.Config{APIKey: "k", BaseURL: srv.URL, Timeout: 10 * time.Second})
	require.NoError(t, err)

	return c, srv
}

func opts() Options {
	return Options{Model: llm.DefaultModel, MaxTokens: 256, MaxTurns: 5}
}

// TestDuplicateToolNamesAreRejected is the check that has to happen at construction.
func TestDuplicateToolNamesAreRejected(t *testing.T) {
	var n int

	_, err := NewRegistry(weather(&n), weather(&n))
	require.Error(t, err)
	require.Contains(t, err.Error(), "get_weather")
}

// TestToolOrderIsStable is the prompt cache's precondition: the tool list is the start of the prefix.
func TestToolOrderIsStable(t *testing.T) {
	names := []string{"delta", "alpha", "echo", "charlie", "bravo", "foxtrot", "golf", "hotel"}

	var tools []Tool

	for _, name := range names {
		tools = append(tools, Tool{Name: name, Schema: ObjectSchema(nil)})
	}

	registry, err := NewRegistry(tools...)
	require.NoError(t, err)

	first, err := json.Marshal(registry.Params())
	require.NoError(t, err)

	// Map iteration order is randomised per range, so eight tools in 20 tries would almost surely differ once.
	for range 20 {
		again, err := json.Marshal(registry.Params())
		require.NoError(t, err)
		require.Equal(t, string(first), string(again), "a reordered tool list is a cache miss on every turn")
	}
}

// TestTheLoopRunsTheToolAndSendsTheResultBack is the protocol, end to end.
func TestTheLoopRunsTheToolAndSendsTheResultBack(t *testing.T) {
	var toolCalls int

	registry, err := NewRegistry(weather(&toolCalls))
	require.NoError(t, err)

	c, srv := client(t,
		fake.ToolUseMessage("toolu_01", "get_weather", `{"city":"Lisbon"}`, 120, 30),
		fake.TextMessage("It is 18C and clear in Lisbon.", 180, 12),
	)

	res, err := Run(context.Background(), c, registry, "What is the weather in Lisbon?", opts())
	require.NoError(t, err)

	require.Equal(t, 1, toolCalls)
	require.Equal(t, 2, res.Turns, "one turn to ask for the tool, one to answer")
	require.Equal(t, "It is 18C and clear in Lisbon.", res.Text)

	require.Len(t, res.Calls, 1)
	require.Equal(t, "get_weather", res.Calls[0].Name)
	require.Equal(t, "18C and clear in Lisbon", res.Calls[0].Out)
	require.NoError(t, res.Calls[0].Err)

	// Usage is per request, so a loop's cost is the sum. It grows faster than the turn count because every
	// turn re-sends the transcript.
	require.Equal(t, int64(300), res.Usage.InputTokens)
	require.Equal(t, int64(42), res.Usage.OutputTokens)

	// Now the request the loop built. This is the part worth reading.
	reqs := srv.Requests()
	require.Len(t, reqs, 2)

	require.Len(t, reqs[0].Messages, 1, "first request: just the question")
	require.Len(t, reqs[0].Tools, 1, "with the tool list")

	require.Len(t, reqs[1].Messages, 3, "second request: question, assistant tool_use, user tool_result")

	// The tool result is in a USER message. It feels like it belongs to the assistant, because it answers
	// something the assistant asked for, and the API rejects it there.
	var third struct {
		Role    string `json:"role"`
		Content []struct {
			Type      string `json:"type"`
			ToolUseID string `json:"tool_use_id"`
			IsError   bool   `json:"is_error"`
		} `json:"content"`
	}

	require.NoError(t, json.Unmarshal(reqs[1].Messages[2], &third))
	require.Equal(t, "user", third.Role)
	require.Len(t, third.Content, 1)
	require.Equal(t, "tool_result", third.Content[0].Type)
	require.Equal(t, "toolu_01", third.Content[0].ToolUseID,
		"the id ties the result to the request; there is no other correspondence")
	require.False(t, third.Content[0].IsError)

	// And the assistant turn kept its tool_use block. Rebuilding it from the text would have dropped it and
	// the next request would be a 400.
	var assistant struct {
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Name string `json:"name"`
		} `json:"content"`
	}

	require.NoError(t, json.Unmarshal(reqs[1].Messages[1], &assistant))
	require.Equal(t, "assistant", assistant.Role)
	require.Equal(t, "tool_use", assistant.Content[0].Type)
	require.Equal(t, "get_weather", assistant.Content[0].Name)
}

// TestAFailingToolReportsBackRatherThanStopping is how an agent recovers.
func TestAFailingToolReportsBackRatherThanStopping(t *testing.T) {
	failing := Tool{
		Name:        "lookup",
		Description: "Looks something up.",
		Schema:      ObjectSchema(map[string]any{"q": map[string]any{"type": "string"}}, "q"),
		Run: func(context.Context, json.RawMessage) (string, error) {
			return "", errors.New("the index is offline")
		},
	}

	registry, err := NewRegistry(failing)
	require.NoError(t, err)

	c, srv := client(t,
		fake.ToolUseMessage("toolu_bad", "lookup", `{"q":"anything"}`, 100, 20),
		fake.TextMessage("I could not look that up: the index is offline.", 150, 14),
	)

	res, err := Run(context.Background(), c, registry, "look it up", opts())
	require.NoError(t, err, "a tool failing is not the loop failing")

	require.Len(t, res.Calls, 1)
	require.Error(t, res.Calls[0].Err)
	require.Contains(t, res.Text, "offline")

	// The error went back as a tool_result with is_error, so the model could say something useful about it.
	var third struct {
		Content []struct {
			IsError bool   `json:"is_error"`
			Content any    `json:"content"`
			Type    string `json:"type"`
		} `json:"content"`
	}

	require.NoError(t, json.Unmarshal(srv.Requests()[1].Messages[2], &third))
	require.True(t, third.Content[0].IsError,
		"is_error tells the model this is a failure rather than a result that happens to read like one")
}

// TestAnUnknownToolNameIsReportedToTheModel covers a registry and transcript that disagree.
func TestAnUnknownToolNameIsReportedToTheModel(t *testing.T) {
	var n int

	registry, err := NewRegistry(weather(&n))
	require.NoError(t, err)

	c, _ := client(t,
		fake.ToolUseMessage("toolu_x", "get_forecast", `{"city":"Lisbon"}`, 100, 20),
		fake.TextMessage("I do not have a forecast tool.", 140, 10),
	)

	res, err := Run(context.Background(), c, registry, "forecast?", opts())
	require.NoError(t, err)
	require.Zero(t, n, "no registered tool ran")

	require.Len(t, res.Calls, 1)
	require.ErrorContains(t, res.Calls[0].Err, `no tool named "get_forecast"`)
}

// TestEveryToolUseBlockNeedsAResult is the 400 waiting to happen.
func TestEveryToolUseBlockNeedsAResult(t *testing.T) {
	var n int

	registry, err := NewRegistry(weather(&n))
	require.NoError(t, err)

	// One response, two tool calls in it.
	two := fake.Response{Body: `{
  "id": "msg_two",
  "type": "message",
  "role": "assistant",
  "model": "claude-haiku-4-5-20251001",
  "content": [
    {"type": "tool_use", "id": "toolu_a", "name": "get_weather", "input": {"city": "Lisbon"}},
    {"type": "tool_use", "id": "toolu_b", "name": "get_weather", "input": {"city": "Porto"}}
  ],
  "stop_reason": "tool_use",
  "stop_sequence": null,
  "usage": {"input_tokens": 100, "output_tokens": 40}
}`}

	c, srv := client(t, two, fake.TextMessage("Lisbon 18C, Porto 18C.", 200, 12))

	res, err := Run(context.Background(), c, registry, "weather in Lisbon and Porto?", opts())
	require.NoError(t, err)

	require.Equal(t, 2, n, "a model can ask for several tools in ONE response")
	require.Len(t, res.Calls, 2)

	var third struct {
		Content []struct {
			ToolUseID string `json:"tool_use_id"`
		} `json:"content"`
	}

	require.NoError(t, json.Unmarshal(srv.Requests()[1].Messages[2], &third))
	require.Len(t, third.Content, 2, "replying to one of two tool_use blocks is a 400")
	require.Equal(t, "toolu_a", third.Content[0].ToolUseID)
	require.Equal(t, "toolu_b", third.Content[1].ToolUseID)
}

// TestTheLoopIsBounded is the bill control.
func TestTheLoopIsBounded(t *testing.T) {
	var n int

	registry, err := NewRegistry(weather(&n))
	require.NoError(t, err)

	// One queued response, which the fake repeats: a model stuck asking for the same tool forever.
	c, srv := client(t, fake.ToolUseMessage("toolu_loop", "get_weather", `{"city":"Lisbon"}`, 100, 20))

	o := opts()
	o.MaxTurns = 3

	res, err := Run(context.Background(), c, registry, "weather?", o)
	require.ErrorIs(t, err, ErrTooManyTurns)

	require.Equal(t, 3, srv.Count(), "the bound is on requests, which is what costs money")
	require.Equal(t, 3, res.Turns)
	require.Equal(t, 3, n)

	// And the partial result is returned alongside the error, so a caller can log what it did before giving
	// up. Returning nil here would throw away the only evidence of why it looped.
	require.Len(t, res.Calls, 3)
	require.Equal(t, int64(300), res.Usage.InputTokens)
}

// TestMaxTurnsMustBePositive refuses the unbounded configuration outright.
func TestMaxTurnsMustBePositive(t *testing.T) {
	registry, err := NewRegistry()
	require.NoError(t, err)

	c, _ := client(t)

	o := opts()
	o.MaxTurns = 0

	_, err = Run(context.Background(), c, registry, "hi", o)
	require.ErrorContains(t, err, "MaxTurns must be positive")
}

// TestToolSchemaGoesOutAsJSONSchema is what the model actually reads.
func TestToolSchemaGoesOutAsJSONSchema(t *testing.T) {
	var n int

	registry, err := NewRegistry(weather(&n))
	require.NoError(t, err)

	c, srv := client(t, fake.TextMessage("no tools needed", 50, 4))

	_, err = Run(context.Background(), c, registry, "hello", opts())
	require.NoError(t, err)

	var tool struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		InputSchema struct {
			Type       string         `json:"type"`
			Properties map[string]any `json:"properties"`
			Required   []string       `json:"required"`
		} `json:"input_schema"`
	}

	require.NoError(t, json.Unmarshal(srv.Requests()[0].Tools[0], &tool))

	require.Equal(t, "get_weather", tool.Name)
	require.Equal(t, "object", tool.InputSchema.Type, "a tool input is always an object at the top level")
	require.Equal(t, []string{"city"}, tool.InputSchema.Required,
		"without required, the model will omit a field it has no value for, which is correct and broken")
	require.Contains(t, tool.Description, "not for a forecast",
		"the description is read by the model and decides whether the tool is chosen")

	// The property description matters too: it is how the model knows what "city" means.
	city, ok := tool.InputSchema.Properties["city"].(map[string]any)
	require.True(t, ok)
	require.NotEmpty(t, city["description"])
}

// TestNoToolUseMeansOneTurn is the case where the model just answers.
func TestNoToolUseMeansOneTurn(t *testing.T) {
	var n int

	registry, err := NewRegistry(weather(&n))
	require.NoError(t, err)

	c, srv := client(t, fake.TextMessage("Hello.", 40, 3))

	res, err := Run(context.Background(), c, registry, "say hello", opts())
	require.NoError(t, err)

	require.Equal(t, 1, res.Turns)
	require.Zero(t, n, "offering a tool does not make the model use one")
	require.Equal(t, 1, srv.Count())
	require.Equal(t, "Hello.", res.Text)
}

// TestAnUnfinishedAnswerIsAnError is the check a loop that only looks for tool_use gets wrong: each of these
// responses has text in it and would otherwise come back as a successful answer.
func TestAnUnfinishedAnswerIsAnError(t *testing.T) {
	for _, reason := range []anthropic.StopReason{
		anthropic.StopReasonMaxTokens,
		anthropic.StopReasonRefusal,
		anthropic.StopReasonModelContextWindowExceeded,
		"a_stop_reason_from_next_year",
	} {
		t.Run(string(reason), func(t *testing.T) {
			var n int

			registry, err := NewRegistry(weather(&n))
			require.NoError(t, err)

			c, srv := client(t, fake.StoppedMessage("The weather in Lis", string(reason), 40, 256))

			res, err := Run(context.Background(), c, registry, "weather in Lisbon?", opts())
			require.ErrorIs(t, err, ErrIncomplete)
			require.Equal(t, 1, srv.Count(), "retrying the same request would stop the same way")

			// The partial text comes back with the error, so the caller can show it, log it, or ask to continue.
			require.Equal(t, reason, res.StopReason)
			require.Equal(t, "The weather in Lis", res.Text)
		})
	}
}

// TestPauseTurnIsResent is the server-tool case: the API paused a long turn and the model picks it up again
// when the transcript comes back unchanged.
func TestPauseTurnIsResent(t *testing.T) {
	registry, err := NewRegistry()
	require.NoError(t, err)

	c, srv := client(t,
		fake.StoppedMessage("Searching.", "pause_turn", 40, 10),
		fake.TextMessage("Found it.", 60, 5),
	)

	res, err := Run(context.Background(), c, registry, "look it up", opts())
	require.NoError(t, err)

	require.Equal(t, "Found it.", res.Text)
	require.Equal(t, anthropic.StopReasonEndTurn, res.StopReason)
	require.Equal(t, 2, res.Turns)

	// The second request ends with the paused assistant turn and nothing after it: no empty user message,
	// no "please continue". Adding either changes what the model is continuing from.
	second := srv.Requests()[1].Messages
	require.Len(t, second, 2)

	var last struct {
		Role string `json:"role"`
	}

	require.NoError(t, json.Unmarshal(second[1], &last))
	require.Equal(t, "assistant", last.Role)
}
