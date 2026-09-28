// Package costs turns token counts into money, and makes the caching discount visible.
//
// # Why this is its own package
//
// Because the arithmetic is where the surprises are, and the arithmetic is pure. No network, no key, no
// non-determinism: a table of prices and a few multiplications, all of it testable.
//
// # The four token counts
//
// A response reports input_tokens and output_tokens, and with prompt caching also cache_creation_input_tokens
// and cache_read_input_tokens. They are priced differently and they do not overlap: a cached prefix is counted
// in the cache fields and NOT in input_tokens, so adding all four is the total the bill is computed from.
//
// Output is several times the price of input on every model, which is the single most useful fact here. A
// system prompt of five thousand tokens is cheap; a five thousand token answer is not.
package costs

import (
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
)

// Price is what one million tokens costs, in US dollars.
//
// # These are a snapshot and will be wrong
//
// Prices change. Hard-coding them in a program that bills anyone is a mistake; the numbers are here so the
// RATIOS can be demonstrated, and those are stable: output is several times input, a cache write is a premium
// over input, a cache read is a large discount.
type Price struct {
	Input         float64
	Output        float64
	CacheWrite    float64
	CacheRead     float64
	ContextWindow int
}

// Prices covers the models used in this module.
//
// Haiku is the default everywhere here for the obvious reason: it is the cheapest, and none of what this module
// demonstrates improves with a more expensive model.
var Prices = map[anthropic.Model]Price{
	anthropic.ModelClaudeHaiku4_5_20251001: {
		Input: 1.00, Output: 5.00, CacheWrite: 1.25, CacheRead: 0.10, ContextWindow: 200_000,
	},
	anthropic.ModelClaudeHaiku4_5: {
		Input: 1.00, Output: 5.00, CacheWrite: 1.25, CacheRead: 0.10, ContextWindow: 200_000,
	},
	anthropic.ModelClaudeSonnet4_5_20250929: {
		Input: 3.00, Output: 15.00, CacheWrite: 3.75, CacheRead: 0.30, ContextWindow: 200_000,
	},
}

// ErrUnknownModel is returned when there is no price for a model.
//
// Not a zero cost. A silent zero is how a program reports a bill of nothing while spending money, and the whole
// point of this package is to make the number visible.
var ErrUnknownModel = fmt.Errorf("costs: no price for that model")

// Breakdown is a cost, itemised.
type Breakdown struct {
	Input      float64
	Output     float64
	CacheWrite float64
	CacheRead  float64
}

// Total is the sum.
func (b Breakdown) Total() float64 {
	return b.Input + b.Output + b.CacheWrite + b.CacheRead
}

// Of computes the cost of one response.
func Of(model anthropic.Model, usage anthropic.Usage) (Breakdown, error) {
	price, ok := Prices[model]
	if !ok {
		return Breakdown{}, fmt.Errorf("%w: %s", ErrUnknownModel, model)
	}

	const perMillion = 1_000_000.0

	return Breakdown{
		Input:      float64(usage.InputTokens) / perMillion * price.Input,
		Output:     float64(usage.OutputTokens) / perMillion * price.Output,
		CacheWrite: float64(usage.CacheCreationInputTokens) / perMillion * price.CacheWrite,
		CacheRead:  float64(usage.CacheReadInputTokens) / perMillion * price.CacheRead,
	}, nil
}

// TotalTokens is every token the request was billed for.
//
// The cache fields do NOT overlap with input_tokens. A request whose whole prompt was a cache hit reports an
// input_tokens near zero and a large cache_read_input_tokens, and a monitor summing only input_tokens reports
// that the traffic vanished.
func TotalTokens(usage anthropic.Usage) int64 {
	return usage.InputTokens + usage.OutputTokens +
		usage.CacheCreationInputTokens + usage.CacheReadInputTokens
}

// ConversationCost is what a multi-turn exchange costs, given the per-turn usage.
//
// # Why a conversation is superlinear
//
// The API is stateless, so every turn re-sends the whole transcript. Turn n pays for turns 1..n-1 as input.
// With turns of roughly equal size, the input tokens over a conversation grow with the SQUARE of the turn
// count, which is why a long chat session costs far more than the sum of its answers suggests.
func ConversationCost(model anthropic.Model, turns []anthropic.Usage) (Breakdown, error) {
	var total Breakdown

	for _, u := range turns {
		b, err := Of(model, u)
		if err != nil {
			return Breakdown{}, err
		}

		total.Input += b.Input
		total.Output += b.Output
		total.CacheWrite += b.CacheWrite
		total.CacheRead += b.CacheRead
	}

	return total, nil
}

// SimulateConversation models n turns where each adds `perTurn` input tokens and produces `perTurn` output.
//
// It exists so the growth can be asserted rather than described. Nothing about it needs an API.
func SimulateConversation(turns, perTurn int) []anthropic.Usage {
	out := make([]anthropic.Usage, 0, turns)

	transcript := 0

	for range turns {
		transcript += perTurn // the new user message

		out = append(out, anthropic.Usage{
			InputTokens:  int64(transcript),
			OutputTokens: int64(perTurn),
		})

		transcript += perTurn // the assistant's reply joins the transcript
	}

	return out
}

// CacheSavings compares a cached prefix against sending it fresh every time.
//
// # When caching pays and when it does not
//
// A cache write costs MORE than an ordinary input token, and a read costs far less. So the first request is a
// loss and every subsequent one is a large win, which means caching pays exactly when the same prefix is reused
// and is a pure cost when it is not.
//
// The break-even is one request: with a write premium of 25% and a read discount of 90%, a second request
// already more than repays the first. That is why a long system prompt or a large document is the canonical
// thing to cache and a user's question is not.
func CacheSavings(model anthropic.Model, prefixTokens, requests int) (cached, uncached float64, err error) {
	price, ok := Prices[model]
	if !ok {
		return 0, 0, fmt.Errorf("%w: %s", ErrUnknownModel, model)
	}

	const perMillion = 1_000_000.0

	tokens := float64(prefixTokens) / perMillion

	// Written once, read on every subsequent request.
	cached = tokens*price.CacheWrite + tokens*price.CacheRead*float64(requests-1)

	// Sent in full every time.
	uncached = tokens * price.Input * float64(requests)

	return cached, uncached, nil
}
