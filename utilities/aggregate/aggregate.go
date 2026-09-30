// Package aggregate fetches from several sources at once and collects the results.
//
// # The shape this replaces
//
// A loop calling each source in turn. Three sources at 100ms each is 300ms, and the work is entirely waiting,
// so it should be 100ms. That is the whole motivation, and it is where most people stop.
//
// What they miss is everything after: an unbounded fan-out over a large list opens a connection per item and
// falls over, a source that hangs holds the whole aggregate forever, and the first error either cancels the
// rest or does not, and which one you want is a decision rather than a default.
package aggregate

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
)

// Source is one thing to fetch from.
type Source struct {
	Name  string
	Fetch func(ctx context.Context) (string, error)
}

// Result is what one source produced.
type Result struct {
	Name  string
	Value string
	Err   error
	Took  time.Duration
}

// Options configures a run.
type Options struct {
	// Limit bounds how many sources are in flight. Zero means no limit.
	//
	// "No limit" is the right default for a handful of sources and wrong for a thousand: an unbounded fan-out
	// over a big list opens a connection per item, and the failure is a connection pool exhausting or a
	// remote service rate-limiting you.
	Limit int

	// Timeout bounds the whole aggregate, not each source. Zero means none.
	Timeout time.Duration
}

// All fetches from every source and returns every result, errors included.
//
// # Why this does not stop at the first error
//
// Because an aggregate usually wants partial data. Three sources where one is down should give two answers and
// one error, not nothing. FirstError below is the other policy, and having both named makes the choice
// explicit rather than accidental.
//
// # Why the results are collected into a pre-sized slice and not a channel
//
// Each goroutine writes to results[i], which needs no mutex and no channel, because no two goroutines touch the
// same index. This is the pattern people reach for a mutex for, and it is the one place in Go where writing to
// a shared slice concurrently is correct: different elements are different memory.
func All(ctx context.Context, sources []Source, opts Options) ([]Result, error) {
	if len(sources) == 0 {
		return nil, nil
	}

	if opts.Timeout > 0 {
		var cancel context.CancelFunc

		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	results := make([]Result, len(sources))

	var (
		wg  sync.WaitGroup
		sem chan struct{}
	)

	// A buffered channel as a semaphore: acquire by sending, release by receiving. It is three lines and it
	// composes with select, which a sync.Mutex-based counter does not.
	if opts.Limit > 0 {
		sem = make(chan struct{}, opts.Limit)
	}

	for i, src := range sources {
		// Acquire BEFORE starting the goroutine, in the loop. Acquiring inside it bounds the fetches and not
		// the goroutines: a thousand sources with a limit of 2 started a thousand goroutines that sat on the
		// semaphore. Here the loop itself waits, so at most Limit goroutines exist at once.
		//
		// The select is also the context check. A cancelled aggregate with a limit of 2 and 100 sources
		// stops handing out slots, and every source not yet started gets the context's error without running.
		if sem != nil {
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				results[i] = Result{Name: src.Name, Err: ctx.Err()}

				continue
			}
		}

		wg.Add(1)

		go func() {
			defer wg.Done()

			if sem != nil {
				defer func() { <-sem }()
			}

			start := time.Now()

			// Checked again here for the no-limit case, and for a context cancelled between the slot and the
			// goroutine starting.
			if err := ctx.Err(); err != nil {
				results[i] = Result{Name: src.Name, Err: err}

				return
			}

			value, err := src.Fetch(ctx)

			results[i] = Result{Name: src.Name, Value: value, Err: err, Took: time.Since(start)}
		}()
	}

	wg.Wait()

	return results, ctx.Err()
}

// ErrAllFailed is returned by Values when nothing succeeded.
var ErrAllFailed = errors.New("aggregate: every source failed")

// Values returns the successful values and a joined error for the rest.
//
// errors.Join, not a string of messages. The joined error still matches errors.Is against each of its parts, so
// a caller can ask "was one of these a context deadline" without parsing prose.
func Values(results []Result) (map[string]string, error) {
	values := make(map[string]string, len(results))

	var errs []error

	for _, r := range results {
		if r.Err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", r.Name, r.Err))

			continue
		}

		values[r.Name] = r.Value
	}

	joined := errors.Join(errs...)

	if len(values) == 0 && joined != nil {
		return nil, fmt.Errorf("%w: %w", ErrAllFailed, joined)
	}

	return values, joined
}

// FirstError fetches from every source and cancels the rest as soon as one fails.
//
// # What errgroup adds over a WaitGroup
//
// A derived context that is cancelled when any goroutine returns an error, plus the first error. That is the
// whole library, and writing it by hand is thirty lines of exactly the code people get wrong.
//
// SetLimit is the bound, and it must be called BEFORE any Go: calling it afterwards panics, which is the
// library refusing to let you change the rules mid-flight.
//
// The right policy when one source failing makes the whole answer useless. All is the right one when partial
// data is worth having, and the difference is a product decision rather than a technical one.
func FirstError(ctx context.Context, sources []Source, opts Options) (map[string]string, error) {
	if opts.Timeout > 0 {
		var cancel context.CancelFunc

		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	g, ctx := errgroup.WithContext(ctx)

	if opts.Limit > 0 {
		g.SetLimit(opts.Limit)
	}

	var (
		mu     sync.Mutex
		values = make(map[string]string, len(sources))
	)

	for _, src := range sources {
		g.Go(func() error {
			// errgroup cancels ctx on the first error, and does nothing else about the sources still waiting
			// for a slot under SetLimit: they start later, on a context that is already cancelled. A source
			// that does not check ctx would then do its whole fetch for an answer that will be thrown away.
			if err := ctx.Err(); err != nil {
				return err
			}

			value, err := src.Fetch(ctx)
			if err != nil {
				return fmt.Errorf("%s: %w", src.Name, err)
			}

			// A map, unlike a slice element, genuinely needs the mutex: concurrent writes to a map are a
			// runtime panic, not a subtle race, and the runtime says so with "concurrent map writes".
			mu.Lock()
			defer mu.Unlock()

			values[src.Name] = value

			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}

	return values, nil
}

// Names returns the sources' names, sorted, for a stable log line.
func Names(results []Result) []string {
	out := make([]string, 0, len(results))
	for _, r := range results {
		out = append(out, r.Name)
	}

	sort.Strings(out)

	return out
}
