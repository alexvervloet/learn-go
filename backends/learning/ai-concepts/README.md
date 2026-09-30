# AI concepts

Calling a model from Go: the request shape, the tool loop, streaming, retrieval, and what it all costs.

```bash
go test ./...
```

No key needed. Every test but one runs against a fake Messages API served by `httptest`.

## How this module is tested

An LLM API is an HTTP API, so [`fake/`](fake/) is an `httptest.Server` replaying canned responses. The SDK
marshals, signs, sends, retries and unmarshals for real; only the sentence at the end is canned.

That is not a compromise. A test asserting "the model answers 4 when asked what 2+2 is" tests the model, which
is not the subject, is not deterministic, and costs money on every run. A test asserting "the SDK makes four
attempts against a 429 and one against a 400" tests the code you wrote and will actually break.

There is exactly one live test, `TestLiveRoundTrip`, gated on `ANTHROPIC_API_KEY` and capped at 16 output
tokens on the cheapest model. Its assertions are about the *shape* of a reply, because those hold on any day.

## What is here

| Package | Subject |
|---|---|
| [`llm/`](llm/) | The client, the request shape, the stateless API, retry behaviour |
| [`agent/`](agent/) | The tool-use loop, bounded |
| [`streaming/`](streaming/) | The SSE event sequence and where usage actually lives |
| [`rag/`](rag/) | Chunking, cosine ranking, prompt assembly |
| [`costs/`](costs/) | Token accounting, and why a conversation is superlinear |
| [`fake/`](fake/) | The test double everything above runs against |

## The default model is Haiku

On purpose, and the code says so where it is defined. Everything here is about the *shape* of the call. None of
it improves with a more expensive model, and a learning repository that defaults to an expensive one teaches an
expensive habit.

It is pinned to the dated id, `claude-haiku-4-5-20251001`, rather than the `claude-haiku-4-5` alias. The alias
follows the newest release of that line, so the same request three months from now can hit different weights.

## The API is stateless

There is no session and no conversation id. Every request carries the whole transcript, which is why a long
chat gets slower and more expensive with every turn.

`TestTheAPIIsStateless` asserts it directly: turn one sends 1 message, turn two sends 3.

`costs.SimulateConversation` puts a number on the consequence. With turns of roughly equal size, input tokens
grow with the **square** of the turn count:

| Turns | Input tokens |
|---|---|
| 5 | 2,500 |
| 10 | 10,000 |

Double the turns, quadruple the input. That is the fact to have in hand before designing anything with a long
context.

## Costs

Output is **5x** the price of input on every model in the table. A five-thousand-token system prompt is cheap;
a five-thousand-token answer is not.

The four token counts do not overlap. A request whose prompt was mostly a cache hit reports an `input_tokens`
near zero and a large `cache_read_input_tokens`, so a monitor summing only `input_tokens` reports that the
traffic vanished.

Caching loses on the first request and wins on every one after, because a write costs a premium and a read is a
90% discount. Measured by `TestCachingLosesOnceAndWinsAfterwards`:

| Requests sharing a 50k-token prefix | Cached | Uncached | Saved |
|---|---|---|---|
| 1 | $0.0625 | $0.0500 | -25% |
| 2 | $0.0675 | $0.1000 | 32% |
| 100 | $0.5575 | $5.0000 | 89% |
| 1,000 | $5.06 | $50.00 | 90% |

Break-even is the second request. Which is why the canonical thing to cache is a long system prompt or a large
document, and not a user's question.

The prices are a snapshot and will be wrong. The ratios are what to remember.

## The tool loop

The whole protocol:

1. Send a request with a list of tools, each a name, a description and a JSON Schema.
2. The model replies with `stop_reason: "tool_use"` and one or more `tool_use` blocks.
3. Run the tools, send the results back as `tool_result` blocks **in a user message**.
4. Repeat until `stop_reason` is `end_turn`.

The model never calls anything. It emits a structured request and waits, so every piece of the execution,
including whether to execute at all, is yours. That is where the safety lives: the model is choosing which of
your functions to run, with arguments derived from input a stranger may have written.

Three things the tests pin because they are the ones that break:

- **The tool result goes in a user message.** It feels like an assistant message, because it answers something
  the assistant asked for, and the API rejects it there.
- **Every `tool_use` block needs a matching `tool_result`.** A model can ask for three tools in one response;
  replying to one is a 400. `TestEveryToolUseBlockNeedsAResult` covers it.
- **`msg.ToParam()`, not a rebuild from the text.** Reconstructing the assistant turn from its text drops the
  `tool_use` block, and the next request is a 400.
- **"Not `tool_use`" does not mean "done".** `max_tokens` cut the answer off, `refusal` declined it and
  `model_context_window_exceeded` ran out of room. Each comes back with text that looks like an answer, so
  `Run` returns it with `ErrIncomplete`. `pause_turn` means a server-side tool paused a long turn, and the loop
  sends the transcript back unchanged so the model carries on. `TestAnUnfinishedAnswerIsAnError` covers these.
- **The tool list goes out in the same order every turn.** Tools are the start of the prompt, and the prompt
  cache matches an exact prefix. Built by ranging over a map, the list changes order between turns and every
  turn pays full price for everything after it. `TestToolOrderIsStable` covers it.

A failing tool sends its error back as a `tool_result` with `is_error`, rather than ending the loop. The model
can then apologise, retry with different arguments, or give up, which is usually better than the program
deciding on its behalf.

**The loop is bounded and there is no unlimited setting.** `MaxTurns: 0` is refused at the start. A model stuck
asking for the same tool is a bill, and `TestTheLoopIsBounded` runs that case against a fake that repeats one
response forever.

The tool's `Description` is the most load-bearing string in the design. It is read by the model and decides
whether the tool is chosen and when. "Gets weather" and "Returns the current temperature in Celsius for a city;
use for conditions right now, not for a forecast" behave differently, and the second is not verbose, it is the
specification.

## Streaming

The event sequence is a contract, asserted by `TestTheEventSequenceIsAContract`:

```
message_start          the message, input tokens, output_tokens: 1
content_block_start
content_block_delta    many
content_block_stop
message_delta          the stop reason and the REAL output token count
message_stop
```

**Output tokens are in the final `message_delta`.** `message_start` reports 1, because nothing has been
generated yet. Reading usage from `message_start` reports every response as costing one output token, and it is
the commonest bug in streaming cost accounting.

`Message.Accumulate` applies each event to a `Message`, so at the end you hold exactly what a non-streaming call
would have returned. That matters most for tool calls, whose arguments arrive as `input_json_delta` fragments
that are not individually valid JSON. Anything trying to `json.Unmarshal` each delta fails on the first one.

**Check `stream.Err()`.** The loop ends on the last event *and* on an error, and from inside it the two look
identical. Same shape as `bufio.Scanner`, forgotten for the same reason.

Stopping a stream early is a latency win and not a cost one. The tokens were already generated.

## Retrieval

The model knows its training data. It does not know your documents, and a context window is not a place to put
all of them. So: split, find the relevant pieces, put those in the prompt. The retrieval half is an ordinary
search problem.

**Chunking decides everything.** A chunk is the unit of retrieval. Too large and every hit drags in paragraphs
of irrelevance. Too small and a chunk loses what made it meaningful: "it costs $40 a month" is useless without
the sentence naming the product. The overlap exists so that a sentence spanning a boundary survives whole
somewhere.

`Fixed` is the baseline everyone writes first and it cuts mid-word. `Sentences` packs whole sentences up to a
budget. The size is in **runes**, not bytes: a byte split lands inside a multi-byte character and produces
invalid UTF-8, which then fails to embed, fails to display, and fails quietly.
`TestFixedChunkingDoesNotSplitRunes` uses a 25-character, 75-byte string to show it.

An overlap equal to or larger than the size is refused, because the window never advances and the loop does not
terminate. So is a negative one, which would step past text between chunks. `Sentences` measures overlap in
sentences and caps it at one less than the chunk holds, so each chunk starts at least a sentence later than the
last. Its `Start` and `End` are rune offsets taken from the splitter, so they slice the source back out even when
a sentence appears twice.

`TestTheSentenceSplitterIsNaive` asserts a known wrong answer: "Dr. Smith arrived." splits into two. Stating the
limit and checking it is better than pretending a regex handles English.

**Cosine ignores magnitude**, so a long document and a short one about the same thing score the same. On
normalised vectors, which is what every embedding API returns, cosine and the dot product are the same number.

**The threshold is the part people leave out.** Without one, a query about something the corpus does not cover
still returns k chunks, those chunks go in the prompt, and the model answers from irrelevant context. And a
threshold of `0` is not a threshold: a chunk sharing no words scores exactly 0, and 0 is not less than 0.

The vectors here are a bag of words, which is a 1970s retrieval model with no notion of meaning: "car" and
"automobile" are orthogonal. `TestBagOfWordsHasNoNotionOfMeaning` asserts the 0.5 a real embedding would score
near 1, which is the entire reason embeddings replaced word counting. Real vectors are in
[database-concepts](../database-concepts/), against pgvector.

## Cost of running this

Everything but `TestLiveRoundTrip` is free and offline. That one asks the cheapest model a two-word question
with `max_tokens: 16`, and skips without a key.
