package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestHandlerRespectingContextStopsWorking is the claim that matters for a
// server under load: an abandoned request must stop costing anything.
func TestHandlerRespectingContextStopsWorking(t *testing.T) {
	stats, err := serverAndClientTogether(300*time.Millisecond, 30*time.Millisecond, true)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("client err = %v, want DeadlineExceeded", err)
	}
	if got := stats.started.Load(); got != 1 {
		t.Errorf("handler started %d times, want 1", got)
	}
	if got := stats.completed.Load(); got != 0 {
		t.Errorf("handler completed %d times, want 0 — it should have noticed the disconnect", got)
	}
	if got := stats.abandoned.Load(); got != 1 {
		t.Errorf("handler abandoned %d times, want 1", got)
	}
}

// TestHandlerIgnoringContextKeepsWorking asserts the bug, so the comparison
// cannot quietly stop being true.
func TestHandlerIgnoringContextKeepsWorking(t *testing.T) {
	stats, err := serverAndClientTogether(200*time.Millisecond, 30*time.Millisecond, false)

	if err == nil {
		t.Error("the client should still have timed out")
	}
	if got := stats.completed.Load(); got != 1 {
		t.Errorf("handler completed %d times, want 1 — it ignores cancellation", got)
	}
	if got := stats.abandoned.Load(); got != 0 {
		t.Errorf("handler abandoned %d times, want 0", got)
	}
}

func TestRequestIDMiddleware(t *testing.T) {
	srv := httptest.NewServer(requestIDMiddleware("req-abc", http.HandlerFunc(echoRequestID)))
	defer srv.Close()

	status, body, err := callWithContext(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if status != http.StatusOK {
		t.Errorf("status = %d, want 200", status)
	}
	if body != "req-abc" {
		t.Errorf("body = %q, want req-abc", body)
	}
}

func TestEchoRequestIDWithoutMiddleware(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(echoRequestID))
	defer srv.Close()

	status, _, err := callWithContext(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if status != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500 when no middleware ran", status)
	}
}

func TestTimeoutMiddleware(t *testing.T) {
	stats := &handlerStats{}
	h := timeoutMiddleware(20*time.Millisecond, handlerUsingRequestContext(stats, 500*time.Millisecond))

	srv := httptest.NewServer(h)
	defer srv.Close()

	// The client is patient; the middleware is not.
	status, _, err := callWithContext(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if status != http.StatusRequestTimeout {
		t.Errorf("status = %d, want 408", status)
	}
	if got := stats.completed.Load(); got != 0 {
		t.Errorf("handler completed %d times, want 0", got)
	}
}

// TestDeadlineDoesNotCrossAPlainHop is the correction that cost this lesson a
// rewrite. HTTP carries no deadline, so the downstream context has none.
func TestDeadlineDoesNotCrossAPlainHop(t *testing.T) {
	observed, err := plainHopLosesTheDeadline(200 * time.Millisecond)

	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if observed.hasDeadline.Load() {
		t.Error("a plain HTTP hop should NOT give the downstream handler a deadline")
	}
}

// TestDeadlineCrossesWhenYouSendIt is the other half: propagation works, it
// just is not free.
func TestDeadlineCrossesWhenYouSendIt(t *testing.T) {
	observed, err := propagatedHopKeepsTheDeadline(200*time.Millisecond, 5*time.Second)

	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !observed.hasDeadline.Load() {
		t.Fatal("the downstream handler should have received a deadline")
	}

	remaining := observed.remainingMs.Load()
	if remaining <= 0 || remaining > 200 {
		t.Errorf("downstream budget = %dms, want something in (0, 200]", remaining)
	}
}

// TestServerClampsTheCallersClaim: a header is untrusted input. A caller
// asking for ten seconds must not get ten seconds.
func TestServerClampsTheCallersClaim(t *testing.T) {
	observed, err := propagatedHopKeepsTheDeadline(10*time.Second, 100*time.Millisecond)

	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !observed.hasDeadline.Load() {
		t.Fatal("expected a deadline")
	}

	remaining := observed.remainingMs.Load()
	if remaining > 100 {
		t.Errorf("downstream budget = %dms, want at most the server's 100ms maximum", remaining)
	}
}

func TestDeadlineFromHeaderMiddleware(t *testing.T) {
	tests := []struct {
		name       string
		header     string
		maxBudget  time.Duration
		wantAtMost int64
	}{
		{"no header falls back to the maximum", "", 100 * time.Millisecond, 100},
		{"a smaller claim is honoured", "50", time.Second, 50},
		{"a larger claim is clamped", "999999", 100 * time.Millisecond, 100},
		{"garbage falls back to the maximum", "not-a-number", 100 * time.Millisecond, 100},
		{"zero falls back to the maximum", "0", 100 * time.Millisecond, 100},
		{"negative falls back to the maximum", "-5", 100 * time.Millisecond, 100},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var remaining int64
			var has bool

			h := deadlineFromHeaderMiddleware(tt.maxBudget, http.HandlerFunc(
				func(w http.ResponseWriter, r *http.Request) {
					d, ok := r.Context().Deadline()
					has = ok
					if ok {
						remaining = time.Until(d).Milliseconds()
					}
				}))

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.header != "" {
				req.Header.Set(deadlineHeader, tt.header)
			}
			h.ServeHTTP(httptest.NewRecorder(), req)

			if !has {
				t.Fatal("the middleware should always set a deadline")
			}
			if remaining > tt.wantAtMost {
				t.Errorf("budget = %dms, want at most %dms", remaining, tt.wantAtMost)
			}
		})
	}
}

// TestCancellationDoesCross: the half that needs no header.
func TestCancellationDoesCross(t *testing.T) {
	if !cancellationDoesCross(500*time.Millisecond, 30*time.Millisecond) {
		t.Error("the server should see its request context cancelled when the client disconnects")
	}
}

func TestCallWithContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("hello"))
	}))
	defer srv.Close()

	status, body, err := callWithContext(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if status != http.StatusTeapot {
		t.Errorf("status = %d, want 418", status)
	}
	if body != "hello" {
		t.Errorf("body = %q, want hello", body)
	}
}

func TestCallWithContextCancelled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(time.Second)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, _, err := callWithContext(ctx, srv.URL)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want DeadlineExceeded", err)
	}
}

func TestGracefulShutdown(t *testing.T) {
	t.Run("waits for the in-flight request", func(t *testing.T) {
		finished, err := gracefulShutdown(60*time.Millisecond, 2*time.Second)

		if !finished {
			t.Error("shutdown should have waited for the in-flight handler")
		}
		if err != nil {
			t.Errorf("shutdown err = %v, want nil", err)
		}
	})

	t.Run("gives up when the budget runs out", func(t *testing.T) {
		_, err := gracefulShutdown(2*time.Second, 30*time.Millisecond)

		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("shutdown err = %v, want DeadlineExceeded", err)
		}
	})
}
