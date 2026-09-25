package main

import (
	"sync"
	"time"
)

// Code that looks racy and is not
// ===============================
//
// Being able to say WHY something is safe is more useful than pattern-matching
// on "shared variable, must need a lock". Each of these is safe, and the reason
// is a specific happens-before edge.

// Not a race 1: distinct slice elements
// -------------------------------------
//
// Several goroutines writing different indexes of one slice share the slice
// HEADER (read-only here) and touch disjoint memory. No lock is needed, and
// adding one would only slow it down.
//
// The condition that makes it safe: nobody writes the header. Replace
// results[i] = v with results = append(results, v) and it is a race again.
func distinctIndexesAreSafe(n int) []int {
	results := make([]int, n)

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Go(func() { results[i] = i })
	}
	wg.Wait()

	return results
}

// Not a race 2: read-only shared data
// -----------------------------------
//
// Concurrent reads are always safe. The requirement is that NOTHING writes
// after the goroutines start, which in practice means building the data before
// launching them and never touching it again.
func readOnlySharingIsSafe(data []int, workers int) int {
	var (
		total atomicIntWrapper
		wg    sync.WaitGroup
	)

	for w := 0; w < workers; w++ {
		wg.Go(func() {
			sum := 0
			for _, v := range data { // read only, by every goroutine
				sum += v
			}
			total.Add(sum)
		})
	}
	wg.Wait()

	return total.Load()
}

// atomicIntWrapper keeps the example's accumulation obviously safe without
// distracting from the point.
type atomicIntWrapper struct {
	mu sync.Mutex
	n  int
}

func (a *atomicIntWrapper) Add(v int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.n += v
}

func (a *atomicIntWrapper) Load() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.n
}

// Not a race 3: ownership transferred over a channel
// --------------------------------------------------
//
// The sender builds a value and hands it over. After the send it never touches
// it again, so only one goroutine can reach it at any moment. No lock anywhere.
//
// The discipline is not enforced by the compiler: keeping a reference and
// writing to it after the send is a race that looks like ordinary code.
type batch struct {
	ID    int
	Items []string
}

func ownershipTransferIsSafe(count int) []int {
	ch := make(chan *batch, count)

	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Go(func() {
			b := &batch{ID: i, Items: []string{"a", "b"}}
			ch <- b
			// b is not touched again. It belongs to the receiver now.
		})
	}

	go func() {
		wg.Wait()
		close(ch)
	}()

	var ids []int
	for b := range ch {
		b.Items = append(b.Items, "processed") // safe: sole owner
		ids = append(ids, b.ID)
	}
	return ids
}

// Not a race 4: a value copied before the goroutine starts
// --------------------------------------------------------
//
// Passing a value as an argument copies it at the `go` statement, which
// happens-before the goroutine runs. The goroutine then works on its own copy.
//
// This is the difference between capturing a variable and passing it. Since Go
// 1.22 loop variables are per-iteration so the common case is safe either way,
// but the distinction still matters for anything the parent mutates.
func copyingBeforeTheGoStatementIsSafe(cfg racyConfig, n int) []int {
	timeouts := make([]int, n)

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		// cfg is copied into the closure's argument HERE, before the goroutine
		// exists, so the parent mutating cfg afterwards cannot affect it.
		wg.Add(1)
		go func(local racyConfig, idx int) {
			defer wg.Done()
			timeouts[idx] = local.Timeout
		}(cfg, i)

		cfg.Timeout++ // the parent mutates its own copy; no goroutine sees it
	}
	wg.Wait()

	return timeouts
}

// Not a race 5: sync.Once
// -----------------------
//
// Once.Do establishes that f() has returned before any Do returns, for every
// caller. So a value initialised inside it is safe to read afterwards with no
// further synchronisation.
type lazyResource struct {
	once  sync.Once
	value string
}

func (r *lazyResource) Get() string {
	r.once.Do(func() {
		time.Sleep(time.Millisecond) // pretend this is expensive
		r.value = "initialised"      // written under the Once
	})

	// Safe without a lock: Do did not return until the write completed, and
	// that is a happens-before edge for every caller.
	return r.value
}

func onceIsSafe(readers int) (values []string, distinct int) {
	r := &lazyResource{}
	values = make([]string, readers)

	var wg sync.WaitGroup
	for i := 0; i < readers; i++ {
		wg.Go(func() { values[i] = r.Get() })
	}
	wg.Wait()

	seen := make(map[string]struct{})
	for _, v := range values {
		seen[v] = struct{}{}
	}
	return values, len(seen)
}

// whyEachIsSafe pairs each case with the rule rather than the reassurance.
func whyEachIsSafe() map[string]string {
	return map[string]string{
		"distinct slice indexes":        "disjoint memory, and nobody writes the slice header",
		"read-only shared data":         "concurrent reads are always safe; nothing writes after the start",
		"ownership over a channel":      "the send is a happens-before edge, and the sender lets go",
		"a value passed as an argument": "copied at the go statement, which happens before the goroutine runs",
		"sync.Once":                     "Do returns only after f() has, for every caller",
	}
}
