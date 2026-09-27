// Package response covers writing one: JSON, errors, streaming and conditional requests.
//
// # The ordering rule, which is the whole package in three lines
//
//	w.Header().Set(...)   headers, first
//	w.WriteHeader(status) the status line, once
//	w.Write(body)         the body, last
//
// Getting that order wrong does not error. A header set after WriteHeader is silently dropped,
// because the response is already committed and the header block has gone out. A second
// WriteHeader logs "superfluous response.WriteHeader call" to the server's error log and is
// ignored. A Write before WriteHeader triggers an implicit 200, so a handler that writes a
// body and then tries to send a 500 sends neither.
//
// Every one of those is a silent bug, and TestHeaderAfterWriteHeaderIsDropped and friends pin
// them down.
//
// # Errors
//
// http.Error(w, msg, status) is the stdlib's answer and it sends text/plain. A JSON API that
// returns text/plain for its errors forces every client to branch on the status code before
// parsing, so this package has WriteError, which sends the same shape every time.
//
// The shape used is RFC 9457 (formerly RFC 7807) problem+json, because it exists, it is what
// the rest of the industry converged on, and inventing another one has no upside.
package response

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
)

// WriteJSON writes v as JSON with the given status.
//
// The four lines that matter:
//
//	Content-Type before WriteHeader, or it is dropped
//	WriteHeader before Write, or the status is an implicit 200
//	the encode error is LOGGED rather than returned, because by the time it happens the
//	  status line has gone out and there is nothing a caller could do
//	a nil body sends no body at all rather than the four bytes "null"
//
// Passing a logger rather than using the package-level one makes the dependency visible and
// lets a test assert on the failure path.
func WriteJSON(w http.ResponseWriter, log *slog.Logger, status int, v any) {
	// 204 and 304 must not have a body, and sending one is a protocol violation that some
	// proxies handle by hanging.
	if v == nil || status == http.StatusNoContent || status == http.StatusNotModified {
		w.WriteHeader(status)
		return
	}

	// Encode into a buffer FIRST. Encoding straight into w means a failure halfway through
	// leaves a truncated body behind a 200, and the client cannot tell. Buffering costs an
	// allocation and turns that into a clean 500.
	body, err := json.Marshal(v)
	if err != nil {
		log.Error("encoding response failed", "error", err, "type", fmt.Sprintf("%T", v))
		http.Error(w, `{"title":"internal server error","status":500}`,
			http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)

	if _, err := w.Write(body); err != nil {
		// A write failure here is almost always the client going away, which is not a
		// server problem and not worth an error-level log line.
		log.Debug("writing response failed", "error", err)
	}
}

// Problem is an RFC 9457 problem detail, the industry's standard error shape.
//
// Type and Instance are the two fields people leave out and then wish they had: Type is a
// stable identifier a client can branch on, and Instance ties the response to a log line.
type Problem struct {
	Type     string `json:"type,omitempty"`
	Title    string `json:"title"`
	Status   int    `json:"status"`
	Detail   string `json:"detail,omitempty"`
	Instance string `json:"instance,omitempty"`

	// Errors carries per-field problems, which RFC 9457 allows as an extension and which
	// is what FastAPI's 422 body provides out of the box.
	Errors []FieldError `json:"errors,omitempty"`
}

// FieldError is one problem with one field.
type FieldError struct {
	Field  string `json:"field"`
	Detail string `json:"detail"`
}

// WriteError sends a problem detail.
//
// The Content-Type is application/problem+json, which is what RFC 9457 specifies. Clients that
// do not know the type still see JSON, because the +json suffix is what the sniffing rules key
// on.
func WriteError(w http.ResponseWriter, log *slog.Logger, status int, title, detail string) {
	WriteProblem(w, log, Problem{
		Title:  title,
		Status: status,
		Detail: detail,
	})
}

// WriteProblem sends a fully specified problem detail.
func WriteProblem(w http.ResponseWriter, log *slog.Logger, p Problem) {
	if p.Status == 0 {
		p.Status = http.StatusInternalServerError
	}
	if p.Title == "" {
		p.Title = http.StatusText(p.Status)
	}

	body, err := json.Marshal(p)
	if err != nil {
		log.Error("encoding problem detail failed", "error", err)
		http.Error(w, http.StatusText(p.Status), p.Status)
		return
	}

	w.Header().Set("Content-Type", "application/problem+json")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(p.Status)

	if _, err := w.Write(body); err != nil {
		log.Debug("writing problem detail failed", "error", err)
	}
}

// WriteValidationError sends a 422 with per-field detail, which is what FastAPI does for free.
//
// 422 rather than 400: 400 means the request was malformed and 422 means it parsed but failed
// validation. Clients can tell "retry with different data" from "your serialiser is broken".
func WriteValidationError(w http.ResponseWriter, log *slog.Logger, fields []FieldError) {
	WriteProblem(w, log, Problem{
		Type:   "https://example.com/problems/validation",
		Title:  "validation failed",
		Status: http.StatusUnprocessableEntity,
		Detail: fmt.Sprintf("%d field(s) failed validation", len(fields)),
		Errors: fields,
	})
}

// NoContent sends a 204 with no body, for a successful DELETE or a PUT with nothing to say.
func NoContent(w http.ResponseWriter) { w.WriteHeader(http.StatusNoContent) }

// Created sends a 201 with a Location header, which is the part people forget. A client that
// just created a resource should not have to guess its URL.
func Created(w http.ResponseWriter, log *slog.Logger, location string, v any) {
	w.Header().Set("Location", location)
	WriteJSON(w, log, http.StatusCreated, v)
}

// Conditional requests
// ====================

// ETag computes a weak entity tag from a body.
//
// Weak rather than strong, denoted by the W/ prefix, because this hashes the serialised bytes
// and two serialisations that differ only in key order are semantically the same document. A
// strong tag promises byte equality, which JSON marshalling does not give you across versions.
//
// FNV rather than SHA-256 because an ETag is a cache key, not a signature: it needs to differ
// when the content differs, and nothing about it is security relevant.
func ETag(body []byte) string {
	const (
		offset = 14695981039346656037
		prime  = 1099511628211
	)

	h := uint64(offset)
	for _, b := range body {
		h ^= uint64(b)
		h *= prime
	}

	return `W/"` + strconv.FormatUint(h, 36) + `"`
}

// MatchesETag reports whether the client already has this version, by checking If-None-Match.
//
// The comparison has to handle a comma-separated list and the "*" wildcard, and it has to
// compare WEAK tags weakly, meaning W/"x" matches "x". Getting that wrong means either never
// serving a 304 or serving one when the content changed.
func MatchesETag(r *http.Request, etag string) bool {
	header := r.Header.Get("If-None-Match")
	if header == "" {
		return false
	}
	if strings.TrimSpace(header) == "*" {
		return true
	}

	want := strings.TrimPrefix(etag, "W/")

	for _, candidate := range strings.Split(header, ",") {
		candidate = strings.TrimSpace(candidate)
		if strings.TrimPrefix(candidate, "W/") == want {
			return true
		}
	}

	return false
}

// WriteJSONWithETag sends v as JSON, or a 304 if the client already has it.
//
// The 304 must carry the ETag and must NOT carry a body, and the second part is what people get
// wrong: a 304 with a body is a protocol violation and some proxies respond to it by hanging
// until a timeout.
func WriteJSONWithETag(w http.ResponseWriter, r *http.Request, log *slog.Logger, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		log.Error("encoding response failed", "error", err)
		WriteError(w, log, http.StatusInternalServerError, "internal server error", "")
		return
	}

	etag := ETag(body)
	w.Header().Set("ETag", etag)

	if MatchesETag(r, etag) {
		// No body, and no Content-Length either: RFC 9110 says a 304 carries the headers
		// that would have been sent, minus the ones about the body.
		w.WriteHeader(http.StatusNotModified)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)

	if _, err := w.Write(body); err != nil {
		log.Debug("writing response failed", "error", err)
	}
}

// Streaming
// =========

// ErrStreamingUnsupported means the ResponseWriter cannot flush, so a stream would be buffered
// to completion and defeat the point.
var ErrStreamingUnsupported = errors.New("response: streaming not supported by this writer")

// StreamJSONLines writes one JSON value per line, flushing after each.
//
// The format is newline-delimited JSON rather than a JSON array, and the reason is the whole
// argument for streaming: an array cannot be parsed until its closing bracket arrives, so a
// client gets nothing until the server is finished. One object per line can be processed as it
// arrives.
//
// http.NewResponseController is what makes this work through middleware. A type assertion to
// http.Flusher fails as soon as anything wraps the writer, which is every real service; see
// middleware/'s recorder.
func StreamJSONLines[T any](w http.ResponseWriter, values <-chan T) error {
	controller := http.NewResponseController(w)

	// application/x-ndjson is the registered type for this. Setting it before the first
	// write is the usual ordering rule.
	w.Header().Set("Content-Type", "application/x-ndjson")

	// No Content-Length, because the length is not known. That is what makes the response
	// chunked, and net/http arranges the chunking itself.
	w.WriteHeader(http.StatusOK)

	encoder := json.NewEncoder(w)

	for v := range values {
		if err := encoder.Encode(v); err != nil {
			return fmt.Errorf("encoding stream value: %w", err)
		}

		// Without the flush, net/http buffers until 2 KB or until the handler returns,
		// so a slow stream arrives all at once and the client's timeout fires first.
		if err := controller.Flush(); err != nil {
			if errors.Is(err, http.ErrNotSupported) {
				return ErrStreamingUnsupported
			}
			return fmt.Errorf("flushing stream: %w", err)
		}
	}

	return nil
}

// Content negotiation
// ===================

// Negotiate picks the best available type for the client's Accept header.
//
// A simplified q-value parse: it honours explicit weights, it honours */* and type/*, and it
// prefers the caller's order on a tie, because the server knows which representation is
// cheapest and the client usually does not care.
//
// The stdlib has no content negotiation at all, which is one of the genuinely missing pieces
// compared with a framework.
func Negotiate(r *http.Request, available ...string) string {
	if len(available) == 0 {
		return ""
	}

	header := r.Header.Get("Accept")
	if header == "" {
		// RFC 9110: no Accept means anything is acceptable.
		return available[0]
	}

	bestType, bestQ := "", -1.0

	for _, offer := range available {
		if q := acceptQuality(header, offer); q > bestQ {
			bestType, bestQ = offer, q
		}
	}

	if bestQ <= 0 {
		return "" // nothing acceptable, which deserves a 406
	}

	return bestType
}

// acceptQuality returns the q-value the Accept header assigns to one media type, or 0.
func acceptQuality(header, offer string) float64 {
	offerType, offerSub, _ := strings.Cut(offer, "/")

	best := 0.0

	for _, part := range strings.Split(header, ",") {
		media, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		media = strings.TrimSpace(media)

		q := 1.0
		for _, param := range strings.Split(params, ";") {
			name, value, found := strings.Cut(strings.TrimSpace(param), "=")
			if !found || strings.TrimSpace(name) != "q" {
				continue
			}
			if parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64); err == nil {
				q = parsed
			}
		}

		mediaType, mediaSub, _ := strings.Cut(media, "/")

		matches := media == "*/*" ||
			(mediaType == offerType && mediaSub == "*") ||
			(mediaType == offerType && mediaSub == offerSub)

		if matches && q > best {
			best = q
		}
	}

	return best
}
