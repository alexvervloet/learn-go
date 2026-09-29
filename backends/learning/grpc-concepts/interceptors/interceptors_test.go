package interceptors_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	greeterv1 "github.com/alexvervloet/learn-go/backends/learning/grpc-concepts/gen/greeterv1"
	stockv1 "github.com/alexvervloet/learn-go/backends/learning/grpc-concepts/gen/stockv1"
	"github.com/alexvervloet/learn-go/backends/learning/grpc-concepts/grpctest"
	"github.com/alexvervloet/learn-go/backends/learning/grpc-concepts/interceptors"
	"github.com/alexvervloet/learn-go/backends/learning/grpc-concepts/server"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestUnaryInterceptorRecordsTheCode.
func TestUnaryInterceptorRecordsTheCode(t *testing.T) {
	rec := &interceptors.Recorder{}

	srv := grpctest.Start(t, grpctest.Options{
		Register: func(s *grpc.Server) {
			greeterv1.RegisterGreeterServer(s, &server.Greeter{ID: 1})
		},
		UnaryInterceptors: []grpc.UnaryServerInterceptor{
			interceptors.LogUnary(discardLogger(), rec),
		},
	})

	client := greeterv1.NewGreeterClient(srv.Dial(t))

	ctx := grpctest.Context(t, 5*time.Second)

	if _, err := client.SayHello(ctx, &greeterv1.HelloRequest{Name: "Ada"}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.SayHello(ctx, &greeterv1.HelloRequest{}); err == nil {
		t.Fatal("expected a validation failure")
	}
	if _, err := client.Fail(ctx, &greeterv1.FailRequest{
		Code: int32(codes.Internal),
	}); err == nil {
		t.Fatal("expected a failure")
	}

	calls := rec.Unary()

	if len(calls) != 3 {
		t.Fatalf("recorded %d calls, want 3", len(calls))
	}

	for _, c := range calls {
		t.Logf("%-38s %-16s %v", c.Method, c.Code.String(), c.Duration.Round(time.Microsecond))
	}

	if calls[0].Code != codes.OK {
		t.Errorf("the successful call recorded %s", calls[0].Code)
	}
	if calls[1].Code != codes.InvalidArgument {
		t.Errorf("the validation failure recorded %s", calls[1].Code)
	}
	if calls[2].Code != codes.Internal {
		t.Errorf("the internal failure recorded %s", calls[2].Code)
	}

	// The full method, which is the only thing that identifies a call: the service, the package and
	// the method name.
	if calls[0].Method != "/greeter.v1.Greeter/SayHello" {
		t.Errorf("the method is %q", calls[0].Method)
	}

	t.Log("status.Code returns OK for a nil error and Unknown for a non-status error, which is " +
		"exactly the two cases a log line needs and the two a type assertion would have to " +
		"handle by hand")
}

// TestAStreamInterceptorSeesOnlyTheOpening unless it wraps the stream.
func TestStreamInterceptorCountsMessages(t *testing.T) {
	rec := &interceptors.Recorder{}

	srv := grpctest.Start(t, grpctest.Options{
		Register: func(s *grpc.Server) {
			stockv1.RegisterStockTickerServer(s, &server.StockTicker{Interval: time.Millisecond})
		},
		StreamInterceptors: []grpc.StreamServerInterceptor{
			interceptors.LogStream(discardLogger(), rec),
		},
		// A unary interceptor too, to show it does NOT see the stream.
		UnaryInterceptors: []grpc.UnaryServerInterceptor{
			interceptors.LogUnary(discardLogger(), rec),
		},
	})

	client := stockv1.NewStockTickerClient(srv.Dial(t))

	ctx := grpctest.Context(t, 10*time.Second)

	stream, err := client.Watch(ctx, &stockv1.WatchRequest{
		Symbols: []string{"AAPL"},
		Limit:   5,
	})
	if err != nil {
		t.Fatal(err)
	}

	for {
		if _, err := stream.Recv(); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatal(err)
		}
	}

	streams := rec.Streams()

	if len(streams) != 1 {
		t.Fatalf("recorded %d streams, want 1", len(streams))
	}

	s := streams[0]

	t.Logf("%s %s: sent %d, received %d", s.Method, s.Code, s.Sent, s.Received)

	if s.Sent != 5 {
		t.Errorf("counted %d sent, want 5", s.Sent)
	}

	// ONE received, which is the request. I expected zero: the request looks like it arrives before
	// the handler runs, and in fact grpc-go delivers it by calling RecvMsg on the stream the
	// interceptor wrapped, so it is counted like any other message.
	if s.Received != 1 {
		t.Errorf("counted %d received, want 1 (the request itself)", s.Received)
	}

	// And the unary interceptor saw nothing, which is the duplication the package doc warns about.
	if len(rec.Unary()) != 0 {
		t.Errorf("the unary interceptor recorded %d calls for a stream", len(rec.Unary()))
	}

	t.Log("a service with a unary logging interceptor and no stream one logs none of its " +
		"streaming traffic, and the streaming traffic is the half that holds long-lived " +
		"connections")

	if rec.MessagesSent.Load() != 5 {
		t.Errorf("the recorder counted %d messages sent", rec.MessagesSent.Load())
	}
}

// TestRecoveryStopsAPanicKillingTheProcess.
func TestRecoveryStopsAPanicKillingTheProcess(t *testing.T) {
	rec := &interceptors.Recorder{}

	srv := grpctest.Start(t, grpctest.Options{
		Register: func(s *grpc.Server) { greeterv1.RegisterGreeterServer(s, &panicker{}) },
		UnaryInterceptors: []grpc.UnaryServerInterceptor{
			interceptors.RecoverUnary(discardLogger(), rec),
		},
	})

	client := greeterv1.NewGreeterClient(srv.Dial(t))

	ctx := grpctest.Context(t, 5*time.Second)

	_, err := client.SayHello(ctx, &greeterv1.HelloRequest{Name: "boom"})

	st, _ := status.FromError(err)

	t.Logf("a panicking handler returned %s: %q", st.Code(), st.Message())

	if st.Code() != codes.Internal {
		t.Errorf("got %s, want Internal", st.Code())
	}

	// The panic value must NOT reach the client. A panic often carries a struct or a wrapped error
	// holding a query or a token.
	if strings.Contains(st.Message(), "secret-connection-string") {
		t.Error("the panic value reached the client")
	}

	if rec.Panics.Load() != 1 {
		t.Errorf("the recorder counted %d panics", rec.Panics.Load())
	}

	// And the server is still serving, which is the whole point.
	reply, err := client.SayHelloTwice(ctx, &greeterv1.HelloRequest{Name: "fine"})
	if err != nil {
		t.Fatalf("the server died with the panic: %v", err)
	}

	t.Logf("the next call still worked: %q", reply.GetMessage())

	t.Log("grpc-go does not recover panics, deliberately: it is a library and swallowing one " +
		"hides a bug. So a service without this interceptor is one nil map away from " +
		"restarting, and every in-flight call on every stream dies with it.")
}

// panicker panics with a value that must not reach the client.
type panicker struct {
	greeterv1.UnimplementedGreeterServer
}

func (p *panicker) SayHello(context.Context, *greeterv1.HelloRequest) (*greeterv1.HelloReply, error) {
	panic(errors.New("nil map write while holding secret-connection-string"))
}

func (p *panicker) SayHelloTwice(context.Context, *greeterv1.HelloRequest) (*greeterv1.HelloReply, error) {
	return &greeterv1.HelloReply{Message: "still alive"}, nil
}

// TestMetadataAuthInterceptor, including the lower-casing trap.
func TestMetadataAuthInterceptor(t *testing.T) {
	srv := grpctest.Start(t, grpctest.Options{
		Register: func(s *grpc.Server) {
			greeterv1.RegisterGreeterServer(s, &server.Greeter{ID: 1})
		},
		UnaryInterceptors: []grpc.UnaryServerInterceptor{
			interceptors.RequireMetadata("authorization"),
		},
	})

	client := greeterv1.NewGreeterClient(srv.Dial(t))

	ctx := grpctest.Context(t, 5*time.Second)

	// No metadata.
	_, err := client.SayHello(ctx, &greeterv1.HelloRequest{Name: "Ada"})

	st, _ := status.FromError(err)

	t.Logf("with no metadata: %s: %s", st.Code(), st.Message())

	if st.Code() != codes.Unauthenticated {
		t.Errorf("got %s, want Unauthenticated", st.Code())
	}

	// With it, in the case a client would naturally write.
	withAuth := metadata.AppendToOutgoingContext(ctx, "Authorization", "Bearer token")

	reply, err := client.SayHello(withAuth, &greeterv1.HelloRequest{Name: "Ada"})
	if err != nil {
		t.Fatalf("a capitalised key was rejected: %v", err)
	}

	t.Logf("with Authorization (capitalised by the client): %q", reply.GetMessage())

	t.Log("the client sent 'Authorization' and the server looked for 'authorization', and it " +
		"worked because HTTP/2 lower-cases header names on the wire and md.Get lower-cases " +
		"what you pass it. Indexing the map directly is what breaks.")

	// The mistake, demonstrated: an interceptor that indexes the map with the capitalised key.
	capitalised := grpctest.Start(t, grpctest.Options{
		Register: func(s *grpc.Server) {
			greeterv1.RegisterGreeterServer(s, &server.Greeter{ID: 1})
		},
		UnaryInterceptors: []grpc.UnaryServerInterceptor{indexTheMap("Authorization")},
	})

	broken := greeterv1.NewGreeterClient(capitalised.Dial(t))

	_, err = broken.SayHello(withAuth, &greeterv1.HelloRequest{Name: "Ada"})

	st, _ = status.FromError(err)

	t.Logf(`an interceptor using md["Authorization"]: %s`, st.Code())

	if st.Code() != codes.Unauthenticated {
		t.Error("indexing the map with a capitalised key worked, so this trap no longer exists")
	}

	t.Log("that interceptor rejects every request, and the reason is invisible in the code")
}

// indexTheMap is the wrong way, kept to show what it does.
func indexTheMap(key string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler) (any, error) {
		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			return nil, status.Error(codes.Unauthenticated, "no metadata")
		}

		// The mistake.
		if len(md[key]) == 0 {
			return nil, status.Errorf(codes.Unauthenticated, "%s is required", key)
		}

		return handler(ctx, req)
	}
}

// TestInterceptorOrdering, because a chain that runs in the wrong order recovers nothing.
func TestInterceptorOrdering(t *testing.T) {
	var order []string

	mark := func(name string) grpc.UnaryServerInterceptor {
		return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo,
			handler grpc.UnaryHandler) (any, error) {
			order = append(order, name+" before")

			resp, err := handler(ctx, req)

			order = append(order, name+" after")

			return resp, err
		}
	}

	srv := grpctest.Start(t, grpctest.Options{
		Register: func(s *grpc.Server) {
			greeterv1.RegisterGreeterServer(s, &server.Greeter{ID: 1})
		},
		UnaryInterceptors: []grpc.UnaryServerInterceptor{
			mark("first"), mark("second"), mark("third"),
		},
	})

	client := greeterv1.NewGreeterClient(srv.Dial(t))

	if _, err := client.SayHello(grpctest.Context(t, 5*time.Second),
		&greeterv1.HelloRequest{Name: "Ada"}); err != nil {
		t.Fatal(err)
	}

	t.Logf("order: %v", order)

	want := []string{
		"first before", "second before", "third before",
		"third after", "second after", "first after",
	}

	if len(order) != len(want) {
		t.Fatalf("got %d entries, want %d", len(order), len(want))
	}

	for i := range want {
		if order[i] != want[i] {
			t.Errorf("position %d is %q, want %q", i, order[i], want[i])
		}
	}

	t.Log("outermost first on the way in and last on the way out, the same as HTTP middleware. " +
		"Which means recovery has to be OUTERMOST or it cannot catch a panic in anything " +
		"above it, and logging wants to be outside recovery so it records the Internal.")
}

// TestClientInterceptors.
func TestClientInterceptors(t *testing.T) {
	rec := &interceptors.Recorder{}

	srv := grpctest.Start(t, grpctest.Options{
		Register: func(s *grpc.Server) {
			greeterv1.RegisterGreeterServer(s, &server.Greeter{ID: 1})
		},
		UnaryInterceptors: []grpc.UnaryServerInterceptor{
			interceptors.LogUnary(nil, rec),
		},
	})

	conn := srv.Dial(t,
		grpc.WithChainUnaryInterceptor(
			interceptors.AddMetadataUnary("x-client", "test-suite", "x-version", "1"),
		))

	client := greeterv1.NewGreeterClient(conn)

	if _, err := client.SayHello(grpctest.Context(t, 5*time.Second),
		&greeterv1.HelloRequest{Name: "Ada"}); err != nil {
		t.Fatal(err)
	}

	calls := rec.Unary()
	if len(calls) != 1 {
		t.Fatalf("recorded %d calls", len(calls))
	}

	t.Logf("the server saw x-client=%v x-version=%v",
		calls[0].Metadata["x-client"], calls[0].Metadata["x-version"])

	if got := calls[0].Metadata["x-client"]; len(got) == 0 || got[0] != "test-suite" {
		t.Errorf("x-client is %v", got)
	}

	t.Log("AppendToOutgoingContext, not NewOutgoingContext: the latter REPLACES whatever was " +
		"there, so an interceptor using it discards metadata set above it or by the caller")
}

// TestRetryOnlyRetriesRetryableCodes.
func TestRetryOnlyRetriesRetryableCodes(t *testing.T) {
	var attempts atomic.Int64

	flaky := &flakyGreeter{attempts: &attempts}

	srv := grpctest.Start(t, grpctest.Options{
		Register: func(s *grpc.Server) { greeterv1.RegisterGreeterServer(s, flaky) },
	})

	conn := srv.Dial(t, grpc.WithChainUnaryInterceptor(
		interceptors.RetryUnary(4, time.Millisecond),
	))

	client := greeterv1.NewGreeterClient(conn)

	ctx := grpctest.Context(t, 10*time.Second)

	t.Run("Unavailable is retried", func(t *testing.T) {
		attempts.Store(0)
		flaky.failuresBeforeSuccess.Store(2)
		flaky.code.Store(int32(codes.Unavailable))

		reply, err := client.SayHello(ctx, &greeterv1.HelloRequest{Name: "Ada"})
		if err != nil {
			t.Fatalf("the retry did not recover: %v", err)
		}

		t.Logf("succeeded after %d attempts: %q", attempts.Load(), reply.GetMessage())

		if attempts.Load() != 3 {
			t.Errorf("made %d attempts, want 3", attempts.Load())
		}
	})

	t.Run("InvalidArgument is not", func(t *testing.T) {
		attempts.Store(0)
		flaky.failuresBeforeSuccess.Store(100)
		flaky.code.Store(int32(codes.InvalidArgument))

		_, err := client.SayHello(ctx, &greeterv1.HelloRequest{Name: "Ada"})

		st, _ := status.FromError(err)

		t.Logf("%s after %d attempt(s)", st.Code(), attempts.Load())

		if attempts.Load() != 1 {
			t.Errorf("made %d attempts at a deterministic failure, want 1", attempts.Load())
		}
	})

	t.Run("DeadlineExceeded is not retried by default", func(t *testing.T) {
		attempts.Store(0)
		flaky.failuresBeforeSuccess.Store(100)
		flaky.code.Store(int32(codes.DeadlineExceeded))

		_, err := client.SayHello(ctx, &greeterv1.HelloRequest{Name: "Ada"})

		st, _ := status.FromError(err)

		t.Logf("%s after %d attempt(s)", st.Code(), attempts.Load())

		if attempts.Load() != 1 {
			t.Errorf("made %d attempts, want 1", attempts.Load())
		}

		t.Log("DeadlineExceeded means the call MAY have happened. Retrying a non-idempotent " +
			"call that timed out is how you charge a card twice, so it is not in the " +
			"default set even though it looks transient.")
	})

	t.Run("gives up after the attempt limit", func(t *testing.T) {
		attempts.Store(0)
		flaky.failuresBeforeSuccess.Store(100)
		flaky.code.Store(int32(codes.Unavailable))

		_, err := client.SayHello(ctx, &greeterv1.HelloRequest{Name: "Ada"})

		if err == nil {
			t.Fatal("expected a failure")
		}

		t.Logf("after 4 attempts: %v", err)

		if attempts.Load() != 4 {
			t.Errorf("made %d attempts, want 4", attempts.Load())
		}
	})

	t.Run("the context bounds the retries", func(t *testing.T) {
		attempts.Store(0)
		flaky.failuresBeforeSuccess.Store(100)
		flaky.code.Store(int32(codes.Unavailable))

		slowConn := srv.Dial(t, grpc.WithChainUnaryInterceptor(
			interceptors.RetryUnary(20, 50*time.Millisecond),
		))

		slow := greeterv1.NewGreeterClient(slowConn)

		short, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
		defer cancel()

		start := time.Now()

		_, err := slow.SayHello(short, &greeterv1.HelloRequest{Name: "Ada"})

		elapsed := time.Since(start)

		t.Logf("20 attempts with a 50ms backoff, under an 80ms deadline: gave up after %v",
			elapsed.Round(10*time.Millisecond))

		if err == nil {
			t.Fatal("expected a failure")
		}

		// 20 attempts at a growing backoff would be seconds.
		if elapsed > 500*time.Millisecond {
			t.Errorf("took %v, so the backoff ignored the context", elapsed)
		}
	})
}

// flakyGreeter fails a set number of times before succeeding.
type flakyGreeter struct {
	greeterv1.UnimplementedGreeterServer

	attempts              *atomic.Int64
	failuresBeforeSuccess atomic.Int64
	code                  atomic.Int32
}

func (f *flakyGreeter) SayHello(_ context.Context, req *greeterv1.HelloRequest) (*greeterv1.HelloReply, error) {
	n := f.attempts.Add(1)

	if n <= f.failuresBeforeSuccess.Load() {
		return nil, status.Errorf(codes.Code(uint32(f.code.Load())), "attempt %d", n)
	}

	return &greeterv1.HelloReply{Message: "Hello, " + req.GetName()}, nil
}
