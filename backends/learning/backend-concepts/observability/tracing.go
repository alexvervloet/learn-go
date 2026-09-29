package observability

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	mathrand "math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// SpanContext is what W3C trace context puts in a header, and it is the whole of distributed tracing's wire
// format.
//
// # The header
//
//	traceparent: 00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01
//	             ^^ ^                              ^ ^              ^ ^^
//	             |  trace id, 16 bytes             | span id, 8 b   | flags
//	             version                                              01 = sampled
//
// 55 characters, fixed width, one header. That is it. Everything else in a tracing system (spans, exporters,
// samplers, collectors, baggage) is about producing and consuming these 55 characters.
//
// # The rules that are easy to get wrong
//
// The trace id is created ONCE, at the edge, and copied unchanged through every service. A service that
// generates a new one starts a new trace and the request appears twice in the UI with no connection.
//
// The span id is NEW in every service, and the incoming span id becomes the new span's PARENT. A service that
// passes the span id through unchanged produces a trace where everything is a sibling.
//
// An all-zero trace id or span id is INVALID per the spec and must be rejected rather than propagated.
//
// The version prefix is not "00" forever: a receiver must accept a higher version and parse the first three
// fields, because that is how the format was designed to be extended. Rejecting version "01" is the bug that
// breaks a service in two years.
type SpanContext struct {
	TraceID string
	SpanID  string
	Sampled bool

	// ParentSpanID is empty at the edge. Kept so a span can report its parent without a second
	// lookup.
	ParentSpanID string
}

// Valid reports whether this is a usable span context.
//
// The all-zero check is the part people skip. A trace id of 32 zeros parses fine as hex and is explicitly
// invalid in the spec, and propagating one produces a trace that every backend silently discards.
func (sc SpanContext) Valid() bool {
	return isValidHexID(sc.TraceID, 32) && isValidHexID(sc.SpanID, 16)
}

// String renders the traceparent header.
func (sc SpanContext) String() string {
	flags := "00"
	if sc.Sampled {
		flags = "01"
	}

	return "00-" + sc.TraceID + "-" + sc.SpanID + "-" + flags
}

// Errors from parsing.
var (
	ErrNoTraceparent  = errors.New("no traceparent header")
	ErrBadTraceparent = errors.New("malformed traceparent")
)

// ParseTraceparent parses the header.
func ParseTraceparent(header string) (SpanContext, error) {
	if header == "" {
		return SpanContext{}, ErrNoTraceparent
	}

	parts := strings.Split(header, "-")

	// At least four fields. MORE than four is allowed, because a future version may append, and a
	// parser that requires exactly four rejects every version after 00.
	if len(parts) < 4 {
		return SpanContext{}, fmt.Errorf("%w: %d fields, want at least 4",
			ErrBadTraceparent, len(parts))
	}

	version, traceID, spanID, flags := parts[0], parts[1], parts[2], parts[3]

	if len(version) != 2 {
		return SpanContext{}, fmt.Errorf("%w: version %q is not two hex digits",
			ErrBadTraceparent, version)
	}

	// Version ff is reserved as invalid by the spec, and anything else is accepted. This is the rule
	// that keeps the format extensible: accept a version you do not know, parse the fields you do.
	if version == "ff" {
		return SpanContext{}, fmt.Errorf("%w: version ff is reserved", ErrBadTraceparent)
	}

	if !isValidHexID(traceID, 32) {
		return SpanContext{}, fmt.Errorf("%w: trace id %q", ErrBadTraceparent, traceID)
	}
	if !isValidHexID(spanID, 16) {
		return SpanContext{}, fmt.Errorf("%w: span id %q", ErrBadTraceparent, spanID)
	}

	if len(flags) != 2 {
		return SpanContext{}, fmt.Errorf("%w: flags %q", ErrBadTraceparent, flags)
	}

	parsedFlags, err := strconv.ParseUint(flags, 16, 8)
	if err != nil {
		return SpanContext{}, fmt.Errorf("%w: flags %q: %w", ErrBadTraceparent, flags, err)
	}

	return SpanContext{
		TraceID: traceID,
		SpanID:  spanID,

		// Bit 0 is the sampled flag. Testing the bit rather than comparing the byte to 01, because
		// other bits are reserved and a future flag would make `flags == "01"` false for a sampled
		// request.
		Sampled: parsedFlags&0x01 == 1,
	}, nil
}

// isValidHexID checks length, hex-ness and the all-zero case.
func isValidHexID(s string, length int) bool {
	if len(s) != length {
		return false
	}

	allZero := true

	for i := range len(s) {
		c := s[i]

		switch {
		case c >= '0' && c <= '9':
			if c != '0' {
				allZero = false
			}
		case c >= 'a' && c <= 'f':
			allZero = false
		default:
			// Uppercase hex is invalid per the spec, which says lowercase. Being strict here
			// matters: a backend that keys on the string treats ABC and abc as two traces.
			return false
		}
	}

	return !allZero
}

type spanContextKey struct{}

// WithSpanContext puts a span context in a context.
func WithSpanContext(ctx context.Context, sc SpanContext) context.Context {
	return context.WithValue(ctx, spanContextKey{}, sc)
}

// SpanContextFrom reads it.
func SpanContextFrom(ctx context.Context) SpanContext {
	sc, _ := ctx.Value(spanContextKey{}).(SpanContext)
	return sc
}

// NewTraceID generates a trace id.
//
// crypto/rand, not math/rand. Not for secrecy: a trace id is public. For COLLISIONS. math/rand's global source
// in a multi-process deployment is seeded per process, and two processes starting in the same second used to
// produce identical sequences in Go 1 (rand/v2 seeds randomly, which fixes it). A collision merges two
// unrelated requests into one trace, which is the kind of bug that gets blamed on the tracing backend.
func NewTraceID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)

	// The all-zero case, which has a probability of 2^-128 and is handled anyway because it is one
	// line and the alternative is an invalid trace id in production once per never, at 3am.
	if isAllZero(b) {
		b[0] = 1
	}

	return hex.EncodeToString(b)
}

// NewSpanID generates a span id.
func NewSpanID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)

	if isAllZero(b) {
		b[0] = 1
	}

	return hex.EncodeToString(b)
}

func isAllZero(b []byte) bool {
	for _, x := range b {
		if x != 0 {
			return false
		}
	}
	return true
}

// StartSpan continues an incoming trace or begins a new one.
//
// This is the function that implements the two rules from the type's doc comment: the trace id is inherited, the
// span id is new, and the incoming span id becomes the parent.
func StartSpan(ctx context.Context) (context.Context, SpanContext) {
	parent := SpanContextFrom(ctx)

	sc := SpanContext{SpanID: NewSpanID()}

	if parent.Valid() {
		sc.TraceID = parent.TraceID
		sc.ParentSpanID = parent.SpanID
		sc.Sampled = parent.Sampled
	} else {
		sc.TraceID = NewTraceID()
		// A new trace. Sampled here, for callers using StartSpan directly; Tracer.Start replaces
		// this with its SampleRate decision.
		sc.Sampled = true
	}

	return WithSpanContext(ctx, sc), sc
}

// Span is a recorded unit of work.
//
// A minimal version of what OTel calls a Span: a name, a duration, the ids, and attributes. Enough to show what
// a tracing backend receives, and far short of what one provides (events, links, status codes, resource
// attributes, semantic conventions).
type Span struct {
	Name         string
	TraceID      string
	SpanID       string
	ParentSpanID string
	Start        time.Time
	Duration     time.Duration
	Attrs        map[string]string
	Error        string
}

// Recorder collects spans, standing in for an exporter.
//
// In a real service this is where the batching, the retry and the network live, and the reason it is an
// interface in OTel is that the export path must not block the request path. A test recorder is a slice behind a
// mutex.
type Recorder struct {
	mu    sync.Mutex
	spans []Span
}

// Record stores a span.
func (r *Recorder) Record(s Span) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.spans = append(r.spans, s)
}

// Spans returns a copy.
func (r *Recorder) Spans() []Span {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Span(nil), r.spans...)
}

// Reset clears the recorder.
func (r *Recorder) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.spans = nil
}

// Tracer starts and finishes spans.
type Tracer struct {
	Recorder *Recorder

	// SampleRate is the fraction of NEW traces, those starting at this service, that are sampled:
	// 0.01 keeps one in a hundred. Zero samples none. The first version sampled every new trace,
	// which turned debug logging on for every request and made "sampling" a no-op.
	SampleRate float64

	// TrustIncomingSampling honours the sampled flag of an incoming traceparent. Off by default,
	// because at the edge the header comes from clients, and honouring it lets any client switch on
	// debug logging for its own requests. Turn it on for a service only reachable from services
	// that made the decision themselves. The incoming TRACE ID is continued either way.
	TrustIncomingSampling bool
}

// sample makes one sampling decision at SampleRate.
func (t *Tracer) sample() bool {
	switch {
	case t.SampleRate >= 1:
		return true
	case t.SampleRate <= 0:
		return false
	default:
		return mathrand.Float64() < t.SampleRate
	}
}

// Start begins a span and returns a function to end it.
//
// The end function takes an error, so the deferred call reads
//
//	ctx, end := tracer.Start(ctx, "loadUser")
//	defer func() { end(err) }()
//
// and NOT `defer end(err)`, which captures err at the defer statement and always records nil. That is a
// genuine Go trap and it is why the signature takes the error rather than reading a named return: taking it as a
// parameter makes the closure necessary and visible.
func (t *Tracer) Start(ctx context.Context, name string) (context.Context, func(error)) {
	ctx, sc := StartSpan(ctx)

	// A new trace at the edge: the sampling decision is made once, here, and every service
	// downstream honours it. That is what makes a sampled trace complete rather than a collection
	// of fragments.
	if sc.ParentSpanID == "" {
		sc.Sampled = t.sample()
		ctx = WithSpanContext(ctx, sc)
	}

	start := time.Now()
	attrs := map[string]string{}

	return ctx, func(err error) {
		span := Span{
			Name:         name,
			TraceID:      sc.TraceID,
			SpanID:       sc.SpanID,
			ParentSpanID: sc.ParentSpanID,
			Start:        start,
			Duration:     time.Since(start),
			Attrs:        attrs,
		}

		if err != nil {
			span.Error = err.Error()
		}

		if t.Recorder != nil {
			t.Recorder.Record(span)
		}
	}
}

// Middleware continues the incoming trace and puts the span context in the request's context.
func (t *Tracer) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		// An incoming traceparent is honoured; a malformed one is IGNORED rather than rejected.
		// Returning 400 for a bad traceparent would let a broken client take an endpoint down, and
		// tracing is not worth failing a request over.
		if sc, err := ParseTraceparent(r.Header.Get("traceparent")); err == nil {
			// Keep the trace id; decide sampling locally unless the caller is trusted.
			if !t.TrustIncomingSampling {
				sc.Sampled = t.sample()
			}
			ctx = WithSpanContext(ctx, sc)
		}

		ctx, end := t.Start(ctx, r.Method+" "+r.URL.Path)

		sc := SpanContextFrom(ctx)

		// The response carries the trace id, which is the cheapest debugging aid there is: a user
		// reporting a problem can quote it, and it goes straight to the trace.
		w.Header().Set("traceparent", sc.String())

		ctx = WithSampled(ctx, sc.Sampled)

		next.ServeHTTP(w, r.WithContext(ctx))

		end(nil)
	})
}

// Inject sets the traceparent header on an outgoing request.
//
// Half of propagation, and the half that gets forgotten: a service that reads the incoming header and does not
// set the outgoing one produces traces that stop at its own boundary, which looks like the downstream service
// being untraced.
func Inject(ctx context.Context, r *http.Request) {
	sc := SpanContextFrom(ctx)
	if !sc.Valid() {
		return
	}

	r.Header.Set("traceparent", sc.String())
}
