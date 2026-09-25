package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"time"
)

// Context in an HTTP server and client
// ====================================
//
// This is where context earns its place, and where most Go programmers first
// meet it.
//
//	SERVER: r.Context() is cancelled when the client disconnects, when the
//	        handler returns, or when Server.Shutdown is called. Pass it to
//	        everything the handler does.
//
//	CLIENT: http.NewRequestWithContext attaches a context to an outbound
//	        request. Cancelling it aborts the request mid-flight, including
//	        while reading the response body.
//
// Chaining the two is what makes a deadline mean something across services:
// the caller's remaining budget becomes the callee's budget.

// slowWork simulates a handler's real work, respecting cancellation.
func slowWork(ctx context.Context, d time.Duration) error {
	select {
	case <-time.After(d):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// handlerUsingRequestContext is the correct shape. r.Context() is cancelled
// when the client goes away, so the handler stops doing work nobody will read.
//
// The counter lets the test prove the work actually stopped, rather than the
// handler merely returning early while a goroutine carried on.
type handlerStats struct {
	started   atomic.Int64
	completed atomic.Int64
	abandoned atomic.Int64
}

func handlerUsingRequestContext(stats *handlerStats, work time.Duration) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		stats.started.Add(1)

		if err := slowWork(r.Context(), work); err != nil {
			stats.abandoned.Add(1)
			// The client has gone, so writing a status is pointless but
			// harmless. Logging the cause is the useful part.
			http.Error(w, "request cancelled", http.StatusRequestTimeout)
			return
		}

		stats.completed.Add(1)
		_, _ = fmt.Fprintln(w, "done")
	}
}

// handlerIgnoringContext is the bug: it does the full work whatever happens,
// so a client that disconnects after 1ms still costs the server the full
// duration. Under load that is how a service falls over.
func handlerIgnoringContext(stats *handlerStats, work time.Duration) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		stats.started.Add(1)

		time.Sleep(work) // no cancellation path at all

		stats.completed.Add(1)
		_, _ = fmt.Fprintln(w, "done")
	}
}

// timeoutMiddleware gives every request a budget. http.TimeoutHandler does
// this in the standard library and also writes a 503; this version is spelled
// out because the mechanism is the lesson.
func timeoutMiddleware(d time.Duration, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), d)
		defer cancel()

		// The handler sees the tighter deadline through r.Context().
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// requestIDMiddleware is the canonical use of context values: middleware
// attaches, handlers read, and nothing in between needs a parameter.
func requestIDMiddleware(id string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := WithRequestID(r.Context(), id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// echoRequestID reads what the middleware attached.
func echoRequestID(w http.ResponseWriter, r *http.Request) {
	id, ok := RequestID(r.Context())
	if !ok {
		http.Error(w, "no request id", http.StatusInternalServerError)
		return
	}
	_, _ = fmt.Fprint(w, id)
}

// callWithContext is the client side. NewRequestWithContext, never
// http.Get: the plain helpers have no way to be cancelled.
func callWithContext(ctx context.Context, url string) (status int, body string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, "", fmt.Errorf("build request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, "", fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // response body close on the read path

	// The context also covers reading the body, which is where a slow server
	// actually hurts. A timeout that stops at Do and not at ReadAll is not a
	// timeout.
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, "", fmt.Errorf("read body: %w", err)
	}

	return resp.StatusCode, string(data), nil
}

// serverAndClientTogether wires both ends: a server whose handler respects the
// request context, and a client that cancels partway.
func serverAndClientTogether(handlerWork, clientPatience time.Duration, respectContext bool) (stats *handlerStats, err error) {
	stats = &handlerStats{}

	var h http.Handler
	if respectContext {
		h = handlerUsingRequestContext(stats, handlerWork)
	} else {
		h = handlerIgnoringContext(stats, handlerWork)
	}

	srv := httptest.NewServer(h)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), clientPatience)
	defer cancel()

	_, _, err = callWithContext(ctx, srv.URL)

	// Give the handler a moment to notice the disconnect and record it.
	time.Sleep(50 * time.Millisecond)

	return stats, err
}

// What actually crosses an HTTP hop
// ---------------------------------
//
// This is the part I got wrong first time, and it is worth stating plainly:
//
//	CANCELLATION crosses.  The client aborting closes the TCP connection, and
//	                       the server's r.Context() is cancelled.
//	THE DEADLINE DOES NOT. HTTP has no standard header for "you have 200ms
//	                       left", so the server's context has NO deadline at
//	                       all, whatever the caller set.
//
// gRPC does propagate it, via the `grpc-timeout` header, which is one concrete
// reason gRPC is nicer for service-to-service work. Over plain HTTP you have to
// send it yourself.
//
// The practical difference: with cancellation only, the server keeps working
// until the client gives up and disconnects. With the deadline propagated, the
// server knows its budget up front and can refuse work it cannot finish.

// deadlineHeader is the convention this file uses. gRPC's equivalent is
// `grpc-timeout`, with a unit suffix; Envoy uses `x-envoy-expected-rq-timeout-ms`.
const deadlineHeader = "X-Request-Timeout-Ms"

// observedBudget records what a downstream handler was actually given.
type observedBudget struct {
	hasDeadline atomic.Bool
	remainingMs atomic.Int64
}

// plainHopLosesTheDeadline demonstrates the default behaviour: the downstream
// handler has a cancellable context with no deadline on it.
func plainHopLosesTheDeadline(callerBudget time.Duration) (observed *observedBudget, err error) {
	observed = &observedBudget{}

	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		d, ok := r.Context().Deadline()
		observed.hasDeadline.Store(ok)
		if ok {
			observed.remainingMs.Store(time.Until(d).Milliseconds())
		}
		_, _ = fmt.Fprint(w, "ok")
	}))
	defer downstream.Close()

	ctx, cancel := context.WithTimeout(context.Background(), callerBudget)
	defer cancel()

	_, _, err = callWithContext(ctx, downstream.URL)
	return observed, err
}

// withDeadlinePropagation sends the remaining budget as a header, which is
// what you have to do over plain HTTP.
func withDeadlinePropagation(ctx context.Context, url string) (status int, body string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, "", fmt.Errorf("build request: %w", err)
	}

	// Send whatever budget is left, if there is one.
	if d, ok := ctx.Deadline(); ok {
		remaining := time.Until(d)
		if remaining <= 0 {
			return 0, "", fmt.Errorf("call %s: %w", url, context.DeadlineExceeded)
		}
		req.Header.Set(deadlineHeader, strconv.FormatInt(remaining.Milliseconds(), 10))
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, "", fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // read path

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, "", fmt.Errorf("read body: %w", err)
	}
	return resp.StatusCode, string(data), nil
}

// deadlineFromHeaderMiddleware is the receiving half: read the header and
// apply it as a real deadline on the request context.
//
// Note the clamp. A caller claiming an hour should not get an hour; the
// server's own maximum still applies, because a header is input and input is
// not to be trusted.
func deadlineFromHeaderMiddleware(maxBudget time.Duration, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		budget := maxBudget

		if raw := r.Header.Get(deadlineHeader); raw != "" {
			if ms, err := strconv.ParseInt(raw, 10, 64); err == nil && ms > 0 {
				if d := time.Duration(ms) * time.Millisecond; d < budget {
					budget = d
				}
			}
		}

		ctx, cancel := context.WithTimeout(r.Context(), budget)
		defer cancel()

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// propagatedHopKeepsTheDeadline is the same experiment with the header wired
// up at both ends.
func propagatedHopKeepsTheDeadline(callerBudget, serverMax time.Duration) (observed *observedBudget, err error) {
	observed = &observedBudget{}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		d, ok := r.Context().Deadline()
		observed.hasDeadline.Store(ok)
		if ok {
			observed.remainingMs.Store(time.Until(d).Milliseconds())
		}
		_, _ = fmt.Fprint(w, "ok")
	})

	downstream := httptest.NewServer(deadlineFromHeaderMiddleware(serverMax, handler))
	defer downstream.Close()

	ctx, cancel := context.WithTimeout(context.Background(), callerBudget)
	defer cancel()

	_, _, err = withDeadlinePropagation(ctx, downstream.URL)
	return observed, err
}

// cancellationDoesCross is the half that works with no effort at all: the
// client disconnecting cancels the server's request context.
func cancellationDoesCross(handlerWork, clientPatience time.Duration) (serverSawCancellation bool) {
	sawIt := make(chan struct{}, 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(handlerWork):
			_, _ = fmt.Fprint(w, "ok")
		case <-r.Context().Done():
			select {
			case sawIt <- struct{}{}:
			default:
			}
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), clientPatience)
	defer cancel()

	_, _, _ = callWithContext(ctx, srv.URL)

	select {
	case <-sawIt:
		return true
	case <-time.After(time.Second):
		return false
	}
}

// gracefulShutdown is the other half: Server.Shutdown stops accepting new
// connections and waits for in-flight handlers, bounded by the context it is
// given.
func gracefulShutdown(inFlightWork, shutdownBudget time.Duration) (finished bool, shutdownErr error) {
	completed := make(chan struct{})

	mux := http.NewServeMux()
	mux.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(inFlightWork)
		close(completed)
		_, _ = fmt.Fprint(w, "ok")
	})

	srv := httptest.NewServer(mux)

	// Start a request and let it get going.
	go func() {
		// The response is irrelevant; starting the request is the point. The
		// body is still closed, because an unclosed body holds the connection
		// open and Shutdown would then wait for it, which would make this
		// demo measure the wrong thing entirely.
		resp, err := http.Get(srv.URL + "/slow") //nolint:noctx // deliberately uncancellable: it must survive shutdown
		if err == nil {
			_ = resp.Body.Close()
		}
	}()
	time.Sleep(20 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), shutdownBudget)
	defer cancel()

	shutdownErr = srv.Config.Shutdown(ctx)
	srv.Close()

	select {
	case <-completed:
		finished = true
	default:
	}

	return finished, shutdownErr
}

// demoHTTP prints server and client integration.
func demoHTTP() {
	stats, err := serverAndClientTogether(300*time.Millisecond, 30*time.Millisecond, true)
	fmt.Printf("  handler respecting r.Context(), client gives up after 30ms:\n")
	fmt.Printf("    started=%d completed=%d abandoned=%d, client err: %v\n",
		stats.started.Load(), stats.completed.Load(), stats.abandoned.Load(),
		errors.Is(err, context.DeadlineExceeded))

	stats, _ = serverAndClientTogether(300*time.Millisecond, 30*time.Millisecond, false)
	fmt.Printf("  handler ignoring it, same client:\n")
	fmt.Printf("    started=%d completed=%d abandoned=%d   <- the work ran anyway\n",
		stats.started.Load(), stats.completed.Load(), stats.abandoned.Load())

	srv := httptest.NewServer(requestIDMiddleware("req-abc", http.HandlerFunc(echoRequestID)))
	defer srv.Close()
	_, body, _ := callWithContext(context.Background(), srv.URL)
	fmt.Printf("\n  middleware attached, handler read: %q\n", body)

	fmt.Printf("\n  what crosses an HTTP hop:\n")

	plain, _ := plainHopLosesTheDeadline(200 * time.Millisecond)
	fmt.Printf("    caller sets a 200ms budget, plain request:\n")
	fmt.Printf("      downstream has a deadline: %t   <- HTTP has no header for it\n",
		plain.hasDeadline.Load())

	propagated, _ := propagatedHopKeepsTheDeadline(200*time.Millisecond, 5*time.Second)
	fmt.Printf("    same budget, sent as %s:\n", deadlineHeader)
	fmt.Printf("      downstream has a deadline: %t, ~%dms remaining\n",
		propagated.hasDeadline.Load(), propagated.remainingMs.Load())

	clamped, _ := propagatedHopKeepsTheDeadline(10*time.Second, 100*time.Millisecond)
	fmt.Printf("    caller claims 10s, server caps at 100ms: ~%dms remaining (input is not trusted)\n",
		clamped.remainingMs.Load())

	fmt.Printf("    cancellation DOES cross with no effort: %t\n",
		cancellationDoesCross(500*time.Millisecond, 30*time.Millisecond))

	finished, serr := gracefulShutdown(60*time.Millisecond, time.Second)
	fmt.Printf("\n  graceful shutdown waited for the in-flight request: %t (err=%v)\n", finished, serr)
}
