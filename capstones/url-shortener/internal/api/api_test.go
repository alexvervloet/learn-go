package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/alexvervloet/learn-go/capstones/url-shortener/internal/apitest"
	"github.com/alexvervloet/learn-go/capstones/url-shortener/internal/auth"
	"github.com/alexvervloet/learn-go/capstones/url-shortener/internal/tasks"
	"github.com/stretchr/testify/require"
)

const goodPassword = "correct horse battery staple"

func harness(t *testing.T) *apitest.Harness {
	t.Helper()

	return apitest.New(t, apitest.HarnessOptions{})
}

// create makes a short link and returns the response body.
func create(t *testing.T, h *apitest.Harness, token string, body map[string]any) map[string]any {
	t.Helper()

	resp := h.Do(t, http.MethodPost, "/api/v1/urls", token, body)

	var out map[string]any

	resp.JSON(t, &out)
	require.Equal(t, http.StatusCreated, resp.Status, "%v", out)

	return out
}

// TestHealthChecksTheDatabase is the claim that a health endpoint should make.
func TestHealthChecksTheDatabase(t *testing.T) {
	h := harness(t)

	resp := h.Do(t, http.MethodGet, "/healthz", "", nil)

	var body map[string]string

	resp.JSON(t, &body)
	require.Equal(t, http.StatusOK, resp.Status)
	require.Equal(t, "ok", body["status"])
}

// TestRegisterAndLogin is the happy path through auth.
func TestRegisterAndLogin(t *testing.T) {
	h := harness(t)

	token := h.Register(t, "alex@example.com", goodPassword)
	require.NotEmpty(t, token)

	claims, err := auth.Parse(h.Secret, token)
	require.NoError(t, err)
	require.Equal(t, "alex@example.com", claims.Email)

	// Logging in gives a second, equally valid token. There is no session to invalidate, which is the
	// stateless-token trade: a user can be logged in from five places and logging out of one changes nothing.
	resp := h.Do(t, http.MethodPost, "/api/v1/login", "", map[string]string{
		"email":    "alex@example.com",
		"password": goodPassword,
	})

	var login map[string]any

	resp.JSON(t, &login)
	require.Equal(t, http.StatusOK, resp.Status)
	require.NotEmpty(t, login["token"])
}

// TestEmailIsCaseInsensitive is the CITEXT column doing its job.
func TestEmailIsCaseInsensitive(t *testing.T) {
	h := harness(t)

	h.Register(t, "alex@example.com", goodPassword)

	// A different case is the SAME account, so registering again is a conflict.
	resp := h.Do(t, http.MethodPost, "/api/v1/register", "", map[string]string{
		"email":    "ALEX@Example.COM",
		"password": goodPassword,
	})

	var problem map[string]any

	resp.JSON(t, &problem)
	require.Equal(t, http.StatusConflict, resp.Status)

	// And logging in with the other case works, without the handler lowercasing anything.
	resp = h.Do(t, http.MethodPost, "/api/v1/login", "", map[string]string{
		"email":    "Alex@EXAMPLE.com",
		"password": goodPassword,
	})

	var login map[string]any

	resp.JSON(t, &login)
	require.Equal(t, http.StatusOK, resp.Status, "%v", login)
}

// TestLoginFailuresAreIndistinguishable is the enumeration defence, at the HTTP layer.
func TestLoginFailuresAreIndistinguishable(t *testing.T) {
	h := harness(t)

	h.Register(t, "alex@example.com", goodPassword)

	noSuchUser := h.Do(t, http.MethodPost, "/api/v1/login", "", map[string]string{
		"email": "nobody@example.com", "password": goodPassword,
	})

	var a map[string]any

	noSuchUser.JSON(t, &a)

	wrongPassword := h.Do(t, http.MethodPost, "/api/v1/login", "", map[string]string{
		"email": "alex@example.com", "password": "a different password here",
	})

	var b map[string]any

	wrongPassword.JSON(t, &b)

	require.Equal(t, http.StatusUnauthorized, noSuchUser.Status)
	require.Equal(t, http.StatusUnauthorized, wrongPassword.Status)
	require.Equal(t, a, b, "the two failures are byte-identical, so a caller cannot enumerate accounts")
}

// TestErrorsAreProblemJSON pins the error format.
func TestErrorsAreProblemJSON(t *testing.T) {
	h := harness(t)

	resp := h.Do(t, http.MethodPost, "/api/v1/register", "", map[string]string{
		"email": "not-an-email", "password": goodPassword,
	})

	require.Equal(t, "application/problem+json", resp.Header.Get("Content-Type"),
		"a proxy or client library can tell an error body from a success body without parsing it")

	var problem struct {
		Type   string `json:"type"`
		Title  string `json:"title"`
		Status int    `json:"status"`
		Detail string `json:"detail"`
	}

	resp.JSON(t, &problem)
	require.Equal(t, http.StatusBadRequest, resp.Status)
	require.Equal(t, http.StatusBadRequest, problem.Status, "the status is in the body as well as the line")
	require.NotEmpty(t, problem.Type, "the type URI is the stable thing; the title is prose")
}

// TestUnauthorizedSendsWWWAuthenticate is the header nearly everyone omits.
func TestUnauthorizedSendsWWWAuthenticate(t *testing.T) {
	h := harness(t)

	for _, token := range []string{"", "not-a-token", "garbage.garbage.garbage"} {
		resp := h.Do(t, http.MethodGet, "/api/v1/urls", token, nil)

		var problem map[string]any

		resp.JSON(t, &problem)

		require.Equal(t, http.StatusUnauthorized, resp.Status, "token %q", token)
		require.Contains(t, resp.Header.Get("WWW-Authenticate"), "Bearer",
			"RFC 7235 requires it on a 401, and it is what tells a client how to authenticate")
	}
}

// TestTheBearerSchemeIsCaseInsensitive is the RFC being followed.
func TestTheBearerSchemeIsCaseInsensitive(t *testing.T) {
	h := harness(t)

	token := h.Register(t, "alex@example.com", goodPassword)

	resp := h.DoWith(t, http.MethodGet, "/api/v1/urls", nil, nil, func(req *http.Request) {
		// Lowercase "bearer", which is what several HTTP clients send.
		req.Header.Set("Authorization", "bearer "+token)
	})

	require.Equal(t, http.StatusOK, resp.Status, "RFC 7235 says the scheme is case-insensitive")
}

// TestCreateAndRedirect is the core of the product.
func TestCreateAndRedirect(t *testing.T) {
	h := harness(t)

	token := h.Register(t, "alex@example.com", goodPassword)

	body := create(t, h, token, map[string]any{"target": "https://example.com/a/very/long/path"})

	slug, _ := body["slug"].(string)
	require.NotEmpty(t, slug)
	require.Equal(t, h.BaseURL+"/"+slug, body["short_url"],
		"the short URL uses the configured base, not the request's Host, because behind a proxy those differ")

	resp := h.Do(t, http.MethodGet, "/"+slug, "", nil)

	require.Equal(t, http.StatusFound, resp.Status,
		"302, not 301: a permanent redirect is cached by browsers and cannot be changed or deleted")
	require.Equal(t, "https://example.com/a/very/long/path", resp.Header.Get("Location"))
}

// TestTheRedirectEnqueuesAClick is the handler's actual contract.
func TestTheRedirectEnqueuesAClick(t *testing.T) {
	h := harness(t)

	token := h.Register(t, "alex@example.com", goodPassword)
	body := create(t, h, token, map[string]any{"target": "https://example.com/"})

	slug, _ := body["slug"].(string)

	h.Enqueuer.Reset()

	resp := h.DoWith(t, http.MethodGet, "/"+slug, nil, nil, func(req *http.Request) {
		req.Header.Set("Referer", "https://news.example/story")
		req.Header.Set("User-Agent", "test-agent/1.0")
	})

	require.Equal(t, http.StatusFound, resp.Status)

	enqueued := h.Enqueuer.Tasks()
	require.Len(t, enqueued, 1)
	require.Equal(t, tasks.TypeRecordClick, enqueued[0].Type())

	var payload tasks.RecordClickPayload

	require.NoError(t, json.Unmarshal(enqueued[0].Payload(), &payload))
	require.Equal(t, "https://news.example/story", payload.Referrer,
		"the header is spelled Referer, with the historical typo; asking for Referrer returns empty")
	require.Equal(t, "test-agent/1.0", payload.UserAgent)
	require.NotZero(t, payload.URLID, "the id, not the slug: a slug can be deleted and re-created")
}

// TestAFailedEnqueueDoesNotBreakTheRedirect is the priority, stated as a test.
func TestAFailedEnqueueDoesNotBreakTheRedirect(t *testing.T) {
	h := harness(t)

	token := h.Register(t, "alex@example.com", goodPassword)
	body := create(t, h, token, map[string]any{"target": "https://example.com/"})

	slug, _ := body["slug"].(string)

	h.Enqueuer.Err = fmt.Errorf("redis is down")

	resp := h.Do(t, http.MethodGet, "/"+slug, "", nil)

	require.Equal(t, http.StatusFound, resp.Status,
		"a missing click is a wrong statistic; a failed redirect is a broken link")
	require.Equal(t, "https://example.com/", resp.Header.Get("Location"))
}

// TestCustomSlug covers the second creation path.
func TestCustomSlug(t *testing.T) {
	h := harness(t)

	token := h.Register(t, "alex@example.com", goodPassword)

	body := create(t, h, token, map[string]any{
		"target": "https://example.com/docs",
		"slug":   "my-docs",
	})

	require.Equal(t, "my-docs", body["slug"])

	// A second claim on the same slug is a conflict, and the constraint is what catches it rather than a
	// check-then-insert that races.
	resp := h.Do(t, http.MethodPost, "/api/v1/urls", token, map[string]any{
		"target": "https://elsewhere.example/",
		"slug":   "my-docs",
	})

	var problem map[string]any

	resp.JSON(t, &problem)
	require.Equal(t, http.StatusConflict, resp.Status)
}

// TestReservedSlugsCannotShadowARoute is the route-collision guard, end to end.
func TestReservedSlugsCannotShadowARoute(t *testing.T) {
	h := harness(t)

	token := h.Register(t, "alex@example.com", goodPassword)

	for _, slug := range []string{"api", "healthz", "metrics"} {
		resp := h.Do(t, http.MethodPost, "/api/v1/urls", token, map[string]any{
			"target": "https://example.com/", "slug": slug,
		})

		var problem map[string]any

		resp.JSON(t, &problem)
		require.Equal(t, http.StatusBadRequest, resp.Status, "slug %q", slug)
	}

	// And the routes still work, which is what the guard protects.
	resp := h.Do(t, http.MethodGet, "/healthz", "", nil)

	require.Equal(t, http.StatusOK, resp.Status)
}

// TestTargetSchemeIsAnAllowList is the security check.
func TestTargetSchemeIsAnAllowList(t *testing.T) {
	h := harness(t)

	token := h.Register(t, "alex@example.com", goodPassword)

	dangerous := []string{
		"javascript:alert(document.cookie)",
		"JaVaScRiPt:alert(1)",
		"data:text/html,<script>alert(1)</script>",
		"file:///etc/passwd",
		"vbscript:msgbox(1)",
		"ftp://example.com/x",
		"not a url at all",
		"//example.com/protocol-relative",
	}

	for _, target := range dangerous {
		resp := h.Do(t, http.MethodPost, "/api/v1/urls", token, map[string]any{"target": target})

		var problem map[string]any

		resp.JSON(t, &problem)
		require.Equal(t, http.StatusBadRequest, resp.Status,
			"a deny-list would miss at least one of these: %q", target)
	}
}

// TestUnknownFieldsAreRejected is why DisallowUnknownFields is on.
func TestUnknownFieldsAreRejected(t *testing.T) {
	h := harness(t)

	token := h.Register(t, "alex@example.com", goodPassword)

	resp := h.Do(t, http.MethodPost, "/api/v1/urls", token, map[string]any{
		"target": "https://example.com/",
		"slugg":  "typo",
	})

	var problem map[string]any

	resp.JSON(t, &problem)
	require.Equal(t, http.StatusBadRequest, resp.Status,
		"a typo in a client field name is caught at the first request rather than in production")
}

// TestAMissingSlugIs404 covers the default case.
func TestAMissingSlugIs404(t *testing.T) {
	h := harness(t)

	resp := h.Do(t, http.MethodGet, "/nothing-here", "", nil)

	var problem map[string]any

	resp.JSON(t, &problem)
	require.Equal(t, http.StatusNotFound, resp.Status)
}

// TestAnExpiredLinkIs410 is the distinction that matters to a crawler.
func TestAnExpiredLinkIs410(t *testing.T) {
	h := harness(t)

	token := h.Register(t, "alex@example.com", goodPassword)

	// Created with a future expiry, because the API refuses a past one, and then expired by moving the row.
	// Sleeping for a second would work and would make the test a second slower for nothing.
	body := create(t, h, token, map[string]any{
		"target":     "https://example.com/",
		"expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
	})

	slug, _ := body["slug"].(string)

	_, err := h.Store.Pool().Exec(t.Context(),
		`UPDATE urls SET expires_at = now() - interval '1 minute' WHERE slug = $1`, slug)
	require.NoError(t, err)

	resp := h.Do(t, http.MethodGet, "/"+slug, "", nil)

	var problem map[string]any

	resp.JSON(t, &problem)
	require.Equal(t, http.StatusGone, resp.Status,
		"410 means this existed and is finished; 404 means it never existed")
}

// TestAPastExpiryIsRejected covers the validation.
func TestAPastExpiryIsRejected(t *testing.T) {
	h := harness(t)

	token := h.Register(t, "alex@example.com", goodPassword)

	resp := h.Do(t, http.MethodPost, "/api/v1/urls", token, map[string]any{
		"target":     "https://example.com/",
		"expires_at": time.Now().Add(-time.Hour).UTC().Format(time.RFC3339),
	})

	var problem map[string]any

	resp.JSON(t, &problem)
	require.Equal(t, http.StatusBadRequest, resp.Status)
}

// TestListPagesWithAKeyset is the pagination contract.
func TestListPagesWithAKeyset(t *testing.T) {
	h := harness(t)

	token := h.Register(t, "alex@example.com", goodPassword)

	const total = 25

	for i := range total {
		create(t, h, token, map[string]any{"target": fmt.Sprintf("https://example.com/%d", i)})
	}

	var seen []string

	cursor := ""
	pages := 0

	for {
		path := "/api/v1/urls?limit=10"
		if cursor != "" {
			path += "&after=" + cursor
		}

		resp := h.Do(t, http.MethodGet, path, token, nil)

		var page struct {
			Items []map[string]any `json:"items"`
			Next  string           `json:"next"`
		}

		resp.JSON(t, &page)
		require.Equal(t, http.StatusOK, resp.Status)

		pages++

		for _, item := range page.Items {
			slug, _ := item["slug"].(string)
			seen = append(seen, slug)
		}

		if page.Next == "" {
			break
		}

		cursor = page.Next

		require.Less(t, pages, 10, "the loop should have terminated")
	}

	require.Equal(t, 3, pages, "25 items at 10 per page")
	require.Len(t, seen, total)

	// Every slug exactly once. That is the property OFFSET cannot give: an insert between two page requests
	// shifts the window and a row is seen twice or skipped.
	unique := map[string]bool{}
	for _, slug := range seen {
		require.False(t, unique[slug], "%q appeared twice", slug)
		unique[slug] = true
	}

	require.Len(t, unique, total)
}

// TestAnInsertBetweenPagesDoesNotShiftTheWindow is the claim behind keyset pagination.
func TestAnInsertBetweenPagesDoesNotShiftTheWindow(t *testing.T) {
	h := harness(t)

	token := h.Register(t, "alex@example.com", goodPassword)

	for i := range 10 {
		create(t, h, token, map[string]any{"target": fmt.Sprintf("https://example.com/%d", i)})
	}

	resp := h.Do(t, http.MethodGet, "/api/v1/urls?limit=5", token, nil)

	var first struct {
		Items []map[string]any `json:"items"`
		Next  string           `json:"next"`
	}

	resp.JSON(t, &first)
	require.Len(t, first.Items, 5)
	require.NotEmpty(t, first.Next)

	// A new URL arrives. Ordered newest-first, it goes at the TOP, which with OFFSET 5 would push one row
	// from page one down into page two and the client would see it twice.
	create(t, h, token, map[string]any{"target": "https://example.com/newest"})

	resp = h.Do(t, http.MethodGet, "/api/v1/urls?limit=5&after="+first.Next, token, nil)

	var second struct {
		Items []map[string]any `json:"items"`
	}

	resp.JSON(t, &second)
	require.Len(t, second.Items, 5)

	firstSlugs := map[string]bool{}
	for _, item := range first.Items {
		slug, _ := item["slug"].(string)
		firstSlugs[slug] = true
	}

	for _, item := range second.Items {
		slug, _ := item["slug"].(string)
		require.False(t, firstSlugs[slug], "%q appeared on both pages", slug)
	}
}

// TestAnotherUsersURLIsA404 is the ownership boundary.
func TestAnotherUsersURLIsA404(t *testing.T) {
	h := harness(t)

	alex := h.Register(t, "alex@example.com", goodPassword)
	sam := h.Register(t, "sam@example.com", goodPassword)

	body := create(t, h, alex, map[string]any{"target": "https://example.com/private"})
	slug, _ := body["slug"].(string)

	// Sam asking for stats gets a 404, not a 403. A 403 would confirm the slug exists.
	resp := h.Do(t, http.MethodGet, "/api/v1/urls/"+slug+"/stats", sam, nil)

	var problem map[string]any

	resp.JSON(t, &problem)
	require.Equal(t, http.StatusNotFound, resp.Status)

	// And Sam cannot delete it.
	resp = h.Do(t, http.MethodDelete, "/api/v1/urls/"+slug, sam, nil)

	resp.JSON(t, &problem)
	require.Equal(t, http.StatusNotFound, resp.Status)

	// The redirect still works, because it is public.
	redirect := h.Do(t, http.MethodGet, "/"+slug, "", nil)

	require.Equal(t, http.StatusFound, redirect.Status)
}

// TestDeleteRemovesTheLink covers the last handler.
func TestDeleteRemovesTheLink(t *testing.T) {
	h := harness(t)

	token := h.Register(t, "alex@example.com", goodPassword)
	body := create(t, h, token, map[string]any{"target": "https://example.com/"})
	slug, _ := body["slug"].(string)

	resp := h.Do(t, http.MethodDelete, "/api/v1/urls/"+slug, token, nil)

	require.Equal(t, http.StatusNoContent, resp.Status)

	gone := h.Do(t, http.MethodGet, "/"+slug, "", nil)

	var problem map[string]any

	gone.JSON(t, &problem)
	require.Equal(t, http.StatusNotFound, gone.Status)
}

// TestAnExpiredTokenIsRejectedByTheAPI ties the TTL to the HTTP layer.
func TestAnExpiredTokenIsRejectedByTheAPI(t *testing.T) {
	h := apitest.New(t, apitest.HarnessOptions{TokenTTL: time.Millisecond})

	token := h.Register(t, "alex@example.com", goodPassword)

	// The TTL is a millisecond, so waiting out a few is enough. This is the one place a sleep is the subject.
	time.Sleep(20 * time.Millisecond)

	resp := h.Do(t, http.MethodGet, "/api/v1/urls", token, nil)

	var problem map[string]any

	resp.JSON(t, &problem)
	require.Equal(t, http.StatusUnauthorized, resp.Status)
}

// TestMethodNotAllowed is Go 1.22 routing doing the work.
//
// The method is part of the pattern, so a POST to a GET-only path is a 405 with an Allow header, from the
// standard library, with no code in this service.
func TestMethodNotAllowed(t *testing.T) {
	h := harness(t)

	resp := h.Do(t, http.MethodDelete, "/healthz", "", nil)

	require.Equal(t, http.StatusMethodNotAllowed, resp.Status)
	require.Contains(t, resp.Header.Get("Allow"), "GET")
}

// TestALargeBodyIsRefused covers MaxBytesReader.
func TestALargeBodyIsRefused(t *testing.T) {
	h := harness(t)

	token := h.Register(t, "alex@example.com", goodPassword)

	// Two megabytes of target, against a one megabyte limit.
	huge := make([]byte, 2<<20)
	for i := range huge {
		huge[i] = 'a'
	}

	resp := h.Do(t, http.MethodPost, "/api/v1/urls", token, map[string]any{"target": string(huge)})

	var problem map[string]any

	resp.JSON(t, &problem)
	require.Equal(t, http.StatusBadRequest, resp.Status,
		"without a limit, a slow infinite body holds a goroutine for as long as the client likes")
}
