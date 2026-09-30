// Command api serves the JSON API and the redirects.
//
// # What this file is for
//
// Wiring, and nothing else. Every decision worth explaining is in a package with a test next to it; main's job
// is to read the configuration, open the connections, start the server, and shut it down properly. A main that
// contains logic is a main whose logic has no test.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/alexvervloet/learn-go/capstones/url-shortener/internal/api"
	"github.com/alexvervloet/learn-go/capstones/url-shortener/internal/cache"
	"github.com/alexvervloet/learn-go/capstones/url-shortener/internal/config"
	"github.com/alexvervloet/learn-go/capstones/url-shortener/internal/store"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// version is set at build time with -ldflags "-X main.version=...".
//
// "unknown" rather than "", because -X against a variable that does not exist is NOT an error: the build
// succeeds and the variable keeps its default. A default of "unknown" makes a typo in the flag visible in the
// "listening" log line at startup; a default of "" looks like a field nobody filled in.
var (
	version = "unknown"
	commit  = "unknown"
)

func main() {
	// main does nothing but call run and exit, so every defer in run actually runs.
	//
	// os.Exit does not run deferred functions. A main with both a defer and an os.Exit silently skips the
	// defer, which is how a connection pool stops being closed and a profile stops being written.
	if err := run(); err != nil {
		slog.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run() error {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	// NotifyContext, not a signal channel.
	//
	// It gives a context that is cancelled on SIGINT or SIGTERM, which is the shape every other cancellation
	// in the program already understands. The stop function restores the default handler, so a SECOND signal
	// kills the process immediately rather than being swallowed by a shutdown that is stuck.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}

	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		return err
	}

	redisClient := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr})
	defer func() { _ = redisClient.Close() }()

	enqueuer := asynq.NewClient(asynq.RedisClientOpt{Addr: cfg.RedisAddr})
	defer func() { _ = enqueuer.Close() }()

	srv := api.New(api.Options{
		Store:    store.New(pool),
		Cache:    cache.New(redisClient, cache.Options{TTL: cfg.CacheTTL}),
		Enqueuer: enqueuer,
		Logger:   log,
		Secret:   cfg.JWTSecret,
		TokenTTL: cfg.TokenTTL,
		BaseURL:  cfg.BaseURL,
	})

	httpServer := &http.Server{
		Addr:    cfg.Addr,
		Handler: srv.Routes(),

		// The three timeouts a public server needs. Without them, a client that opens a connection and sends
		// one byte a minute holds a goroutine and a file descriptor indefinitely, which is Slowloris and
		// costs the attacker nothing.
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errc := make(chan error, 1)

	go func() {
		log.Info("listening", "addr", cfg.Addr, "version", version, "commit", commit)

		// ErrServerClosed is what Shutdown causes, so it is the SUCCESS case here and not an error.
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}

		close(errc)
	}()

	select {
	case err := <-errc:
		return err

	case <-ctx.Done():
		log.Info("shutting down")
	}

	// A fresh context with its own deadline.
	//
	// ctx is already cancelled, so passing it to Shutdown would close every connection immediately, which is
	// precisely what a graceful shutdown is not. 20 seconds is longer than WriteTimeout, so a request that is
	// allowed to be in flight has time to finish.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		return err
	}

	log.Info("stopped")

	return nil
}
