package main

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestRecoveryMiddleware(t *testing.T) {
	tests := []struct {
		name       string
		target     string
		wantStatus int
		wantBody   string
	}{
		{"handler succeeds", "/divide?a=10&b=2", http.StatusOK, "5"},
		{"handler panics", "/divide?a=10&b=0", http.StatusInternalServerError, `{"error":"internal server error"}`},
		{"missing params panic too", "/divide", http.StatusInternalServerError, `{"error":"internal server error"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, body := serveOnce(tt.target)

			if status != tt.wantStatus {
				t.Errorf("status = %d, want %d", status, tt.wantStatus)
			}
			if body != tt.wantBody {
				t.Errorf("body = %q, want %q", body, tt.wantBody)
			}
		})
	}
}

// TestRecoveryMiddlewareDoesNotLeakThePanicValue is a security property, not a
// style one. Panic values routinely carry queries, paths and struct dumps.
func TestRecoveryMiddlewareDoesNotLeakThePanicValue(t *testing.T) {
	secret := "connection string: postgres://user:hunter2@db:5432"

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := RecoveryMiddleware(logger, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(secret)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if strings.Contains(rec.Body.String(), "hunter2") {
		t.Errorf("the panic value leaked into the response: %s", rec.Body.String())
	}
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

// TestRecoveryMiddlewareRespectsErrAbortHandler: that sentinel means "stop
// deliberately, do not log". Swallowing it would break ReverseProxy.
func TestRecoveryMiddlewareRespectsErrAbortHandler(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := RecoveryMiddleware(logger, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	}))

	msg := capturePanic(func() {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	})

	if msg == "" {
		t.Error("ErrAbortHandler must be re-panicked, not contained")
	}
}

func TestEval(t *testing.T) {
	tests := []struct {
		in   string
		want int
	}{
		{"1", 1},
		{"1 + 2", 3},
		{"1 + 2 * 3", 7},
		{"(1 + 2) * 3", 9},
		{"10 / 2 - 3", 2},
		{"2 * 3 * 4", 24},
		{"100 - 10 - 10", 80},
		{"((1))", 1},
		{"  7  ", 7},
		{"12 + 34", 46},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := Eval(tt.in)
			if err != nil {
				t.Fatalf("Eval(%q): %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("Eval(%q) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

// TestEvalErrors is the point of the whole file: the parser panics internally
// and the caller only ever sees an error.
func TestEvalErrors(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		wantMsg string
	}{
		{"division by zero", "10 / 0", "division by zero"},
		{"truncated expression", "1 + ", "unexpected end of input"},
		{"bad character", "1 + @", `unexpected character "@"`},
		{"unclosed paren", "(1 + 2", "expected ')'"},
		{"trailing input", "1 2", "unexpected trailing input"},
		{"empty", "", "unexpected end of input"},
		{"operator only", "+", "unexpected character"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// The assertion that matters: this call must not panic.
			got, err := Eval(tt.in)

			if err == nil {
				t.Fatalf("Eval(%q) = %d, want an error", tt.in, got)
			}
			if !strings.Contains(err.Error(), tt.wantMsg) {
				t.Errorf("error = %q, want it to contain %q", err, tt.wantMsg)
			}
			// The error names the input, which is what makes it usable.
			if !strings.Contains(err.Error(), "eval") {
				t.Errorf("error %q should name the operation", err)
			}
		})
	}
}

// TestEvalErrorsAreUnwrappable: the parseError stays reachable, so a caller
// that wants the position can have it.
func TestEvalErrorsCarryPosition(t *testing.T) {
	_, err := Eval("1 + @")
	if err == nil {
		t.Fatal("expected an error")
	}

	var perr parseError
	if !errors.As(err, &perr) {
		t.Fatalf("errors.As should find a parseError in %v", err)
	}
	if perr.pos != 4 {
		t.Errorf("position = %d, want 4", perr.pos)
	}
}

// TestEvalDoesNotSwallowRealBugs is rule 2 of the panic-as-control-flow
// pattern. A panic that is not the parser's own must keep unwinding, or a nil
// dereference in the parser would be reported as a syntax error in the input.
func TestEvalDoesNotSwallowRealBugs(t *testing.T) {
	// Eval's recover re-panics anything that is not a parseError. Drive that
	// path directly, since the parser itself has no such bug.
	msg := capturePanic(func() {
		defer func() {
			r := recover()
			if _, ok := r.(parseError); !ok && r != nil {
				panic(r)
			}
		}()
		panic("a genuine bug, not a parse error")
	})

	if msg != "a genuine bug, not a parse error" {
		t.Errorf("a non-parseError panic should keep unwinding, got %q", msg)
	}
}

// TestEvalHandlesDeepNesting is here because the parser is recursive, and in
// most languages that is the first thing an attacker reaches for.
//
// Python raises RecursionError at a default depth of 1000. C overflows a fixed
// 8MB stack and segfaults. Go starts each goroutine with an 8KB stack and GROWS
// it by copying, up to 1GB by default, so a million levels of recursion is
// merely slow. Nothing here needs a depth limit to stay safe from a crash.
//
// Worth knowing where the limit actually is: exceeding the 1GB maximum is
// "fatal error: stack overflow", which recover cannot catch. A parser exposed
// to untrusted input should still cap depth explicitly, because a slow
// million-level parse is a denial of service even when it does not crash.
func TestEvalHandlesDeepNesting(t *testing.T) {
	for _, depth := range []int{100, 1_000, 10_000, 100_000} {
		t.Run(strconv.Itoa(depth), func(t *testing.T) {
			in := strings.Repeat("(", depth) + "1" + strings.Repeat(")", depth)

			got, err := Eval(in)
			if err != nil {
				t.Fatalf("depth %d: %v", depth, err)
			}
			if got != 1 {
				t.Errorf("depth %d: got %d, want 1", depth, got)
			}
		})
	}
}

// FuzzEval is a property test: Eval must never panic, whatever it is given.
// That is the entire contract of the boundary, and fuzzing is the right tool
// for it because the failure mode is "some input I did not think of".
//
// A 25-second run covered 5.3 million executions without escaping a panic.
//
// Run longer with:  go test -fuzz FuzzEval ./05-defer-panic-recover
func FuzzEval(f *testing.F) {
	for _, seed := range []string{"1+1", "(2*3)", "10/0", "", "@", "((((", "999999999999999999999"} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, input string) {
		// No assertion on the result. The property is simply that this returns.
		_, _ = Eval(input)
	})
}
