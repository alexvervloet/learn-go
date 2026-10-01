// Package api is the HTTP layer.
//
// # What is different from the url-shortener capstone
//
// Two tokens rather than one, so there is a /refresh endpoint and a logout that actually logs out. A rate
// limiter on the credential endpoints. And a domain with relationships, so the handlers are about validation
// and ownership rather than about one lookup.
package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/alexvervloet/learn-go/capstones/bookmark-manager/internal/auth"
	"github.com/alexvervloet/learn-go/capstones/bookmark-manager/internal/ratelimit"
	"github.com/alexvervloet/learn-go/capstones/bookmark-manager/internal/store"
	"github.com/go-chi/chi/v5/middleware"
)

// Server holds the dependencies.
type Server struct {
	store          *store.Store
	limiter        *ratelimit.Limiter
	accountLimiter *ratelimit.Limiter
	log            *slog.Logger

	secret     []byte
	accessTTL  time.Duration
	refreshTTL time.Duration
	hashCost   int
}

// Options configures a Server.
type Options struct {
	Store   *store.Store
	Limiter *ratelimit.Limiter

	// AccountLimiter limits attempts per email address, on top of Limiter's cap on the endpoints as a whole.
	AccountLimiter *ratelimit.Limiter

	Logger *slog.Logger

	Secret     []byte
	AccessTTL  time.Duration
	RefreshTTL time.Duration
	HashCost   int
}

// New builds a Server.
func New(opts Options) *Server {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}

	if opts.HashCost == 0 {
		opts.HashCost = auth.Cost
	}

	if opts.AccessTTL == 0 {
		opts.AccessTTL = 15 * time.Minute
	}

	if opts.RefreshTTL == 0 {
		opts.RefreshTTL = 30 * 24 * time.Hour
	}

	return &Server{
		store:          opts.Store,
		limiter:        opts.Limiter,
		accountLimiter: opts.AccountLimiter,
		log:            opts.Logger,
		secret:         opts.Secret,
		accessTTL:      opts.AccessTTL,
		refreshTTL:     opts.RefreshTTL,
		hashCost:       opts.HashCost,
	}
}

// Routes returns the handler.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", s.handleHealth)

	// The credential endpoints carry the rate limiter. The others do not, because a rate limit on a read is a
	// different feature with a different key and a different failure policy.
	mux.Handle("POST /api/v1/register", s.rateLimited(s.handleRegister))
	mux.Handle("POST /api/v1/login", s.rateLimited(s.handleLogin))
	mux.HandleFunc("POST /api/v1/refresh", s.handleRefresh)
	mux.HandleFunc("POST /api/v1/logout", s.handleLogout)

	mux.Handle("GET /api/v1/bookmarks", s.authenticated(s.handleListBookmarks))
	mux.Handle("POST /api/v1/bookmarks", s.authenticated(s.handleCreateBookmark))
	mux.Handle("DELETE /api/v1/bookmarks/{id}", s.authenticated(s.handleDeleteBookmark))
	mux.Handle("GET /api/v1/search", s.authenticated(s.handleSearch))

	mux.Handle("GET /api/v1/categories", s.authenticated(s.handleListCategories))
	mux.Handle("POST /api/v1/categories", s.authenticated(s.handleCreateCategory))
	mux.Handle("DELETE /api/v1/categories/{id}", s.authenticated(s.handleDeleteCategory))

	mux.Handle("GET /api/v1/tags", s.authenticated(s.handleListTags))

	var h http.Handler = mux
	h = s.logRequests(h)
	h = middleware.Recoverer(h)
	h = middleware.RequestID(h)

	return h
}

// ---------------------------------------------------------------------------
// Errors
// ---------------------------------------------------------------------------

// Problem is RFC 9457.
type Problem struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// Problem type URIs. The URI is the stable contract; the title is prose.
const (
	TypeValidation   = "https://example.com/probs/validation"
	TypeUnauthorized = "https://example.com/probs/unauthorized"
	TypeNotFound     = "https://example.com/probs/not-found"
	TypeConflict     = "https://example.com/probs/conflict"
	TypeRateLimited  = "https://example.com/probs/rate-limited"
	TypeInternal     = "https://example.com/probs/internal"
)

func (s *Server) problem(w http.ResponseWriter, r *http.Request, status int, typeURI, title, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(Problem{
		Type: typeURI, Title: title, Status: status, Detail: detail,
	}); err != nil {
		s.log.ErrorContext(r.Context(), "write problem", "error", err)
	}
}

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
		s.log.ErrorContext(r.Context(), "write response", "error", err)
	}
}

// decode reads a JSON body with the limits a public endpoint needs.
func decode[T any](w http.ResponseWriter, r *http.Request) (T, error) {
	var v T

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	if err := dec.Decode(&v); err != nil {
		return v, err
	}

	// A second value in the body is a request-smuggling shape, not a nicety.
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

func (s *Server) authenticated(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scheme, token, found := strings.Cut(r.Header.Get("Authorization"), " ")
		if !found || !strings.EqualFold(scheme, "Bearer") || token == "" {
			w.Header().Set("WWW-Authenticate", `Bearer realm="bookmark-manager"`)
			s.problem(w, r, http.StatusUnauthorized, TypeUnauthorized, "Unauthorized",
				"Send an Authorization header of the form: Bearer <token>")

			return
		}

		claims, err := auth.ParseAccess(s.secret, token)
		if err != nil {
			// error="invalid_token" is RFC 6750's way of saying "the token, not the credentials". A client
			// that sees it knows to refresh rather than to ask the user to log in again, which is the whole
			// point of having two tokens.
			w.Header().Set("WWW-Authenticate", `Bearer realm="bookmark-manager", error="invalid_token"`)
			s.problem(w, r, http.StatusUnauthorized, TypeUnauthorized, "Unauthorized", "The access token is not valid.")

			return
		}

		next(w, r.WithContext(context.WithValue(r.Context(), claimsKey, claims)))
	})
}

// userID reads the subject from the context.
func (s *Server) userID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	claims, ok := r.Context().Value(claimsKey).(*auth.Claims)
	if !ok {
		s.problem(w, r, http.StatusUnauthorized, TypeUnauthorized, "Unauthorized", "No claims on this request.")

		return 0, false
	}

	id, err := claims.UserID()
	if err != nil {
		s.problem(w, r, http.StatusUnauthorized, TypeUnauthorized, "Unauthorized", "The token has no usable subject.")

		return 0, false
	}

	return id, true
}

// CredentialsKey is the rate-limit bucket the credential endpoints share.
//
// One bucket for register AND login, not one each. Both run bcrypt, both are what a credential-stuffing run
// aims at, and an attacker told that login is full simply moves to register. Two buckets is two limits that
// each look right and together allow twice the traffic.
const CredentialsKey = "credentials"

// rateLimited wraps the credential endpoints.
//
// # Keyed on the endpoint class, not on the client address
//
// This service does not have a trusted proxy in front of it, so r.RemoteAddr is the TCP peer, which behind any
// load balancer is the load balancer. Keying on it would give every user one shared bucket.
//
// So this is a GLOBAL limit on the credential endpoints, which is a blunt instrument and is honest about what
// it protects: bcrypt and the database, not an individual account. Set it to what bcrypt can afford, not to
// what one user needs, because every user shares it: at 20 a minute, one script logging in over and over
// locks everyone out. The per-account limit is limitAccount, keyed on the email in the body, which is why it
// lives in the handlers and not in this middleware.
//
// # Fail closed
//
// If Redis is unreachable, this refuses. On a login endpoint the limiter is the only thing between a
// credential-stuffing run and bcrypt, so failing open converts a Redis outage into an open door. A read
// endpoint would fail open, and that is the decision the handler makes rather than the limiter.
func (s *Server) rateLimited(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.limiter == nil {
			next(w, r)

			return
		}

		decision, err := s.limiter.Allow(r.Context(), CredentialsKey)
		if err != nil {
			s.limiterDown(w, r, err)

			return
		}

		setRateHeaders(w, decision)

		if !decision.Allowed {
			s.refuse(w, r, decision)

			return
		}

		next(w, r)
	})
}

// limitAccount applies the per-account limit and reports whether the request may go on.
//
// # Why per account, and why it only shows itself on a refusal
//
// The global bucket protects bcrypt; this one protects an account. Keyed on the lower-cased email, it stops a
// guesser working through passwords for one person without touching anyone else's logins, which the global
// bucket cannot do. It fails closed for the same reason the global one does.
//
// On success it sets no headers, so the RateLimit-* headers keep describing the endpoint-wide bucket. Telling
// a caller how many attempts remain on a particular account is telling a guesser their budget.
func (s *Server) limitAccount(w http.ResponseWriter, r *http.Request, email string) bool {
	if s.accountLimiter == nil {
		return true
	}

	decision, err := s.accountLimiter.Allow(r.Context(), accountKey(email))
	if err != nil {
		s.limiterDown(w, r, err)

		return false
	}

	if !decision.Allowed {
		setRateHeaders(w, decision)
		s.refuse(w, r, decision)

		return false
	}

	return true
}

// accountKey is the per-account bucket's key. Lower-cased, so VICTIM@example.com is not a fresh budget.
func accountKey(email string) string {
	return "account:" + strings.ToLower(email)
}

// setRateHeaders writes the draft RFC headers. RateLimit-Reset is a number of SECONDS from now rather than a
// timestamp, which is what makes it possible to compute without agreeing on a clock.
func setRateHeaders(w http.ResponseWriter, d ratelimit.Decision) {
	w.Header().Set("RateLimit-Limit", strconv.Itoa(d.Limit))
	w.Header().Set("RateLimit-Remaining", strconv.Itoa(d.Remaining))
	w.Header().Set("RateLimit-Reset", strconv.Itoa(int(d.ResetIn.Seconds()+0.999)))
}

// refuse is the 429. Retry-After as well as the RateLimit headers, because it is the one every client library
// already understands. It is also seconds, and it must be at least 1: a value of 0 tells a client to retry
// immediately.
func (s *Server) refuse(w http.ResponseWriter, r *http.Request, d ratelimit.Decision) {
	retry := max(int(d.ResetIn.Seconds()+0.999), 1)

	w.Header().Set("Retry-After", strconv.Itoa(retry))
	s.problem(w, r, http.StatusTooManyRequests, TypeRateLimited, "Too many requests",
		fmt.Sprintf("Try again in %d second(s).", retry))
}

// limiterDown is the fail-closed answer when Redis is unreachable.
func (s *Server) limiterDown(w http.ResponseWriter, r *http.Request, err error) {
	s.log.ErrorContext(r.Context(), "rate limiter unavailable", "error", err)
	s.problem(w, r, http.StatusServiceUnavailable, TypeRateLimited, "Unavailable",
		"The rate limiter is unreachable, so credential endpoints are closed.")
}

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

		next.ServeHTTP(ww, r)

		s.log.InfoContext(r.Context(), "request",
			"method", r.Method, "path", r.URL.Path, "status", ww.Status(),
			"duration_ms", time.Since(start).Milliseconds(),
			"request_id", middleware.GetReqID(r.Context()),
		)
	})
}

// ---------------------------------------------------------------------------
// Auth handlers
// ---------------------------------------------------------------------------

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
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

type tokenPair struct {
	AccessToken    string    `json:"access_token"`
	RefreshToken   string    `json:"refresh_token"`
	AccessExpires  time.Time `json:"access_expires_at"`
	RefreshExpires time.Time `json:"refresh_expires_at"`
	TokenType      string    `json:"token_type"`
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	body, err := decode[credentials](w, r)
	if err != nil {
		s.problem(w, r, http.StatusBadRequest, TypeValidation, "Bad request", err.Error())

		return
	}

	email := strings.TrimSpace(body.Email)

	if !s.limitAccount(w, r, email) {
		return
	}

	// Not a regex. The only way to validate an email address is to send one, and every regex that tries
	// rejects valid addresses. 254 is RFC 5321's maximum.
	if !strings.Contains(email, "@") || len(email) > 254 {
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
			s.problem(w, r, http.StatusConflict, TypeConflict, "Conflict", "That email is already registered.")

			return
		}

		s.internalError(w, r, "create user", err)

		return
	}

	s.issuePair(w, r, user, "", http.StatusCreated)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	body, err := decode[credentials](w, r)
	if err != nil {
		s.problem(w, r, http.StatusBadRequest, TypeValidation, "Bad request", err.Error())

		return
	}

	email := strings.TrimSpace(body.Email)

	if !s.limitAccount(w, r, email) {
		return
	}

	user, err := s.store.UserByEmail(r.Context(), email)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
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

	// A successful login clears the account's bucket. The bucket counts guesses, and the person who just
	// proved they know the password is not guessing: without this, two typos followed by a success leave them
	// two attempts closer to a lockout, and a script that logs in on a schedule locks itself out. A failure to
	// clear is logged and not fatal, because the login itself succeeded.
	if s.accountLimiter != nil {
		if err := s.accountLimiter.Reset(r.Context(), accountKey(email)); err != nil {
			s.log.WarnContext(r.Context(), "reset account rate limit", "error", err)
		}
	}

	s.issuePair(w, r, user, "", http.StatusOK)
}

// issuePair mints an access token and a refresh token, starting or continuing a family.
func (s *Server) issuePair(w http.ResponseWriter, r *http.Request, user *store.User, family string, status int) {
	access, err := auth.IssueAccess(s.secret, user.ID, user.Email, s.accessTTL)
	if err != nil {
		s.internalError(w, r, "issue access token", err)

		return
	}

	token, hash, err := auth.NewRefreshToken()
	if err != nil {
		s.internalError(w, r, "mint refresh token", err)

		return
	}

	expires := time.Now().Add(s.refreshTTL)

	if _, err := s.store.StoreRefreshToken(r.Context(), user.ID, hash, expires, family, nil); err != nil {
		s.internalError(w, r, "store refresh token", err)

		return
	}

	s.writeJSON(w, r, status, tokenPair{
		AccessToken:    access,
		RefreshToken:   token,
		AccessExpires:  time.Now().Add(s.accessTTL).UTC().Truncate(time.Second),
		RefreshExpires: expires.UTC().Truncate(time.Second),
		// "Bearer", because RFC 6750 says the scheme is what tells a client how to send it. Omitting it means
		// every client hard-codes the word.
		TokenType: "Bearer",
	})
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

// handleRefresh exchanges a refresh token for a new pair.
//
// # The reuse branch is the interesting one
//
// A token that has already been used means a copy exists somewhere it should not. The legitimate client threw
// its copy away when it got the replacement, so the only ways to see a second use are theft or a lost response
// that the client retried.
//
// The two are indistinguishable from here, so both get the safe answer: revoke the family. The honest client
// logs in again. The attacker gets one request.
func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	body, err := decode[refreshRequest](w, r)
	if err != nil {
		s.problem(w, r, http.StatusBadRequest, TypeValidation, "Bad request", err.Error())

		return
	}

	oldHash, err := auth.HashRefreshToken(body.RefreshToken)
	if err != nil {
		s.problem(w, r, http.StatusUnauthorized, TypeUnauthorized, "Unauthorized", "That is not a refresh token.")

		return
	}

	token, newHash, err := auth.NewRefreshToken()
	if err != nil {
		s.internalError(w, r, "mint refresh token", err)

		return
	}

	next, err := s.store.RotateRefreshToken(r.Context(), oldHash, newHash, s.refreshTTL)
	if err != nil {
		if errors.Is(err, store.ErrReuse) {
			// The family is in the error. Pulling it out of a message is fragile, so the store is asked
			// again rather than parsed.
			if existing, lookupErr := s.store.RefreshTokenByHash(r.Context(), oldHash); lookupErr == nil {
				revoked, revokeErr := s.store.RevokeFamily(r.Context(), existing.Family)
				if revokeErr != nil {
					s.log.ErrorContext(r.Context(), "revoke family", "error", revokeErr)
				}

				s.log.WarnContext(r.Context(), "refresh token reuse detected",
					"user_id", existing.UserID, "family", existing.Family, "revoked", revoked)
			}

			s.problem(w, r, http.StatusUnauthorized, TypeUnauthorized, "Unauthorized",
				"That refresh token was already used. Every session in its chain has been revoked; log in again.")

			return
		}

		if errors.Is(err, store.ErrNotFound) {
			s.problem(w, r, http.StatusUnauthorized, TypeUnauthorized, "Unauthorized",
				"That refresh token is not valid.")

			return
		}

		s.internalError(w, r, "rotate refresh token", err)

		return
	}

	// The access token needs the email, which the refresh token does not carry. One lookup per refresh, which
	// happens every fifteen minutes rather than every request, and that is the whole point of the split.
	user, err := s.userByID(r.Context(), next.UserID)
	if err != nil {
		s.internalError(w, r, "look up user", err)

		return
	}

	access, err := auth.IssueAccess(s.secret, user.ID, user.Email, s.accessTTL)
	if err != nil {
		s.internalError(w, r, "issue access token", err)

		return
	}

	s.writeJSON(w, r, http.StatusOK, tokenPair{
		AccessToken:    access,
		RefreshToken:   token,
		AccessExpires:  time.Now().Add(s.accessTTL).UTC().Truncate(time.Second),
		RefreshExpires: next.ExpiresAt.UTC().Truncate(time.Second),
		TokenType:      "Bearer",
	})
}

// userByID is a small lookup the refresh path needs.
func (s *Server) userByID(ctx context.Context, id int64) (*store.User, error) {
	var u store.User

	err := s.store.Pool().QueryRow(ctx,
		`SELECT id, email, password_hash, created_at FROM users WHERE id = $1`, id,
	).Scan(&u.ID, &u.Email, &u.PasswordHash, &u.CreatedAt)

	return &u, err
}

// handleLogout revokes one refresh token.
//
// # Why logout takes the refresh token and not the access token
//
// The access token cannot be revoked; it expires. Revoking the refresh token is what actually ends the session,
// and it takes effect at the moment the access token runs out, which is the price of a stateless access token
// and the reason its TTL is fifteen minutes and not a day.
//
// It returns 204 whether or not the token existed. A logout that reports "no such token" is an oracle for
// whether a token is valid, and there is nothing a client would do differently.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	body, err := decode[refreshRequest](w, r)
	if err != nil {
		s.problem(w, r, http.StatusBadRequest, TypeValidation, "Bad request", err.Error())

		return
	}

	hash, err := auth.HashRefreshToken(body.RefreshToken)
	if err != nil {
		w.WriteHeader(http.StatusNoContent)

		return
	}

	if err := s.store.RevokeToken(r.Context(), hash); err != nil && !errors.Is(err, store.ErrNotFound) {
		s.internalError(w, r, "revoke token", err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// Bookmark handlers
// ---------------------------------------------------------------------------

type bookmarkRequest struct {
	URL         string   `json:"url"`
	Title       string   `json:"title"`
	Description string   `json:"description,omitempty"`
	CategoryID  *int64   `json:"category_id,omitempty"`
	Tags        []string `json:"tags,omitempty"`
}

type bookmarkResponse struct {
	ID          int64     `json:"id"`
	URL         string    `json:"url"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	CategoryID  *int64    `json:"category_id,omitempty"`
	Tags        []string  `json:"tags"`
	CreatedAt   time.Time `json:"created_at"`
}

func toResponse(b *store.Bookmark) bookmarkResponse {
	tags := b.Tags
	if tags == nil {
		// A nil slice marshals to `null` and an empty one to `[]`. A client iterating the field has to
		// special-case null, so every list field is normalised here rather than in every client.
		tags = []string{}
	}

	return bookmarkResponse{
		ID: b.ID, URL: b.URL, Title: b.Title, Description: b.Description,
		CategoryID: b.CategoryID, Tags: tags, CreatedAt: b.CreatedAt.UTC(),
	}
}

func (s *Server) handleCreateBookmark(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.userID(w, r)
	if !ok {
		return
	}

	body, err := decode[bookmarkRequest](w, r)
	if err != nil {
		s.problem(w, r, http.StatusBadRequest, TypeValidation, "Bad request", err.Error())

		return
	}

	if err := validateURL(body.URL); err != nil {
		s.problem(w, r, http.StatusBadRequest, TypeValidation, "Bad request", err.Error())

		return
	}

	title := strings.TrimSpace(body.Title)
	if title == "" || len(title) > 500 {
		s.problem(w, r, http.StatusBadRequest, TypeValidation, "Bad request", "title is required and must be under 500 characters.")

		return
	}

	if len(body.Tags) > 20 {
		s.problem(w, r, http.StatusBadRequest, TypeValidation, "Bad request", "at most 20 tags.")

		return
	}

	bookmark, err := s.store.CreateBookmark(r.Context(), userID, store.NewBookmark{
		CategoryID:  body.CategoryID,
		URL:         body.URL,
		Title:       title,
		Description: strings.TrimSpace(body.Description),
		Tags:        body.Tags,
	})
	if err != nil {
		switch {
		case errors.Is(err, store.ErrURLSaved):
			s.problem(w, r, http.StatusConflict, TypeConflict, "Conflict", "You have already saved that URL.")
		case errors.Is(err, store.ErrNotFound):
			s.problem(w, r, http.StatusBadRequest, TypeValidation, "Bad request", "No such category.")
		default:
			s.internalError(w, r, "create bookmark", err)
		}

		return
	}

	s.writeJSON(w, r, http.StatusCreated, toResponse(bookmark))
}

// validateURL is the same allow-list argument as the url-shortener's.
//
// A deny-list of "javascript" misses "JaVaScRiPt:", "data:" and whatever comes next. A bookmark is rendered as
// a link, so a javascript: URL saved here runs in whoever clicks it.
func validateURL(raw string) error {
	if raw == "" {
		return errors.New("url is required")
	}

	const maxLen = 2048
	if len(raw) > maxLen {
		return fmt.Errorf("url is %d characters, the limit is %d", len(raw), maxLen)
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("url is not a URL: %w", err)
	}

	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
	default:
		return fmt.Errorf("url must be http or https, got %q", parsed.Scheme)
	}

	if parsed.Host == "" {
		return errors.New("url has no host")
	}

	return nil
}

func (s *Server) handleListBookmarks(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.userID(w, r)
	if !ok {
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

	after, err := decodeCursor(r.URL.Query().Get("after"))
	if err != nil {
		s.problem(w, r, http.StatusBadRequest, TypeValidation, "Bad request", "after is not a cursor this API issued.")

		return
	}

	// One row more than the page, to learn whether there is a next page without a count(*). The extra row is
	// dropped before the response.
	if tag := r.URL.Query().Get("tag"); tag != "" {
		bookmarks, err := s.store.BookmarksByTag(r.Context(), userID, tag, limit+1, after)
		if err != nil {
			s.internalError(w, r, "list by tag", err)

			return
		}

		s.writeList(w, r, bookmarks, limit)

		return
	}

	// ListTwoQueries, not ListOneQuery or ListNPlusOne.
	//
	// The three are measured in the store's tests. Two queries is the one that keeps the row scan simple and
	// does not depend on the driver's array handling, and the difference from one query is one round trip.
	bookmarks, _, err := s.store.ListTwoQueries(r.Context(), userID, limit+1, after)
	if err != nil {
		s.internalError(w, r, "list bookmarks", err)

		return
	}

	s.writeList(w, r, bookmarks, limit)
}

// writeList writes one page. bookmarks may hold one row more than limit, which means there is a next page, and
// "next" is then the cursor to ask for it with. On the last page there is no "next" at all.
func (s *Server) writeList(w http.ResponseWriter, r *http.Request, bookmarks []store.Bookmark, limit int) {
	var next string

	if len(bookmarks) > limit {
		bookmarks = bookmarks[:limit]
		last := bookmarks[len(bookmarks)-1]
		next = encodeCursor(store.Cursor{CreatedAt: last.CreatedAt, ID: last.ID})
	}

	items := make([]bookmarkResponse, 0, len(bookmarks))
	for i := range bookmarks {
		items = append(items, toResponse(&bookmarks[i]))
	}

	body := map[string]any{"items": items}
	if next != "" {
		body["next"] = next
	}

	s.writeJSON(w, r, http.StatusOK, body)
}

// encodeCursor makes a cursor opaque: base64url of "<unix microseconds>.<id>". Microseconds because that is
// timestamptz's precision, so the value round-trips exactly. Opaque so a client passes it back rather than
// building one, and the format can change without breaking anyone who did.
func encodeCursor(c store.Cursor) string {
	return base64.RawURLEncoding.EncodeToString(fmt.Appendf(nil, "%d.%d", c.CreatedAt.UnixMicro(), c.ID))
}

// decodeCursor reverses encodeCursor. An empty string is the first page.
func decodeCursor(raw string) (*store.Cursor, error) {
	if raw == "" {
		return nil, nil
	}

	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, err
	}

	micros, id, ok := strings.Cut(string(b), ".")
	if !ok {
		return nil, errors.New("cursor has no separator")
	}

	us, err := strconv.ParseInt(micros, 10, 64)
	if err != nil {
		return nil, err
	}

	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return nil, err
	}

	return &store.Cursor{CreatedAt: time.UnixMicro(us), ID: n}, nil
}

func (s *Server) handleDeleteBookmark(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.userID(w, r)
	if !ok {
		return
	}

	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.problem(w, r, http.StatusBadRequest, TypeValidation, "Bad request", "id must be a number.")

		return
	}

	if err := s.store.DeleteBookmark(r.Context(), userID, id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			// 404, not 403: a 403 confirms the bookmark exists and belongs to somebody.
			s.problem(w, r, http.StatusNotFound, TypeNotFound, "Not found", "No such bookmark.")

			return
		}

		s.internalError(w, r, "delete bookmark", err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.userID(w, r)
	if !ok {
		return
	}

	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		s.problem(w, r, http.StatusBadRequest, TypeValidation, "Bad request", "q is required.")

		return
	}

	results, err := s.store.Search(r.Context(), userID, query, 50)
	if err != nil {
		s.internalError(w, r, "search", err)

		return
	}

	type hit struct {
		bookmarkResponse

		Rank float32 `json:"rank"`
	}

	items := make([]hit, 0, len(results))
	for i := range results {
		items = append(items, hit{
			bookmarkResponse: toResponse(&results[i].Bookmark),
			Rank:             results[i].Rank,
		})
	}

	s.writeJSON(w, r, http.StatusOK, map[string]any{"items": items, "query": query})
}

// ---------------------------------------------------------------------------
// Category and tag handlers
// ---------------------------------------------------------------------------

func (s *Server) handleCreateCategory(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.userID(w, r)
	if !ok {
		return
	}

	body, err := decode[struct {
		Name string `json:"name"`
	}](w, r)
	if err != nil {
		s.problem(w, r, http.StatusBadRequest, TypeValidation, "Bad request", err.Error())

		return
	}

	name := strings.TrimSpace(body.Name)
	if name == "" || len(name) > 100 {
		s.problem(w, r, http.StatusBadRequest, TypeValidation, "Bad request", "name is required and must be under 100 characters.")

		return
	}

	category, err := s.store.CreateCategory(r.Context(), userID, name)
	if err != nil {
		if errors.Is(err, store.ErrNameTaken) {
			s.problem(w, r, http.StatusConflict, TypeConflict, "Conflict", "You already have a category with that name.")

			return
		}

		s.internalError(w, r, "create category", err)

		return
	}

	s.writeJSON(w, r, http.StatusCreated, map[string]any{"id": category.ID, "name": category.Name})
}

func (s *Server) handleListCategories(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.userID(w, r)
	if !ok {
		return
	}

	categories, err := s.store.CategoriesOf(r.Context(), userID)
	if err != nil {
		s.internalError(w, r, "list categories", err)

		return
	}

	items := make([]map[string]any, 0, len(categories))
	for _, c := range categories {
		items = append(items, map[string]any{"id": c.ID, "name": c.Name})
	}

	s.writeJSON(w, r, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleDeleteCategory(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.userID(w, r)
	if !ok {
		return
	}

	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.problem(w, r, http.StatusBadRequest, TypeValidation, "Bad request", "id must be a number.")

		return
	}

	if err := s.store.DeleteCategory(r.Context(), userID, id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.problem(w, r, http.StatusNotFound, TypeNotFound, "Not found", "No such category.")

			return
		}

		s.internalError(w, r, "delete category", err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListTags(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.userID(w, r)
	if !ok {
		return
	}

	tags, err := s.store.TagsOf(r.Context(), userID)
	if err != nil {
		s.internalError(w, r, "list tags", err)

		return
	}

	items := make([]map[string]any, 0, len(tags))
	for _, t := range tags {
		items = append(items, map[string]any{"id": t.ID, "name": t.Name, "count": t.Count})
	}

	s.writeJSON(w, r, http.StatusOK, map[string]any{"items": items})
}
