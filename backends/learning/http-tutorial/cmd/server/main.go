// Command server wires every package in this module into one runnable service.
//
//	go run ./cmd/server
//	curl -i localhost:8080/health
//	curl -s localhost:8080/items/ | jq
//	curl -s -XPOST localhost:8080/items/ -d '{"name":"chisel"}' | jq
//	curl -s localhost:8080/stream          # one JSON object per second
//	curl -i localhost:8080/items/nope      # a plain-text 400: the id is not a number
//	curl -i -H 'Accept: text/plain' localhost:8080/negotiated/1   # content negotiation
//
// Then ctrl-C and watch the graceful shutdown.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/alexvervloet/learn-go/backends/learning/http-tutorial/middleware"
	"github.com/alexvervloet/learn-go/backends/learning/http-tutorial/request"
	"github.com/alexvervloet/learn-go/backends/learning/http-tutorial/response"
	"github.com/alexvervloet/learn-go/backends/learning/http-tutorial/routing"
	"github.com/alexvervloet/learn-go/backends/learning/http-tutorial/server"
)

func main() {
	// run rather than doing the work in main, so every error path is a return and there is
	// exactly one os.Exit in the program. A deferred Close in main is skipped by os.Exit,
	// which is the bug this shape avoids.
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8080"
	}

	store := routing.NewStore()

	mux := routing.NewMux(store)
	mux.Handle("GET /stream", streamHandler(log))
	mux.Handle("GET /negotiated/{id}", negotiatedHandler(store, log))
	mux.Handle("POST /validated", validatedHandler(log))
	mux.Handle("GET /panic", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("deliberate, to show the recovery middleware")
	}))

	// Proxy headers are trusted only when a deployment says it sits behind a proxy
	// that sets them. This server is usually run directly, where any client could
	// write its own X-Forwarded-For.
	trustProxy := os.Getenv("TRUST_PROXY_HEADERS") == "true"

	handler := middleware.Production(log, trustProxy)(mux)

	cfg := server.Default(addr)
	srv := server.New(cfg, handler, log)

	return server.Run(context.Background(), srv, cfg.ShutdownGrace, log)
}

// streamHandler sends one JSON object per second for five seconds, so the flushing is visible
// with curl rather than just asserted in a test.
func streamHandler(log *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		type tick struct {
			N  int       `json:"n"`
			At time.Time `json:"at"`
		}

		values := make(chan tick)

		go func() {
			defer close(values)

			for i := 1; i <= 5; i++ {
				select {
				case values <- tick{N: i, At: time.Now()}:
				case <-r.Context().Done():
					// The client went away, so stop producing. Without this the
					// goroutine blocks on the send forever.
					return
				}

				select {
				case <-time.After(time.Second):
				case <-r.Context().Done():
					return
				}
			}
		}()

		if err := response.StreamJSONLines(w, values); err != nil {
			log.Error("stream failed", "error", err)
		}
	})
}

// negotiatedHandler answers in whichever type the client asked for, or 406 if it asked for
// something impossible.
func negotiatedHandler(store *routing.Store, log *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(r.PathValue("id"))
		if err != nil {
			response.WriteError(w, log, http.StatusBadRequest, "bad id", "id must be a number")
			return
		}

		item, ok := store.Get(id)
		if !ok {
			response.WriteError(w, log, http.StatusNotFound, "not found",
				fmt.Sprintf("no item with id %d", id))
			return
		}

		switch response.Negotiate(r, "application/json", "text/plain") {
		case "application/json":
			response.WriteJSONWithETag(w, r, log, http.StatusOK, item)

		case "text/plain":
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprintf(w, "%d\t%s\n", item.ID, item.Name)

		default:
			response.WriteError(w, log, http.StatusNotAcceptable, "not acceptable",
				"this endpoint serves application/json or text/plain")
		}
	})
}

// validatedHandler shows the whole request-decoding path: size limit, strict JSON, per-field
// validation, and a 422 with the detail FastAPI gives for free.
func validatedHandler(log *slog.Logger) http.Handler {
	type payload struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
		Email string `json:"email"`
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body payload

		if err := request.DecodeJSON(w, r, 1<<16, &body); err != nil {
			response.WriteError(w, log, request.StatusFor(err), "bad request", err.Error())
			return
		}

		// The thirty lines pydantic would have written. Collected rather than returned on
		// the first failure, so the client learns about every problem at once.
		var problems []response.FieldError

		if body.Name == "" {
			problems = append(problems, response.FieldError{
				Field: "name", Detail: "is required",
			})
		} else if len(body.Name) > 50 {
			problems = append(problems, response.FieldError{
				Field: "name", Detail: "must be 50 characters or fewer",
			})
		}

		if body.Count < 1 {
			problems = append(problems, response.FieldError{
				Field: "count", Detail: "must be at least 1",
			})
		}

		if body.Email != "" && !strings.Contains(body.Email, "@") {
			problems = append(problems, response.FieldError{
				Field: "email", Detail: "must contain @",
			})
		}

		if len(problems) > 0 {
			response.WriteValidationError(w, log, problems)
			return
		}

		response.Created(w, log, "/validated/1", body)
	})
}
