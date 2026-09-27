package pgxdemo

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/alexvervloet/learn-go/backends/learning/database-concepts/dbtest"
)

// pool opens a pool with the given config against the test database, and closes it when the test ends.
//
// Its own pool rather than dbtest.Pool, because the whole subject here is pool CONFIGURATION and a
// shared pool cannot be reconfigured. dbtest.Pool is still called first so a missing database skips.
func pool(t *testing.T, cfg Config) *pgxpool.Pool {
	t.Helper()
	return openPool(t, cfg, true)
}

// leakyPool is the same thing without the deferred Close, and it exists because of a 156-second test
// run that taught me something.
//
// pgxpool.Pool.Close BLOCKS until every acquired connection is released. A test that deliberately leaks
// connections and then closes its pool in t.Cleanup therefore passes every assertion and hangs forever
// on the way out, which looks exactly like the test itself hanging.
//
// That is not a flaw in pgx. Close waiting is what lets a service drain in-flight queries during a
// graceful shutdown, and it is the same decision http.Server.Shutdown makes. The consequence is worth
// knowing: a leaked connection does not just exhaust the pool, it also blocks shutdown, so the process
// has to be killed rather than stopped.
func leakyPool(t *testing.T, cfg Config) *pgxpool.Pool {
	t.Helper()
	return openPool(t, cfg, false)
}

func openPool(t *testing.T, cfg Config, closeAtEnd bool) *pgxpool.Pool {
	t.Helper()

	// Skips if there is no database, and gives the URL this package's own database so it does not
	// fight the others.
	shared := dbtest.Pool(t)

	p, err := Open(context.Background(), shared.Config().ConnString(), cfg)
	if err != nil {
		t.Fatalf("opening a pool: %v", err)
	}

	if closeAtEnd {
		t.Cleanup(p.Close)
	}

	return p
}

func TestPoolReportsItsState(t *testing.T) {
	p := pool(t, Small())
	ctx := context.Background()

	before := Snapshot(p)
	t.Logf("fresh: %s", before)

	if before.Max != 2 {
		t.Errorf("MaxConns is %d, want 2", before.Max)
	}

	conn, err := p.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}

	during := Snapshot(p)
	t.Logf("one acquired: %s (saturation %.0f%%)", during, during.Saturation()*100)

	if during.Acquired != 1 {
		t.Errorf("Acquired is %d, want 1", during.Acquired)
	}
	if during.Saturation() != 0.5 {
		t.Errorf("saturation is %.2f, want 0.50", during.Saturation())
	}

	conn.Release()

	after := Snapshot(p)
	t.Logf("released: %s", after)

	if after.Acquired != 0 {
		t.Errorf("Acquired is %d after Release, want 0", after.Acquired)
	}
}

// TestPoolExhaustionLooksLikeASlowDatabase is the point of the package.
func TestPoolExhaustionLooksLikeASlowDatabase(t *testing.T) {
	p := pool(t, Small())
	ctx := context.Background()

	// Take every connection.
	var held []*pgxpool.Conn
	for range 2 {
		conn, err := p.Acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}
		held = append(held, conn)
	}

	t.Logf("pool full: %s", Snapshot(p))

	// A third caller waits, then fails with the context's error. Note what it is NOT: there is
	// nothing in it about the pool.
	start := time.Now()

	waitCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()

	_, err := p.Acquire(waitCtx)
	waited := time.Since(start)

	if err == nil {
		t.Fatal("acquired a third connection from a pool of two")
	}

	t.Logf("raw Acquire after %v: %v", waited.Round(time.Millisecond), err)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected context.DeadlineExceeded, got %v", err)
	}

	// AcquireWithTimeout says which bottleneck it is, which is the whole reason it exists.
	_, err = AcquireWithTimeout(ctx, p, 50*time.Millisecond)

	if !errors.Is(err, ErrPoolExhausted) {
		t.Errorf("expected ErrPoolExhausted, got %v", err)
	}

	t.Logf("AcquireWithTimeout: %v", err)

	for _, conn := range held {
		conn.Release()
	}

	// And a query on a freed pool works again, so nothing was broken.
	if err := CorrectQuery(ctx, p); err != nil {
		t.Errorf("the pool did not recover: %v", err)
	}
}

// TestALeakedConnectionKillsThePool shows how few requests it takes.
func TestALeakedConnectionKillsThePool(t *testing.T) {
	cfg := Small()
	cfg.MaxConns = 3

	// leakyPool, not pool: see the comment on leakyPool. Closing this one would never return.
	p := leakyPool(t, cfg)
	ctx := context.Background()

	// Each call succeeds. That is the problem.
	for i := 1; i <= int(cfg.MaxConns); i++ {
		if err := LeakyQuery(ctx, p); err != nil {
			t.Fatalf("leak %d returned an error, which it should not: %v", i, err)
		}
		t.Logf("after %d leaked request(s): %s", i, Snapshot(p))
	}

	// The next one hangs until its context expires.
	next, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()

	err := LeakyQuery(next, p)

	if err == nil {
		t.Fatal("the pool served a fourth request after three leaks")
	}

	t.Logf("request %d: %v", cfg.MaxConns+1, err)
	t.Logf("%d requests, no database error, no error log, service down", cfg.MaxConns+1)

	// A released pool cannot be recovered by the application, which is what makes this a restart.
	// The connections are held by garbage that is still reachable from the pool.
	stat := Snapshot(p)
	if stat.Acquired != cfg.MaxConns {
		t.Errorf("expected all %d connections held, got %d", cfg.MaxConns, stat.Acquired)
	}
}

// TestConcurrencyIsBoundedByThePool is the number people get wrong: launching 100 goroutines against a
// pool of 5 gives concurrency 5, not 100.
func TestConcurrencyIsBoundedByThePool(t *testing.T) {
	cfg := Small()
	cfg.MaxConns = 5

	p := pool(t, cfg)
	ctx := context.Background()

	const goroutines = 50
	const queryTime = 20 * time.Millisecond

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		maxSeen int32
	)

	start := time.Now()

	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()

			conn, err := p.Acquire(ctx)
			if err != nil {
				t.Errorf("acquiring: %v", err)
				return
			}
			defer conn.Release()

			mu.Lock()
			if acquired := Snapshot(p).Acquired; acquired > maxSeen {
				maxSeen = acquired
			}
			mu.Unlock()

			// pg_sleep, so the wait happens on the server and the connection is genuinely
			// busy rather than just held.
			var ok bool
			if err := conn.QueryRow(ctx, "SELECT pg_sleep($1) IS NULL",
				queryTime.Seconds()).Scan(&ok); err != nil {
				t.Errorf("sleeping: %v", err)
			}
		}()
	}

	wg.Wait()
	elapsed := time.Since(start)

	// 50 queries of 20ms through 5 connections is 10 batches, so about 200ms. Through 50
	// connections it would be about 20ms.
	expected := time.Duration(goroutines/int(cfg.MaxConns)) * queryTime

	t.Logf("%d goroutines, pool of %d, %v each: took %v (about %v expected)",
		goroutines, cfg.MaxConns, queryTime, elapsed.Round(time.Millisecond), expected)
	t.Logf("the most connections in use at once was %d", maxSeen)
	t.Logf("pool stats: %s", Snapshot(p))

	if maxSeen > cfg.MaxConns {
		t.Errorf("saw %d connections in use, above MaxConns of %d", maxSeen, cfg.MaxConns)
	}

	// The lower bound is the real assertion: it CANNOT have gone faster than the pool allows.
	if elapsed < expected/2 {
		t.Errorf("took %v, which is faster than %d connections can serve %d queries of %v",
			elapsed, cfg.MaxConns, goroutines, queryTime)
	}

	// And the pool counted the waits, which is the metric to alert on.
	if waits := Snapshot(p).EmptyAcquireCount; waits == 0 {
		t.Error("EmptyAcquireCount is 0, so nothing ever waited and the test proved nothing")
	} else {
		t.Logf("EmptyAcquireCount is %d: that is the leading indicator, and it rises long "+
			"before anything times out", waits)
	}
}

// TestQueryModesAllWork, and the point is that they are interchangeable for correctness and not for
// cost.
func TestQueryModesAllWork(t *testing.T) {
	p := pool(t, Sensible())
	ctx := context.Background()

	for _, m := range Modes() {
		t.Run(m.Name, func(t *testing.T) {
			// The mode is per-query, passed as the first argument. It can also be set
			// per-connection in the pool config, which is what you do in front of
			// PgBouncer.
			var n int
			if err := p.QueryRow(ctx, "SELECT count(*) FROM books WHERE author_id = $1",
				m.Mode, int64(1)).Scan(&n); err != nil {
				t.Fatalf("%s: %v", m.Name, err)
			}

			t.Logf("%s: %d books", m.Name, n)
		})
	}
}

// TestSimpleProtocolQuotesItsArguments, because "it interpolates the arguments" sounds like an injection
// hole and is not one.
func TestSimpleProtocolQuotesItsArguments(t *testing.T) {
	p := pool(t, Sensible())
	ctx := context.Background()

	nasty := "'; DROP TABLE books; --"

	var got string
	if err := p.QueryRow(ctx, "SELECT $1::text", pgx.QueryExecModeSimpleProtocol, nasty).
		Scan(&got); err != nil {
		t.Fatal(err)
	}

	if got != nasty {
		t.Errorf("got %q, want %q", got, nasty)
	}

	// And books is still there.
	var n int
	if err := p.QueryRow(ctx, "SELECT count(*) FROM books").Scan(&n); err != nil {
		t.Fatalf("books is gone: %v", err)
	}

	t.Logf("the simple protocol sent %q as a quoted literal and books still has %d rows", nasty, n)
	t.Log("it is safe because pgx quotes, not because the database parses parameters; the " +
		"same string built with fmt.Sprintf into the SQL is the injection")
}

// TestMinConnsPrewarms measures what MinConns buys, which is the first request after a quiet period.
func TestMinConnsPrewarms(t *testing.T) {
	ctx := context.Background()

	cold := Config{MaxConns: 5, MinConns: 0, MaxConnLifetime: time.Minute,
		MaxConnIdleTime: 30 * time.Second, HealthCheckPeriod: 10 * time.Second}

	warm := cold
	warm.MinConns = 5

	measure := func(cfg Config) time.Duration {
		p := pool(t, cfg)

		// Give the health checker a moment to open MinConns.
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if Snapshot(p).Total >= cfg.MinConns {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}

		t.Logf("MinConns=%d: %s", cfg.MinConns, Snapshot(p))

		// Time five acquires in a row, which on a cold pool means five handshakes.
		start := time.Now()

		var conns []*pgxpool.Conn
		for range 5 {
			c, err := p.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			conns = append(conns, c)
		}

		elapsed := time.Since(start)

		for _, c := range conns {
			c.Release()
		}

		return elapsed
	}

	coldTime := measure(cold)
	warmTime := measure(warm)

	t.Logf("five acquires from a cold pool: %v", coldTime.Round(time.Microsecond))
	t.Logf("five acquires from a warm pool: %v", warmTime.Round(time.Microsecond))

	// I expected this to be too small to see over a unix socket and it is not. Over four runs: 8.5,
	// 9.5, 10.3 and 16.3ms cold against 1.3 to 2.0ms warm, so five to ten times, with no TLS and no
	// network. Ping in Open opens one connection, so the cold pool pays four handshakes and the warm
	// pool pays none.
	//
	// The assertion is the direction rather than the factor, because the factor depends on the
	// machine. On a managed database over TLS the four handshakes are tens of milliseconds, which
	// is a visible p99 spike on the first request after a quiet period, blamed on the database and
	// caused by MinConns being 0.
	if warmTime > coldTime {
		t.Errorf("the pre-warmed pool was slower: %v vs %v", warmTime, coldTime)
	}

	t.Logf("pre-warming saved %v on four handshakes over a unix socket; over TLS to a managed "+
		"database it is the difference between a 40ms first request and a 1ms one",
		(coldTime - warmTime).Round(time.Microsecond))
}

// TestClosingAPoolWithLeakedConnectionsBlocks pins down what cost 156 seconds of a test run.
//
// Close waits for every acquired connection. So the leak that exhausts the pool also prevents the
// process from shutting down cleanly, and a graceful shutdown with a leaked connection becomes a
// SIGKILL.
func TestClosingAPoolWithLeakedConnectionsBlocks(t *testing.T) {
	cfg := Small()
	cfg.MaxConns = 1

	p := leakyPool(t, cfg)
	ctx := context.Background()

	if err := LeakyQuery(ctx, p); err != nil {
		t.Fatal(err)
	}

	closed := make(chan struct{})

	go func() {
		p.Close()
		close(closed)
	}()

	select {
	case <-closed:
		t.Error("Close returned with a connection still acquired; pgx changed its behaviour")
	case <-time.After(200 * time.Millisecond):
		t.Log("Close is still blocked after 200ms, waiting for the leaked connection")
	}

	// There is no handle to release, which is the point: the connection is reachable only from the
	// pool. The goroutine stays blocked for the rest of the process, which is harmless in a test
	// binary and is a hung shutdown in a service.
	t.Log("nothing in the application can release it, so this pool never closes")
}

// TestCorrectQueryReleases is the control for the test above.
func TestCorrectQueryReleases(t *testing.T) {
	cfg := Small()
	cfg.MaxConns = 1

	p := pool(t, cfg)
	ctx := context.Background()

	for range 10 {
		if err := CorrectQuery(ctx, p); err != nil {
			t.Fatal(err)
		}
	}

	stat := Snapshot(p)
	t.Logf("after 10 requests through a pool of 1: %s", stat)

	if stat.Acquired != 0 {
		t.Errorf("%d connections still acquired", stat.Acquired)
	}
}
