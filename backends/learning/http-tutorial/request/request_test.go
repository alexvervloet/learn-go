package request

import (
	"bytes"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

type item struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

func post(body string) (*httptest.ResponseRecorder, *http.Request) {
	return httptest.NewRecorder(), httptest.NewRequest("POST", "/", strings.NewReader(body))
}

func TestDecodeJSON(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr error
		want    item
	}{
		{"valid", `{"name":"hammer","count":3}`, nil, item{"hammer", 3}},
		{"missing fields are zero", `{}`, nil, item{}},
		{"empty body", ``, ErrEmptyBody, item{}},
		{"malformed", `{`, ErrBadJSON, item{}},
		{"wrong type", `{"count":"three"}`, ErrBadJSON, item{}},
		{"unknown field", `{"nmae":"typo"}`, ErrUnknownField, item{}},
		{"two values", `{"name":"a"}{"name":"b"}`, ErrTrailingData, item{}},
		{"trailing garbage", `{"name":"a"} oops`, ErrTrailingData, item{}},
		{"null body", `null`, nil, item{}},
		{"an array where an object was wanted", `[]`, ErrBadJSON, item{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, r := post(tt.body)

			var got item
			err := DecodeJSON(w, r, 1<<20, &got)

			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if got != tt.want {
					t.Errorf("decoded %+v, want %+v", got, tt.want)
				}
				return
			}

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			t.Logf("message: %v", err)
		})
	}
}

// TestDisallowUnknownFieldsIsTheImportantLine: without it, a typo in a client's payload succeeds
// and leaves the field at its zero value, which is a bug with no error anywhere.
func TestDisallowUnknownFieldsIsTheImportantLine(t *testing.T) {
	const typo = `{"nmae":"hammer"}`

	// With the check, as DecodeJSON does it.
	w, r := post(typo)
	var strict item
	if err := DecodeJSON(w, r, 1<<20, &strict); !errors.Is(err, ErrUnknownField) {
		t.Errorf("DecodeJSON accepted a typo: %v", err)
	}

	// Without it, which is what json.NewDecoder(r.Body).Decode(&v) does.
	var lax item
	if err := jsonDecodeLax(typo, &lax); err != nil {
		t.Fatalf("the plain decoder errored: %v", err)
	}
	if lax.Name != "" {
		t.Errorf("Name = %q, expected the typo to have been ignored", lax.Name)
	}

	t.Log(`the plain decoder accepted {"nmae":"hammer"} and left Name empty; ` +
		`DisallowUnknownFields is what turns that into a 400`)
}

// jsonDecodeLax is json.NewDecoder(body).Decode(v) with no DisallowUnknownFields, which is
// what a handler writes before learning about the option.
func jsonDecodeLax(body string, v any) error {
	return json.NewDecoder(strings.NewReader(body)).Decode(v)
}

func strconvParseBool(s string) (bool, error) { return strconv.ParseBool(s) }

func TestTooLarge(t *testing.T) {
	big := `{"name":"` + strings.Repeat("x", 1000) + `"}`

	w, r := post(big)

	var got item
	err := DecodeJSON(w, r, 100, &got)

	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
	if StatusFor(err) != http.StatusRequestEntityTooLarge {
		t.Errorf("StatusFor = %d, want 413", StatusFor(err))
	}
	t.Logf("message: %v", err)
}

func TestStatusFor(t *testing.T) {
	tests := map[error]int{
		nil:                          http.StatusOK,
		ErrTooLarge:                  http.StatusRequestEntityTooLarge,
		ErrBadJSON:                   http.StatusBadRequest,
		ErrUnknownField:              http.StatusBadRequest,
		ErrEmptyBody:                 http.StatusBadRequest,
		ErrTrailingData:              http.StatusBadRequest,
		errors.New("something else"): http.StatusInternalServerError,
	}

	for err, want := range tests {
		if got := StatusFor(err); got != want {
			t.Errorf("StatusFor(%v) = %d, want %d", err, got, want)
		}
	}
}

// TestOptionalFields is the four-way distinction pydantic makes and encoding/json does not.
func TestOptionalFields(t *testing.T) {
	type payload struct {
		// A value type collapses absent, null and "" into "".
		Plain string `json:"plain"`

		// A pointer distinguishes absent-or-null from empty.
		Pointer *string `json:"pointer"`

		// json.RawMessage is the only way to tell absent from null: absent leaves it nil,
		// null leaves it the four bytes "null".
		Raw json.RawMessage `json:"raw"`
	}

	tests := []struct {
		name        string
		body        string
		wantPlain   string
		wantPointer string // "<nil>" or the value
		wantRaw     string // "<nil>" or the raw bytes
	}{
		{"all set", `{"plain":"a","pointer":"b","raw":"c"}`, "a", "b", `"c"`},
		{"all empty strings", `{"plain":"","pointer":"","raw":""}`, "", "", `""`},
		{"all null", `{"plain":null,"pointer":null,"raw":null}`, "", "<nil>", "null"},
		{"all absent", `{}`, "", "<nil>", "<nil>"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, r := post(tt.body)

			var got payload
			if err := DecodeJSON(w, r, 1<<20, &got); err != nil {
				t.Fatal(err)
			}

			if got.Plain != tt.wantPlain {
				t.Errorf("Plain = %q, want %q", got.Plain, tt.wantPlain)
			}

			pointer := "<nil>"
			if got.Pointer != nil {
				pointer = *got.Pointer
			}
			if pointer != tt.wantPointer {
				t.Errorf("Pointer = %s, want %s", pointer, tt.wantPointer)
			}

			raw := "<nil>"
			if got.Raw != nil {
				raw = string(got.Raw)
			}
			if raw != tt.wantRaw {
				t.Errorf("Raw = %s, want %s", raw, tt.wantRaw)
			}
		})
	}

	t.Log("only json.RawMessage distinguishes an absent field from an explicit null; " +
		"a pointer collapses those two and a value type collapses all three")
}

func TestQuery(t *testing.T) {
	r := httptest.NewRequest("GET",
		"/?name=hammer&count=3&flag&on=on&tags=a&tags=b&csv=x,+y,z&when=2026-01-02T03:04:05Z&sort=asc", nil)

	q := NewQuery(r)

	if got := q.String("name", "?"); got != "hammer" {
		t.Errorf("String(name) = %q", got)
	}
	if got := q.String("absent", "fallback"); got != "fallback" {
		t.Errorf("String(absent) = %q, want the fallback", got)
	}
	if got := q.Int("count", 0); got != 3 {
		t.Errorf("Int(count) = %d", got)
	}
	if got := q.Int("absent", 7); got != 7 {
		t.Errorf("Int(absent) = %d, want the fallback", got)
	}
	if got := q.Bool("flag", false); !got {
		t.Error("a bare ?flag should be true")
	}
	if got := q.Bool("on", false); !got {
		t.Error(`?on=on should be true: "on" is what an HTML checkbox sends`)
	}
	if got := q.Strings("tags"); !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("Strings(tags) = %v, want [a b]", got)
	}
	if got := q.CSV("csv"); !slices.Equal(got, []string{"x", "y", "z"}) {
		t.Errorf("CSV = %v, want [x y z]", got)
	}
	if got := q.Time("when", time.Time{}); got.Year() != 2026 {
		t.Errorf("Time = %v", got)
	}
	if got := q.OneOf("sort", "desc", "asc", "desc"); got != "asc" {
		t.Errorf("OneOf = %q", got)
	}

	if err := q.Err(); err != nil {
		t.Errorf("unexpected errors: %v", err)
	}
}

// TestQueryHasDistinguishesEmptyFromAbsent, because ?a= and no ?a are different requests.
func TestQueryHasDistinguishesEmptyFromAbsent(t *testing.T) {
	q := NewQuery(httptest.NewRequest("GET", "/?empty=", nil))

	if !q.Has("empty") {
		t.Error("?empty= should be present")
	}
	if q.Has("absent") {
		t.Error("a parameter that was not sent should not be present")
	}
	if got := q.String("empty", "fallback"); got != "" {
		t.Errorf("String(empty) = %q, want \"\": the client did say something", got)
	}
}

// TestQueryAccumulatesErrors is the design decision: a client sending three bad parameters
// should learn about all three.
func TestQueryAccumulatesErrors(t *testing.T) {
	r := httptest.NewRequest("GET", "/?count=abc&limit=99999&mode=sideways&flag=maybe", nil)

	q := NewQuery(r)

	q.Int("count", 0)
	q.IntInRange("limit", 10, 1, 100)
	q.OneOf("mode", "fast", "fast", "slow")
	q.Bool("flag", false)

	errs := q.Errors()
	if len(errs) != 4 {
		t.Fatalf("got %d errors, want 4: %v", len(errs), errs)
	}

	for _, e := range errs {
		t.Logf("  %s", e)
	}

	if err := q.Err(); !errors.Is(err, ErrBadQuery) {
		t.Errorf("Err() = %v, want it to wrap ErrBadQuery", err)
	}
	if StatusFor(q.Err()) != http.StatusBadRequest {
		t.Error("the accumulated error should map to 400")
	}
}

// TestQueryIntInRangeErrorsRatherThanClamping: a client asking for limit=100000 should be told,
// not quietly given 100 and left wondering why the next page never comes.
func TestQueryIntInRangeErrorsRatherThanClamping(t *testing.T) {
	q := NewQuery(httptest.NewRequest("GET", "/?limit=100000", nil))

	got := q.IntInRange("limit", 25, 1, 100)

	if got != 25 {
		t.Errorf("IntInRange = %d, want the fallback 25", got)
	}
	if q.Err() == nil {
		t.Error("an out-of-range value should be reported, not clamped silently")
	}
}

// TestParseBoolRejectsOn documents why Query.Bool does not just call strconv.ParseBool: "on" is
// what an HTML checkbox sends and ParseBool rejects it.
func TestParseBoolRejectsOn(t *testing.T) {
	if _, err := strconvParseBool("on"); err == nil {
		t.Error("strconv.ParseBool accepted \"on\"; the wrapper may no longer be needed")
	}

	q := NewQuery(httptest.NewRequest("GET", "/?a=on&b=off&c=yes&d=no&e=TRUE&f=0", nil))

	want := map[string]bool{"a": true, "b": false, "c": true, "d": false, "e": true, "f": false}
	for key, expected := range want {
		if got := q.Bool(key, !expected); got != expected {
			t.Errorf("Bool(%s) = %v, want %v", key, got, expected)
		}
	}
	if err := q.Err(); err != nil {
		t.Errorf("unexpected errors: %v", err)
	}
}

// TestStringsVersusGet: r.URL.Query().Get returns only the FIRST value of a repeated parameter,
// so a handler using it silently drops the rest.
func TestStringsVersusGet(t *testing.T) {
	r := httptest.NewRequest("GET", "/?tag=a&tag=b&tag=c", nil)

	if got := r.URL.Query().Get("tag"); got != "a" {
		t.Errorf("Get = %q, want only the first value", got)
	}
	if got := NewQuery(r).Strings("tag"); !slices.Equal(got, []string{"a", "b", "c"}) {
		t.Errorf("Strings = %v, want all three", got)
	}
}

// TestFormValueMergesTheQueryString is the r.ParseForm trap: r.FormValue falls back to the query
// string, so a handler expecting a POST field silently accepts ?field= instead.
func TestFormValueMergesTheQueryString(t *testing.T) {
	// A POST with an EMPTY body and the value in the query string.
	r := httptest.NewRequest("POST", "/?id=from-query", strings.NewReader(""))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	if got := r.FormValue("id"); got != "from-query" {
		t.Errorf("FormValue = %q, want it to have fallen back to the query string", got)
	}
	if got := r.PostFormValue("id"); got != "" {
		t.Errorf("PostFormValue = %q, want \"\": it reads the body only", got)
	}

	t.Log("r.FormValue merges the query string into the form, so it accepts ?id= for a field " +
		"that should have come from the body. r.PostFormValue is almost always what you want.")
}

func TestParseFormURLEncoded(t *testing.T) {
	r := httptest.NewRequest("POST", "/", strings.NewReader("name=hammer&count=3"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	if err := ParseForm(r, 1<<20); err != nil {
		t.Fatal(err)
	}

	if got := r.PostFormValue("name"); got != "hammer" {
		t.Errorf("name = %q", got)
	}
	if got := r.PostFormValue("count"); got != "3" {
		t.Errorf("count = %q", got)
	}
}

func TestReadUpload(t *testing.T) {
	// A PNG magic number, so the sniffer has something real to find.
	pngHeader := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	content := append(pngHeader, bytes.Repeat([]byte{0}, 100)...)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// A deliberately hostile filename, and a Content-Type the client made up.
	// CreateFormFile hardcodes application/octet-stream, so the header has to be built by
	// hand to set a Content-Type the client "claimed".
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", `form-data; name="file"; filename="../../etc/passwd"`)
	h.Set("Content-Type", "text/plain")

	part, err := writer.CreatePart(h)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	r := httptest.NewRequest("POST", "/upload", body)
	r.Header.Set("Content-Type", writer.FormDataContentType())

	if err := ParseForm(r, 1<<20); err != nil {
		t.Fatal(err)
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()

	upload, data, err := ReadUpload(file, header, 1<<20)
	if err != nil {
		t.Fatal(err)
	}

	if upload.Size != int64(len(content)) {
		t.Errorf("Size = %d, want %d", upload.Size, len(content))
	}
	if len(data) != len(content) {
		t.Errorf("read %d bytes, want %d", len(data), len(content))
	}

	// The claimed type and the real type disagree, which is the whole point.
	if upload.ContentType != "text/plain" {
		t.Errorf("ContentType = %q, want the client's claim", upload.ContentType)
	}
	if !strings.HasPrefix(upload.DetectedType, "image/png") {
		t.Errorf("DetectedType = %q, want image/png", upload.DetectedType)
	}

	// The stdlib already applied filepath.Base, so the traversal is gone. That is more than
	// the usual advice implies and less than enough; see TestFilenameIsPartlySanitised.
	if upload.Filename != "passwd" {
		t.Errorf("Filename = %q, want %q: mime/multipart applies filepath.Base",
			upload.Filename, "passwd")
	}

	t.Logf("client claimed %q, the bytes say %q, and %q arrived as %q",
		upload.ContentType, upload.DetectedType, "../../etc/passwd", upload.Filename)
}

// TestFilenameIsPartlySanitised is the finding that corrected this package's own warning.
//
// mime/multipart applies filepath.Base to the client's filename, which strips forward-slash
// paths. It does NOT strip backslashes on Unix, because filepath.Base is OS-specific, and it
// does not touch "." or "..", which are the two that matter most.
func TestFilenameIsPartlySanitised(t *testing.T) {
	send := func(t *testing.T, filename string) (string, error) {
		t.Helper()

		body := &bytes.Buffer{}
		w := multipart.NewWriter(body)

		h := make(textproto.MIMEHeader)
		h.Set("Content-Disposition", `form-data; name="file"; filename="`+filename+`"`)

		part, err := w.CreatePart(h)
		if err != nil {
			return "", err
		}
		if _, err := part.Write([]byte("x")); err != nil {
			return "", err
		}
		if err := w.Close(); err != nil {
			return "", err
		}

		r := httptest.NewRequest("POST", "/", body)
		r.Header.Set("Content-Type", w.FormDataContentType())

		if err := r.ParseMultipartForm(1 << 20); err != nil {
			return "", err
		}

		f, header, err := r.FormFile("file")
		if err != nil {
			return "", err
		}
		defer func() { _ = f.Close() }()

		return header.Filename, nil
	}

	stripped := map[string]string{
		"../../etc/passwd": "passwd",
		"/etc/passwd":      "passwd",
		"sub/dir/file.txt": "file.txt",
		"normal.txt":       "normal.txt",
		"dir/":             "dir",
	}
	for sent, want := range stripped {
		got, err := send(t, sent)
		if err != nil {
			t.Fatalf("%q: %v", sent, err)
		}
		if got != want {
			t.Errorf("sent %q, got %q, want %q", sent, got, want)
		}
	}

	// Not stripped on Unix, because filepath.Base does not treat backslash as a separator
	// there. The same code is therefore safe on Windows and not here.
	survives := []string{
		`..\..\windows\system32\config`,
		`C:\Users\x\file.txt`,
		"..",
		".",
	}
	for _, sent := range survives {
		got, err := send(t, sent)
		if err != nil {
			t.Fatalf("%q: %v", sent, err)
		}

		if runtime.GOOS == "windows" {
			t.Logf("windows: sent %q, got %q", sent, got)
			continue
		}
		if got != sent {
			t.Errorf("sent %q, got %q, expected it to survive on %s", sent, got, runtime.GOOS)
		}
	}

	// A null byte breaks the MIME header, so the request never reaches a handler.
	if _, err := send(t, "a\x00b.txt"); err == nil {
		t.Error("a null byte in the filename was accepted; it should make the header malformed")
	} else {
		t.Logf("a null byte is rejected at parse time: %v", err)
	}
}

func TestSafeFilename(t *testing.T) {
	tests := []struct {
		in   string
		want string
		ok   bool
	}{
		{"report.pdf", "report.pdf", true},
		{"../../etc/passwd", "passwd", true},
		{`..\..\windows\config`, "config", true},
		{`C:\Users\x\file.txt`, "file.txt", true},
		{"..", "", false},
		{".", "", false},
		{"...", "", false},
		{".hidden", "hidden", true},
		{"", "", false},
		{"   ", "", false},
		{"my file (1).txt", "my_file__1_.txt", true},
		{"naïve.txt", "na_ve.txt", true},
		{"a/b/c", "c", true},
	}

	for _, tt := range tests {
		got, ok := SafeFilename(tt.in)

		if ok != tt.ok {
			t.Errorf("SafeFilename(%q) ok = %v, want %v (got %q)", tt.in, ok, tt.ok, got)
			continue
		}
		if ok && got != tt.want {
			t.Errorf("SafeFilename(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}

	// The property that matters: whatever comes out is a single path element that cannot
	// escape a directory.
	for _, tt := range tests {
		got, ok := SafeFilename(tt.in)
		if !ok {
			continue
		}
		if strings.ContainsAny(got, `/\`) || got == "." || got == ".." {
			t.Errorf("SafeFilename(%q) = %q, which is not a safe single element", tt.in, got)
		}
	}
}

func TestReadUploadRejectsOversize(t *testing.T) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	part, err := writer.CreateFormFile("file", "big.bin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(bytes.Repeat([]byte{'x'}, 500)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	r := httptest.NewRequest("POST", "/upload", body)
	r.Header.Set("Content-Type", writer.FormDataContentType())

	if err := ParseForm(r, 1<<20); err != nil {
		t.Fatal(err)
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()

	if _, _, err := ReadUpload(file, header, 100); !errors.Is(err, ErrTooLarge) {
		t.Errorf("err = %v, want ErrTooLarge", err)
	}

	// Exactly at the limit is allowed, which is the off-by-one the +1 read exists for.
	file2, header2, err := r.FormFile("file")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file2.Close() }()

	if _, _, err := ReadUpload(file2, header2, 500); err != nil {
		t.Errorf("a file exactly at the limit was rejected: %v", err)
	}
}

func TestBearerToken(t *testing.T) {
	tests := []struct {
		header string
		want   string
		ok     bool
	}{
		{"Bearer abc123", "abc123", true},
		{"bearer abc123", "abc123", true}, // RFC 7235 says the scheme is case-insensitive
		{"BEARER abc123", "abc123", true},
		{"Bearer   abc123  ", "abc123", true},
		{"Basic abc123", "", false},
		{"Bearer ", "", false},
		{"Bearer", "", false},
		{"", "", false},
		{"abc123", "", false},
	}

	for _, tt := range tests {
		r := httptest.NewRequest("GET", "/", nil)
		if tt.header != "" {
			r.Header.Set("Authorization", tt.header)
		}

		got, ok := BearerToken(r)

		if ok != tt.ok || got != tt.want {
			t.Errorf("BearerToken(%q) = %q, %v; want %q, %v", tt.header, got, ok, tt.want, tt.ok)
		}
	}
}

func TestCookie(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(&http.Cookie{Name: "session", Value: "abc"})

	if got := Cookie(r, "session", "none"); got != "abc" {
		t.Errorf("Cookie = %q", got)
	}
	if got := Cookie(r, "absent", "none"); got != "none" {
		t.Errorf("Cookie(absent) = %q, want the fallback", got)
	}
}

// TestClientIPRequiresTrust is the security point: X-Forwarded-For is client-supplied, so
// trusting it unconditionally defeats rate limiting and audit logging at once.
func TestClientIPRequiresTrust(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.1:54321"
	r.Header.Set("X-Forwarded-For", "1.2.3.4, 5.6.7.8")

	if got := ClientIP(r, false); got != "10.0.0.1" {
		t.Errorf("untrusted: ClientIP = %q, want the real RemoteAddr", got)
	}

	// Trusted: the LAST entry, because a proxy appends and an attacker prepends.
	if got := ClientIP(r, true); got != "5.6.7.8" {
		t.Errorf("trusted: ClientIP = %q, want the last entry", got)
	}

	t.Log("an attacker sets X-Forwarded-For: 1.2.3.4 and the proxy appends the real address, " +
		"so the LAST entry is the one the proxy saw and the first is whatever was claimed")
}

func TestClientIPWithoutAPort(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.1" // no port, which happens with some test setups

	if got := ClientIP(r, false); got != "10.0.0.1" {
		t.Errorf("ClientIP = %q", got)
	}
}

// TestParseFormCapsTheWholeBody: ParseMultipartForm's argument is how much to keep in MEMORY, and a multipart
// body larger than that spills to temporary files with no limit at all. The first version passed maxBytes
// straight through, so a "1KB" form accepted 64KB on disk without an error.
func TestParseFormCapsTheWholeBody(t *testing.T) {
	var body bytes.Buffer

	w := multipart.NewWriter(&body)
	part, _ := w.CreateFormFile("upload", "big.bin")
	_, _ = part.Write(bytes.Repeat([]byte("x"), 64<<10))
	_ = w.Close()

	r := httptest.NewRequest(http.MethodPost, "/", &body)
	r.Header.Set("Content-Type", w.FormDataContentType())

	err := ParseForm(r, 1<<10)
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("a 64KB multipart body against a 1KB limit: err = %v, want ErrTooLarge", err)
	}
	if StatusFor(err) != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", StatusFor(err))
	}

	// And a malformed form is a form error, not a JSON one.
	bad := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("a=%zz"))
	bad.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	err = ParseForm(bad, 1<<10)
	if !errors.Is(err, ErrBadForm) || errors.Is(err, ErrBadJSON) {
		t.Errorf("malformed form: err = %v, want ErrBadForm and not ErrBadJSON", err)
	}
}

// TestSplitHostPortHandlesIPv6: the first version cut at the last colon, which for "[::1]" is inside the
// brackets, and returned ":" as the host.
func TestSplitHostPortHandlesIPv6(t *testing.T) {
	for addr, want := range map[string]string{
		"203.0.113.7:443":   "203.0.113.7",
		"203.0.113.7":       "203.0.113.7",
		"[2001:db8::1]:443": "2001:db8::1",
		"[::1]":             "::1",
		"::1":               "::1",
	} {
		if host, _, err := splitHostPort(addr); err != nil || host != want {
			t.Errorf("splitHostPort(%q) = %q, %v; want %q", addr, host, err, want)
		}
	}
}
