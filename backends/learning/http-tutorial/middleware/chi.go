package middleware

import (
	"log/slog"
	"net/http"
	"path"
	"strings"
	"time"

	chimw "github.com/go-chi/chi/v5/middleware"
)

// Using chi's middleware with the standard library's router
// =========================================================
//
// This is the combination worth knowing about, and it is the one this repo picked. chi has two
// halves that can be used independently:
//
//	github.com/go-chi/chi/v5             the router
//	github.com/go-chi/chi/v5/middleware  a middleware library
//
// Since Go 1.22 the router half is largely redundant: ServeMux does methods, path variables and
// specificity-based precedence. The middleware half is not, and it is a plain
// func(http.Handler) http.Handler, so it drops straight into a stdlib mux with no adapter.
//
// With one exception, which is the thing worth knowing. chimw.CleanPath PANICS with a nil
// pointer dereference when there is no chi router in the chain, because it reaches for
// chi.RouteContext and writes to it. Not only on paths that need cleaning: on every request.
// I tested all seventeen of chi's middleware against a bare stdlib handler and CleanPath is
// the only one that does this; see TestChiCleanPathNeedsChisRouter and CleanPath below for the
// five-line replacement.
//
// What chi's middleware gives you that the stdlib does not:
//
//	Compress          gzip and deflate negotiation, done correctly
//	Throttle          a concurrency limiter with a backlog
//	RealIP            DEPRECATED and IP-spoofable; see RealIP below for why, and for
//	                  the replacement this package ships
//	CleanPath         collapses //double//slashes before routing
//	StripSlashes      the other half of the redirect problem in routing/
//	Timeout           the same idea as this package's Timeout
//	Recoverer         the same idea as Recovery
//	WrapResponseWriter a recorder like this package's, with Unwrap and the optional
//	                  interfaces handled
//	GetHead           routes HEAD to a GET handler, which ServeMux has done since 1.22
//
// The overlapping ones are here so the hand-written versions can be compared with a
// production implementation. Compress and Throttle are the two there is no reason to write
// yourself.
//
// # What chi's router still gives you
//
// Route groups with per-group middleware, `chi.Mount` for sub-routers, regexp patterns, and
// `chi.RouteContext` for reverse lookups. If a service needs per-group middleware on a dozen
// groups, chi's router earns its 1,000 lines. Most do not.

// Production returns the chain a real service would run, mixing chi's middleware with this
// package's.
//
// The order is the argument:
//
//  1. RealIP        before anything that logs or rate-limits by address. THIS
//     package's, not chi's, which is deprecated as IP-spoofable. Whether to
//     trust the proxy headers is the caller's decision, and the only safe
//     default is no: a server reachable directly lets any client write its
//     own X-Forwarded-For. The first version passed true unconditionally
//  2. RequestID     before logging, so the line has an ID
//  3. Logger        outside Recovery, so a panicking request still gets a request line
//  4. Recovery      outside the handler
//  5. CleanPath     before routing, since it changes the path the router sees
//  6. SecureHeaders anywhere before the handler writes
//  7. Compress      innermost of the response-touching ones, so it wraps the smallest set
//  8. Timeout       innermost, so it bounds only the handler
func Production(log *slog.Logger, trustProxyHeaders bool) Middleware {
	return Chain(
		RealIP(trustProxyHeaders),
		RequestID,
		Logger(log),
		Recovery(log),
		CleanPath,
		SecureHeaders,
		chimw.Compress(5),
		Timeout(30*time.Second),
	)
}

// RealIP rewrites r.RemoteAddr from the proxy headers, when they can be trusted.
//
// chi has one of these and it is DEPRECATED, with three security advisories against it
// (GHSA-3fxj-6jh8-hvhx, GHSA-rjr7-jggh-pgcp, GHSA-9g5q-2w5x-hmxf). Two reasons, and both are
// worth understanding because they are the two ways every implementation of this gets it wrong:
//
//	IT TAKES THE LEFTMOST X-Forwarded-For VALUE. A proxy APPENDS the address it saw, so the
//	  leftmost entry is whatever the client sent and the rightmost is the only one the proxy
//	  vouches for. Taking the leftmost lets anyone choose their own apparent IP, which
//	  defeats rate limiting and audit logging at the same time.
//
//	IT TRUSTS THE HEADERS UNCONDITIONALLY. A service reachable directly, and not only
//	  through its load balancer, has no reason to believe any of them.
//
// This version takes the RIGHTMOST entry and makes the trust decision an argument, so a
// deployment that is not behind a proxy passes false and keeps the real peer address.
//
// "Rightmost" is still a simplification: with N proxies in the chain you want the (N+1)th from
// the right, because the last N were added by infrastructure you control. Anything more careful
// than this has to know the topology.
func RealIP(trustProxyHeaders bool) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !trustProxyHeaders {
				next.ServeHTTP(w, r)
				return
			}

			if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
				parts := strings.Split(forwarded, ",")

				// The LAST entry: the nearest proxy appended it, so it is the only
				// one not under the client's control.
				if ip := strings.TrimSpace(parts[len(parts)-1]); ip != "" {
					r.RemoteAddr = ip
				}
			} else if realIP := r.Header.Get("X-Real-IP"); realIP != "" {
				r.RemoteAddr = strings.TrimSpace(realIP)
			}

			next.ServeHTTP(w, r)
		})
	}
}

// CleanPath collapses repeated slashes and resolves . and .. before the router sees the path.
//
// chi has one of these and it cannot be used without chi's router. path.Clean does the work;
// the only judgement is what to do when the cleaned path differs, and redirecting is the right
// answer rather than rewriting silently, because a client that built a bad URL should learn
// about it and because two URLs serving the same content is bad for caches.
//
// path.Clean strips a trailing slash, which would defeat routing/'s subtree patterns, so it is
// put back.
func CleanPath(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cleaned := path.Clean(r.URL.Path)

		// path.Clean("/a/b/") is "/a/b", and a subtree pattern needs the slash.
		if strings.HasSuffix(r.URL.Path, "/") && !strings.HasSuffix(cleaned, "/") {
			cleaned += "/"
		}

		if cleaned == r.URL.Path {
			next.ServeHTTP(w, r)
			return
		}

		// 308 rather than 301, for the same reason ServeMux uses 307: a permanent
		// redirect that does not preserve the method turns a POST into a GET.
		target := *r.URL
		target.Path = cleaned

		http.Redirect(w, r, target.RequestURI(), http.StatusPermanentRedirect)
	})
}

// Throttled limits how many requests are in flight at once, queueing the rest.
//
// Different from rate limiting, and the distinction matters: a rate limiter caps requests per
// second and rejects the excess, while a throttle caps CONCURRENCY and makes the excess wait.
// A service whose bottleneck is a connection pool wants the throttle, because rejecting a
// request that would have succeeded in 50 ms is worse than making it wait 50 ms.
//
// backend-concepts covers rate limiting proper.
func Throttled(limit int) Middleware {
	return chimw.ThrottleBacklog(limit, limit*10, time.Second)
}
