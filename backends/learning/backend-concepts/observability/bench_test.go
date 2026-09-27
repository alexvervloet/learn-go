package observability

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

// BenchmarkLogging prices the handler chain, because "structured logging is slow" is a claim people make without
// a number and the number decides whether to sample.
func BenchmarkLogging(b *testing.B) {
	ctx := WithRequestID(context.Background(), "req-abc123")
	ctx = WithSpanContext(ctx, SpanContext{
		TraceID: "4bf92f3577b34da6a3ce929d0e0e4736",
		SpanID:  "00f067aa0ba902b7",
		Sampled: true,
	})

	for _, tc := range []struct {
		name string
		log  *slog.Logger
	}{
		{"JSON handler", slog.New(slog.NewJSONHandler(io.Discard, nil))},
		{"text handler", slog.New(slog.NewTextHandler(io.Discard, nil))},
		{"+ ContextHandler", slog.New(ContextHandler{Handler: slog.NewJSONHandler(io.Discard, nil)})},
		{"+ LevelFilter", NewLogger(debugHandler(), slog.LevelInfo)},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				tc.log.InfoContext(ctx, "handled request",
					"method", "GET", "route", "/users/{id}", "status", 200)
			}
		})
	}

	// The saving the filter buys: a dropped line costs one comparison, because slog calls Enabled
	// before evaluating the arguments.
	//
	// The inner handler is at LevelDebug, which is not a detail. With slog's default of Info, the
	// "kept because sampled" case below measures 44ns rather than 800, because the inner handler
	// drops it anyway and sampling does nothing. That is how the trap in NewLogger's doc comment was
	// found.
	filtered := NewLogger(debugHandler(), slog.LevelInfo)

	b.Run("dropped debug line", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			filtered.DebugContext(ctx, "not logged",
				"method", "GET", "route", "/users/{id}", "status", 200)
		}
	})

	// The same line when it is kept, for the ratio.
	b.Run("kept debug line (sampled)", func(b *testing.B) {
		sampled := WithSampled(ctx, true)

		b.ReportAllocs()
		for b.Loop() {
			filtered.DebugContext(sampled, "logged",
				"method", "GET", "route", "/users/{id}", "status", 200)
		}
	})
}

// BenchmarkRedaction, because a redaction that costs something at every log line would not be used.
func BenchmarkRedaction(b *testing.B) {
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))

	plain := "sk_live_9f8e7d6c5b4a3210"
	redacted := Redact(plain)

	b.Run("plain string", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			log.Info("x", "token", plain)
		}
	})

	b.Run("Redacted", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			log.Info("x", "token", redacted)
		}
	})
}

// BenchmarkTracing prices the parse, the generation and the middleware.
func BenchmarkTracing(b *testing.B) {
	const header = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"

	b.Run("ParseTraceparent", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := ParseTraceparent(header); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("NewTraceID", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = NewTraceID()
		}
	})

	b.Run("SpanContext.String", func(b *testing.B) {
		sc, err := ParseTraceparent(header)
		if err != nil {
			b.Fatal(err)
		}

		b.ReportAllocs()
		for b.Loop() {
			_ = sc.String()
		}
	})
}

// BenchmarkMiddlewareStack is the number that matters: what observability costs per request, against a handler
// that does nothing.
func BenchmarkMiddlewareStack(b *testing.B) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})

	tracer := &Tracer{Recorder: &Recorder{}}
	metrics := NewMetrics("bench", RouteFromPattern, nil)

	for _, tc := range []struct {
		name string
		h    http.Handler
	}{
		{"bare handler", inner},
		{"metrics", metrics.Middleware(inner)},
		{"tracing", tracer.Middleware(inner)},
		{"both", tracer.Middleware(metrics.Middleware(inner))},
	} {
		b.Run(tc.name, func(b *testing.B) {
			w := &nullWriter{header: make(http.Header, 8)}
			r := httptest.NewRequest("GET", "/users/42", nil)

			b.ReportAllocs()
			for b.Loop() {
				tc.h.ServeHTTP(w, r)
			}
		})
	}
}

// BenchmarkGather is what a Prometheus scrape costs, and it grows with cardinality, which is the other half of
// the cardinality argument: the series cost memory in Prometheus AND cpu in the service, every scrape.
func BenchmarkGather(b *testing.B) {
	for _, routes := range []int{1, 100, 10_000} {
		m := NewMetrics("gather", RouteFromPattern, nil)

		for i := range routes {
			m.RequestsTotal.WithLabelValues(itoa(i), "GET", "200").Inc()
		}

		b.Run(itoa(routes)+" label values", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := m.Registry.Gather(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// debugHandler is an inner handler that will actually emit debug lines.
func debugHandler() slog.Handler {
	return slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug})
}

type nullWriter struct {
	header http.Header
	status int
}

func (w *nullWriter) Header() http.Header         { return w.header }
func (w *nullWriter) Write(b []byte) (int, error) { return len(b), nil }
func (w *nullWriter) WriteHeader(status int)      { w.status = status }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}

	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
