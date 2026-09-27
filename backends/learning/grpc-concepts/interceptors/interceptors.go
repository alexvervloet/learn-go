// Package interceptors is gRPC's middleware, and the thing about it that is not like HTTP middleware.
//
// # Four kinds, not one
//
// HTTP has one middleware shape: func(http.Handler) http.Handler. gRPC has four, because a call is either unary
// or streaming and either inbound or outbound:
//
//	grpc.UnaryServerInterceptor    server side, one request
//	grpc.StreamServerInterceptor   server side, a stream
//	grpc.UnaryClientInterceptor    client side, one request
//	grpc.StreamClientInterceptor   client side, a stream
//
// A logging interceptor therefore has to be written twice, and a codebase with one of the two silently does not
// log half its traffic. That is the single most common gRPC observability gap.
//
// # The streaming one is harder and the reason is worth understanding
//
// A unary interceptor sees the request and the response, so it can time the call, log the payload, and retry.
//
// A streaming interceptor sees the stream OPENING and nothing else. To observe messages it has to WRAP the
// stream and intercept SendMsg and RecvMsg, which means implementing grpc.ServerStream, which means forwarding
// six methods. That is why so many streaming interceptors only count streams and not messages.
//
// # Ordering
//
// Interceptors run in the order given, outermost first, the same as HTTP middleware. grpc.ChainUnaryInterceptor
// appends, so calling it twice does not replace the chain. Before Go gRPC 1.28 there was no chaining at all and
// every project had its own; the ones that still do are usually go-grpc-middleware.
package interceptors

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// Recorder collects what the interceptors saw, so a test can assert rather than read logs.
type Recorder struct {
	mu sync.Mutex

	UnaryCalls   []CallRecord
	StreamCalls  []StreamRecord
	MessagesSent atomic.Int64
	MessagesRecv atomic.Int64
	Panics       atomic.Int64
}

// CallRecord is one unary call.
type CallRecord struct {
	Method   string
	Code     codes.Code
	Duration time.Duration
	Metadata map[string][]string
}

// StreamRecord is one stream.
type StreamRecord struct {
	Method   string
	Code     codes.Code
	Sent     int64
	Received int64
}

func (r *Recorder) addUnary(rec CallRecord) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.UnaryCalls = append(r.UnaryCalls, rec)
}

func (r *Recorder) addStream(rec StreamRecord) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.StreamCalls = append(r.StreamCalls, rec)
}

// Unary returns a copy of the unary records.
func (r *Recorder) Unary() []CallRecord {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]CallRecord(nil), r.UnaryCalls...)
}

// Streams returns a copy of the stream records.
func (r *Recorder) Streams() []StreamRecord {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]StreamRecord(nil), r.StreamCalls...)
}

// Reset clears the recorder.
func (r *Recorder) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.UnaryCalls = nil
	r.StreamCalls = nil
	r.MessagesSent.Store(0)
	r.MessagesRecv.Store(0)
	r.Panics.Store(0)
}

// LogUnary times and records a unary call.
//
// status.Code(err) rather than a type assertion. It returns codes.OK for a nil error and codes.Unknown for an
// error that is not a status, which is exactly the two cases a log line needs and the two a type assertion
// would have to handle by hand.
func LogUnary(log *slog.Logger, rec *Recorder) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler) (any, error) {
		start := time.Now()

		resp, err := handler(ctx, req)

		duration := time.Since(start)
		code := status.Code(err)

		incoming, _ := metadata.FromIncomingContext(ctx)

		if rec != nil {
			rec.addUnary(CallRecord{
				Method:   info.FullMethod,
				Code:     code,
				Duration: duration,
				Metadata: incoming,
			})
		}

		if log != nil {
			// The level depends on the code, which is the point of logging the code rather than
			// the error: a NotFound is not a problem and an Internal is.
			level := slog.LevelInfo
			if code != codes.OK && code != codes.NotFound && code != codes.InvalidArgument {
				level = slog.LevelError
			}

			log.Log(ctx, level, "unary call",
				"method", info.FullMethod,
				"code", code.String(),
				"duration", duration)
		}

		return resp, err
	}
}

// LogStream records a stream, including its message counts.
//
// The wrapping is what makes the counts possible, and it is the part a streaming interceptor usually skips.
func LogStream(log *slog.Logger, rec *Recorder) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo,
		handler grpc.StreamHandler) error {
		wrapped := &countingServerStream{ServerStream: ss, recorder: rec}

		err := handler(srv, wrapped)

		code := status.Code(err)

		if rec != nil {
			rec.addStream(StreamRecord{
				Method:   info.FullMethod,
				Code:     code,
				Sent:     wrapped.sent.Load(),
				Received: wrapped.received.Load(),
			})
		}

		if log != nil {
			log.Info("stream closed",
				"method", info.FullMethod,
				"code", code.String(),
				"sent", wrapped.sent.Load(),
				"received", wrapped.received.Load())
		}

		return err
	}
}

// countingServerStream wraps a stream to count messages.
//
// # The six methods
//
// grpc.ServerStream has SetHeader, SendHeader, SetTrailer, Context, SendMsg and RecvMsg. Embedding the interface
// forwards all of them, so only the two being intercepted need writing. That embedding is the reason this is
// twenty lines rather than sixty, and forgetting it produces a type that does not satisfy the interface with an
// error naming whichever method comes first alphabetically.
type countingServerStream struct {
	grpc.ServerStream

	recorder *Recorder

	sent     atomic.Int64
	received atomic.Int64
}

// SendMsg counts and forwards.
func (s *countingServerStream) SendMsg(m any) error {
	if err := s.ServerStream.SendMsg(m); err != nil {
		return err
	}

	s.sent.Add(1)

	if s.recorder != nil {
		s.recorder.MessagesSent.Add(1)
	}

	return nil
}

// RecvMsg counts and forwards.
//
// The count is incremented only on SUCCESS, so the io.EOF that ends a client stream is not counted as a message.
// Counting it makes every stream report one more message than it carried, which is the kind of off-by-one that
// gets noticed a year later in a capacity plan.
//
// Worth knowing, and measured rather than assumed: a SERVER-streaming call goes through here too. grpc-go
// delivers the single request message by calling RecvMsg on the stream the interceptor wrapped, so a
// server-streaming call reports 1 received and N sent. I expected 0, because the request looks like it arrives
// before the handler runs, and it does not.
func (s *countingServerStream) RecvMsg(m any) error {
	if err := s.ServerStream.RecvMsg(m); err != nil {
		return err
	}

	s.received.Add(1)

	if s.recorder != nil {
		s.recorder.MessagesRecv.Add(1)
	}

	return nil
}

// RecoverUnary turns a panic into codes.Internal.
//
// # Why this is not optional
//
// A panic in a gRPC handler kills the PROCESS, not the call. grpc-go does not recover, deliberately: it is a
// library and swallowing a panic hides a bug. So a service without a recovery interceptor is one nil map away
// from restarting, and every in-flight call on every stream dies with it.
//
// The message must NOT contain the panic value. A panic often carries a pointer, a struct or a wrapped error
// holding a query or a token, and putting it in a status sends it to the client. It goes in the log.
func RecoverUnary(log *slog.Logger, rec *Recorder) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler) (resp any, err error) {
		defer func() {
			r := recover()
			if r == nil {
				return
			}

			if rec != nil {
				rec.Panics.Add(1)
			}

			if log != nil {
				// The panic value and the stack go here, where an operator can see them.
				log.Error("handler panicked",
					"method", info.FullMethod,
					"panic", fmt.Sprint(r),
					"stack", string(stack()))
			}

			// And the client gets a code and nothing else.
			err = status.Error(codes.Internal, "internal error")
		}()

		return handler(ctx, req)
	}
}

// RecoverStream is the same for streams, and it has to exist separately.
//
// This is the duplication the package doc warns about: a service with RecoverUnary and not RecoverStream has
// recovery on half its traffic, and the half without it is the half that holds long-lived connections.
func RecoverStream(log *slog.Logger, rec *Recorder) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo,
		handler grpc.StreamHandler) (err error) {
		defer func() {
			r := recover()
			if r == nil {
				return
			}

			if rec != nil {
				rec.Panics.Add(1)
			}

			if log != nil {
				log.Error("stream handler panicked",
					"method", info.FullMethod,
					"panic", fmt.Sprint(r),
					"stack", string(stack()))
			}

			err = status.Error(codes.Internal, "internal error")
		}()

		return handler(srv, ss)
	}
}

// RequireMetadata rejects a call without a given metadata key, which is the shape of an auth interceptor.
//
// # Metadata keys are lower-cased
//
// metadata.FromIncomingContext returns keys already lower-cased, because HTTP/2 requires lower-case header
// names. So md.Get("Authorization") returns nothing and md.Get("authorization") works, and the mistake is
// invisible: a service that looks for the wrong case rejects every request as unauthenticated.
//
// md.Get does the lower-casing for you. Indexing the map directly does not, which is why Get exists.
func RequireMetadata(key string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler) (any, error) {
		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			return nil, status.Error(codes.Unauthenticated, "no metadata")
		}

		values := md.Get(key)
		if len(values) == 0 || values[0] == "" {
			return nil, status.Errorf(codes.Unauthenticated, "%s is required", key)
		}

		return handler(ctx, req)
	}
}

// AddMetadataUnary is a CLIENT interceptor that attaches metadata to every outgoing call.
//
// metadata.AppendToOutgoingContext, not NewOutgoingContext. NewOutgoingContext REPLACES whatever was there, so
// an interceptor using it discards metadata set by an interceptor above it or by the caller. Append is almost
// always what is meant and it is the longer name.
func AddMetadataUnary(pairs ...string) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any,
		cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		ctx = metadata.AppendToOutgoingContext(ctx, pairs...)

		return invoker(ctx, method, req, reply, cc, opts...)
	}
}

// RetryUnary retries a call on a retryable code.
//
// # Which codes are retryable
//
// Unavailable and ResourceExhausted, and nothing else by default. The reasoning:
//
//	Unavailable        the server was not reachable, so the call probably did not happen
//	ResourceExhausted  a rate limit or a quota, so waiting may help
//	DeadlineExceeded   the call MAY have happened. Retrying a non-idempotent call that timed out
//	                   is how you charge a card twice, so it is not in the default set.
//	Internal, Unknown  the call happened and failed. Retrying repeats the failure.
//	InvalidArgument    deterministic. Retrying is a slow way to fail.
//
// An interceptor that retries everything is worse than one that retries nothing.
//
// # And the part that makes retries dangerous
//
// A retry on a unary call is safe only if the method is IDEMPOTENT, and gRPC has no way to declare that. So the
// safe default is to retry nothing and to opt in per method, which is what grpc-go's built-in retry policy does
// through a service config rather than an interceptor.
func RetryUnary(attempts int, backoff time.Duration, retryable ...codes.Code) grpc.UnaryClientInterceptor {
	if len(retryable) == 0 {
		retryable = []codes.Code{codes.Unavailable, codes.ResourceExhausted}
	}

	set := make(map[codes.Code]struct{}, len(retryable))
	for _, c := range retryable {
		set[c] = struct{}{}
	}

	return func(ctx context.Context, method string, req, reply any,
		cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		var err error

		for attempt := 1; attempt <= attempts; attempt++ {
			err = invoker(ctx, method, req, reply, cc, opts...)
			if err == nil {
				return nil
			}

			if _, ok := set[status.Code(err)]; !ok {
				return err
			}

			if attempt == attempts {
				break
			}

			// The context bounds the retries, deadline included. Sleeping through a cancelled
			// context and then retrying is how a client with a 100ms deadline takes 600ms to
			// fail.
			select {
			case <-ctx.Done():
				return status.FromContextError(ctx.Err()).Err()
			case <-time.After(backoff * time.Duration(attempt)):
			}
		}

		return fmt.Errorf("after %d attempts: %w", attempts, err)
	}
}

// ErrNoStream is returned by the stream interceptor when it cannot open a stream at all.
var ErrNoStream = errors.New("no stream")

// stack captures a stack trace for the recovery interceptors.
func stack() []byte {
	buf := make([]byte, 8<<10)

	// runtime.Stack with false, so only THIS goroutine's stack is captured. Passing true dumps every goroutine,
	// which in a service under load is megabytes per panic and turns a bug into a log-volume incident.
	n := runtime.Stack(buf, false)

	return buf[:n]
}
