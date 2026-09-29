package response

import (
	"encoding/json"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func discard() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// TestOrderingRules is the package's subject: every one of these is a silent bug.
func TestOrderingRules(t *testing.T) {
	t.Run("a header set after WriteHeader is dropped", func(t *testing.T) {
		rec := httptest.NewRecorder()

		rec.WriteHeader(http.StatusOK)
		rec.Header().Set("X-Too-Late", "1")

		if got := rec.Result().Header.Get("X-Too-Late"); got != "" {
			t.Errorf("X-Too-Late = %q; httptest may not model this, a real server drops it", got)
		}
	})

	t.Run("Write without WriteHeader implies 200", func(t *testing.T) {
		rec := httptest.NewRecorder()

		if _, err := rec.Write([]byte("body")); err != nil {
			t.Fatal(err)
		}

		if rec.Code != http.StatusOK {
			t.Errorf("status = %d, want an implicit 200", rec.Code)
		}
	})

	t.Run("a second WriteHeader is ignored", func(t *testing.T) {
		rec := httptest.NewRecorder()

		rec.WriteHeader(http.StatusTeapot)
		rec.WriteHeader(http.StatusInternalServerError)

		if rec.Code != http.StatusTeapot {
			t.Errorf("status = %d, want the first one", rec.Code)
		}
	})

	t.Run("a status after a body is too late", func(t *testing.T) {
		rec := httptest.NewRecorder()

		// The shape of the bug: write the body, then discover a problem.
		_, _ = rec.Write([]byte("partial"))
		rec.WriteHeader(http.StatusInternalServerError)

		if rec.Code != http.StatusOK {
			t.Errorf("status = %d; the implicit 200 cannot be taken back", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "partial") {
			t.Error("the partial body should still be there")
		}
	})
}

func TestWriteJSON(t *testing.T) {
	type payload struct {
		Name string `json:"name"`
	}

	rec := httptest.NewRecorder()
	WriteJSON(rec, discard(), http.StatusCreated, payload{Name: "hammer"})

	if rec.Code != http.StatusCreated {
		t.Errorf("status = %d, want 201", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q", got)
	}

	// Content-Length is set because the body was buffered first, which is what makes a
	// truncated response impossible.
	want := strconv.Itoa(rec.Body.Len())
	if got := rec.Header().Get("Content-Length"); got != want {
		t.Errorf("Content-Length = %q, want %q", got, want)
	}

	var decoded payload
	if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	if decoded.Name != "hammer" {
		t.Errorf("decoded %+v", decoded)
	}
}

// TestWriteJSONSendsNoBodyWhereABodyIsIllegal: a 204 or 304 with a body is a protocol violation
// and some proxies respond by hanging until a timeout.
func TestWriteJSONSendsNoBodyWhereABodyIsIllegal(t *testing.T) {
	for _, status := range []int{http.StatusNoContent, http.StatusNotModified} {
		rec := httptest.NewRecorder()
		WriteJSON(rec, discard(), status, map[string]string{"should": "not appear"})

		if rec.Code != status {
			t.Errorf("status = %d, want %d", rec.Code, status)
		}
		if rec.Body.Len() != 0 {
			t.Errorf("status %d sent a body: %q", status, rec.Body.String())
		}
	}

	// And a nil value sends nothing rather than the four bytes "null".
	rec := httptest.NewRecorder()
	WriteJSON(rec, discard(), http.StatusOK, nil)

	if rec.Body.Len() != 0 {
		t.Errorf("a nil value sent %q, want nothing", rec.Body.String())
	}
}

// TestWriteJSONBuffersFirst is why WriteJSON marshals into a buffer instead of encoding straight
// into the writer. An un-encodable value halfway through would otherwise leave a truncated body
// behind a 200 and the client could not tell.
func TestWriteJSONBuffersFirst(t *testing.T) {
	// A channel cannot be marshalled, and neither can a NaN.
	unencodable := []any{
		make(chan int),
		map[string]any{"ok": 1, "bad": math.NaN()},
		func() {},
	}

	for _, v := range unencodable {
		rec := httptest.NewRecorder()
		WriteJSON(rec, discard(), http.StatusOK, v)

		if rec.Code != http.StatusInternalServerError {
			t.Errorf("%T: status = %d, want 500", v, rec.Code)
		}
		// The body is a clean error, not a half-written object.
		if strings.Contains(rec.Body.String(), `"ok"`) {
			t.Errorf("%T: a partial object leaked: %q", v, rec.Body.String())
		}
	}
}

func TestWriteError(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteError(rec, discard(), http.StatusNotFound, "not found", "item 42 does not exist")

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/problem+json" {
		t.Errorf("Content-Type = %q, want application/problem+json", got)
	}

	var p Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if p.Title != "not found" || p.Status != 404 || p.Detail != "item 42 does not exist" {
		t.Errorf("problem = %+v", p)
	}
}

// TestWriteProblemFillsInTheTitle: a caller that passes only a status should still get a valid
// problem document.
func TestWriteProblemFillsInTheTitle(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteProblem(rec, discard(), Problem{Status: http.StatusForbidden})

	var p Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if p.Title != "Forbidden" {
		t.Errorf("Title = %q, want the status text", p.Title)
	}

	// And a zero status becomes a 500 rather than an invalid response.
	rec = httptest.NewRecorder()
	WriteProblem(rec, discard(), Problem{Title: "something"})

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500 for a zero status", rec.Code)
	}
}

func TestWriteValidationError(t *testing.T) {
	rec := httptest.NewRecorder()

	WriteValidationError(rec, discard(), []FieldError{
		{Field: "name", Detail: "is required"},
		{Field: "count", Detail: "must be positive"},
	})

	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", rec.Code)
	}

	var p Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if len(p.Errors) != 2 {
		t.Fatalf("got %d field errors, want 2", len(p.Errors))
	}
	if p.Errors[0].Field != "name" {
		t.Errorf("first field = %q", p.Errors[0].Field)
	}
	if !strings.Contains(p.Detail, "2 field") {
		t.Errorf("Detail = %q", p.Detail)
	}
}

func TestCreatedSetsLocation(t *testing.T) {
	rec := httptest.NewRecorder()
	Created(rec, discard(), "/items/42", map[string]int{"id": 42})

	if rec.Code != http.StatusCreated {
		t.Errorf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "/items/42" {
		t.Errorf("Location = %q", got)
	}
}

func TestETag(t *testing.T) {
	a := ETag([]byte(`{"a":1}`))
	b := ETag([]byte(`{"a":1}`))
	c := ETag([]byte(`{"a":2}`))

	if a != b {
		t.Errorf("the same body gave different tags: %q and %q", a, b)
	}
	if a == c {
		t.Error("different bodies gave the same tag")
	}
	if !strings.HasPrefix(a, `W/"`) {
		t.Errorf("tag = %q, want a weak tag", a)
	}
}

func TestMatchesETag(t *testing.T) {
	const etag = `W/"abc"`

	tests := []struct {
		header string
		want   bool
	}{
		{`W/"abc"`, true},
		{`"abc"`, true}, // a weak tag matches a strong one weakly
		{`*`, true},
		{` * `, true},
		{`W/"other"`, false},
		{`W/"other", W/"abc"`, true}, // a list
		{`W/"x", W/"y"`, false},
		{``, false},
	}

	for _, tt := range tests {
		r := httptest.NewRequest("GET", "/", nil)
		if tt.header != "" {
			r.Header.Set("If-None-Match", tt.header)
		}

		if got := MatchesETag(r, etag); got != tt.want {
			t.Errorf("If-None-Match: %q -> %v, want %v", tt.header, got, tt.want)
		}
	}
}

func TestWriteJSONWithETag(t *testing.T) {
	body := map[string]int{"id": 1}

	// First request: a 200 with a tag.
	r := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	WriteJSONWithETag(rec, r, discard(), http.StatusOK, body)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	etag := rec.Header().Get("ETag")
	if etag == "" {
		t.Fatal("no ETag was set")
	}

	// Second request with the tag: a 304, no body.
	r = httptest.NewRequest("GET", "/", nil)
	r.Header.Set("If-None-Match", etag)

	rec = httptest.NewRecorder()
	WriteJSONWithETag(rec, r, discard(), http.StatusOK, body)

	if rec.Code != http.StatusNotModified {
		t.Errorf("status = %d, want 304", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("a 304 sent a body: %q", rec.Body.String())
	}
	if got := rec.Header().Get("ETag"); got != etag {
		t.Errorf("the 304 dropped the ETag: %q", got)
	}
	if got := rec.Header().Get("Content-Length"); got != "" {
		t.Errorf("a 304 set Content-Length: %q", got)
	}

	// A changed body gives a different tag and a 200.
	r = httptest.NewRequest("GET", "/", nil)
	r.Header.Set("If-None-Match", etag)

	rec = httptest.NewRecorder()
	WriteJSONWithETag(rec, r, discard(), http.StatusOK, map[string]int{"id": 2})

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 for changed content", rec.Code)
	}
}

func TestStreamJSONLines(t *testing.T) {
	values := make(chan int, 3)
	for i := 1; i <= 3; i++ {
		values <- i
	}
	close(values)

	rec := httptest.NewRecorder()
	if err := StreamJSONLines(rec, values); err != nil {
		t.Fatal(err)
	}

	if got := rec.Header().Get("Content-Type"); got != "application/x-ndjson" {
		t.Errorf("Content-Type = %q", got)
	}

	// One JSON value per line, so a client can process as it arrives.
	lines := strings.Split(strings.TrimSpace(rec.Body.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3: %q", len(lines), rec.Body.String())
	}
	for i, line := range lines {
		var n int
		if err := json.Unmarshal([]byte(line), &n); err != nil {
			t.Errorf("line %d is not JSON: %q", i, line)
		}
		if n != i+1 {
			t.Errorf("line %d = %d, want %d", i, n, i+1)
		}
	}

	// No Content-Length: the length is unknown, which is what makes the response chunked.
	if got := rec.Header().Get("Content-Length"); got != "" {
		t.Errorf("Content-Length = %q, want it unset for a stream", got)
	}
}

// TestStreamJSONLinesReportsUnsupportedFlush: a writer that cannot flush would buffer the whole
// stream, which defeats the point, so it is an error rather than a silent degradation.
func TestStreamJSONLinesReportsUnsupportedFlush(t *testing.T) {
	values := make(chan int, 1)
	values <- 1
	close(values)

	// A minimal writer with no Flush and no Unwrap.
	err := StreamJSONLines(&noFlushWriter{header: make(http.Header)}, values)

	if err == nil {
		t.Fatal("expected an error from a writer that cannot flush")
	}
	if !strings.Contains(err.Error(), "streaming not supported") {
		t.Errorf("err = %v, want ErrStreamingUnsupported", err)
	}
}

type noFlushWriter struct {
	header http.Header
}

func (w *noFlushWriter) Header() http.Header         { return w.header }
func (w *noFlushWriter) Write(b []byte) (int, error) { return len(b), nil }
func (w *noFlushWriter) WriteHeader(int)             {}

func TestNegotiate(t *testing.T) {
	tests := []struct {
		name      string
		accept    string
		available []string
		want      string
	}{
		{"exact match", "application/json", []string{"application/json", "text/html"}, "application/json"},
		{"second choice", "text/html", []string{"application/json", "text/html"}, "text/html"},
		{"wildcard", "*/*", []string{"application/json", "text/html"}, "application/json"},
		{"type wildcard", "text/*", []string{"application/json", "text/html"}, "text/html"},
		{"no header means anything", "", []string{"application/json"}, "application/json"},
		{"nothing acceptable", "image/png", []string{"application/json"}, ""},
		{
			name:      "q values are honoured",
			accept:    "application/json;q=0.5, text/html;q=0.9",
			available: []string{"application/json", "text/html"},
			want:      "text/html",
		},
		{
			name:      "q=0 means unacceptable",
			accept:    "application/json;q=0, text/html",
			available: []string{"application/json", "text/html"},
			want:      "text/html",
		},
		{
			name:      "a browser's Accept header",
			accept:    "text/html,application/xhtml+xml,application/xml;q=0.9,image/webp,*/*;q=0.8",
			available: []string{"application/json", "text/html"},
			want:      "text/html",
		},
		{"no offers", "*/*", nil, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			if tt.accept != "" {
				r.Header.Set("Accept", tt.accept)
			}

			if got := Negotiate(r, tt.available...); got != tt.want {
				t.Errorf("Negotiate(%q, %v) = %q, want %q", tt.accept, tt.available, got, tt.want)
			}
		})
	}
}

// TestNegotiatePrefersTheServersOrderOnATie, because the server knows which representation is
// cheapest and the client usually does not care.
func TestNegotiatePrefersTheServersOrderOnATie(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Accept", "application/json, text/html")

	if got := Negotiate(r, "text/html", "application/json"); got != "text/html" {
		t.Errorf("Negotiate = %q, want the first offer on a tie", got)
	}
	if got := Negotiate(r, "application/json", "text/html"); got != "application/json" {
		t.Errorf("Negotiate = %q, want the first offer on a tie", got)
	}
}

// TestNegotiateUsesTheMostSpecificRange: "anything but JSON" must not get JSON. The first version took the
// highest q among every range that matched, so the */*;q=1 outvoted application/json;q=0.
func TestNegotiateUsesTheMostSpecificRange(t *testing.T) {
	negotiate := func(accept string) string {
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("Accept", accept)
		return Negotiate(r, "application/json", "text/plain")
	}

	if got := negotiate("*/*;q=1, application/json;q=0"); got != "text/plain" {
		t.Errorf("Negotiate(anything but JSON) = %q, want text/plain", got)
	}
	if got := negotiate("text/*;q=0.1, text/plain;q=0.9, */*;q=0.5"); got != "text/plain" {
		t.Errorf("Negotiate = %q, want text/plain at its exact q of 0.9", got)
	}
}
