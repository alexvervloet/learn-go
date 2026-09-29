// Package observability is logs, metrics and traces, and the mistakes that make each of them useless.
//
// # The three, and what each is for
//
//	logs     one line per event, high detail, high volume, queried by humans after the fact
//	metrics  numbers aggregated over time, low volume, queried by dashboards and alerts
//	traces   one record per request spanning services, showing where the time went
//
// The failure mode is using the wrong one. Counting things by grepping logs works until the log volume makes it
// expensive; putting a user id in a metric label makes the metric unusable; logging every span turns a trace
// into a log. Each section below is about the specific way its tool gets misused.
//
// # What this package does and does not use
//
// slog for logs and prometheus/client_golang for metrics, both of which are what a Go service would use.
//
// For traces it implements W3C trace context BY HAND rather than importing OpenTelemetry, and that is a
// deliberate trade. OTel is what a real service should use: it has exporters, sampling, baggage, semantic
// conventions and instrumentation for every library. It is also about 40 modules and a concept vocabulary that
// hides what is actually happening, which is a 55-character header being parsed, incremented and passed on.
// Forty lines here shows the mechanism, and knowing the mechanism is what makes OTel's configuration make
// sense.
package observability

import (
	"context"
	"log/slog"
	"strings"
)

// Redacted wraps a value that must never reach a log.
//
// # Why this is a type and not a code review rule
//
// "Do not log passwords" is a rule people follow until someone logs a whole struct. `slog.Any("user", user)`
// with a Password field prints the password, and nothing in the type system objects.
//
// slog.LogValuer is the fix: a type that implements it controls its own rendering, so the secret cannot be
// logged even by accident, even inside a struct, even by a `%+v` in a different package.
type Redacted[T any] struct {
	value T
}

// Redact wraps a value.
func Redact[T any](v T) Redacted[T] { return Redacted[T]{value: v} }

// Value returns the wrapped value, for the code that genuinely needs it.
//
// Named Value rather than Unwrap or Get, so that a call site reading `token.Value()` says "I am deliberately
// taking the secret out of its wrapper".
func (r Redacted[T]) Value() T { return r.value }

// LogValue implements slog.LogValuer.
//
// This is the whole mechanism. slog calls it instead of reflecting over the value, so the secret never reaches
// a Handler.
func (r Redacted[T]) LogValue() slog.Value { return slog.StringValue("REDACTED") }

// String implements fmt.Stringer, because slog is not the only thing that prints values.
//
// Without this, `fmt.Printf("%v", cfg)` on a struct holding a Redacted field prints the underlying value
// through reflection. LogValuer covers slog and Stringer covers fmt, and both are needed: a type that is safe
// in one and not the other is a type that leaks the first time someone debugs with a Println.
func (r Redacted[T]) String() string { return "REDACTED" }

// GoString implements fmt.GoStringer, which is what %#v uses.
//
// The third one, and the reason it is here is that %#v is what a panic message and a testify diff use. Two out
// of three is a leak waiting for a specific verb.
func (r Redacted[T]) GoString() string { return "REDACTED" }

// MarshalJSON stops it reaching an API response too.
//
// Not strictly observability, and included because a Redacted field in a config struct that gets served on a
// /debug endpoint is the same bug through a different door.
func (r Redacted[T]) MarshalJSON() ([]byte, error) { return []byte(`"REDACTED"`), nil }

// requestIDKey and traceKey are the context keys.
type (
	requestIDKey struct{}
	loggerKey    struct{}
)

// WithRequestID puts a request id in the context.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, id)
}

// RequestID reads it.
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

// WithLogger puts a logger in the context.
//
// # The argument against doing this
//
// A logger in a context is contentious, and the objection is fair: a context is for request-scoped values and
// cancellation, and a logger is a dependency. The alternative is passing a *slog.Logger to every function,
// which is explicit and correct and means touching every signature in the call graph to add one field to one
// log line.
//
// The compromise this package takes: the logger in the context is the one carrying request-scoped ATTRIBUTES
// (request id, trace id, user), and a package's own base logger is still a field on its struct. So the context
// carries data, which is what it is for, and the data happens to be pre-attached to a logger so every call site
// does not have to re-attach it.
func WithLogger(ctx context.Context, log *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerKey{}, log)
}

// Logger reads the request's logger, or returns the fallback.
//
// Never returns nil. A Logger() that can return nil produces a nil-pointer panic inside an error path, which is
// the worst possible place for one: the log line that would have explained the failure is the thing that
// crashes.
func Logger(ctx context.Context, fallback *slog.Logger) *slog.Logger {
	if log, ok := ctx.Value(loggerKey{}).(*slog.Logger); ok && log != nil {
		return log
	}

	if fallback != nil {
		return fallback
	}

	return slog.Default()
}

// ContextHandler wraps a slog.Handler and pulls request-scoped attributes out of the context.
//
// # Why this rather than log.With at every call site
//
// slog.Handler's Handle method receives the context. So a handler can read the request id and the trace id from
// it and attach them to every record, and no call site has to remember. That is the difference between a
// service where 90% of log lines have a request id and one where all of them do.
//
// The alternative, `log = log.With("request_id", id)` at the top of each handler, works and relies on every
// author doing it. This is the same reason the recorder in http-tutorial exists: make the correct thing the
// default rather than a convention.
type ContextHandler struct {
	slog.Handler
}

// Handle implements slog.Handler.
func (h ContextHandler) Handle(ctx context.Context, r slog.Record) error {
	if id := RequestID(ctx); id != "" {
		r.AddAttrs(slog.String("request_id", id))
	}

	if sc := SpanContextFrom(ctx); sc.Valid() {
		// The trace id is what joins a log line to a trace, and it is the single highest-value
		// attribute in a distributed system: it turns "find the logs for this slow request" from a
		// timestamp-and-grep exercise into one query.
		r.AddAttrs(
			slog.String("trace_id", sc.TraceID),
			slog.String("span_id", sc.SpanID),
		)
	}

	return h.Handler.Handle(ctx, r)
}

// WithAttrs and WithGroup have to be forwarded so the wrapper survives log.With.
//
// This is the part that gets missed. A Handler wrapper that does not implement these returns the INNER
// handler from With, so `log.With("k", "v")` silently unwraps the ContextHandler and every later line loses the
// request id. The bug appears only in code that calls With, which is most code.
//
// # The limitation WithGroup cannot fix
//
// Forwarding WithGroup keeps the wrapper and puts the context attributes INSIDE the group:
//
//	log.WithGroup("http").InfoContext(ctx, "x")  ->  {"http":{"request_id":"req-1"}}
//
// not `{"request_id":"req-1","http":{}}`. That is not a bug in this wrapper, it is what the Handler API allows:
// by the time Handle runs, the inner handler has already been told it is inside a group, and a record's
// attributes go wherever the handler puts them. There is no way for a wrapper to add a TOP-LEVEL attribute to a
// handler that is inside a group.
//
// A handler could fix it by taking over group handling entirely: not forwarding WithGroup, tracking the group
// names itself, and reassembling the nesting in Handle. That is a reimplementation of slog's group semantics
// inside a wrapper, and getting it wrong is worse than the nesting.
//
// So the practical guidance, which the test pins down: either do not use WithGroup for loggers that carry
// request-scoped attributes, or query the nested path. A log query for `request_id` will not match
// `http.request_id`, and that mismatch is the thing to know about before building a dashboard on it.
func (h ContextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return ContextHandler{Handler: h.Handler.WithAttrs(attrs)}
}

// WithGroup forwards likewise.
func (h ContextHandler) WithGroup(name string) slog.Handler {
	return ContextHandler{Handler: h.Handler.WithGroup(name)}
}

// LevelFilter is a Handler that samples below a level.
//
// # The problem it solves
//
// Debug logging in production is off because it is too expensive, which means it is unavailable exactly when it
// is needed. Sampling is the middle ground: keep every line at or above Always, and below it keep a request's
// lines only if that request was sampled. Which requests are sampled is Tracer.SampleRate's decision. (An
// earlier version of this comment promised "one in N info lines", which nothing implemented, and the tracer
// sampled every request, so every debug line was written.)
//
// The sampling decision has to be per REQUEST rather than per line, or a sampled trace has holes in it and the
// remaining lines cannot be assembled into a story. That is why the decision is stored in the context by
// SampleDecision rather than made in Handle.
type LevelFilter struct {
	slog.Handler

	// Always is the level at and above which everything is kept.
	Always slog.Level
}

// Enabled implements slog.Handler.
//
// Enabled, not Handle, and the difference is the whole cost saving. slog calls Enabled BEFORE evaluating the
// arguments, so a dropped line costs one comparison. Measured: 13.3ns for a dropped line against 788ns for one
// that is written, and zero allocations against one. Filtering in Handle means the arguments are formatted
// first, which for a debug line with a few attributes is most of the expense.
//
// The delegation on the sampled path is the part to be careful with: it asks the INNER handler, so an inner
// handler left at slog's default Info level makes sampling a no-op. See NewLogger.
func (f LevelFilter) Enabled(ctx context.Context, level slog.Level) bool {
	if level >= f.Always {
		return true
	}

	if Sampled(ctx) {
		return f.Handler.Enabled(ctx, level)
	}

	return false
}

// WithAttrs forwards, as above.
func (f LevelFilter) WithAttrs(attrs []slog.Attr) slog.Handler {
	return LevelFilter{Handler: f.Handler.WithAttrs(attrs), Always: f.Always}
}

// WithGroup forwards, as above.
func (f LevelFilter) WithGroup(name string) slog.Handler {
	return LevelFilter{Handler: f.Handler.WithGroup(name), Always: f.Always}
}

type sampledKey struct{}

// WithSampled marks a request as one to log in full.
func WithSampled(ctx context.Context, sampled bool) context.Context {
	return context.WithValue(ctx, sampledKey{}, sampled)
}

// Sampled reports whether this request is being logged in full.
func Sampled(ctx context.Context) bool {
	sampled, _ := ctx.Value(sampledKey{}).(bool)
	return sampled
}

// NewLogger builds the handler chain a service would use.
//
// The order matters and is the opposite of what reads naturally. The OUTERMOST wrapper runs first, so the
// filter has to be outside the context handler: a line that will be dropped should not have attributes computed
// for it.
//
// # The misconfiguration this cannot prevent
//
// The INNER handler must be created at the lowest level you ever want to see, which is almost always
// slog.LevelDebug. slog's handlers default to Info, and LevelFilter.Enabled defers to the inner handler for a
// sampled request, so an inner handler left at Info means sampling can never turn debug logging on. Everything
// still works and the feature silently does nothing.
//
// A benchmark found this: a "kept because sampled" debug line measured 44ns against 788ns for an info line,
// which is not the cost of writing a log line. It was still being dropped.
//
//	slog.NewJSONHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug})  // the inner handler
//	NewLogger(inner, slog.LevelInfo)                                      // LevelFilter does the gating
//
// TestSamplingNeedsADebugInnerHandler pins it down.
func NewLogger(inner slog.Handler, always slog.Level) *slog.Logger {
	return slog.New(LevelFilter{
		Handler: ContextHandler{Handler: inner},
		Always:  always,
	})
}

// Sanitize strips newlines and control characters from a value destined for a log.
//
// # Log injection, which is real and boring
//
// A log line is text. A user-controlled value containing a newline can write a second, fake line: an attacker
// who can set their username to "alice\nlevel=INFO msg=\"admin granted\"" has written whatever they like into
// the log, and anything parsing those logs believes it.
//
// Both slog handlers escape this: JSON escapes newlines inside strings, and the text handler quotes any message,
// key or value containing a newline and writes it as \n. (An earlier version of this comment said the text
// handler does not, in all cases; checked on Go 1.27 with a newline in the message, a key and a value, it does.)
// What escapes nothing is the log line built without slog: fmt.Printf, log.Printf, a string concatenated into a
// file, a value handed to a system that splits on newlines.
//
// So: slog removes the problem, and this function exists for the values that reach a sink slog does not
// format.
func Sanitize(s string) string {
	if !strings.ContainsFunc(s, isControl) {
		return s
	}

	var b strings.Builder
	b.Grow(len(s))

	for _, r := range s {
		if isControl(r) {
			b.WriteRune(' ')
			continue
		}
		b.WriteRune(r)
	}

	return b.String()
}

func isControl(r rune) bool {
	return r == '\n' || r == '\r' || r == 0x1b || (r < 0x20 && r != '\t')
}
