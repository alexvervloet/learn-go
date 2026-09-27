// Package webhooks is receiving a webhook you did not send, which is a security problem before it is an
// integration problem.
//
// # The threat model
//
// A webhook endpoint is a URL on the public internet that performs privileged actions on behalf of a third
// party. "Payment succeeded, mark the order paid" is an endpoint that anyone who finds the URL can call. So
// every real webhook provider signs its requests, and the receiver's job is to verify the signature before
// looking at the body.
//
// Four things have to be checked and the usual implementation checks one:
//
//	the SIGNATURE, so the body came from someone holding the secret
//	the TIMESTAMP, so a captured request cannot be replayed a month later
//	the signature is compared in CONSTANT TIME, so the comparison does not leak the expected value
//	the EVENT ID has not been processed before, because providers retry and at-least-once delivery
//	  means duplicates are normal traffic rather than an attack
//
// # Why constant time is not paranoia here
//
// `==` on two byte slices in Go stops at the first differing byte. An attacker who can send many requests and
// measure the response time can therefore find the expected signature one byte at a time: 256 requests per
// byte, 64 bytes, about 16,000 requests to forge a signature for a body of their choosing. That is an
// afternoon.
//
// Over the internet the timing signal is buried in network noise and the attack needs far more samples, which
// is why this is often dismissed. `hmac.Equal` is one function call and removes the argument entirely.
//
// # What the payload is signed over
//
// The RAW BODY, byte for byte, before any parsing. This is the detail that breaks implementations: read the
// body, unmarshal it into a struct, re-marshal it to verify, and the signature fails because Go reordered the
// keys. The body has to be read once, kept, verified, and only then parsed, which is what VerifyRequest below
// does and why it returns the bytes.
package webhooks

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Errors a caller needs to tell apart.
//
// Separate sentinels because the right response differs: a bad signature is a 401 and worth alerting on, a
// stale timestamp is a 400 that is probably a clock problem, and a duplicate is a 200 because the provider
// did nothing wrong and will keep retrying until it gets one.
var (
	ErrNoSignature      = errors.New("no signature header")
	ErrBadSignature     = errors.New("signature does not match")
	ErrMalformedHeader  = errors.New("malformed signature header")
	ErrStaleTimestamp   = errors.New("timestamp outside the tolerance")
	ErrFutureTimestamp  = errors.New("timestamp is in the future")
	ErrBodyTooLarge     = errors.New("body too large")
	ErrAlreadyProcessed = errors.New("event already processed")
)

// Verifier checks incoming webhook requests.
type Verifier struct {
	// Secrets is every secret that may have signed the request, newest first.
	//
	// A SLICE, not a string, and that is the whole reason secret rotation is possible. Rotating a
	// single-secret webhook means a window where either the old or the new secret is rejected, so
	// either the provider's queue backs up or events are lost. With a list, both are accepted during
	// the overlap and the old one is removed afterwards.
	Secrets []string

	// Tolerance is how far the timestamp may be from now. Five minutes is the usual figure and it is
	// a compromise: shorter rejects requests delayed by a provider's retry queue, longer widens the
	// replay window.
	Tolerance time.Duration

	// MaxBody caps what will be read. Without it, a request with no Content-Length and an endless
	// body reads until the process runs out of memory, and the signature is never even checked
	// because the read never finishes.
	MaxBody int64

	// Now is the clock, injectable so the timestamp tests do not sleep.
	Now func() time.Time

	// Header names, so one Verifier can serve providers that disagree about them. Stripe uses
	// Stripe-Signature with both values in one header; GitHub uses X-Hub-Signature-256 for the
	// signature and no timestamp at all.
	SignatureHeader string
	TimestampHeader string
}

// NewVerifier builds one with defensible defaults.
func NewVerifier(secrets ...string) *Verifier {
	return &Verifier{
		Secrets:         secrets,
		Tolerance:       5 * time.Minute,
		MaxBody:         1 << 20,
		Now:             time.Now,
		SignatureHeader: "X-Signature-256",
		TimestampHeader: "X-Signature-Timestamp",
	}
}

func (v *Verifier) now() time.Time {
	if v.Now == nil {
		return time.Now()
	}
	return v.Now()
}

// Sign produces the signature a sender would send.
//
// Exported because it is what a TEST needs, and because a service that both sends and receives webhooks needs
// both halves. Having the signing code next to the verifying code is also the only way to keep the signed
// string identical in both, which is where these implementations go wrong.
//
// The signed payload is `timestamp.body`, which is Stripe's scheme. The timestamp has to be INSIDE the signed
// data: a signature over the body alone can be replayed with any timestamp, so including the header in the
// MAC is what makes the timestamp check meaningful rather than advisory.
func Sign(secret string, timestamp time.Time, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))

	// Unix seconds, and the separator matters. Without one, timestamp 1 with body "23" and timestamp
	// 12 with body "3" sign the same bytes, which is a length-extension-flavoured ambiguity that a
	// single byte removes.
	_, _ = mac.Write([]byte(strconv.FormatInt(timestamp.Unix(), 10)))
	_, _ = mac.Write([]byte("."))
	_, _ = mac.Write(body)

	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// Verify checks a signature against a body and timestamp.
//
// Returns the error rather than a bool, so the caller can respond differently to each failure, and takes the
// body as bytes rather than a Reader because it must be the RAW body and a Reader invites reading it twice.
func (v *Verifier) Verify(signature string, timestamp time.Time, body []byte) error {
	if signature == "" {
		return ErrNoSignature
	}

	if len(v.Secrets) == 0 {
		return errors.New("webhooks: no secrets configured")
	}

	// The timestamp first, because it is cheap and because a stale request should not have its
	// signature checked at all: doing the HMAC anyway is work an attacker can make you do.
	if err := v.checkTimestamp(timestamp); err != nil {
		return err
	}

	// Every configured secret is tried, and the loop does NOT return early on a match. Returning
	// early leaks which secret matched through timing, which for a two-secret rotation is a single
	// bit and not worth the branch. Keeping a flag costs nothing.
	//
	// This is also why matched is a bool set with |= rather than a return: an early return inside a
	// loop over secrets is the constant-time mistake one level up from using ==.
	matched := false

	for _, secret := range v.Secrets {
		expected := Sign(secret, timestamp, body)

		// hmac.Equal, not ==. It compares every byte regardless of where the first difference
		// is, so the time taken does not depend on how much of the signature was correct.
		if hmac.Equal([]byte(expected), []byte(signature)) {
			matched = true
		}
	}

	if !matched {
		return ErrBadSignature
	}

	return nil
}

func (v *Verifier) checkTimestamp(timestamp time.Time) error {
	if v.Tolerance <= 0 {
		return nil
	}

	now := v.now()
	skew := now.Sub(timestamp)

	// Both directions, and the future case is separate. A timestamp far in the future is not a
	// replay, it is a clock problem at the sender or a forged header, and the two need different
	// investigation. Checking only `now - ts > tolerance` accepts a timestamp from the year 3000,
	// which then never goes stale.
	if skew > v.Tolerance {
		return fmt.Errorf("%w: %v old, tolerance is %v", ErrStaleTimestamp,
			skew.Round(time.Second), v.Tolerance)
	}

	if -skew > v.Tolerance {
		return fmt.Errorf("%w: %v ahead", ErrFutureTimestamp, (-skew).Round(time.Second))
	}

	return nil
}

// VerifyRequest reads, verifies and returns the body of an HTTP request.
//
// The order is the point of the function. Read the body ONCE into memory, bounded; verify the bytes; return
// them. A handler that parses first and verifies later is verifying something the sender did not sign.
func (v *Verifier) VerifyRequest(r *http.Request) ([]byte, error) {
	limit := v.MaxBody
	if limit <= 0 {
		limit = 1 << 20
	}

	// MaxBytesReader rather than io.LimitReader: LimitReader stops silently at the limit, so an
	// oversized body arrives TRUNCATED and fails signature verification with a confusing error.
	// MaxBytesReader returns an error, so the response can say what happened.
	body, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, limit))
	if err != nil {
		return nil, fmt.Errorf("%w: limit is %d bytes: %w", ErrBodyTooLarge, limit, err)
	}

	tsHeader := r.Header.Get(v.TimestampHeader)
	if tsHeader == "" {
		return body, fmt.Errorf("%w: no %s", ErrMalformedHeader, v.TimestampHeader)
	}

	seconds, err := strconv.ParseInt(tsHeader, 10, 64)
	if err != nil {
		return body, fmt.Errorf("%w: %s is not a unix timestamp: %w",
			ErrMalformedHeader, v.TimestampHeader, err)
	}

	if err := v.Verify(r.Header.Get(v.SignatureHeader), time.Unix(seconds, 0), body); err != nil {
		return body, err
	}

	return body, nil
}

// ParseStripeStyle splits a header that carries the timestamp and one or more signatures together.
//
// Stripe's format is `t=1614556800,v1=abc...,v1=def...`, with several v1 values during a rotation. Worth
// implementing because it is the format most people meet first, and because the multiple-signature case is the
// part that gets dropped.
func ParseStripeStyle(header string) (timestamp time.Time, signatures []string, err error) {
	if header == "" {
		return time.Time{}, nil, ErrNoSignature
	}

	var seconds int64
	seen := false

	for _, part := range strings.Split(header, ",") {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			return time.Time{}, nil, fmt.Errorf("%w: %q has no '='", ErrMalformedHeader, part)
		}

		switch key {
		case "t":
			seconds, err = strconv.ParseInt(value, 10, 64)
			if err != nil {
				return time.Time{}, nil, fmt.Errorf("%w: bad t=%q: %w",
					ErrMalformedHeader, value, err)
			}
			seen = true

		case "v1":
			signatures = append(signatures, value)

		default:
			// Unknown keys are ignored rather than rejected, because a provider adding a
			// v2 must not break every existing receiver. Being liberal about extra fields
			// and strict about the ones you use is the rule that keeps integrations
			// working.
		}
	}

	if !seen {
		return time.Time{}, nil, fmt.Errorf("%w: no t= field", ErrMalformedHeader)
	}
	if len(signatures) == 0 {
		return time.Time{}, nil, fmt.Errorf("%w: no v1= field", ErrMalformedHeader)
	}

	return time.Unix(seconds, 0), signatures, nil
}

// Deduper remembers processed event IDs.
//
// # Why this is not optional
//
// Every webhook provider delivers at least once, which means duplicates are normal traffic. Stripe retries for
// three days. A handler that charges a card or sends an email on each delivery will do it several times, and
// the trigger is usually the receiver being slow rather than anything unusual.
//
// # Why in-memory is a teaching version and not the answer
//
// This one is a map with a TTL, which is correct for one process and wrong for every real deployment: five
// replicas each have their own map, so a duplicate delivered to a different replica is not caught. The real
// version is a unique constraint on the event ID in the database, inside the same transaction as the work,
// which makes the deduplication and the work atomic. Redis SET NX is the middle option and has the
// crash-between-mark-and-work problem.
//
// The interface here is what matters; swapping the implementation for a table is 20 lines.
type Deduper struct {
	mu   sync.Mutex
	seen map[string]time.Time

	ttl        time.Duration
	now        func() time.Time
	lastSweep  time.Time
	sweepEvery time.Duration
}

// NewDeduper builds one.
//
// The TTL has to be longer than the provider's retry window, or a retry after the entry expires is processed
// again. Stripe retries for three days, which means a three-day TTL, which means this map is not the right
// implementation for Stripe. Making that arithmetic explicit is the point of the parameter.
func NewDeduper(ttl time.Duration) *Deduper {
	return &Deduper{
		seen:       make(map[string]time.Time),
		ttl:        ttl,
		now:        time.Now,
		sweepEvery: ttl / 10,
	}
}

// Seen records an event ID and reports whether it was already there.
//
// One method rather than Has plus Add, because the check and the record have to be atomic. Two calls leave a
// window where two concurrent deliveries of the same event both see "not present" and both proceed, which is
// the bug the deduper exists to prevent, reintroduced by its own API.
func (d *Deduper) Seen(id string) bool {
	now := d.now()

	d.mu.Lock()
	defer d.mu.Unlock()

	d.maybeSweep(now)

	if at, ok := d.seen[id]; ok && now.Sub(at) < d.ttl {
		return true
	}

	d.seen[id] = now

	return false
}

func (d *Deduper) maybeSweep(now time.Time) {
	if d.sweepEvery > 0 && now.Sub(d.lastSweep) < d.sweepEvery {
		return
	}

	d.lastSweep = now

	for id, at := range d.seen {
		if now.Sub(at) >= d.ttl {
			delete(d.seen, id)
		}
	}
}

// Len reports how many IDs are held, so a test can measure the growth.
func (d *Deduper) Len() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.seen)
}

// Handler wires verification and deduplication in front of a handler.
//
// # The status codes, which are the part providers care about
//
//	200  processed, or a duplicate. A duplicate is a success: the provider did nothing wrong and
//	     returning anything else makes it retry forever.
//	400  the request is malformed. The provider will not fix it by retrying.
//	401  the signature is wrong. Worth an alert: either a secret is stale or someone is probing.
//	500  processing failed. The provider SHOULD retry, which is what you want.
//
// The one to get right is 200 for a duplicate. Returning 409 feels more honest and produces an endless retry
// loop, because most providers treat anything outside 2xx as "try again".
func (v *Verifier) Handler(d *Deduper, eventID func([]byte) string, process func([]byte) error) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		body, err := v.VerifyRequest(r)

		switch {
		case err == nil:
			// Verified.

		case errors.Is(err, ErrBadSignature), errors.Is(err, ErrNoSignature):
			http.Error(w, "invalid signature", http.StatusUnauthorized)
			return

		case errors.Is(err, ErrBodyTooLarge):
			http.Error(w, "body too large", http.StatusRequestEntityTooLarge)
			return

		default:
			// Malformed headers, stale or future timestamps. All the sender's problem and
			// none of them fixed by retrying.
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if d != nil && eventID != nil {
			if id := eventID(body); id != "" && d.Seen(id) {
				// 200, and a body saying why, so a human reading the provider's
				// delivery log can tell a duplicate from a fresh success.
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"status":"duplicate"}` + "\n"))
				return
			}
		}

		if err := process(body); err != nil {
			// 500, so the provider retries. The deduper has already recorded the ID, which
			// means the retry will be treated as a duplicate and the work will never happen.
			//
			// That is the crash-between-mark-and-work problem, and it is why the real
			// implementation records the ID in the same transaction as the work rather than
			// before it. Stated here rather than hidden, because the in-memory Deduper cannot
			// fix it.
			http.Error(w, "processing failed", http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}` + "\n"))
	})
}
