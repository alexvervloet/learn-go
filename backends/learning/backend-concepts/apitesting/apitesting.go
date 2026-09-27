// Package apitesting is testing an HTTP API from the outside, and the choices that decide whether the tests
// are worth keeping.
//
// # What this does not cover
//
// The mechanics of httptest, table-driven tests, golden files and fakes are in testing-concepts, and handler
// testing is in http-tutorial. This is the layer above: testing a whole service, testing what it sends to
// other services, and writing assertions that survive the code changing.
//
// # The three ways to invoke a handler, and when each is right
//
//	handler.ServeHTTP(rec, req)     no network, no server, microseconds. Tests ONE handler and
//	                                skips the router, the middleware and the server's own
//	                                parsing. Right for a handler's logic.
//	httptest.NewServer(mux)         a real listener on a real port. Tests routing, middleware,
//	                                the client, TLS, redirects, timeouts and anything that
//	                                depends on the wire. Milliseconds. Right for a service.
//	the real binary in a container  tests the configuration and the startup path too. Seconds to
//	                                minutes. Right for a handful of smoke tests and nothing else.
//
// Most suites pick one and use it for everything. The first misses every integration bug and the second is
// slow enough that people stop running it.
//
// # The assertion that survives
//
// A test asserting on a complete JSON response breaks every time a field is added, and the fix is always to
// update the expected value, which means nobody reads the diff. A test asserting on the fields it cares about
// keeps working and says what it means. Both are in here, with the tradeoff stated: the loose one misses a
// field that disappeared, so something has to pin the shape, and that something is a schema test rather than
// every test.
package apitesting

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
)

// Response is a captured HTTP response, with helpers that say what failed rather than that something did.
type Response struct {
	Status  int
	Header  http.Header
	Body    []byte
	Elapsed time.Duration
}

// JSON decodes the body into v.
func (r Response) JSON(v any) error {
	if err := json.Unmarshal(r.Body, v); err != nil {
		return fmt.Errorf("decoding %q: %w", truncate(r.Body, 200), err)
	}
	return nil
}

// Map decodes the body into a generic map, for the assertions that do not need a type.
func (r Response) Map() (map[string]any, error) {
	var m map[string]any

	if err := r.JSON(&m); err != nil {
		return nil, err
	}

	return m, nil
}

// String renders the response for a failure message.
//
// A failing HTTP test whose message is "expected 200, got 500" sends the reader to the logs. One that prints
// the status, the content type and the body answers the question in the test output, and a 500 usually has the
// answer in its body.
func (r Response) String() string {
	var b strings.Builder

	fmt.Fprintf(&b, "%d %s", r.Status, http.StatusText(r.Status))

	if ct := r.Header.Get("Content-Type"); ct != "" {
		fmt.Fprintf(&b, " (%s)", ct)
	}

	if len(r.Body) > 0 {
		fmt.Fprintf(&b, "\n%s", truncate(r.Body, 800))
	}

	return b.String()
}

// Normalise replaces values that change between runs, so a response can be compared to a golden file.
//
// # The three that always need it
//
//	timestamps  a response containing time.Now differs on every run
//	ids         a uuid or a database sequence differs on every run
//	durations   anything measured
//
// The naive fix is to assert on every other field and skip these, which is fine until the response nests and
// the test becomes forty assertions. Replacing them with a marker keeps the whole-document comparison and its
// readable diff.
//
// The important part is that it replaces the VALUE and keeps the KEY, so a field disappearing is still caught.
// A normaliser that deletes the field cannot tell "the timestamp changed" from "the timestamp is gone".
func Normalise(body []byte, fields ...string) ([]byte, error) {
	var v any

	if err := json.Unmarshal(body, &v); err != nil {
		return nil, fmt.Errorf("normalising: %w", err)
	}

	want := make(map[string]struct{}, len(fields))
	for _, f := range fields {
		want[f] = struct{}{}
	}

	normalised := normaliseValue(v, want)

	// An Encoder with SetEscapeHTML(false), not json.Marshal.
	//
	// json.Marshal escapes <, > and & as \u003c, \u003e and \u0026, unconditionally. That is a
	// default from the days of embedding JSON in a <script> tag, and it is still on in 2026 for
	// every use of the package. The first version of this golden file read
	// "created_at": "\u003cnormalised\u003e", which is correct JSON and unreadable in a diff,
	// which defeats the only reason to write a golden file.
	//
	// The Encoder is the only way to turn it off. There is no option on Marshal.
	var buf bytes.Buffer

	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")

	if err := enc.Encode(normalised); err != nil {
		return nil, fmt.Errorf("re-encoding: %w", err)
	}

	// Encode already appends a newline, which Marshal does not. One more small difference between
	// the two that catches people comparing their output.
	return buf.Bytes(), nil
}

func normaliseValue(v any, fields map[string]struct{}) any {
	switch typed := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))

		// Sorted, because Go's map iteration is randomised and json.Marshal sorts keys anyway.
		// Doing it here keeps the nested handling deterministic too.
		for _, key := range slices.Sorted(maps.Keys(typed)) {
			if _, ok := fields[key]; ok {
				out[key] = "<normalised>"
				continue
			}

			out[key] = normaliseValue(typed[key], fields)
		}

		return out

	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = normaliseValue(item, fields)
		}
		return out

	default:
		return v
	}
}

// Shape describes the fields a response must have, without pinning their values.
//
// # Why this rather than a full comparison
//
// A test that compares the whole document breaks when a field is added, which is a change that broke nothing.
// A test that checks three fields keeps passing when a field is REMOVED, which is a change that broke
// everything.
//
// Shape is the middle: it asserts that these keys exist with these types, and says nothing about anything else.
// One shape test per endpoint pins the contract, and the behavioural tests around it assert on the values they
// care about.
type Shape map[string]string

// Check reports every mismatch rather than the first.
//
// Every one, because a shape with four problems fixed one per run is four runs. Returning a slice rather than
// an error is what makes that natural at the call site.
func (s Shape) Check(body []byte) []string {
	var v map[string]any

	if err := json.Unmarshal(body, &v); err != nil {
		return []string{fmt.Sprintf("body is not a JSON object: %v", err)}
	}

	var problems []string

	for _, field := range slices.Sorted(maps.Keys(s)) {
		want := s[field]

		actual, ok := lookup(v, field)
		if !ok {
			problems = append(problems, fmt.Sprintf("%s: missing", field))
			continue
		}

		if got := jsonType(actual); got != want {
			problems = append(problems,
				fmt.Sprintf("%s: got %s, want %s", field, got, want))
		}
	}

	return problems
}

// lookup resolves a dotted path, so a shape can describe nested fields.
func lookup(v map[string]any, path string) (any, bool) {
	parts := strings.Split(path, ".")

	var current any = v

	for _, part := range parts {
		m, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}

		current, ok = m[part]
		if !ok {
			return nil, false
		}
	}

	return current, true
}

// jsonType names a decoded JSON value's type.
//
// The distinction between a number and an integer is deliberately absent: JSON has one number type and Go
// decodes every one into a float64, so a shape asserting "integer" would be asserting something the format does
// not have. A test that needs it decodes into a typed struct instead.
func jsonType(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case float64:
		return "number"
	case string:
		return "string"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return fmt.Sprintf("%T", v)
	}
}

// RecordingTransport is a fake http.RoundTripper, for testing what a service SENDS.
//
// # Why a RoundTripper and not an httptest.Server
//
// Both work. A server is more realistic: the request is serialised, sent over a socket and parsed, so a bug in
// how a header is set shows up. A RoundTripper is faster, needs no port, and gives the request as a *http.Request
// rather than as bytes to parse.
//
// The rule that decides it: if the test is about the REQUEST, use a RoundTripper and assert on the object. If
// it is about the RESPONSE handling (timeouts, retries, redirects, connection reuse), use a server, because
// those behaviours live in the transport that a fake RoundTripper replaces.
type RecordingTransport struct {
	// Respond returns the response for a request. A function rather than a fixed response, so one
	// transport can serve a sequence, fail the third call, or vary by path.
	Respond func(*http.Request) (*http.Response, error)

	mu       sync.Mutex
	requests []*http.Request
	bodies   [][]byte
}

// RoundTrip implements http.RoundTripper.
func (t *RecordingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	// The body has to be read here and put back, because a RoundTripper receives a stream that the
	// caller will not be able to read again. A recorder that stores the request without its body
	// records the half nobody needed.
	var body []byte

	if r.Body != nil {
		var err error

		body, err = io.ReadAll(r.Body)
		if err != nil {
			return nil, fmt.Errorf("reading the request body: %w", err)
		}

		_ = r.Body.Close()

		r.Body = io.NopCloser(bytes.NewReader(body))
	}

	t.mu.Lock()
	t.requests = append(t.requests, r.Clone(r.Context()))
	t.bodies = append(t.bodies, body)
	t.mu.Unlock()

	if t.Respond == nil {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader("{}")),
			Request:    r,
		}, nil
	}

	return t.Respond(r)
}

// Requests returns what was sent.
func (t *RecordingTransport) Requests() []*http.Request {
	t.mu.Lock()
	defer t.mu.Unlock()

	return slices.Clone(t.requests)
}

// Body returns the body of the nth request.
func (t *RecordingTransport) Body(n int) []byte {
	t.mu.Lock()
	defer t.mu.Unlock()

	if n < 0 || n >= len(t.bodies) {
		return nil
	}

	return t.bodies[n]
}

// Client returns an http.Client using this transport.
func (t *RecordingTransport) Client() *http.Client {
	return &http.Client{Transport: t}
}

// JSONResponse builds a response for a Respond function.
func JSONResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// Do sends a request to a handler without a network.
//
// httptest.NewRequest, not http.NewRequest, and the difference matters: NewRequest builds a CLIENT request,
// whose RemoteAddr is empty and whose URL is absolute, and a handler that reads either gets something the real
// server would never send. httptest.NewRequest builds a SERVER request, with RemoteAddr set and the URL
// relative, which is what ServeHTTP would receive.
func Do(h http.Handler, method, target string, body any) (Response, error) {
	var reader io.Reader

	if body != nil {
		switch typed := body.(type) {
		case string:
			reader = strings.NewReader(typed)
		case []byte:
			reader = bytes.NewReader(typed)
		default:
			encoded, err := json.Marshal(body)
			if err != nil {
				return Response{}, fmt.Errorf("encoding the request body: %w", err)
			}
			reader = bytes.NewReader(encoded)
		}
	}

	req := httptest.NewRequest(method, target, reader)

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	rec := httptest.NewRecorder()

	start := time.Now()
	h.ServeHTTP(rec, req)
	elapsed := time.Since(start)

	result := rec.Result()
	defer func() { _ = result.Body.Close() }()

	responseBody, err := io.ReadAll(result.Body)
	if err != nil {
		return Response{}, fmt.Errorf("reading the response body: %w", err)
	}

	return Response{
		Status:  result.StatusCode,
		Header:  result.Header,
		Body:    responseBody,
		Elapsed: elapsed,
	}, nil
}

// uuidPattern and timePattern find values that change between runs, for a normaliser that does not know the
// field names.
//
// Used by NormaliseText below, which is the fallback for a response that is not JSON. Regex-replacing values in
// a structured document is worse than walking it, which is why the JSON path does not use these.
var (
	uuidPattern = regexp.MustCompile(
		`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)

	timePattern = regexp.MustCompile(
		`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})`)
)

// NormaliseText replaces uuids and RFC 3339 timestamps in a non-JSON body.
func NormaliseText(body []byte) []byte {
	out := uuidPattern.ReplaceAll(body, []byte("<uuid>"))
	return timePattern.ReplaceAll(out, []byte("<timestamp>"))
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + fmt.Sprintf("... (%d bytes total)", len(b))
}
