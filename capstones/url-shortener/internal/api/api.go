// Package api is the HTTP layer: routing, middleware, handlers.
//
// # Two surfaces in one service
//
// A JSON API under /api, and a redirect at the root. They have different callers, different error formats and
// different performance profiles, and they share a router because a short link has to be short and
// short.example.com/api/v1/urls/abc is not.
//
// The root route is the reason shortener.Reserved exists: a slug of "api" would shadow the API and Go's
// ServeMux would resolve it in favour of the more specific pattern, silently.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/alexvervloet/learn-go/capstones/url-shortener/internal/auth"
	"github.com/alexvervloet/learn-go/capstones/url-shortener/internal/cache"
	"github.com/alexvervloet/learn-go/capstones/url-shortener/internal/shortener"
	"github.com/alexvervloet/learn-go/capstones/url-shortener/internal/store"
	"github.com/alexvervloet/learn-go/capstones/url-shortener/internal/tasks"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/hibiken/asynq"
)

// Enqueuer is what the API needs from asynq.
//
// An interface, so a test runs without Redis and asserts on what WOULD have been enqueued. That is the right
// seam: the click is enqueued by the handler and performed by the worker, so the handler's contract is the
// enqueue.
type Enqueuer interface {
	EnqueueContext(ctx context.Context, task *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error)
}

// Server holds the dependencies.
type Server struct {
	store    *store.Store
	cache    *cache.Cache
	enqueuer Enqueuer
	log      *slog.Logger

	secret   []byte
	tokenTTL time.Duration
	baseURL  string

	// hashCost is bcrypt's work factor, injected so a test can use the minimum. Hard-coding auth.Cost here
	// would make every test that registers a user a quarter of a second slower, and the pressure to "fix"
	// that lands on the production constant.
	hashCost int
}

// Options configures a Server.
type Options struct {
	Store    *store.Store
	Cache    *cache.Cache
	Enqueuer Enqueuer
	Logger   *slog.Logger

	Secret   []byte
	TokenTTL time.Duration
	BaseURL  string
	HashCost int
}

// New builds a Server.
func New(opts Options) *Server {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}

	if opts.HashCost == 0 {
		opts.HashCost = auth.Cost
	}

	if opts.TokenTTL == 0 {
		opts.TokenTTL = time.Hour
	}

	return &Server{
		store:    opts.Store,
		cache:    opts.Cache,
		enqueuer: opts.Enqueuer,
		log:      opts.Logger,
		secret:   opts.Secret,
		tokenTTL: opts.TokenTTL,
		baseURL:  strings.TrimSuffix(opts.BaseURL, "/"),
		hashCost: opts.HashCost,
	}
}

// Routes returns the handler.
//
// # Go 1.22 patterns, not a router library
//
// "POST /api/v1/urls" and "GET /{slug}" are stdlib. The method is part of the pattern, wildcards bind with
// r.PathValue, and the precedence rule is "the most specific pattern wins" rather than "the first one
// registered", so /api/v1/urls beats /{slug} regardless of order.
//
// chi is here for its middleware only. That is a deliberate split: the routing is stdlib because it now does
// the job, and the middleware is chi's because writing RequestID, RealIP and Recoverer again is not learning
// anything.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", s.handleHealth)

	mux.HandleFunc("POST /api/v1/register", s.handleRegister)
	mux.HandleFunc("POST /api/v1/login", s.handleLogin)

	mux.Handle("POST /api/v1/urls", s.authenticated(s.handleCreateURL))
	mux.Handle("GET /api/v1/urls", s.authenticated(s.handleListURLs))
	mux.Handle("DELETE /api/v1/urls/{slug}", s.authenticated(s.handleDeleteURL))
	mux.Handle("GET /api/v1/urls/{slug}/stats", s.authenticated(s.handleStats))

	// The catch-all, last in the file and last in precedence. It is the redirect.
	mux.HandleFunc("GET /{slug}", s.handleRedirect)

	// Middleware wraps outside in, so the recoverer is outermost and catches a panic from anything below it,
	// including the logger.
	var h http.Handler = mux
	h = s.logRequests(h)
	h = middleware.Recoverer(h)
	h = middleware.RealIP(h)
	h = middleware.RequestID(h)

	return h
}

// ---------------------------------------------------------------------------
// Errors
// ---------------------------------------------------------------------------

// Problem is RFC 9457, which is the one standard for an HTTP error body.
//
// # Why a standard shape matters here
//
// A client has to distinguish "your token expired" from "that slug is taken" from "the database is down", and a
// bare string does not let it. A type URI does, and it is stable in a way prose is not.
type Problem struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// Problem type URIs.
const (
	TypeValidation     = "https://example.com/probs/validation"
	TypeUnauthorized   = "https://example.com/probs/unauthorized"
	TypeNotFound       = "https://example.com/probs/not-found"
	TypeGone           = "https://example.com/probs/gone"
	TypeConflict       = "https://example.com/probs/conflict"
	TypeInternal       = "https://example.com/probs/internal"
	TypeMethodNotAllow = "https://example.com/probs/method-not-allowed"
)

func (s *Server) problem(w http.ResponseWriter, r *http.Request, status int, typeURI, title, detail string) {
	// application/problem+json, not application/json. A proxy or a client library can then tell an error body
	// from a success body without parsing it.
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(Problem{
		Type:   typeURI,
		Title:  title,
		Status: status,
		Detail: detail,
	}); err != nil {
		s.log.ErrorContext(r.Context(), "write problem", "error", err)
	}
}

// internalError logs the real error and tells the client nothing.
//
// The detail of a database error is a map of the schema. The client gets a request id, which is what a support
// conversation needs, and the log gets the rest.
func (s *Server) internalError(w http.ResponseWriter, r *http.Request, msg string, err error) {
	id := middleware.GetReqID(r.Context())

	s.log.ErrorContext(r.Context(), msg, "error", err, "request_id", id)
	s.problem(w, r, http.StatusInternalServerError, TypeInternal, "Internal error",
		"Something went wrong. Quote request id "+id+" if you report this.")
}

func (s *Server) writeJSON(w http.ResponseWriter, r *http.Request, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(body); err != nil {
		// The status line is already sent, so there is nothing to tell the client. Log it and move on.
		s.log.ErrorContext(r.Context(), "write response", "error", err)
	}
}

// decode reads a JSON body with the limits a public endpoint needs.
func decode[T any](w http.ResponseWriter, r *http.Request) (T, error) {
	var v T

	// A megabyte. Without a limit, a request body is read until the client stops sending, and a slow infinite
	// body holds a goroutine and memory for as long as the attacker likes.
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

	dec := json.NewDecoder(r.Body)

	// An unknown field is an error, so a client sending {"slugg": "x"} is told rather than silently getting a
	// generated slug. This catches a typo in a client at the first request instead of in production.
	dec.DisallowUnknownFields()

	if err := dec.Decode(&v); err != nil {
		return v, err
	}

	// A second Decode must hit EOF. Without this, {"a":1}{"b":2} decodes the first object and ignores the
	// rest, which is a request smuggling shape rather than a nicety.
	if dec.More() {
		return v, errors.New("body has more than one JSON value")
	}

	return v, nil
}

// ---------------------------------------------------------------------------
// Middleware
// ---------------------------------------------------------------------------

type contextKey string

const claimsKey contextKey = "claims"

// authenticated checks the bearer token and puts the claims in the context.
func (s *Server) authenticated(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")

		// The scheme is case-insensitive per RFC 7235, and clients get it wrong in both directions.
		scheme, token, found := strings.Cut(header, " ")
		if !found || !strings.EqualFold(scheme, "Bearer") || token == "" {
			// WWW-Authenticate is required by the spec on a 401 and is what tells a client HOW to
			// authenticate. Most services omit it; it costs one line.
			w.Header().Set("WWW-Authenticate", `Bearer realm="url-shortener"`)
			s.problem(w, r, http.StatusUnauthorized, TypeUnauthorized, "Unauthorized",
				"Send an Authorization header of the form: Bearer <token>")

			return
		}

		claims, err := auth.Parse(s.secret, token)
		if err != nil {
			w.Header().Set("WWW-Authenticate", `Bearer realm="url-shortener", error="invalid_token"`)
			s.problem(w, r, http.StatusUnauthorized, TypeUnauthorized, "Unauthorized", "The token is not valid.")

			return
		}

		next(w, r.WithContext(context.WithValue(r.Context(), claimsKey, claims)))
	})
}

// claimsOf reads the claims a route's middleware put there.
func claimsOf(ctx context.Context) (*auth.Claims, bool) {
	claims, ok := ctx.Value(claimsKey).(*auth.Claims)

	return claims, ok
}

// logRequests logs one line per request.
func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// WrapResponseWriter, so the status is available after the handler ran. A plain ResponseWriter does
		// not remember what status was written, and wrapping it by hand means reimplementing Flush, Hijack
		// and Push or breaking whatever uses them.
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

		next.ServeHTTP(ww, r)

		s.log.InfoContext(r.Context(), "request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", ww.Status(),
			"bytes", ww.BytesWritten(),
			"duration_ms", time.Since(start).Milliseconds(),
			"request_id", middleware.GetReqID(r.Context()),
		)
	})
}

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	// A health check that only says "the process is up" is a health check that stays green while the database
	// is gone. This pings the pool, which is what the orchestrator actually wants to know.
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	if err := s.store.Pool().Ping(ctx); err != nil {
		s.problem(w, r, http.StatusServiceUnavailable, TypeInternal, "Unhealthy", "The database is not reachable.")

		return
	}

	s.writeJSON(w, r, http.StatusOK, map[string]string{"status": "ok"})
}

type credentials struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type tokenResponse struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	body, err := decode[credentials](w, r)
	if err != nil {
		s.problem(w, r, http.StatusBadRequest, TypeValidation, "Bad request", err.Error())

		return
	}

	email := strings.TrimSpace(body.Email)
	if !strings.Contains(email, "@") || len(email) > 254 {
		// Deliberately not a regex. The only correct way to validate an email address is to send one, and
		// every regex that tries rejects valid addresses. The length limit is the RFC 5321 maximum.
		s.problem(w, r, http.StatusBadRequest, TypeValidation, "Bad request", "That does not look like an email address.")

		return
	}

	if err := auth.ValidatePassword(body.Password); err != nil {
		s.problem(w, r, http.StatusBadRequest, TypeValidation, "Bad request", err.Error())

		return
	}

	hash, err := auth.Hash(body.Password, s.hashCost)
	if err != nil {
		s.internalError(w, r, "hash password", err)

		return
	}

	user, err := s.store.CreateUser(r.Context(), email, hash)
	if err != nil {
		if errors.Is(err, store.ErrEmailTaken) {
			// A 409 rather than a 400, because the request was well-formed and the state was the problem.
			//
			// This does leak that an email is registered, which is a real trade: the alternative is to
			// return 201 and send an email saying "someone tried to register with your address", which is
			// what a service handling anything sensitive should do. For this one, a clear error wins.
			s.problem(w, r, http.StatusConflict, TypeConflict, "Conflict", "That email is already registered.")

			return
		}

		s.internalError(w, r, "create user", err)

		return
	}

	s.issueToken(w, r, user, http.StatusCreated)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	body, err := decode[credentials](w, r)
	if err != nil {
		s.problem(w, r, http.StatusBadRequest, TypeValidation, "Bad request", err.Error())

		return
	}

	user, err := s.store.UserByEmail(r.Context(), strings.TrimSpace(body.Email))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			// The same response as a wrong password, and the same amount of work is NOT done, which is a
			// timing oracle a determined attacker can use to enumerate accounts. Closing it means hashing
			// against a dummy hash here; that costs a bcrypt on every probe and is the right call for a
			// service where account existence is sensitive. It is not, here, and saying so is better than
			// leaving the reader to wonder.
			s.problem(w, r, http.StatusUnauthorized, TypeUnauthorized, "Unauthorized", "Email or password is wrong.")

			return
		}

		s.internalError(w, r, "look up user", err)

		return
	}

	if err := auth.Verify(user.PasswordHash, body.Password); err != nil {
		s.problem(w, r, http.StatusUnauthorized, TypeUnauthorized, "Unauthorized", "Email or password is wrong.")

		return
	}

	s.issueToken(w, r, user, http.StatusOK)
}

func (s *Server) issueToken(w http.ResponseWriter, r *http.Request, user *store.User, status int) {
	token, err := auth.Issue(s.secret, user.ID, user.Email, s.tokenTTL)
	if err != nil {
		s.internalError(w, r, "issue token", err)

		return
	}

	s.writeJSON(w, r, status, tokenResponse{
		Token:     token,
		ExpiresAt: time.Now().Add(s.tokenTTL).UTC().Truncate(time.Second),
	})
}

type createURLRequest struct {
	Target    string     `json:"target"`
	Slug      string     `json:"slug,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

type urlResponse struct {
	Slug       string     `json:"slug"`
	ShortURL   string     `json:"short_url"`
	Target     string     `json:"target"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	ClickCount int64      `json:"click_count"`
}

func (s *Server) toResponse(u *store.URL) urlResponse {
	return urlResponse{
		Slug:       u.Slug,
		ShortURL:   s.baseURL + "/" + u.Slug,
		Target:     u.Target,
		CreatedAt:  u.CreatedAt.UTC(),
		ExpiresAt:  u.ExpiresAt,
		ClickCount: u.ClickCount,
	}
}

func (s *Server) handleCreateURL(w http.ResponseWriter, r *http.Request) {
	claims, _ := claimsOf(r.Context())

	userID, err := claims.UserID()
	if err != nil {
		s.problem(w, r, http.StatusUnauthorized, TypeUnauthorized, "Unauthorized", "The token has no usable subject.")

		return
	}

	body, err := decode[createURLRequest](w, r)
	if err != nil {
		s.problem(w, r, http.StatusBadRequest, TypeValidation, "Bad request", err.Error())

		return
	}

	if err := validateTarget(body.Target); err != nil {
		s.problem(w, r, http.StatusBadRequest, TypeValidation, "Bad request", err.Error())

		return
	}

	if body.Slug != "" {
		if err := shortener.ValidateCustom(body.Slug); err != nil {
			s.problem(w, r, http.StatusBadRequest, TypeValidation, "Bad request", err.Error())

			return
		}
	}

	if body.ExpiresAt != nil && body.ExpiresAt.Before(time.Now()) {
		s.problem(w, r, http.StatusBadRequest, TypeValidation, "Bad request", "expires_at is in the past.")

		return
	}

	url, err := s.store.CreateURL(r.Context(), userID, body.Target, body.Slug, body.ExpiresAt, shortener.Obfuscate)
	if err != nil {
		if errors.Is(err, store.ErrSlugTaken) {
			s.problem(w, r, http.StatusConflict, TypeConflict, "Conflict", "That slug is already taken.")

			return
		}

		s.internalError(w, r, "create url", err)

		return
	}

	// The negative cache entry from anyone who asked for this slug before it existed. Without this, a slug
	// someone probed stays 404 for the negative TTL after it is created.
	if s.cache != nil {
		if err := s.cache.Delete(r.Context(), url.Slug); err != nil {
			s.log.WarnContext(r.Context(), "clear cache after create", "slug", url.Slug, "error", err)
		}
	}

	w.Header().Set("Location", s.baseURL+"/"+url.Slug)
	s.writeJSON(w, r, http.StatusCreated, s.toResponse(url))
}

// validateTarget checks the URL a short link points at.
//
// # Why this is a security check and not a formatting one
//
// A shortener that accepts any string becomes a redirector for whatever an attacker wants: a javascript: URL
// that runs in whoever follows the link, a file: URL, or a link to an internal address that turns the service
// into an SSRF probe for anything that follows redirects server-side.
//
// The scheme allow-list is the whole defence and it has to be an allow-list. A deny-list of "javascript" misses
// "JaVaScRiPt:", "data:", "vbscript:" and whatever a browser adds next.
func validateTarget(target string) error {
	if target == "" {
		return errors.New("target is required")
	}

	const maxLen = 2048
	if len(target) > maxLen {
		return fmt.Errorf("target is %d characters, the limit is %d", len(target), maxLen)
	}

	parsed, err := url.Parse(target)
	if err != nil {
		return fmt.Errorf("target is not a URL: %w", err)
	}

	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
	default:
		return fmt.Errorf("target must be http or https, got %q", parsed.Scheme)
	}

	if parsed.Host == "" {
		return errors.New("target has no host")
	}

	return nil
}

func (s *Server) handleListURLs(w http.ResponseWriter, r *http.Request) {
	claims, _ := claimsOf(r.Context())

	userID, err := claims.UserID()
	if err != nil {
		s.problem(w, r, http.StatusUnauthorized, TypeUnauthorized, "Unauthorized", "The token has no usable subject.")

		return
	}

	limit := 20
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 100 {
			s.problem(w, r, http.StatusBadRequest, TypeValidation, "Bad request", "limit must be 1 to 100.")

			return
		}

		limit = n
	}

	var (
		afterCreatedAt *time.Time
		afterID        int64
	)

	if raw := r.URL.Query().Get("after"); raw != "" {
		createdAt, id, err := parseCursor(raw)
		if err != nil {
			s.problem(w, r, http.StatusBadRequest, TypeValidation, "Bad request", err.Error())

			return
		}

		afterCreatedAt, afterID = &createdAt, id
	}

	urls, err := s.store.URLsByUser(r.Context(), userID, limit, afterCreatedAt, afterID)
	if err != nil {
		s.internalError(w, r, "list urls", err)

		return
	}

	items := make([]urlResponse, 0, len(urls))
	for i := range urls {
		items = append(items, s.toResponse(&urls[i]))
	}

	resp := map[string]any{"items": items}

	// A cursor only when a next page might exist. Returning one for a short page makes a client fetch an
	// empty page to find out it is done.
	if len(urls) == limit {
		last := urls[len(urls)-1]
		resp["next"] = formatCursor(last.CreatedAt, last.ID)
	}

	s.writeJSON(w, r, http.StatusOK, resp)
}

// formatCursor encodes the keyset.
//
// RFC 3339 with nanoseconds, then the id. The nanoseconds matter: truncating to a second makes two URLs created
// in the same second indistinguishable to the cursor, and the tuple comparison then skips one.
func formatCursor(createdAt time.Time, id int64) string {
	return createdAt.UTC().Format(time.RFC3339Nano) + "," + strconv.FormatInt(id, 10)
}

func parseCursor(raw string) (time.Time, int64, error) {
	timestamp, idPart, found := strings.Cut(raw, ",")
	if !found {
		return time.Time{}, 0, errors.New("after is not a cursor")
	}

	createdAt, err := time.Parse(time.RFC3339Nano, timestamp)
	if err != nil {
		return time.Time{}, 0, fmt.Errorf("after has a bad timestamp: %w", err)
	}

	id, err := strconv.ParseInt(idPart, 10, 64)
	if err != nil {
		return time.Time{}, 0, fmt.Errorf("after has a bad id: %w", err)
	}

	return createdAt, id, nil
}

func (s *Server) handleDeleteURL(w http.ResponseWriter, r *http.Request) {
	claims, _ := claimsOf(r.Context())

	userID, err := claims.UserID()
	if err != nil {
		s.problem(w, r, http.StatusUnauthorized, TypeUnauthorized, "Unauthorized", "The token has no usable subject.")

		return
	}

	slug := r.PathValue("slug")

	if err := s.store.DeleteURL(r.Context(), userID, slug); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.problem(w, r, http.StatusNotFound, TypeNotFound, "Not found", "No such short link.")

			return
		}

		s.internalError(w, r, "delete url", err)

		return
	}

	// Delete from the cache AFTER the database, not before.
	//
	// Before, and a concurrent reader can repopulate the cache from the row that is still there, leaving a
	// deleted URL cached for a full TTL. After, the worst case is a brief window where the cache serves a
	// redirect to something already deleted, which ends at the next read.
	if s.cache != nil {
		if err := s.cache.Delete(r.Context(), slug); err != nil {
			s.log.WarnContext(r.Context(), "clear cache after delete", "slug", slug, "error", err)
		}
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	claims, _ := claimsOf(r.Context())

	userID, err := claims.UserID()
	if err != nil {
		s.problem(w, r, http.StatusUnauthorized, TypeUnauthorized, "Unauthorized", "The token has no usable subject.")

		return
	}

	slug := r.PathValue("slug")

	url, err := s.store.URLBySlug(r.Context(), slug)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.problem(w, r, http.StatusNotFound, TypeNotFound, "Not found", "No such short link.")

			return
		}

		s.internalError(w, r, "look up url", err)

		return
	}

	if url.UserID != userID {
		// A 404, not a 403. Telling a caller that a slug exists but is not theirs lets them enumerate other
		// people's slugs one request at a time.
		s.problem(w, r, http.StatusNotFound, TypeNotFound, "Not found", "No such short link.")

		return
	}

	s.writeJSON(w, r, http.StatusOK, s.toResponse(url))
}

// handleRedirect is the hot path.
//
// # What it does and what it deliberately does not
//
// It reads the cache, falls back to the store, sends a 302, and enqueues a click. It does not write to
// Postgres, does not wait for the enqueue's result beyond the call, and does not fail the redirect when the
// enqueue fails. A missing click is a wrong statistic; a failed redirect is a broken link.
func (s *Server) handleRedirect(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")

	entry, err := s.lookup(r.Context(), slug)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.problem(w, r, http.StatusNotFound, TypeNotFound, "Not found", "No such short link.")

			return
		}

		s.internalError(w, r, "look up slug", err)

		return
	}

	if entry.Missing {
		s.problem(w, r, http.StatusNotFound, TypeNotFound, "Not found", "No such short link.")

		return
	}

	if entry.ExpiresAt != nil && entry.ExpiresAt.Before(time.Now()) {
		// 410 Gone, not 404.
		//
		// The distinction is real: 404 means "I have never heard of this", 410 means "this existed and is
		// finished". A crawler treats them differently, and a person gets a better message.
		s.problem(w, r, http.StatusGone, TypeGone, "Gone", "That short link has expired.")

		return
	}

	s.enqueueClick(r, entry.URLID)

	// 302, not 301.
	//
	// 301 is permanent and browsers cache it aggressively and sometimes forever. A shortener that issues 301s
	// cannot change or delete a link: the browser will not ask again. 302 costs a request per visit and keeps
	// the service in control of its own URLs, which is the entire product.
	//
	// It also means the click counter sees every visit rather than only the first.
	http.Redirect(w, r, entry.Target, http.StatusFound)
}

// lookup goes through the cache when there is one.
func (s *Server) lookup(ctx context.Context, slug string) (cache.Entry, error) {
	fetch := func(ctx context.Context) (cache.Entry, error) {
		url, err := s.store.URLBySlug(ctx, slug)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				// A negative entry, not an error. The absence is a fact worth caching.
				return cache.Entry{Missing: true}, nil
			}

			return cache.Entry{}, err
		}

		return cache.Entry{URLID: url.ID, Target: url.Target, ExpiresAt: url.ExpiresAt}, nil
	}

	if s.cache == nil {
		return fetch(ctx)
	}

	return s.cache.Lookup(ctx, slug, fetch)
}

// enqueueClick fires and forgets, with a log line when it fails.
func (s *Server) enqueueClick(r *http.Request, urlID int64) {
	if s.enqueuer == nil {
		return
	}

	task, err := tasks.NewRecordClick(tasks.RecordClickPayload{
		URLID: urlID,
		// The referrer header is spelled "Referer", with the historical typo, and Go's canonical form keeps
		// it. Asking for "Referrer" returns "".
		Referrer:  r.Referer(),
		UserAgent: r.UserAgent(),
		ClickedAt: time.Now().UTC(),
	})
	if err != nil {
		s.log.WarnContext(r.Context(), "build click task", "error", err)

		return
	}

	// A short timeout of its own rather than the request's context.
	//
	// The request's context is cancelled when the response is written, and this runs just before the
	// redirect, so using it is a race between the enqueue and the client's connection closing. Its own
	// context with a deadline is the correct shape for work that outlives the request.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 2*time.Second)
	defer cancel()

	if _, err := s.enqueuer.EnqueueContext(ctx, task); err != nil {
		s.log.WarnContext(r.Context(), "enqueue click", "url_id", urlID, "error", err)
	}
}
