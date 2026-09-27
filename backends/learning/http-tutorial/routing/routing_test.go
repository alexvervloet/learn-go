package routing

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// do sends a request through the mux and returns the recorder.
func do(t *testing.T, mux http.Handler, method, target string, body string) *httptest.ResponseRecorder {
	t.Helper()

	var reader *strings.Reader
	if body != "" {
		reader = strings.NewReader(body)
	} else {
		reader = strings.NewReader("")
	}

	req := httptest.NewRequest(method, target, reader)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	return rec
}

func TestRoutes(t *testing.T) {
	mux := NewMux(NewStore())

	tests := []struct {
		name       string
		method     string
		target     string
		body       string
		wantStatus int
		wantBody   string // a substring
	}{
		{"health", "GET", "/health", "", http.StatusOK, "ok"},
		{"list", "GET", "/items/", "", http.StatusOK, `"hammer"`},
		{"one item", "GET", "/items/2", "", http.StatusOK, `"nails"`},
		{"literal beats wildcard", "GET", "/items/latest", "", http.StatusOK, `"saw"`},
		{"bad id", "GET", "/items/abc", "", http.StatusBadRequest, "must be a number"},
		{"missing id", "GET", "/items/999", "", http.StatusNotFound, "not found"},
		{"create", "POST", "/items/", `{"name":"chisel"}`, http.StatusCreated, `"chisel"`},
		{"create with no name", "POST", "/items/", `{}`, http.StatusBadRequest, "name is required"},
		{"create with bad JSON", "POST", "/items/", `{`, http.StatusBadRequest, "invalid JSON"},
		{"multi-segment wildcard", "GET", "/files/a/b/c.txt", "", http.StatusOK, `"a/b/c.txt"`},
		{"subtree", "GET", "/api/anything/at/all", "", http.StatusOK, `"subtree"`},
		{"two variables", "GET", "/users/ada/items/7", "", http.StatusOK, `"ada"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(t, mux, tt.method, tt.target, tt.body)

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d (body %q)", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tt.wantBody) {
				t.Errorf("body = %q, want it to contain %q", rec.Body.String(), tt.wantBody)
			}
		})
	}
}

// TestPrecedenceIsBySpecificityNotRegistrationOrder is the rule that surprises people coming
// from Express or Flask, where the first matching route wins.
//
// Both orders are registered, and both give the same answer.
func TestPrecedenceIsBySpecificityNotRegistrationOrder(t *testing.T) {
	for _, order := range []string{"literal first", "wildcard first"} {
		t.Run(order, func(t *testing.T) {
			mux := http.NewServeMux()

			literal := func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte("literal"))
			}
			wildcard := func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte("wildcard"))
			}

			if order == "literal first" {
				mux.HandleFunc("GET /items/latest", literal)
				mux.HandleFunc("GET /items/{id}", wildcard)
			} else {
				mux.HandleFunc("GET /items/{id}", wildcard)
				mux.HandleFunc("GET /items/latest", literal)
			}

			if got := do(t, mux, "GET", "/items/latest", "").Body.String(); got != "literal" {
				t.Errorf("/items/latest matched %q, want the literal pattern", got)
			}
			if got := do(t, mux, "GET", "/items/42", "").Body.String(); got != "wildcard" {
				t.Errorf("/items/42 matched %q, want the wildcard pattern", got)
			}
		})
	}
}

// TestConflictingPatternsPanic is the best thing about the 1.22 router. Two patterns that
// overlap with neither being more specific cannot be resolved, so registration panics rather
// than silently picking one.
//
// A router that picks one produces a bug that appears only for certain URLs. A router that
// refuses to start makes it impossible to deploy.
func TestConflictingPatternsPanic(t *testing.T) {
	cases := []struct {
		name   string
		first  string
		second string
		panics bool
	}{
		{
			// /items/latest matches both, and neither is more specific: the first is
			// specific in the second segment, the second in the third.
			name:   "wildcards in different positions",
			first:  "GET /items/{id}/detail",
			second: "GET /items/latest/{part}",
			panics: true,
		},
		{
			name:   "the same pattern twice",
			first:  "GET /items/{id}",
			second: "GET /items/{other}",
			panics: true,
		},
		{
			// No conflict: the literal is strictly more specific.
			name:   "a literal and a wildcard",
			first:  "GET /items/{id}",
			second: "GET /items/latest",
			panics: false,
		},
		{
			// No conflict: different methods never overlap.
			name:   "different methods",
			first:  "GET /items/{id}",
			second: "POST /items/{id}",
			panics: false,
		},
		{
			// No conflict: {$} matches only /items/, the subtree matches below it too,
			// and {$} is more specific.
			name:   "exact and subtree",
			first:  "GET /items/",
			second: "GET /items/{$}",
			panics: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var panicked bool
			var recovered any

			func() {
				defer func() {
					if r := recover(); r != nil {
						panicked, recovered = true, r
					}
				}()

				mux := http.NewServeMux()
				mux.HandleFunc(tc.first, func(http.ResponseWriter, *http.Request) {})
				mux.HandleFunc(tc.second, func(http.ResponseWriter, *http.Request) {})
			}()

			if panicked != tc.panics {
				t.Errorf("registering %q then %q panicked = %v, want %v (recovered: %v)",
					tc.first, tc.second, panicked, tc.panics, recovered)
			}
			if panicked {
				t.Logf("panic message: %v", recovered)
			}
		})
	}
}

// TestDollarMeansExactly pins down {$}, which is the difference between a collection endpoint
// and a catch-all that swallows every URL beneath it.
func TestDollarMeansExactly(t *testing.T) {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /exact/{$}", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("exact"))
	})
	mux.HandleFunc("GET /subtree/", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("subtree"))
	})

	tests := []struct {
		target     string
		wantStatus int
		wantBody   string
	}{
		{"/exact/", http.StatusOK, "exact"},
		{"/exact/below", http.StatusNotFound, ""},
		{"/exact/a/b", http.StatusNotFound, ""},
		{"/subtree/", http.StatusOK, "subtree"},
		{"/subtree/below", http.StatusOK, "subtree"},
		{"/subtree/a/b/c", http.StatusOK, "subtree"},
	}

	for _, tt := range tests {
		rec := do(t, mux, "GET", tt.target, "")

		if rec.Code != tt.wantStatus {
			t.Errorf("%s: status = %d, want %d", tt.target, rec.Code, tt.wantStatus)
		}
		if tt.wantBody != "" && rec.Body.String() != tt.wantBody {
			t.Errorf("%s: body = %q, want %q", tt.target, rec.Body.String(), tt.wantBody)
		}
	}
}

// TestTrailingSlashRedirect maps out the redirect behaviour, which has four parts and I got
// all four wrong before running it.
//
//  1. The redirect is 307 Temporary Redirect, not 301 Moved Permanently. 307 preserves
//     the method; a 301 lets clients turn a POST into a GET, which is how the classic
//     "my POST silently became a GET" bug happens.
//  2. {$} does NOT prevent the redirect. "GET /exact/{$}" still redirects /exact to
//     /exact/. All {$} does is stop the pattern matching paths BELOW /exact/.
//  3. The redirect is method-aware. With "GET /subtree/" registered, POST /subtree gives
//     405 rather than a redirect, because POST is not registered at all. With a pattern
//     carrying no method, POST /subtree gets the 307.
//  4. It only ever ADDS a slash. Pattern "/plain" does not redirect /plain/ to /plain;
//     that is a plain 404.
func TestTrailingSlashRedirect(t *testing.T) {
	tests := []struct {
		pattern    string
		method     string
		target     string
		wantStatus int
		wantLoc    string
	}{
		// A subtree pattern redirects the slash-less form.
		{"GET /subtree/", "GET", "/subtree", http.StatusTemporaryRedirect, "/subtree/"},
		{"GET /subtree/", "GET", "/subtree/", http.StatusOK, ""},
		{"GET /subtree/", "GET", "/subtree/below", http.StatusOK, ""},

		// {$} redirects too: it restricts what is matched below, not the redirect.
		{"GET /exact/{$}", "GET", "/exact", http.StatusTemporaryRedirect, "/exact/"},
		{"GET /exact/{$}", "GET", "/exact/", http.StatusOK, ""},
		{"GET /exact/{$}", "GET", "/exact/below", http.StatusNotFound, ""},

		// The method wins over the redirect: POST is not registered, so 405.
		{"GET /subtree/", "POST", "/subtree", http.StatusMethodNotAllowed, ""},
		{"GET /exact/{$}", "POST", "/exact", http.StatusMethodNotAllowed, ""},

		// A pattern with no method redirects any method.
		{"/anymethod/", "POST", "/anymethod", http.StatusTemporaryRedirect, "/anymethod/"},
		{"/anymethod/", "GET", "/anymethod", http.StatusTemporaryRedirect, "/anymethod/"},

		// And the redirect is one-directional: it adds a slash, never removes one.
		{"GET /plain", "GET", "/plain", http.StatusOK, ""},
		{"GET /plain", "GET", "/plain/", http.StatusNotFound, ""},
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.target+" against "+tt.pattern, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc(tt.pattern, func(http.ResponseWriter, *http.Request) {})

			rec := do(t, mux, tt.method, tt.target, "")

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if got := rec.Header().Get("Location"); got != tt.wantLoc {
				t.Errorf("Location = %q, want %q", got, tt.wantLoc)
			}
		})
	}
}

// TestMethodMismatchIs405WithAllow is worth knowing because it is free and most hand-rolled
// routers get it wrong. The stdlib sets Allow for you.
func TestMethodMismatchIs405WithAllow(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /items/{id}", func(http.ResponseWriter, *http.Request) {})
	mux.HandleFunc("DELETE /items/{id}", func(http.ResponseWriter, *http.Request) {})

	rec := do(t, mux, "POST", "/items/1", "")

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}

	allow := rec.Header().Get("Allow")
	for _, method := range []string{"GET", "DELETE"} {
		if !strings.Contains(allow, method) {
			t.Errorf("Allow = %q, want it to list %s", allow, method)
		}
	}

	t.Logf("Allow: %s", allow)
}

// TestGETAlsoMatchesHEAD is a detail that costs people a confused hour. Registering "GET"
// registers HEAD as well, because HTTP says HEAD is GET without a body.
func TestGETAlsoMatchesHEAD(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /thing", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("body"))
	})

	rec := do(t, mux, "HEAD", "/thing", "")

	if rec.Code != http.StatusOK {
		t.Errorf("HEAD status = %d, want 200", rec.Code)
	}

	// httptest.ResponseRecorder does not strip the body the way a real server does, so
	// this documents the routing behaviour rather than the transport behaviour.
	t.Logf("HEAD matched the GET route; recorder body is %q (a real server strips it)",
		rec.Body.String())

	// Registering HEAD explicitly alongside GET does NOT conflict, and the HEAD pattern
	// wins for HEAD requests. "HEAD /thing" is strictly more specific than the HEAD half
	// of "GET /thing", so there is nothing to resolve.
	both := http.NewServeMux()
	both.HandleFunc("GET /thing", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("get"))
	})
	both.HandleFunc("HEAD /thing", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("head"))
	})

	if got := do(t, both, "HEAD", "/thing", "").Body.String(); got != "head" {
		t.Errorf("HEAD matched %q, want the explicit HEAD handler", got)
	}
	if got := do(t, both, "GET", "/thing", "").Body.String(); got != "get" {
		t.Errorf("GET matched %q, want the GET handler", got)
	}

	// The Allow header still lists both.
	notAllowed := do(t, both, "POST", "/thing", "")
	if got := notAllowed.Header().Get("Allow"); !strings.Contains(got, "GET") || !strings.Contains(got, "HEAD") {
		t.Errorf("Allow = %q", got)
	}
}

// TestPathValueOfAnUnknownWildcardIsEmpty: no error, no panic, just "". A typo in the
// wildcard name is therefore silent, which is the one place this API is worse than a
// router that returns a map.
func TestPathValueOfAnUnknownWildcardIsEmpty(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /items/{id}", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("id=" + r.PathValue("id") + " typo=" + r.PathValue("ID")))
	})

	got := do(t, mux, "GET", "/items/7", "").Body.String()

	if got != "id=7 typo=" {
		t.Errorf("body = %q, want %q", got, "id=7 typo=")
	}
}

// TestWildcardDoesNotMatchAcrossSegments: {id} is one segment. Matching a path is what
// {path...} is for.
func TestWildcardDoesNotMatchAcrossSegments(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /one/{seg}", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("one"))
	})

	if rec := do(t, mux, "GET", "/one/a/b", ""); rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404: {seg} must not span segments", rec.Code)
	}
}

// TestWildcardsAreURLDecoded: the value from PathValue is decoded, so a %2F in the URL arrives
// as a slash inside a single segment. That is a real source of path-traversal bugs in
// hand-rolled file servers.
func TestWildcardsAreURLDecoded(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /f/{name}", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(r.PathValue("name")))
	})

	// %2F is an encoded slash. The router treats it as part of ONE segment.
	rec := do(t, mux, "GET", "/f/a%2Fb", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Body.String(); got != "a/b" {
		t.Errorf("PathValue = %q, want %q: the value is URL-decoded", got, "a/b")
	}

	t.Log(`/f/a%2Fb matched one segment and PathValue returned "a/b". ` +
		`A handler that joins that onto a directory has a path-traversal bug; ` +
		`use filepath.Clean and check the result is still under the root.`)
}

func TestListIsJSONAndOrdered(t *testing.T) {
	mux := NewMux(NewStore())

	rec := do(t, mux, "GET", "/items/", "")

	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}

	var items []Item
	if err := json.NewDecoder(rec.Body).Decode(&items); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}

	if len(items) != 3 {
		t.Fatalf("got %d items, want 3", len(items))
	}
	for i, item := range items {
		if item.ID != i+1 {
			t.Errorf("item %d has ID %d, want %d", i, item.ID, i+1)
		}
	}
}

func TestStore(t *testing.T) {
	s := NewStore()

	if s.Len() != 3 {
		t.Errorf("Len() = %d, want 3", s.Len())
	}
	if _, ok := s.Get(99); ok {
		t.Error("Get(99) found something")
	}

	added := s.Add("chisel")
	if added.ID != 4 || added.Name != "chisel" {
		t.Errorf("Add returned %+v", added)
	}
	if got, ok := s.Get(4); !ok || got.Name != "chisel" {
		t.Errorf("Get(4) = %+v, %v", got, ok)
	}
}
