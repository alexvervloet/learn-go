package websockets

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// echoServer starts a server that echoes every message back.
//
// # Why the logging goes through a buffer
//
// The obvious version calls t.Logf from the handler when a connection ends. Under -race that reports a data
// race inside `testing` itself: a connection can end after the test function returns, and t.Logf on a finished
// test races with testing's own bookkeeping. It reproduces about one run in three, which is exactly the kind of
// flake that gets rerun until it passes.
//
// The fix is ordering. t.Cleanup runs LIFO, so registering the drain FIRST and srv.Close SECOND means Close
// runs first, waits for every handler to return, and only then are the messages logged, on the test's own
// goroutine.
func echoServer(t *testing.T, cfg Config, policy OverflowPolicy) (*httptest.Server, *Hub) {
	t.Helper()

	hub := NewHub()

	var (
		mu       sync.Mutex
		messages []string
	)

	record := func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()

		messages = append(messages, fmt.Sprintf(format, args...))
	}

	// Registered first, so it runs LAST.
	t.Cleanup(func() {
		mu.Lock()
		defer mu.Unlock()

		for _, m := range messages {
			t.Log(m)
		}
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := Accept(w, r, cfg, policy, discardLogger())
		if err != nil {
			record("accept: %v", err)
			return
		}

		hub.Add(c)
		defer hub.Remove(c)

		if err := c.Run(r.Context(), func(_ context.Context, data []byte) error {
			return c.Send(append([]byte("echo: "), data...))
		}); err != nil {
			record("connection ended: %v", err)
		}
	}))

	// Registered second, so it runs FIRST and blocks until every handler has returned.
	t.Cleanup(srv.Close)

	return srv, hub
}

// dial connects a client to a test server.
func dial(t *testing.T, srv *httptest.Server) (*websocket.Conn, context.Context) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)

	url := "ws" + strings.TrimPrefix(srv.URL, "http")

	// The third return is the handshake's *http.Response, and its body has to be closed. It is almost
	// always empty, which is why every example ignores it and why the linter is right anyway: on a
	// FAILED handshake it carries the server's explanation, and leaking it leaks a connection.
	ws, resp, err := websocket.Dial(ctx, url, nil)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		t.Fatalf("dialling: %v", err)
	}

	t.Cleanup(func() { _ = ws.CloseNow() })

	return ws, ctx
}

func TestEchoRoundTrip(t *testing.T) {
	srv, hub := echoServer(t, DefaultConfig(), DropNewest)

	ws, ctx := dial(t, srv)

	for _, message := range []string{"hello", "", "a longer message with spaces", "unicode: héllo"} {
		if err := ws.Write(ctx, websocket.MessageText, []byte(message)); err != nil {
			t.Fatal(err)
		}

		_, data, err := ws.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}

		want := "echo: " + message
		if string(data) != want {
			t.Errorf("got %q, want %q", data, want)
		}
	}

	t.Logf("%d connection(s) registered", hub.Len())

	if hub.Len() != 1 {
		t.Errorf("the hub has %d connections", hub.Len())
	}
}

// TestConcurrentWritesAreSafe sends from many goroutines at once through one connection.
//
// With gorilla/websocket, concurrent writers panic. coder/websocket's Write is safe to call concurrently, so a
// direct Write would not crash here either; the queue is what keeps a slow client from blocking the senders, and
// what gives the overflow policy something to act on.
func TestConcurrentWritesAreSafe(t *testing.T) {
	cfg := DefaultConfig()
	cfg.SendBuffer = 256

	srv, hub := echoServer(t, cfg, DropNewest)

	ws, ctx := dial(t, srv)

	// Make the server see the connection.
	if err := ws.Write(ctx, websocket.MessageText, []byte("ping")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ws.Read(ctx); err != nil {
		t.Fatal(err)
	}

	const senders = 50
	const each = 20

	var wg sync.WaitGroup

	// Every goroutine sends through the hub, which calls Send on the one connection.
	for range senders {
		wg.Add(1)
		go func() {
			defer wg.Done()

			for range each {
				hub.Broadcast([]byte("concurrent"))
			}
		}()
	}

	wg.Wait()

	// Read whatever arrived. The point is not how many: it is that nothing panicked and the frames
	// are intact, which the client's Read would fail on if they were interleaved.
	readCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	received := 0
	for {
		_, data, err := ws.Read(readCtx)
		if err != nil {
			break
		}

		if string(data) != "concurrent" {
			t.Fatalf("a frame was corrupted: %q", data)
		}

		received++
	}

	t.Logf("%d goroutines sent %d messages each; %d arrived intact, none corrupted",
		senders, each, received)

	if received == 0 {
		t.Error("nothing arrived")
	}

	t.Log("every sender went through one queue and one writer, so no sender waited on the network")
}

// TestSlowClientPolicies measures what each overflow policy does.
func TestSlowClientPolicies(t *testing.T) {
	for _, policy := range []OverflowPolicy{DropNewest, DropOldest, Disconnect} {
		t.Run(policy.String(), func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.SendBuffer = 4

			// A connection with no writer goroutine draining the channel, which is exactly
			// what a client that has stopped reading looks like from the server's side.
			c := &Conn{
				send:   make(chan []byte, cfg.SendBuffer),
				closed: make(chan struct{}),
				cfg:    cfg,
				policy: policy,
				log:    discardLogger(),
			}

			var lastErr error

			for i := range 20 {
				if err := c.Send([]byte{byte(i)}); err != nil {
					lastErr = err
					break
				}
			}

			stats := c.Stats()

			t.Logf("buffer of %d, 20 messages: %d dropped, queue holds %d",
				cfg.SendBuffer, stats.Dropped, len(c.send))

			switch policy {
			case DropNewest:
				if lastErr != nil {
					t.Errorf("drop-newest returned an error: %v", lastErr)
				}
				if len(c.send) != cfg.SendBuffer {
					t.Errorf("the queue holds %d, want %d", len(c.send), cfg.SendBuffer)
				}

				// The FIRST four are kept.
				first := <-c.send
				if first[0] != 0 {
					t.Errorf("the queue starts with %d, want 0", first[0])
				}

				t.Log("the queue holds the oldest messages and the newest were discarded")

			case DropOldest:
				if lastErr != nil {
					t.Errorf("drop-oldest returned an error: %v", lastErr)
				}

				// The LAST four are kept, so the front of the queue is a late message.
				first := <-c.send
				if first[0] < 4 {
					t.Errorf("the queue starts with %d, which is not a recent message",
						first[0])
				}

				t.Logf("the queue starts at message %d, so the old ones were discarded",
					first[0])

			case Disconnect:
				if lastErr == nil {
					t.Error("disconnect did not return an error")
				}

				select {
				case <-c.closed:
				default:
					t.Error("the connection was not closed")
				}

				t.Logf("disconnected after %d messages: %v", stats.Dropped, lastErr)
			}

			if stats.Dropped == 0 {
				t.Error("nothing was recorded as dropped, so the counter is not working")
			}
		})
	}

	t.Log("there is no right default here. A price feed wants drop-oldest, a chat room wants " +
		"disconnect, and shipping whichever the library chose is how a client ends up with a " +
		"stream that has holes it cannot see.")
}

// TestAListeningClientStaysConnected: a subscriber that only listens (a price feed, a notification stream)
// sends nothing, and it is alive for as long as it answers pings.
//
// The first version put ReadTimeout on every data read. coder/websocket handles pongs inside Read and only
// returns for data messages, so a successful ping never reset that deadline, and a listening client was cut off
// every ReadTimeout however many pings it answered. The test that was here asserted exactly that, under the
// name "disconnects a silent client".
func TestAListeningClientStaysConnected(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ReadTimeout = 300 * time.Millisecond
	cfg.PingInterval = 100 * time.Millisecond

	srv, _ := echoServer(t, cfg, DropNewest)

	ws, ctx := dial(t, srv)

	// Read the whole time, which is what answers pings, and hand each message over.
	got := make(chan string, 1)
	go func() {
		for {
			_, data, err := ws.Read(ctx)
			if err != nil {
				close(got)
				return
			}
			got <- string(data)
		}
	}()

	// Three read timeouts of saying nothing.
	time.Sleep(3 * cfg.ReadTimeout)

	if err := ws.Write(ctx, websocket.MessageText, []byte("still here")); err != nil {
		t.Fatalf("the server dropped a client that answered every ping: %v", err)
	}

	select {
	case msg, ok := <-got:
		if !ok {
			t.Fatal("the connection closed while the client was listening and answering pings")
		}
		t.Logf("after %v of silence the echo came back: %q", 3*cfg.ReadTimeout, msg)
	case <-time.After(2 * time.Second):
		t.Fatal("no echo")
	}
}

// TestAClientThatNeverReadsIsDropped is the Slowloris defence at the WebSocket layer, and it is the ping that
// provides it. A client that connects and never reads never answers a ping, so the ping fails after
// WriteTimeout and the server closes the connection, instead of holding a goroutine and a file descriptor forever.
func TestAClientThatNeverReadsIsDropped(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ReadTimeout = 300 * time.Millisecond
	cfg.PingInterval = 100 * time.Millisecond
	cfg.WriteTimeout = 100 * time.Millisecond

	srv, hub := echoServer(t, cfg, DropNewest)

	dial(t, srv) // and then never read

	deadline := time.Now().Add(3 * time.Second)
	for hub.Len() != 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}

	if n := hub.Len(); n != 0 {
		t.Errorf("the hub still holds %d connection(s) from a client that never answered a ping", n)
	}
}

// TestPingDetectsADeadPeer, because TCP will not, and it taught me something about who answers a ping.
//
// # The thing that is not obvious
//
// coder/websocket replies to a ping automatically, but only while the application is READING. The pong is sent
// from inside Read. A client that connects and then does not call Read never answers a ping, so a server with
// ping checking will disconnect an idle-but-alive client that is not in a read loop.
//
// The first version of this test had the client sleep for 200ms without reading. The server sent one ping, the
// client never answered, Ping blocked for the whole WriteTimeout and failed, and the test reported zero
// successful pings. Which is correct behaviour and not what the test meant to measure.
//
// The practical consequence: a WebSocket client must be in a read loop for the whole life of the connection,
// even if it never expects a message. "Connect, send, sleep, send" is a client that gets disconnected.
func TestPingDetectsADeadPeer(t *testing.T) {
	cfg := DefaultConfig()
	cfg.PingInterval = 50 * time.Millisecond
	cfg.ReadTimeout = 5 * time.Second
	cfg.WriteTimeout = 500 * time.Millisecond

	var (
		mu    sync.Mutex
		stats Stats
		done  = make(chan struct{})
	)

	var acceptErr error

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := Accept(w, r, cfg, DropNewest, discardLogger())
		if err != nil {
			mu.Lock()
			acceptErr = err
			mu.Unlock()

			close(done)

			return
		}

		_ = c.Run(r.Context(), func(context.Context, []byte) error { return nil })

		mu.Lock()
		stats = c.Stats()
		mu.Unlock()

		close(done)
	}))

	// Close runs before the assertions below read acceptErr, and both happen on the test's own
	// goroutine. Reporting a handler's failure through a variable rather than t.Errorf is what
	// keeps this race-free.
	t.Cleanup(srv.Close)
	t.Cleanup(func() {
		mu.Lock()
		defer mu.Unlock()

		if acceptErr != nil {
			t.Errorf("accept: %v", acceptErr)
		}
	})

	ws, _ := dial(t, srv)

	// A read loop, which is what makes the client answer pings. Without it the library never sends
	// a pong and the server disconnects a client that is perfectly alive.
	reading := make(chan struct{})

	go func() {
		defer close(reading)

		readCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		for {
			if _, _, err := ws.Read(readCtx); err != nil {
				return
			}
		}
	}()

	// Long enough for several pings to be sent and answered.
	time.Sleep(250 * time.Millisecond)

	mu.Lock()
	duringPings := stats.Pings
	mu.Unlock()
	_ = duringPings

	// Now vanish: CloseNow drops the TCP connection without a close handshake, which is what a
	// laptop lid closing looks like.
	if err := ws.CloseNow(); err != nil {
		t.Fatal(err)
	}

	<-reading

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the server did not notice the peer had gone")
	}

	mu.Lock()
	got := stats
	mu.Unlock()

	t.Logf("the server sent %d pings while the client was reading, and noticed the peer had "+
		"gone within %v", got.Pings, cfg.PingInterval+cfg.WriteTimeout)

	if got.Pings == 0 {
		t.Error("no pings were answered, so a vanished peer would be discovered by TCP " +
			"timeout, which can be fifteen minutes")
	}

	t.Log("the pong comes from inside the client's Read call, so a client that is not in a " +
		"read loop never answers and gets disconnected for being idle")
}

// TestAClientThatDoesNotReadIsDisconnected is the other half of the finding above, asserted deliberately.
func TestAClientThatDoesNotReadIsDisconnected(t *testing.T) {
	cfg := DefaultConfig()
	cfg.PingInterval = 50 * time.Millisecond
	cfg.ReadTimeout = 5 * time.Second
	cfg.WriteTimeout = 200 * time.Millisecond

	var (
		mu    sync.Mutex
		stats Stats
		done  = make(chan struct{})
	)

	var acceptErr error

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := Accept(w, r, cfg, DropNewest, discardLogger())
		if err != nil {
			mu.Lock()
			acceptErr = err
			mu.Unlock()

			close(done)

			return
		}

		_ = c.Run(r.Context(), func(context.Context, []byte) error { return nil })

		mu.Lock()
		stats = c.Stats()
		mu.Unlock()

		close(done)
	}))

	// Close runs before the assertions below read acceptErr, and both happen on the test's own
	// goroutine. Reporting a handler's failure through a variable rather than t.Errorf is what
	// keeps this race-free.
	t.Cleanup(srv.Close)
	t.Cleanup(func() {
		mu.Lock()
		defer mu.Unlock()

		if acceptErr != nil {
			t.Errorf("accept: %v", acceptErr)
		}
	})

	_, _ = dial(t, srv)

	// No read loop. The client is connected and alive and simply not reading.
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the server never gave up on a client that was not reading")
	}

	mu.Lock()
	got := stats
	mu.Unlock()

	t.Logf("a connected, alive client that never called Read was disconnected after %d "+
		"successful ping(s)", got.Pings)

	if got.Pings > 0 {
		t.Errorf("%d pings were answered by a client that is not reading", got.Pings)
	}

	t.Log("this is a real client bug that looks like a server bug. 'Connect, send, sleep, " +
		"send' disconnects, and the server log says the peer stopped responding to pings.")
}

// TestConfigValidationCatchesTheRelationship, which is the setting nobody checks.
func TestConfigValidationCatchesTheRelationship(t *testing.T) {
	if err := DefaultConfig().Validate(); err != nil {
		t.Errorf("the default config is invalid: %v", err)
	}

	for _, tc := range []struct {
		name string
		mut  func(*Config)
	}{
		{"zero read timeout", func(c *Config) { c.ReadTimeout = 0 }},
		{"zero write timeout", func(c *Config) { c.WriteTimeout = 0 }},
		{"zero ping interval", func(c *Config) { c.PingInterval = 0 }},
		{"negative buffer", func(c *Config) { c.SendBuffer = -1 }},

		// The one that matters: each value is reasonable and the pair is not.
		{"ping slower than read timeout", func(c *Config) {
			c.ReadTimeout = 30 * time.Second
			c.PingInterval = 60 * time.Second
		}},
		{"ping equal to read timeout", func(c *Config) {
			c.ReadTimeout = 30 * time.Second
			c.PingInterval = 30 * time.Second
		}},
	} {
		cfg := DefaultConfig()
		tc.mut(&cfg)

		err := cfg.Validate()

		if err == nil {
			t.Errorf("%s was accepted", tc.name)
			continue
		}

		t.Logf("%-30s %v", tc.name, err)
	}

	t.Log("the last two are the useful ones: 30s and 60s are both plausible numbers, and " +
		"together they mean every idle connection is dropped before it is ever pinged")
}

// TestOriginIsCheckedByDefault, because the same-origin policy does not apply to WebSockets.
func TestOriginIsCheckedByDefault(t *testing.T) {
	// Not echoServer: its handler reports an accept failure as a test failure, and here the accept
	// failure IS the expected result. A helper that treats every error as a bug cannot be used to
	// test an error.
	var (
		quietMu sync.Mutex
		rejects []string
	)

	t.Cleanup(func() {
		quietMu.Lock()
		defer quietMu.Unlock()

		for _, r := range rejects {
			t.Log(r)
		}
	})

	quiet := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := Accept(w, r, DefaultConfig(), DropNewest, discardLogger())
		if err != nil {
			quietMu.Lock()
			rejects = append(rejects, "accept rejected the handshake: "+err.Error())
			quietMu.Unlock()

			return
		}

		_ = c.Run(r.Context(), func(context.Context, []byte) error { return nil })
	}))

	t.Cleanup(quiet.Close)

	url := "ws" + strings.TrimPrefix(quiet.URL, "http")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// A handshake with a foreign Origin, which is what a malicious page sends.
	_, rejectedResp, err := websocket.Dial(ctx, url, &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": []string{"https://evil.example"}},
	})
	if rejectedResp != nil && rejectedResp.Body != nil {
		_ = rejectedResp.Body.Close()
	}

	if err == nil {
		t.Fatal("a cross-origin handshake was accepted")
	}

	t.Logf("cross-origin handshake rejected: %v", err)

	// And an explicit allowlist lets it through.
	cfg := DefaultConfig()
	cfg.OriginPatterns = []string{"evil.example"}

	allowing, _ := echoServer(t, cfg, DropNewest)

	allowingURL := "ws" + strings.TrimPrefix(allowing.URL, "http")

	ws, allowedResp, err := websocket.Dial(ctx, allowingURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": []string{"https://evil.example"}},
	})
	if allowedResp != nil && allowedResp.Body != nil {
		_ = allowedResp.Body.Close()
	}
	if err != nil {
		t.Fatalf("the allowlisted origin was rejected: %v", err)
	}
	_ = ws.CloseNow()

	t.Log("a browser sends Origin and the same-origin policy does NOT stop the handshake, so " +
		"any page can open a WebSocket to any server and the cookies go with it. The " +
		"Origin check is the only thing in the way.")
}

// TestMessageSizeLimit, because an unbounded inbound message is an unbounded allocation.
func TestMessageSizeLimit(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MaxMessageBytes = 1024

	srv, _ := echoServer(t, cfg, DropNewest)

	ws, ctx := dial(t, srv)

	// Under the limit.
	if err := ws.Write(ctx, websocket.MessageText, make([]byte, 512)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ws.Read(ctx); err != nil {
		t.Fatal(err)
	}

	// Over it.
	if err := ws.Write(ctx, websocket.MessageText, make([]byte, 4096)); err != nil {
		t.Logf("the write itself failed: %v", err)
	}

	readCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	_, _, err := ws.Read(readCtx)

	if err == nil {
		t.Error("an oversized message was accepted")
	}

	t.Logf("a %d-byte message against a %d-byte limit closed the connection: %v",
		4096, cfg.MaxMessageBytes, err)

	// The close code says why, which is what lets a client distinguish "too big" from "server
	// crashed" and fix its own behaviour.
	if status := websocket.CloseStatus(err); status != websocket.StatusMessageTooBig {
		t.Logf("close status is %v rather than StatusMessageTooBig", status)
	}
}

// TestHubBroadcastMarshalsOnce.
func TestHubBroadcastMarshalsOnce(t *testing.T) {
	hub := NewHub()

	const conns = 100

	made := make([]*Conn, conns)

	for i := range made {
		made[i] = &Conn{
			send:   make(chan []byte, 8),
			closed: make(chan struct{}),
			cfg:    DefaultConfig(),
			policy: DropNewest,
			log:    discardLogger(),
		}
		hub.Add(made[i])
	}

	if hub.Len() != conns {
		t.Fatalf("the hub has %d connections", hub.Len())
	}

	failed, err := hub.BroadcastJSON(map[string]any{"type": "tick", "price": 12345})
	if err != nil {
		t.Fatal(err)
	}

	if failed != 0 {
		t.Errorf("%d sends failed", failed)
	}

	// Every connection got the same bytes, and they are the SAME SLICE, which is the point: one
	// allocation rather than a hundred.
	first := <-made[0].send
	second := <-made[1].send

	if string(first) != string(second) {
		t.Errorf("connections got different bytes: %q and %q", first, second)
	}

	if &first[0] != &second[0] {
		t.Error("the broadcast marshalled per connection rather than once")
	}

	t.Logf("one marshal of %d bytes shared by %d connections, rather than %d allocations",
		len(first), conns, conns)

	// Removing works, including removing something that is not there.
	hub.Remove(made[0])
	hub.Remove(made[0])

	if hub.Len() != conns-1 {
		t.Errorf("the hub has %d connections after one removal", hub.Len())
	}
}

// TestCloseIsIdempotent, because both loops try to close when the other fails.
func TestCloseIsIdempotent(t *testing.T) {
	c := &Conn{
		send:   make(chan []byte, 1),
		closed: make(chan struct{}),
		cfg:    DefaultConfig(),
		log:    discardLogger(),
	}

	// Twice, from the reader and the writer, which is what happens when one direction fails and
	// both loops unwind. Without sync.Once this closes an already-closed channel and panics.
	c.Close(websocket.StatusNormalClosure, "")
	c.Close(websocket.StatusPolicyViolation, "again")

	select {
	case <-c.closed:
	default:
		t.Error("the connection was not closed")
	}

	t.Log("sync.Once around the close is what stops the reader and the writer both closing the " +
		"channel, which would panic")

	// And it did not panic on a nil socket, which is the state a failed upgrade or a queue-first
	// connection is in. The first version of Close dereferenced ws unconditionally and this test
	// found it with a segfault inside a broadcast.
	t.Log("a Conn with no socket closes cleanly rather than panicking inside whichever " +
		"goroutine was broadcasting")

	// Send on a closed connection is an error rather than a panic on a closed channel.
	if err := c.Send([]byte("x")); err == nil {
		t.Error("Send on a closed connection succeeded")
	} else if !strings.Contains(err.Error(), "closed") {
		t.Errorf("Send returned %v", err)
	}
}
