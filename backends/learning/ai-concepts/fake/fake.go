// Package fake is a Messages API served from canned responses.
//
// # Why this and not a mocked client
//
// The SDK is what is being learned. A mock replaces it, so the test proves the mock returns what the mock was
// told to return. An httptest.Server replaces the MODEL and keeps the SDK: the request is marshalled, signed,
// sent, retried and unmarshalled by the real code, and only the sentence at the end is canned.
//
// It also makes the request inspectable. Every test here can assert on the JSON that actually went out, which
// is the thing a reader wants to see and the thing that breaks when the SDK changes.
package fake

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
)

// Request is one captured request body, decoded far enough to assert on.
type Request struct {
	Model     string            `json:"model"`
	MaxTokens int64             `json:"max_tokens"`
	System    json.RawMessage   `json:"system"`
	Messages  []json.RawMessage `json:"messages"`
	Tools     []json.RawMessage `json:"tools"`
	Stream    bool              `json:"stream"`

	// Raw is the whole body, for the assertions the fields above do not cover.
	Raw []byte `json:"-"`

	// Headers are the ones the SDK set.
	Headers http.Header `json:"-"`
}

// Response is what the server should reply with.
type Response struct {
	// Status defaults to 200.
	Status int

	// Body is the response body. For a 200 it is a Message; for an error it is an error envelope.
	Body string

	// Header lets a test set retry-after and friends.
	Header http.Header
}

// Server is a fake Messages API.
type Server struct {
	*httptest.Server

	mu        sync.Mutex
	responses []Response
	requests  []Request
}

// New starts a server that replies with the given responses, in order.
//
// Running out of responses is a 500 with a message saying so, rather than a panic in a handler goroutine. A
// panic there does not fail the test, it kills the process, which is a bad way to learn that the test asked for
// one more turn than it queued.
func New(t *testing.T, responses ...Response) *Server {
	t.Helper()

	s := &Server{responses: responses}

	s.Server = httptest.NewServer(http.HandlerFunc(s.handle))

	t.Cleanup(s.Close)

	return s
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)

	var req Request
	_ = json.Unmarshal(body, &req)

	req.Raw = body
	req.Headers = r.Header.Clone()

	s.mu.Lock()

	s.requests = append(s.requests, req)

	var resp Response

	if len(s.responses) == 0 {
		resp = Response{
			Status: http.StatusInternalServerError,
			Body:   fmt.Sprintf(`{"type":"error","error":{"type":"api_error","message":"fake: no response queued for request %d"}}`, len(s.requests)),
		}
	} else {
		resp = s.responses[0]

		// A single queued response repeats, which is what a retry test wants. Several are consumed in order,
		// which is what a tool loop wants.
		if len(s.responses) > 1 {
			s.responses = s.responses[1:]
		}
	}

	s.mu.Unlock()

	for k, values := range resp.Header {
		for _, v := range values {
			w.Header().Add(k, v)
		}
	}

	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "application/json")
	}

	status := resp.Status
	if status == 0 {
		status = http.StatusOK
	}

	w.WriteHeader(status)
	_, _ = io.WriteString(w, resp.Body)
}

// Requests returns every request the server received.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]Request(nil), s.requests...)
}

// Count returns how many requests arrived.
func (s *Server) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return len(s.requests)
}

// TextMessage builds a Message response carrying one text block.
func TextMessage(text string, inputTokens, outputTokens int) Response {
	encoded, _ := json.Marshal(text)

	return Response{Body: fmt.Sprintf(`{
  "id": "msg_fake",
  "type": "message",
  "role": "assistant",
  "model": "claude-haiku-4-5-20251001",
  "content": [{"type": "text", "text": %s}],
  "stop_reason": "end_turn",
  "stop_sequence": null,
  "usage": {"input_tokens": %d, "output_tokens": %d}
}`, encoded, inputTokens, outputTokens)}
}

// ToolUseMessage builds a Message response asking for a tool call.
//
// stop_reason is "tool_use", which is the signal the agent loop switches on. A response with a tool_use block
// and a stop_reason of "end_turn" is not a thing the API produces, and code that only looks at the blocks works
// anyway right up until a response has both text and a tool call.
func ToolUseMessage(id, name, inputJSON string, inputTokens, outputTokens int) Response {
	return Response{Body: fmt.Sprintf(`{
  "id": "msg_fake_tool",
  "type": "message",
  "role": "assistant",
  "model": "claude-haiku-4-5-20251001",
  "content": [{"type": "tool_use", "id": %q, "name": %q, "input": %s}],
  "stop_reason": "tool_use",
  "stop_sequence": null,
  "usage": {"input_tokens": %d, "output_tokens": %d}
}`, id, name, inputJSON, inputTokens, outputTokens)}
}

// ErrorResponse builds an API error envelope.
func ErrorResponse(status int, errType, message string) Response {
	return Response{
		Status: status,
		Body:   fmt.Sprintf(`{"type":"error","error":{"type":%q,"message":%q}}`, errType, message),
	}
}

// RateLimited builds a 429 with a retry-after header.
func RateLimited(seconds int) Response {
	resp := ErrorResponse(http.StatusTooManyRequests, "rate_limit_error", "slow down")
	resp.Header = http.Header{"Retry-After": []string{strconv.Itoa(seconds)}}

	return resp
}

// StreamResponse builds a server-sent-event stream for a text response.
//
// # The event sequence is not negotiable
//
// message_start, then for each block content_block_start / content_block_delta... / content_block_stop, then
// message_delta carrying the stop reason and the output token count, then message_stop.
//
// The output tokens are in the LAST message_delta, not in message_start. message_start's usage has the input
// count and an output count of 1, because at that point nothing has been generated. Code that reads usage from
// message_start reports every response as costing one output token.
func StreamResponse(chunks []string, inputTokens, outputTokens int) Response {
	var b []byte

	event := func(name, data string) {
		b = append(b, "event: "+name+"\ndata: "+data+"\n\n"...)
	}

	event("message_start", fmt.Sprintf(`{"type":"message_start","message":{"id":"msg_stream","type":"message","role":"assistant","model":"claude-haiku-4-5-20251001","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":%d,"output_tokens":1}}}`, inputTokens))
	event("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)

	for _, chunk := range chunks {
		encoded, _ := json.Marshal(chunk)
		event("content_block_delta", fmt.Sprintf(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":%s}}`, encoded))
	}

	event("content_block_stop", `{"type":"content_block_stop","index":0}`)
	event("message_delta", fmt.Sprintf(`{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":%d}}`, outputTokens))
	event("message_stop", `{"type":"message_stop"}`)

	return Response{
		Body:   string(b),
		Header: http.Header{"Content-Type": []string{"text/event-stream"}},
	}
}
