// Package agent is the tool-use loop: the thing that turns a text generator into something that can act.
//
// # The whole idea in four steps
//
//  1. You send a request with a list of tools, each a name, a description and a JSON Schema for its input.
//  2. The model replies with stop_reason "tool_use" and one or more tool_use blocks.
//  3. You run the tools and send the results back as tool_result blocks in a USER message.
//  4. Repeat until the model stops asking.
//
// That is the entire protocol. The model does not call anything; it emits a structured request and waits. Every
// piece of the execution, including deciding whether to execute at all, is yours.
//
// # Where the safety lives
//
// Entirely in step 3. The model is choosing which of YOUR functions to run and with what arguments, from input
// that may include text a stranger wrote. A tool that runs a shell command is a shell command an attacker can
// reach through a prompt. So: no tool does anything the caller could not do, the input is validated against
// the schema by your code and not on trust, and anything irreversible confirms.
//
// # The loop is the dangerous part
//
// Without a bound it does not terminate. A model that keeps asking for the same tool, a tool that keeps
// returning an error, a request that grows by a turn each time: all three are bugs that turn into a bill. The
// loop here takes MaxTurns and returns an error rather than a partial answer, because a silent cap looks like
// the model deciding to stop.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
)

// Tool is one function the model can ask for.
type Tool struct {
	Name string

	// Description is read by the MODEL and is the most load-bearing string in the whole design. It decides
	// whether the tool is chosen, and when. "Gets weather" and "Returns the current temperature in Celsius
	// for a city; use when the user asks about weather conditions right now, not for forecasts" behave
	// differently, and the second is not verbose, it is the specification.
	Description string

	// Schema is JSON Schema for the input. The model uses it to build arguments and the runtime uses it to
	// reject bad ones. Required fields matter: without them the model will omit a field it does not have a
	// value for, which is correct behaviour and a broken call.
	Schema anthropic.ToolInputSchemaParam

	// Run executes the tool. The raw JSON is passed rather than a decoded map, so the handler decides its own
	// type and a decode failure is the handler's error to report.
	Run func(ctx context.Context, input json.RawMessage) (string, error)
}

// Param converts a Tool into the SDK's request shape.
func (t Tool) Param() anthropic.ToolUnionParam {
	return anthropic.ToolUnionParam{
		OfTool: &anthropic.ToolParam{
			Name:        t.Name,
			Description: anthropic.String(t.Description),
			InputSchema: t.Schema,
		},
	}
}

// Registry holds the tools by name.
type Registry map[string]Tool

// NewRegistry builds one and rejects a duplicate name.
//
// A duplicate is not a warning. The model picks by name, and in a map the second tool would silently replace the
// first, so a call the model meant for one runs the other.
func NewRegistry(tools ...Tool) (Registry, error) {
	r := make(Registry, len(tools))

	for _, t := range tools {
		if _, exists := r[t.Name]; exists {
			return nil, fmt.Errorf("agent: two tools named %q", t.Name)
		}

		r[t.Name] = t
	}

	return r, nil
}

// Params returns the tool list for a request, sorted by name.
//
// The order matters more than it looks. Tools are the first thing in the prompt, and prompt caching matches a
// byte-for-byte prefix, so a tool list in map iteration order is a different prefix on most turns and every
// cached token after it is paid for again. Sorting makes the list identical on every turn of every run.
func (r Registry) Params() []anthropic.ToolUnionParam {
	out := make([]anthropic.ToolUnionParam, 0, len(r))
	for _, name := range slices.Sorted(maps.Keys(r)) {
		out = append(out, r[name].Param())
	}

	return out
}

// Call is one tool invocation, recorded.
type Call struct {
	Turn  int
	ID    string
	Name  string
	Input json.RawMessage
	Out   string
	Err   error
	Took  time.Duration
}

// Result is what a finished loop produced.
type Result struct {
	Text  string
	Calls []Call
	Turns int
	Usage anthropic.Usage
}

// ErrTooManyTurns is returned when the loop hits its bound.
var ErrTooManyTurns = errors.New("agent: the model kept asking for tools past the turn limit")

// Options configures a run.
type Options struct {
	Model     anthropic.Model
	MaxTokens int64
	System    string

	// MaxTurns bounds the loop. There is no sensible unlimited value.
	MaxTurns int
}

// Run drives the loop until the model stops asking for tools.
//
// # Three details that are easy to get wrong
//
//   - The tool RESULT goes in a user message. It feels like an assistant message because it is the output of
//     something the assistant asked for, and the API rejects it there.
//   - EVERY tool_use block in one response needs a matching tool_result in the next request. A model can ask
//     for three tools at once; replying to one is a 400.
//   - A failing tool sends its error back as a tool_result with is_error set, not as a dropped turn. The model
//     can then apologise, try different arguments, or give up, which is usually better than the program
//     deciding on its behalf.
func Run(ctx context.Context, client anthropic.Client, registry Registry, question string, opts Options) (*Result, error) {
	if opts.MaxTurns <= 0 {
		return nil, errors.New("agent: MaxTurns must be positive; an unbounded loop is a bill")
	}

	messages := []anthropic.MessageParam{
		anthropic.NewUserMessage(anthropic.NewTextBlock(question)),
	}

	result := &Result{}

	for turn := 1; turn <= opts.MaxTurns; turn++ {
		params := anthropic.MessageNewParams{
			Model:     opts.Model,
			MaxTokens: opts.MaxTokens,
			Messages:  messages,
			Tools:     registry.Params(),
		}

		if opts.System != "" {
			params.System = []anthropic.TextBlockParam{{Text: opts.System}}
		}

		msg, err := client.Messages.New(ctx, params)
		if err != nil {
			return nil, fmt.Errorf("turn %d: %w", turn, err)
		}

		result.Turns = turn

		// Usage is per request, so a loop's total is the sum. This is the number that makes a runaway loop
		// visible, and it grows superlinearly because each turn re-sends the whole transcript.
		result.Usage.InputTokens += msg.Usage.InputTokens
		result.Usage.OutputTokens += msg.Usage.OutputTokens

		// ToParam keeps every block, including the tool_use ones. Rebuilding the assistant turn from its text
		// would drop them and the next request would be a 400.
		messages = append(messages, msg.ToParam())

		if msg.StopReason != anthropic.StopReasonToolUse {
			result.Text = textOf(msg)

			return result, nil
		}

		var results []anthropic.ContentBlockParamUnion

		for _, block := range msg.Content {
			use := block.AsToolUse()
			if use.ID == "" {
				continue
			}

			call := Call{Turn: turn, ID: use.ID, Name: use.Name, Input: json.RawMessage(use.JSON.Input.Raw())}

			tool, known := registry[use.Name]
			if !known {
				// A name the registry does not have. This is not impossible: it happens when the tool list
				// and the transcript disagree, which is what a mid-conversation change to the registry looks
				// like. Telling the model is better than failing, because it can pick something else.
				call.Err = fmt.Errorf("no tool named %q", use.Name)
				result.Calls = append(result.Calls, call)
				results = append(results, anthropic.NewToolResultBlock(use.ID, call.Err.Error(), true))

				continue
			}

			start := time.Now()
			out, err := tool.Run(ctx, call.Input)
			call.Took = time.Since(start)

			call.Out, call.Err = out, err
			result.Calls = append(result.Calls, call)

			if err != nil {
				results = append(results, anthropic.NewToolResultBlock(use.ID, err.Error(), true))
				continue
			}

			results = append(results, anthropic.NewToolResultBlock(use.ID, out, false))
		}

		if len(results) == 0 {
			return nil, fmt.Errorf("turn %d: stop_reason was tool_use with no tool_use block", turn)
		}

		messages = append(messages, anthropic.NewUserMessage(results...))
	}

	return result, fmt.Errorf("%w: %d turns", ErrTooManyTurns, opts.MaxTurns)
}

// textOf joins a message's text blocks.
func textOf(msg *anthropic.Message) string {
	var out string

	for _, block := range msg.Content {
		if text := block.AsText(); text.Text != "" {
			out += text.Text
		}
	}

	return out
}

// ObjectSchema builds a JSON Schema for an object, which is the only top-level shape a tool input can have.
//
// The API requires "type": "object" at the top. A tool taking a single string still takes an object with one
// property, which is not a limitation worth fighting: named arguments survive a change of mind and a positional
// one does not.
func ObjectSchema(properties map[string]any, required ...string) anthropic.ToolInputSchemaParam {
	return anthropic.ToolInputSchemaParam{
		Properties: properties,
		Required:   required,
	}
}
