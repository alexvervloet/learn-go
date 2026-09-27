package ratelimit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alexvervloet/learn-go/backends/learning/backend-concepts/internal/redistest"
)

// BenchmarkAllow prices the four in-memory algorithms plus the Redis one.
//
// The number that matters is the ratio between the in-memory limiters and the Redis one, because that ratio
// is the cost of correctness across replicas, and it is what someone is implicitly choosing when they leave a
// per-process limiter in a service that scales out.
func BenchmarkAllow(b *testing.B) {
	ctx := context.Background()

	// A generous limit, so the benchmark measures the allow path rather than the reject path. They
	// are different: a rejection does more work in SlidingLog and in TokenBucket.
	const limit = 1 << 30

	for _, tc := range []struct {
		name string
		l    Limiter
	}{
		{"fixed window", NewFixedWindow(limit, time.Minute)},
		{"sliding counter", NewSlidingCounter(limit, time.Minute)},
		{"token bucket (x/time/rate)", NewTokenBucket(limit, time.Minute, limit)},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := tc.l.Allow(ctx, "client"); err != nil {
					b.Fatal(err)
				}
			}
		})
	}

	// SlidingLog separately, because it grows a slice per request and a benchmark with a limit of
	// 2^30 would hold a billion timestamps.
	b.Run("sliding log (limit 1000)", func(b *testing.B) {
		l := NewSlidingLog(1_000, time.Minute)

		b.ReportAllocs()
		for b.Loop() {
			if _, err := l.Allow(ctx, "client"); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// BenchmarkRedisAllow is the same operation over a network round trip and a Lua script.
func BenchmarkRedisAllow(b *testing.B) {
	client := redistest.Client(b)
	ctx := context.Background()

	l := NewRedisLimiter(client, 1<<30, time.Minute, "rl-bench")

	b.Run("EVALSHA round trip", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := l.Allow(ctx, "client"); err != nil {
				b.Fatal(err)
			}
		}
	})

	// PING, as the floor: what a round trip costs when the command does nothing. The difference
	// between this and the line above is what the script itself costs.
	b.Run("PING (the round trip alone)", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if err := client.Ping(ctx).Err(); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// BenchmarkParallelAllow, because a limiter is called from every request handler at once and a mutex that
// looks free at one goroutine may not be.
func BenchmarkParallelAllow(b *testing.B) {
	ctx := context.Background()

	const limit = 1 << 30

	for _, tc := range []struct {
		name string
		l    Limiter
	}{
		{"fixed window", NewFixedWindow(limit, time.Minute)},
		{"sliding counter", NewSlidingCounter(limit, time.Minute)},
		{"token bucket", NewTokenBucket(limit, time.Minute, limit)},
	} {
		// One key, so every goroutine contends on the same entry. That is the worst case and it is
		// also the realistic one for a per-endpoint limit.
		b.Run(tc.name+"/one key", func(b *testing.B) {
			b.ReportAllocs()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					if _, err := tc.l.Allow(ctx, "shared"); err != nil {
						b.Fatal(err)
					}
				}
			})
		})
	}
}

// BenchmarkMiddleware is the whole path, so the limiter's cost can be compared against the handler it
// protects.
func BenchmarkMiddleware(b *testing.B) {
	handler := benchHandler()

	limited := Middleware(NewSlidingCounter(1<<30, time.Minute),
		func(r *http.Request) string { return r.RemoteAddr })(handler)

	b.Run("bare handler", func(b *testing.B) {
		w, r := benchRequest()

		b.ReportAllocs()
		for b.Loop() {
			handler.ServeHTTP(w, r)
		}
	})

	b.Run("with the limiter", func(b *testing.B) {
		w, r := benchRequest()

		b.ReportAllocs()
		for b.Loop() {
			limited.ServeHTTP(w, r)
		}
	})
}

// benchHandler is a handler that does the least possible, so the comparison is the middleware's cost and not
// a handler's.
func benchHandler() http.Handler {
	body := []byte("ok")

	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	})
}

// benchRequest returns a reusable writer and request.
//
// A nullWriter rather than httptest.NewRecorder, for the reason the http-tutorial benchmarks found:
// NewRecorder allocates about a kilobyte across ten allocations, which is more than most middleware, and
// using one per iteration hides every difference behind it.
func benchRequest() (http.ResponseWriter, *http.Request) {
	return &nullWriter{header: make(http.Header, 8)}, httptest.NewRequest("GET", "/", nil)
}

type nullWriter struct {
	header http.Header
	status int
}

func (w *nullWriter) Header() http.Header         { return w.header }
func (w *nullWriter) Write(b []byte) (int, error) { return len(b), nil }
func (w *nullWriter) WriteHeader(status int)      { w.status = status }
