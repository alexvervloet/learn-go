package costs

import (
	"fmt"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/stretchr/testify/require"
)

const model = anthropic.ModelClaudeHaiku4_5_20251001

// TestOutputCostsMoreThanInput is the one ratio worth memorising.
func TestOutputCostsMoreThanInput(t *testing.T) {
	for name, price := range Prices {
		// anthropic.Model is a defined string type, so a plain name works as the subtest name.
		t.Run(name, func(t *testing.T) {
			require.Greater(t, price.Output, price.Input,
				"output is several times the price of input on every model")
			require.Equal(t, 5.0, price.Output/price.Input,
				"a 5x ratio: a long system prompt is cheap, a long answer is not")
		})
	}
}

// TestUnknownModelIsAnError rather than a free one.
func TestUnknownModelIsAnError(t *testing.T) {
	_, err := Of("claude-does-not-exist", anthropic.Usage{InputTokens: 1000})
	require.ErrorIs(t, err, ErrUnknownModel)
}

// TestCacheFieldsDoNotOverlapWithInput is the monitoring trap.
func TestCacheFieldsDoNotOverlapWithInput(t *testing.T) {
	// A request whose prompt was almost entirely a cache hit.
	usage := anthropic.Usage{
		InputTokens:          12,
		OutputTokens:         200,
		CacheReadInputTokens: 45_000,
	}

	require.Equal(t, int64(45_212), TotalTokens(usage))

	b, err := Of(model, usage)
	require.NoError(t, err)

	// The cached read dominates the token count and not the bill, which is the whole point.
	require.InDelta(t, 0.0045, b.CacheRead, 1e-9)
	require.InDelta(t, 0.001, b.Output, 1e-9)
	require.InDelta(t, 0.0000120, b.Input, 1e-9)

	// A monitor summing only InputTokens reports 12 tokens for a request that read 45,000.
	require.Equal(t, int64(12), usage.InputTokens)
}

// TestAConversationGrowsWithTheSquareOfItsLength is the stateless-API consequence, measured.
func TestAConversationGrowsWithTheSquareOfItsLength(t *testing.T) {
	const perTurn = 100

	short := SimulateConversation(5, perTurn)
	long := SimulateConversation(10, perTurn)

	sum := func(turns []anthropic.Usage) int64 {
		var total int64
		for _, u := range turns {
			total += u.InputTokens
		}

		return total
	}

	shortInput, longInput := sum(short), sum(long)

	// Twice the turns, more than twice the input. The exact ratio for n turns is n^2 rather than n:
	// 5 turns is 100+300+500+700+900 = 2500, and 10 turns is 10000.
	require.Equal(t, int64(2_500), shortInput)
	require.Equal(t, int64(10_000), longInput)
	require.Equal(t, int64(4), longInput/shortInput, "double the turns, quadruple the input tokens")

	shortCost, err := ConversationCost(model, short)
	require.NoError(t, err)

	longCost, err := ConversationCost(model, long)
	require.NoError(t, err)

	t.Logf("5 turns: $%.6f, 10 turns: $%.6f", shortCost.Total(), longCost.Total())
	require.Greater(t, longCost.Total()/shortCost.Total(), 2.0,
		"the cost more than doubles, because the input half grows quadratically")
}

// TestCachingLosesOnceAndWinsAfterwards is the break-even.
func TestCachingLosesOnceAndWinsAfterwards(t *testing.T) {
	const prefix = 50_000

	tests := []struct {
		requests  int
		cacheWins bool
	}{
		{requests: 1, cacheWins: false},
		{requests: 2, cacheWins: true},
		{requests: 10, cacheWins: true},
		{requests: 100, cacheWins: true},
	}

	for _, tc := range tests {
		t.Run(fmt.Sprintf("%d requests", tc.requests), func(t *testing.T) {
			cached, uncached, err := CacheSavings(model, prefix, tc.requests)
			require.NoError(t, err)

			if tc.cacheWins {
				require.Less(t, cached, uncached,
					"cached $%.4f vs uncached $%.4f", cached, uncached)
			} else {
				require.Greater(t, cached, uncached,
					"the first request pays a write premium and reads nothing: $%.4f vs $%.4f", cached, uncached)
			}

			t.Logf("%d requests of a %d-token prefix: cached $%.4f, uncached $%.4f (%.0f%% saved)",
				tc.requests, prefix, cached, uncached, 100*(1-cached/uncached))
		})
	}
}

// TestCachingSavesMostOfTheBillAtScale puts a number on it.
func TestCachingSavesMostOfTheBillAtScale(t *testing.T) {
	cached, uncached, err := CacheSavings(model, 50_000, 1_000)
	require.NoError(t, err)

	saving := 1 - cached/uncached
	require.Greater(t, saving, 0.85, "a reused prefix is a 90% discount, minus the one write")

	t.Logf("1000 requests sharing a 50k-token prefix: $%.2f cached against $%.2f uncached, %.1f%% saved",
		cached, uncached, 100*saving)
}

// TestBreakdownTotalsItsParts is the arithmetic, pinned.
func TestBreakdownTotalsItsParts(t *testing.T) {
	b, err := Of(model, anthropic.Usage{
		InputTokens:              1_000_000,
		OutputTokens:             1_000_000,
		CacheCreationInputTokens: 1_000_000,
		CacheReadInputTokens:     1_000_000,
	})
	require.NoError(t, err)

	// One million of each, so the breakdown is the price table read back.
	require.InDelta(t, 1.00, b.Input, 1e-9)
	require.InDelta(t, 5.00, b.Output, 1e-9)
	require.InDelta(t, 1.25, b.CacheWrite, 1e-9)
	require.InDelta(t, 0.10, b.CacheRead, 1e-9)
	require.InDelta(t, 7.35, b.Total(), 1e-9)
}

// TestOneHourCacheWritesCostMore splits cache_creation_input_tokens by TTL. The total is 1M tokens, 400k of them
// written with the one-hour TTL at 2x input and the rest at 1.25x.
func TestOneHourCacheWritesCostMore(t *testing.T) {
	b, err := Of(model, anthropic.Usage{
		CacheCreationInputTokens: 1_000_000,
		CacheCreation: anthropic.CacheCreation{
			Ephemeral5mInputTokens: 600_000,
			Ephemeral1hInputTokens: 400_000,
		},
	})
	require.NoError(t, err)

	require.InDelta(t, 0.6*1.25+0.4*2.00, b.CacheWrite, 1e-9)
}

// TestEveryPriceFollowsTheCacheMultipliers checks the table against the published multipliers, so a row typed
// in by hand cannot drift from them.
func TestEveryPriceFollowsTheCacheMultipliers(t *testing.T) {
	for name, price := range Prices {
		t.Run(name, func(t *testing.T) {
			require.InDelta(t, 1.25*price.Input, price.CacheWrite, 1e-9)
			require.InDelta(t, 2.00*price.Input, price.CacheWrite1h, 1e-9)

			// Reads are 0.1x input, with published exceptions.
			read := 0.10
			if name == anthropic.ModelClaudeOpus5_5 {
				read = 0.05
			}

			require.InDelta(t, read*price.Input, price.CacheRead, 1e-9)
		})
	}
}
