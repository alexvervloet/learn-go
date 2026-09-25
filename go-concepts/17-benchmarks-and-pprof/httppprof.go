package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/pprof"
	"sort"
	"strings"
)

// net/http/pprof
// ==============
//
// A running service exposes the same profiles over HTTP:
//
//	import _ "net/http/pprof"    // registers handlers on DefaultServeMux
//
//	go func() { log.Println(http.ListenAndServe("localhost:6060", nil)) }()
//
// Then:
//
//	go tool pprof http://localhost:6060/debug/pprof/profile?seconds=30
//	go tool pprof http://localhost:6060/debug/pprof/heap
//	curl http://localhost:6060/debug/pprof/goroutine?debug=2
//
// THE BLANK IMPORT IS THE TRAP. It registers on http.DefaultServeMux, so if
// your service also serves on DefaultServeMux, the profiling endpoints are on
// your PUBLIC port. That has been a real vulnerability in real services more
// than once.
//
// The endpoints are unauthenticated and reveal the source layout, every
// goroutine's stack, and the command line. Bind them to localhost, or register
// them explicitly on an admin mux as below.

// adminMux registers the pprof handlers EXPLICITLY, on a mux of your choosing,
// rather than relying on the blank import's side effect on DefaultServeMux.
//
// This is the form to use. It makes the endpoints visible in the code, puts
// them on a mux you control, and lets you wrap them in authentication.
func adminMux() *http.ServeMux {
	mux := http.NewServeMux()

	// The index, and the handlers it links to.
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)

	// The named profiles are served by Index through its /debug/pprof/ prefix,
	// so they need no separate registration. Listing them explicitly documents
	// what is exposed:
	//
	//	/debug/pprof/heap
	//	/debug/pprof/goroutine
	//	/debug/pprof/allocs
	//	/debug/pprof/block
	//	/debug/pprof/mutex
	//	/debug/pprof/threadcreate

	return mux
}

// requireAuth wraps a handler in a check. A shared token is the minimum; in
// practice this is an internal-only listener, mTLS, or an authenticated admin
// route, and the point is that SOMETHING is in front of it.
func requireAuth(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		provided := r.Header.Get("Authorization")

		// A constant-time comparison would be correct here; a plain one is
		// adequate for a token that never leaves a private network, and
		// subtle.ConstantTimeCompare is what you want otherwise.
		if provided != "Bearer "+token {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// separateAdminListener is the shape to copy: the application on one port, the
// admin endpoints on another, bound to localhost.
//
//	appSrv := &http.Server{Addr: ":8080", Handler: appMux}
//	adminSrv := &http.Server{Addr: "127.0.0.1:6060", Handler: adminMux()}
//
//	go adminSrv.ListenAndServe()
//	appSrv.ListenAndServe()
//
// Binding to 127.0.0.1 rather than :6060 is the difference between "reachable
// from the host" and "reachable from the internet". In Kubernetes, a port that
// is not in the Service is not exposed, which is a second layer rather than a
// substitute.
func separateAdminListener() []string {
	return []string{
		"the application on :8080, the admin endpoints on 127.0.0.1:6060",
		"binding to 127.0.0.1 rather than :6060 is the whole difference",
		"reach it with kubectl port-forward, or an SSH tunnel",
		"never in the Service, never in the Ingress, never on the public listener",
	}
}

// exercisePprofEndpoints starts a test server with the admin mux and fetches
// two endpoints, so the demo shows real output rather than describing it.
func exercisePprofEndpoints() (endpoints []string, goroutineDump string, err error) {
	srv := httptest.NewServer(adminMux())
	defer srv.Close()

	get := func(path string) (int, string, error) {
		resp, gerr := http.Get(srv.URL + path) //nolint:noctx // a local test server
		if gerr != nil {
			return 0, "", fmt.Errorf("get %s: %w", path, gerr)
		}
		defer resp.Body.Close() //nolint:errcheck // read path

		body, rerr := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if rerr != nil {
			return resp.StatusCode, "", fmt.Errorf("read %s: %w", path, rerr)
		}
		return resp.StatusCode, string(body), nil
	}

	for _, path := range []string{
		"/debug/pprof/heap?debug=1",
		"/debug/pprof/goroutine?debug=1",
		"/debug/pprof/allocs?debug=1",
		"/debug/pprof/cmdline",
	} {
		status, _, gerr := get(path)
		if gerr != nil {
			return nil, "", gerr
		}
		endpoints = append(endpoints, fmt.Sprintf("%-34s %d", path, status))
	}

	_, goroutineDump, err = get("/debug/pprof/goroutine?debug=1")
	if err != nil {
		return endpoints, "", err
	}

	return endpoints, goroutineDump, nil
}

// authProtectsTheEndpoints demonstrates the wrapper rejecting and accepting.
func authProtectsTheEndpoints() (withoutToken, withToken int, err error) {
	const token = "secret-admin-token"

	srv := httptest.NewServer(requireAuth(token, adminMux()))
	defer srv.Close()

	call := func(header string) (int, error) {
		req, rerr := http.NewRequest(http.MethodGet, srv.URL+"/debug/pprof/heap?debug=1", nil) //nolint:noctx // a local test server
		if rerr != nil {
			return 0, fmt.Errorf("build request: %w", rerr)
		}
		if header != "" {
			req.Header.Set("Authorization", header)
		}

		resp, derr := http.DefaultClient.Do(req)
		if derr != nil {
			return 0, fmt.Errorf("do: %w", derr)
		}
		defer resp.Body.Close() //nolint:errcheck // read path

		_, _ = io.Copy(io.Discard, resp.Body)
		return resp.StatusCode, nil
	}

	withoutToken, err = call("")
	if err != nil {
		return 0, 0, err
	}

	withToken, err = call("Bearer " + token)
	if err != nil {
		return withoutToken, 0, err
	}

	return withoutToken, withToken, nil
}

// endpointReference lists what each one gives you.
func endpointReference() map[string]string {
	return map[string]string{
		"/debug/pprof/":                   "an HTML index of everything available",
		"/debug/pprof/profile?seconds=30": "a 30-second CPU profile; the default is 30",
		"/debug/pprof/heap":               "the heap: what is still reachable",
		"/debug/pprof/allocs":             "every allocation since start",
		"/debug/pprof/goroutine?debug=2":  "EVERY goroutine's full stack: the leak-hunting tool",
		"/debug/pprof/block":              "blocking profile; needs runtime.SetBlockProfileRate",
		"/debug/pprof/mutex":              "contention; needs runtime.SetMutexProfileFraction",
		"/debug/pprof/trace?seconds=5":    "an execution trace, for go tool trace",
	}
}

// profilesThatNeedEnabling: two of them collect nothing until you turn them on,
// which is a common half-hour lost to an empty profile.
func profilesThatNeedEnabling() []string {
	return []string{
		"runtime.SetBlockProfileRate(1)      // every blocking event; 0 disables",
		"runtime.SetMutexProfileFraction(1)  // every contention event; 0 disables",
		"both cost real overhead: sample rather than capture everything in production",
		"without them, /debug/pprof/block and /mutex return an EMPTY profile and no error",
	}
}

// securityRules, because this is the part that has gone wrong in real services.
func securityRules() []string {
	return []string{
		"the blank import registers on http.DefaultServeMux, which may be your PUBLIC mux",
		"the endpoints are unauthenticated and there is no flag to change that",
		"goroutine?debug=2 dumps every stack: paths, function names, sometimes arguments",
		"cmdline dumps the command line, which often carries flags you did not mean to publish",
		"profile?seconds=N ties up a profiling slot: a trivial denial of service",
		"so: a separate listener on 127.0.0.1, or an explicit mux behind authentication",
	}
}

// demoHTTPPprof prints the endpoints working.
func demoHTTPPprof() {
	endpoints, dump, err := exercisePprofEndpoints()
	fmt.Printf("  a real admin mux, four endpoints (err=%v):\n", err)
	for _, e := range endpoints {
		fmt.Printf("    %s\n", e)
	}

	lines := strings.Split(strings.TrimSpace(dump), "\n")
	fmt.Printf("\n  the first lines of /debug/pprof/goroutine?debug=1:\n")
	for _, line := range lines[:min(4, len(lines))] {
		fmt.Printf("    | %s\n", line)
	}

	withoutToken, withToken, err := authProtectsTheEndpoints()
	fmt.Printf("\n  behind authentication: no token -> %d, with a token -> %d (err=%v)\n",
		withoutToken, withToken, err)

	fmt.Println("\n  the endpoints:")
	keys := make([]string, 0, len(endpointReference()))
	for k := range endpointReference() {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Printf("    %-32s %s\n", k, endpointReference()[k])
	}

	fmt.Println("\n  two profiles collect nothing until enabled:")
	for _, s := range profilesThatNeedEnabling() {
		fmt.Printf("    %s\n", s)
	}

	fmt.Println("\n  a separate admin listener:")
	for _, s := range separateAdminListener() {
		fmt.Printf("    %s\n", s)
	}

	fmt.Println("\n  the security rules:")
	for _, s := range securityRules() {
		fmt.Printf("    %s\n", s)
	}
}
