package server_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	chatv1 "github.com/alexvervloet/learn-go/backends/learning/grpc-concepts/gen/chatv1"
	greeterv1 "github.com/alexvervloet/learn-go/backends/learning/grpc-concepts/gen/greeterv1"
	stockv1 "github.com/alexvervloet/learn-go/backends/learning/grpc-concepts/gen/stockv1"
	uploadv1 "github.com/alexvervloet/learn-go/backends/learning/grpc-concepts/gen/uploadv1"
	"github.com/alexvervloet/learn-go/backends/learning/grpc-concepts/grpctest"
	"github.com/alexvervloet/learn-go/backends/learning/grpc-concepts/server"
)

func greeterClient(t *testing.T, g *server.Greeter) greeterv1.GreeterClient {
	t.Helper()

	srv := grpctest.Start(t, grpctest.Options{
		Register: func(s *grpc.Server) { greeterv1.RegisterGreeterServer(s, g) },
	})

	return greeterv1.NewGreeterClient(srv.Dial(t))
}

// TestUnaryCall is the baseline, and pins the optional-field behaviour.
func TestUnaryCall(t *testing.T) {
	client := greeterClient(t, &server.Greeter{ID: 7})

	ctx := grpctest.Context(t, 5*time.Second)

	for _, tc := range []struct {
		name string
		req  *greeterv1.HelloRequest
		want string
	}{
		{"plain", &greeterv1.HelloRequest{Name: "Ada"}, "Hello, Ada"},
		{"with titles", &greeterv1.HelloRequest{
			Name:   "Ada",
			Titles: []string{"Dr", "Prof"},
		}, "Prof Dr Hello, Ada"},

		// An optional field SET to the empty string is different from one not set, which is the whole
		// reason the field is optional.
		{"nickname set", &greeterv1.HelloRequest{
			Name:     "Ada",
			Nickname: strPtr("Addy"),
		}, "Hello, Addy"},
		{"nickname set to empty", &greeterv1.HelloRequest{
			Name:     "Ada",
			Nickname: strPtr(""),
		}, "Hello, "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reply, err := client.SayHello(ctx, tc.req)
			if err != nil {
				t.Fatal(err)
			}

			t.Logf("%q -> %q (served by %d)", tc.req.String(), reply.GetMessage(),
				reply.GetServedBy())

			if reply.GetMessage() != tc.want {
				t.Errorf("got %q, want %q", reply.GetMessage(), tc.want)
			}
		})
	}

	t.Log("the last two cases differ only in whether nickname was SET, and without `optional` in " +
		"the proto they would be indistinguishable: proto3 without it cannot tell an absent " +
		"string from an empty one, so 'clear this field' cannot be expressed")
}

// TestValidationIsTheServersJob, because an empty request is valid protobuf.
func TestValidationIsTheServersJob(t *testing.T) {
	client := greeterClient(t, &server.Greeter{ID: 1})

	ctx := grpctest.Context(t, 5*time.Second)

	// An empty message. protobuf has no required fields in proto3, so this is a perfectly valid
	// HelloRequest and the wire encoding is zero bytes.
	_, err := client.SayHello(ctx, &greeterv1.HelloRequest{})

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("got a non-status error: %v", err)
	}

	t.Logf("an empty request: %s: %s", st.Code(), st.Message())

	if st.Code() != codes.InvalidArgument {
		t.Errorf("got %s, want InvalidArgument", st.Code())
	}

	t.Log("proto3 removed required fields, so every field is optional at the wire level and " +
		"validation is entirely the server's job. A schema cannot express 'name is required'.")
}

// TestStatusCodesAreNotHTTPCodes.
func TestStatusCodesAreNotHTTPCodes(t *testing.T) {
	client := greeterClient(t, &server.Greeter{ID: 1})

	ctx := grpctest.Context(t, 5*time.Second)

	for _, code := range []codes.Code{
		codes.NotFound,
		codes.PermissionDenied,
		codes.Unauthenticated,
		codes.ResourceExhausted,
		codes.FailedPrecondition,
		codes.Aborted,
		codes.AlreadyExists,
		codes.Unimplemented,
		codes.Internal,
	} {
		_, err := client.Fail(ctx, &greeterv1.FailRequest{
			Code:    int32(code),
			Message: "test",
		})

		st, _ := status.FromError(err)

		if st.Code() != code {
			t.Errorf("asked for %s, got %s", code, st.Code())
		}

		t.Logf("%-20s numeric %2d", code.String(), int(code))
	}

	// The one that catches people: a PLAIN Go error becomes codes.Unknown, which tells the client
	// nothing at all.
	plainErrorServer := &failWithPlainError{}

	srv := grpctest.Start(t, grpctest.Options{
		Register: func(s *grpc.Server) { greeterv1.RegisterGreeterServer(s, plainErrorServer) },
	})

	plain := greeterv1.NewGreeterClient(srv.Dial(t))

	_, err := plain.SayHello(ctx, &greeterv1.HelloRequest{Name: "x"})

	st, _ := status.FromError(err)

	t.Logf("a plain errors.New from a handler: %s: %q", st.Code(), st.Message())

	if st.Code() != codes.Unknown {
		t.Errorf("got %s, want Unknown", st.Code())
	}

	t.Log("codes.Unknown is what a client cannot act on: it cannot tell a bug from a bad request " +
		"from a transient failure, so it either retries everything or nothing")
}

// failWithPlainError returns a plain Go error, to show what the client sees.
type failWithPlainError struct {
	greeterv1.UnimplementedGreeterServer
}

func (f *failWithPlainError) SayHello(context.Context, *greeterv1.HelloRequest) (*greeterv1.HelloReply, error) {
	return nil, errors.New("something went wrong")
}

// TestErrorDetailsCarryStructure, which HTTP has no equivalent for.
func TestErrorDetailsCarryStructure(t *testing.T) {
	client := greeterClient(t, &server.Greeter{ID: 1})

	ctx := grpctest.Context(t, 5*time.Second)

	_, err := client.Fail(ctx, &greeterv1.FailRequest{
		Code:        int32(codes.InvalidArgument),
		Message:     "validation failed",
		WithDetails: true,
	})

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("not a status: %v", err)
	}

	t.Logf("%s: %s, with %d detail(s)", st.Code(), st.Message(), len(st.Details()))

	var (
		sawBadRequest bool
		sawRetryInfo  bool
	)

	for _, detail := range st.Details() {
		switch typed := detail.(type) {
		case *errdetails.BadRequest:
			sawBadRequest = true

			for _, v := range typed.GetFieldViolations() {
				t.Logf("  field %-12s %s", v.GetField(), v.GetDescription())
			}

		case *errdetails.RetryInfo:
			sawRetryInfo = true

			t.Logf("  retry after %v", typed.GetRetryDelay().AsDuration())

		default:
			t.Logf("  unknown detail type %T", typed)
		}
	}

	if !sawBadRequest {
		t.Error("no BadRequest detail")
	}
	if !sawRetryInfo {
		t.Error("no RetryInfo detail")
	}

	t.Log("a client can act on this without parsing a message string: which fields were wrong, " +
		"and how long to wait. The standard errdetails types mean a generic client can render " +
		"them, which a custom payload cannot.")
}

// TestMetadataHeadersAndTrailers, and the difference between them.
func TestMetadataHeadersAndTrailers(t *testing.T) {
	client := greeterClient(t, &server.Greeter{ID: 42})

	ctx := grpctest.Context(t, 5*time.Second)

	// The client has to ASK for headers and trailers, with call options. A client that does not pass
	// them never sees the metadata and cannot tell it was sent.
	var (
		header  metadata.MD
		trailer metadata.MD
	)

	reply, err := client.SayHello(ctx, &greeterv1.HelloRequest{Name: "Ada"},
		grpc.Header(&header), grpc.Trailer(&trailer))
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("reply: %q", reply.GetMessage())
	t.Logf("headers:  %v", filterMetadata(header, "x-"))
	t.Logf("trailers: %v", filterMetadata(trailer, "x-"))

	if got := header.Get("x-server-id"); len(got) == 0 || got[0] != "42" {
		t.Errorf("x-server-id header is %v", got)
	}
	if got := trailer.Get("x-duration-ms"); len(got) == 0 {
		t.Error("no x-duration-ms trailer")
	}

	// Keys come back LOWER-CASED, because HTTP/2 requires it. Get does the lower-casing; indexing the
	// map does not.
	if _, ok := header["X-Server-Id"]; ok {
		t.Error("the metadata map has a capitalised key, which HTTP/2 does not allow")
	}
	if len(header.Get("X-Server-Id")) == 0 {
		t.Error("Get did not lower-case the key it was given")
	}

	t.Log("md.Get lower-cases what you pass it and indexing the map does not, so a service that " +
		`looks for md["Authorization"] rejects every request as unauthenticated`)

	// And metadata sent by the CLIENT arrives as incoming metadata on the server.
	outCtx := metadata.AppendToOutgoingContext(ctx, "x-request-id", "req-1", "x-tenant", "acme")

	if _, err := client.SayHello(outCtx, &greeterv1.HelloRequest{Name: "Ada"}); err != nil {
		t.Fatal(err)
	}
}

// TestServerStreaming.
func TestServerStreaming(t *testing.T) {
	ticker := &server.StockTicker{Interval: time.Millisecond}

	srv := grpctest.Start(t, grpctest.Options{
		Register: func(s *grpc.Server) { stockv1.RegisterStockTickerServer(s, ticker) },
	})

	client := stockv1.NewStockTickerClient(srv.Dial(t))

	ctx := grpctest.Context(t, 10*time.Second)

	stream, err := client.Watch(ctx, &stockv1.WatchRequest{
		Symbols: []string{"AAPL", "GOOG"},
		Limit:   6,
	})
	if err != nil {
		t.Fatal(err)
	}

	var prices []*stockv1.Price

	for {
		price, err := stream.Recv()

		// io.EOF is the stream ending NORMALLY. Treating it as a failure rejects every successful
		// stream, and it is the same idiom as the client-streaming server's Recv.
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("receiving: %v", err)
		}

		prices = append(prices, price)
	}

	t.Logf("received %d prices", len(prices))
	for _, p := range prices {
		t.Logf("  %-5s %6d cents  seq %d  at %s",
			p.GetSymbol(), p.GetCents(), p.GetSequence(),
			p.GetObservedAt().AsTime().Format(time.RFC3339Nano))
	}

	if len(prices) != 6 {
		t.Errorf("got %d prices, want 6", len(prices))
	}

	// The sequence is monotonic, which is the ordering guarantee a single stream gives.
	for i, p := range prices {
		if p.GetSequence() != int32(i) {
			t.Errorf("price %d has sequence %d", i, p.GetSequence())
		}
	}

	// And the symbols alternate, which shows the server is reading the request.
	if prices[0].GetSymbol() != "AAPL" || prices[1].GetSymbol() != "GOOG" {
		t.Errorf("symbols are %q and %q", prices[0].GetSymbol(), prices[1].GetSymbol())
	}
}

// TestCancellingAStreamStopsTheServer is the leak this checks for.
func TestCancellingAStreamStopsTheServer(t *testing.T) {
	ticker := &server.StockTicker{Interval: 5 * time.Millisecond}

	srv := grpctest.Start(t, grpctest.Options{
		Register: func(s *grpc.Server) { stockv1.RegisterStockTickerServer(s, ticker) },
	})

	client := stockv1.NewStockTickerClient(srv.Dial(t))

	ctx, cancel := context.WithCancel(context.Background())

	// Limit 0 means stream forever, which is the case that leaks.
	stream, err := client.Watch(ctx, &stockv1.WatchRequest{
		Symbols: []string{"AAPL"},
		Limit:   0,
	})
	if err != nil {
		cancel()
		t.Fatal(err)
	}

	for range 3 {
		if _, err := stream.Recv(); err != nil {
			cancel()
			t.Fatal(err)
		}
	}

	sentWhenCancelled := ticker.Sent()

	// The client hangs up.
	cancel()

	// The next Recv reports why.
	_, err = stream.Recv()

	st, _ := status.FromError(err)

	t.Logf("after cancelling, Recv returned %s: %v", st.Code(), err)

	if st.Code() != codes.Canceled {
		t.Errorf("got %s, want Canceled", st.Code())
	}

	// Give the server a moment, then check it stopped producing.
	time.Sleep(100 * time.Millisecond)

	sentAfter := ticker.Sent()

	t.Logf("the server had sent %d ticks when the client hung up, and %d 100ms later",
		sentWhenCancelled, sentAfter)

	// A few more may be in flight, and it must not still be running: at a 5ms interval, 100ms of
	// unchecked production would be 20 more.
	if sentAfter-sentWhenCancelled > 5 {
		t.Errorf("the server sent %d more ticks after the client hung up, so it is not "+
			"checking its context", sentAfter-sentWhenCancelled)
	}

	t.Log("a streaming server that does not select on stream.Context().Done() keeps producing " +
		"forever, and the leak does not show up in testing because tests do not usually hang " +
		"up mid-stream")
}

// TestDeadlinePropagates, which is the gRPC behaviour with no HTTP equivalent.
func TestDeadlinePropagates(t *testing.T) {
	ticker := &server.StockTicker{Interval: 20 * time.Millisecond}

	srv := grpctest.Start(t, grpctest.Options{
		Register: func(s *grpc.Server) { stockv1.RegisterStockTickerServer(s, ticker) },
	})

	client := stockv1.NewStockTickerClient(srv.Dial(t))

	// A 120ms deadline against a 20ms interval: about 6 ticks, then the deadline.
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()

	stream, err := client.WatchWithDeadline(ctx, &stockv1.WatchRequest{
		Symbols: []string{"AAPL"},
		Limit:   0,
	})
	if err != nil {
		t.Fatal(err)
	}

	received := 0

	for {
		if _, err := stream.Recv(); err != nil {
			st, _ := status.FromError(err)

			t.Logf("after %d ticks, the stream ended with %s", received, st.Code())

			if st.Code() != codes.DeadlineExceeded {
				t.Errorf("got %s, want DeadlineExceeded", st.Code())
			}

			break
		}

		received++
	}

	if received == 0 {
		t.Error("the deadline fired before any tick arrived")
	}

	t.Log("the CLIENT's deadline became the server's context deadline. Nothing on the server " +
		"configured this: gRPC sends the deadline as the grpc-timeout header and the server " +
		"applies it, which is the mechanism that makes a mesh-wide timeout budget possible.")
}

// TestClientStreaming, and the io.EOF that means success.
func TestClientStreaming(t *testing.T) {
	srv := grpctest.Start(t, grpctest.Options{
		Register: func(s *grpc.Server) {
			uploadv1.RegisterUploaderServer(s, &server.Uploader{MaxBytes: 1 << 20})
		},
	})

	client := uploadv1.NewUploaderClient(srv.Dial(t))

	ctx := grpctest.Context(t, 10*time.Second)

	stream, err := client.Upload(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// The first message is metadata.
	if err := stream.Send(&uploadv1.Chunk{
		Payload: &uploadv1.Chunk_Metadata{
			Metadata: &uploadv1.UploadMetadata{
				Filename:    "book.txt",
				ContentType: "text/plain",
			},
		},
	}); err != nil {
		t.Fatal(err)
	}

	content := []byte(strings.Repeat("the quick brown fox. ", 500))

	const chunkSize = 1024

	for offset := 0; offset < len(content); offset += chunkSize {
		end := min(offset+chunkSize, len(content))

		if err := stream.Send(&uploadv1.Chunk{
			Payload: &uploadv1.Chunk_Data{Data: content[offset:end]},
		}); err != nil {
			t.Fatal(err)
		}
	}

	// CloseAndRecv, which closes the send side AND waits for the single reply. Two operations in one
	// call, and the reason a client stream has no separate Recv.
	summary, err := stream.CloseAndRecv()
	if err != nil {
		t.Fatal(err)
	}

	sum := sha256.Sum256(content)
	want := hex.EncodeToString(sum[:])

	t.Logf("uploaded %d bytes in %d chunks", summary.GetBytesReceived(), summary.GetChunks())
	t.Logf("sha256: %s", summary.GetSha256())

	if summary.GetBytesReceived() != int64(len(content)) {
		t.Errorf("received %d bytes, sent %d", summary.GetBytesReceived(), len(content))
	}
	if summary.GetSha256() != want {
		t.Errorf("hash mismatch:\n  got  %s\n  want %s", summary.GetSha256(), want)
	}
	if summary.GetFilename() != "book.txt" {
		t.Errorf("filename is %q", summary.GetFilename())
	}

	t.Logf("%d chunks of %d bytes reassembled to a matching hash, so the stream preserved order "+
		"and every byte", summary.GetChunks(), chunkSize)
}

// TestUploadValidation and the size limit.
func TestUploadValidation(t *testing.T) {
	srv := grpctest.Start(t, grpctest.Options{
		Register: func(s *grpc.Server) {
			uploadv1.RegisterUploaderServer(s, &server.Uploader{MaxBytes: 2048})
		},
	})

	client := uploadv1.NewUploaderClient(srv.Dial(t))

	ctx := grpctest.Context(t, 10*time.Second)

	t.Run("data before metadata", func(t *testing.T) {
		stream, err := client.Upload(ctx)
		if err != nil {
			t.Fatal(err)
		}

		if err := stream.Send(&uploadv1.Chunk{
			Payload: &uploadv1.Chunk_Data{Data: []byte("x")},
		}); err != nil {
			t.Fatal(err)
		}

		_, err = stream.CloseAndRecv()

		st, _ := status.FromError(err)

		t.Logf("%s: %s", st.Code(), st.Message())

		if st.Code() != codes.InvalidArgument {
			t.Errorf("got %s, want InvalidArgument", st.Code())
		}
	})

	t.Run("too large", func(t *testing.T) {
		stream, err := client.Upload(ctx)
		if err != nil {
			t.Fatal(err)
		}

		if err := stream.Send(&uploadv1.Chunk{
			Payload: &uploadv1.Chunk_Metadata{
				Metadata: &uploadv1.UploadMetadata{Filename: "big"},
			},
		}); err != nil {
			t.Fatal(err)
		}

		// Send until the server rejects. A Send after the server has returned an error fails with
		// EOF, which is the transport saying the stream is gone, and the real error comes from
		// CloseAndRecv.
		for range 10 {
			if err := stream.Send(&uploadv1.Chunk{
				Payload: &uploadv1.Chunk_Data{Data: make([]byte, 1024)},
			}); err != nil {
				break
			}
		}

		_, err = stream.CloseAndRecv()

		st, _ := status.FromError(err)

		t.Logf("%s: %s", st.Code(), st.Message())

		if st.Code() != codes.ResourceExhausted {
			t.Errorf("got %s, want ResourceExhausted", st.Code())
		}

		t.Log("ResourceExhausted rather than InvalidArgument: the request was well formed and " +
			"there was too much of it, which tells the client to split rather than to fix")
	})

	t.Run("no metadata at all", func(t *testing.T) {
		stream, err := client.Upload(ctx)
		if err != nil {
			t.Fatal(err)
		}

		// An empty stream: CloseAndRecv immediately.
		_, err = stream.CloseAndRecv()

		st, _ := status.FromError(err)

		t.Logf("%s: %s", st.Code(), st.Message())

		if st.Code() != codes.InvalidArgument {
			t.Errorf("got %s, want InvalidArgument", st.Code())
		}
	})
}

// TestBidirectionalStreaming, and the deadlock the naive server produces.
func TestBidirectionalStreaming(t *testing.T) {
	t.Run("concurrent server", func(t *testing.T) {
		srv := grpctest.Start(t, grpctest.Options{
			Register: func(s *grpc.Server) {
				chatv1.RegisterChatServer(s, &server.Chat{Mode: server.Concurrent})
			},
		})

		client := chatv1.NewChatClient(srv.Dial(t))

		ctx := grpctest.Context(t, 10*time.Second)

		stream, err := client.Join(ctx)
		if err != nil {
			t.Fatal(err)
		}

		// A reader goroutine, because the client has the same problem as the server: the two
		// directions are independent and a client that sends everything before reading can fill
		// the flow-control window and block.
		var (
			received []*chatv1.ChatMessage
			wg       sync.WaitGroup
			recvErr  error
		)

		wg.Add(1)

		go func() {
			defer wg.Done()

			for {
				msg, err := stream.Recv()

				if errors.Is(err, io.EOF) {
					return
				}
				if err != nil {
					recvErr = err
					return
				}

				received = append(received, msg)
			}
		}()

		for i := range 3 {
			if err := stream.Send(&chatv1.ChatMessage{
				From:     "client",
				Text:     "hello " + itoa(i),
				Sequence: int32(i),
			}); err != nil {
				t.Fatal(err)
			}
		}

		// CloseSend, not Close. It closes the SEND side and leaves the receive side open, which is
		// what lets the server reply after the client has finished asking.
		if err := stream.CloseSend(); err != nil {
			t.Fatal(err)
		}

		wg.Wait()

		if recvErr != nil {
			t.Fatal(recvErr)
		}

		t.Logf("sent 3, received %d", len(received))
		for _, m := range received {
			t.Logf("  %s: %q (seq %d)", m.GetFrom(), m.GetText(), m.GetSequence())
		}

		// Three echoes plus the unprompted farewell, which is the thing a request-response protocol
		// cannot do.
		if len(received) != 4 {
			t.Errorf("received %d messages, want 4 (3 echoes and a farewell)", len(received))
		}

		if len(received) == 4 && received[3].GetText() != "goodbye" {
			t.Errorf("the last message is %q, want the unprompted farewell", received[3].GetText())
		}

		t.Log("the fourth message was sent by the server after the client had stopped sending. " +
			"That independence is what bidirectional means, and it is why the server needs " +
			"a goroutine per direction.")
	})

	t.Run("read-then-write server deadlocks a request-response client", func(t *testing.T) {
		srv := grpctest.Start(t, grpctest.Options{
			Register: func(s *grpc.Server) {
				chatv1.RegisterChatServer(s, &server.Chat{Mode: server.ReadThenWrite})
			},
		})

		client := chatv1.NewChatClient(srv.Dial(t))

		// A SHORT deadline, because the point is that this hangs. Without one the test would wait
		// for the test binary's own timeout.
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()

		stream, err := client.Join(ctx)
		if err != nil {
			t.Fatal(err)
		}

		// The client sends one message and waits for a reply, which is what any code written
		// against a request-response mental model does.
		if err := stream.Send(&chatv1.ChatMessage{From: "client", Text: "hello"}); err != nil {
			t.Fatal(err)
		}

		start := time.Now()

		_, err = stream.Recv()

		elapsed := time.Since(start)

		st, _ := status.FromError(err)

		t.Logf("the client waited %v for a reply and got %s",
			elapsed.Round(10*time.Millisecond), st.Code())

		if st.Code() != codes.DeadlineExceeded {
			t.Errorf("got %s, want DeadlineExceeded; the deadlock did not happen", st.Code())
		}

		t.Log("the server is reading until EOF before sending anything, and the client is " +
			"waiting for a reply before closing. Neither is wrong on its own and together " +
			"they wait for each other. The only thing that ended it was the deadline.")
	})
}

func strPtr(s string) *string { return &s }

func filterMetadata(md metadata.MD, prefix string) map[string][]string {
	out := map[string][]string{}

	for k, v := range md {
		if strings.HasPrefix(k, prefix) {
			out[k] = v
		}
	}

	return out
}

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

// TestAFailedSendEndsTheBidiCall guards the writer's error path. When Send fails, the writer waits for the
// reader, and the reader may be parked in Recv with the client still waiting for a reply. Recv returns only when
// the stream ends, so that wait would hang if a failed Send left the stream open.
//
// It does not, and this test is what says so. A message over the server's MaxSendMsgSize is refused locally,
// the connection is fine, and grpc-go still ends the stream: any SendMsg error writes the final status. If a
// grpc-go upgrade changed that, this test would hang until the client's deadline and fail.
func TestAFailedSendEndsTheBidiCall(t *testing.T) {
	srv := grpctest.Start(t, grpctest.Options{
		Register: func(s *grpc.Server) {
			chatv1.RegisterChatServer(s, &server.Chat{Mode: server.Concurrent})
		},
		ServerOptions: []grpc.ServerOption{grpc.MaxSendMsgSize(64)},
	})

	client := chatv1.NewChatClient(srv.Dial(t))

	ctx := grpctest.Context(t, 3*time.Second)

	stream, err := client.Join(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// The echo of this is over 64 bytes, and the client does not close its side: it waits for an answer.
	if err := stream.Send(&chatv1.ChatMessage{From: "client", Text: strings.Repeat("x", 200)}); err != nil {
		t.Fatal(err)
	}

	_, err = stream.Recv()

	if got := status.Code(err); got != codes.ResourceExhausted {
		t.Fatalf("Recv: %v (code %s); want the handler to return the send failure, not hang until the "+
			"client's deadline", err, got)
	}
}
