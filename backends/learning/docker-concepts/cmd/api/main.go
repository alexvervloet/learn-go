// Command api is the service the Dockerfiles build.
//
// It is deliberately small and deliberately not trivial: it serves HTTP, reads its configuration from the
// environment, connects to nothing, and reports the build information the linker injected. That last part is what
// makes several of the image comparisons measurable from inside the container.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"strconv"
	"syscall"
	"time"
)

// Build information, injected at link time with -ldflags.
//
// # Why these are vars and not consts
//
// -X only works on a string VARIABLE in the main package (or any package, named by its full path). A const cannot
// be patched, because the compiler has already folded it into every use.
//
// The defaults matter: a binary built without the flags reports "unknown" rather than an empty string, so a
// container whose image was built by the wrong pipeline says so instead of looking like a field nobody filled in.
var (
	version   = "unknown"
	commit    = "unknown"
	buildTime = "unknown"
)

func main() {
	if err := run(); err != nil {
		// A plain Fprintln and an exit code, not log.Fatal. log.Fatal writes to the standard logger
		// with a timestamp prefix, which in a container whose logs are already timestamped by the
		// runtime is a second timestamp on every line.
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

func run() error {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	addr := ":" + env("PORT", "8080")

	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})

	// /buildinfo is what the image tests read. It reports what the linker injected AND what the Go
	// runtime knows about itself, which is how a test can tell a CGO-enabled binary from a static one
	// from inside the container.
	mux.HandleFunc("GET /buildinfo", func(w http.ResponseWriter, _ *http.Request) {
		info := map[string]any{
			"version":    version,
			"commit":     commit,
			"build_time": buildTime,
			"go_version": runtime.Version(),
			"goos":       runtime.GOOS,
			"goarch":     runtime.GOARCH,
			"cgo":        cgoEnabled(),
			"uid":        os.Getuid(),
			"gid":        os.Getgid(),
			"hostname":   hostname(),
		}

		// The module's own build settings, which include whether CGO was on and what flags were
		// used. debug.ReadBuildInfo is how a binary reports how it was built, and it works for any
		// Go binary: `go version -m ./binary` reads the same data from outside.
		if bi, ok := debug.ReadBuildInfo(); ok {
			settings := map[string]string{}

			for _, s := range bi.Settings {
				switch s.Key {
				case "CGO_ENABLED", "GOOS", "GOARCH", "vcs.revision", "vcs.modified", "-ldflags":
					settings[s.Key] = s.Value
				}
			}

			info["build_settings"] = settings
			info["main_module"] = bi.Main.Path
		}

		w.Header().Set("Content-Type", "application/json")

		if err := json.NewEncoder(w).Encode(info); err != nil {
			log.Error("encoding the build info", "error", err)
		}
	})

	// /dns resolves a name, which is the test for whether a scratch image can do it. A static binary
	// with the pure-Go resolver can; one built with CGO against glibc needs libnss and fails in an
	// image that does not have it, with an error that says "no such host" for a name that exists.
	mux.HandleFunc("GET /dns", func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Query().Get("name")
		if name == "" {
			name = "localhost"
		}

		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()

		addrs, err := net.DefaultResolver.LookupHost(ctx, name)

		w.Header().Set("Content-Type", "application/json")

		result := map[string]any{"name": name}

		if err != nil {
			result["error"] = err.Error()
		} else {
			result["addresses"] = addrs
		}

		_ = json.NewEncoder(w).Encode(result)
	})

	// /tls makes an outbound HTTPS request, which is the test for whether the image has CA
	// certificates. Without /etc/ssl/certs a scratch image cannot verify any certificate, and the
	// error is "x509: certificate signed by unknown authority" for every host, which reads like a
	// server problem.
	mux.HandleFunc("GET /tls", func(w http.ResponseWriter, r *http.Request) {
		target := r.URL.Query().Get("url")
		if target == "" {
			target = "https://example.com"
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		req, err := http.NewRequestWithContext(ctx, "GET", target, nil)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		result := map[string]any{"url": target}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			result["error"] = err.Error()
		} else {
			result["status"] = resp.StatusCode
			_ = resp.Body.Close()
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
	})

	// /tz reports the local time zone, which is the test for whether the image has the zoneinfo
	// database. A scratch image without it silently reports UTC for every zone, so a service
	// formatting a user's local time is wrong and nothing errors.
	mux.HandleFunc("GET /tz", func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Query().Get("name")
		if name == "" {
			name = "Europe/London"
		}

		result := map[string]any{"requested": name}

		loc, err := time.LoadLocation(name)
		if err != nil {
			result["error"] = err.Error()
		} else {
			now := time.Date(2025, 7, 1, 12, 0, 0, 0, time.UTC).In(loc)

			result["zone"] = loc.String()
			result["formatted"] = now.Format(time.RFC3339)

			zone, offset := now.Zone()
			result["abbreviation"] = zone
			result["offset_seconds"] = offset
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
	})

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// SIGTERM, which is what a container runtime sends. SIGINT for a local run.
	//
	// This is the part a Dockerfile can break: with a shell-form CMD the process is a child of
	// /bin/sh, the shell is PID 1, and it does not forward signals. So the container is killed after
	// the grace period instead of stopping, in-flight requests are dropped, and the symptom is
	// "our deploys drop connections".
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errs := make(chan error, 1)

	go func() {
		log.Info("listening", "addr", addr, "version", version, "pid", os.Getpid())

		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errs <- err
			return
		}

		errs <- nil
	}()

	select {
	case err := <-errs:
		return err
	case <-ctx.Done():
		log.Info("shutting down", "signal", "received")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}

	log.Info("stopped cleanly")

	return <-errs
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func hostname() string {
	h, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return h
}

// cgoEnabled reports whether this binary was built with cgo.
//
// Read from the build info rather than from a build tag, because a build tag reports what the SOURCE asked for
// and this reports what the BUILD did. A binary built with CGO_ENABLED=1 on a machine with no C compiler silently
// falls back, and only the build info knows.
func cgoEnabled() bool {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return false
	}

	for _, s := range bi.Settings {
		if s.Key == "CGO_ENABLED" {
			on, err := strconv.ParseBool(s.Value)
			return err == nil && on
		}
	}

	return false
}
