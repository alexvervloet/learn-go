package server_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	greeterv1 "github.com/alexvervloet/learn-go/backends/learning/grpc-concepts/gen/greeterv1"
	stockv1 "github.com/alexvervloet/learn-go/backends/learning/grpc-concepts/gen/stockv1"
	"github.com/alexvervloet/learn-go/backends/learning/grpc-concepts/grpctest"
	"github.com/alexvervloet/learn-go/backends/learning/grpc-concepts/server"
)

// BenchmarkEncoding is the claim that gets made for protobuf, measured: smaller and faster than JSON.
//
// Both halves are measured because only one of them is usually true by as much as people say.
func BenchmarkEncoding(b *testing.B) {
	price := &stockv1.Price{
		Symbol:     "AAPL",
		Cents:      1234567,
		ObservedAt: timestamppb.New(time.Date(2025, 6, 1, 12, 0, 0, 123456789, time.UTC)),
		Sequence:   42,
	}

	protoBytes, err := proto.Marshal(price)
	if err != nil {
		b.Fatal(err)
	}

	// encoding/json on the generated struct, which is what a service does when it exposes the same
	// type over HTTP. It is not protojson: the generated struct has json tags, so it encodes, and the
	// field names and the timestamp shape are different from protojson's.
	jsonBytes, err := json.Marshal(price)
	if err != nil {
		b.Fatal(err)
	}

	// protojson, which is the canonical JSON mapping for protobuf: camelCase names and an RFC 3339
	// timestamp. This is what a gRPC gateway emits.
	protoJSONBytes, err := protojson.Marshal(price)
	if err != nil {
		b.Fatal(err)
	}

	b.Logf("protobuf:   %3d bytes  %s", len(protoBytes), protoBytes)
	b.Logf("json:       %3d bytes  %s", len(jsonBytes), jsonBytes)
	b.Logf("protojson:  %3d bytes  %s", len(protoJSONBytes), protoJSONBytes)
	b.Logf("protobuf is %.1fx smaller than encoding/json and %.1fx smaller than protojson",
		float64(len(jsonBytes))/float64(len(protoBytes)),
		float64(len(protoJSONBytes))/float64(len(protoBytes)))

	b.Run("proto.Marshal", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := proto.Marshal(price); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("json.Marshal", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := json.Marshal(price); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("protojson.Marshal", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := protojson.Marshal(price); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("proto.Unmarshal", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			var out stockv1.Price
			if err := proto.Unmarshal(protoBytes, &out); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("json.Unmarshal", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			var out stockv1.Price
			if err := json.Unmarshal(jsonBytes, &out); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// BenchmarkUnaryCall is what a round trip costs over bufconn, which is a floor rather than a realistic number:
// there is no network, so this measures the protocol and the framework and nothing else.
func BenchmarkUnaryCall(b *testing.B) {
	srv := grpctest.Start(b, grpctest.Options{
		Register: func(s *grpc.Server) {
			greeterv1.RegisterGreeterServer(s, &server.Greeter{ID: 1})
		},
	})

	client := greeterv1.NewGreeterClient(srv.Dial(b))

	ctx := grpctest.Context(b, time.Minute)

	req := &greeterv1.HelloRequest{Name: "Ada"}

	b.Run("over bufconn", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := client.SayHello(ctx, req); err != nil {
				b.Fatal(err)
			}
		}
	})

	// And over a real TCP listener on loopback, which adds the socket and the kernel.
	real := grpctest.Start(b, grpctest.Options{
		Register: func(s *grpc.Server) {
			greeterv1.RegisterGreeterServer(s, &server.Greeter{ID: 1})
		},
		RealListener: true,
	})

	realClient := greeterv1.NewGreeterClient(real.Dial(b))

	b.Run("over loopback TCP", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := realClient.SayHello(ctx, req); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Log("bufconn is the floor: no socket, no kernel, no TCP. The gap to loopback is what the " +
		"transport costs, and a real network adds its round trip on top of both.")
}

// BenchmarkInterceptorOverhead prices the middleware, because a chain of six is normal.
func BenchmarkInterceptorOverhead(b *testing.B) {
	ctx := grpctest.Context(b, time.Minute)
	req := &greeterv1.HelloRequest{Name: "Ada"}

	bare := grpctest.Start(b, grpctest.Options{
		Register: func(s *grpc.Server) {
			greeterv1.RegisterGreeterServer(s, &server.Greeter{ID: 1})
		},
	})

	bareClient := greeterv1.NewGreeterClient(bare.Dial(b))

	b.Run("no interceptors", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := bareClient.SayHello(ctx, req); err != nil {
				b.Fatal(err)
			}
		}
	})

	noop := func(c context.Context, r any, _ *grpc.UnaryServerInfo,
		h grpc.UnaryHandler) (any, error) {
		return h(c, r)
	}

	for _, depth := range []int{1, 6} {
		chain := make([]grpc.UnaryServerInterceptor, depth)
		for i := range chain {
			chain[i] = noop
		}

		srv := grpctest.Start(b, grpctest.Options{
			Register: func(s *grpc.Server) {
				greeterv1.RegisterGreeterServer(s, &server.Greeter{ID: 1})
			},
			UnaryInterceptors: chain,
		})

		client := greeterv1.NewGreeterClient(srv.Dial(b))

		b.Run(itoa(depth)+" no-op interceptors", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := client.SayHello(ctx, req); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkStreamThroughput is what streaming buys over N unary calls, which is the reason streaming exists.
func BenchmarkStreamThroughput(b *testing.B) {
	srv := grpctest.Start(b, grpctest.Options{
		Register: func(s *grpc.Server) {
			// Interval 0 becomes the 10ms default in Watch, which would dominate. A nanosecond
			// keeps the timer out of the measurement.
			stockv1.RegisterStockTickerServer(s, &server.StockTicker{Interval: time.Nanosecond})
		},
	})

	client := stockv1.NewStockTickerClient(srv.Dial(b))

	ctx := grpctest.Context(b, time.Minute)

	const messages = 100

	b.Run("100 messages in one stream", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			stream, err := client.Watch(ctx, &stockv1.WatchRequest{
				Symbols: []string{"AAPL"},
				Limit:   messages,
			})
			if err != nil {
				b.Fatal(err)
			}

			for range messages {
				if _, err := stream.Recv(); err != nil {
					b.Fatal(err)
				}
			}
		}
	})

	greeter := grpctest.Start(b, grpctest.Options{
		Register: func(s *grpc.Server) {
			greeterv1.RegisterGreeterServer(s, &server.Greeter{ID: 1})
		},
	})

	greeterClient := greeterv1.NewGreeterClient(greeter.Dial(b))

	req := &greeterv1.HelloRequest{Name: "Ada"}

	b.Run("100 unary calls", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			for range messages {
				if _, err := greeterClient.SayHello(ctx, req); err != nil {
					b.Fatal(err)
				}
			}
		}
	})

	b.Log("a stream amortises the per-call framing, the headers and the status trailer over every " +
		"message. That is the whole performance argument for streaming, and it is why a " +
		"chatty API is the thing to fix before the transport.")
}
