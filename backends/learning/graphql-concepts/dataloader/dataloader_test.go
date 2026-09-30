package dataloader

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// echo is a batch function that returns every key as its own value and records each batch it was given.
type echo struct {
	mu      sync.Mutex
	batches [][]int
}

func (e *echo) fetch(_ context.Context, keys []int) (map[int]int, error) {
	e.mu.Lock()
	e.batches = append(e.batches, slices.Sorted(slices.Values(keys)))
	e.mu.Unlock()

	out := make(map[int]int, len(keys))
	for _, k := range keys {
		out[k] = k * 10
	}

	return out, nil
}

// loadAll calls Load for every key from its own goroutine, the way sibling resolvers do.
func loadAll(t *testing.T, l *Loader[int, int], keys []int) []int {
	t.Helper()

	out := make([]int, len(keys))

	var wg sync.WaitGroup

	for i, k := range keys {
		wg.Go(func() {
			v, err := l.Load(context.Background(), k)
			if err != nil {
				t.Errorf("load %d: %v", k, err)
			}

			out[i] = v
		})
	}

	wg.Wait()

	return out
}

// TestConcurrentLoadsShareOneBatch is the N+1 fix: many resolvers, one call to the store.
func TestConcurrentLoadsShareOneBatch(t *testing.T) {
	e := &echo{}
	l := New(e.fetch, 20*time.Millisecond, 100)

	got := loadAll(t, l, []int{1, 2, 3, 4, 5})

	if !slices.Equal(got, []int{10, 20, 30, 40, 50}) {
		t.Fatalf("got %v", got)
	}

	if len(e.batches) != 1 {
		t.Fatalf("%d batches, want 1: %v", len(e.batches), e.batches)
	}
}

// TestARepeatedKeyIsFetchedOnce is the per-request cache.
func TestARepeatedKeyIsFetchedOnce(t *testing.T) {
	e := &echo{}
	l := New(e.fetch, 20*time.Millisecond, 100)

	loadAll(t, l, []int{7, 7, 7})
	loadAll(t, l, []int{7})

	if len(e.batches) != 1 || !slices.Equal(e.batches[0], []int{7}) {
		t.Fatalf("batches %v, want one batch holding 7 once", e.batches)
	}

	if s := l.Stats(); s.Loads != 4 || s.Hits != 3 {
		t.Fatalf("stats %s, want 4 loads and 3 hits", s)
	}
}

// TestMaxBatchSplitsTheWork is the database parameter limit.
func TestMaxBatchSplitsTheWork(t *testing.T) {
	e := &echo{}
	l := New(e.fetch, 20*time.Millisecond, 2)

	loadAll(t, l, []int{1, 2, 3, 4, 5})

	for _, b := range e.batches {
		if len(b) > 2 {
			t.Fatalf("a batch of %d went past MaxBatch 2: %v", len(b), e.batches)
		}
	}
}

// TestPrimeSkipsTheFetch is a cache hit without a batch.
func TestPrimeSkipsTheFetch(t *testing.T) {
	var calls atomic.Int32

	l := New(func(context.Context, []int) (map[int]int, error) {
		calls.Add(1)
		return nil, nil
	}, time.Millisecond, 10)

	l.Prime(3, 33)

	v, err := l.Load(context.Background(), 3)
	if err != nil || v != 33 {
		t.Fatalf("got %d, %v", v, err)
	}

	if calls.Load() != 0 {
		t.Fatal("a primed key was fetched anyway")
	}
}

// TestAMissingKeyIsATypedError lets a caller tell "absent" from "failed" without reading the message.
func TestAMissingKeyIsATypedError(t *testing.T) {
	l := New(func(context.Context, []int) (map[int]int, error) {
		return map[int]int{}, nil
	}, time.Millisecond, 10)

	_, err := l.Load(context.Background(), 9)
	if !errors.Is(err, ErrNoResult) {
		t.Fatalf("got %v, want ErrNoResult", err)
	}
}
