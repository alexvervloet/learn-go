// Package dataloader is the fix for GraphQL's N+1, written out rather than imported.
//
// # Why GraphQL has an N+1 problem by construction
//
// A resolver resolves ONE field of ONE object. `{ authors { books { title } } }` calls the authors resolver once
// and the books resolver once PER AUTHOR, because that is what the execution model says: resolve the parent,
// then resolve each child's fields against each parent.
//
// So 50 authors is 51 queries, and nothing in the schema or the resolver says so. In REST the same endpoint
// would be one handler making one query, and the N+1 would be a loop somebody wrote. In GraphQL it is the
// default and avoiding it takes work.
//
// # What a dataloader does
//
// It sits between the resolver and the store. Each resolver calls Load(key) and gets a future. The loader
// collects keys, waits a moment or until a batch is full, calls the batch function once, and completes every
// future.
//
// So the 50 books resolvers still run, still 50 calls to Load, and ONE call to the store.
//
// # The three things that make it work
//
//	BATCHING   collect keys and fetch them together. The whole point.
//	CACHING    the same key asked for twice in one request is fetched once. Per REQUEST, never
//	           across requests: a loader that outlives a request serves stale data and, worse,
//	           serves one user's data to another.
//	TIMING     the batch has to fire when the resolvers have all called Load and not before. This
//	           is the part everyone gets wrong, and it is why this package exists rather than a
//	           `sync.Map` and a comment.
//
// # The timing problem
//
// graphql-go and gqlgen resolve sibling fields CONCURRENTLY, so the 50 books resolvers run in 50 goroutines at
// roughly the same time. A loader that fires on the first Load batches one key. A loader that waits for a fixed
// number batches correctly and hangs when there are fewer.
//
// The answer every implementation uses is a short timer: the first Load starts a window, every Load inside it
// joins the batch, and the batch fires when the window closes or the batch is full. The window is a millisecond
// or two, which is invisible next to a database round trip and is a real added latency when there is nothing to
// batch.
//
// dataloadgen and graph-gophers/dataloader both do this. Writing it out is fifty lines and the timing is the
// thing worth understanding.
package dataloader

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// ErrNoResult is the error for a key the batch function did not return.
//
// A caller that treats a missing key as an empty answer checks for it with errors.Is. Matching on the message
// instead ties the caller to a string this package is free to reword.
var ErrNoResult = errors.New("dataloader: no result for key")

// BatchFunc fetches many keys at once.
//
// Returning a map rather than a slice: the caller asked for a set and some may be missing, so a slice would have
// to be the same length and order as the input with a hole for each miss. A map makes the absence explicit.
type BatchFunc[K comparable, V any] func(ctx context.Context, keys []K) (map[K]V, error)

// Loader batches and caches loads of one type.
type Loader[K comparable, V any] struct {
	batch BatchFunc[K, V]

	// Wait is how long to collect keys before firing. See the package doc: too short and nothing
	// batches, too long and every request pays it.
	wait time.Duration

	// MaxBatch caps a batch, because a database has a parameter limit (Postgres: 65535) and a query
	// with 50,000 ids in an IN clause is slower than five with 10,000.
	maxBatch int

	mu sync.Mutex

	// cache is per LOADER, and a loader is per REQUEST. A loader stored on a server struct caches
	// across requests, which serves stale data and can serve one user's data to another.
	cache map[K]*result[V]

	// pending is the batch being collected.
	pending map[K]*result[V]

	// timer fires the pending batch. nil when nothing is pending.
	timer *time.Timer

	batches atomic.Int64
	loads   atomic.Int64
	hits    atomic.Int64
}

// result is a future: a value that will be there once done is closed.
type result[V any] struct {
	done  chan struct{}
	value V
	err   error
}

// New builds a loader.
func New[K comparable, V any](batch BatchFunc[K, V], wait time.Duration, maxBatch int) *Loader[K, V] {
	if wait <= 0 {
		wait = time.Millisecond
	}
	if maxBatch <= 0 {
		maxBatch = 1000
	}

	return &Loader[K, V]{
		batch:    batch,
		wait:     wait,
		maxBatch: maxBatch,
		cache:    make(map[K]*result[V]),
		pending:  make(map[K]*result[V]),
	}
}

// Load fetches one key, batching it with whatever else arrives in the window.
//
// Blocks until the batch completes, which is what makes it usable from a resolver: the resolver's signature
// returns a value, not a future, so the waiting has to happen inside.
func (l *Loader[K, V]) Load(ctx context.Context, key K) (V, error) {
	var zero V

	l.loads.Add(1)

	l.mu.Lock()

	// Already fetched or being fetched in this request.
	if r, ok := l.cache[key]; ok {
		l.mu.Unlock()
		l.hits.Add(1)

		select {
		case <-r.done:
			return r.value, r.err
		case <-ctx.Done():
			return zero, ctx.Err()
		}
	}

	r := &result[V]{done: make(chan struct{})}

	l.cache[key] = r
	l.pending[key] = r

	// Full: fire now rather than waiting out the window.
	if len(l.pending) >= l.maxBatch {
		batch := l.takePendingLocked()
		l.mu.Unlock()

		l.run(ctx, batch)
	} else {
		// The first key of a batch starts the timer. Later keys join it, which is why the timer is
		// started only when there is not one.
		if l.timer == nil {
			l.timer = time.AfterFunc(l.wait, func() {
				l.mu.Lock()
				batch := l.takePendingLocked()
				l.mu.Unlock()

				if len(batch) > 0 {
					// context.WithoutCancel: the timer fires on its own goroutine and
					// the context that started the batch may belong to a resolver that
					// has already returned. Using it would cancel the batch for every
					// OTHER waiter.
					l.run(context.WithoutCancel(ctx), batch)
				}
			})
		}

		l.mu.Unlock()
	}

	select {
	case <-r.done:
		return r.value, r.err
	case <-ctx.Done():
		return zero, ctx.Err()
	}
}

// takePendingLocked removes the pending batch and stops the timer. Called with the mutex held.
func (l *Loader[K, V]) takePendingLocked() map[K]*result[V] {
	if l.timer != nil {
		l.timer.Stop()
		l.timer = nil
	}

	batch := l.pending
	l.pending = make(map[K]*result[V])

	return batch
}

// run calls the batch function and completes every future.
func (l *Loader[K, V]) run(ctx context.Context, batch map[K]*result[V]) {
	if len(batch) == 0 {
		return
	}

	l.batches.Add(1)

	keys := make([]K, 0, len(batch))
	for k := range batch {
		keys = append(keys, k)
	}

	values, err := l.batch(ctx, keys)

	for k, r := range batch {
		if err != nil {
			r.err = err
		} else if v, ok := values[k]; ok {
			r.value = v
		} else {
			// A key the batch function did not return. This has to be an error rather than a
			// zero value: a resolver that gets a zero Author renders `{"id":"","name":""}` and
			// nothing says the row was missing.
			r.err = fmt.Errorf("%w %v", ErrNoResult, k)
		}

		// Closing the channel is what completes the future, and it must happen exactly once per
		// result. The map guarantees that: a key appears once.
		close(r.done)
	}
}

// LoadMany fetches several keys, which is one call rather than N to Load.
//
// Still one batch, because the keys go into the same pending map. The difference is that LoadMany does not wait
// between them, so they cannot land in different windows.
func (l *Loader[K, V]) LoadMany(ctx context.Context, keys []K) ([]V, error) {
	out := make([]V, len(keys))

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs error
	)

	for i, key := range keys {
		wg.Add(1)

		go func() {
			defer wg.Done()

			v, err := l.Load(ctx, key)

			mu.Lock()
			defer mu.Unlock()

			if err != nil {
				if errs == nil {
					errs = err
				}
				return
			}

			out[i] = v
		}()
	}

	wg.Wait()

	return out, errs
}

// Prime puts a value in the cache without fetching it.
//
// The optimisation nobody uses and everybody should. A query that already fetched the books knows their authors'
// ids, and often the authors themselves; priming the author loader with them means the author resolver is a cache
// hit rather than a batch.
func (l *Loader[K, V]) Prime(key K, value V) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if _, ok := l.cache[key]; ok {
		return
	}

	r := &result[V]{done: make(chan struct{}), value: value}
	close(r.done)

	l.cache[key] = r
}

// Stats reports what the loader did.
func (l *Loader[K, V]) Stats() Stats {
	return Stats{
		Loads:   l.loads.Load(),
		Batches: l.batches.Load(),
		Hits:    l.hits.Load(),
	}
}

// Stats is a loader's counters.
type Stats struct {
	Loads   int64
	Batches int64
	Hits    int64
}

// String formats them.
func (s Stats) String() string {
	return fmt.Sprintf("%d loads, %d batches, %d cache hits", s.Loads, s.Batches, s.Hits)
}
