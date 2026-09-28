package api_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/alexvervloet/learn-go/capstones/bookmark-manager/internal/apitest"
	"github.com/alexvervloet/learn-go/capstones/bookmark-manager/internal/auth"
	"github.com/stretchr/testify/require"
)

const goodPassword = "correct horse battery staple"

func harness(t *testing.T) *apitest.Harness {
	t.Helper()

	return apitest.New(t, apitest.HarnessOptions{})
}

// refresh exchanges a refresh token and returns the response.
func refresh(t *testing.T, h *apitest.Harness, token string) *apitest.Response {
	t.Helper()

	return h.Do(t, http.MethodPost, "/api/v1/refresh", "", map[string]string{"refresh_token": token})
}

// TestRegisterReturnsBothTokens is the shape of the credential response.
func TestRegisterReturnsBothTokens(t *testing.T) {
	h := harness(t)

	resp := h.Do(t, http.MethodPost, "/api/v1/register", "", map[string]string{
		"email": "alex@example.com", "password": goodPassword,
	})
	require.Equal(t, http.StatusCreated, resp.Status)

	var body struct {
		AccessToken    string    `json:"access_token"`
		RefreshToken   string    `json:"refresh_token"`
		AccessExpires  time.Time `json:"access_expires_at"`
		RefreshExpires time.Time `json:"refresh_expires_at"`
		TokenType      string    `json:"token_type"`
	}

	resp.JSON(t, &body)

	require.NotEmpty(t, body.AccessToken)
	require.NotEmpty(t, body.RefreshToken)
	require.Equal(t, "Bearer", body.TokenType, "RFC 6750: the scheme tells a client how to send it")
	require.True(t, body.RefreshExpires.After(body.AccessExpires),
		"the refresh token outlives the access token, or refreshing is pointless")
}

// TestTheRefreshTokenIsNotAJWT is a design point worth asserting.
//
// An access token is a JWT because it is verified without a lookup. A refresh token is verified BY a lookup, so
// it carries no claims and gains nothing from being signed. It is 32 random bytes, and anything a JWT would
// have carried is a column on the row instead, where it can change.
func TestTheRefreshTokenIsNotAJWT(t *testing.T) {
	h := harness(t)

	tokens := h.Register(t, "alex@example.com", goodPassword)

	require.Contains(t, tokens.Access, ".", "a JWT has three dot-separated segments")
	require.NotContains(t, tokens.Refresh, ".", "the refresh token is opaque")
	require.Len(t, tokens.Refresh, 43, "32 bytes in unpadded base64url")
}

// TestRefreshRotatesTheToken is the core of the scheme.
func TestRefreshRotatesTheToken(t *testing.T) {
	h := harness(t)

	first := h.Register(t, "alex@example.com", goodPassword)

	resp := refresh(t, h, first.Refresh)
	require.Equal(t, http.StatusOK, resp.Status, "%s", resp.Body)

	var second apitest.Tokens

	resp.JSON(t, &second)

	require.NotEqual(t, first.Refresh, second.Refresh, "every refresh issues a new token")
	require.NotEqual(t, first.Access, second.Access)

	// The new one works.
	third := refresh(t, h, second.Refresh)
	require.Equal(t, http.StatusOK, third.Status)
}

// TestTheOldRefreshTokenStopsWorking is what rotation means.
func TestTheOldRefreshTokenStopsWorking(t *testing.T) {
	h := harness(t)

	first := h.Register(t, "alex@example.com", goodPassword)

	resp := refresh(t, h, first.Refresh)
	require.Equal(t, http.StatusOK, resp.Status)

	reused := refresh(t, h, first.Refresh)
	require.Equal(t, http.StatusUnauthorized, reused.Status, "a refresh token is valid exactly once")
}

// TestReuseRevokesTheWholeFamily is the attack detection.
//
// A second use of a consumed token means a copy exists somewhere it should not. The legitimate client threw its
// copy away when it received the replacement, so the only explanations are theft and a retried request whose
// response was lost.
//
// They are indistinguishable from the server, so both get the safe answer: revoke the chain. The honest user
// logs in again; an attacker gets one request.
func TestReuseRevokesTheWholeFamily(t *testing.T) {
	h := harness(t)

	first := h.Register(t, "alex@example.com", goodPassword)

	// A legitimate refresh. The client now holds `second`.
	resp := refresh(t, h, first.Refresh)
	require.Equal(t, http.StatusOK, resp.Status)

	var second apitest.Tokens

	resp.JSON(t, &second)

	// The client's own token still works at this point.
	probe := refresh(t, h, second.Refresh)
	require.Equal(t, http.StatusOK, probe.Status)

	var third apitest.Tokens

	probe.JSON(t, &third)

	// Now an attacker replays the FIRST token, which was consumed two rotations ago.
	stolen := refresh(t, h, first.Refresh)
	require.Equal(t, http.StatusUnauthorized, stolen.Status)

	var problem struct {
		Detail string `json:"detail"`
	}

	stolen.JSON(t, &problem)
	require.Contains(t, problem.Detail, "revoked")

	// And the legitimate client's CURRENT token is dead too, because the whole family went. That is the
	// point: the attacker cannot keep the session, and the real user notices immediately rather than in a
	// month.
	afterRevoke := refresh(t, h, third.Refresh)
	require.Equal(t, http.StatusUnauthorized, afterRevoke.Status,
		"revoking the family ends the session for everyone holding a token from it")
}

// TestRevokingAFamilyDoesNotTouchOtherSessions is why the family is a column.
func TestRevokingAFamilyDoesNotTouchOtherSessions(t *testing.T) {
	h := harness(t)

	laptop := h.Register(t, "alex@example.com", goodPassword)

	// A second login: a phone. Its own family.
	loginResp := h.Do(t, http.MethodPost, "/api/v1/login", "", map[string]string{
		"email": "alex@example.com", "password": goodPassword,
	})
	require.Equal(t, http.StatusOK, loginResp.Status)

	var phone apitest.Tokens

	loginResp.JSON(t, &phone)

	// Rotate the laptop's token, then replay the old one to trigger the revocation.
	rotated := refresh(t, h, laptop.Refresh)
	require.Equal(t, http.StatusOK, rotated.Status)

	stolen := refresh(t, h, laptop.Refresh)
	require.Equal(t, http.StatusUnauthorized, stolen.Status)

	// The phone is unaffected.
	phoneRefresh := refresh(t, h, phone.Refresh)
	require.Equal(t, http.StatusOK, phoneRefresh.Status,
		"a compromised session is revoked; the user's other devices keep working")
}

// TestLogoutRevokesOneToken is the ordinary path.
func TestLogoutRevokesOneToken(t *testing.T) {
	h := harness(t)

	tokens := h.Register(t, "alex@example.com", goodPassword)

	out := h.Do(t, http.MethodPost, "/api/v1/logout", "", map[string]string{"refresh_token": tokens.Refresh})
	require.Equal(t, http.StatusNoContent, out.Status)

	after := refresh(t, h, tokens.Refresh)
	require.Equal(t, http.StatusUnauthorized, after.Status)

	// The ACCESS token still works until it expires. That is the price of a stateless access token, and it is
	// why its TTL is fifteen minutes rather than a day.
	list := h.Do(t, http.MethodGet, "/api/v1/bookmarks", tokens.Access, nil)
	require.Equal(t, http.StatusOK, list.Status,
		"logout ends the session at the next refresh, not at the next request")
}

// TestLogoutIsSilentAboutUnknownTokens closes an oracle.
func TestLogoutIsSilentAboutUnknownTokens(t *testing.T) {
	h := harness(t)

	for _, token := range []string{"", "not-a-token", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"} {
		resp := h.Do(t, http.MethodPost, "/api/v1/logout", "", map[string]string{"refresh_token": token})
		require.Equal(t, http.StatusNoContent, resp.Status,
			"a logout that reports 'no such token' tells a caller which tokens are valid")
	}
}

// TestAnInvalidAccessTokenSaysWhy is the RFC 6750 header a client acts on.
func TestAnInvalidAccessTokenSaysWhy(t *testing.T) {
	h := harness(t)

	resp := h.Do(t, http.MethodGet, "/api/v1/bookmarks", "garbage.garbage.garbage", nil)
	require.Equal(t, http.StatusUnauthorized, resp.Status)
	require.Contains(t, resp.Header.Get("WWW-Authenticate"), `error="invalid_token"`,
		"this is how a client knows to refresh rather than to ask the user to log in again")

	// No token at all is a different message: nothing to refresh.
	none := h.Do(t, http.MethodGet, "/api/v1/bookmarks", "", nil)
	require.Equal(t, http.StatusUnauthorized, none.Status)
	require.Contains(t, none.Header.Get("WWW-Authenticate"), "Bearer")
	require.NotContains(t, none.Header.Get("WWW-Authenticate"), "invalid_token")
}

// TestAnExpiredAccessTokenCanBeRefreshed is the whole workflow.
func TestAnExpiredAccessTokenCanBeRefreshed(t *testing.T) {
	// One second, which is the FLOOR and not a choice.
	//
	// A JWT's exp is a NumericDate, and jwt/v5 truncates every date to jwt.TimePrecision, which is one second.
	// A 300ms TTL therefore truncates to zero: exp equals iat and the token is expired the moment it is
	// signed. The first two versions of this test used 50ms and 300ms and both failed on working code, with
	// the token expiring before the test could look at it.
	//
	// auth.IssueAccess now refuses anything below a second rather than minting a dead token.
	h := apitest.New(t, apitest.HarnessOptions{AccessTTL: time.Second})

	tokens := h.Register(t, "alex@example.com", goodPassword)

	// The one place a sleep is the subject rather than a smell. 1.5s against a 1s TTL, so the expiry has
	// definitely passed and the token issued by the refresh has a full second to be looked at.
	time.Sleep(1500 * time.Millisecond)

	expired := h.Do(t, http.MethodGet, "/api/v1/bookmarks", tokens.Access, nil)
	require.Equal(t, http.StatusUnauthorized, expired.Status)

	resp := refresh(t, h, tokens.Refresh)
	require.Equal(t, http.StatusOK, resp.Status, "the refresh token is still good: %s", resp.Body)

	var fresh apitest.Tokens

	resp.JSON(t, &fresh)
	require.NotEqual(t, tokens.Access, fresh.Access)

	// The new token is checked by PARSING it rather than by making another request with it.
	//
	// The first version made the request, and it failed intermittently: the access TTL here is 50ms, which is
	// the point of the test, and a token with 50ms of life does not reliably survive a round trip on a loaded
	// machine. Parsing is the same assertion without the race.
	claims, err := auth.ParseAccess(h.Secret, fresh.Access)
	require.NoError(t, err, "the refresh issued a valid, unexpired access token")

	id, err := claims.UserID()
	require.NoError(t, err)
	require.Positive(t, id)
}

// ---------------------------------------------------------------------------
// Rate limiting
// ---------------------------------------------------------------------------

// TestTheLoginEndpointIsRateLimited covers the middleware end to end.
func TestTheLoginEndpointIsRateLimited(t *testing.T) {
	h := apitest.New(t, apitest.HarnessOptions{LoginLimit: 3, LoginWindow: time.Minute})

	h.Register(t, "alex@example.com", goodPassword)

	// Register and login share ONE bucket, so the registration above used one of the three and two logins
	// remain. Separate buckets per path would allow three of each, which is six bcrypt calls against a limit
	// that says three.
	for i := range 2 {
		resp := h.Do(t, http.MethodPost, "/api/v1/login", "", map[string]string{
			"email": "alex@example.com", "password": goodPassword,
		})
		require.Equal(t, http.StatusOK, resp.Status, "attempt %d", i)
	}

	refused := h.Do(t, http.MethodPost, "/api/v1/login", "", map[string]string{
		"email": "alex@example.com", "password": goodPassword,
	})
	require.Equal(t, http.StatusTooManyRequests, refused.Status)

	// Retry-After, which every client library understands, and at least 1: a 0 tells a client to retry now.
	retry := refused.Header.Get("Retry-After")
	require.NotEmpty(t, retry)
	require.NotEqual(t, "0", retry)

	require.Equal(t, "3", refused.Header.Get("RateLimit-Limit"))
	require.Equal(t, "0", refused.Header.Get("RateLimit-Remaining"))
}

// TestRateLimitHeadersAppearOnSuccessToo is what lets a client pace itself.
func TestRateLimitHeadersAppearOnSuccessToo(t *testing.T) {
	h := apitest.New(t, apitest.HarnessOptions{LoginLimit: 10, LoginWindow: time.Minute})

	resp := h.Do(t, http.MethodPost, "/api/v1/register", "", map[string]string{
		"email": "alex@example.com", "password": goodPassword,
	})
	require.Equal(t, http.StatusCreated, resp.Status)

	require.Equal(t, "10", resp.Header.Get("RateLimit-Limit"))
	require.Equal(t, "9", resp.Header.Get("RateLimit-Remaining"),
		"a client that only sees headers on a 429 has already been refused once")
}

// TestReadsAreNotRateLimited is the decision, stated.
func TestReadsAreNotRateLimited(t *testing.T) {
	h := apitest.New(t, apitest.HarnessOptions{LoginLimit: 2, LoginWindow: time.Minute})

	tokens := h.Register(t, "alex@example.com", goodPassword)

	for i := range 20 {
		resp := h.Do(t, http.MethodGet, "/api/v1/bookmarks", tokens.Access, nil)
		require.Equal(t, http.StatusOK, resp.Status, "read %d", i)
		require.Empty(t, resp.Header.Get("RateLimit-Limit"),
			"the credential limit is a global one on a bcrypt endpoint; a read limit is a different feature "+
				"with a different key")
	}
}

// ---------------------------------------------------------------------------
// Bookmarks
// ---------------------------------------------------------------------------

func createBookmark(t *testing.T, h *apitest.Harness, token string, body map[string]any) map[string]any {
	t.Helper()

	resp := h.Do(t, http.MethodPost, "/api/v1/bookmarks", token, body)

	var out map[string]any

	resp.JSON(t, &out)
	require.Equal(t, http.StatusCreated, resp.Status, "%v", out)

	return out
}

// TestCreateAndListBookmarks is the core of the product.
func TestCreateAndListBookmarks(t *testing.T) {
	h := harness(t)

	tokens := h.Register(t, "alex@example.com", goodPassword)

	created := createBookmark(t, h, tokens.Access, map[string]any{
		"url":         "https://go.dev/doc/effective_go",
		"title":       "Effective Go",
		"description": "how to write clear, idiomatic Go",
		"tags":        []string{"go", "style"},
	})

	require.Equal(t, "Effective Go", created["title"])
	require.ElementsMatch(t, []any{"go", "style"}, created["tags"])

	resp := h.Do(t, http.MethodGet, "/api/v1/bookmarks", tokens.Access, nil)
	require.Equal(t, http.StatusOK, resp.Status)

	var list struct {
		Items []map[string]any `json:"items"`
	}

	resp.JSON(t, &list)
	require.Len(t, list.Items, 1)
	require.ElementsMatch(t, []any{"go", "style"}, list.Items[0]["tags"])
}

// TestABookmarkWithNoTagsHasAnEmptyArray is the null-versus-empty rule.
func TestABookmarkWithNoTagsHasAnEmptyArray(t *testing.T) {
	h := harness(t)

	tokens := h.Register(t, "alex@example.com", goodPassword)

	created := createBookmark(t, h, tokens.Access, map[string]any{
		"url": "https://go.dev/", "title": "Go",
	})

	tags, ok := created["tags"].([]any)
	require.True(t, ok, "tags is an array, not null: a client iterating it should not need a null check")
	require.Empty(t, tags)
}

// TestTheURLSchemeIsAnAllowList is the security check.
func TestTheURLSchemeIsAnAllowList(t *testing.T) {
	h := harness(t)

	tokens := h.Register(t, "alex@example.com", goodPassword)

	for _, target := range []string{
		"javascript:alert(document.cookie)",
		"JaVaScRiPt:alert(1)",
		"data:text/html,<script>alert(1)</script>",
		"file:///etc/passwd",
		"vbscript:msgbox(1)",
		"//example.com/protocol-relative",
		"not a url at all",
	} {
		resp := h.Do(t, http.MethodPost, "/api/v1/bookmarks", tokens.Access, map[string]any{
			"url": target, "title": "x",
		})
		require.Equal(t, http.StatusBadRequest, resp.Status,
			"a deny-list would miss at least one of these: %q", target)
	}
}

// TestSavingTheSameURLTwiceIsAConflict covers the composite unique.
func TestSavingTheSameURLTwiceIsAConflict(t *testing.T) {
	h := harness(t)

	tokens := h.Register(t, "alex@example.com", goodPassword)

	createBookmark(t, h, tokens.Access, map[string]any{"url": "https://go.dev/", "title": "Go"})

	resp := h.Do(t, http.MethodPost, "/api/v1/bookmarks", tokens.Access, map[string]any{
		"url": "https://go.dev/", "title": "Go again",
	})
	require.Equal(t, http.StatusConflict, resp.Status)
}

// TestAnotherUsersBookmarkIsA404 is the ownership boundary.
func TestAnotherUsersBookmarkIsA404(t *testing.T) {
	h := harness(t)

	alex := h.Register(t, "alex@example.com", goodPassword)
	sam := h.Register(t, "sam@example.com", goodPassword)

	created := createBookmark(t, h, alex.Access, map[string]any{"url": "https://go.dev/", "title": "Go"})

	id, ok := created["id"].(float64)
	require.True(t, ok)

	resp := h.Do(t, http.MethodDelete, fmt.Sprintf("/api/v1/bookmarks/%d", int64(id)), sam.Access, nil)
	require.Equal(t, http.StatusNotFound, resp.Status, "a 403 would confirm the bookmark exists")

	// Alex's list is unaffected.
	list := h.Do(t, http.MethodGet, "/api/v1/bookmarks", alex.Access, nil)
	require.Equal(t, http.StatusOK, list.Status)

	var body struct {
		Items []map[string]any `json:"items"`
	}

	list.JSON(t, &body)
	require.Len(t, body.Items, 1)
}

// TestSearchReturnsRankedResults covers the search endpoint.
func TestSearchReturnsRankedResults(t *testing.T) {
	h := harness(t)

	tokens := h.Register(t, "alex@example.com", goodPassword)

	createBookmark(t, h, tokens.Access, map[string]any{
		"url": "https://a.example/", "title": "An introduction to goroutines", "description": "unrelated",
	})
	createBookmark(t, h, tokens.Access, map[string]any{
		"url": "https://b.example/", "title": "Unrelated", "description": "mentions goroutines briefly",
	})

	resp := h.Do(t, http.MethodGet, "/api/v1/search?q=goroutines", tokens.Access, nil)
	require.Equal(t, http.StatusOK, resp.Status)

	var body struct {
		Items []struct {
			Title string  `json:"title"`
			Rank  float32 `json:"rank"`
		} `json:"items"`
	}

	resp.JSON(t, &body)
	require.Len(t, body.Items, 2)
	require.Equal(t, "An introduction to goroutines", body.Items[0].Title, "a title match outranks a description match")
	require.Greater(t, body.Items[0].Rank, body.Items[1].Rank)
}

// TestSearchIsScopedToTheUser is the boundary that a search endpoint gets wrong most often.
func TestSearchIsScopedToTheUser(t *testing.T) {
	h := harness(t)

	alex := h.Register(t, "alex@example.com", goodPassword)
	sam := h.Register(t, "sam@example.com", goodPassword)

	createBookmark(t, h, alex.Access, map[string]any{
		"url": "https://secret.example/", "title": "Alex's private research on marmalade",
	})

	resp := h.Do(t, http.MethodGet, "/api/v1/search?q=marmalade", sam.Access, nil)
	require.Equal(t, http.StatusOK, resp.Status)

	var body struct {
		Items []map[string]any `json:"items"`
	}

	resp.JSON(t, &body)
	require.Empty(t, body.Items, "a search that forgets the user id is a data leak with a nice interface")
}

// TestFilteringByTagKeepsTheOtherTags is the double join, through HTTP.
func TestFilteringByTagKeepsTheOtherTags(t *testing.T) {
	h := harness(t)

	tokens := h.Register(t, "alex@example.com", goodPassword)

	createBookmark(t, h, tokens.Access, map[string]any{
		"url": "https://a.example/", "title": "A", "tags": []string{"go", "testing", "concurrency"},
	})
	createBookmark(t, h, tokens.Access, map[string]any{
		"url": "https://b.example/", "title": "B", "tags": []string{"rust"},
	})

	resp := h.Do(t, http.MethodGet, "/api/v1/bookmarks?tag=testing", tokens.Access, nil)
	require.Equal(t, http.StatusOK, resp.Status)

	var body struct {
		Items []map[string]any `json:"items"`
	}

	resp.JSON(t, &body)
	require.Len(t, body.Items, 1)
	require.ElementsMatch(t, []any{"go", "testing", "concurrency"}, body.Items[0]["tags"])
}

// TestCategoriesAreScopedPerUser covers the composite unique through HTTP.
func TestCategoriesAreScopedPerUser(t *testing.T) {
	h := harness(t)

	alex := h.Register(t, "alex@example.com", goodPassword)
	sam := h.Register(t, "sam@example.com", goodPassword)

	for _, token := range []string{alex.Access, sam.Access} {
		resp := h.Do(t, http.MethodPost, "/api/v1/categories", token, map[string]string{"name": "Reading"})
		require.Equal(t, http.StatusCreated, resp.Status, "two people can both have a Reading category")
	}

	// The same name twice for one user is a conflict.
	again := h.Do(t, http.MethodPost, "/api/v1/categories", alex.Access, map[string]string{"name": "Reading"})
	require.Equal(t, http.StatusConflict, again.Status)
}

// TestTagCountsAreReported covers the LEFT JOIN through HTTP.
func TestTagCountsAreReported(t *testing.T) {
	h := harness(t)

	tokens := h.Register(t, "alex@example.com", goodPassword)

	createBookmark(t, h, tokens.Access, map[string]any{
		"url": "https://a.example/", "title": "A", "tags": []string{"go", "testing"},
	})
	createBookmark(t, h, tokens.Access, map[string]any{
		"url": "https://b.example/", "title": "B", "tags": []string{"go"},
	})

	resp := h.Do(t, http.MethodGet, "/api/v1/tags", tokens.Access, nil)
	require.Equal(t, http.StatusOK, resp.Status)

	var body struct {
		Items []struct {
			Name  string `json:"name"`
			Count int64  `json:"count"`
		} `json:"items"`
	}

	resp.JSON(t, &body)

	counts := map[string]int64{}
	for _, item := range body.Items {
		counts[item.Name] = item.Count
	}

	require.Equal(t, int64(2), counts["go"])
	require.Equal(t, int64(1), counts["testing"])
}

// TestUnknownFieldsAreRejected is why DisallowUnknownFields is on.
func TestUnknownFieldsAreRejected(t *testing.T) {
	h := harness(t)

	tokens := h.Register(t, "alex@example.com", goodPassword)

	resp := h.Do(t, http.MethodPost, "/api/v1/bookmarks", tokens.Access, map[string]any{
		"url": "https://go.dev/", "title": "Go", "titel": "typo",
	})
	require.Equal(t, http.StatusBadRequest, resp.Status,
		"a typo in a client field name is caught at the first request rather than in production")
}

// TestErrorsAreProblemJSON pins the error format.
func TestErrorsAreProblemJSON(t *testing.T) {
	h := harness(t)

	resp := h.Do(t, http.MethodPost, "/api/v1/register", "", map[string]string{
		"email": "not-an-email", "password": goodPassword,
	})

	require.Equal(t, "application/problem+json", resp.Header.Get("Content-Type"))

	var problem struct {
		Type   string `json:"type"`
		Status int    `json:"status"`
	}

	resp.JSON(t, &problem)
	require.Equal(t, http.StatusBadRequest, resp.Status)
	require.Equal(t, http.StatusBadRequest, problem.Status)
	require.NotEmpty(t, problem.Type)
}

// TestMethodNotAllowed is Go 1.22 routing doing the work with no code in this service.
func TestMethodNotAllowed(t *testing.T) {
	h := harness(t)

	resp := h.Do(t, http.MethodDelete, "/healthz", "", nil)
	require.Equal(t, http.StatusMethodNotAllowed, resp.Status)
	require.Contains(t, resp.Header.Get("Allow"), "GET")
}

// TestHealthChecksTheDatabase is the claim a health endpoint should make.
func TestHealthChecksTheDatabase(t *testing.T) {
	h := harness(t)

	resp := h.Do(t, http.MethodGet, "/healthz", "", nil)
	require.Equal(t, http.StatusOK, resp.Status)

	var body map[string]string

	resp.JSON(t, &body)
	require.Equal(t, "ok", body["status"])
}
