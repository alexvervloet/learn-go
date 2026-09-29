// Package request covers getting data out of an *http.Request: path values, query strings,
// JSON bodies, forms, uploads, headers and cookies.
//
// # There is no pydantic
//
// This is the biggest difference from the FastAPI version, and it is worth being clear about
// what is lost and what is gained.
//
// FastAPI reads a function signature, derives a schema from the type annotations, validates the
// request against it, coerces the types, and returns a 422 with a field-by-field error report
// before the handler runs. None of that exists in Go. What Go gives you instead:
//
//	encoding/json          decodes into a struct, and that is all it does
//	strconv                one conversion at a time, with an error each
//	r.PathValue, r.URL.Query(), r.FormValue   strings
//
// So a Go handler that wants FastAPI's behaviour writes it. That is thirty lines for a typical
// endpoint, and the thirty lines are the subject of this package and of validation/.
//
// What Go gains: the decoding is explicit, so there is no question about what happened to a
// field that was absent versus zero versus malformed, and the answers are in the code rather
// than in a framework's coercion rules. DecodeJSON below is the version to copy.
//
// # The four ways a JSON field can arrive
//
// This is the distinction pydantic makes for you and encoding/json does not:
//
//	{"name": "x"}   present, set
//	{"name": ""}    present, set to the zero value
//	{"name": null}  present, explicitly null
//	{}              absent
//
// Decoding into a `string` collapses the last three into "". Decoding into a `*string`
// distinguishes absent-or-null from empty. Distinguishing null from absent needs
// json.RawMessage or a custom UnmarshalJSON, and TestOptionalFields shows all three.
package request

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Errors returned by the decoders. Sentinels so a handler can map them to status codes without
// string matching.
var (
	// ErrBadJSON means the body was not valid JSON, or did not fit the target type.
	ErrBadJSON = errors.New("request: malformed JSON")

	// ErrTooLarge means the body exceeded the limit. Distinguished from ErrBadJSON because
	// it deserves a 413 rather than a 400.
	ErrTooLarge = errors.New("request: body too large")

	// ErrUnknownField means the body contained a field the target type does not have.
	ErrUnknownField = errors.New("request: unknown field")

	// ErrEmptyBody means there was no body at all, which is different from an empty object.
	ErrEmptyBody = errors.New("request: empty body")

	// ErrTrailingData means there was more than one JSON value in the body.
	ErrTrailingData = errors.New("request: unexpected data after the JSON value")

	// ErrBadForm means a form body could not be parsed, and ErrBadQuery that query
	// parameters were missing or malformed. Separate from ErrBadJSON, which both used to
	// wrap: a log line saying "malformed JSON" about a form or a query string sends
	// whoever reads it to the wrong place.
	ErrBadForm  = errors.New("request: malformed form")
	ErrBadQuery = errors.New("request: invalid query parameters")
)

// DecodeJSON reads exactly one JSON value from the body into v.
//
// This is the function every Go service ends up writing, and the five things it does that
// json.NewDecoder(r.Body).Decode(v) does not are the whole point:
//
//  1. Caps the body with http.MaxBytesReader, so a 10 GB request cannot make the process
//     allocate 10 GB. json.Decoder has no size limit of its own.
//  2. Calls DisallowUnknownFields, so a typo in a client's payload is an error rather than
//     a silently ignored field. This is the single most useful line here: without it,
//     {"nmae": "x"} succeeds and leaves Name empty.
//  3. Rejects a second JSON value, because Decode reads one and stops. Without the check,
//     `{"a":1}{"b":2}` succeeds and the second object is ignored.
//  4. Distinguishes an empty body from an empty object, which are different requests.
//  5. Turns the decoder's errors into sentinels, so the caller maps them to statuses rather
//     than matching on error strings.
//
// maxBytes of 0 means no limit, which is only right when a middleware already capped it.
func DecodeJSON(w http.ResponseWriter, r *http.Request, maxBytes int64, v any) error {
	body := r.Body
	if maxBytes > 0 {
		// MaxBytesReader rather than io.LimitReader: LimitReader truncates silently, so
		// an oversized body becomes a malformed one and the client gets a confusing 400.
		// MaxBytesReader errors, and it also tells the server to stop reading.
		body = http.MaxBytesReader(w, r.Body, maxBytes)
	}

	decoder := json.NewDecoder(body)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(v); err != nil {
		return classify(err)
	}

	// Decode stops after one value. Anything left is a second value, or trailing garbage.
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ErrTrailingData
	}

	return nil
}

// classify turns encoding/json's errors into this package's sentinels, wrapping the original so
// the detail survives for logging.
//
// The type switch is the part worth copying. encoding/json returns four different error types
// and the useful message is in a different field of each.
func classify(err error) error {
	var (
		syntaxErr        *json.SyntaxError
		unmarshalTypeErr *json.UnmarshalTypeError
		maxBytesErr      *http.MaxBytesError
	)

	switch {
	case errors.As(err, &maxBytesErr):
		return fmt.Errorf("%w: limit is %d bytes", ErrTooLarge, maxBytesErr.Limit)

	case errors.As(err, &syntaxErr):
		return fmt.Errorf("%w at byte %d: %s", ErrBadJSON, syntaxErr.Offset, syntaxErr.Error())

	case errors.As(err, &unmarshalTypeErr):
		// The most useful message of the four: it names the field and both types.
		return fmt.Errorf("%w: field %q expects %s, got %s",
			ErrBadJSON, unmarshalTypeErr.Field, unmarshalTypeErr.Type, unmarshalTypeErr.Value)

	case errors.Is(err, io.EOF):
		return ErrEmptyBody

	case errors.Is(err, io.ErrUnexpectedEOF):
		return fmt.Errorf("%w: body ended mid-value", ErrBadJSON)

	case strings.HasPrefix(err.Error(), "json: unknown field "):
		// DisallowUnknownFields reports this as a plain error with no type, so a string
		// prefix is the only way to recognise it. That is a wart in encoding/json and it
		// has been open as a proposal for years.
		field := strings.TrimPrefix(err.Error(), "json: unknown field ")
		return fmt.Errorf("%w: %s", ErrUnknownField, field)

	default:
		return fmt.Errorf("%w: %w", ErrBadJSON, err)
	}
}

// StatusFor maps this package's errors to HTTP status codes.
//
// A separate function rather than something the decoder returns, because the mapping is a
// policy decision and belongs with the handler.
func StatusFor(err error) int {
	switch {
	case err == nil:
		return http.StatusOK
	case errors.Is(err, ErrTooLarge):
		return http.StatusRequestEntityTooLarge
	case errors.Is(err, ErrUnknownField), errors.Is(err, ErrBadJSON),
		errors.Is(err, ErrEmptyBody), errors.Is(err, ErrTrailingData),
		errors.Is(err, ErrBadForm), errors.Is(err, ErrBadQuery):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

// Query parameters
// ================

// Query wraps url.Values with typed accessors and error collection.
//
// The stdlib gives you r.URL.Query() returning map[string][]string, so every parameter is a
// string and every conversion is three lines. A handler reading five query parameters writes
// fifteen lines of strconv and error checks, which is where this type earns itself.
//
// The design decision: errors ACCUMULATE rather than returning early. A client sending three
// bad parameters should learn about all three, which is what FastAPI's 422 body does and what
// a Go handler returning on the first error does not.
type Query struct {
	values map[string][]string
	errs   []string
}

// NewQuery reads the query string from a request.
func NewQuery(r *http.Request) *Query {
	return &Query{values: r.URL.Query()}
}

// Err returns all the accumulated problems as one error, or nil.
func (q *Query) Err() error {
	if len(q.errs) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrBadQuery, strings.Join(q.errs, "; "))
}

// Errors returns the accumulated problems individually, for a structured error response.
func (q *Query) Errors() []string { return q.errs }

func (q *Query) fail(format string, args ...any) {
	q.errs = append(q.errs, fmt.Sprintf(format, args...))
}

// Has reports whether the parameter was present at all, which is different from being empty.
// ?a= and no ?a are different requests and a handler sometimes needs to tell them apart.
func (q *Query) Has(key string) bool {
	_, ok := q.values[key]
	return ok
}

// String returns the parameter, or fallback if absent. An empty ?key= returns "" rather than
// the fallback, because the client did say something.
func (q *Query) String(key, fallback string) string {
	if !q.Has(key) {
		return fallback
	}
	return q.values[key][0]
}

// Int returns the parameter as an int, recording an error if it is not one.
func (q *Query) Int(key string, fallback int) int {
	if !q.Has(key) {
		return fallback
	}

	raw := q.values[key][0]

	n, err := strconv.Atoi(raw)
	if err != nil {
		q.fail("%s must be an integer, got %q", key, raw)
		return fallback
	}

	return n
}

// IntInRange is Int with bounds, which is the form almost every real query parameter wants.
//
// Clamping silently would be worse than erroring: a client asking for limit=100000 on an API
// whose maximum is 100 should be told, not quietly given 100 and left to wonder why the next
// page never comes.
func (q *Query) IntInRange(key string, fallback, lo, hi int) int {
	n := q.Int(key, fallback)

	if n < lo || n > hi {
		q.fail("%s must be between %d and %d, got %d", key, lo, hi, n)
		return fallback
	}

	return n
}

// Bool accepts the forms an HTML form and a hand-written URL actually produce.
//
// strconv.ParseBool takes 1, t, T, TRUE, true, True and their false counterparts, and rejects
// "yes" and "on". "on" is what an HTML checkbox sends, so a handler using ParseBool alone
// rejects its own form.
func (q *Query) Bool(key string, fallback bool) bool {
	if !q.Has(key) {
		return fallback
	}

	raw := q.values[key][0]

	// A bare ?flag with no value means true, which is how flags are written by hand.
	if raw == "" {
		return true
	}

	switch strings.ToLower(raw) {
	case "on", "yes":
		return true
	case "off", "no":
		return false
	}

	b, err := strconv.ParseBool(raw)
	if err != nil {
		q.fail("%s must be a boolean, got %q", key, raw)
		return fallback
	}

	return b
}

// Time parses an RFC 3339 timestamp, which is what an API should accept and emit.
func (q *Query) Time(key string, fallback time.Time) time.Time {
	if !q.Has(key) {
		return fallback
	}

	raw := q.values[key][0]

	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		q.fail("%s must be an RFC 3339 timestamp, got %q", key, raw)
		return fallback
	}

	return t
}

// Strings returns every value for a repeated parameter, so ?tag=a&tag=b gives both.
//
// The stdlib's r.URL.Query().Get returns only the first, which is the trap: a handler using Get
// silently drops the rest, and the client has no way to know.
func (q *Query) Strings(key string) []string {
	return q.values[key]
}

// CSV splits a single comma-separated parameter, which is the other convention for lists.
// ?tags=a,b,c and ?tags=a&tags=b&tags=c are both common and they need different code.
func (q *Query) CSV(key string) []string {
	raw := q.String(key, "")
	if raw == "" {
		return nil
	}

	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))

	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			out = append(out, trimmed)
		}
	}

	return out
}

// OneOf restricts a parameter to a fixed set, which is the enum case.
func (q *Query) OneOf(key, fallback string, allowed ...string) string {
	if !q.Has(key) {
		return fallback
	}

	raw := q.values[key][0]
	for _, a := range allowed {
		if raw == a {
			return raw
		}
	}

	q.fail("%s must be one of %s, got %q", key, strings.Join(allowed, ", "), raw)
	return fallback
}

// Forms and uploads
// =================

// ParseForm reads an application/x-www-form-urlencoded or multipart body.
//
// r.ParseForm merges the query string into r.PostForm's sibling r.Form, which is a trap:
// r.FormValue("id") returns a query parameter when the body has no such field, so a handler
// expecting a POST field silently accepts ?id=. r.PostFormValue reads the body only, and is
// almost always what you want.
//
// maxBytes caps the whole body, which ParseMultipartForm on its own does not do. Its
// argument is only how much to hold in MEMORY: a multipart body larger than that spills to
// temporary files with no limit at all. The first version passed maxBytes straight through,
// so a form "limited" to 1KB accepted 64KB on disk. http.MaxBytesReader is the cap, the same
// as for JSON.
func ParseForm(r *http.Request, maxBytes int64) error {
	r.Body = http.MaxBytesReader(nil, r.Body, maxBytes)

	// Pick the parser from the Content-Type rather than trying multipart and falling
	// back. ParseMultipartForm calls ParseForm internally, THROWS AWAY its error, and
	// reports ErrNotMultipart; a second ParseForm then sees the form already parsed and
	// returns nil. The first version did exactly that, so a malformed urlencoded body
	// ("a=%zz") parsed without complaint.
	var err error

	if mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mediaType == "multipart/form-data" {
		err = r.ParseMultipartForm(maxBytes)
	} else {
		err = r.ParseForm()
	}

	var tooLarge *http.MaxBytesError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &tooLarge):
		return fmt.Errorf("%w: over %d bytes", ErrTooLarge, maxBytes)
	default:
		return fmt.Errorf("%w: %w", ErrBadForm, err)
	}
}

// Upload describes one uploaded file, with the parts a handler must not trust separated from
// the parts it can.
type Upload struct {
	// Filename is what mime/multipart reports, which is NOT the raw client value: the
	// stdlib applies filepath.Base to it first. That helps and does not finish the job.
	// Measured on Unix, in TestFilenameIsPartlySanitised:
	//
	//	sent                             header.Filename
	//	../../etc/passwd                 passwd           stripped
	//	/etc/passwd                      passwd           stripped
	//	sub/dir/file.txt                 file.txt         stripped
	//	..\..\windows\system32\config    unchanged        NOT stripped
	//	C:\Users\x\file.txt              unchanged        NOT stripped
	//	..                               ..               NOT stripped
	//	.                                .                NOT stripped
	//
	// filepath.Base is OS-specific, so the backslash cases ARE stripped on Windows and not
	// on Unix, which means the same code is safe on one and not the other.
	//
	// The two that matter most are ".." and ".", because filepath.Join(dir, "..") is the
	// parent directory. SafeFilename below is what to use instead of trusting this.
	//
	// A filename containing a null byte never gets this far: it makes the MIME header
	// malformed and ParseMultipartForm rejects the whole request.
	Filename string

	// Size is the real byte count, measured while reading.
	Size int64

	// ContentType is the client's claim, also not to be trusted. DetectedType is what the
	// bytes actually look like.
	ContentType  string
	DetectedType string
}

// ReadUpload reads one uploaded file, capping its size and sniffing its real type.
//
// Three things here are the whole lesson about file uploads:
//
//	the filename is PARTLY sanitised by the stdlib and still not safe as a path; see the
//	  Upload.Filename comment for what survives, and SafeFilename for the fix
//	the Content-Type header is attacker-controlled, so the real type is sniffed from the
//	  first 512 bytes with http.DetectContentType
//	the size limit has to be enforced while READING, because the multipart header's
//	  reported size is also attacker-controlled
func ReadUpload(file multipart.File, header *multipart.FileHeader, maxBytes int64) (Upload, []byte, error) {
	// 512 bytes is what http.DetectContentType looks at, and it is the number from the
	// WHATWG sniffing spec rather than an arbitrary choice.
	const sniffLen = 512

	limited := io.LimitReader(file, maxBytes+1)

	data, err := io.ReadAll(limited)
	if err != nil {
		return Upload{}, nil, fmt.Errorf("reading upload: %w", err)
	}

	// Reading maxBytes+1 is how you tell "exactly at the limit" from "over it".
	if int64(len(data)) > maxBytes {
		return Upload{}, nil, fmt.Errorf("%w: limit is %d bytes", ErrTooLarge, maxBytes)
	}

	sniff := data
	if len(sniff) > sniffLen {
		sniff = sniff[:sniffLen]
	}

	return Upload{
		Filename:     header.Filename,
		Size:         int64(len(data)),
		ContentType:  header.Header.Get("Content-Type"),
		DetectedType: http.DetectContentType(sniff),
	}, data, nil
}

// Headers and cookies
// ===================

// BearerToken extracts a bearer token from the Authorization header.
//
// The prefix comparison is case-insensitive because RFC 7235 says the scheme is, and a client
// sending "bearer" lowercase is within spec. A handler comparing against "Bearer " exactly
// rejects a conforming client.
func BearerToken(r *http.Request) (string, bool) {
	header := r.Header.Get("Authorization")

	const prefix = "bearer "
	if len(header) < len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return "", false
	}

	token := strings.TrimSpace(header[len(prefix):])
	return token, token != ""
}

// Cookie returns a cookie's value, or fallback.
//
// r.Cookie returns http.ErrNoCookie rather than an empty string, so the two-line wrapper is
// what every handler writes.
func Cookie(r *http.Request, name, fallback string) string {
	c, err := r.Cookie(name)
	if err != nil {
		return fallback
	}
	return c.Value
}

// ClientIP returns the best guess at the client's address.
//
// The important part is the warning. X-Forwarded-For is a client-supplied header: anyone can
// send one, so trusting it without knowing a proxy set it is a way to defeat rate limiting and
// audit logs at the same time. Only trust it when the request came through a proxy you
// control, and then take the LAST entry rather than the first, because a proxy appends and an
// attacker prepends.
//
// trustProxy makes that decision explicit rather than hiding it. chi's RealIP does the same
// job and trusts the header unconditionally, which is fine behind a load balancer and wrong
// when exposed directly.
func ClientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
			parts := strings.Split(forwarded, ",")
			// The LAST entry: the proxy appended it, so it is the one the proxy saw.
			return strings.TrimSpace(parts[len(parts)-1])
		}
		if real := r.Header.Get("X-Real-IP"); real != "" {
			return strings.TrimSpace(real)
		}
	}

	// RemoteAddr is host:port and the port is never wanted.
	if host, _, err := splitHostPort(r.RemoteAddr); err == nil {
		return host
	}

	return r.RemoteAddr
}

// SafeFilename turns a client-supplied filename into something usable as a single path
// element, or reports that there is nothing usable in it.
//
// What the stdlib's filepath.Base does not handle, and this does:
//
//	backslashes, which are not separators on Unix so filepath.Base leaves them
//	"." and "..", which come through unchanged and which filepath.Join then honours
//	a leading dot, which makes a hidden file
//	an empty result
//
// The stronger advice is to not use the client's filename at all: generate an ID, keep the
// original as metadata, and send it back in a Content-Disposition header. This exists for the
// cases where a human has to recognise the file in a directory listing.
func SafeFilename(name string) (string, bool) {
	// Cut at the last separator of EITHER kind, since filepath.Base only knows the host
	// OS's.
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}

	name = strings.TrimSpace(name)

	// "." and ".." are the dangerous ones: filepath.Join(dir, "..") is dir's parent. A
	// leading dot at all makes a hidden file, which is rarely intended.
	name = strings.TrimLeft(name, ".")

	// An allow list, not a deny list: the set of safe characters is short and known, and a
	// deny list is always missing something.
	var sb strings.Builder
	for _, c := range name {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '.', c == '-', c == '_':
			sb.WriteRune(c)
		default:
			sb.WriteByte('_')
		}
	}

	cleaned := sb.String()
	if cleaned == "" {
		return "", false
	}

	return cleaned, true
}

// splitHostPort is net.SplitHostPort, wrapped so a missing port is not an error.
func splitHostPort(addr string) (string, string, error) {
	i := strings.LastIndex(addr, ":")
	if i < 0 {
		return addr, "", nil
	}

	// An IPv6 address without a port looks like "::1", so a bracket check is needed.
	if strings.Contains(addr[:i], ":") && !strings.HasPrefix(addr, "[") {
		return addr, "", nil
	}

	return strings.TrimSuffix(strings.TrimPrefix(addr[:i], "["), "]"), addr[i+1:], nil
}
