package main

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
)

// Recovering at a boundary
// ========================
//
// The three legitimate uses of recover are all at an edge where "this one unit
// of work failed" is meaningfully different from "the program is broken":
//
//	an HTTP handler      -> 500 for this request, keep serving
//	a worker pool job    -> mark the job failed, keep the pool
//	a package's own API  -> convert an internal panic to an error
//
// Anywhere else, recovering keeps a corrupted program running.

// RecoveryMiddleware contains a panicking handler. net/http already does this
// for you, which is worth knowing before writing your own: the default server
// recovers per connection, logs the stack, and closes the connection.
//
// A hand-written one is still worth having, because the stdlib's version closes
// the connection WITHOUT writing a response, so the client sees a dropped
// connection rather than a 500. This one writes the status.
func RecoveryMiddleware(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			rec := recover()
			if rec == nil {
				return
			}

			// http.ErrAbortHandler is a sentinel panic value meaning "stop,
			// deliberately, and do not log this". httputil.ReverseProxy uses it.
			// Re-panicking lets the server handle it as intended.
			// A direct comparison, not errors.Is: the panic VALUE is compared,
			// and a panic value is `any`, not an error chain. errors.Is would
			// need rec to be an error in the first place, which a panic value
			// generally is not.
			//
			//nolint:errorlint // comparing a panic value, not unwrapping an error
			if rec == http.ErrAbortHandler {
				panic(rec)
			}

			logger.Error("handler panicked",
				"panic", rec,
				"method", r.Method,
				"path", r.URL.Path,
			)

			// Never put the panic value in the response. It routinely contains
			// a query, a file path, or a struct dump, and this goes to the
			// client. A generic message plus a logged detail is the trade.
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"internal server error"}`))
		}()

		next.ServeHTTP(w, r)
	})
}

// divideHandler panics on a zero divisor, standing in for any handler with a
// bug in it.
func divideHandler(w http.ResponseWriter, r *http.Request) {
	a, _ := strconv.Atoi(r.URL.Query().Get("a"))
	b, _ := strconv.Atoi(r.URL.Query().Get("b"))

	// No guard on b. Dividing by zero panics, and the middleware catches it.
	// The write error is discarded here because the handler has no way to
	// report it: the client has gone, and the status line is already sent.
	// Real handlers log it. See lesson 13 for what a short write means.
	_, _ = fmt.Fprintf(w, "%d", a/b)
}

// serveOnce wires the middleware around the handler and runs one request
// through httptest, which needs no port and no cleanup.
func serveOnce(target string) (status int, body string) {
	// A logger writing nowhere, so the demo output stays readable. A real
	// service passes slog.Default().
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	h := RecoveryMiddleware(logger, http.HandlerFunc(divideHandler))

	req := httptest.NewRequest(http.MethodGet, target, nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	res := rec.Result()
	defer res.Body.Close() //nolint:errcheck // in-memory body, Close cannot fail

	return res.StatusCode, strings.TrimSpace(rec.Body.String())
}

// Panic as internal control flow
// ==============================
//
// A recursive parser threading an error return through every level obscures the
// grammar. Panicking with a private type and recovering at the public entry
// point keeps the recursion readable, and callers never see a panic.
//
// encoding/json does this. So does text/template. The rules that make it safe:
//
//  1. The panic value is an UNEXPORTED type, so recover can identify it.
//  2. Anything else re-panics, so real bugs are not swallowed.
//  3. The panic never crosses the package boundary.

// parseError is the private panic value. Being unexported means no other
// package can produce one, so recovering on it cannot catch someone else's bug.
type parseError struct {
	pos int
	msg string
}

func (e parseError) Error() string {
	return fmt.Sprintf("position %d: %s", e.pos, e.msg)
}

// exprParser evaluates a tiny arithmetic grammar over integers:
//
//	expr   = term { ("+" | "-") term }
//	term   = factor { ("*" | "/") factor }
//	factor = digit+ | "(" expr ")"
type exprParser struct {
	input string
	pos   int
}

// fail raises a parseError. Every level of the recursion calls this instead of
// returning an error, which is what keeps the grammar functions one line each.
func (p *exprParser) fail(format string, args ...any) {
	panic(parseError{pos: p.pos, msg: fmt.Sprintf(format, args...)})
}

func (p *exprParser) peek() byte {
	for p.pos < len(p.input) && p.input[p.pos] == ' ' {
		p.pos++
	}
	if p.pos >= len(p.input) {
		return 0
	}
	return p.input[p.pos]
}

func (p *exprParser) expr() int {
	v := p.term()
	for {
		switch p.peek() {
		case '+':
			p.pos++
			v += p.term()
		case '-':
			p.pos++
			v -= p.term()
		default:
			return v
		}
	}
}

func (p *exprParser) term() int {
	v := p.factor()
	for {
		switch p.peek() {
		case '*':
			p.pos++
			v *= p.factor()
		case '/':
			p.pos++
			d := p.factor()
			if d == 0 {
				p.fail("division by zero")
			}
			v /= d
		default:
			return v
		}
	}
}

func (p *exprParser) factor() int {
	switch c := p.peek(); {
	case c == '(':
		p.pos++
		v := p.expr()
		if p.peek() != ')' {
			p.fail("expected ')'")
		}
		p.pos++
		return v
	case c >= '0' && c <= '9':
		start := p.pos
		for p.pos < len(p.input) && p.input[p.pos] >= '0' && p.input[p.pos] <= '9' {
			p.pos++
		}
		n, _ := strconv.Atoi(p.input[start:p.pos])
		return n
	case c == 0:
		p.fail("unexpected end of input")
		return 0
	default:
		p.fail("unexpected character %q", string(c))
		return 0
	}
}

// Eval is the public entry point, and the only place recover appears. Callers
// get an error; the panic never escapes this function.
func Eval(input string) (result int, err error) {
	defer func() {
		r := recover()
		if r == nil {
			return
		}

		// Rule 2: only OUR panic type is converted. A nil dereference caused by
		// a bug in the parser must still crash, loudly, rather than being
		// reported as a syntax error in the user's input.
		perr, ok := r.(parseError)
		if !ok {
			panic(r)
		}
		err = fmt.Errorf("eval %q: %w", input, perr)
	}()

	p := &exprParser{input: input}
	result = p.expr()

	if p.peek() != 0 {
		p.fail("unexpected trailing input")
	}

	return result, nil
}

// demoBoundaries prints the middleware and the parser.
func demoBoundaries() {
	for _, target := range []string{"/divide?a=10&b=2", "/divide?a=10&b=0"} {
		status, body := serveOnce(target)
		fmt.Printf("  GET %-20s -> %d  %s\n", target, status, body)
	}
	fmt.Println("  ...the process is still running, and the panic value never reached the client")

	fmt.Println()
	for _, in := range []string{
		"1 + 2 * 3",
		"(1 + 2) * 3",
		"10 / 2 - 3",
		"10 / 0",
		"1 + ",
		"1 + @",
		"(1 + 2",
		"1 2",
	} {
		v, err := Eval(in)
		if err != nil {
			fmt.Printf("  Eval(%-12q) -> %v\n", in, err)
			continue
		}
		fmt.Printf("  Eval(%-12q) -> %d\n", in, v)
	}
}
