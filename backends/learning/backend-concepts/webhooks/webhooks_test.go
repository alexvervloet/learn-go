package webhooks

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"
)

const secret = "whsec_test_9f8e7d6c5b4a"

var fixedTime = time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)

func newTestVerifier() *Verifier {
	v := NewVerifier(secret)
	v.Now = func() time.Time { return fixedTime }
	return v
}

func TestSignAndVerifyRoundTrip(t *testing.T) {
	v := newTestVerifier()

	body := []byte(`{"id":"evt_1","type":"payment.succeeded","amount":1999}`)

	sig := Sign(secret, fixedTime, body)

	t.Logf("signature: %s", sig)

	if err := v.Verify(sig, fixedTime, body); err != nil {
		t.Fatalf("a signature we just made did not verify: %v", err)
	}

	// Any change to the body breaks it, including one that JSON would consider equivalent.
	for _, tampered := range [][]byte{
		[]byte(`{"id":"evt_1","type":"payment.succeeded","amount":19990}`),
		[]byte(`{"id":"evt_1","type":"payment.succeeded","amount":1999} `),
		[]byte(`{"amount":1999,"id":"evt_1","type":"payment.succeeded"}`),
		{},
	} {
		if err := v.Verify(sig, fixedTime, tampered); !errors.Is(err, ErrBadSignature) {
			t.Errorf("body %q verified against the wrong signature: %v", tampered, err)
		}
	}

	t.Log("the third case is the same JSON with reordered keys, and it fails. That is why the " +
		"RAW body has to be signed and verified: unmarshal then re-marshal changes the bytes.")

	// A different timestamp breaks it too, because the timestamp is inside the MAC.
	if err := v.Verify(sig, fixedTime.Add(time.Second), body); err == nil {
		t.Error("the signature verified with a different timestamp, so the timestamp is not " +
			"covered by the MAC and the tolerance check is only advisory")
	}
}

func TestSeparatorPreventsAmbiguity(t *testing.T) {
	// Without a separator between the timestamp and the body, timestamp 1 with body "23" signs the
	// same bytes as timestamp 12 with body "3".
	a := Sign(secret, time.Unix(1, 0), []byte("23"))
	b := Sign(secret, time.Unix(12, 0), []byte("3"))

	t.Logf("t=1  body=23: %s", a)
	t.Logf("t=12 body=3:  %s", b)

	if a == b {
		t.Error("two different (timestamp, body) pairs produced the same signature, so the " +
			"separator is missing")
	}
}

// TestTimestampToleranceBothWays, because checking only one direction accepts a timestamp from the year 3000.
func TestTimestampToleranceBothWays(t *testing.T) {
	v := newTestVerifier()
	v.Tolerance = 5 * time.Minute

	body := []byte(`{"id":"evt_1"}`)

	for _, tc := range []struct {
		name   string
		offset time.Duration
		want   error
	}{
		{"now", 0, nil},
		{"4 minutes old", -4 * time.Minute, nil},
		{"6 minutes old", -6 * time.Minute, ErrStaleTimestamp},
		{"a month old", -30 * 24 * time.Hour, ErrStaleTimestamp},
		{"4 minutes ahead", 4 * time.Minute, nil},
		{"6 minutes ahead", 6 * time.Minute, ErrFutureTimestamp},
		// 200 years, not 975. time.Duration is an int64 count of NANOSECONDS, so it maxes
		// out at about 292 years and `975 * 365 * 24 * time.Hour` does not compile:
		// "constant 30747600000000000000 overflows int64". Worth knowing before writing a
		// timeout in centuries, and a reason to express far-future instants as a time.Time
		// rather than an offset.
		{"200 years ahead", 200 * 365 * 24 * time.Hour, ErrFutureTimestamp},
	} {
		ts := fixedTime.Add(tc.offset)
		sig := Sign(secret, ts, body)

		err := v.Verify(sig, ts, body)

		switch {
		case tc.want == nil && err != nil:
			t.Errorf("%s: %v", tc.name, err)
		case tc.want != nil && !errors.Is(err, tc.want):
			t.Errorf("%s: got %v, want %v", tc.name, err, tc.want)
		default:
			t.Logf("%-16s %v", tc.name, errOrOK(err))
		}
	}
}

// TestSecretRotation is the reason Secrets is a slice.
func TestSecretRotation(t *testing.T) {
	oldSecret, newSecret := "whsec_old", "whsec_new"

	body := []byte(`{"id":"evt_rotate"}`)

	// During the overlap, both are accepted.
	both := NewVerifier(newSecret, oldSecret)
	both.Now = func() time.Time { return fixedTime }

	for name, s := range map[string]string{"old": oldSecret, "new": newSecret} {
		if err := both.Verify(Sign(s, fixedTime, body), fixedTime, body); err != nil {
			t.Errorf("the %s secret was rejected during rotation: %v", name, err)
		}
	}

	t.Log("both secrets accepted during the overlap, so neither the provider's queue backs up " +
		"nor events are lost")

	// Afterwards, only the new one.
	onlyNew := NewVerifier(newSecret)
	onlyNew.Now = func() time.Time { return fixedTime }

	if err := onlyNew.Verify(Sign(oldSecret, fixedTime, body), fixedTime, body); !errors.Is(err, ErrBadSignature) {
		t.Errorf("the old secret still works after rotation: %v", err)
	}

	// And a verifier with no secrets is a configuration mistake, not a request that verifies.
	empty := NewVerifier()
	empty.Now = func() time.Time { return fixedTime }

	if err := empty.Verify(Sign(newSecret, fixedTime, body), fixedTime, body); err == nil {
		t.Error("a verifier with no secrets accepted a request")
	} else {
		t.Logf("no secrets configured: %v", err)
	}
}

// TestNearMissSignaturesAreRejected: a signature matching the expected one for all but its last character is
// rejected exactly like one matching nothing.
//
// This does NOT show the comparison is constant-time: == would pass it too. An earlier version was named
// TestConstantTimeComparison and said it checked that the code calls hmac.Equal, which it did not.
// TestVerifyComparesInConstantTime below checks that, and why a timing measurement is not the way.
func TestNearMissSignaturesAreRejected(t *testing.T) {
	v := newTestVerifier()

	body := []byte(`{"id":"evt_timing"}`)
	expected := Sign(secret, fixedTime, body)

	// Signatures differing from the expected one at increasing positions. All must be rejected
	// identically.
	for _, prefixLen := range []int{7, 20, 40, len(expected) - 1} {
		forged := expected[:prefixLen] + flipHexAt(expected, prefixLen)

		if len(forged) != len(expected) {
			forged = forged + expected[len(forged):]
		}

		err := v.Verify(forged, fixedTime, body)

		if !errors.Is(err, ErrBadSignature) {
			t.Errorf("a signature matching %d characters gave %v, want ErrBadSignature",
				prefixLen, err)
		}
	}

	t.Logf("signatures sharing 7, 20, 40 and %d characters of the expected %d are all rejected "+
		"the same way", len(expected)-1, len(expected))

	// And a signature of the WRONG LENGTH is rejected without a panic, which the naive
	// subtle.ConstantTimeCompare does not guarantee (it returns 0 for unequal lengths, and some
	// hand-rolled loops index out of range).
	for _, wrong := range []string{"", "sha256=", expected + "00", expected[:10]} {
		if err := v.Verify(wrong, fixedTime, body); err == nil {
			t.Errorf("a signature of length %d verified", len(wrong))
		}
	}

	t.Log("hmac.Equal handles unequal lengths, so a truncated or padded signature is a plain " +
		"rejection rather than a panic")
}

// TestVerifyRequestReadsTheBodyOnce is the mistake that makes signatures fail for no visible reason.
func TestVerifyRequestReadsTheBodyOnce(t *testing.T) {
	v := newTestVerifier()

	type payload struct {
		ID     string `json:"id"`
		Type   string `json:"type"`
		Amount int    `json:"amount"`
	}

	body := []byte(`{"id":"evt_1","type":"payment.succeeded","amount":1999}`)

	req := signedRequest(t, v, fixedTime, body)

	got, err := v.VerifyRequest(req)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(got, body) {
		t.Errorf("VerifyRequest returned %q, want %q", got, body)
	}

	// The returned bytes are what to parse, and the parse happens AFTER verification.
	var p payload
	if err := json.Unmarshal(got, &p); err != nil {
		t.Fatal(err)
	}
	if p.ID != "evt_1" || p.Amount != 1999 {
		t.Errorf("parsed %+v", p)
	}

	// The mistake, demonstrated: parse, re-marshal, verify. Go sorts struct fields by declaration and
	// map keys alphabetically, so the bytes change.
	reencoded, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("original:   %s", body)
	t.Logf("re-encoded: %s", reencoded)

	if bytes.Equal(body, reencoded) {
		t.Log("the re-encoded bytes happen to match here; with a map, or any field order " +
			"difference, they would not")
	} else {
		if err := v.Verify(Sign(secret, fixedTime, body), fixedTime, reencoded); err == nil {
			t.Error("the re-encoded body verified, which it should not")
		}
		t.Log("verifying the re-encoded body fails, and the error says 'signature does not " +
			"match', which sends people looking for a wrong secret")
	}
}

// TestBodyLimitIsAnErrorNotATruncation, because a truncated body fails signature verification with a
// misleading error.
func TestBodyLimitIsAnErrorNotATruncation(t *testing.T) {
	v := newTestVerifier()
	v.MaxBody = 64

	big := bytes.Repeat([]byte("x"), 1024)

	req := signedRequest(t, v, fixedTime, big)

	_, err := v.VerifyRequest(req)

	if !errors.Is(err, ErrBodyTooLarge) {
		t.Errorf("got %v, want ErrBodyTooLarge", err)
	}

	t.Logf("a 1 KB body against a 64 byte limit: %v", err)
	t.Log("io.LimitReader would have returned 64 bytes and no error, and the signature check " +
		"would then fail with 'signature does not match'")
}

func TestParseStripeStyle(t *testing.T) {
	for _, tc := range []struct {
		name    string
		header  string
		sigs    int
		wantErr error
	}{
		{"one signature", "t=1748779200,v1=abc123", 1, nil},
		{"two during rotation", "t=1748779200,v1=abc123,v1=def456", 2, nil},
		{"unknown fields ignored", "t=1748779200,v1=abc,v2=future,extra=x", 1, nil},
		{"spaces", " t=1748779200 , v1=abc ", 1, nil},
		{"no timestamp", "v1=abc123", 0, ErrMalformedHeader},
		{"no signature", "t=1748779200", 0, ErrMalformedHeader},
		{"no equals", "t=1748779200,v1", 0, ErrMalformedHeader},
		{"bad timestamp", "t=yesterday,v1=abc", 0, ErrMalformedHeader},
		{"empty", "", 0, ErrNoSignature},
	} {
		ts, sigs, err := ParseStripeStyle(tc.header)

		switch {
		case tc.wantErr != nil:
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("%s: got %v, want %v", tc.name, err, tc.wantErr)
			}
		case err != nil:
			t.Errorf("%s: %v", tc.name, err)
		default:
			if len(sigs) != tc.sigs {
				t.Errorf("%s: got %d signatures, want %d", tc.name, len(sigs), tc.sigs)
			}
			if ts.Unix() != 1748779200 {
				t.Errorf("%s: timestamp %v", tc.name, ts)
			}
			t.Logf("%-24s t=%v, %d signature(s)", tc.name, ts.UTC().Format(time.RFC3339),
				len(sigs))
		}
	}

	t.Log("unknown fields are ignored on purpose: a provider adding a v2 must not break every " +
		"existing receiver")
}

// TestDeduperCatchesRetries.
func TestDeduperCatchesRetries(t *testing.T) {
	clock := fixedTime

	d := NewDeduper(time.Hour)
	d.now = func() time.Time { return clock }

	if d.Seen("evt_1") {
		t.Error("a fresh id was reported as seen")
	}

	for i := range 5 {
		if !d.Seen("evt_1") {
			t.Errorf("retry %d was not caught", i+1)
		}
	}

	if d.Len() != 1 {
		t.Errorf("holding %d ids, want 1", d.Len())
	}

	// A different id is not a duplicate.
	if d.Seen("evt_2") {
		t.Error("a different id was reported as seen")
	}

	// Past the TTL, the id is forgotten, which is why the TTL has to be longer than the provider's
	// retry window.
	clock = clock.Add(2 * time.Hour)

	// Seen reports whether the id was ALREADY there, so a forgotten id gives false. I had these
	// branches the wrong way round first time, which is a hazard of a boolean whose name is a past
	// participle: "seen" reads as a state and returns a question's answer.
	if d.Seen("evt_1") {
		t.Error("the id was still remembered past its TTL")
	} else {
		t.Log("after the TTL the id is forgotten, so the same event would be processed again")
	}

	t.Log("Stripe retries for three days, so a three-day TTL, so this in-memory map is not the " +
		"right implementation for Stripe. A unique constraint on the event id, in the same " +
		"transaction as the work, is.")
}

// TestDeduperIsAtomic, because a Has-then-Add API would reintroduce the bug it exists to prevent.
func TestDeduperIsAtomic(t *testing.T) {
	d := NewDeduper(time.Hour)

	const callers = 200

	var (
		wg        sync.WaitGroup
		firstSeen int
		mu        sync.Mutex
	)

	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()

			if !d.Seen("evt_concurrent") {
				mu.Lock()
				firstSeen++
				mu.Unlock()
			}
		}()
	}

	wg.Wait()

	t.Logf("%d concurrent deliveries of one event: %d were told they were first", callers, firstSeen)

	if firstSeen != 1 {
		t.Errorf("%d callers were told they were first, want exactly 1", firstSeen)
	}
}

// TestDeduperSweeps, so the map does not grow forever.
func TestDeduperSweeps(t *testing.T) {
	clock := fixedTime

	d := NewDeduper(time.Hour)
	d.now = func() time.Time { return clock }

	for i := range 1_000 {
		d.Seen("evt_" + strconv.Itoa(i))
	}

	t.Logf("after 1,000 events: %d ids held", d.Len())

	if d.Len() != 1_000 {
		t.Errorf("holding %d, want 1000", d.Len())
	}

	clock = clock.Add(2 * time.Hour)

	// One more call triggers the sweep.
	d.Seen("evt_new")

	t.Logf("two hours later, after one more event: %d ids held", d.Len())

	if d.Len() != 1 {
		t.Errorf("holding %d after the sweep, want 1", d.Len())
	}
}

// TestHandlerStatusCodes is what the provider actually reacts to.
func TestHandlerStatusCodes(t *testing.T) {
	v := newTestVerifier()
	d := NewDeduper(time.Hour)

	var processed []string

	eventID := func(body []byte) string {
		var e struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(body, &e)
		return e.ID
	}

	failNext := false

	handler := v.Handler(d, eventID, func(body []byte) error {
		if failNext {
			return errors.New("the database is down")
		}
		processed = append(processed, eventID(body))
		return nil
	})

	body := []byte(`{"id":"evt_status","type":"ping"}`)

	// A good request.
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, signedRequest(t, v, fixedTime, body))

	t.Logf("valid request:      %d %s", rec.Code, rec.Body.String())

	if rec.Code != http.StatusOK {
		t.Errorf("got %d, want 200", rec.Code)
	}

	// The same request again, which is a retry.
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, signedRequest(t, v, fixedTime, body))

	t.Logf("duplicate:          %d %s", rec.Code, rec.Body.String())

	if rec.Code != http.StatusOK {
		t.Errorf("a duplicate got %d; anything but 200 makes the provider retry forever",
			rec.Code)
	}
	if len(processed) != 1 {
		t.Errorf("processed %d times, want 1", len(processed))
	}

	// A bad signature.
	bad := signedRequest(t, v, fixedTime, body)
	bad.Header.Set(v.SignatureHeader, "sha256="+hex.EncodeToString(make([]byte, 32)))

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, bad)

	t.Logf("bad signature:      %d", rec.Code)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("got %d, want 401", rec.Code)
	}

	// A stale timestamp.
	stale := signedRequest(t, v, fixedTime.Add(-time.Hour), body)

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, stale)

	t.Logf("stale timestamp:    %d %s", rec.Code, rec.Body.String())

	if rec.Code != http.StatusBadRequest {
		t.Errorf("got %d, want 400", rec.Code)
	}

	// No signature header at all.
	unsigned := httptest.NewRequest("POST", "/hooks", bytes.NewReader(body))
	unsigned.Header.Set(v.TimestampHeader, strconv.FormatInt(fixedTime.Unix(), 10))

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, unsigned)

	t.Logf("no signature:       %d", rec.Code)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("got %d, want 401", rec.Code)
	}

	// A GET.
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/hooks", nil))

	t.Logf("GET:                %d, Allow: %s", rec.Code, rec.Header().Get("Allow"))

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("got %d, want 405", rec.Code)
	}

	// Processing failure: 500, so the provider retries.
	failNext = true

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, signedRequest(t, v, fixedTime, []byte(`{"id":"evt_fails"}`)))

	t.Logf("processing failed:  %d", rec.Code)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("got %d, want 500", rec.Code)
	}

	// And the retry of the failed event is processed, not swallowed as a duplicate. The handler
	// records the id before the work (so two concurrent deliveries cannot both run it) and forgets
	// it when the work fails. The first version did not forget, and this test asserted the loss as
	// if it were unavoidable.
	failNext = false

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, signedRequest(t, v, fixedTime, []byte(`{"id":"evt_fails"}`)))

	t.Logf("its retry:          %d %s", rec.Code, rec.Body.String())

	if rec.Code != http.StatusOK || bytes.Contains(rec.Body.Bytes(), []byte("duplicate")) {
		t.Errorf("the retry of a failed event got %d %s; it must be processed, or the event is lost",
			rec.Code, rec.Body.String())
	}
	if processed[len(processed)-1] != "evt_fails" {
		t.Errorf("processed %v; the retried event never ran", processed)
	}
}

// TestBodyIsReturnedEvenOnFailure, so a handler can log what it rejected.
func TestBodyIsReturnedEvenOnFailure(t *testing.T) {
	v := newTestVerifier()

	body := []byte(`{"id":"evt_rejected"}`)

	req := signedRequest(t, v, fixedTime, body)
	req.Header.Set(v.SignatureHeader, "sha256=deadbeef")

	got, err := v.VerifyRequest(req)

	if err == nil {
		t.Fatal("expected a signature failure")
	}

	if !bytes.Equal(got, body) {
		t.Errorf("the body was not returned on failure: %q", got)
	}

	t.Log("the body comes back even when verification fails, so a handler can log what it " +
		"rejected without reading a consumed reader")
}

// signedRequest builds a correctly signed POST.
func signedRequest(t *testing.T, v *Verifier, ts time.Time, body []byte) *http.Request {
	t.Helper()

	req := httptest.NewRequest("POST", "/hooks", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(v.TimestampHeader, strconv.FormatInt(ts.Unix(), 10))
	req.Header.Set(v.SignatureHeader, Sign(secret, ts, body))

	return req
}

// flipHexAt returns a hex character different from the one at i.
func flipHexAt(s string, i int) string {
	if i >= len(s) {
		return "0"
	}
	if s[i] == '0' {
		return "1"
	}
	return "0"
}

func errOrOK(err error) string {
	if err == nil {
		return "accepted"
	}
	return err.Error()
}

// A compile-time check that the signing helper and crypto/hmac agree, so a refactor of Sign cannot silently
// change the scheme.
func TestSignMatchesHandRolledHMAC(t *testing.T) {
	body := []byte("the body")

	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(strconv.FormatInt(fixedTime.Unix(), 10) + "." + string(body)))

	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	if got := Sign(secret, fixedTime, body); got != want {
		t.Errorf("Sign gave\n  %s\nwant\n  %s", got, want)
	}
}

// TestVerifyComparesInConstantTime checks the source rather than the clock.
//
// A timing test would be a bad test: a nanosecond difference measured from Go, on a machine with a scheduler and a
// turbo clock, varies more between runs than between == and hmac.Equal. What can be checked is that Verify
// compares signatures with a constant-time function and never with == or !=. go/ast makes that a real check
// rather than a comment.
func TestVerifyComparesInConstantTime(t *testing.T) {
	fset := token.NewFileSet()

	file, err := parser.ParseFile(fset, "webhooks.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	var verify *ast.FuncDecl
	for _, d := range file.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Name.Name == "Verify" && fn.Recv != nil {
			verify = fn
		}
	}
	if verify == nil {
		t.Fatal("no Verify method in webhooks.go")
	}

	constantTime := false

	ast.Inspect(verify.Body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.CallExpr:
			if sel, ok := n.Fun.(*ast.SelectorExpr); ok {
				name := fmt.Sprint(sel.X) + "." + sel.Sel.Name
				if name == "hmac.Equal" || name == "subtle.ConstantTimeCompare" {
					constantTime = true
				}
			}
		case *ast.BinaryExpr:
			// Comparing against a literal (signature == "") tests for absence and leaks nothing. What
			// must not happen is comparing the signature with another computed value.
			_, xLit := n.X.(*ast.BasicLit)
			_, yLit := n.Y.(*ast.BasicLit)
			if (n.Op == token.EQL || n.Op == token.NEQ) && !xLit && !yLit {
				for _, side := range []ast.Expr{n.X, n.Y} {
					if id, ok := side.(*ast.Ident); ok && (id.Name == "signature" || id.Name == "expected") {
						t.Errorf("%s: Verify compares %s with %s", fset.Position(n.Pos()), id.Name, n.Op)
					}
				}
			}
		}
		return true
	})

	if !constantTime {
		t.Error("Verify never calls hmac.Equal or subtle.ConstantTimeCompare")
	}
}
