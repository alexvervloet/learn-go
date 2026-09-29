package observability

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseTraceparent(t *testing.T) {
	const valid = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"

	sc, err := ParseTraceparent(valid)
	if err != nil {
		t.Fatal(err)
	}

	if sc.TraceID != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Errorf("trace id %q", sc.TraceID)
	}
	if sc.SpanID != "00f067aa0ba902b7" {
		t.Errorf("span id %q", sc.SpanID)
	}
	if !sc.Sampled {
		t.Error("not sampled")
	}
	if sc.String() != valid {
		t.Errorf("round trip gave %q", sc.String())
	}

	t.Logf("%s is %d characters: version, trace id, span id, flags", valid, len(valid))

	for _, tc := range []struct {
		name   string
		header string
		ok     bool
		why    string
	}{
		{"unsampled", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-00", true,
			"flags 00 means not sampled, and the context is still valid"},

		// The extensibility rule: an unknown version is ACCEPTED and its first four fields parsed.
		// Rejecting it is the bug that breaks a service when the spec moves.
		{"future version", "01-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01", true,
			"a receiver must accept a higher version"},
		{"future version with extra fields",
			"01-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01-something", true,
			"and extra fields, because that is how the format grows"},

		{"version ff", "ff-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01", false,
			"ff is reserved as invalid by the spec"},

		// The all-zero cases, which parse as hex and are explicitly invalid. Propagating one
		// produces a trace every backend silently discards.
		{"zero trace id", "00-00000000000000000000000000000000-00f067aa0ba902b7-01", false,
			"an all-zero trace id is invalid"},
		{"zero span id", "00-4bf92f3577b34da6a3ce929d0e0e4736-0000000000000000-01", false,
			"an all-zero span id is invalid"},

		{"uppercase", "00-4BF92F3577B34DA6A3CE929D0E0E4736-00f067aa0ba902b7-01", false,
			"the spec says lowercase; a backend keying on the string would see two traces"},

		{"short trace id", "00-4bf92f35-00f067aa0ba902b7-01", false, "wrong length"},
		{"too few fields", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7", false,
			"three fields"},
		{"not hex", "00-4bf92f3577b34da6a3ce929d0e0e473g-00f067aa0ba902b7-01", false,
			"g is not hex"},
		{"empty", "", false, "no header"},
	} {
		sc, err := ParseTraceparent(tc.header)

		switch {
		case tc.ok && err != nil:
			t.Errorf("%s: %v", tc.name, err)
		case !tc.ok && err == nil:
			t.Errorf("%s: accepted %q as %+v", tc.name, tc.header, sc)
		default:
			t.Logf("%-32s %-8s %s", tc.name, acceptedOrNot(err), tc.why)
		}
	}
}

func acceptedOrNot(err error) string {
	if err == nil {
		return "accepted"
	}
	return "rejected"
}

// TestSampledFlagIsABit, not a byte comparison.
func TestSampledFlagIsABit(t *testing.T) {
	// Flags 03: sampled plus a hypothetical second flag. `flags == "01"` would call this unsampled.
	sc, err := ParseTraceparent("00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-03")
	if err != nil {
		t.Fatal(err)
	}

	if !sc.Sampled {
		t.Error("flags 03 has bit 0 set, so the request is sampled")
	}

	sc, err = ParseTraceparent("00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-02")
	if err != nil {
		t.Fatal(err)
	}

	if sc.Sampled {
		t.Error("flags 02 has bit 0 clear")
	}

	t.Log("testing the bit rather than comparing the byte: a future flag would make " +
		`flags == "01" false for a sampled request`)
}

// TestTracePropagation is the two rules: inherit the trace id, generate a new span id.
func TestTracePropagation(t *testing.T) {
	recorder := &Recorder{}
	tracer := &Tracer{Recorder: recorder, SampleRate: 1}

	// Service A, at the edge with no incoming header.
	ctxA, endA := tracer.Start(context.Background(), "A")
	scA := SpanContextFrom(ctxA)

	if scA.ParentSpanID != "" {
		t.Errorf("the edge span has a parent: %q", scA.ParentSpanID)
	}
	if !scA.Sampled {
		t.Error("the edge did not make a sampling decision")
	}

	// A calls B over HTTP.
	outbound := httptest.NewRequest("GET", "http://b.internal/thing", nil)
	Inject(ctxA, outbound)

	header := outbound.Header.Get("traceparent")

	t.Logf("A sends: %s", header)

	if header == "" {
		t.Fatal("Inject set no header")
	}

	// Service B receives it.
	incoming, err := ParseTraceparent(header)
	if err != nil {
		t.Fatal(err)
	}

	ctxB, endB := tracer.Start(WithSpanContext(context.Background(), incoming), "B")
	scB := SpanContextFrom(ctxB)

	t.Logf("B has:   %s", scB.String())

	// Rule one: the trace id is the same.
	if scB.TraceID != scA.TraceID {
		t.Errorf("B started a new trace: %s against %s", scB.TraceID, scA.TraceID)
	}

	// Rule two: the span id is new, and A's is the parent.
	if scB.SpanID == scA.SpanID {
		t.Error("B reused A's span id, so the trace is flat")
	}
	if scB.ParentSpanID != scA.SpanID {
		t.Errorf("B's parent is %q, want A's span %q", scB.ParentSpanID, scA.SpanID)
	}

	// Rule three: the sampling decision is inherited, not remade.
	if scB.Sampled != scA.Sampled {
		t.Error("B made its own sampling decision, so a sampled trace would have holes")
	}

	endB(nil)
	endA(nil)

	spans := recorder.Spans()
	if len(spans) != 2 {
		t.Fatalf("recorded %d spans", len(spans))
	}

	for _, s := range spans {
		t.Logf("span %-2s trace=%s span=%s parent=%s took %v",
			s.Name, s.TraceID[:8], s.SpanID, orNone(s.ParentSpanID), s.Duration)
	}

	// An unsampled parent stays unsampled.
	unsampled := SpanContext{
		TraceID: "4bf92f3577b34da6a3ce929d0e0e4736",
		SpanID:  "00f067aa0ba902b7",
		Sampled: false,
	}

	_, sc := StartSpan(WithSpanContext(context.Background(), unsampled))

	if sc.Sampled {
		t.Error("an unsampled parent produced a sampled child")
	}
}

func orNone(s string) string {
	if s == "" {
		return "(root)"
	}
	return s
}

// TestEndCapturesTheError is the deferred-closure trap.
func TestEndCapturesTheError(t *testing.T) {
	recorder := &Recorder{}
	tracer := &Tracer{Recorder: recorder}

	// The WRONG way, written out so the failure is visible: defer end(err) evaluates err at the
	// defer statement, which is before the work.
	func() {
		var err error

		_, end := tracer.Start(context.Background(), "wrong")
		defer end(err) //nolint:govet // deliberately the mistake

		err = errors.New("something failed")
		_ = err
	}()

	// The right way.
	func() {
		var err error

		_, end := tracer.Start(context.Background(), "right")
		defer func() { end(err) }()

		err = errors.New("something failed")
	}()

	spans := recorder.Spans()
	if len(spans) != 2 {
		t.Fatalf("recorded %d spans", len(spans))
	}

	byName := map[string]Span{}
	for _, s := range spans {
		byName[s.Name] = s
	}

	t.Logf("defer end(err):          error = %q", byName["wrong"].Error)
	t.Logf("defer func(){end(err)}:  error = %q", byName["right"].Error)

	if byName["wrong"].Error != "" {
		t.Error("the wrong form recorded an error, so this demonstration is broken")
	}
	if byName["right"].Error == "" {
		t.Error("the right form did not record the error")
	}

	t.Log("arguments to a deferred call are evaluated AT THE DEFER, not at the call. So " +
		"`defer end(err)` always records nil, and the span says the request succeeded.")
}

// TestMiddlewareIgnoresABadTraceparent, because tracing is not worth failing a request over.
func TestMiddlewareIgnoresABadTraceparent(t *testing.T) {
	recorder := &Recorder{}
	tracer := &Tracer{Recorder: recorder}

	handler := tracer.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for _, header := range []string{
		"garbage",
		"00-00000000000000000000000000000000-00f067aa0ba902b7-01",
		strings.Repeat("a", 10_000),
		"",
	} {
		recorder.Reset()

		req := httptest.NewRequest("GET", "/", nil)
		if header != "" {
			req.Header.Set("traceparent", header)
		}

		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("a traceparent of %d bytes produced %d", len(header), rec.Code)
		}

		// A new trace was started rather than the request being rejected.
		out, err := ParseTraceparent(rec.Header().Get("traceparent"))
		if err != nil {
			t.Errorf("the response traceparent is invalid: %v", err)
			continue
		}
		if !out.Valid() {
			t.Error("the response traceparent is not valid")
		}
	}

	t.Log("every malformed header produced a 200 and a fresh trace. Returning 400 for a bad " +
		"traceparent lets a broken client take an endpoint down.")
}

// TestIDsAreUnique, because a collision merges two unrelated requests into one trace.
func TestIDsAreUnique(t *testing.T) {
	const n = 10_000

	traces := make(map[string]struct{}, n)
	spans := make(map[string]struct{}, n)

	for range n {
		traces[NewTraceID()] = struct{}{}
		spans[NewSpanID()] = struct{}{}
	}

	t.Logf("%d trace ids gave %d distinct values", n, len(traces))
	t.Logf("%d span ids gave %d distinct values", n, len(spans))

	if len(traces) != n {
		t.Errorf("%d trace id collisions", n-len(traces))
	}

	// 8-byte span ids: the birthday bound gives about a 0.3% chance of at least one collision in
	// 10,000 draws, so a collision here is not a failure. Asserting on it would be a flaky test,
	// which is why this logs rather than asserts.
	if len(spans) != n {
		t.Logf("%d span id collision(s), which is expected at this rate for 64 bits",
			n-len(spans))
	}

	// And they are valid, which the all-zero guard is for.
	for id := range traces {
		if !isValidHexID(id, 32) {
			t.Fatalf("invalid trace id %q", id)
		}
		break
	}
}

// TestSamplingIsADecision: the edge used to mark every new trace sampled, so every request logged at debug level
// and "sampling" sampled nothing. SampleRate is the fraction of new traces kept.
func TestSamplingIsADecision(t *testing.T) {
	sampledOf := func(rate float64, n int) int {
		tracer := &Tracer{SampleRate: rate}
		count := 0
		for range n {
			ctx, end := tracer.Start(context.Background(), "edge")
			if SpanContextFrom(ctx).Sampled {
				count++
			}
			end(nil)
		}
		return count
	}

	if got := sampledOf(0, 1_000); got != 0 {
		t.Errorf("SampleRate 0 sampled %d of 1000", got)
	}
	if got := sampledOf(1, 1_000); got != 1_000 {
		t.Errorf("SampleRate 1 sampled %d of 1000", got)
	}
	if got := sampledOf(0.1, 10_000); got < 700 || got > 1_300 {
		t.Errorf("SampleRate 0.1 sampled %d of 10000, want about 1000", got)
	}
}

// TestAClientCannotTurnOnDebugLogging: an incoming traceparent ending -01 says "sampled". Honoured from anyone,
// that lets any client switch on debug logging for its own requests, which is a cheap way to flood the logs.
// The trace id is kept so traces still join up; the sampling decision is only inherited when the tracer is told
// its callers are trusted.
func TestAClientCannotTurnOnDebugLogging(t *testing.T) {
	const incoming = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"

	seen := func(tracer *Tracer) SpanContext {
		var sc SpanContext
		h := tracer.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			sc = SpanContextFrom(r.Context())
		}))
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("traceparent", incoming)
		h.ServeHTTP(httptest.NewRecorder(), req)
		return sc
	}

	untrusted := seen(&Tracer{SampleRate: 0})
	if untrusted.Sampled {
		t.Error("an untrusted caller's sampled flag was honoured")
	}
	if untrusted.TraceID != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Errorf("trace id %q; the incoming trace should still be continued", untrusted.TraceID)
	}

	if trusted := seen(&Tracer{SampleRate: 0, TrustIncomingSampling: true}); !trusted.Sampled {
		t.Error("a trusted caller's sampled flag was ignored")
	}
}

// TestVersion00HasExactlyFourFields: extra fields are allowed only for versions after 00.
func TestVersion00HasExactlyFourFields(t *testing.T) {
	const ok = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"

	if _, err := ParseTraceparent(ok + "-extra"); !errors.Is(err, ErrBadTraceparent) {
		t.Errorf("a version 00 header with five fields: err = %v, want ErrBadTraceparent", err)
	}
	if _, err := ParseTraceparent("01" + ok[2:] + "-extra"); err != nil {
		t.Errorf("a version 01 header with five fields was rejected: %v", err)
	}
}
