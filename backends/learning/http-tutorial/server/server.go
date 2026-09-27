// Package server covers the http.Server settings that matter and the shutdown that does not
// drop requests.
//
// # The zero value is not safe
//
// This is the most consequential default in net/http:
//
//	http.ListenAndServe(":8080", handler)
//
// That is the line in every tutorial, and it creates a server with NO TIMEOUTS. Not long ones:
// none. A client that opens a connection and sends one byte of a request header holds a
// goroutine and a file descriptor forever. A few thousand of those and the process is out of
// descriptors, having served nothing. That is Slowloris, it is twenty years old, and the Go
// default is still vulnerable to it.
//
// The Python comparison is worth making: uvicorn and gunicorn ship with timeouts on, so a
// FastAPI service is protected by its runtime and a Go service has to ask.
//
// # Which timeout does what
//
//	ReadHeaderTimeout  header must arrive within this. The Slowloris defence, and the one
//	                   to set if you set only one.
//	ReadTimeout        headers AND body. Too short breaks large uploads, so it is often
//	                   left off in favour of per-handler deadlines.
//	WriteTimeout       from the end of the headers to the end of the response. Too short
//	                   breaks slow clients and streaming.
//	IdleTimeout        how long a keep-alive connection may sit unused. Defaults to
//	                   ReadTimeout when unset, which means an unset ReadTimeout gives an
//	                   unbounded idle timeout too.
//
// ReadTimeout and WriteTimeout are absolute, not idle: they start when the request arrives and
// do not reset on progress. That makes them wrong for streaming and for large uploads, and the
// answer is per-handler deadlines through http.ResponseController.
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os/signal"
	"syscall"
	"time"
)

// Config holds the settings worth naming. The zero value is deliberately not useful; use
// Default.
type Config struct {
	Addr              string
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration

	// ShutdownGrace is how long in-flight requests get after a signal arrives.
	ShutdownGrace time.Duration

	// MaxHeaderBytes caps the request header. The default is 1 MB, which is generous for
	// something an attacker controls entirely.
	MaxHeaderBytes int
}

// Default returns settings that are safe rather than permissive.
//
// The numbers are defensible rather than tuned:
//
//	ReadHeaderTimeout 5s   no legitimate client takes longer to send headers
//	ReadTimeout       30s  enough for a body over a slow connection, short enough to
//	                       bound a stuck one. Endpoints accepting large uploads need a
//	                       per-handler deadline instead.
//	WriteTimeout      30s  and the same caveat for streaming endpoints
//	IdleTimeout       120s longer than a typical keep-alive so connections are reused,
//	                       shorter than a load balancer's own idle timeout
//	MaxHeaderBytes    1MB  the stdlib default, kept explicit so it is visible
func Default(addr string) Config {
	return Config{
		Addr:              addr,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
		ShutdownGrace:     15 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
}

// New builds an *http.Server from a Config.
//
// Returning the server rather than starting it is what makes it testable: a test can point it
// at a random port, and Run below takes it as an argument.
func New(cfg Config, handler http.Handler, log *slog.Logger) *http.Server {
	return &http.Server{
		Addr:              cfg.Addr,
		Handler:           handler,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
		MaxHeaderBytes:    cfg.MaxHeaderBytes,

		// The server's own error log, which is where net/http reports things no handler
		// sees: TLS handshake failures, "superfluous WriteHeader", malformed requests.
		// Without this they go to the standard logger and miss the structured output.
		ErrorLog: slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
}

// Run starts the server and blocks until a signal arrives, then shuts it down gracefully.
//
// Three things here are the whole point.
//
// ListenAndServe returns http.ErrServerClosed on a clean Shutdown, and treating that as a
// failure is the most common mistake in this function. It is a successful outcome.
//
// signal.NotifyContext is the modern form. The older idiom is a chan os.Signal and a select,
// and this is four lines shorter and composes with everything else that takes a context.
//
// Shutdown stops accepting, then waits for in-flight requests. It does NOT interrupt them, so
// a handler that ignores its context can outlive the grace period, and the deadline on the
// shutdown context is what stops Run hanging forever.
func Run(ctx context.Context, srv *http.Server, grace time.Duration, log *slog.Logger) error {
	// SIGINT is ctrl-C, SIGTERM is what a container runtime sends. Both should be graceful;
	// SIGKILL cannot be caught and is why the grace period has to be shorter than the
	// orchestrator's own.
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errs := make(chan error, 1)

	go func() {
		log.Info("server starting", "addr", srv.Addr)

		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errs <- fmt.Errorf("listen: %w", err)
			return
		}
		errs <- nil
	}()

	select {
	case err := <-errs:
		// The server stopped on its own, which means it failed to start.
		return err

	case <-ctx.Done():
		log.Info("shutdown signal received", "grace", grace)
	}

	// A FRESH context: ctx is already cancelled, and passing it to Shutdown would abort
	// immediately. This is the mistake that turns a graceful shutdown into an instant one,
	// and it looks correct.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), grace)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		// Shutdown returns the context's error when the grace period expires, which means
		// requests were still running. Close() then drops them.
		log.Error("graceful shutdown timed out, closing connections", "error", err)

		if closeErr := srv.Close(); closeErr != nil {
			// errors.Join rather than one formatted message, so both errors stay
			// matchable with errors.Is. Two %w verbs in one Errorf would work too;
			// Join reads better for unrelated failures.
			return errors.Join(
				fmt.Errorf("shutdown: %w", err),
				fmt.Errorf("close: %w", closeErr),
			)
		}
		return fmt.Errorf("shutdown: %w", err)
	}

	log.Info("server stopped cleanly")

	// Drain the goroutine so it cannot leak.
	return <-errs
}

// Listen creates a listener on a specific address, so a caller can learn the real port before
// the server starts.
//
// Needed for two things: binding port 0 in tests and reading back what the OS chose, and
// systemd-style socket activation where the listener is handed in. srv.ListenAndServe cannot do
// either, because it binds and serves in one call.
func Listen(addr string) (net.Listener, error) {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listening on %s: %w", addr, err)
	}
	return listener, nil
}

// Serve is Run against an existing listener.
func Serve(ctx context.Context, srv *http.Server, listener net.Listener, grace time.Duration, log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errs := make(chan error, 1)

	go func() {
		log.Info("server starting", "addr", listener.Addr().String())

		if err := srv.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errs <- fmt.Errorf("serve: %w", err)
			return
		}
		errs <- nil
	}()

	select {
	case err := <-errs:
		return err
	case <-ctx.Done():
		log.Info("shutdown signal received", "grace", grace)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), grace)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("graceful shutdown timed out, closing connections", "error", err)
		_ = srv.Close()
		return fmt.Errorf("shutdown: %w", err)
	}

	log.Info("server stopped cleanly")
	return <-errs
}

// ExtendDeadline pushes back the read and write deadlines for one request.
//
// This is the answer to "ReadTimeout and WriteTimeout are absolute". A streaming endpoint or a
// large upload sets its own deadline per chunk instead, so progress resets the clock and a
// stalled connection still dies.
//
// Before Go 1.20 this needed a type assertion to an unexported interface and most code simply
// turned the server-wide timeouts off instead, which is how services ended up unprotected.
func ExtendDeadline(w http.ResponseWriter, d time.Duration) error {
	controller := http.NewResponseController(w)

	deadline := time.Now().Add(d)

	if err := controller.SetWriteDeadline(deadline); err != nil {
		return fmt.Errorf("setting write deadline: %w", err)
	}
	if err := controller.SetReadDeadline(deadline); err != nil {
		return fmt.Errorf("setting read deadline: %w", err)
	}

	return nil
}
