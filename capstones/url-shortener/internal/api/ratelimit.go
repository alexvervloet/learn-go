package api

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/alexvervloet/learn-go/capstones/url-shortener/internal/ratelimit"
)

// TypeRateLimited is the problem type for a 429 and for the limiter being unreachable.
const TypeRateLimited = "https://example.com/probs/rate-limited"

// CredentialsKey is the endpoint-wide bucket register and login share.
//
// One bucket for both, not one each. Both run bcrypt, both are what a credential-stuffing run aims at, and an
// attacker told that login is full simply moves to register.
const CredentialsKey = "credentials"

// rateLimited wraps the credential endpoints in the endpoint-wide limit.
//
// # Two limits, and why this one is not per client
//
// This service trusts no proxy header (see Routes), so it has no client address worth keying on: behind a load
// balancer r.RemoteAddr is the load balancer. So there are two limits. This one is GLOBAL and protects bcrypt,
// which every user shares, so it is sized to the CPU and not to one person. limitAccount is per email and
// protects an account from password guessing without touching anyone else's logins.
//
// # Fail closed
//
// If Redis is unreachable, credential endpoints refuse. The limiter is the only thing between a
// credential-stuffing run and bcrypt, so failing open turns a Redis outage into an open door. Redirects are not
// limited at all, and a Redis outage only slows them, through the cache.
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
// On success it sets no headers, so the RateLimit-* headers keep describing the endpoint-wide bucket. Telling a
// caller how many attempts remain on a particular account is telling a guesser their budget.
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

// clearAccount resets the per-account bucket after a successful login. The bucket counts guesses, and someone
// who just proved they know the password is not guessing. A failure is logged, not fatal: the login succeeded.
func (s *Server) clearAccount(r *http.Request, email string) {
	if s.accountLimiter == nil {
		return
	}

	if err := s.accountLimiter.Reset(r.Context(), accountKey(email)); err != nil {
		s.log.WarnContext(r.Context(), "reset account rate limit", "error", err)
	}
}

// accountKey is the per-account bucket's key. Lower-cased, so VICTIM@example.com is not a fresh budget.
func accountKey(email string) string {
	return "account:" + strings.ToLower(email)
}

// setRateHeaders writes the draft RFC headers. RateLimit-Reset is SECONDS from now, not a timestamp, so it
// needs no agreement about clocks.
func setRateHeaders(w http.ResponseWriter, d ratelimit.Decision) {
	w.Header().Set("RateLimit-Limit", strconv.Itoa(d.Limit))
	w.Header().Set("RateLimit-Remaining", strconv.Itoa(d.Remaining))
	w.Header().Set("RateLimit-Reset", strconv.Itoa(int(d.ResetIn.Seconds()+0.999)))
}

// refuse is the 429, with Retry-After because every client library understands it. At least 1: a 0 tells a
// client to retry immediately.
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
