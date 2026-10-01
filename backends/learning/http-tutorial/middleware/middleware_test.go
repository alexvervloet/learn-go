package middleware

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	chimw "github.com/go-chi/chi/v5/middleware"
)

// discardLogger is a logger that produces nothing, for tests that do not inspect the output.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// captureLogger returns a logger writing JSON into a buffer, so a test can assert on fields.
func captureLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewJSONHandler(&buf, nil)), &buf
}

func ok(body string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	})
}

// TestOrderingIsOutsideIn pins down what Chain produces. The first argument is outermost, so
// on the way IN the order is as written and on the way OUT it is reversed.
func TestOrderingIsOutsideIn(t *testing.T) {
	var order []string

	tag := func(name string) Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, name+" in")
				next.ServeHTTP(w, r)
				order = append(order, name+" out")
			})
		}
	}

	h := Chain(tag("a"), tag("b"), tag("c"))(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			order = append(order, "handler")
		}))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))

	want := []string{"a in", "b in", "c in", "handler", "c out", "b out", "a out"}

	if len(order) != len(want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Errorf("step %d = %q, want %q (full: %v)", i, order[i], want[i], order)
		}
	}
}

// TestChainWithNoMiddlewareIsTheIdentity, because a variadic that breaks on zero arguments is
// a nuisance at every call site that builds its chain conditionally.
func TestChainWithNoMiddlewareIsTheIdentity(t *testing.T) {
	h := Chain()(ok("through"))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	if got := rec.Body.String(); got != "through" {
		t.Errorf("body = %q, want %q", got, "through")
	}
}

func TestRequestID(t *testing.T) {
	var seen string

	h := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = RequestIDFrom(r.Context())
	}))

	// Generated when absent.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	if seen == "" {
		t.Error("no request ID reached the handler")
	}
	if got := rec.Header().Get("X-Request-ID"); got != seen {
		t.Errorf("header = %q but the handler saw %q", got, seen)
	}

	// Honoured when supplied, so a trace ID survives across services.
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Request-ID", "from-upstream")

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if seen != "from-upstream" {
		t.Errorf("handler saw %q, want the supplied ID", seen)
	}
	if got := rec.Header().Get("X-Request-ID"); got != "from-upstream" {
		t.Errorf("header = %q, want the supplied ID", got)
	}
}

// TestRequestIDsAreUnique exercises the atomic counter under real concurrency, which is the
// condition every middleware actually runs in.
func TestRequestIDsAreUnique(t *testing.T) {
	const n = 500

	ids := make(chan string, n)

	h := RequestID(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		ids <- RequestIDFrom(r.Context())
	}))

	done := make(chan struct{})
	for range n {
		go func() {
			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
			done <- struct{}{}
		}()
	}
	for range n {
		<-done
	}
	close(ids)

	seen := make(map[string]bool, n)
	for id := range ids {
		if seen[id] {
			t.Fatalf("duplicate request ID %q", id)
		}
		seen[id] = true
	}

	if len(seen) != n {
		t.Errorf("got %d distinct IDs from %d requests", len(seen), n)
	}
}

// TestRequestIDFromAnEmptyContext returns "" rather than panicking, which matters because any
// code path that skips the middleware still calls the getter.
func TestRequestIDFromAnEmptyContext(t *testing.T) {
	if got := RequestIDFrom(context.Background()); got != "" {
		t.Errorf("RequestIDFrom(empty) = %q, want \"\"", got)
	}
}

func TestLogger(t *testing.T) {
	log, buf := captureLogger()

	h := Chain(RequestID, Logger(log))(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte("hello"))
		}))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/things", nil))

	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("log output is not JSON: %v (%q)", err, buf.String())
	}

	checks := map[string]any{
		"method": "POST",
		"path":   "/things",
		"status": float64(http.StatusCreated),
		"bytes":  float64(5),
	}
	for field, want := range checks {
		if line[field] != want {
			t.Errorf("log %s = %v, want %v", field, line[field], want)
		}
	}

	if line["request_id"] == "" || line["request_id"] == nil {
		t.Error("the log line has no request_id; RequestID must be outside Logger")
	}
	if line["duration"] == nil {
		t.Error("the log line has no duration")
	}
}

// TestLoggerRecordsTheImplicitStatus: a handler that writes a body without calling WriteHeader
// gets an implicit 200, and a recorder that does not know that reports 0.
func TestLoggerRecordsTheImplicitStatus(t *testing.T) {
	log, buf := captureLogger()

	h := Logger(log)(ok("body"))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))

	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatal(err)
	}

	if line["status"] != float64(200) {
		t.Errorf("status = %v, want 200 for a handler that only wrote a body", line["status"])
	}
}

// TestLoggerRecordsAHandlerThatWroteNothing: no WriteHeader, no Write. net/http sends 200, so
// that is what the log must say.
func TestLoggerRecordsAHandlerThatWroteNothing(t *testing.T) {
	log, buf := captureLogger()

	h := Logger(log)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))

	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatal(err)
	}

	if line["status"] != float64(200) {
		t.Errorf("status = %v, want 200", line["status"])
	}
	if line["bytes"] != float64(0) {
		t.Errorf("bytes = %v, want 0", line["bytes"])
	}
}

func TestRecovery(t *testing.T) {
	log, buf := captureLogger()

	h := Recovery(log)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/explode", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "internal server error") {
		t.Errorf("body = %q", rec.Body.String())
	}

	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("log output is not JSON: %v", err)
	}
	if line["error"] != "boom" {
		t.Errorf("logged error = %v, want boom", line["error"])
	}
	if stack, _ := line["stack"].(string); !strings.Contains(stack, "middleware_test.go") {
		t.Error("the log line has no useful stack trace")
	}
}

// TestRecoveryMustBeOutermost is named after the advice, and it disproves it.
//
// "Recovery must be the outermost middleware" is the rule everyone repeats. Against a LOGGER
// it is backwards: with Recovery outside, the panic never reaches the logger and the request
// log line is lost. With Logger outside, you get both.
//
//	Recovery outside Logger:  1 log line   (the panic; no request line)
//	Logger outside Recovery:  2 log lines  (the panic, and the request with status 500)
//
// The argument for putting Recovery outermost is real but different: it catches panics in other
// middleware, not just in the handler. Nothing catches a panic in whatever sits outside it.
func TestRecoveryMustBeOutermost(t *testing.T) {
	panicking := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	})

	countLines := func(buf *bytes.Buffer) int {
		trimmed := strings.TrimSpace(buf.String())
		if trimmed == "" {
			return 0
		}
		return strings.Count(trimmed, "\n") + 1
	}

	t.Run("recovery outside logging loses the request line", func(t *testing.T) {
		log, buf := captureLogger()

		h := Chain(Recovery(log), Logger(log))(panicking)

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

		if rec.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want 500", rec.Code)
		}
		if got := countLines(buf); got != 1 {
			t.Errorf("%d log lines, want 1: %q", got, buf.String())
		}
		if !strings.Contains(buf.String(), "panic recovered") {
			t.Error("the panic was not logged")
		}
		if strings.Contains(buf.String(), `"msg":"request"`) {
			t.Error("a request line appeared; the panic should have escaped the logger")
		}
	})

	t.Run("logging outside recovery gets both", func(t *testing.T) {
		log, buf := captureLogger()

		h := Chain(Logger(log), Recovery(log))(panicking)

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

		if rec.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want 500", rec.Code)
		}
		if got := countLines(buf); got != 2 {
			t.Errorf("%d log lines, want 2: %q", got, buf.String())
		}
		if !strings.Contains(buf.String(), "panic recovered") {
			t.Error("the panic was not logged")
		}
		if !strings.Contains(buf.String(), `"status":500`) {
			t.Error("the request line does not report the 500")
		}
	})

	t.Run("no recovery at all", func(t *testing.T) {
		log, buf := captureLogger()

		h := Logger(log)(panicking)

		var panicked bool
		func() {
			defer func() {
				if recover() != nil {
					panicked = true
				}
			}()
			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
		}()

		if !panicked {
			t.Error("the panic should have escaped the logger")
		}
		if got := countLines(buf); got != 0 {
			t.Errorf("a panicking request produced %d log lines: %q", got, buf.String())
		}
	})
}

// TestRecoveryRepanicsErrAbortHandler: net/http documents ErrAbortHandler as the way a handler
// abandons a response on purpose. Turning it into a 500 would defeat that.
func TestRecoveryRepanicsErrAbortHandler(t *testing.T) {
	h := Recovery(discardLogger())(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	}))

	var recovered any
	func() {
		defer func() { recovered = recover() }()
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	}()

	if recovered == nil {
		t.Fatal("ErrAbortHandler was swallowed; it must propagate to net/http")
	}
	if err, isErr := recovered.(error); !isErr || !errors.Is(err, http.ErrAbortHandler) {
		t.Errorf("re-panicked with %v, want http.ErrAbortHandler", recovered)
	}
}

// TestRecoveryAfterTheResponseIsCommitted: once a status is written it cannot be taken back,
// so the 500 is best-effort. The client gets a truncated 200, which is the honest outcome and
// worth knowing about.
func TestRecoveryAfterTheResponseIsCommitted(t *testing.T) {
	h := Recovery(discardLogger())(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("partial"))
		panic("too late")
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d; once committed it cannot become a 500", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "partial") {
		t.Errorf("body = %q, want the partial response", rec.Body.String())
	}

	t.Log("the panic was recovered but the 200 stands: a middleware cannot un-send a status")
}

// TestWrapperKeepsFlush is the ResponseController half. A recorder that wraps the writer hides
// http.Flusher, and one Unwrap method gives it back.
func TestWrapperKeepsFlush(t *testing.T) {
	t.Run("through the recorder, using ResponseController", func(t *testing.T) {
		var flushed bool

		h := Logger(discardLogger())(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("chunk"))

			// This is the Go 1.20+ way, and it works through any wrapper that has
			// Unwrap.
			if err := http.NewResponseController(w).Flush(); err != nil {
				t.Errorf("Flush through the wrapper failed: %v", err)
				return
			}
			flushed = true
		}))

		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))

		if !flushed {
			t.Error("the handler could not flush through the recorder")
		}
	})

	t.Run("a type assertion to http.Flusher also works through the wrapper", func(t *testing.T) {
		var flusher, hijacker bool

		h := Logger(discardLogger())(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			// The pre-1.20 idiom. Your own code should use ResponseController, but the
			// middleware you import may not, and it sees this recorder.
			_, flusher = w.(http.Flusher)
			_, hijacker = w.(http.Hijacker)
		}))

		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))

		if !flusher || !hijacker {
			t.Errorf("Flusher=%v Hijacker=%v: Unwrap alone is not enough for code that type-asserts",
				flusher, hijacker)
		}
	})
}

// TestProductionStreams sends one line, waits for the client to READ it, and only then sends the second.
//
// If anything in the chain swallows Flush, the first line sits in a buffer until the handler returns, the
// client never sees it, and the handler waits forever. So a broken chain fails this test with a timeout rather
// than passing slowly. The first version of the recorder had Unwrap and no Flush method, which is enough for
// a handler using http.ResponseController and NOT enough for chi's Compress, which type-asserts http.Flusher
// on the writer it wraps. /stream in cmd/server delivered everything at the end.
func TestProductionStreams(t *testing.T) {
	read := make(chan struct{})

	srv := httptest.NewServer(Production(discardLogger(), false)(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/x-ndjson")

			_, _ = w.Write([]byte("{\"n\":1}\n"))
			if err := http.NewResponseController(w).Flush(); err != nil {
				t.Errorf("Flush through Production failed: %v", err)
				return
			}

			select {
			case <-read:
			case <-r.Context().Done():
				return
			}

			_, _ = w.Write([]byte("{\"n\":2}\n"))
		})))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)

	// Asking for gzip is what makes chi's Compress wrap the writer, and a streaming client
	// behind a browser always asks. DisableCompression stops the client decoding it for us,
	// so read through a gzip reader when the server did compress.
	req.Header.Set("Accept-Encoding", "gzip")

	resp, err := (&http.Transport{DisableCompression: true}).RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	var body io.Reader = resp.Body
	if resp.Header.Get("Content-Encoding") == "gzip" {
		zr, err := gzip.NewReader(resp.Body)
		if err != nil {
			t.Fatalf("the first line never arrived: %v", err)
		}
		body = zr
	}

	line, err := bufio.NewReader(body).ReadString('\n')
	if err != nil {
		t.Fatalf("the first line never arrived before the handler finished: %v", err)
	}

	close(read)

	if line != "{\"n\":1}\n" {
		t.Errorf("first line = %q", line)
	}
}

// TestProductionCanHijack is the WebSocket half. An upgrade takes over the connection with Hijack, and a
// wrapper that hides http.Hijacker makes every upgrade fail with "not supported" through this chain.
func TestProductionCanHijack(t *testing.T) {
	srv := httptest.NewServer(Production(discardLogger(), false)(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			conn, buf, err := http.NewResponseController(w).Hijack()
			if err != nil {
				t.Errorf("Hijack through Production failed: %v", err)
				return
			}
			defer func() { _ = conn.Close() }()

			_, _ = buf.WriteString("HTTP/1.1 200 OK\r\nContent-Length: 2\r\nConnection: close\r\n\r\nok")
			_ = buf.Flush()
		})))
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ok" {
		t.Errorf("body = %q, want the bytes written on the hijacked connection", body)
	}
}

// TestRecorderUnwrapReachesTheRealWriter, stated directly.
func TestRecorderUnwrapReachesTheRealWriter(t *testing.T) {
	underlying := httptest.NewRecorder()
	rec := &recorder{ResponseWriter: underlying}

	if rec.Unwrap() != http.ResponseWriter(underlying) {
		t.Error("Unwrap did not return the wrapped writer")
	}
}

// TestRecorderIgnoresASecondWriteHeader: a handler calling WriteHeader twice is a bug, and
// net/http logs it and keeps the first status. The recorder has to agree, or the log line
// disagrees with what the client got.
func TestRecorderIgnoresASecondWriteHeader(t *testing.T) {
	log, buf := captureLogger()

	h := Logger(log)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		w.WriteHeader(http.StatusInternalServerError) // ignored by net/http
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	if rec.Code != http.StatusTeapot {
		t.Errorf("client got %d, want 418", rec.Code)
	}

	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatal(err)
	}
	if line["status"] != float64(http.StatusTeapot) {
		t.Errorf("log says %v, client got 418", line["status"])
	}
}

func TestTimeoutCancelsTheContext(t *testing.T) {
	var deadlineHit bool

	h := Timeout(10 * time.Millisecond)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
			deadlineHit = true
			http.Error(w, "timed out", http.StatusGatewayTimeout)
		case <-time.After(2 * time.Second):
			_, _ = w.Write([]byte("finished"))
		}
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/slow", nil))

	if !deadlineHit {
		t.Fatal("the handler did not see the deadline")
	}
	if rec.Code != http.StatusGatewayTimeout {
		t.Errorf("status = %d, want 504", rec.Code)
	}
}

// TestTimeoutDoesNotStopTheHandler is the part people expect to work and which cannot. Go has
// no way to kill a goroutine, so a handler that ignores its context runs to completion.
func TestTimeoutDoesNotStopTheHandler(t *testing.T) {
	finished := make(chan struct{})

	h := Timeout(1 * time.Millisecond)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Deliberately ignores r.Context().
		time.Sleep(20 * time.Millisecond)
		_, _ = w.Write([]byte("finished anyway"))
		close(finished)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/ignores-context", nil))

	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("the handler never finished")
	}

	if got := rec.Body.String(); got != "finished anyway" {
		t.Errorf("body = %q, want the handler's full response", got)
	}

	t.Log("Timeout cancelled the context and the handler ignored it, so it ran to " +
		"completion and its response was sent. A timeout is a request, not a kill.")
}

func TestSecureHeaders(t *testing.T) {
	h := SecureHeaders(ok("body"))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	want := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "strict-origin-when-cross-origin",
	}
	for header, value := range want {
		if got := rec.Header().Get(header); got != value {
			t.Errorf("%s = %q, want %q", header, got, value)
		}
	}
}

func TestMaxBody(t *testing.T) {
	h := MaxBody(10)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "too big", http.StatusRequestEntityTooLarge)
			return
		}
		_, _ = w.Write(body)
	}))

	t.Run("under the limit", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/", strings.NewReader("small"))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK || rec.Body.String() != "small" {
			t.Errorf("status %d body %q", rec.Code, rec.Body.String())
		}
	})

	t.Run("over the limit", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/", strings.NewReader(strings.Repeat("x", 100)))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Errorf("status = %d, want 413", rec.Code)
		}
	})

	t.Run("a lying Content-Length does not help the attacker", func(t *testing.T) {
		// Content-Length says 5 and the body is 100 bytes. MaxBytesReader caps what is
		// READ, so the lie changes nothing.
		req := httptest.NewRequest("POST", "/", strings.NewReader(strings.Repeat("x", 100)))
		req.ContentLength = 5

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Errorf("status = %d, want 413", rec.Code)
		}
	})
}

// TestChiCleanPathNeedsChisRouter documents the one chi middleware that does not work with the
// standard library's router.
//
// It panics with a nil pointer dereference on EVERY request, not only on paths that need
// cleaning, because it writes the cleaned path into chi.RouteContext and there is no route
// context without chi's router. I tested all seventeen of chi's middleware against a bare
// handler; this is the only one.
func TestChiCleanPathNeedsChisRouter(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})

	for _, target := range []string{"/already/clean", "//needs//cleaning"} {
		var recovered any

		func() {
			defer func() { recovered = recover() }()

			chimw.CleanPath(handler).ServeHTTP(
				httptest.NewRecorder(),
				httptest.NewRequest("GET", target, nil),
			)
		}()

		if recovered == nil {
			t.Errorf("%s: chimw.CleanPath did not panic; chi may have fixed this", target)
		}
	}

	// And every other chi middleware used in this package is fine without chi's router.
	for name, mw := range map[string]Middleware{
		"Compress":     chimw.Compress(5),
		"StripSlashes": chimw.StripSlashes,
		"Recoverer":    chimw.Recoverer,
	} {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("chimw.%s panicked without chi's router: %v", name, r)
				}
			}()

			rec := httptest.NewRecorder()
			mw(handler).ServeHTTP(rec, httptest.NewRequest("GET", "/x", nil))

			if rec.Code != http.StatusOK {
				t.Errorf("chimw.%s gave status %d", name, rec.Code)
			}
		}()
	}
}

func TestCleanPath(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(r.URL.Path))
	})

	tests := []struct {
		target     string
		wantStatus int
		wantResult string // the body for 200, the Location for a redirect
	}{
		{"/already/clean", http.StatusOK, "/already/clean"},
		{"//double//slashes", http.StatusPermanentRedirect, "/double/slashes"},
		{"/a/./b", http.StatusPermanentRedirect, "/a/b"},
		{"/a/b/../c", http.StatusPermanentRedirect, "/a/c"},

		// A trailing slash survives, because routing/'s subtree patterns need it and
		// path.Clean strips it.
		{"/subtree/", http.StatusOK, "/subtree/"},
		{"//subtree//", http.StatusPermanentRedirect, "/subtree/"},

		// The query string survives the redirect.
		{"//x?a=1", http.StatusPermanentRedirect, "/x?a=1"},
	}

	for _, tt := range tests {
		t.Run(tt.target, func(t *testing.T) {
			rec := httptest.NewRecorder()
			CleanPath(handler).ServeHTTP(rec, httptest.NewRequest("GET", tt.target, nil))

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}

			got := rec.Body.String()
			if tt.wantStatus != http.StatusOK {
				got = rec.Header().Get("Location")
			}
			if got != tt.wantResult {
				t.Errorf("got %q, want %q", got, tt.wantResult)
			}
		})
	}
}

// TestRealIPTakesTheRightmostEntry is the bug chi's deprecated RealIP has, and the reason this
// package ships its own.
//
// A proxy APPENDS the address it saw, so the rightmost entry is the only one not under the
// client's control. Taking the leftmost lets anyone choose their own apparent IP, which defeats
// rate limiting and audit logging together.
func TestRealIPTakesTheRightmostEntry(t *testing.T) {
	var seen string

	handler := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen = r.RemoteAddr
	})

	request := func() *http.Request {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = "10.0.0.1:1234"
		// An attacker prepended 1.2.3.4; the proxy appended 203.0.113.9.
		r.Header.Set("X-Forwarded-For", "1.2.3.4, 203.0.113.9")
		return r
	}

	t.Run("trusting the headers uses the rightmost entry", func(t *testing.T) {
		RealIP(true)(handler).ServeHTTP(httptest.NewRecorder(), request())

		if seen != "203.0.113.9" {
			t.Errorf("RemoteAddr = %q, want the rightmost entry", seen)
		}
		if seen == "1.2.3.4" {
			t.Error("took the client-supplied leftmost entry, which is the spoofing bug")
		}
	})

	t.Run("not trusting them leaves the peer address alone", func(t *testing.T) {
		RealIP(false)(handler).ServeHTTP(httptest.NewRecorder(), request())

		if seen != "10.0.0.1:1234" {
			t.Errorf("RemoteAddr = %q, want the untouched peer address", seen)
		}
	})

	t.Run("X-Real-IP is used when X-Forwarded-For is absent", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = "10.0.0.1:1234"
		r.Header.Set("X-Real-IP", "198.51.100.7")

		RealIP(true)(handler).ServeHTTP(httptest.NewRecorder(), r)

		if seen != "198.51.100.7" {
			t.Errorf("RemoteAddr = %q", seen)
		}
	})

	t.Run("no headers at all", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = "10.0.0.1:1234"

		RealIP(true)(handler).ServeHTTP(httptest.NewRecorder(), r)

		if seen != "10.0.0.1:1234" {
			t.Errorf("RemoteAddr = %q, want the peer address", seen)
		}
	})
}

// TestProductionTrustsProxyHeadersOnlyWhenTold: a client talking to the server directly can send any
// X-Forwarded-For it likes. The first version of Production passed RealIP(true) unconditionally, so every
// deployment of it let clients choose the address it logged and rate-limited by.
func TestProductionTrustsProxyHeadersOnlyWhenTold(t *testing.T) {
	var seen string

	handler := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { seen = r.RemoteAddr })

	req := func() *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = "203.0.113.7:51234"
		r.Header.Set("X-Forwarded-For", "1.2.3.4, 198.51.100.9")
		return r
	}

	Production(discardLogger(), false)(handler).ServeHTTP(httptest.NewRecorder(), req())
	if seen != "203.0.113.7:51234" {
		t.Errorf("untrusted: RemoteAddr = %q; the forged header was believed", seen)
	}

	Production(discardLogger(), true)(handler).ServeHTTP(httptest.NewRecorder(), req())
	if seen != "198.51.100.9" {
		t.Errorf("trusted: RemoteAddr = %q, want the rightmost entry, the one the proxy added", seen)
	}
}

// TestRecorderPassesTheStatusAfterEarlyHints: 103 Early Hints comes before the real status.
func TestRecorderPassesTheStatusAfterEarlyHints(t *testing.T) {
	srv := httptest.NewServer(Logger(discardLogger())(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Link", "</style.css>; rel=preload; as=style")
		w.WriteHeader(http.StatusEarlyHints)
		w.WriteHeader(http.StatusCreated)
	})))
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Errorf("status %d after a 103, want 201: the recorder swallowed the real status", resp.StatusCode)
	}
}

// TestCrossOriginRefusesCrossSiteWrites is CSRF protection without tokens. Each case is a request a browser
// (or something that is not one) would send, and what http.CrossOriginProtection makes of it.
func TestCrossOriginRefusesCrossSiteWrites(t *testing.T) {
	protect, err := CrossOrigin("https://partner.example")
	if err != nil {
		t.Fatal(err)
	}

	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	h := protect(ok)

	for _, tc := range []struct {
		name    string
		method  string
		headers map[string]string
		want    int
	}{
		{"cross-site POST", http.MethodPost, map[string]string{"Sec-Fetch-Site": "cross-site"}, http.StatusForbidden},
		{"cross-site DELETE", http.MethodDelete, map[string]string{"Sec-Fetch-Site": "cross-site"}, http.StatusForbidden},
		{"same-origin POST", http.MethodPost, map[string]string{"Sec-Fetch-Site": "same-origin"}, http.StatusOK},
		{"cross-site GET, which must not change state", http.MethodGet, map[string]string{"Sec-Fetch-Site": "cross-site"}, http.StatusOK},
		{"no browser headers: curl or a service", http.MethodPost, nil, http.StatusOK},
		{"old browser, Origin is another host", http.MethodPost, map[string]string{"Origin": "https://evil.example"}, http.StatusForbidden},
		{"old browser, Origin is this host", http.MethodPost, map[string]string{"Origin": "http://api.example"}, http.StatusOK},
		{"a trusted origin", http.MethodPost, map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://partner.example"}, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, "http://api.example/things", nil)
			for k, v := range tc.headers {
				r.Header.Set(k, v)
			}

			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)

			if w.Code != tc.want {
				t.Errorf("got %d, want %d", w.Code, tc.want)
			}
		})
	}

	if _, err := CrossOrigin("not a url"); err == nil {
		t.Error("a malformed trusted origin should be refused at construction")
	}
}
