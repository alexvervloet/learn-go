// Package websockets is a real-time connection and the four ways it breaks in production.
//
// # Why coder/websocket and not gorilla
//
// gorilla/websocket was the default for a decade, was archived in 2022, and is maintained again now. It is
// still fine. coder/websocket (formerly nhooyr.io/websocket) is chosen here for one reason that matters to this
// repo: its API is context-first, so every read and write takes a context and a cancelled context closes the
// connection. With gorilla, timeouts are `SetReadDeadline` calls and a context has to be bridged onto them by
// hand, which is an extra thing to get wrong in every handler.
//
// # The four failures
//
//	CONCURRENT WRITES     with gorilla/websocket, two goroutines calling WriteMessage at once panic
//	                      ("concurrent write to websocket connection"). coder/websocket locks
//	                      internally and documents every method except Read as safe to call
//	                      concurrently, so here it is not a crash. One writer goroutine and a
//	                      channel are still the design, for a different reason: see Conn.
//	NO LIVENESS CHECK     a client that opens a connection and never reads holds a goroutine and a
//	                      file descriptor forever. This is Slowloris again, at a different layer.
//	                      The fix is NOT a deadline on every read, which also cuts off clients
//	                      that only listen: it is the ping, which a live client answers and a
//	                      dead or idle-flooding one does not.
//	DEAD CONNECTIONS      TCP does not notice a peer that vanished (laptop lid closed, mobile
//	                      network dropped) until it tries to write and the retransmits time out,
//	                      which can be fifteen minutes. A ping every N seconds with a pong deadline
//	                      detects it in N.
//	UNBOUNDED BUFFERING   a slow client that cannot keep up makes the server's send channel grow
//	                      until the process runs out of memory. The answer is a bounded channel and
//	                      a policy for what happens when it is full, and the policy has to be
//	                      chosen rather than defaulted.
//
// # The origin check
//
// A browser sends an Origin header on a WebSocket handshake, and the SAME-ORIGIN POLICY DOES NOT APPLY: any
// page on any site can open a WebSocket to any server, and the cookies go with it. That is cross-site WebSocket
// hijacking, and the only thing between a service and it is checking the Origin header on the handshake.
//
// coder/websocket rejects cross-origin requests by default and OriginPatterns opts into specific ones, which is
// the right way round. `InsecureSkipVerify` is the option people reach for to make development work, and it is
// the one that ships.
package websockets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

// Config holds the settings that decide whether a connection survives contact with a real network.
type Config struct {
	// ReadTimeout is the longest a connection may go without a sign of life: a message from the
	// peer, or a ping it answered. A client that listens and answers pings stays connected
	// indefinitely, which a subscriber has to. One that has vanished, or never reads, stops
	// answering pings and is dropped, which is what stops an idle-connection flood.
	ReadTimeout time.Duration

	// WriteTimeout bounds a single write. Without it, a write to a client whose TCP window is full
	// blocks the writer goroutine indefinitely.
	WriteTimeout time.Duration

	// PingInterval is how often to ping an idle connection. This is the only way to notice a peer
	// that vanished: TCP will not tell you until it tries to write and exhausts its retransmits,
	// which is minutes.
	PingInterval time.Duration

	// SendBuffer is how many messages may be queued for a slow client.
	//
	// Zero would be unbuffered, which means every send blocks until the client reads it, which
	// means one slow client stalls whatever is broadcasting. A large buffer means a slow client
	// consumes memory until it is disconnected. There is no right answer, only a chosen one, and
	// the cost of each is measured in the tests.
	SendBuffer int

	// MaxMessageBytes caps an inbound message. The default in coder/websocket is 32 KiB, which is
	// sensible and is worth setting explicitly so the number is visible.
	MaxMessageBytes int64

	// OriginPatterns is the allowlist for the Origin header. Empty means same-origin only, which is
	// the library's default and the right default.
	OriginPatterns []string
}

// DefaultConfig is defensible rather than permissive.
func DefaultConfig() Config {
	return Config{
		ReadTimeout:  60 * time.Second,
		WriteTimeout: 10 * time.Second,

		// Shorter than ReadTimeout, so a quiet but live connection answers a ping before it has
		// been silent for ReadTimeout. If PingInterval were longer, every quiet connection would
		// be dropped before it was ever pinged.
		PingInterval: 20 * time.Second,

		SendBuffer:      16,
		MaxMessageBytes: 32 * 1024,
	}
}

// Validate catches the configurations that cannot work.
//
// A constructor that accepts a PingInterval longer than the ReadTimeout produces a service where every
// connection drops after the read timeout and nobody knows why, because each setting looks reasonable on its
// own. The relationship between them is the thing to check.
func (c Config) Validate() error {
	if c.ReadTimeout <= 0 {
		return errors.New("websockets: ReadTimeout must be positive")
	}
	if c.WriteTimeout <= 0 {
		return errors.New("websockets: WriteTimeout must be positive")
	}
	if c.PingInterval <= 0 {
		return errors.New("websockets: PingInterval must be positive")
	}

	if c.PingInterval >= c.ReadTimeout {
		return fmt.Errorf("websockets: PingInterval (%v) must be shorter than ReadTimeout (%v), "+
			"or every idle connection is dropped before it is pinged",
			c.PingInterval, c.ReadTimeout)
	}

	if c.SendBuffer < 0 {
		return errors.New("websockets: SendBuffer cannot be negative")
	}

	return nil
}

// OverflowPolicy is what happens when a client's send buffer is full.
//
// A named type rather than a bool, because there are three sensible answers and a service has to pick one per
// use case. There is no default that is right for both a chat room and a price feed.
type OverflowPolicy int

const (
	// DropNewest discards the message being sent. Right for a price feed or a metrics stream, where
	// the newest message supersedes the ones behind it and the client will catch up.
	DropNewest OverflowPolicy = iota

	// DropOldest discards the queued message at the front. Right when recency matters more than
	// completeness, which is the same feed case from the other direction.
	DropOldest

	// Disconnect closes the connection. Right for a chat room or anything where a gap in the stream
	// makes the rest meaningless: better to make the client reconnect and resynchronise than to
	// serve it a stream with holes it cannot see.
	Disconnect
)

// String names the policy for a log line.
func (p OverflowPolicy) String() string {
	switch p {
	case DropNewest:
		return "drop-newest"
	case DropOldest:
		return "drop-oldest"
	case Disconnect:
		return "disconnect"
	default:
		return "unknown"
	}
}

// Conn is one client, with a single writer goroutine.
//
// # The single-writer pattern
//
// Everything that wants to send puts a message on a channel, and one goroutine is the only thing that ever calls
// Write. Not because coder/websocket needs it: its Write is safe to call concurrently (gorilla's is not, and
// panics). An earlier version of this comment said the library panics; it doesn't. The reason is BACKPRESSURE.
// A broadcaster calling Write directly blocks on the slowest client for up to WriteTimeout, once per message.
// With a bounded channel in between, a send never blocks the broadcaster, and a client that cannot keep up hits
// the overflow policy instead of stalling everyone else.
//
// Reads are different: coder/websocket allows only one concurrent reader, and there is usually only one anyway.
type Conn struct {
	ws  *websocket.Conn
	cfg Config
	log *slog.Logger

	send chan []byte

	// closeOnce guards Close, because both the reader and the writer will try to close when the
	// other fails, and closing a channel twice panics.
	closeOnce sync.Once
	closed    chan struct{}

	policy OverflowPolicy

	// Counters, so the tests can assert on behaviour and a service can export it.
	sent     atomic.Int64
	received atomic.Int64

	// lastAlive is when the peer last showed it was there: a data message, or an answered ping.
	// Unix nanoseconds, atomic because the reader and the writer both set it.
	lastAlive atomic.Int64
	dropped   atomic.Int64
	pingsSent atomic.Int64
}

// Accept upgrades an HTTP request to a WebSocket.
func Accept(w http.ResponseWriter, r *http.Request, cfg Config, policy OverflowPolicy, log *slog.Logger) (*Conn, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	if log == nil {
		log = slog.Default()
	}

	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// Empty means same-origin only. The library's default, stated explicitly because the
		// alternative (InsecureSkipVerify) is the option people reach for to make development
		// work and then ship.
		OriginPatterns: cfg.OriginPatterns,

		// Compression costs CPU per message and helps only on text that repeats. For small JSON
		// messages it usually makes things slower, so it is off and the decision is visible.
		CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		return nil, fmt.Errorf("upgrading: %w", err)
	}

	ws.SetReadLimit(cfg.MaxMessageBytes)

	c := &Conn{
		ws:     ws,
		cfg:    cfg,
		log:    log,
		send:   make(chan []byte, cfg.SendBuffer),
		closed: make(chan struct{}),
		policy: policy,
	}

	return c, nil
}

// Send queues a message for the writer goroutine.
//
// Never blocks, and that is the whole point: a broadcast loop calling Send on a hundred connections must not be
// held up by the slowest one. The overflow policy decides what happens instead, and the counter records it so a
// dashboard can show how often it happens.
func (c *Conn) Send(data []byte) error {
	select {
	case <-c.closed:
		return errors.New("websockets: connection closed")
	default:
	}

	select {
	case c.send <- data:
		return nil
	default:
	}

	// The buffer is full.
	c.dropped.Add(1)

	switch c.policy {
	case DropNewest:
		return nil

	case DropOldest:
		// Drain one and retry once. The drain and the send are not atomic, so another sender can
		// slip in between them, which is why this does not loop: retrying forever under
		// contention is how a "non-blocking" send blocks.
		select {
		case <-c.send:
		default:
		}

		select {
		case c.send <- data:
			return nil
		default:
			return nil
		}

	case Disconnect:
		c.Close(websocket.StatusPolicyViolation, "client too slow")
		return errors.New("websockets: client too slow, disconnected")

	default:
		return fmt.Errorf("websockets: unknown overflow policy %d", c.policy)
	}
}

// Run drives the connection until it closes.
//
// Two goroutines: one reads, one writes and pings. Run blocks until either ends, then closes the other, which is
// the pattern that avoids leaking a goroutine when only one direction fails.
//
// handle is called for each inbound message, on the READER goroutine, so a slow handler stops that connection
// reading. That is deliberate: the alternative (a goroutine per message) is unbounded concurrency per client
// and loses message ordering, which for most protocols is a correctness problem rather than a performance one.
func (c *Conn) Run(ctx context.Context, handle func(context.Context, []byte) error) error {
	c.lastAlive.Store(time.Now().UnixNano()) // the handshake just happened
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	errs := make(chan error, 2)

	go func() { errs <- c.readLoop(ctx, handle) }()
	go func() { errs <- c.writeLoop(ctx) }()

	// The first error wins and cancels the other loop.
	err := <-errs
	cancel()

	// Drain the second, so neither goroutine leaks. Without this the loser writes to a channel
	// nobody reads; the buffer of 2 means it would not block, and draining is still the clearer
	// contract.
	<-errs

	c.Close(websocket.StatusNormalClosure, "")

	// A normal close is not an error. websocket.CloseStatus returns -1 for anything that is not a
	// close frame, which is how a real failure is told from a client going away.
	if status := websocket.CloseStatus(err); status == websocket.StatusNormalClosure ||
		status == websocket.StatusGoingAway {
		return nil
	}

	if errors.Is(err, context.Canceled) {
		return nil
	}

	return err
}

func (c *Conn) readLoop(ctx context.Context, handle func(context.Context, []byte) error) error {
	for {
		// No deadline on the read itself. The first version put ReadTimeout on every Read, as the
		// Slowloris defence, and cut off every client that only listens: coder/websocket answers
		// pongs INSIDE Read and returns only for data messages, so a successful ping could never
		// reset that deadline. Liveness is the writer's job instead (see writeLoop): a peer that has
		// vanished, or never reads, stops answering pings and is dropped, while one that listens
		// and answers stays. TestAListeningClientStaysConnected and
		// TestAClientThatNeverReadsIsDropped hold both halves.
		typ, data, err := c.ws.Read(ctx)
		if err != nil {
			return fmt.Errorf("reading: %w", err)
		}

		c.lastAlive.Store(time.Now().UnixNano())
		c.received.Add(1)

		if typ != websocket.MessageText && typ != websocket.MessageBinary {
			continue
		}

		if err := handle(ctx, data); err != nil {
			return fmt.Errorf("handling a message: %w", err)
		}
	}
}

func (c *Conn) writeLoop(ctx context.Context) error {
	ticker := time.NewTicker(c.cfg.PingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case <-c.closed:
			return errors.New("websockets: closed")

		case data := <-c.send:
			writeCtx, cancel := context.WithTimeout(ctx, c.cfg.WriteTimeout)

			err := c.ws.Write(writeCtx, websocket.MessageText, data)

			cancel()

			if err != nil {
				return fmt.Errorf("writing: %w", err)
			}

			c.sent.Add(1)

		case <-ticker.C:
			// The backstop: however the pings are going, a peer silent for longer than
			// ReadTimeout (no message, no answered ping) is gone.
			if silent := time.Since(time.Unix(0, c.lastAlive.Load())); silent > c.cfg.ReadTimeout {
				return fmt.Errorf("no message or pong for %v, over the %v ReadTimeout",
					silent.Round(time.Millisecond), c.cfg.ReadTimeout)
			}

			// Ping with a deadline. coder/websocket's Ping BLOCKS until the pong arrives or
			// the context expires, which is exactly the semantics wanted: a peer that has
			// vanished fails here rather than being discovered minutes later by a write.
			pingCtx, cancel := context.WithTimeout(ctx, c.cfg.WriteTimeout)

			err := c.ws.Ping(pingCtx)

			cancel()

			if err != nil {
				return fmt.Errorf("ping: %w", err)
			}

			c.lastAlive.Store(time.Now().UnixNano())
			c.pingsSent.Add(1)
		}
	}
}

// Close closes the connection once.
//
// The nil check on ws is not defensive padding. Close is called from Send when the overflow policy is
// Disconnect, and Send is reachable on a Conn whose socket was never established: a failed upgrade, a test
// harness, or a connection built to queue messages before the client arrives. A nil dereference there panics
// inside whatever goroutine happened to be broadcasting, which is a process-wide crash for a case that should
// be a no-op. A test found it by building exactly that Conn.
func (c *Conn) Close(status websocket.StatusCode, reason string) {
	c.closeOnce.Do(func() {
		close(c.closed)

		if c.ws == nil {
			return
		}

		// The error is ignored deliberately: by the time Close runs the connection is usually
		// already gone, and a failure to send a close frame to a peer that has vanished is the
		// normal case rather than a problem.
		_ = c.ws.Close(status, reason)
	})
}

// Stats is what the connection counted.
type Stats struct {
	Sent, Received, Dropped, Pings int64
}

// Stats returns a snapshot.
func (c *Conn) Stats() Stats {
	return Stats{
		Sent:     c.sent.Load(),
		Received: c.received.Load(),
		Dropped:  c.dropped.Load(),
		Pings:    c.pingsSent.Load(),
	}
}

// Hub broadcasts to a set of connections.
//
// # Why a mutex and not a channel
//
// The canonical Go chat example uses a hub goroutine with register, unregister and broadcast channels. It works
// and it serialises everything through one goroutine, so a broadcast to 10,000 connections happens one at a
// time while registrations queue behind it.
//
// A RWMutex around a map lets broadcasts run concurrently with each other and only blocks for registration. The
// sends are non-blocking (see Conn.Send), so holding a read lock across a broadcast is bounded by the number of
// connections rather than by the slowest client.
type Hub struct {
	mu    sync.RWMutex
	conns map[*Conn]struct{}
}

// NewHub builds one.
func NewHub() *Hub {
	return &Hub{conns: make(map[*Conn]struct{})}
}

// Add registers a connection.
func (h *Hub) Add(c *Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.conns[c] = struct{}{}
}

// Remove deregisters one.
func (h *Hub) Remove(c *Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()

	delete(h.conns, c)
}

// Len reports how many connections are registered.
func (h *Hub) Len() int {
	h.mu.RLock()
	defer h.mu.RUnlock()

	return len(h.conns)
}

// Broadcast sends to every connection.
//
// Returns how many sends failed, rather than an error, because one client failing is not a reason for the
// broadcast to fail and a caller almost always wants the count for a metric.
func (h *Hub) Broadcast(data []byte) (failed int) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for c := range h.conns {
		if err := c.Send(data); err != nil {
			failed++
		}
	}

	return failed
}

// BroadcastJSON marshals once and sends the same bytes to everyone.
//
// Marshalling once rather than per connection is the difference between one allocation and N. With 10,000
// connections and a 200-byte message that is 2 MB of garbage per broadcast, which at ten broadcasts a second is
// a GC problem rather than a JSON problem.
func (h *Hub) BroadcastJSON(v any) (failed int, err error) {
	data, err := json.Marshal(v)
	if err != nil {
		return 0, fmt.Errorf("marshalling a broadcast: %w", err)
	}

	return h.Broadcast(data), nil
}
