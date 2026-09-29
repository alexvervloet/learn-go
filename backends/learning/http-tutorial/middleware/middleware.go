// Package middleware covers the handler-wrapping pattern, which is the whole of Go's
// middleware story.
//
// # There is no middleware API
//
// Go has no Middleware type, no registration hook, no framework. There is one convention:
//
//	func(http.Handler) http.Handler
//
// A function taking a handler and returning a handler that wraps it. That is the entire
// contract, it is not declared anywhere in the standard library, and every Go HTTP library
// agrees on it, which is why chi's middleware works with the stdlib's router and with gin's
// and with anything else.
//
// Compare FastAPI, where middleware is a decorator on the app object and the framework owns
// the ordering. Here you own it, because a chain is just nested function calls.
//
// # Ordering
//
// Chain(a, b, c)(h) produces a(b(c(h))). On the way IN the order is a, b, c; on the way OUT
// it is c, b, a. That reversal is the thing to get right, and it decides real behaviour.
//
// Two orderings that matter, and the first one is the opposite of the advice usually given:
//
//	LOGGING GOES OUTSIDE RECOVERY. "Recovery must be outermost" is the common rule and
//	  it is wrong here. With Logger outside Recovery a panicking request produces TWO log
//	  lines, the panic and the request line with its 500. With Recovery outside Logger the
//	  panic never reaches the logger and the request line is lost entirely. Measured in
//	  TestRecoveryMustBeOutermost.
//
//	REQUEST ID GOES OUTSIDE LOGGING, or the log line has no ID to correlate on.
//
// The reason Recovery is often described as outermost is that it catches panics in other
// MIDDLEWARE, not just in the handler. That is a real argument, and it competes with the
// logging one; nothing catches a panic in whatever is outside the recovery.
//
// # The ResponseWriter wrapping problem
//
// Logging the status code needs the status code, and http.ResponseWriter has no way to read
// it back. So a logging middleware has to wrap the writer and remember what was written.
//
// That wrapper breaks things. http.ResponseWriter is one interface, but real writers also
// implement http.Flusher, http.Hijacker and io.ReaderFrom, and a wrapper that only
// implements ResponseWriter hides all of them. Streaming responses stop flushing, WebSocket
// upgrades stop working, and sendfile optimisations quietly turn into byte copies.
//
// Go 1.20 added http.ResponseController, which is the fix: it finds the underlying
// capability by unwrapping, so a wrapper only needs an Unwrap method. TestWrapperKeepsFlush
// measures both halves of that.
package middleware

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"strconv"
	"sync/atomic"
	"time"
)

// Middleware is the convention, given a name so signatures read better. Nothing requires this
// type to exist and no library exports a compatible one, because the underlying func type is
// what everything agrees on.
type Middleware func(http.Handler) http.Handler

// Chain composes middleware so that the first argument is the outermost wrapper.
//
// Chain(a, b, c)(h) is a(b(c(h))). The loop runs BACKWARDS, because each step wraps what has
// been built so far, and building from the front would invert the order. Getting that
// backwards is the classic bug and it is invisible until a panic skips the log line.
func Chain(middlewares ...Middleware) Middleware {
	return func(final http.Handler) http.Handler {
		h := final

		for i := len(middlewares) - 1; i >= 0; i-- {
			h = middlewares[i](h)
		}

		return h
	}
}

// Request IDs
// ===========

// requestIDKey is an unexported struct type, which is the Go convention for context keys.
//
// A string key would collide with any other package using the same string, and the collision
// would be silent: one package's value would be read as another's. An unexported type cannot
// be constructed outside this package, so collision is impossible. This is why every
// context-key declaration you see looks like this.
type requestIDKey struct{}

var requestCounter atomic.Uint64

// RequestID attaches an ID to the request context and echoes it in a header.
//
// The counter is atomic because middleware runs on many goroutines at once: net/http serves
// every request in its own goroutine, so anything a middleware shares between requests is
// concurrent by default. See go-concepts/09.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if id == "" {
			id = "req-" + strconv.FormatUint(requestCounter.Add(1), 10)
		}

		w.Header().Set("X-Request-ID", id)

		// A new context, and a new request holding it. http.Request is immutable by
		// convention: WithContext returns a shallow copy rather than mutating.
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id)))
	})
}

// RequestIDFrom returns the request ID, or "" if none was set.
//
// A typed accessor rather than making callers do the assertion, which is the other half of the
// context-key convention: the key is unexported and the getter is exported, so the type
// assertion happens once in the package that owns the key.
func RequestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

// Response recording
// ==================

// recorder wraps http.ResponseWriter to remember the status and byte count.
//
// # Why it has Unwrap AND Flush AND Hijack
//
// Embedding http.ResponseWriter promotes Header, Write and WriteHeader, and nothing else. The
// optional interfaces the real writer has (http.Flusher, http.Hijacker) are hidden, so a
// streaming handler inside this middleware cannot flush and a WebSocket upgrade cannot hijack.
//
// Go 1.20's answer is Unwrap: http.ResponseController walks Unwrap until it finds a writer that
// can do the job. That covers code YOU write, as long as you use ResponseController.
//
// It does not cover code other people wrote. chi's Compress, and plenty of other middleware,
// still does `w.(http.Flusher)` on the writer it wraps, which is this recorder when Compress is
// inside Logger. The first version of this type had only Unwrap, the comment said that restored
// "every optional capability at once", and Production's /stream delivered everything at the end
// while WebSocket upgrades failed. TestProductionStreams and TestProductionCanHijack are the
// regression tests.
//
// So a wrapper that sits in someone else's chain needs both: Unwrap for ResponseController, and
// the methods themselves for type assertions. Each method delegates through ResponseController,
// so it keeps working when the writer underneath is itself a wrapper.
type recorder struct {
	http.ResponseWriter

	status      int
	written     int64
	wroteHeader bool
}

// Unwrap lets http.ResponseController reach the real writer.
func (r *recorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// Flush makes the recorder an http.Flusher, for middleware that type-asserts rather than using
// ResponseController. The error is dropped because http.Flusher has no way to return it.
func (r *recorder) Flush() {
	_ = http.NewResponseController(r.ResponseWriter).Flush()
}

// Hijack makes the recorder an http.Hijacker, which is what a WebSocket upgrade needs.
func (r *recorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(r.ResponseWriter).Hijack()
}

// WriteHeader records the status and passes it through. A second call is swallowed, because
// net/http ignores it too and the recorded status has to match what the client got.
func (r *recorder) WriteHeader(status int) {
	if r.wroteHeader {
		// A second WriteHeader is a bug in the handler: net/http logs
		// "superfluous response.WriteHeader call" and ignores it. Swallowing it here
		// keeps the recorded status truthful.
		return
	}

	r.ResponseWriter.WriteHeader(status)

	// A 1xx is informational (103 Early Hints is the one handlers send) and is followed by
	// the real status, which net/http accepts. Only a final status ends the header phase.
	// The first version marked any status as final, so after a 103 the real 200 or 404 was
	// swallowed here and never reached the client.
	if status >= 200 {
		r.status = status
		r.wroteHeader = true
	}
}

// Write records the byte count, and supplies the implicit 200 that a handler writing a body
// without calling WriteHeader gets from net/http.
func (r *recorder) Write(b []byte) (int, error) {
	// A handler that writes without calling WriteHeader gets an implicit 200, and the
	// recorder has to know that or it reports 0.
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}

	n, err := r.ResponseWriter.Write(b)
	r.written += int64(n)

	return n, err
}

// Status returns the status written, defaulting to 200 for a handler that wrote nothing at
// all, which is what net/http sends.
func (r *recorder) Status() int {
	if r.status == 0 {
		return http.StatusOK
	}
	return r.status
}

// Logging
// =======

// Logger logs one line per request, with method, path, status, size and duration.
//
// slog rather than log: it is in the standard library as of Go 1.21, it does structured
// output, and a log line with fields is what anything downstream can actually query. The
// Python version reaches for structlog to get here.
func Logger(log *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &recorder{ResponseWriter: w}

			next.ServeHTTP(rec, r)

			log.Info("request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", rec.Status(),
				"bytes", rec.written,
				"duration", time.Since(start),
				"request_id", RequestIDFrom(r.Context()),
			)
		})
	}
}

// Recovery
// ========

// Recovery turns a panic in a handler into a 500.
//
// Without it, net/http already recovers the panic and closes the connection, so the server
// does not die. What it does not do is send a response, so the client sees a dropped
// connection rather than a 500, and nothing useful is logged.
//
// Where it goes is a trade-off, set out in the package doc. Outermost, it also catches a panic
// in any other middleware; but then Logger is inside it, and a panicking request loses its
// log line. Production puts Logger outside Recovery so the 500 is logged, and accepts that a
// panic inside Logger itself is not caught. (An earlier version of this comment said Recovery
// must be outermost, contradicting the package doc and Production.)
func Recovery(log *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				recovered := recover()
				if recovered == nil {
					return
				}

				// http.ErrAbortHandler is net/http's documented way for a handler to
				// abandon a response deliberately: net/http recovers it silently and
				// closes the connection. Turning it into a 500 would defeat that, so
				// it is re-panicked.
				//
				// The comparison is against the panic VALUE, not through errors.Is:
				// a panic value is an `any`, and net/http panics with that sentinel
				// itself rather than something wrapping it.
				if err, ok := recovered.(error); ok && errors.Is(err, http.ErrAbortHandler) {
					panic(recovered)
				}

				log.Error("panic recovered",
					"error", fmt.Sprint(recovered),
					"path", r.URL.Path,
					"request_id", RequestIDFrom(r.Context()),
					"stack", string(debug.Stack()),
				)

				// A best-effort 500. If the handler already wrote a status this does
				// nothing but log "superfluous WriteHeader", which is correct: the
				// response is already committed and cannot be taken back.
				http.Error(w, "internal server error", http.StatusInternalServerError)
			}()

			next.ServeHTTP(w, r)
		})
	}
}

// Timeouts
// ========

// Timeout gives the handler a deadline through the request context.
//
// It does NOT stop the handler. Go has no way to kill a goroutine, so all this can do is
// cancel the context and let the handler notice. A handler that ignores r.Context() runs to
// completion regardless, and whatever it writes after the deadline is still sent: this
// middleware never touches the response. (An earlier version of this comment said the
// response is discarded, which describes the standard library's http.TimeoutHandler. That
// one buffers the handler's output and replies 503 at the deadline instead, at the cost of
// holding every response in memory and breaking streaming.)
//
// That is the whole difference from a language with thread interruption, and it is why every
// blocking call in a Go handler should take a context.
func Timeout(d time.Duration) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), d)
			defer cancel()

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// Headers
// =======

// SecureHeaders sets the headers a service should send whether or not anyone asked.
//
// Deliberately short. Real deployments set these at the edge, and a Go service that sets them
// too is being defensive about being deployed somewhere that does not.
func SecureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()

		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")

		next.ServeHTTP(w, r)
	})
}

// MaxBody rejects requests whose body is longer than n bytes.
//
// http.MaxBytesReader is the important part, and it is not the same as checking
// Content-Length: a chunked request has no Content-Length, and a lying one is a trivial
// attack. MaxBytesReader caps what can actually be read, so a handler that decodes JSON
// cannot be made to allocate without bound.
//
// The reader returns an error on overflow rather than truncating, which is what lets the
// handler distinguish "too big" from "malformed".
func MaxBody(n int64) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, n)
			next.ServeHTTP(w, r)
		})
	}
}
