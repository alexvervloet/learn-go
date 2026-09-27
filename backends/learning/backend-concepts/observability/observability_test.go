package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestRedactedCannotLeakThroughAnyVerb is the point of the type.
func TestRedactedCannotLeakThroughAnyVerb(t *testing.T) {
	const secret = "sk_live_9f8e7d6c5b4a3210"

	type config struct {
		Host  string
		Token Redacted[string]
	}

	cfg := config{Host: "api.example.test", Token: Redact(secret)}

	// Every way a value reaches output.
	var buf bytes.Buffer

	log := slog.New(slog.NewJSONHandler(&buf, nil))
	log.Info("starting", "config", cfg, "token", cfg.Token)

	rendered := []string{
		buf.String(),
		fmt.Sprintf("%v", cfg),
		fmt.Sprintf("%+v", cfg),
		fmt.Sprintf("%#v", cfg),
		cfg.Token.String(),
		fmt.Sprint(cfg),
	}

	encoded, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	rendered = append(rendered, string(encoded))

	for i, s := range rendered {
		if strings.Contains(s, secret) {
			t.Errorf("rendering %d leaked the secret: %s", i, s)
		}
	}

	t.Logf("slog JSON:  %s", strings.TrimSpace(buf.String()))
	t.Logf("%%+v:        %+v", cfg)
	t.Logf("%%#v:        %#v", cfg)
	t.Logf("json:       %s", encoded)

	// And the value is still reachable for the code that needs it.
	if cfg.Token.Value() != secret {
		t.Error("Value() did not return the secret")
	}

	t.Log("four interfaces are needed: LogValuer for slog, Stringer for the plain and string " +
		"verbs, GoStringer for the Go-syntax verb, and Marshaler for JSON. Implementing " +
		"three of four leaks on one verb.")
}

// TestRedactedWithoutGoStringerWouldLeak demonstrates why the third interface is not optional.
func TestRedactedWithoutGoStringerWouldLeak(t *testing.T) {
	// A type with LogValuer and Stringer but no GoStringer, which is what most implementations of this
	// idea have.
	type partial struct{ value string }

	// Not implementing anything, to show what %#v does to a plain struct.
	p := partial{value: "sk_live_leaked"}

	withHash := fmt.Sprintf("%#v", p)

	t.Logf("a plain struct under %%#v: %s", withHash)

	if !strings.Contains(withHash, "sk_live_leaked") {
		t.Skip("the Go-syntax verb did not print the field, so this demonstration does not apply")
	}

	t.Log("the Go-syntax verb prints unexported fields through reflection, and it is what a " +
		"panic message and a testify diff use. That is the verb a Stringer-only redaction " +
		"misses.")

	// The full version does not.
	full := Redact("sk_live_leaked")

	if strings.Contains(fmt.Sprintf("%#v", full), "sk_live_leaked") {
		t.Error("Redacted leaked under the Go-syntax verb")
	}
}

// TestContextHandlerAttachesRequestScopedAttributes.
func TestContextHandlerAttachesRequestScopedAttributes(t *testing.T) {
	var buf bytes.Buffer

	log := slog.New(ContextHandler{Handler: slog.NewJSONHandler(&buf, nil)})

	ctx := WithRequestID(context.Background(), "req-abc123")
	ctx = WithSpanContext(ctx, SpanContext{
		TraceID: "4bf92f3577b34da6a3ce929d0e0e4736",
		SpanID:  "00f067aa0ba902b7",
		Sampled: true,
	})

	log.InfoContext(ctx, "handled")

	line := decodeLine(t, buf.String())

	t.Logf("%s", strings.TrimSpace(buf.String()))

	for key, want := range map[string]string{
		"request_id": "req-abc123",
		"trace_id":   "4bf92f3577b34da6a3ce929d0e0e4736",
		"span_id":    "00f067aa0ba902b7",
	} {
		if line[key] != want {
			t.Errorf("%s is %v, want %q", key, line[key], want)
		}
	}

	// Without the context, nothing is added and nothing breaks.
	buf.Reset()
	log.Info("no context")

	line = decodeLine(t, buf.String())

	if _, ok := line["request_id"]; ok {
		t.Error("a request id appeared with no context")
	}
}

// TestWithAttrsPreservesTheWrapper is the bug in most Handler wrappers.
func TestWithAttrsPreservesTheWrapper(t *testing.T) {
	var buf bytes.Buffer

	log := slog.New(ContextHandler{Handler: slog.NewJSONHandler(&buf, nil)})

	ctx := WithRequestID(context.Background(), "req-xyz")

	// log.With, which is what every real call site does.
	withField := log.With("component", "billing")

	withField.InfoContext(ctx, "charged")

	line := decodeLine(t, buf.String())

	t.Logf("%s", strings.TrimSpace(buf.String()))

	if line["component"] != "billing" {
		t.Errorf("the attribute from With is missing: %v", line)
	}

	// The one that matters. A wrapper that does not implement WithAttrs returns the INNER handler
	// here, so the request id silently disappears for every logger built with With, which is most of
	// them.
	if line["request_id"] != "req-xyz" {
		t.Error("the request id was lost through log.With, so WithAttrs is not forwarding " +
			"the wrapper")
	}

	// WithGroup keeps the wrapper too, and puts the attributes INSIDE the group, which is not what I
	// assumed and is what the Handler API allows.
	buf.Reset()

	log.WithGroup("http").InfoContext(ctx, "grouped")

	line = decodeLine(t, buf.String())

	t.Logf("%s", strings.TrimSpace(buf.String()))

	if _, atTopLevel := line["request_id"]; atTopLevel {
		t.Error("the request id is at the top level inside a group, which contradicts the " +
			"documented behaviour")
	}

	group, ok := line["http"].(map[string]any)
	if !ok {
		t.Fatalf("no http group in %v; the wrapper was lost through WithGroup", line)
	}

	if group["request_id"] != "req-xyz" {
		t.Errorf("the request id is not in the group either: %v", group)
	}

	t.Log("the wrapper survives WithGroup and the attribute lands at http.request_id rather " +
		"than request_id. By the time Handle runs the inner handler already knows it is in a " +
		"group, and a wrapper cannot add a top-level attribute to it. A log query for " +
		"request_id will not match, which is worth knowing before building a dashboard.")
}

// TestLevelFilterDropsBeforeFormatting is where the cost saving is.
func TestLevelFilterDropsBeforeFormatting(t *testing.T) {
	var buf bytes.Buffer

	// A handler that records whether it was asked to handle anything.
	inner := &countingHandler{Handler: slog.NewJSONHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})}

	log := NewLogger(inner, slog.LevelInfo)

	ctx := context.Background()

	// Below the always-level and not sampled: dropped.
	log.DebugContext(ctx, "cheap", "expensive", expensiveValue{t: t})

	if inner.handled != 0 {
		t.Errorf("the handler was called %d times for a dropped line", inner.handled)
	}
	if buf.Len() != 0 {
		t.Errorf("a dropped line produced output: %s", buf.String())
	}

	t.Log("the dropped debug line did not call LogValue on its argument, so the expensive " +
		"attribute was never computed")

	// At or above the always-level: kept.
	log.InfoContext(ctx, "kept")

	if inner.handled != 1 {
		t.Errorf("the handler was called %d times, want 1", inner.handled)
	}

	// Below the level but SAMPLED: kept, which is the point of per-request sampling.
	sampled := WithSampled(ctx, true)

	log.DebugContext(sampled, "kept because sampled")

	if inner.handled != 2 {
		t.Errorf("a sampled debug line was dropped")
	}

	t.Log("sampling is decided per REQUEST, not per line, so a sampled trace has no holes in it")

	// And errors are always kept.
	log.ErrorContext(ctx, "boom")

	if inner.handled != 3 {
		t.Error("an error was dropped")
	}
}

// expensiveValue panics if it is ever rendered, which is how the test proves the work was skipped.
type expensiveValue struct{ t *testing.T }

func (e expensiveValue) LogValue() slog.Value {
	e.t.Error("LogValue was called on a dropped line, so the arguments were evaluated")
	return slog.StringValue("should not happen")
}

type countingHandler struct {
	slog.Handler
	handled int
}

func (c *countingHandler) Handle(ctx context.Context, r slog.Record) error {
	c.handled++
	return c.Handler.Handle(ctx, r)
}

func (c *countingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &countingHandler{Handler: c.Handler.WithAttrs(attrs)}
}

func (c *countingHandler) WithGroup(name string) slog.Handler {
	return &countingHandler{Handler: c.Handler.WithGroup(name)}
}

// TestLogInjection, and what structured logging does about it.
func TestLogInjection(t *testing.T) {
	attack := "alice\nlevel=INFO msg=\"admin granted\" user=attacker"

	// JSON escapes it, which is the first reason to prefer a structured format.
	var jsonBuf bytes.Buffer
	slog.New(slog.NewJSONHandler(&jsonBuf, nil)).Info("login", "user", attack)

	jsonLines := strings.Count(strings.TrimSpace(jsonBuf.String()), "\n") + 1

	t.Logf("JSON handler produced %d line(s): %s", jsonLines, strings.TrimSpace(jsonBuf.String()))

	if jsonLines != 1 {
		t.Errorf("the JSON handler produced %d lines, so the newline was not escaped", jsonLines)
	}

	// The text handler quotes the value, which also holds.
	var textBuf bytes.Buffer
	slog.New(slog.NewTextHandler(&textBuf, nil)).Info("login", "user", attack)

	textLines := strings.Count(strings.TrimSpace(textBuf.String()), "\n") + 1

	t.Logf("text handler produced %d line(s)", textLines)

	// And the version that does not: a hand-formatted line.
	handRolled := fmt.Sprintf("level=INFO msg=login user=%s", attack)
	handRolledLines := strings.Count(strings.TrimSpace(handRolled), "\n") + 1

	t.Logf("fmt.Sprintf produced %d lines:", handRolledLines)
	for _, line := range strings.Split(handRolled, "\n") {
		t.Logf("  %s", line)
	}

	if handRolledLines == 1 {
		t.Error("the hand-rolled line did not split, so this demonstration is broken")
	}

	// Sanitize is for the values that reach a non-structured sink anyway.
	clean := Sanitize(attack)

	if strings.Contains(clean, "\n") {
		t.Errorf("Sanitize left a newline in %q", clean)
	}

	t.Logf("Sanitize: %q", clean)

	// It leaves ordinary values alone, including tabs, and does not allocate for them.
	for _, s := range []string{"alice", "", "a\tb", "user@example.test", "héllo"} {
		if got := Sanitize(s); got != s {
			t.Errorf("Sanitize(%q) = %q", s, got)
		}
	}

	// And it catches the escape sequences a terminal would interpret, which is the other half of log
	// injection: ANSI codes in a log can rewrite what an operator sees.
	if got := Sanitize("normal\x1b[2Kfake"); strings.Contains(got, "\x1b") {
		t.Errorf("Sanitize left an escape character in %q", got)
	}
}

func decodeLine(t *testing.T, s string) map[string]any {
	t.Helper()

	line := strings.TrimSpace(s)
	if i := strings.Index(line, "\n"); i >= 0 {
		line = line[:i]
	}

	var m map[string]any
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		t.Fatalf("decoding %q: %v", line, err)
	}

	return m
}

// TestNoNilLoggerFromContext, because a nil logger panics inside an error path.
func TestNoNilLoggerFromContext(t *testing.T) {
	// Nothing in the context and no fallback.
	if Logger(context.Background(), nil) == nil {
		t.Fatal("Logger returned nil")
	}

	// A nil logger explicitly in the context, which is what a careless WithLogger does.
	//nolint:staticcheck // deliberately storing nil
	ctx := WithLogger(context.Background(), nil)

	fallback := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))

	if got := Logger(ctx, fallback); got != fallback {
		t.Error("a nil logger in the context was returned instead of the fallback")
	}

	t.Log("Logger never returns nil, because the place a nil-pointer panic hurts most is the " +
		"error path that was about to explain the failure")
}

// TestFullStackMiddleware wires all three together, which is the only way to see that they agree on the trace
// id.
func TestFullStackMiddleware(t *testing.T) {
	var buf bytes.Buffer

	recorder := &Recorder{}
	tracer := &Tracer{Recorder: recorder}

	metrics := NewMetrics("test", RouteFromPattern, nil)

	log := NewLogger(slog.NewJSONHandler(&buf, nil), slog.LevelInfo)

	mux := http.NewServeMux()
	mux.Handle("GET /users/{id}", CaptureRoute(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			Logger(r.Context(), log).InfoContext(r.Context(), "loading user",
				"id", r.PathValue("id"))

			_, _ = w.Write([]byte(`{"id":"` + r.PathValue("id") + `"}`))
		})))

	// CaptureRoute is registered INSIDE the mux, because http.ServeMux sets r.Pattern when it
	// matches and an outer middleware runs before that. Without it every metric is labelled
	// "unmatched", which is what the first version of this test found.
	routed := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := WithRequestID(r.Context(), "req-1")
		ctx = WithLogger(ctx, log)

		mux.ServeHTTP(w, r.WithContext(ctx))
	})

	handler := tracer.Middleware(metrics.Middleware(routed))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/users/42", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d", rec.Code)
	}

	t.Logf("response traceparent: %s", rec.Header().Get("traceparent"))
	t.Logf("log line: %s", strings.TrimSpace(buf.String()))

	// The log line and the response header carry the same trace id, which is what makes a user's
	// bug report actionable.
	sc, err := ParseTraceparent(rec.Header().Get("traceparent"))
	if err != nil {
		t.Fatal(err)
	}

	line := decodeLine(t, buf.String())

	if line["trace_id"] != sc.TraceID {
		t.Errorf("the log says trace_id=%v and the response header says %s",
			line["trace_id"], sc.TraceID)
	}
	if line["request_id"] != "req-1" {
		t.Errorf("request_id is %v", line["request_id"])
	}

	// One span, recorded.
	spans := recorder.Spans()
	if len(spans) != 1 {
		t.Fatalf("recorded %d spans, want 1", len(spans))
	}
	if spans[0].TraceID != sc.TraceID {
		t.Errorf("the span's trace id is %s", spans[0].TraceID)
	}

	t.Logf("span: %s took %v, trace %s", spans[0].Name, spans[0].Duration, spans[0].TraceID)

	// And the metric used the ROUTE, not the path.
	series, err := seriesNames(metrics)
	if err != nil {
		t.Fatal(err)
	}

	found := false
	for _, s := range series {
		if strings.Contains(s, `route="/users/{id}"`) {
			found = true
		}
		if strings.Contains(s, `route="/users/42"`) {
			t.Errorf("a metric was labelled with the raw path: %s", s)
		}
	}

	if !found {
		t.Errorf("no metric labelled with the route; got %v", series)
	}

	t.Logf("the metric label is the route: %v", series)
}

func seriesNames(m *Metrics) ([]string, error) {
	families, err := m.Registry.Gather()
	if err != nil {
		return nil, err
	}

	var out []string

	for _, family := range families {
		for _, metric := range family.GetMetric() {
			labels := make([]string, 0, len(metric.GetLabel()))
			for _, l := range metric.GetLabel() {
				labels = append(labels, fmt.Sprintf("%s=%q", l.GetName(), l.GetValue()))
			}

			out = append(out, family.GetName()+"{"+strings.Join(labels, ",")+"}")
		}
	}

	return out, nil
}

// TestSamplingNeedsADebugInnerHandler is the misconfiguration a benchmark found, pinned down.
//
// LevelFilter gates on its own Always level and then defers to the inner handler. slog's handlers default to
// Info, so an inner handler built without options drops every debug line whatever the sampling says, and the
// feature silently does nothing.
func TestSamplingNeedsADebugInnerHandler(t *testing.T) {
	sampled := WithSampled(context.Background(), true)

	// The wrong way: the inner handler is at slog's default of Info.
	var wrongBuf bytes.Buffer

	wrong := NewLogger(slog.NewJSONHandler(&wrongBuf, nil), slog.LevelInfo)
	wrong.DebugContext(sampled, "should appear and does not")

	t.Logf("inner handler at the default level, sampled debug line: %d bytes of output",
		wrongBuf.Len())

	if wrongBuf.Len() != 0 {
		t.Error("the default inner handler emitted a debug line, so this trap no longer exists")
	}

	// The right way.
	var rightBuf bytes.Buffer

	right := NewLogger(slog.NewJSONHandler(&rightBuf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}), slog.LevelInfo)

	right.DebugContext(sampled, "appears")

	t.Logf("inner handler at LevelDebug, sampled debug line: %s",
		strings.TrimSpace(rightBuf.String()))

	if rightBuf.Len() == 0 {
		t.Error("a sampled debug line was dropped by a debug-level inner handler")
	}

	// And an UNsampled debug line is still dropped, which is the whole point of the filter.
	rightBuf.Reset()

	right.DebugContext(context.Background(), "dropped")

	if rightBuf.Len() != 0 {
		t.Errorf("an unsampled debug line was written: %s", rightBuf.String())
	}

	t.Log("the levels are set in two places and only one of them is obvious. A feature that " +
		"silently does nothing is worse than one that fails, which is why this is a test " +
		"rather than a sentence in a README.")
}
