# http-tutorial

> 📚 [Repository root](../../../README.md) · **Core path · step 1 of 4** · Next: [testing-concepts ➡](../testing-concepts/)

The mirror of the Python repo's `fast-api-tutorial/`, and the module where the two languages
differ most.

FastAPI reads your function signature, derives a schema, validates the request, coerces the
types, and returns a 422 with field-by-field detail before your handler runs. Go has none of
that. What it has instead is `net/http`, which since **Go 1.22** does enough routing that the
first question in every Go tutorial ("which router?") mostly went away.

So this module is about two things: what the standard library now does for you, and what you
write by hand because nothing will write it for you.

```
routing/     ServeMux since 1.22: methods, wildcards, precedence, the redirect rules
middleware/  the func(http.Handler) http.Handler convention, and chi's library
request/     decoding: JSON, query, forms, uploads, headers, cookies
response/    writing: JSON, problem+json errors, ETags, streaming, negotiation
server/      timeouts that stop Slowloris, and shutdown that does not drop requests
cmd/server/  all of it wired into something you can curl
```

## Routing: what 1.22 changed

```go
mux.HandleFunc("GET /items/{id}", h)      // a method and a path variable
mux.HandleFunc("GET /items/{$}", h)       // EXACTLY /items/, not the subtree
mux.HandleFunc("GET /files/{path...}", h) // multi-segment, must be last
```

`r.PathValue("id")` reads the variable. No third-party router, no context key, no type
assertion.

**Precedence is by specificity, not registration order.** `GET /items/latest` beats
`GET /items/{id}` whichever was registered first, because it matches a strict subset.
`TestPrecedenceIsBySpecificityNotRegistrationOrder` runs both orders and gets the same answer.

**Conflicting patterns panic at registration.** Two patterns that overlap with neither being
more specific cannot be resolved, so the mux refuses to start. That is the best thing about the
new router: a router that silently picks one produces a bug for certain URLs only, and one that
refuses to start cannot be deployed.

### Four things about the trailing-slash redirect, and I had all four wrong

| | |
|---|---|
| The redirect is **307**, not 301 | 307 preserves the method. A 301 lets clients turn a POST into a GET, which is how that classic bug happens. |
| `{$}` does **not** prevent it | `GET /exact/{$}` still redirects `/exact` → `/exact/`. All `{$}` does is stop matching *below* `/exact/`. |
| It is method-aware | With only `GET /subtree/` registered, `POST /subtree` gives **405**, not a redirect. A pattern with no method redirects any method. |
| It only ever **adds** a slash | Pattern `/plain` does not redirect `/plain/` → `/plain`. That is a 404. |

Two more, both free and both worth knowing:

- Registering `GET` registers **HEAD** too, and `Allow` on a 405 lists it: `DELETE, GET, HEAD`.
- Wildcards are **URL-decoded**. `/f/a%2Fb` matches `GET /f/{name}` as one segment and
  `PathValue` returns `a/b`. A handler that joins that onto a directory has a path-traversal
  bug.

`r.PathValue` of a wildcard that does not exist returns `""` with no error, so a typo in the
name is silent. That is the one place this API is worse than a router returning a map.

## Middleware: one convention, no API

```go
func(http.Handler) http.Handler
```

That is the whole contract. It is not declared in the standard library, and every Go HTTP
library agrees on it anyway, which is why chi's middleware works with the stdlib's router.

`Chain(a, b, c)(h)` is `a(b(c(h)))`: `a` outermost. The loop runs **backwards**, because each
step wraps what was built so far.

### Logging goes outside recovery, which is the opposite of the usual advice

"Recovery must be the outermost middleware" is the rule everyone repeats. Against a logger it is
backwards, measured:

| order | log lines on a panicking request |
|---|---|
| Recovery outside Logger | **1** — the panic. The request line is lost. |
| Logger outside Recovery | **2** — the panic, and the request line with its 500. |

The panic never reaches a logger that sits inside the recovery, so the request you most wanted
logged is the one that produces no request line.

The argument for Recovery outermost is real but different: it catches panics in other
*middleware*. Nothing catches a panic in whatever sits outside it.

### The ResponseWriter wrapping problem

Logging a status code needs the status code, and `http.ResponseWriter` cannot be read back. So a
logging middleware wraps the writer — and that wrapper hides `http.Flusher`, `http.Hijacker` and
`io.ReaderFrom`. Streaming stops flushing, WebSocket upgrades stop working, `sendfile` turns into
a byte copy.

Go 1.20's `http.ResponseController` is half the fix. It walks `Unwrap` until it finds a writer
that can do the job, so this one method makes `http.NewResponseController(w).Flush()` work through
the wrapper:

```go
func (r *recorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }
```

That covers code you write. It doesn't cover the middleware you import. chi's `Compress` still does
`w.(http.Flusher)` on the writer it wraps, and inside `Production` that writer is the recorder. The
first version of this package had only `Unwrap`, and `/stream` delivered every line at the end while
WebSocket upgrades failed with "http.Hijacker is unavailable".

So the recorder has `Unwrap` and also `Flush` and `Hijack` methods, each delegating through
`ResponseController` so they work when the writer underneath is a wrapper too.
`TestProductionStreams` reads the first line of a stream before the handler sends the second, and
`TestProductionCanHijack` takes over the connection, both through the full `Production` chain.

### chi: use the middleware, skip the router

Since 1.22 chi's router is largely redundant. Its middleware is not: `Compress`, `Throttle`,
`RealIP` and `WrapResponseWriter` are all things there is no reason to write yourself.

**Two of them had to be replaced, and both were found by tooling rather than by reading.**

`chimw.CleanPath` **panics** with a nil pointer on every request when there is no chi router in
the chain, because it writes into `chi.RouteContext`. I tested all seventeen of chi's middleware
against a bare stdlib handler; it is the only one that does this. Found by a benchmark crashing.

`chimw.RealIP` is **deprecated as IP-spoofable**, with three advisories against it
(GHSA-3fxj-6jh8-hvhx, GHSA-rjr7-jggh-pgcp, GHSA-9g5q-2w5x-hmxf). Found by `staticcheck`, after I
had already written the correct version of the same logic thirty lines away in `request.ClientIP`
and not connected the two. Two reasons it is wrong, and they are the two ways everyone gets this
wrong:

- **It takes the leftmost `X-Forwarded-For` value.** A proxy *appends* the address it saw, so
  the leftmost entry is whatever the client sent and the rightmost is the only one the proxy
  vouches for. Taking the leftmost lets anyone choose their own apparent IP, which defeats rate
  limiting and audit logging together.
- **It trusts the headers unconditionally.** A service reachable directly, and not only through
  its load balancer, has no reason to believe any of them.

This module ships replacements for both. `RealIP(trustProxyHeaders bool)` takes the rightmost
entry and makes the trust decision an argument, and `Production` passes that decision through
rather than making it. The first version of `Production` called `RealIP(true)`, so even
`cmd/server`, run directly on a laptop, let any client choose the address it logged.
`cmd/server` trusts the headers only when `TRUST_PROXY_HEADERS=true`.

### What it all costs

Measured against a null `ResponseWriter`, because `httptest.NewRecorder` allocates 1,010 bytes
across 10 allocations and hid every difference behind itself.

| | ns/op | B/op |
|---|---|---|
| bare handler | 5.4 | 2 |
| `Recovery` | 10.1 | 2 |
| chi `StripSlashes` | 10.5 | 2 |
| `CleanPath` (ours) | 18.4 | 2 |
| `MaxBody` | 42.3 | 66 |
| `RealIP` (ours) | 61.2 | 2 |
| `SecureHeaders` | 120.9 | 50 |
| chi `Compress` | 139.0 | 114 |
| `RequestID` | 224.7 | 460 |
| `Timeout` | 270.7 | 594 |
| `Logger` | 793.8 | 130 |
| **the whole `Production` chain** | **1,878** | 1,360 |

A no-op layer costs about **1.8 ns** (5.5 ns bare → 40.9 ns at twenty layers). Chain depth is
free; the logger is 40% of the total, and `context.WithTimeout` allocating a timer is most of
the rest.

The recorder wrapper is 1.8 ns → 25.6 ns per request, and **23 of those 24 ns are the
allocation**: reusing the wrapper brings it to 3.0 ns. A `sync.Pool` would remove it.

Against a handler that touches a database, 1.9 µs is 0.2%.

## Request: there is no pydantic

`DecodeJSON` is the function every Go service ends up writing. Five things it does that
`json.NewDecoder(r.Body).Decode(v)` does not:

1. **Caps the body** with `http.MaxBytesReader`, so a 10 GB request cannot allocate 10 GB.
2. **`DisallowUnknownFields`**, which is the single most valuable line here. Without it,
   `{"nmae":"hammer"}` succeeds and leaves `Name` empty — a bug with no error anywhere.
3. **Rejects a second JSON value**, because `Decode` reads one and stops. `{"a":1}{"b":2}`
   otherwise succeeds and the second object vanishes.
4. **Distinguishes an empty body from `{}`**, which are different requests.
5. **Turns four error types into sentinels**, so handlers map them to statuses instead of
   matching error strings.

`DisallowUnknownFields` reports its error as a plain string with no type, so recognising it
needs a `strings.HasPrefix` on `"json: unknown field "`. That is a wart in `encoding/json` and
it has been an open proposal for years.

### The four ways a JSON field arrives

The distinction pydantic makes for you:

| body | `string` | `*string` | `json.RawMessage` |
|---|---|---|---|
| `{"x":"a"}` | `"a"` | `"a"` | `"a"` |
| `{"x":""}` | `""` | `""` | `""` |
| `{"x":null}` | `""` | **nil** | `null` |
| `{}` | `""` | **nil** | **nil** |

Only `json.RawMessage` tells an absent field from an explicit null. A pointer collapses those
two; a value type collapses all three.

### Upload filenames: Go is safer than the advice, and not safe

The usual warning is that the filename is attacker-controlled and can be `../../etc/passwd`. In
Go it cannot, quite: `mime/multipart` applies `filepath.Base` first. Measured on Unix:

| sent | `header.Filename` | |
|---|---|---|
| `../../etc/passwd` | `passwd` | stripped |
| `/etc/passwd` | `passwd` | stripped |
| `sub/dir/file.txt` | `file.txt` | stripped |
| `..\..\windows\system32\config` | unchanged | **not** stripped |
| `C:\Users\x\file.txt` | unchanged | **not** stripped |
| `..` | `..` | **not** stripped |
| `.` | `.` | **not** stripped |

`filepath.Base` is OS-specific, so the backslash cases *are* stripped on Windows and not here:
the same code is safe on one platform and not the other. And `..` surviving is the one that
matters, because `filepath.Join(dir, "..")` is the parent directory.

A null byte in the filename makes the MIME header malformed and `ParseMultipartForm` rejects the
whole request, which is a good default.

`SafeFilename` handles all of it. The stronger advice still applies: generate an ID, keep the
original as metadata, serve it back in `Content-Disposition`.

### Three more traps, each one line

**`r.FormValue` merges the query string.** A POST handler reading `r.FormValue("id")` silently
accepts `?id=` when the body has no such field. `r.PostFormValue` reads the body only and is
almost always what you want.

**`r.URL.Query().Get` returns only the first value.** `?tag=a&tag=b&tag=c` gives `"a"` and the
rest are dropped with no error.

**`strconv.ParseBool` rejects `"on"`**, which is exactly what an HTML checkbox sends. A handler
using it alone rejects its own form.

`Query` accumulates errors rather than returning on the first, because a client sending three
bad parameters should learn about all three — which is what FastAPI's 422 does and what an
early return does not. It also errors on an out-of-range value rather than clamping: a client
asking for `limit=100000` should be told, not quietly given 100 and left wondering why the next
page never comes.

## Response: the ordering rule

```go
w.Header().Set(...)    // headers, first
w.WriteHeader(status)  // the status line, once
w.Write(body)          // the body, last
```

Every violation is silent:

| | |
|---|---|
| a header after `WriteHeader` | dropped; the header block has gone out |
| a second `WriteHeader` | ignored, with `superfluous response.WriteHeader call` in the server log |
| `Write` before `WriteHeader` | implicit 200, so a later 500 goes nowhere |

`WriteJSON` **marshals into a buffer first**. Encoding straight into the writer means a failure
halfway through leaves a truncated body behind a 200 and the client cannot tell. Buffering costs
one allocation and turns that into a clean 500. It also sets `Content-Length`, which only the
buffered version can.

Errors are **RFC 9457 problem+json**, because it exists and the industry converged on it.
Validation failures are **422**, not 400: 400 means the request was malformed, 422 means it
parsed and failed validation, and a client can tell "retry with different data" from "your
serialiser is broken".

Streaming is **newline-delimited JSON**, not a JSON array, and that is the whole argument for
streaming: an array cannot be parsed until its closing bracket arrives.

ETags are **weak** (`W/"..."`), because the hash is over serialised bytes and two serialisations
differing only in key order are the same document. A strong tag promises byte equality that JSON
marshalling does not provide across versions.

The stdlib has **no content negotiation at all**, which is one of the genuinely missing pieces.
`Negotiate` is a simplified q-value parse that prefers the server's order on a tie, because the
server knows which representation is cheapest.

## Server: the zero value is not safe

```go
http.ListenAndServe(":8080", handler)
```

That line is in every tutorial and it creates a server with **no timeouts**. Not long ones:
none. A client that opens a connection and sends one byte of a header holds a goroutine and a
file descriptor forever.

`TestSlowlorisNeedsReadHeaderTimeout` demonstrates it with a raw socket. Sending
`GET / HTTP/1.1\r\nHost: localhost\r\nX-Slow: ` and then nothing:

| | |
|---|---|
| `ReadHeaderTimeout: 100ms` | the server hangs up |
| `ReadHeaderTimeout: 0` (the default) | the connection is still held after 500 ms |

That is Slowloris. It is twenty years old and the Go default is still vulnerable. Worth noting
that uvicorn and gunicorn ship with timeouts on, so the FastAPI version of this module is
protected by its runtime and the Go version has to ask.

| | |
|---|---|
| `ReadHeaderTimeout` | the Slowloris defence. If you set only one, set this. |
| `ReadTimeout` | headers **and** body. Too short breaks large uploads. |
| `WriteTimeout` | headers-end to response-end. Too short breaks streaming. |
| `IdleTimeout` | keep-alive lifetime. Defaults to `ReadTimeout`, so leaving that unset leaves this unbounded too. |

### ReadTimeout and WriteTimeout are absolute, not idle

They start when the request arrives and **do not reset on progress**, so a stream longer than
the timeout is cut off even though it was never stalled. Six chunks at 40 ms with a 100 ms
`WriteTimeout`:

| | |
|---|---|
| plain | **3 of 6 chunks**, then `unexpected EOF` |
| `SetWriteDeadline` per chunk | all 6 |

`ExtendDeadline` is the fix, and before Go 1.20 it needed a type assertion to an unexported
interface — so most code turned the server-wide timeouts off instead, which is how services
ended up unprotected.

### Shutdown

Three things in `Run`:

**`ListenAndServe` returns `http.ErrServerClosed` on a clean `Shutdown`.** Treating that as a
failure is the most common mistake in this function, and it makes every clean shutdown look like
a crash.

**`signal.NotifyContext`**, not a `chan os.Signal` and a select. Four lines shorter and it
composes with everything else taking a context.

**The shutdown context must be fresh.** Passing the already-cancelled signal context to
`Shutdown` makes it return immediately and abandon in-flight requests. It looks correct.
`TestShutdownWithACancelledContextIsInstant` is that bug.

## Running it

```bash
go run ./cmd/server

curl -i localhost:8080/health
curl -s localhost:8080/items/ | jq
curl -s -XPOST localhost:8080/items/ -d '{"name":"chisel"}' | jq
curl -s localhost:8080/stream                          # one object per second
curl -i localhost:8080/negotiated/99                   # problem+json 404
curl -i -H 'Accept: text/plain' localhost:8080/negotiated/1
curl -s -XPOST localhost:8080/validated -d '{}' | jq   # a 422 with field detail
curl -i localhost:8080/panic                           # recovered, two log lines
```

Then ctrl-C and watch the graceful shutdown.

```bash
go test ./...
go test -run XXX -bench . -benchmem ./middleware
go test -v -run Slowloris ./server
go test -v -run FilenameIsPartlySanitised ./request
```

## What Go does not have

Honestly, because the FastAPI version gets all of this for free:

| | |
|---|---|
| schema from type annotations | write the struct and the validation |
| automatic 422 with field detail | `response.WriteValidationError`, and the checks by hand |
| OpenAPI generation | `swaggo/swag` from comments, or write the spec |
| interactive docs | nothing equivalent in the stdlib |
| dependency injection | pass things to constructors |
| content negotiation | `response.Negotiate`, 60 lines |

What Go gives back: the decoding is explicit, so there is never a question about what happened
to a field that was absent versus zero versus malformed. The answer is in the code rather than in
a framework's coercion rules.
