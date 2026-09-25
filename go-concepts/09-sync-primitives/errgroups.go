package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// errgroup
// ========
//
// A WaitGroup that also collects the first error and cancels everything else.
// It is the single most useful concurrency helper in Go, and it lives in
// golang.org/x/sync/errgroup, not the standard library.
//
// This module has no dependencies on purpose, so the version below is written
// out. That is not a loss: errgroup is about sixty lines, and seeing them makes
// it obvious why the real one behaves as it does.
//
// In your own code, use the real thing:
//
//	import "golang.org/x/sync/errgroup"
//
//	g, ctx := errgroup.WithContext(ctx)
//	g.SetLimit(8)
//	for _, u := range urls {
//	    g.Go(func() error { return fetch(ctx, u) })
//	}
//	if err := g.Wait(); err != nil { ... }

// Group runs goroutines and collects the FIRST non-nil error. When created
// with WithContext, the first error also cancels the context, so the others
// stop rather than finishing work nobody will use.
type Group struct {
	wg sync.WaitGroup

	// cancel is called once, by the first goroutine to fail.
	cancelOnce sync.Once
	cancel     context.CancelCauseFunc

	// errOnce guards firstErr, so a burst of simultaneous failures still
	// records exactly the first one to arrive.
	errOnce  sync.Once
	firstErr error

	// sem bounds concurrency when SetLimit is used. A nil channel means
	// unlimited, which is the nil-channel trick from lesson 08 used as a flag.
	sem chan struct{}

	// started records whether Go has been called, so SetLimit can refuse to
	// change the limit underneath running goroutines.
	started bool
}

// WithContext returns a Group and a derived context that is cancelled as soon
// as any goroutine returns an error, or when Wait returns.
//
// CancelCauseFunc rather than CancelFunc so ctx.Err() callers can recover the
// causing error with context.Cause(ctx), instead of only learning "cancelled".
func WithContext(parent context.Context) (*Group, context.Context) {
	ctx, cancel := context.WithCancelCause(parent)
	return &Group{cancel: cancel}, ctx
}

// SetLimit bounds how many goroutines run at once. n <= 0 means unlimited.
// It must be called before any Go, which is what the real errgroup enforces
// with a panic.
func (g *Group) SetLimit(n int) {
	if g.wgStarted() {
		panic("errgroup: SetLimit must be called before Go")
	}
	if n <= 0 {
		g.sem = nil
		return
	}
	g.sem = make(chan struct{}, n)
}

// wgStarted is a small helper so SetLimit can complain usefully. The real
// errgroup tracks this differently; the check matters more than the mechanism.
func (g *Group) wgStarted() bool { return g.started }

// Go runs fn in a new goroutine. The first fn to return a non-nil error has
// that error recorded and the context cancelled; later errors are discarded.
func (g *Group) Go(fn func() error) {
	g.started = true

	if g.sem != nil {
		g.sem <- struct{}{} // blocks when the limit is reached
	}

	g.wg.Add(1)
	go func() {
		defer func() {
			if g.sem != nil {
				<-g.sem
			}
			g.wg.Done()
		}()

		if err := fn(); err != nil {
			g.errOnce.Do(func() {
				g.firstErr = err
				g.cancelOnce.Do(func() {
					if g.cancel != nil {
						g.cancel(err) // cancel WITH the cause
					}
				})
			})
		}
	}()
}

// Wait blocks until every goroutine has returned, then reports the first error.
//
// Cancelling in Wait matters: without it, a Group whose goroutines all
// succeeded would leave its derived context uncancelled, leaking whatever the
// context holds.
func (g *Group) Wait() error {
	g.wg.Wait()

	g.cancelOnce.Do(func() {
		if g.cancel != nil {
			g.cancel(g.firstErr)
		}
	})

	return g.firstErr
}

// fetchResult stands in for anything fetched concurrently.
type fetchResult struct {
	URL   string
	Bytes int
}

// fetchAll is the shape errgroup exists for: run N things, stop at the first
// failure, and return the successes gathered so far.
//
// Results go into a pre-sized slice indexed by position, so no mutex is needed
// and the output order matches the input order, which a channel would not give.
func fetchAll(ctx context.Context, urls []string, fetch func(context.Context, string) (int, error)) ([]fetchResult, error) {
	g, ctx := WithContext(ctx)
	g.SetLimit(4) // bound concurrency: 1000 urls should not be 1000 requests

	results := make([]fetchResult, len(urls))

	for i, u := range urls {
		g.Go(func() error {
			n, err := fetch(ctx, u)
			if err != nil {
				return fmt.Errorf("fetch %s: %w", u, err)
			}
			results[i] = fetchResult{URL: u, Bytes: n}
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return results, err
	}
	return results, nil
}

// fakeFetch simulates a network call that respects cancellation.
func fakeFetch(failOn string, delay time.Duration) func(context.Context, string) (int, error) {
	return func(ctx context.Context, url string) (int, error) {
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(delay):
		}

		if url == failOn {
			return 0, errors.New("404 not found")
		}
		return len(url) * 10, nil
	}
}

// waitGroupCannotDoThis is the comparison worth making. A plain WaitGroup has
// nowhere to put an error, so every use of one alongside concurrent fallible
// work grows the same three pieces of scaffolding by hand.
func waitGroupCannotDoThis() []string {
	return []string{
		"a WaitGroup has no error: you add a mutex and an error variable",
		"...and a sync.Once, or the last failure overwrites the first",
		"...and a context, or the others keep working after one has failed",
		"that is errgroup. Reimplementing it per package is how it gets subtly wrong",
	}
}

// demoErrgroups prints group behaviour.
func demoErrgroups() {
	urls := []string{"a.example", "b.example", "c.example", "d.example", "e.example"}

	results, err := fetchAll(context.Background(), urls, fakeFetch("", 5*time.Millisecond))
	fmt.Printf("  all succeed: err=%v\n", err)
	for _, r := range results {
		fmt.Printf("    %-12s %d bytes\n", r.URL, r.Bytes)
	}

	results, err = fetchAll(context.Background(), urls, fakeFetch("c.example", 20*time.Millisecond))
	fmt.Printf("\n  one fails: err=%v\n", err)
	succeeded := 0
	for _, r := range results {
		if r.URL != "" {
			succeeded++
		}
	}
	fmt.Printf("    %d of %d completed before the group was cancelled\n", succeeded, len(urls))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err = fetchAll(ctx, urls, fakeFetch("", 50*time.Millisecond))
	fmt.Printf("\n  caller's deadline propagates: err=%v\n", err)

	fmt.Println("\n  what a bare WaitGroup makes you build:")
	for _, s := range waitGroupCannotDoThis() {
		fmt.Printf("    %s\n", s)
	}
}
