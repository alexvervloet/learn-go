package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func discard() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestDefaultSetsEveryTimeout(t *testing.T) {
	cfg := Default(":8080")

	// The point of the test is that NONE of these is zero, because a zero timeout means no
	// timeout and that is the vulnerability this package is about.
	timeouts := map[string]time.Duration{
		"ReadHeaderTimeout": cfg.ReadHeaderTimeout,
		"ReadTimeout":       cfg.ReadTimeout,
		"WriteTimeout":      cfg.WriteTimeout,
		"IdleTimeout":       cfg.IdleTimeout,
		"ShutdownGrace":     cfg.ShutdownGrace,
	}
	for name, d := range timeouts {
		if d <= 0 {
			t.Errorf("%s = %v, want a positive duration", name, d)
		}
	}

	if cfg.MaxHeaderBytes <= 0 {
		t.Error("MaxHeaderBytes is not set")
	}

	srv := New(cfg, http.NotFoundHandler(), discard())

	if srv.ReadHeaderTimeout != cfg.ReadHeaderTimeout {
		t.Error("New did not carry ReadHeaderTimeout through")
	}
	if srv.ErrorLog == nil {
		t.Error("ErrorLog is nil, so net/http's own errors go to the default logger")
	}
}

// TestSlowlorisNeedsReadHeaderTimeout is the demonstration that justifies the whole package.
//
// A connection that sends a partial request header and then nothing holds a goroutine and a file
// descriptor. With ReadHeaderTimeout set, the server closes it. Without, it is held for as long
// as the attacker keeps the socket open, which is the twenty-year-old Slowloris attack and which
// http.ListenAndServe is still vulnerable to by default.
func TestSlowlorisNeedsReadHeaderTimeout(t *testing.T) {
	// A partial header: no blank line, so the request is never complete.
	const partial = "GET / HTTP/1.1\r\nHost: localhost\r\nX-Slow: "

	start := func(t *testing.T, readHeaderTimeout time.Duration) (net.Conn, func()) {
		t.Helper()

		listener, err := Listen("127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}

		srv := &http.Server{
			Handler:           http.NotFoundHandler(),
			ReadHeaderTimeout: readHeaderTimeout,
			ErrorLog:          slog.NewLogLogger(discard().Handler(), slog.LevelWarn),
		}

		go func() { _ = srv.Serve(listener) }()

		conn, err := net.Dial("tcp", listener.Addr().String())
		if err != nil {
			t.Fatal(err)
		}

		if _, err := conn.Write([]byte(partial)); err != nil {
			t.Fatal(err)
		}

		return conn, func() {
			_ = conn.Close()
			_ = srv.Close()
		}
	}

	t.Run("with a header timeout the server hangs up", func(t *testing.T) {
		conn, cleanup := start(t, 100*time.Millisecond)
		defer cleanup()

		// Reading blocks until the server closes, which it should do after the timeout.
		if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
			t.Fatal(err)
		}

		buf := make([]byte, 1024)
		n, err := conn.Read(buf)

		switch {
		case err == nil && n > 0:
			// Some versions send 408 Request Timeout before closing, which is even
			// better than a bare close.
			if !strings.Contains(string(buf[:n]), "408") {
				t.Logf("server responded with %q", strings.TrimSpace(string(buf[:n])))
			}
		case errors.Is(err, io.EOF):
			// The server closed the connection, which is the expected outcome.
		default:
			t.Fatalf("read returned %v after %d bytes; the server did not hang up",
				err, n)
		}
	})

	t.Run("without one the connection is held open", func(t *testing.T) {
		conn, cleanup := start(t, 0) // zero means NO timeout, which is the default
		defer cleanup()

		// The server should still be holding it after a wait that comfortably exceeds
		// the timeout used above.
		if err := conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond)); err != nil {
			t.Fatal(err)
		}

		buf := make([]byte, 1024)
		_, err := conn.Read(buf)

		var netErr net.Error
		if !errors.As(err, &netErr) || !netErr.Timeout() {
			t.Errorf("read returned %v; expected OUR deadline to fire, meaning the "+
				"server was still holding the connection", err)
		}

		t.Log("with ReadHeaderTimeout unset the server held a half-sent request open " +
			"indefinitely. That is Slowloris, and http.ListenAndServe has no timeouts.")
	})
}

// TestGracefulShutdownWaitsForInFlightRequests is the behaviour Shutdown is for.
func TestGracefulShutdownWaitsForInFlightRequests(t *testing.T) {
	var completed atomic.Bool

	started := make(chan struct{})

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		time.Sleep(200 * time.Millisecond)
		completed.Store(true)
		_, _ = w.Write([]byte("done"))
	})

	listener, err := Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	srv := &http.Server{Handler: handler}
	go func() { _ = srv.Serve(listener) }()

	// Fire a request and wait for the handler to start.
	responses := make(chan string, 1)
	go func() {
		resp, err := http.Get("http://" + listener.Addr().String())
		if err != nil {
			responses <- "error: " + err.Error()
			return
		}
		defer func() { _ = resp.Body.Close() }()

		body, _ := io.ReadAll(resp.Body)
		responses <- string(body)
	}()

	<-started

	// Shut down while the handler is still running.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	shutdownStart := time.Now()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	elapsed := time.Since(shutdownStart)

	if !completed.Load() {
		t.Error("Shutdown returned before the in-flight handler finished")
	}
	if elapsed < 100*time.Millisecond {
		t.Errorf("Shutdown took %v; it should have waited for the handler", elapsed)
	}

	if got := <-responses; got != "done" {
		t.Errorf("the client got %q, want the full response", got)
	}
}

// TestShutdownWithACancelledContextIsInstant is the mistake that turns a graceful shutdown into
// an abrupt one, and it looks correct: the signal context is already cancelled, so passing it to
// Shutdown means the deadline has already passed.
func TestShutdownWithACancelledContextIsInstant(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		_, _ = w.Write([]byte("done"))
	})

	listener, err := Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	srv := &http.Server{Handler: handler}
	go func() { _ = srv.Serve(listener) }()

	go func() {
		resp, err := http.Get("http://" + listener.Addr().String())
		if err == nil {
			_ = resp.Body.Close()
		}
	}()

	<-started

	// The bug: reusing the already-cancelled signal context.
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	err = srv.Shutdown(cancelled)

	if !errors.Is(err, context.Canceled) {
		t.Errorf("Shutdown returned %v, want context.Canceled", err)
	}

	close(release)
	_ = srv.Close()

	t.Log("Shutdown with an already-cancelled context returns immediately and abandons " +
		"in-flight requests. Run() uses context.Background() for the shutdown context, " +
		"which is the only reason it works.")
}

// TestErrServerClosedIsNotAFailure: treating it as one is the most common mistake in a Run
// function, and it makes every clean shutdown look like a crash in the logs.
func TestErrServerClosedIsNotAFailure(t *testing.T) {
	listener, err := Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	srv := &http.Server{Handler: http.NotFoundHandler()}

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(listener) }()

	time.Sleep(20 * time.Millisecond)

	if err := srv.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}

	got := <-serveErr
	if !errors.Is(got, http.ErrServerClosed) {
		t.Errorf("Serve returned %v, want http.ErrServerClosed", got)
	}

	t.Log("Serve returns http.ErrServerClosed on a clean shutdown; it is a success, not a failure")
}

// TestServeReturnsOnSignal exercises Serve's whole loop by sending the process a signal.
func TestServeReturnsOnSignal(t *testing.T) {
	listener, err := Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	srv := &http.Server{Handler: http.NotFoundHandler()}

	// A context cancelled from the outside stands in for the signal, so the test does not
	// have to send itself a SIGTERM and interfere with the test binary.
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- Serve(ctx, srv, listener, time.Second, discard()) }()

	// The server is up and serving.
	resp, err := http.Get("http://" + listener.Addr().String())
	if err != nil {
		t.Fatalf("server did not come up: %v", err)
	}
	_ = resp.Body.Close()

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Serve returned %v, want nil after a clean shutdown", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after the context was cancelled")
	}
}

// TestServeReportsABindFailure: the server stopping on its own means it failed to start, and Run
// has to report that rather than blocking forever.
func TestServeReportsABindFailure(t *testing.T) {
	// Take a port, then try to serve on it with a second listener.
	first, err := Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Close() }()

	addr := first.Addr().String()

	if _, err := Listen(addr); err == nil {
		t.Skip("this platform allows two listeners on one port")
	} else if !strings.Contains(err.Error(), addr) {
		t.Errorf("the error does not name the address: %v", err)
	}
}

func TestRunReportsABindFailure(t *testing.T) {
	// Port 1 needs root, so binding it fails as an ordinary user.
	srv := &http.Server{Addr: "127.0.0.1:1", Handler: http.NotFoundHandler()}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	err := Run(ctx, srv, time.Second, discard())

	if err == nil {
		t.Skip("binding port 1 succeeded, so this test is running as root")
	}
	if !strings.Contains(err.Error(), "listen") {
		t.Errorf("err = %v, want it to mention listening", err)
	}
}

func TestExtendDeadline(t *testing.T) {
	// A real server, because httptest.ResponseRecorder has no deadline support and
	// ResponseController reports ErrNotSupported for it.
	var handlerErr error

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		handlerErr = ExtendDeadline(w, 10*time.Second)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if handlerErr != nil {
		t.Errorf("ExtendDeadline: %v", handlerErr)
	}
}

// TestExtendDeadlineOnARecorder documents the failure mode, since a unit test that passes a
// recorder will hit it.
func TestExtendDeadlineOnARecorder(t *testing.T) {
	err := ExtendDeadline(httptest.NewRecorder(), time.Second)

	if err == nil {
		t.Skip("httptest.ResponseRecorder now supports deadlines")
	}
	if !errors.Is(err, http.ErrNotSupported) {
		t.Errorf("err = %v, want http.ErrNotSupported", err)
	}

	t.Log("httptest.ResponseRecorder cannot set deadlines, so a handler that calls " +
		"ExtendDeadline needs httptest.NewServer rather than a recorder")
}

// TestAbsoluteTimeoutsBreakStreaming is the reason ExtendDeadline exists. WriteTimeout starts
// when the request arrives and does not reset on progress, so a stream longer than the timeout is
// cut off mid-response even though it was never stalled.
func TestAbsoluteTimeoutsBreakStreaming(t *testing.T) {
	const chunks = 6
	const chunkDelay = 40 * time.Millisecond

	stream := func(w http.ResponseWriter, extend bool) {
		controller := http.NewResponseController(w)

		for i := range chunks {
			if extend {
				// Each chunk pushes the deadline back, so progress resets the clock.
				_ = controller.SetWriteDeadline(time.Now().Add(time.Second))
			}

			_, _ = fmt.Fprintf(w, "chunk %d\n", i)
			_ = controller.Flush()

			time.Sleep(chunkDelay)
		}
	}

	run := func(t *testing.T, extend bool) (string, error) {
		t.Helper()

		listener, err := Listen("127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}

		srv := &http.Server{
			// Shorter than the whole stream, longer than one chunk.
			WriteTimeout: 100 * time.Millisecond,
			Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				stream(w, extend)
			}),
			ErrorLog: slog.NewLogLogger(discard().Handler(), slog.LevelWarn),
		}
		defer func() { _ = srv.Close() }()

		go func() { _ = srv.Serve(listener) }()

		resp, err := http.Get("http://" + listener.Addr().String())
		if err != nil {
			return "", err
		}
		defer func() { _ = resp.Body.Close() }()

		body, err := io.ReadAll(resp.Body)
		return string(body), err
	}

	t.Run("without extending, the stream is cut off", func(t *testing.T) {
		body, err := run(t, false)

		lines := strings.Count(strings.TrimSpace(body), "\n") + 1
		if strings.TrimSpace(body) == "" {
			lines = 0
		}

		t.Logf("got %d of %d chunks, err = %v", lines, chunks, err)

		if lines >= chunks && err == nil {
			t.Error("the whole stream arrived; WriteTimeout should have cut it off")
		}
	})

	t.Run("extending per chunk keeps it alive", func(t *testing.T) {
		body, err := run(t, true)
		if err != nil {
			t.Fatalf("the stream failed: %v", err)
		}

		lines := strings.Count(strings.TrimSpace(body), "\n") + 1
		if lines != chunks {
			t.Errorf("got %d of %d chunks: %q", lines, chunks, body)
		}
	})
}
