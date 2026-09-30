// Command api serves the bookmark manager.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/alexvervloet/learn-go/capstones/bookmark-manager/internal/api"
	"github.com/alexvervloet/learn-go/capstones/bookmark-manager/internal/config"
	"github.com/alexvervloet/learn-go/capstones/bookmark-manager/internal/ratelimit"
	"github.com/alexvervloet/learn-go/capstones/bookmark-manager/internal/store"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

var (
	version = "unknown"
	commit  = "unknown"
)

func main() {
	// main does nothing but call run and exit, so every defer in run actually runs. os.Exit skips them.
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

	// NotifyContext gives a context cancelled on SIGINT or SIGTERM, which every other cancellation in the
	// program already understands. stop restores the default handler, so a SECOND signal kills the process
	// rather than being swallowed by a shutdown that is stuck.
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

	if err := redisClient.Ping(ctx).Err(); err != nil {
		// A hard failure, because the credential endpoints fail CLOSED when the limiter is unreachable. A
		// service that starts without its limiter is a service whose login endpoint returns 503 to everyone.
		return err
	}

	limiter, err := ratelimit.New(redisClient, ratelimit.Options{
		Limit:  cfg.LoginLimit,
		Window: cfg.LoginWindow,
		Prefix: "login",
	})
	if err != nil {
		return err
	}

	accountLimiter, err := ratelimit.New(redisClient, ratelimit.Options{
		Limit:  cfg.AccountLimit,
		Window: cfg.AccountWindow,
		Prefix: "account",
	})
	if err != nil {
		return err
	}

	st := store.New(pool)

	// Expired refresh tokens are rows nobody can use, and nothing else removes them. There is no worker in this
	// service, so the API sweeps hourly. Every replica does it, which is harmless because the DELETE is
	// idempotent. The WaitGroup is waited on before pool.Close runs (defers run last-in first-out), so a sweep in
	// progress is never left holding a closed pool.
	var sweeper sync.WaitGroup
	defer sweeper.Wait()

	sweeper.Go(func() { sweepExpiredTokens(ctx, st, log, time.Hour) })

	srv := api.New(api.Options{
		Store:          st,
		Limiter:        limiter,
		AccountLimiter: accountLimiter,
		Logger:         log,
		Secret:         cfg.JWTSecret,
		AccessTTL:      cfg.AccessTTL,
		RefreshTTL:     cfg.RefreshTTL,
	})

	httpServer := &http.Server{
		Addr:    cfg.Addr,
		Handler: srv.Routes(),

		// The timeouts a public server needs. Without them a client that sends one byte a minute holds a
		// goroutine and a file descriptor indefinitely, which is Slowloris and costs the attacker nothing.
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errc := make(chan error, 1)

	go func() {
		log.Info("listening", "addr", cfg.Addr, "version", version, "commit", commit)

		// ErrServerClosed is what Shutdown causes, so it is the success case.
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

	// A fresh context: ctx is already cancelled, and passing it to Shutdown would close every connection
	// immediately, which is the opposite of graceful.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		return err
	}

	log.Info("stopped")

	return nil
}

// sweepExpiredTokens deletes expired refresh tokens every interval until ctx ends.
func sweepExpiredTokens(ctx context.Context, st *store.Store, log *slog.Logger, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			deleted, err := st.DeleteExpiredTokens(ctx)
			if err != nil {
				log.ErrorContext(ctx, "sweep expired refresh tokens", "error", err)

				continue
			}

			log.InfoContext(ctx, "swept expired refresh tokens", "deleted", deleted)
		}
	}
}
