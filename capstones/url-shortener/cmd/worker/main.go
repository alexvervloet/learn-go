// Command worker runs the background jobs: click recording and the expiry sweep.
//
// # Why a separate binary
//
// Two reasons, and the second is the real one.
//
// It scales separately: a spike in redirects needs more API replicas and the same number of workers, because
// the queue absorbs the difference. And it FAILS separately: a worker that crashes on a bad task does not take
// the redirects down with it.
//
// The cost is a second image, a second deployment, and a shared Redis that both now depend on.
package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/alexvervloet/learn-go/capstones/url-shortener/internal/config"
	"github.com/alexvervloet/learn-go/capstones/url-shortener/internal/store"
	"github.com/alexvervloet/learn-go/capstones/url-shortener/internal/tasks"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
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

	st := store.New(pool)

	server := asynq.NewServer(
		asynq.RedisClientOpt{Addr: cfg.RedisAddr},
		asynq.Config{
			// Concurrency is goroutines, and the ceiling that matters is the DATABASE pool rather than the
			// CPU: every click task does two writes. More workers than connections means workers waiting on
			// the pool, which looks like a slow queue and is a misconfiguration.
			Concurrency: 10,
			Queues:      tasks.Queues,

			// ShutdownTimeout bounds how long a graceful stop waits for in-flight tasks. Anything still
			// running when it expires is requeued, which is safe here because the handlers tolerate a repeat.
			ShutdownTimeout: 20 * time.Second,

			Logger: asynqLogger{log},
		},
	)

	// The scheduler that enqueues the nightly sweep.
	//
	// It is a separate component from the server, and running it in more than one replica is how a job runs
	// twice. asynq.Unique on the task is the guard: a second enqueue inside the window is refused.
	scheduler := asynq.NewScheduler(
		asynq.RedisClientOpt{Addr: cfg.RedisAddr},
		&asynq.SchedulerOpts{
			// UTC, explicitly. A scheduler using the machine's local time moves by an hour twice a year and
			// there is nothing in the code to explain it.
			Location: time.UTC,
			Logger:   asynqLogger{log},
		},
	)

	if _, err := scheduler.Register("17 3 * * *", tasks.NewSweepExpired()); err != nil {
		return err
	}

	mux := tasks.Mux(st, st, func(err error) bool {
		// "This task can never succeed": the URL is gone. RecordClick checks for the row before it inserts
		// the click, so a deleted URL arrives here as ErrNotFound and not as a foreign-key error.
		return errors.Is(err, store.ErrNotFound)
	}, func(msg string, args ...any) { log.Info(msg, args...) })

	if err := scheduler.Start(); err != nil {
		return err
	}

	// Start, not Run. Run installs its own SIGTERM handler and calls Shutdown when the signal arrives, and so
	// does this function, through ctx. asynq's Shutdown returns at once for a server that is already shutting
	// down, so whichever call came second returned early. If that was this one, run returned and the deferred
	// pool.Close ran while in-flight tasks were still draining and writing to the database. With Start there is
	// one signal handler, ctx, and one Shutdown, the one below, which waits.
	if err := server.Start(mux); err != nil {
		scheduler.Shutdown()

		return err
	}

	log.Info("worker started", "queues", tasks.Queues, "concurrency", 10)

	<-ctx.Done()
	log.Info("shutting down")

	// Shutdown, not Stop.
	//
	// Stop halts the processing of NEW tasks and leaves the server running, which is what you want before a
	// deploy. Shutdown waits for the in-flight ones and then returns. Calling only Stop leaves the process
	// alive and the orchestrator eventually kills it.
	server.Shutdown()
	scheduler.Shutdown()

	log.Info("stopped")

	return nil
}

// asynqLogger adapts slog to asynq's logging interface.
//
// asynq wants Debug/Info/Warn/Error/Fatal with variadic any, which predates slog. Without an adapter it writes
// to the standard logger and the output is half JSON and half not, which breaks every log aggregator.
type asynqLogger struct{ log *slog.Logger }

func (a asynqLogger) Debug(args ...any) { a.log.Debug("asynq", "msg", args) }
func (a asynqLogger) Info(args ...any)  { a.log.Info("asynq", "msg", args) }
func (a asynqLogger) Warn(args ...any)  { a.log.Warn("asynq", "msg", args) }
func (a asynqLogger) Error(args ...any) { a.log.Error("asynq", "msg", args) }
func (a asynqLogger) Fatal(args ...any) { a.log.Error("asynq fatal", "msg", args) }
