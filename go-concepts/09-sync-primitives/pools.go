package main

import (
	"bytes"
	"fmt"
	"runtime"
	"sync"
)

// sync.Pool
// =========
//
// A cache of allocated-but-unused objects, to reduce garbage collector
// pressure. It is NOT a resource pool.
//
// The critical property: items may be REMOVED AT ANY TIME, without notice.
// The garbage collector clears pools on every cycle. So a Pool can never hold
// anything whose loss matters: not database connections, not open files, not
// anything needing Close.
//
// What it is for: short-lived objects allocated in a hot path, where the
// allocation itself is the cost. Byte buffers in an encoder, scratch slices in
// a parser. encoding/json and net/http both use one internally.

// bufferPool reuses bytes.Buffers. New is called when the pool is empty, so
// Get never returns nil.
var bufferPool = sync.Pool{
	New: func() any {
		// Returning a pointer, not a value. Putting a non-pointer into a Pool
		// allocates when it is boxed into the `any` interface, which defeats
		// the purpose. staticcheck's SA6002 flags it.
		return new(bytes.Buffer)
	},
}

// renderWithPool formats a report using a pooled buffer. The three-step shape
// is the whole pattern:
//
//	buf := pool.Get().(*bytes.Buffer)
//	defer func() { buf.Reset(); pool.Put(buf) }()
//	...use buf...
//
// Reset before Put, always. A buffer returned with content in it will hand
// that content to the next caller, which is a data leak between requests and a
// genuinely nasty bug to find.
func renderWithPool(rows []string) string {
	buf := bufferPool.Get().(*bytes.Buffer)
	defer func() {
		buf.Reset() // MUST happen before Put
		bufferPool.Put(buf)
	}()

	for i, r := range rows {
		fmt.Fprintf(buf, "%d:%s;", i, r)
	}

	// The string must be COPIED out. Returning buf.Bytes() would hand the
	// caller a slice into memory the pool is about to reuse.
	return buf.String()
}

// renderWithoutPool is the same work, allocating fresh each time, for the
// benchmark to compare against.
func renderWithoutPool(rows []string) string {
	var buf bytes.Buffer

	for i, r := range rows {
		fmt.Fprintf(&buf, "%d:%s;", i, r)
	}

	return buf.String()
}

// forgettingResetLeaksData demonstrates the bug: a buffer returned to the pool
// with content still in it hands that content to the next caller.
//
// The `reused` result is not decoration. sync.Pool.Get is NOT guaranteed to
// return what you just Put: the pool is a per-P cache, so if the goroutine
// moves to another P between the Put and the Get, or a GC runs in between, Get
// misses and calls New instead. CI caught a test of mine that assumed reuse,
// on the race job, where the different scheduling made the miss likely.
//
// So this reports whether reuse actually happened, and the caller decides what
// that means. The bug is real; observing it is probabilistic.
func forgettingResetLeaksData() (first, second string, reused bool) {
	pool := sync.Pool{New: func() any { return new(bytes.Buffer) }}

	// Caller 1 writes and returns the buffer WITHOUT resetting.
	buf := pool.Get().(*bytes.Buffer)
	buf.WriteString("user-1-secret")
	first = buf.String()
	pool.Put(buf) // no Reset

	// Caller 2 may or may not get the same buffer back.
	buf2 := pool.Get().(*bytes.Buffer)
	reused = buf == buf2

	buf2.WriteString("user-2-data")
	second = buf2.String()

	return first, second, reused
}

// leakIsObservable retries until reuse happens, so a test can assert the
// consequence rather than the scheduling. It returns the leaked content once
// the pool actually hands the buffer back.
func leakIsObservable(attempts int) (leaked string, observed bool) {
	for i := 0; i < attempts; i++ {
		_, second, reused := forgettingResetLeaksData()
		if reused {
			return second, true
		}
	}
	return "", false
}

// gcClearsThePool is the property that rules out using it as a resource pool.
// After a garbage collection, what you put in is usually gone.
func gcClearsThePool() (beforeGC, afterGC bool) {
	pool := sync.Pool{} // no New, so Get returns nil when empty

	marker := new(bytes.Buffer)
	marker.WriteString("marker")
	pool.Put(marker)

	beforeGC = pool.Get() != nil

	pool.Put(marker)
	runtime.GC()
	runtime.GC() // two cycles: the first moves to victim cache, the second clears

	afterGC = pool.Get() != nil

	return beforeGC, afterGC
}

// whatAPoolMustNeverHold is the rule, stated.
func whatAPoolMustNeverHold() []string {
	return []string{
		"database connections: use database/sql's own pool, which tracks lifetimes",
		"open files or sockets: nothing calls Close when the GC drops them",
		"anything stateful that must not be lost: the GC clears pools without notice",
		"large objects kept alive only by the pool: that is a leak, not a cache",
		"values rather than pointers: boxing into `any` allocates (staticcheck SA6002)",
	}
}

// demoPools prints pooling behaviour.
func demoPools() {
	rows := []string{"alpha", "beta", "gamma"}

	fmt.Printf("  renderWithPool:    %s\n", renderWithPool(rows))
	fmt.Printf("  renderWithoutPool: %s\n", renderWithoutPool(rows))
	fmt.Println("  ...identical output; `go test -bench BenchmarkRender -benchmem` shows the difference")

	fmt.Printf("\n  forgetting Reset before Put:\n")
	if leaked, observed := leakIsObservable(100); observed {
		fmt.Printf("    caller 1 wrote: %q\n", "user-1-secret")
		fmt.Printf("    caller 2 got:   %q   <- caller 1's data is still there\n", leaked)
	} else {
		fmt.Println("    the pool did not hand the buffer back in 100 attempts")
	}
	fmt.Println("    (Get is not guaranteed to return what you Put: it is a per-P cache)")

	beforeGC, afterGC := gcClearsThePool()
	fmt.Printf("\n  pool contents survive a Get: %t\n", beforeGC)
	fmt.Printf("  pool contents survive a GC: %t\n", afterGC)

	fmt.Println("\n  what a Pool must never hold:")
	for _, s := range whatAPoolMustNeverHold() {
		fmt.Printf("    %s\n", s)
	}
}
