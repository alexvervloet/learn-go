// Package llm is calling a model from Go: the client, the request shape, retries, and what a response costs.
//
// # How this module is tested
//
// Almost entirely without the network. An LLM API is an HTTP API, so an httptest.Server replaying recorded
// responses exercises the real SDK against a real transport, deterministically and for nothing. The tests that
// genuinely need a live model are gated on ANTHROPIC_API_KEY and skip without it.
//
// That split is not a compromise. A test asserting "the model answers 4 when asked what 2+2 is" tests the
// model, which is not the subject, is not deterministic and costs money on every run. A test asserting "the
// SDK retries a 429 three times, honouring retry-after" tests the code you wrote and will actually break.
//
// # The default model here is Haiku
//
// The cheapest capable model, on purpose. Everything in this module is about the SHAPE of the call: the
// message list, the tool loop, the streaming events, the token accounting. None of it gets better with a more
// expensive model, and a learning repository that defaults to an expensive one teaches an expensive habit.
package llm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// DefaultModel is the model every example uses unless it says otherwise.
//
// The dated form, not the alias. `claude-haiku-4-5` follows the newest release of that line, so a request made
// today and the same request in three months can hit different weights. For a demo that is fine and for
// anything whose output is compared against a stored expectation it is a silent break, so the habit worth
// building is to pin.
const DefaultModel = anthropic.ModelClaudeHaiku4_5_20251001

// Config is what a caller decides.
type Config struct {
	APIKey string

	// BaseURL points the client somewhere else, which is how the tests here reach an httptest.Server. It is
	// also how you reach a proxy, a gateway, or a recording of yesterday's traffic.
	BaseURL string

	// MaxRetries bounds the SDK's own retrying. The default is 2, and the SDK retries 408, 409, 429 and 5xx
	// with exponential backoff, honouring a retry-after header when one is present.
	//
	// Worth knowing what is NOT retried: a 400 (your request is wrong) and a 401 (your key is wrong) are
	// terminal, which is right, and an overloaded_error arrives as a 529 and is.
	MaxRetries int

	// Timeout bounds one ATTEMPT, not the call. The SDK starts a fresh timer for each try and retries an attempt
	// that timed out, so with the default two retries a 10 second Timeout can take over 30 seconds to fail. To
	// bound the whole call, retries and backoff included, put a deadline on the ctx you pass in.
	//
	// A generation of several thousand tokens takes tens of seconds, so a 10 second timeout on a long completion
	// is a timeout that fires on success, and then again on each retry.
	Timeout time.Duration

	// HTTPClient replaces the transport, which is how a test counts requests.
	HTTPClient *http.Client
}

// ErrNoAPIKey is returned when there is no key to use.
var ErrNoAPIKey = errors.New("llm: no API key; set ANTHROPIC_API_KEY")

// New builds a client.
//
// # Why the key is not read from the environment by the SDK here
//
// It would be: anthropic.NewClient() with no options reads ANTHROPIC_API_KEY itself. Passing it explicitly
// means a caller can see where it came from, and means a test can supply a fake one without setting a process
// environment variable that every other test in the binary then shares.
func New(cfg Config) (anthropic.Client, error) {
	if cfg.APIKey == "" {
		return anthropic.Client{}, ErrNoAPIKey
	}

	opts := []option.RequestOption{option.WithAPIKey(cfg.APIKey)}

	if cfg.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(cfg.BaseURL))
	}

	if cfg.MaxRetries > 0 {
		opts = append(opts, option.WithMaxRetries(cfg.MaxRetries))
	}

	if cfg.Timeout > 0 {
		opts = append(opts, option.WithRequestTimeout(cfg.Timeout))
	}

	if cfg.HTTPClient != nil {
		opts = append(opts, option.WithHTTPClient(cfg.HTTPClient))
	}

	return anthropic.NewClient(opts...), nil
}

// APIKeyFromEnv reads the key, or returns ErrNoAPIKey.
func APIKeyFromEnv() (string, error) {
	if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
		return key, nil
	}

	return "", ErrNoAPIKey
}

// Ask sends a single-turn question and returns the text.
//
// # The three things every request needs
//
// Model, MaxTokens and Messages. MaxTokens is REQUIRED, which surprises people coming from other APIs, and it
// is a cap rather than a target: the model usually stops earlier. Setting it too low truncates mid-sentence and
// reports StopReason "max_tokens", which is the thing to check before blaming the prompt.
//
// # The system prompt is not a message
//
// It is a separate field, not a message with role "system". That is a real difference: system content is not
// part of the conversational turn structure, so it cannot be confused with something the user said, and it
// cannot be the last turn.
func Ask(ctx context.Context, client anthropic.Client, system, question string, maxTokens int64) (string, anthropic.Usage, error) {
	params := anthropic.MessageNewParams{
		Model:     DefaultModel,
		MaxTokens: maxTokens,
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(question)),
		},
	}

	if system != "" {
		params.System = []anthropic.TextBlockParam{{Text: system}}
	}

	msg, err := client.Messages.New(ctx, params)
	if err != nil {
		return "", anthropic.Usage{}, err
	}

	return TextOf(msg), msg.Usage, nil
}

// TextOf joins the text blocks of a response.
//
// # Why a response is a LIST of blocks
//
// Because one response can hold several kinds of content: text, a tool call, a thinking block, an image
// reference. Treating it as a string works until the first tool call, and then the tool call is silently
// dropped. Iterating and switching on the type is the shape that does not break later.
func TextOf(msg *anthropic.Message) string {
	var b strings.Builder

	for _, block := range msg.Content {
		if text := block.AsText(); text.Text != "" {
			b.WriteString(text.Text)
		}
	}

	return b.String()
}

// Conversation accumulates turns.
//
// # Why this type exists at all
//
// The API is STATELESS. There is no session, no conversation id, and nothing on the server remembers the last
// exchange. Every request carries the entire history, which is why a long conversation gets slower and more
// expensive with every turn: the input token count is the whole transcript, every time.
//
// That is also the thing to know before designing anything around it. A chat UI that keeps a thousand turns is
// re-sending a thousand turns.
type Conversation struct {
	System   string
	Messages []anthropic.MessageParam
}

// User appends a user turn.
func (c *Conversation) User(text string) {
	c.Messages = append(c.Messages, anthropic.NewUserMessage(anthropic.NewTextBlock(text)))
}

// Assistant appends an assistant turn, which is how you continue a conversation you already have.
func (c *Conversation) Assistant(text string) {
	c.Messages = append(c.Messages, anthropic.NewAssistantMessage(anthropic.NewTextBlock(text)))
}

// Params builds a request from the conversation.
func (c *Conversation) Params(model anthropic.Model, maxTokens int64) anthropic.MessageNewParams {
	params := anthropic.MessageNewParams{
		Model:     model,
		MaxTokens: maxTokens,
		Messages:  c.Messages,
	}

	if c.System != "" {
		params.System = []anthropic.TextBlockParam{{Text: c.System}}
	}

	return params
}

// Send makes the call and appends the reply, so the conversation is ready for the next turn.
func (c *Conversation) Send(ctx context.Context, client anthropic.Client, model anthropic.Model, maxTokens int64) (*anthropic.Message, error) {
	msg, err := client.Messages.New(ctx, c.Params(model, maxTokens))
	if err != nil {
		return nil, err
	}

	// ToParam converts a response back into a request message. Doing it by hand is where tool_use blocks get
	// lost, because reconstructing from the text alone drops every other block type.
	c.Messages = append(c.Messages, msg.ToParam())

	return msg, nil
}

// StatusCode digs the HTTP status out of an SDK error, or returns 0.
func StatusCode(err error) int {
	var apiErr *anthropic.Error
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode
	}

	return 0
}

// IsRetryable reports whether an error is one the SDK would have retried.
//
// Useful for deciding what to do AFTER the SDK gave up: a 429 that survived the retries means back off much
// further, a 500 means try again later, and a 400 means fix the code.
func IsRetryable(err error) bool {
	switch code := StatusCode(err); code {
	case http.StatusRequestTimeout, http.StatusConflict, http.StatusTooManyRequests:
		return true
	case 529: // overloaded_error, which is Anthropic-specific and not in net/http
		return true
	default:
		return code >= 500
	}
}

// Describe renders an error for a log line.
//
// The point is that an API error has a type string as well as a status, and the type is the stable thing.
// "invalid_request_error" does not change; the message does.
func Describe(err error) string {
	var apiErr *anthropic.Error
	if !errors.As(err, &apiErr) {
		return err.Error()
	}

	return fmt.Sprintf("status=%d request_id=%s retryable=%t: %v",
		apiErr.StatusCode, apiErr.RequestID, IsRetryable(err), err)
}
