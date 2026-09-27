package jwtauth

import (
	"crypto/rand"
	"crypto/rsa"
	"net/http"
	"net/http/httptest"
	"testing"
)

// BenchmarkSignAndVerify is the number that decides HS256 against RS256.
//
// The asymmetry is the point, and it is larger than I expected. Measured here:
//
//	HS256        mint   2.94µs   verify   4.12µs
//	RS256-2048   mint 890µs      verify  31.95µs
//	RS256-4096   mint   5.54ms   verify 148.67µs
//
// RSA signing is 303 times HS256 signing at 2048 bits, because it is exponentiation by a 2048-bit private
// exponent, while verification uses the public exponent 65537 and is 7.8 times HS256. So the cost falls on
// whoever MINTS, which is usually one service at login, and the services that verify pay comparatively little.
// That asymmetry is what makes RS256 practical at all.
//
// 4096 bits multiplies both by about 5, and key GENERATION by 11.5 (52ms against 597ms), which is a startup
// cost rather than a per-request one and is still worth knowing before putting it in a health check.
//
// Also worth noticing: HS256 VERIFY (4.12µs) is slower than HS256 MINT (2.94µs), which looks backwards. The
// HMAC is the same work either way; verification additionally parses three base64 segments, unmarshals the
// claims, and validates exp, nbf, iss and aud. The signature is not the expensive part at all.
func BenchmarkSignAndVerify(b *testing.B) {
	hs, err := NewHS256([]byte("a-bench-secret-that-is-long-enough!!"), "k1")
	if err != nil {
		b.Fatal(err)
	}
	hs.Issuer, hs.Audience = "learn-go", "api"

	key2048, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		b.Fatal(err)
	}

	key4096, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		b.Fatal(err)
	}

	rs2048 := NewRS256(key2048, "k1")
	rs2048.Issuer, rs2048.Audience = "learn-go", "api"

	rs4096 := NewRS256(key4096, "k1")
	rs4096.Issuer, rs4096.Audience = "learn-go", "api"

	roles := []string{"admin", "billing"}

	for _, tc := range []struct {
		name string
		i    *Issuer
	}{
		{"HS256", hs},
		{"RS256-2048", rs2048},
		{"RS256-4096", rs4096},
	} {
		token, err := tc.i.Mint("user-42", roles, "a@example.test")
		if err != nil {
			b.Fatal(err)
		}

		b.Run(tc.name+"/mint", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := tc.i.Mint("user-42", roles, "a@example.test"); err != nil {
					b.Fatal(err)
				}
			}
		})

		b.Run(tc.name+"/verify", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := tc.i.Verify(token); err != nil {
					b.Fatal(err)
				}
			}
		})

		b.Logf("%s token: %d bytes", tc.name, len(token))
	}
}

// BenchmarkKeyGeneration, because it is the reason a service generates its keys once and not per request, and
// the reason 4096 bits is not free.
func BenchmarkKeyGeneration(b *testing.B) {
	for _, bits := range []int{2048, 4096} {
		b.Run(itoa(bits)+" bits", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := rsa.GenerateKey(rand.Reader, bits); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkAuthenticateMiddleware is the whole request path, so the verification cost can be compared against
// the handler it protects.
func BenchmarkAuthenticateMiddleware(b *testing.B) {
	hs, err := NewHS256([]byte("a-bench-secret-that-is-long-enough!!"), "k1")
	if err != nil {
		b.Fatal(err)
	}
	hs.Issuer, hs.Audience = "learn-go", "api"

	token, err := hs.Mint("user-42", []string{"admin"}, "")
	if err != nil {
		b.Fatal(err)
	}

	bare := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})

	authenticated := hs.Authenticate(bare)
	authorised := hs.Authenticate(RequireRole("admin")(bare))

	request := func() *http.Request {
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("Authorization", "Bearer "+token)
		return r
	}

	for _, tc := range []struct {
		name string
		h    http.Handler
	}{
		{"bare handler", bare},
		{"Authenticate", authenticated},
		{"Authenticate + RequireRole", authorised},
	} {
		b.Run(tc.name, func(b *testing.B) {
			w := &nullWriter{header: make(http.Header, 8)}
			r := request()

			b.ReportAllocs()
			for b.Loop() {
				tc.h.ServeHTTP(w, r)
			}
		})
	}

	// The comparison against a session id in Redis, which is where the measurement contradicted what
	// I was about to write.
	//
	// A Redis round trip is 20.2µs, measured by BenchmarkRedisAllow in the ratelimit package. HS256
	// verification is 4.1µs, so a JWT saves about 16µs per request. RS256-2048 verification is
	// 32.0µs, which is MORE than the Redis lookup it was supposed to replace.
	//
	// So "a JWT saves you a database round trip" is true for HS256 and false for RS256 on this
	// machine. The reason to choose RS256 is that twelve services can verify without being able to
	// mint, and that is an authority argument, not a performance one. Stating it as a performance
	// win is a claim that does not survive a benchmark.
	b.Log("HS256 verify 4.1µs, RS256-2048 verify 32.0µs, a Redis session lookup 20.2µs " +
		"(measured in the ratelimit package): RS256 verification costs MORE than the " +
		"lookup it replaces")
}

type nullWriter struct {
	header http.Header
	status int
}

func (w *nullWriter) Header() http.Header         { return w.header }
func (w *nullWriter) Write(b []byte) (int, error) { return len(b), nil }
func (w *nullWriter) WriteHeader(status int)      { w.status = status }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}

	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
