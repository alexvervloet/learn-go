package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestPprofEndpointsRespond: the explicit registration must actually serve the
// profiles, or the "do not rely on the blank import" advice costs you them.
func TestPprofEndpointsRespond(t *testing.T) {
	mux := adminMux()

	tests := []struct {
		path       string
		wantStatus int
	}{
		{"/debug/pprof/", http.StatusOK},
		{"/debug/pprof/heap?debug=1", http.StatusOK},
		{"/debug/pprof/goroutine?debug=1", http.StatusOK},
		{"/debug/pprof/allocs?debug=1", http.StatusOK},
		{"/debug/pprof/cmdline", http.StatusOK},
		{"/debug/pprof/block?debug=1", http.StatusOK},
		{"/debug/pprof/mutex?debug=1", http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tt.path, nil))

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if rec.Body.Len() == 0 {
				t.Error("the response body is empty")
			}
		})
	}
}

// TestNonPprofPathsAreNotServed: the admin mux should serve the profiles and
// nothing else, so mounting it does not accidentally expose more.
func TestNonPprofPathsAreNotServed(t *testing.T) {
	mux := adminMux()

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code == http.StatusOK {
		t.Error("the admin mux should not serve /")
	}
}

func TestExercisePprofEndpoints(t *testing.T) {
	endpoints, dump, err := exercisePprofEndpoints()
	if err != nil {
		t.Fatalf("exercise: %v", err)
	}

	if len(endpoints) != 4 {
		t.Errorf("checked %d endpoints, want 4", len(endpoints))
	}
	for _, e := range endpoints {
		if !strings.HasSuffix(e, "200") {
			t.Errorf("endpoint did not return 200: %s", e)
		}
	}

	if !strings.Contains(dump, "goroutine profile") {
		t.Errorf("the goroutine dump does not look right: %q", dump[:min(80, len(dump))])
	}
}

// TestAuthProtectsTheEndpoints is the security property: without a token the
// profiles must not be served.
func TestAuthProtectsTheEndpoints(t *testing.T) {
	withoutToken, withToken, err := authProtectsTheEndpoints()
	if err != nil {
		t.Fatalf("auth check: %v", err)
	}

	if withoutToken != http.StatusUnauthorized {
		t.Errorf("without a token: status %d, want 401", withoutToken)
	}
	if withToken != http.StatusOK {
		t.Errorf("with a token: status %d, want 200", withToken)
	}
}

// TestWrongTokenIsRejected covers the case a naive prefix check would pass.
func TestWrongTokenIsRejected(t *testing.T) {
	const token = "secret-admin-token"

	h := requireAuth(token, adminMux())

	for _, header := range []string{
		"",
		"Bearer wrong",
		"Bearer",
		token,                   // missing the Bearer prefix
		"Bearer " + token + "x", // a prefix of the right token is not the right token
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/debug/pprof/heap?debug=1", nil)
		if header != "" {
			req.Header.Set("Authorization", header)
		}

		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("header %q: status %d, want 401", header, rec.Code)
		}
	}
}

func TestUnauthorizedResponseSetsWWWAuthenticate(t *testing.T) {
	h := requireAuth("token", adminMux())

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/debug/pprof/heap", nil))

	if got := rec.Header().Get("WWW-Authenticate"); got == "" {
		t.Error("a 401 should carry WWW-Authenticate")
	}
}

func TestSecurityDocsArePresent(t *testing.T) {
	if got := securityRules(); len(got) < 5 {
		t.Errorf("expected at least 5 security rules, got %d", len(got))
	}
	if got := endpointReference(); len(got) < 6 {
		t.Errorf("expected at least 6 documented endpoints, got %d", len(got))
	}
	if got := profilesThatNeedEnabling(); len(got) < 3 {
		t.Errorf("expected at least 3 notes, got %d", len(got))
	}
	if got := separateAdminListener(); len(got) < 3 {
		t.Errorf("expected at least 3 notes on the listener, got %d", len(got))
	}
}

// TestSecurityRulesMentionDefaultServeMux: the blank-import trap is the one
// that has bitten real services, so it must be in the list.
func TestSecurityRulesMentionDefaultServeMux(t *testing.T) {
	joined := strings.Join(securityRules(), " ")

	if !strings.Contains(joined, "DefaultServeMux") {
		t.Error("the blank-import / DefaultServeMux trap should be documented")
	}
}
