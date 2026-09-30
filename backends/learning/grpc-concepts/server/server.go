// Package server implements the four RPC patterns, and the mistakes each one invites.
//
// # What gRPC is, in one paragraph that is not marketing
//
// A .proto file describes messages and methods. A generator turns it into a typed client and a server interface
// in every language. Calls travel over HTTP/2 with protobuf bodies, so one TCP connection carries many
// concurrent calls with no head-of-line blocking, and the payload is binary with no field names on the wire.
//
// What that buys, concretely: a client that cannot send a field the server does not have, a compiler error
// instead of a 400, and about a third the bytes of the equivalent JSON.
//
// What it costs: a browser cannot speak it without a proxy, a curl command cannot reproduce a request, and every
// schema change is a deployment-ordering question.
//
// # The four patterns and what changes
//
//	unary          one in, one out. The generated method looks like a function call.
//	server stream  one in, many out. The method takes a stream and returns only an error.
//	client stream  many in, one out. The method's Recv ends with io.EOF, which is not an error.
//	bidirectional  many both ways, INDEPENDENTLY. The naive implementation deadlocks.
//
// # The three things that are different from HTTP and bite immediately
//
// A DEADLINE is mandatory in practice and propagates. A client's deadline becomes the server's context deadline,
// and the server's outgoing calls inherit it. A call with no deadline can hang forever, which in a mesh means
// one slow service holds connections everywhere.
//
// ERRORS are a code, a message and optional structured DETAILS. codes.NotFound is not 404: it is its own
// enumeration with seventeen values, and mapping it to HTTP is a gateway's job. Returning a plain Go error gives
// the client codes.Unknown, which tells it nothing.
//
// METADATA is HTTP/2 headers, sent before the body and again as trailers after it. Keys are lower-cased, and a
// "-bin" suffix means the value is base64-encoded binary. Headers go out with the first response message, so
// SetHeader after that fails, and the value is not sent at all. It does not slide into the trailers. Anything
// known only once the work is done has to be set as a trailer from the start.
package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	chatv1 "github.com/alexvervloet/learn-go/backends/learning/grpc-concepts/gen/chatv1"
	greeterv1 "github.com/alexvervloet/learn-go/backends/learning/grpc-concepts/gen/greeterv1"
	stockv1 "github.com/alexvervloet/learn-go/backends/learning/grpc-concepts/gen/stockv1"
	uploadv1 "github.com/alexvervloet/learn-go/backends/learning/grpc-concepts/gen/uploadv1"
)

// Greeter implements the unary service.
//
// The embedded UnimplementedGreeterServer is not optional in practice, and the reason is a design decision in
// protoc-gen-go-grpc worth knowing: with it embedded, adding a method to the .proto leaves this type still
// compiling and the new method returns codes.Unimplemented. Without it, adding a method breaks the build.
//
// Which is right depends on what you want to happen when the schema moves ahead of the server. For a service
// whose proto is owned elsewhere, embedding is the only way to deploy at all. For one that owns its own proto, a
// build failure is the better outcome, and `option (grpc.go.require_unimplemented_servers) = false` turns the
// embedding off.
type Greeter struct {
	greeterv1.UnimplementedGreeterServer

	// ID is returned in served_by, so a test can tell which server answered.
	ID int32

	calls atomic.Int64
}

// SayHello is the unary call.
func (g *Greeter) SayHello(ctx context.Context, req *greeterv1.HelloRequest) (*greeterv1.HelloReply, error) {
	g.calls.Add(1)

	// Validate first, and return InvalidArgument rather than an empty response. A gRPC server that accepts an
	// empty name and replies "Hello, !" has moved the error to the client's logs.
	if req.GetName() == "" {
		return nil, status.Error(codes.InvalidArgument, "name is required")
	}

	// The context carries the client's deadline. Checking it before doing work is what makes a deadline mean
	// anything: without this, a server with a queue does the work for calls the client has already given up on.
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}

	greeting := "Hello, " + req.GetName()

	// GetNickname on an `optional` field returns the value; Nickname != nil is how you ask whether it was SET.
	// That distinction is the whole reason the field is optional, and the getter hides it.
	if req.Nickname != nil {
		greeting = "Hello, " + req.GetNickname()
	}

	for _, title := range req.GetTitles() {
		greeting = title + " " + greeting
	}

	// Metadata sent as HEADERS, which means before the response body. Set after the headers have gone out, it
	// would not be sent at all: SetHeader returns an error and the value is dropped.
	if err := grpcSetHeader(ctx, metadata.Pairs(
		"x-server-id", fmt.Sprint(g.ID),
		"x-call-count", fmt.Sprint(g.calls.Load()),
	)); err != nil {
		return nil, err
	}

	// And a TRAILER, set here and sent after the response. Trailers are how gRPC reports a status at all, and
	// they are the only way to send metadata whose value is not known until the work is done.
	if err := grpcSetTrailer(ctx, metadata.Pairs("x-duration-ms", "0")); err != nil {
		return nil, err
	}

	return &greeterv1.HelloReply{Message: greeting, ServedBy: g.ID}, nil
}

// SayHelloTwice exists to show that the method name is part of the wire path.
func (g *Greeter) SayHelloTwice(ctx context.Context, req *greeterv1.HelloRequest) (*greeterv1.HelloReply, error) {
	reply, err := g.SayHello(ctx, req)
	if err != nil {
		return nil, err
	}

	reply.Message = reply.GetMessage() + " " + reply.GetMessage()

	return reply, nil
}

// Fail returns the requested status code, with details when asked.
//
// # What error details are for
//
// A code and a message are what HTTP gives you. Details are a repeated Any, so an error can carry a structured
// payload: which fields were invalid, how long to wait before retrying, a link to documentation.
//
// google.golang.org/genproto/googleapis/rpc/errdetails defines the standard ones, and using those rather than
// your own means a generic client can render them. BadRequest with FieldViolations is the one every validation
// error should use.
func (g *Greeter) Fail(_ context.Context, req *greeterv1.FailRequest) (*greeterv1.HelloReply, error) {
	code := codes.Code(uint32(req.GetCode()))

	message := req.GetMessage()
	if message == "" {
		message = "requested failure"
	}

	if !req.GetWithDetails() {
		return nil, status.Error(code, message)
	}

	st := status.New(code, message)

	// WithDetails can fail, because it marshals each detail into an Any. Ignoring that error gives an error
	// without its details and no indication why, so the fallback returns the plain status rather than nothing.
	withDetails, err := st.WithDetails(
		&errdetails.BadRequest{
			FieldViolations: []*errdetails.BadRequest_FieldViolation{
				{Field: "name", Description: "must not be empty"},
				{Field: "titles[0]", Description: "must be one of Dr, Prof"},
			},
		},
		&errdetails.RetryInfo{
			RetryDelay: durationProto(2 * time.Second),
		},
	)
	if err != nil {
		return nil, st.Err()
	}

	return nil, withDetails.Err()
}

// Calls reports how many times SayHello was called.
func (g *Greeter) Calls() int64 { return g.calls.Load() }

// StockTicker implements server streaming.
type StockTicker struct {
	stockv1.UnimplementedStockTickerServer

	// Interval between ticks, small in tests.
	Interval time.Duration

	sent atomic.Int64
}

// Watch streams prices.
//
// # The three things a streaming server must do
//
// Check the CONTEXT. A client that hangs up cancels the server's context, and a server that does not check it
// keeps producing forever. This is the most common gRPC leak there is, and it does not show up in testing
// because tests do not hang up mid-stream.
//
// Return the context's error as a STATUS, not as a plain error. status.FromContextError maps context.Canceled to
// codes.Canceled and DeadlineExceeded to codes.DeadlineExceeded, which is what a client's error handling expects.
//
// Never call Send concurrently. A grpc.ServerStream is safe for one sender and one receiver, and no more. Two
// goroutines sending on one stream is the same bug as two goroutines writing to a WebSocket.
func (s *StockTicker) Watch(req *stockv1.WatchRequest, stream stockv1.StockTicker_WatchServer) error {
	symbols := req.GetSymbols()
	if len(symbols) == 0 {
		return status.Error(codes.InvalidArgument, "at least one symbol is required")
	}

	interval := s.Interval
	if interval <= 0 {
		interval = 10 * time.Millisecond
	}

	ctx := stream.Context()

	limit := req.GetLimit()

	for i := int32(0); limit == 0 || i < limit; i++ {
		select {
		case <-ctx.Done():
			// The client is gone. Returning the status form rather than ctx.Err() is what makes the
			// client see Canceled rather than Unknown.
			return status.FromContextError(ctx.Err()).Err()

		case <-time.After(interval):
		}

		symbol := symbols[int(i)%len(symbols)]

		if err := stream.Send(&stockv1.Price{
			Symbol:     symbol,
			Cents:      10_000 + int64(i)*25,
			ObservedAt: timestamppb.New(time.Now()),
			Sequence:   i,
		}); err != nil {
			// A Send error means the stream is broken, and the only thing to do is return. Wrapping
			// it in a status would overwrite whatever the transport already reported.
			return fmt.Errorf("sending tick %d: %w", i, err)
		}

		s.sent.Add(1)
	}

	// Returning nil completes the stream with codes.OK, which is what the client sees as io.EOF from Recv.
	return nil
}

// WatchWithDeadline is Watch, and exists so a test can name the deadline case separately.
func (s *StockTicker) WatchWithDeadline(req *stockv1.WatchRequest, stream stockv1.StockTicker_WatchWithDeadlineServer) error {
	return s.Watch(req, stream)
}

// Sent reports how many ticks were sent.
func (s *StockTicker) Sent() int64 { return s.sent.Load() }

// Uploader implements client streaming.
type Uploader struct {
	uploadv1.UnimplementedUploaderServer

	// MaxBytes caps an upload. Without it, a client streaming forever fills the server's memory, and unlike an
	// HTTP body there is no Content-Length to check first.
	MaxBytes int64
}

// Upload receives chunks and replies once.
//
// # The io.EOF that is not an error
//
// Recv returns io.EOF when the client has called CloseSend. That is the normal, successful end of a client
// stream, and it is the one place gRPC's Go API uses an error value to mean success. Code that treats every Recv
// error as a failure rejects every successful upload.
//
// Any OTHER error from Recv is a real failure, including codes.Canceled when the client hangs up mid-upload, so
// the check has to be errors.Is(err, io.EOF) and not err != nil.
func (u *Uploader) Upload(stream uploadv1.Uploader_UploadServer) error {
	var (
		meta     *uploadv1.UploadMetadata
		received int64
		chunks   int32
	)

	hash := sha256.New()

	maxBytes := u.MaxBytes
	if maxBytes <= 0 {
		maxBytes = 10 << 20
	}

	for {
		chunk, err := stream.Recv()

		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("receiving a chunk: %w", err)
		}

		switch payload := chunk.GetPayload().(type) {
		case *uploadv1.Chunk_Metadata:
			if meta != nil {
				return status.Error(codes.InvalidArgument,
					"metadata sent twice; it must be the first message only")
			}

			meta = payload.Metadata

		case *uploadv1.Chunk_Data:
			if meta == nil {
				return status.Error(codes.InvalidArgument,
					"the first message must be metadata")
			}

			received += int64(len(payload.Data))

			if received > maxBytes {
				// ResourceExhausted, not InvalidArgument: the request was well formed and
				// there was too much of it. The distinction tells the client whether to fix
				// the request or to split it.
				return status.Errorf(codes.ResourceExhausted,
					"upload exceeds %d bytes", maxBytes)
			}

			if _, err := hash.Write(payload.Data); err != nil {
				return status.Errorf(codes.Internal, "hashing: %v", err)
			}

			chunks++

		case nil:
			// A Chunk with no oneof set. proto3 allows it and it means nothing, so saying so is
			// better than counting it as an empty data chunk.
			return status.Error(codes.InvalidArgument, "a chunk must carry metadata or data")

		default:
			return status.Errorf(codes.InvalidArgument, "unknown payload type %T", payload)
		}
	}

	if meta == nil {
		return status.Error(codes.InvalidArgument, "no metadata was sent")
	}

	// SendAndClose, not Send. A client-streaming server sends exactly one message and this is the method that
	// does it; calling Send would not compile, which is one of the places the generated types stop a mistake.
	return stream.SendAndClose(&uploadv1.UploadSummary{
		Filename:      meta.GetFilename(),
		BytesReceived: received,
		Chunks:        chunks,
		Sha256:        hex.EncodeToString(hash.Sum(nil)),
	})
}

// Chat implements bidirectional streaming.
type Chat struct {
	chatv1.UnimplementedChatServer

	// Mode selects the implementation, so a test can run the broken one deliberately.
	Mode ChatMode
}

// ChatMode picks between the two shapes.
type ChatMode int

const (
	// Concurrent is the correct shape: a goroutine reads while the main body writes, so the two directions are
	// independent.
	Concurrent ChatMode = iota

	// ReadThenWrite is the shape that deadlocks. The server reads until EOF before sending anything, so a
	// client that waits for a reply before closing waits forever.
	ReadThenWrite
)

// Join runs a bidirectional stream.
func (c *Chat) Join(stream chatv1.Chat_JoinServer) error {
	if c.Mode == ReadThenWrite {
		return c.readThenWrite(stream)
	}

	return c.concurrent(stream)
}

// concurrent is the correct implementation.
//
// One goroutine per direction, and the two ends have to be joined carefully:
//
//	the READER ends on io.EOF or a real error, and tells the writer by closing a channel
//	the WRITER ends when the reader is done or the context is cancelled
//	Join returns only once BOTH have finished, or the stream is closed under a goroutine still
//	  using it, which is a use-after-free of the transport
//
// The last point is the one that produces "panic: send on closed connection" in production.
func (c *Chat) concurrent(stream chatv1.Chat_JoinServer) error {
	ctx := stream.Context()

	incoming := make(chan *chatv1.ChatMessage, 16)

	var (
		wg       sync.WaitGroup
		recvErr  error
		recvOnce sync.Once
	)

	wg.Add(1)

	go func() {
		defer wg.Done()
		defer close(incoming)

		for {
			msg, err := stream.Recv()

			if errors.Is(err, io.EOF) {
				return
			}
			if err != nil {
				recvOnce.Do(func() { recvErr = err })
				return
			}

			select {
			case incoming <- msg:
			case <-ctx.Done():
				return
			}
		}
	}()

	// The writer, on this goroutine. Echoing each message back plus one unprompted message at the end, so a test
	// can see that the server sends without having been asked.
	var sequence int32

	for msg := range incoming {
		sequence++

		if err := stream.Send(&chatv1.ChatMessage{
			From:     "server",
			Text:     "echo: " + msg.GetText(),
			Sequence: sequence,
		}); err != nil {
			// Drain the reader before returning, or Join returns while the goroutine is still
			// touching the stream. This cannot hang on a reader parked in Recv: grpc-go ends the
			// stream on any SendMsg error, which makes that Recv return. TestAFailedSendEndsTheBidiCall
			// holds it to that.
			wg.Wait()
			return fmt.Errorf("sending: %w", err)
		}
	}

	wg.Wait()

	if recvErr != nil {
		return fmt.Errorf("receiving: %w", recvErr)
	}

	// One more, after the client has finished sending. This is what "independent directions" means and what a
	// half-duplex protocol cannot do.
	if err := stream.Send(&chatv1.ChatMessage{
		From:     "server",
		Text:     "goodbye",
		Sequence: sequence + 1,
	}); err != nil {
		return fmt.Errorf("sending the farewell: %w", err)
	}

	return nil
}

// readThenWrite is the broken shape, kept because the deadlock is the lesson.
//
// It reads every message before sending any. Correct if the client always closes its send side first, and a
// deadlock the moment the client waits for a reply before closing, which is what a request-response client does
// without thinking about it.
func (c *Chat) readThenWrite(stream chatv1.Chat_JoinServer) error {
	var received []*chatv1.ChatMessage

	for {
		msg, err := stream.Recv()

		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("receiving: %w", err)
		}

		received = append(received, msg)
	}

	for i, msg := range received {
		if err := stream.Send(&chatv1.ChatMessage{
			From:     "server",
			Text:     "echo: " + msg.GetText(),
			Sequence: int32(i + 1),
		}); err != nil {
			return fmt.Errorf("sending: %w", err)
		}
	}

	return nil
}

// grpcSetHeader sends metadata as HEADERS, turning the failure into a status.
//
// grpc.SetHeader merges when it is called more than once, and returns an error when it is called after the
// headers have gone out: after SendHeader, after the first Send, or once the status is on its way. Returning that
// unwrapped would give the client codes.Unknown for what is a server bug.
func grpcSetHeader(ctx context.Context, md metadata.MD) error {
	if err := grpc.SetHeader(ctx, md); err != nil {
		return status.Errorf(codes.Internal, "setting headers: %v", err)
	}
	return nil
}

// grpcSetTrailer sends metadata as TRAILERS, after the response.
//
// I wrote this with no error return and a comment saying SetTrailer cannot fail, because trailers are not sent
// until the handler returns and so cannot be "too late". The linter disagreed: it returns an error, for the case
// where the context is not a server context at all.
//
// Which is a useful correction and does not change the asymmetry that matters. SetHeader fails when it is CALLED
// TOO LATE, after the first Send; SetTrailer has no such window, which is why anything computed during the call
// (a duration, a row count, a cache status) belongs in a trailer.
//
// The failure here is a programming error rather than a runtime condition, so it becomes codes.Internal rather
// than being ignored.
func grpcSetTrailer(ctx context.Context, md metadata.MD) error {
	if err := grpc.SetTrailer(ctx, md); err != nil {
		return status.Errorf(codes.Internal, "setting trailers: %v", err)
	}
	return nil
}

// durationProto converts a Go duration for errdetails.RetryInfo.
func durationProto(d time.Duration) *durationpb.Duration {
	return durationpb.New(d)
}
