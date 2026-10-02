package middleware

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	chimw "github.com/go-chi/chi/v5/middleware"
)

// nullWriter is a do-nothing http.ResponseWriter, so a benchmark measures the middleware rather
// than the recorder.
//
// httptest.NewRecorder allocates 1,010 bytes across 10 allocations, which is more than most of
// the middleware here, and using one per iteration hid every difference behind it. The header
// map is allocated once and reused; middleware that sets headers therefore writes into the
// same map every iteration, which is fine for a cost measurement and would not be for a
// correctness test.
type nullWriter struct {
	header http.Header
	status int
}

func newNullWriter() *nullWriter { return &nullWriter{header: make(http.Header, 8)} }

func (w *nullWriter) Header() http.Header         { return w.header }
func (w *nullWriter) Write(b []byte) (int, error) { return len(b), nil }
func (w *nullWriter) WriteHeader(status int)      { w.status = status }

// What a middleware chain costs. Each layer is a closure call, so the question is whether that
// is a rounding error next to a real handler or something to think about.
func BenchmarkChainDepth(b *testing.B) {
	noop := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
		})
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})

	for _, depth := range []int{0, 1, 5, 20} {
		chain := make([]Middleware, depth)
		for i := range chain {
			chain[i] = noop
		}

		h := Chain(chain...)(handler)
		req := httptest.NewRequest("GET", "/", nil)

		w := newNullWriter()

		b.Run(itoa(depth)+" layers", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				h.ServeHTTP(w, req)
			}
		})
	}
}

// Each middleware this package defines, priced on its own against a bare handler.
func BenchmarkEachMiddleware(b *testing.B) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})

	discard := slog.New(slog.NewTextHandler(io.Discard, nil))

	variants := []struct {
		name string
		h    http.Handler
	}{
		{"bare handler", handler},
		{"RequestID", RequestID(handler)},
		{"Logger", Logger(discard)(handler)},
		{"Recovery", Recovery(discard)(handler)},
		{"Timeout", Timeout(time.Second)(handler)},
		{"SecureHeaders", SecureHeaders(handler)},
		{"MaxBody", MaxBody(1 << 20)(handler)},
		{"RealIP (this package)", RealIP(true)(handler)},
		{"CleanPath (this package)", CleanPath(handler)},
		{"chi StripSlashes", chimw.StripSlashes(handler)},
		{"chi Compress", chimw.Compress(5)(handler)},
		{"chi Recoverer", chimw.Recoverer(handler)},
		{"the whole Production chain", Production(discard)(handler)},
	}

	req := httptest.NewRequest("GET", "/", nil)

	for _, v := range variants {
		w := newNullWriter()

		b.Run(v.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				v.h.ServeHTTP(w, req)
			}
		})
	}
}

// The recorder wrapper against no wrapper, which is the cost of being able to log a status code.
func BenchmarkRecorderWrapper(b *testing.B) {
	req := httptest.NewRequest("GET", "/", nil)

	body := []byte("ok")

	b.Run("direct write", func(b *testing.B) {
		w := newNullWriter()

		b.ReportAllocs()
		for b.Loop() {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(body)
		}
	})

	b.Run("through the recorder", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			// The wrapper allocation is part of what is being measured: a logging
			// middleware makes one per request.
			rec := &recorder{ResponseWriter: newNullWriter()}
			rec.WriteHeader(http.StatusOK)
			_, _ = rec.Write(body)
		}
	})

	b.Run("through the recorder, reusing it", func(b *testing.B) {
		w := newNullWriter()
		rec := &recorder{ResponseWriter: w}

		b.ReportAllocs()
		for b.Loop() {
			rec.wroteHeader = false
			rec.WriteHeader(http.StatusOK)
			_, _ = rec.Write(body)
		}
	})

	// What reaching the real writer through a wrapper costs, which is the price of being
	// able to log a status code and still stream.
	b.Run("Flush via http.NewResponseController", func(b *testing.B) {
		h := Logger(slog.New(slog.NewTextHandler(io.Discard, nil)))(
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_ = http.NewResponseController(w).Flush()
			}))

		// A recorder, because nullWriter has no Flush and ResponseController would
		// report ErrNotSupported.
		b.ReportAllocs()
		for b.Loop() {
			h.ServeHTTP(httptest.NewRecorder(), req)
		}
	})
}

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
