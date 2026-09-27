package apitesting

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// update rewrites the golden files, the same flag testing-concepts uses.
var update = flag.Bool("update", false, "rewrite the golden files")

// service is the thing under test: a small API with a router, middleware and an upstream call.
//
// Deliberately more than one handler, because the point of this package is testing the layers a single-handler
// test skips.
type service struct {
	client *http.Client
	now    func() time.Time
}

func (s *service) handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /orders/{id}", s.getOrder)
	mux.HandleFunc("POST /orders", s.createOrder)
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
	})

	// One middleware, so a test can tell whether it ran.
	return requestID(mux)
}

func (s *service) getOrder(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	if id == "missing" {
		writeJSON(w, http.StatusNotFound, map[string]any{
			"type":   "about:blank",
			"title":  "Not Found",
			"status": 404,
			"detail": "no order with id " + id,
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"id":         id,
		"status":     "paid",
		"total":      1999,
		"created_at": s.now().UTC().Format(time.RFC3339Nano),
		"customer": map[string]any{
			"id":    "cust_42",
			"email": "a@example.test",
		},
		"lines": []any{
			map[string]any{"sku": "BOOK-1", "quantity": 2, "price": 999},
			map[string]any{"sku": "BOOK-2", "quantity": 1, "price": 1},
		},
	})
}

// createOrder calls an upstream payment service, so there is something to record.
func (s *service) createOrder(w http.ResponseWriter, r *http.Request) {
	var body struct {
		CustomerID string `json:"customer_id"`
		Total      int    `json:"total"`
	}

	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"type": "about:blank", "title": "Bad Request", "status": 400,
			"detail": "invalid JSON",
		})
		return
	}

	req, err := http.NewRequestWithContext(r.Context(), "POST",
		"https://payments.internal/charges",
		strings.NewReader(fmt.Sprintf(`{"amount":%d,"currency":"GBP"}`, body.Total)))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"detail": err.Error()})
		return
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "order-"+body.CustomerID)

	resp, err := s.client.Do(req)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"type": "about:blank", "title": "Bad Gateway", "status": 502,
			"detail": "the payment service is unavailable",
		})
		return
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"type": "about:blank", "title": "Bad Gateway", "status": 502,
			"detail": fmt.Sprintf("the payment service returned %d", resp.StatusCode),
		})
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"id":         "ord_new",
		"status":     "paid",
		"created_at": s.now().UTC().Format(time.RFC3339Nano),
	})
}

func requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Request-Id", "req-fixed-for-tests")
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	_ = json.NewEncoder(w).Encode(v)
}

func newService(t *testing.T, transport *RecordingTransport) *service {
	t.Helper()

	client := &http.Client{}
	if transport != nil {
		client = transport.Client()
	}

	return &service{
		client: client,
		now:    func() time.Time { return time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC) },
	}
}

// TestThreeWaysToInvoke measures the cost of each, because the choice is usually made on habit.
func TestThreeWaysToInvoke(t *testing.T) {
	s := newService(t, nil)
	h := s.handler()

	// 1. Direct, no network.
	direct, err := Do(h, "GET", "/orders/ord_1", nil)
	if err != nil {
		t.Fatal(err)
	}

	// 2. A real server on a real port.
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	start := time.Now()

	resp, err := http.Get(srv.URL + "/orders/ord_1")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	overServer := time.Since(start)

	t.Logf("handler.ServeHTTP: %v", direct.Elapsed.Round(time.Microsecond))
	t.Logf("httptest.Server:   %v", overServer.Round(time.Microsecond))
	t.Logf("about %.0fx", float64(overServer)/float64(direct.Elapsed))

	if direct.Status != http.StatusOK || resp.StatusCode != http.StatusOK {
		t.Errorf("statuses %d and %d", direct.Status, resp.StatusCode)
	}

	// Both ran the middleware, because both went through the router.
	if direct.Header.Get("X-Request-Id") == "" {
		t.Error("the direct call skipped the middleware")
	}
	if resp.Header.Get("X-Request-Id") == "" {
		t.Error("the server call skipped the middleware")
	}

	t.Log("both went through the mux and the middleware, because the handler under test IS " +
		"the whole stack. Calling a single HandlerFunc directly is what skips them, and that " +
		"is the distinction that matters, not httptest.Server against ServeHTTP.")
}

// TestServerRequestIsNotAClientRequest is the httptest.NewRequest difference.
func TestServerRequestIsNotAClientRequest(t *testing.T) {
	server := httptest.NewRequest("GET", "/orders/1", nil)

	client, err := http.NewRequest("GET", "http://example.test/orders/1", nil)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("httptest.NewRequest: RemoteAddr=%q URL=%q RequestURI=%q",
		server.RemoteAddr, server.URL.String(), server.RequestURI)
	t.Logf("http.NewRequest:     RemoteAddr=%q URL=%q RequestURI=%q",
		client.RemoteAddr, client.URL.String(), client.RequestURI)

	if server.RemoteAddr == "" {
		t.Error("httptest.NewRequest left RemoteAddr empty")
	}
	if client.RemoteAddr != "" {
		t.Error("http.NewRequest set RemoteAddr")
	}

	// A handler that reads RemoteAddr for rate limiting or logging gets "" from a client request,
	// which is a key that every request shares.
	t.Log("a handler that keys a rate limit on RemoteAddr gets an empty string from a client " +
		"request, so every request in the test shares one bucket and the test passes for the " +
		"wrong reason")
}

// TestGoldenResponseWithNormalisation is the whole-document comparison, made stable.
func TestGoldenResponseWithNormalisation(t *testing.T) {
	s := newService(t, nil)

	resp, err := Do(s.handler(), "GET", "/orders/ord_1", nil)
	if err != nil {
		t.Fatal(err)
	}

	if resp.Status != http.StatusOK {
		t.Fatalf("got %s", resp)
	}

	// created_at changes on every run in a real service. Normalise replaces the VALUE and keeps the
	// KEY, so a field disappearing is still caught.
	normalised, err := Normalise(resp.Body, "created_at")
	if err != nil {
		t.Fatal(err)
	}

	assertGolden(t, "order.golden.json", normalised)

	// The key is still there, which a normaliser that deleted the field would not give.
	var m map[string]any
	if err := json.Unmarshal(normalised, &m); err != nil {
		t.Fatal(err)
	}

	if _, ok := m["created_at"]; !ok {
		t.Error("Normalise removed the field instead of replacing its value")
	}
	if m["created_at"] != "<normalised>" {
		t.Errorf("created_at is %v", m["created_at"])
	}

	// And the golden file is readable, which json.Marshal would not have given: it escapes < and
	// > unconditionally, so the marker would be written as \u003cnormalised\u003e.
	if strings.Contains(string(normalised), `\u003c`) {
		t.Error("the output is HTML-escaped, so a golden diff is unreadable")
	}

	t.Log("the whole document is compared, so an added field fails the test and the diff shows " +
		"exactly what changed. That is the tradeoff: it is noisy for intentional changes and " +
		"it never misses one.")
}

// TestNormaliseIsRecursive, because the interesting timestamps are nested.
func TestNormaliseIsRecursive(t *testing.T) {
	body := []byte(`{
		"id": "1",
		"created_at": "2025-06-01T12:00:00Z",
		"customer": {"id": "c1", "created_at": "2024-01-01T00:00:00Z"},
		"events": [
			{"type": "paid", "at": "2025-06-01T12:00:01Z"},
			{"type": "shipped", "at": "2025-06-02T09:00:00Z"}
		]
	}`)

	normalised, err := Normalise(body, "created_at", "at")
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("%s", normalised)

	if strings.Contains(string(normalised), "2025-06-01") {
		t.Error("a nested timestamp survived")
	}
	if strings.Contains(string(normalised), "2024-01-01") {
		t.Error("a timestamp inside an object survived")
	}
	if !strings.Contains(string(normalised), `"id": "c1"`) {
		t.Error("a nested field that should have been left alone was changed")
	}

	// Two normalisations of the same document are identical, which is what makes it usable as a
	// golden comparison at all.
	again, err := Normalise(body, "created_at", "at")
	if err != nil {
		t.Fatal(err)
	}

	if string(again) != string(normalised) {
		t.Error("two normalisations differ, so map iteration order is leaking through")
	}
}

// TestShapeSurvivesAddedFieldsAndCatchesRemovedOnes is the argument for the middle assertion.
func TestShapeSurvivesAddedFields(t *testing.T) {
	shape := Shape{
		"id":             "string",
		"status":         "string",
		"total":          "number",
		"customer.id":    "string",
		"customer.email": "string",
		"lines":          "array",
	}

	s := newService(t, nil)

	resp, err := Do(s.handler(), "GET", "/orders/ord_1", nil)
	if err != nil {
		t.Fatal(err)
	}

	if problems := shape.Check(resp.Body); len(problems) != 0 {
		t.Errorf("the response does not match the shape: %v", problems)
	}

	// A field ADDED: the shape still passes, because it says nothing about fields it does not name.
	withExtra := addField(t, resp.Body, "loyalty_points", 250)

	if problems := shape.Check(withExtra); len(problems) != 0 {
		t.Errorf("adding a field broke the shape: %v", problems)
	}

	t.Log("an added field does not fail the shape, which is what stops every test in the suite " +
		"needing an update for a change that broke nothing")

	// A field REMOVED or RETYPED: caught, and every problem is reported rather than the first.
	broken := []byte(`{"id":"1","total":"1999","customer":{"id":"c1"},"lines":[]}`)

	problems := shape.Check(broken)

	t.Logf("a response missing two fields and with one wrong type:")
	for _, p := range problems {
		t.Logf("  %s", p)
	}

	if len(problems) != 3 {
		t.Errorf("reported %d problems, want 3: status missing, total retyped, "+
			"customer.email missing", len(problems))
	}

	t.Log("all three at once, because fixing one per run is three runs")
}

// TestShapeChecksNestedPaths.
func TestShapeChecksNestedPaths(t *testing.T) {
	body := []byte(`{"a":{"b":{"c":"deep"}},"n":null,"t":true,"arr":[1,2]}`)

	for _, tc := range []struct {
		shape Shape
		ok    bool
	}{
		{Shape{"a.b.c": "string"}, true},
		{Shape{"n": "null"}, true},
		{Shape{"t": "boolean"}, true},
		{Shape{"arr": "array"}, true},
		{Shape{"a.b": "object"}, true},
		{Shape{"a.b.c": "number"}, false},
		{Shape{"a.b.d": "string"}, false},
		{Shape{"a.x.c": "string"}, false},
		{Shape{"missing": "string"}, false},
	} {
		problems := tc.shape.Check(body)

		if tc.ok && len(problems) != 0 {
			t.Errorf("%v should have matched: %v", tc.shape, problems)
		}
		if !tc.ok && len(problems) == 0 {
			t.Errorf("%v should not have matched", tc.shape)
		}
	}

	// A number is a number, whatever it looks like. JSON has one numeric type and every value
	// decodes into a float64, so a shape cannot assert "integer".
	if problems := (Shape{"arr": "array"}).Check([]byte(`{"arr":{}}`)); len(problems) == 0 {
		t.Error("an object matched an array")
	}

	t.Log("JSON has one number type, so a shape asserting 'integer' would be asserting " +
		"something the format does not have. A test that needs it decodes into a struct.")
}

// TestRecordingTransportCapturesWhatWeSend is the other direction: testing the request, not the response.
func TestRecordingTransportCapturesWhatWeSend(t *testing.T) {
	transport := &RecordingTransport{
		Respond: func(*http.Request) (*http.Response, error) {
			return JSONResponse(http.StatusOK, `{"charge_id":"ch_1"}`), nil
		},
	}

	s := newService(t, transport)

	resp, err := Do(s.handler(), "POST", "/orders",
		map[string]any{"customer_id": "cust_42", "total": 1999})
	if err != nil {
		t.Fatal(err)
	}

	if resp.Status != http.StatusCreated {
		t.Fatalf("got %s", resp)
	}

	requests := transport.Requests()

	if len(requests) != 1 {
		t.Fatalf("the service made %d upstream calls, want 1", len(requests))
	}

	upstream := requests[0]

	t.Logf("upstream: %s %s", upstream.Method, upstream.URL)
	t.Logf("  Content-Type:    %s", upstream.Header.Get("Content-Type"))
	t.Logf("  Idempotency-Key: %s", upstream.Header.Get("Idempotency-Key"))
	t.Logf("  body:            %s", transport.Body(0))

	if upstream.URL.String() != "https://payments.internal/charges" {
		t.Errorf("called %s", upstream.URL)
	}
	if upstream.Header.Get("Idempotency-Key") != "order-cust_42" {
		t.Errorf("Idempotency-Key is %q", upstream.Header.Get("Idempotency-Key"))
	}

	// The body was captured AND was still readable by the client, which is the part a naive
	// recorder gets wrong.
	if got := string(transport.Body(0)); got != `{"amount":1999,"currency":"GBP"}` {
		t.Errorf("the body was %q", got)
	}

	t.Log("a RoundTripper receives a stream, so the recorder has to read the body and put it " +
		"back. One that just stores the request records the half nobody needed.")
}

// TestUpstreamFailuresBecomeGatewayErrors, which is the thing a RoundTripper makes easy to test.
func TestUpstreamFailuresBecomeGatewayErrors(t *testing.T) {
	for _, tc := range []struct {
		name    string
		respond func(*http.Request) (*http.Response, error)
		want    int
	}{
		{"upstream 200", func(*http.Request) (*http.Response, error) {
			return JSONResponse(http.StatusOK, `{"charge_id":"ch_1"}`), nil
		}, http.StatusCreated},

		{"upstream 500", func(*http.Request) (*http.Response, error) {
			return JSONResponse(http.StatusInternalServerError, `{"error":"boom"}`), nil
		}, http.StatusBadGateway},

		{"upstream 402", func(*http.Request) (*http.Response, error) {
			return JSONResponse(http.StatusPaymentRequired, `{"error":"declined"}`), nil
		}, http.StatusBadGateway},

		{"connection refused", func(*http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("dial tcp: connection refused")
		}, http.StatusBadGateway},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newService(t, &RecordingTransport{Respond: tc.respond})

			resp, err := Do(s.handler(), "POST", "/orders",
				map[string]any{"customer_id": "c", "total": 1})
			if err != nil {
				t.Fatal(err)
			}

			t.Logf("%s -> %d", tc.name, resp.Status)

			if resp.Status != tc.want {
				t.Errorf("got %s, want %d", resp, tc.want)
			}
		})
	}

	t.Log("every upstream failure becomes a 502 and none of them becomes a 500, which is the " +
		"distinction that tells an on-call engineer whose problem it is")
}

// TestFailureMessagesSayWhatHappened, because "expected 200, got 500" sends the reader to the logs.
func TestFailureMessagesSayWhatHappened(t *testing.T) {
	s := newService(t, nil)

	resp, err := Do(s.handler(), "GET", "/orders/missing", nil)
	if err != nil {
		t.Fatal(err)
	}

	rendered := resp.String()

	t.Logf("what a failure would print:\n%s", rendered)

	for _, want := range []string{"404", "Not Found", "application/json", "no order with id"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("the rendered response does not mention %q", want)
		}
	}

	// A long body is truncated with its real length, so a test that returns a megabyte does not
	// fill the output and still says how much there was.
	long := Response{Status: 200, Body: []byte(strings.Repeat("x", 5000))}

	rendered = long.String()

	if len(rendered) > 1000 {
		t.Errorf("a 5000-byte body rendered as %d characters", len(rendered))
	}
	if !strings.Contains(rendered, "5000 bytes total") {
		t.Error("the truncated render does not say how much was there")
	}
}

// TestNormaliseText is the fallback for a body that is not JSON.
func TestNormaliseText(t *testing.T) {
	body := []byte("order 3f7a1b2c-4d5e-6f70-8192-a3b4c5d6e7f8 created at 2025-06-01T12:00:00Z " +
		"and updated at 2025-06-01T12:34:56.789+01:00")

	got := NormaliseText(body)

	t.Logf("before: %s", body)
	t.Logf("after:  %s", got)

	if strings.Contains(string(got), "3f7a1b2c") {
		t.Error("the uuid survived")
	}
	if strings.Contains(string(got), "2025-06-01") {
		t.Error("a timestamp survived")
	}
	if !strings.Contains(string(got), "order <uuid> created") {
		t.Errorf("the surrounding text changed: %s", got)
	}

	t.Log("regex replacement is the fallback. For JSON, walking the document is better: it can " +
		"target a field by name and it cannot accidentally rewrite a value that happens to " +
		"look like a timestamp.")
}

func addField(t *testing.T, body []byte, key string, value any) []byte {
	t.Helper()

	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatal(err)
	}

	m[key] = value

	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}

	return out
}

// assertGolden compares against testdata, rewriting it under -update.
func assertGolden(t *testing.T, name string, got []byte) {
	t.Helper()

	path := filepath.Join("testdata", name)

	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o600); err != nil {
			t.Fatal(err)
		}

		t.Logf("updated %s", path)

		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v\nrun: go test ./apitesting -update", path, err)
	}

	if string(got) != string(want) {
		t.Errorf("%s differs:\n--- want\n%s\n--- got\n%s", path, want, got)
	}
}
