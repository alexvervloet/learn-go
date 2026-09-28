// Package awstest points the AWS SDK at a LocalStack running on this machine.
//
// # Why LocalStack and not a mock
//
// The AWS SDK is easy to mock, because every client method is an interface method away from a fake. The result
// tests the fake. What these modules are about is what the SERVICE does: whether a conditional write is rejected,
// what a visibility timeout actually hides, how many items a Query reads compared to a Scan, whether a filter
// policy drops a message before it reaches the queue. None of that lives in the SDK.
//
// LocalStack is not AWS either, and the README says where it differs. It runs the same wire protocol and the same
// service semantics for everything demonstrated here, which is enough.
//
// # What this package gives a test
//
//   - Config(t), which skips the test when LocalStack is not running rather than failing it.
//   - A Counter, which counts HTTP round trips to the service. That is the machine-independent number these
//     tests assert on: a Query that reads 3 items reads 3 items on any machine, and an SDK paginator that makes
//     4 calls makes 4 calls. Durations go in a t.Log next to the assertion.
//   - Name(t, kind), which builds a resource name unique to this test, because LocalStack is one shared
//     namespace and `go test ./...` runs packages at the same time.
package awstest

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
)

// DefaultEndpoint is where LocalStack listens. One port for every service: the SDK picks the service from the
// request body and headers, not from the host, so a single endpoint can serve S3, SQS, SNS and DynamoDB.
const DefaultEndpoint = "http://localhost:4566"

// Region is the region every test uses.
//
// LocalStack does not care, but the SDK does: SigV4 signs the region into the credential scope, so a client with
// no region configured fails before it sends anything.
const Region = "us-east-1"

// Endpoint returns the endpoint from the environment or the default.
func Endpoint() string {
	if e := os.Getenv("AWS_ENDPOINT_URL"); e != "" {
		return e
	}

	return DefaultEndpoint
}

var (
	once     sync.Once
	reachErr error
)

// available reports whether LocalStack answers, and caches the answer for the whole test binary.
//
// It asks for /_localstack/health, which is LocalStack's own endpoint and not an AWS API. That distinction
// matters: an unsigned GET to an AWS path returns a signature error, which is indistinguishable from a
// misconfigured client. The health endpoint answers 200 with a JSON body listing every service and its state.
func available() error {
	once.Do(func() {
		client := &http.Client{Timeout: 3 * time.Second}

		resp, err := client.Get(Endpoint() + "/_localstack/health")
		if err != nil {
			reachErr = err
			return
		}

		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			reachErr = fmt.Errorf("health endpoint returned %s", resp.Status)
		}
	})

	return reachErr
}

// Config returns an SDK config aimed at LocalStack, or skips the test.
//
// # The three things that have to be set
//
// BaseEndpoint sends every request to LocalStack instead of the real AWS endpoint for the region. Static
// credentials stop the SDK walking its chain of providers, which on a developer machine can find a real profile
// in ~/.aws/credentials and send the request to real AWS with real billing attached. And the region has to be
// set because it goes into the signature.
//
// The credentials are the literal strings "test"/"test" that LocalStack documents. It does not validate them,
// but it does require SOMETHING, because the SDK will not sign without a credential and an unsigned request is
// rejected.
func Config(t testing.TB) aws.Config {
	t.Helper()

	if err := available(); err != nil {
		t.Skipf("LocalStack is not reachable at %s (%v).\n"+
			"Start it with: docker compose -f backends/learning/aws-concepts/docker-compose.yml up -d",
			Endpoint(), err)
	}

	cfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithRegion(Region),
		config.WithBaseEndpoint(Endpoint()),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	return cfg
}

// Counter is an aws.HTTPClient that counts the requests passing through it.
//
// # Why count at the HTTP layer
//
// One SDK call is not one HTTP request. A retry is a second request. A paginator makes one per page. An S3
// multipart upload makes one per part plus two more. Counting at the method call would miss all of it, and
// counting inside the SDK means reaching into middleware. The transport sees exactly what went on the wire.
//
// The counters are atomics because the SDK can issue requests concurrently, and a test that reads a plain int
// while the SDK writes it is a data race that -race will find.
type Counter struct {
	inner aws.HTTPClient

	total  atomic.Int64
	byName sync.Map // operation name -> *atomic.Int64
}

// NewCounter wraps an HTTP client. A nil inner client means a default one.
func NewCounter(inner aws.HTTPClient) *Counter {
	if inner == nil {
		inner = &http.Client{Timeout: 30 * time.Second}
	}

	return &Counter{inner: inner}
}

// Do counts the request and passes it on.
//
// The operation name comes from the X-Amz-Target header for JSON protocols (DynamoDB sends
// "DynamoDB_20120810.Query") and from the Action form field for query protocols (SQS and SNS). S3 is REST and
// has neither, so an S3 request is counted by method and path shape instead. That is three protocols in one
// SDK, which is a fair summary of AWS.
func (c *Counter) Do(req *http.Request) (*http.Response, error) {
	c.total.Add(1)

	if name := operation(req); name != "" {
		v, _ := c.byName.LoadOrStore(name, new(atomic.Int64))
		v.(*atomic.Int64).Add(1)
	}

	return c.inner.Do(req)
}

// Total returns how many HTTP requests have gone through.
func (c *Counter) Total() int {
	return int(c.total.Load())
}

// Count returns how many requests carried a given operation name.
func (c *Counter) Count(operation string) int {
	v, ok := c.byName.Load(operation)
	if !ok {
		return 0
	}

	return int(v.(*atomic.Int64).Load())
}

// Operations returns every operation seen and its count, for a t.Log when an assertion fails.
func (c *Counter) Operations() map[string]int {
	out := map[string]int{}

	c.byName.Range(func(k, v any) bool {
		out[k.(string)] = int(v.(*atomic.Int64).Load())
		return true
	})

	return out
}

// Reset zeroes the counters, so one test can measure several phases.
func (c *Counter) Reset() {
	c.total.Store(0)
	c.byName.Range(func(k, _ any) bool {
		c.byName.Delete(k)
		return true
	})
}

// operation extracts an operation name from a request, best effort.
func operation(req *http.Request) string {
	if target := req.Header.Get("X-Amz-Target"); target != "" {
		// "DynamoDB_20120810.Query" -> "Query".
		if i := strings.LastIndex(target, "."); i >= 0 {
			return target[i+1:]
		}

		return target
	}

	// SQS and SNS in their query protocol put the action in the form body. The SDK's newer SQS client uses JSON
	// and hits the branch above, so this is here for SNS.
	//
	// # Why this reads the body by hand instead of calling req.ParseForm
	//
	// ParseForm CONSUMES the body. The first version of this called it, and SNS requests started failing on
	// retry with `ContentLength=93 with Body length 0`: the SDK retried the request, the transport found a
	// declared length and an empty reader, and the connection broke. Three attempts later the operation gave
	// up. An HTTP client middleware that reads a request body and does not put it back breaks every retry
	// above it, and this is the cheapest possible demonstration.
	//
	// So: read it, parse a copy, and hand the bytes back as a fresh reader before the request goes out.
	if ct := req.Header.Get("Content-Type"); strings.HasPrefix(ct, "application/x-www-form-urlencoded") && req.Body != nil {
		body, err := io.ReadAll(req.Body)

		_ = req.Body.Close()

		req.Body = io.NopCloser(bytes.NewReader(body))

		if err == nil {
			if values, err := url.ParseQuery(string(body)); err == nil {
				if action := values.Get("Action"); action != "" {
					return action
				}
			}
		}
	}

	return ""
}

// WithCounter returns a config whose HTTP client counts, and the counter.
//
// Every client built from the returned config shares the counter, which is what makes a fan-out test possible:
// publish to SNS, then count the SQS receives.
func WithCounter(cfg aws.Config) (aws.Config, *Counter) {
	c := NewCounter(cfg.HTTPClient)
	cfg.HTTPClient = c

	return cfg, c
}

// Name builds a resource name unique to this test and this test binary.
//
// # Why not just the test name
//
// LocalStack is ONE namespace for the whole machine. `go test ./...` runs package test binaries concurrently, so
// two packages with a test called TestRoundTrip would fight over one bucket. Subtests make it worse: t.Name()
// contains slashes, which S3 allows in a key and not in a bucket name.
//
// The binary name disambiguates the package, the test name disambiguates the test, and everything is lowercased
// with non-alphanumerics collapsed to hyphens because S3 bucket names are DNS labels: lowercase letters, digits
// and hyphens, 3 to 63 characters, no underscores and no uppercase. DynamoDB and SQS are laxer, and using one
// scheme everywhere means one rule to remember.
func Name(t testing.TB, kind string) string {
	t.Helper()

	binary := strings.TrimSuffix(filepath.Base(os.Args[0]), ".test")
	binary = strings.TrimSuffix(binary, ".exe")

	raw := fmt.Sprintf("%s-%s-%s", kind, binary, t.Name())

	var b strings.Builder

	lastHyphen := false

	for _, r := range strings.ToLower(raw) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastHyphen = false
		case !lastHyphen:
			b.WriteByte('-')
			lastHyphen = true
		}
	}

	name := strings.Trim(b.String(), "-")

	// 63 is the S3 limit. Truncating from the front would drop the kind prefix, which is what makes a stray
	// resource identifiable in `awslocal s3 ls`, so the tail goes first.
	if len(name) > 63 {
		name = strings.Trim(name[:63], "-")
	}

	return name
}
