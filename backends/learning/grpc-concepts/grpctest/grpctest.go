// Package grpctest starts an in-process gRPC server for tests.
//
// # bufconn against a real port
//
// google.golang.org/grpc/test/bufconn is an in-memory net.Listener. A server listens on it, a client dials it
// through a custom dialer, and no TCP is involved: no port to allocate, no firewall, no TIME_WAIT, and it works
// in a sandbox with no network.
//
// It is the right default for testing gRPC code, and it is worth knowing what it does NOT test. There is no
// real socket, so nothing about connection establishment, TLS, keepalives, load balancing or DNS is exercised.
// Those need a real listener, and a suite should have a couple of tests that use one.
//
// This package offers both, because the difference is the point.
package grpctest

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// Server is a running gRPC server plus a dialer for it.
type Server struct {
	// Register is called with the grpc.Server before it starts, so a test registers its services.
	grpc *grpc.Server

	dial func(context.Context, string) (net.Conn, error)

	// Addr is set only for a real listener.
	Addr string
}

// Options configures a test server.
type Options struct {
	// Register attaches the services. Required: a server with nothing registered answers every call with
	// codes.Unimplemented, which looks like a client bug.
	Register func(*grpc.Server)

	// UnaryInterceptors and StreamInterceptors are chained in order, outermost first.
	UnaryInterceptors  []grpc.UnaryServerInterceptor
	StreamInterceptors []grpc.StreamServerInterceptor

	// RealListener uses TCP on a random port instead of bufconn.
	RealListener bool

	// ServerOptions are passed through, for the settings a test wants to change (message sizes,
	// keepalive, concurrency limits).
	ServerOptions []grpc.ServerOption
}

// Start runs a server and stops it when the test ends.
func Start(t testing.TB, opts Options) *Server {
	t.Helper()

	if opts.Register == nil {
		t.Fatal("grpctest: Register is required")
	}

	serverOpts := append([]grpc.ServerOption(nil), opts.ServerOptions...)

	if len(opts.UnaryInterceptors) > 0 {
		serverOpts = append(serverOpts, grpc.ChainUnaryInterceptor(opts.UnaryInterceptors...))
	}
	if len(opts.StreamInterceptors) > 0 {
		serverOpts = append(serverOpts, grpc.ChainStreamInterceptor(opts.StreamInterceptors...))
	}

	srv := grpc.NewServer(serverOpts...)

	opts.Register(srv)

	s := &Server{grpc: srv}

	var listener net.Listener

	if opts.RealListener {
		var err error

		// Port 0, so the OS picks a free one. A fixed port in a test suite is a conflict waiting for
		// two packages to run at once, which `go test ./...` does by default.
		listener, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listening: %v", err)
		}

		s.Addr = listener.Addr().String()

		addr := s.Addr
		s.dial = func(ctx context.Context, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "tcp", addr)
		}
	} else {
		// 1 MB buffer. bufconn blocks when the buffer is full, so a size smaller than the largest
		// message deadlocks, and that deadlock looks exactly like a server that stopped responding.
		bl := bufconn.Listen(1 << 20)

		listener = bl
		s.dial = func(ctx context.Context, _ string) (net.Conn, error) {
			return bl.DialContext(ctx)
		}
	}

	served := make(chan error, 1)

	go func() { served <- srv.Serve(listener) }()

	t.Cleanup(func() {
		// GracefulStop waits for in-flight calls, which is what a test wants: Stop would cut a
		// stream mid-message and the test's own assertions would race the teardown.
		//
		// It can also hang, on a stream nobody closed, so it gets a deadline and falls back to Stop.
		stopped := make(chan struct{})

		go func() {
			srv.GracefulStop()
			close(stopped)
		}()

		select {
		case <-stopped:
		case <-time.After(5 * time.Second):
			t.Log("GracefulStop did not finish in 5s, forcing Stop; a stream was probably " +
				"left open")
			srv.Stop()
			<-stopped
		}

		if err := <-served; err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			t.Errorf("serving: %v", err)
		}
	})

	return s
}

// Dial connects a client to the server.
func (s *Server) Dial(t testing.TB, opts ...grpc.DialOption) *grpc.ClientConn {
	t.Helper()

	all := append([]grpc.DialOption{
		grpc.WithContextDialer(s.dial),

		// insecure.NewCredentials(), not grpc.WithInsecure(). The latter is deprecated and the
		// replacement is deliberately more verbose, because "insecure" should be visible at the call
		// site rather than a flag.
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	}, opts...)

	// grpc.NewClient, not grpc.Dial. Dial is deprecated as of 1.63: it did a blocking connect by
	// default and its name promised a connection it did not always make. NewClient is lazy and
	// connects on the first RPC, which is what almost every caller wanted.
	//
	// The target is "passthrough:///bufnet" rather than "bufnet" because NewClient defaults to the dns
	// resolver, which tries to resolve the name. passthrough hands it to the dialer unchanged, and
	// without it a bufconn test fails with a DNS error.
	conn, err := grpc.NewClient("passthrough:///bufnet", all...)
	if err != nil {
		t.Fatalf("creating a client: %v", err)
	}

	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Errorf("closing the client: %v", err)
		}
	})

	return conn
}

// Context returns a context with a deadline, which every gRPC call in a test should have.
//
// Not context.Background(). A call with no deadline against a server that hangs makes the test hang until the
// test binary's own timeout, ten minutes by default, and the panic that produces names every goroutine rather
// than the test.
func Context(t testing.TB, d time.Duration) context.Context {
	t.Helper()

	if d <= 0 {
		d = 10 * time.Second
	}

	ctx, cancel := context.WithTimeout(context.Background(), d)
	t.Cleanup(cancel)

	return ctx
}
