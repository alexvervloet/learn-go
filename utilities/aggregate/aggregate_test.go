package aggregate

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// slow builds a source that takes d and succeeds.
func slow(name string, d time.Duration) Source {
	return Source{
		Name: name,
		Fetch: func(ctx context.Context) (string, error) {
			select {
			case <-time.After(d):
				return name + "-value", nil
			case <-ctx.Done():
				return "", ctx.Err()
			}
		},
	}
}

// failing builds a source that fails immediately.
func failing(name string, err error) Source {
	return Source{
		Name:  name,
		Fetch: func(context.Context) (string, error) { return "", err },
	}
}

// TestSourcesRunConcurrently is the whole motivation.
//
// The assertion is not "it was fast", which is a property of the machine. It is that the WALL CLOCK is far
// below the SUM, which holds on any machine that can run three goroutines: sequential would be at least 300ms
// and this is bounded well under that.
func TestSourcesRunConcurrently(t *testing.T) {
	const each = 100 * time.Millisecond

	sources := []Source{slow("a", each), slow("b", each), slow("c", each)}

	start := time.Now()

	results, err := All(context.Background(), sources, Options{})
	require.NoError(t, err)

	elapsed := time.Since(start)

	require.Len(t, results, 3)

	for _, r := range results {
		require.NoError(t, r.Err)
		require.Equal(t, r.Name+"-value", r.Value)
	}

	require.Less(t, elapsed, 3*each,
		"sequential would be %v; concurrent was %v", 3*each, elapsed)

	t.Logf("three %v sources in %v", each, elapsed.Round(time.Millisecond))
}

// TestAllReturnsPartialResults is the policy difference.
func TestAllReturnsPartialResults(t *testing.T) {
	boom := errors.New("the service is down")

	results, err := All(context.Background(), []Source{
		slow("a", time.Millisecond),
		failing("b", boom),
		slow("c", time.Millisecond),
	}, Options{})
	require.NoError(t, err, "one source failing is not the aggregate failing")

	values, joined := Values(results)
	require.Len(t, values, 2, "two answers and one error, not nothing")
	require.Equal(t, "a-value", values["a"])
	require.Equal(t, "c-value", values["c"])

	require.Error(t, joined)
	require.ErrorIs(t, joined, boom,
		"errors.Join keeps each part matchable, so a caller can ask what kind of failure it was")
	require.Contains(t, joined.Error(), "b:")
}

// TestEveryFailureIsDistinguishable covers the all-failed case.
func TestEveryFailureIsDistinguishable(t *testing.T) {
	results, err := All(context.Background(), []Source{
		failing("a", errors.New("down")),
		failing("b", errors.New("also down")),
	}, Options{})
	require.NoError(t, err)

	values, joined := Values(results)
	require.Nil(t, values)
	require.ErrorIs(t, joined, ErrAllFailed,
		"no data at all is a different outcome from partial data, and a caller usually treats it differently")
}

// TestTheLimitBoundsConcurrency is what stops a fan-out over a large list falling over.
func TestTheLimitBoundsConcurrency(t *testing.T) {
	const (
		sources = 50
		limit   = 4
	)

	var (
		inFlight atomic.Int32
		peak     atomic.Int32
	)

	list := make([]Source, 0, sources)

	for i := range sources {
		list = append(list, Source{
			Name: fmt.Sprintf("s%d", i),
			Fetch: func(context.Context) (string, error) {
				now := inFlight.Add(1)
				defer inFlight.Add(-1)

				// A compare-and-swap loop rather than a max, because two goroutines can read the same peak
				// and both write a lower value. This is the shape people get wrong with a plain Store.
				for {
					old := peak.Load()
					if now <= old || peak.CompareAndSwap(old, now) {
						break
					}
				}

				time.Sleep(5 * time.Millisecond)

				return "ok", nil
			},
		})
	}

	results, err := All(context.Background(), list, Options{Limit: limit})
	require.NoError(t, err)
	require.Len(t, results, sources)

	require.LessOrEqual(t, peak.Load(), int32(limit),
		"%d sources never had more than %d in flight, peak was %d", sources, limit, peak.Load())

	t.Logf("%d sources, limit %d, peak concurrency %d", sources, limit, peak.Load())
}

// TestWithoutALimitEverythingRunsAtOnce is the contrast.
func TestWithoutALimitEverythingRunsAtOnce(t *testing.T) {
	const sources = 30

	var (
		started sync.WaitGroup
		release = make(chan struct{})
		peak    atomic.Int32
	)

	started.Add(sources)

	list := make([]Source, 0, sources)

	for i := range sources {
		list = append(list, Source{
			Name: fmt.Sprintf("s%d", i),
			Fetch: func(context.Context) (string, error) {
				peak.Add(1)
				started.Done()

				// Block until every source has started, which is only possible if they all run at once.
				<-release

				return "ok", nil
			},
		})
	}

	go func() {
		started.Wait()
		close(release)
	}()

	_, err := All(context.Background(), list, Options{})
	require.NoError(t, err)

	require.Equal(t, int32(sources), peak.Load(),
		"no limit means one goroutine per source, which is fine for 30 and not for 30,000")
}

// TestTheTimeoutBoundsTheWholeAggregate is the other half of the safety.
func TestTheTimeoutBoundsTheWholeAggregate(t *testing.T) {
	results, err := All(context.Background(), []Source{
		slow("fast", time.Millisecond),
		slow("slow", 10*time.Second),
	}, Options{Timeout: 100 * time.Millisecond})

	require.ErrorIs(t, err, context.DeadlineExceeded,
		"the aggregate reports that it was cut short, so a caller does not report partial data as complete")

	values, joined := Values(results)
	require.Equal(t, "fast-value", values["fast"], "what finished is still returned")
	require.ErrorIs(t, joined, context.DeadlineExceeded)
}

// TestCancellationSkipsQueuedSources is why the context check is after the gate.
//
// Without it, a cancelled aggregate with a limit of 2 and 100 sources still runs all 100, two at a time,
// because every goroutine was already started before the cancellation.
func TestCancellationSkipsQueuedSources(t *testing.T) {
	const sources = 40

	var ran atomic.Int32

	ctx, cancel := context.WithCancel(context.Background())

	list := make([]Source, 0, sources)

	for i := range sources {
		list = append(list, Source{
			Name: fmt.Sprintf("s%d", i),
			Fetch: func(context.Context) (string, error) {
				if ran.Add(1) == 2 {
					// Cancel once a couple have run.
					cancel()
				}

				time.Sleep(5 * time.Millisecond)

				return "ok", nil
			},
		})
	}

	defer cancel()

	_, err := All(ctx, list, Options{Limit: 2})
	require.ErrorIs(t, err, context.Canceled)

	require.Less(t, ran.Load(), int32(sources),
		"only %d of %d sources ran; the rest saw the cancelled context at the gate", ran.Load(), sources)

	t.Logf("%d of %d sources ran before the cancellation took effect", ran.Load(), sources)
}

// TestFirstErrorCancelsTheRest is the other policy.
func TestFirstErrorCancelsTheRest(t *testing.T) {
	boom := errors.New("the service is down")

	var started, cancelled atomic.Bool

	watcher := Source{
		Name: "watcher",
		Fetch: func(ctx context.Context) (string, error) {
			started.Store(true)

			select {
			case <-time.After(2 * time.Second):
				return "never", nil
			case <-ctx.Done():
				cancelled.Store(true)

				return "", ctx.Err()
			}
		},
	}

	start := time.Now()

	values, err := FirstError(context.Background(), []Source{watcher, failing("bad", boom)}, Options{})

	elapsed := time.Since(start)

	require.ErrorIs(t, err, boom)
	require.Nil(t, values, "nothing is returned, because the policy is that partial data is useless here")
	// Two correct outcomes, and which one happens is scheduling. The watcher was already fetching and saw the
	// cancellation, or the failure landed before its goroutine got going and it never started at all.
	if started.Load() {
		require.True(t, cancelled.Load(), "errgroup cancelled the derived context on the first error")
	}
	require.Less(t, elapsed, time.Second, "it did not wait out the slow source: %v", elapsed)
}

// TestFirstErrorReturnsEverythingWhenNothingFails is the happy path.
func TestFirstErrorReturnsEverythingWhenNothingFails(t *testing.T) {
	values, err := FirstError(context.Background(), []Source{
		slow("a", time.Millisecond), slow("b", time.Millisecond), slow("c", time.Millisecond),
	}, Options{Limit: 2})
	require.NoError(t, err)

	require.Equal(t, map[string]string{"a": "a-value", "b": "b-value", "c": "c-value"}, values)
}

// TestAnEmptyListIsNotAnError is the boundary.
func TestAnEmptyListIsNotAnError(t *testing.T) {
	results, err := All(context.Background(), nil, Options{})
	require.NoError(t, err)
	require.Empty(t, results)

	values, joined := Values(results)
	require.Empty(t, values)
	require.NoError(t, joined, "no sources is not the same as every source failing")
}

// TestALimitBoundsGoroutinesNotJustFetches is the difference between limiting the work and limiting the
// goroutines. A thousand sources with a limit of 2 used to start a thousand goroutines that queued on the
// semaphore; the fetches were bounded, the stacks and scheduler load were not.
func TestALimitBoundsGoroutinesNotJustFetches(t *testing.T) {
	const n = 1000

	release := make(chan struct{})
	running := make(chan struct{}, n)

	sources := make([]Source, n)
	for i := range sources {
		sources[i] = Source{Name: fmt.Sprint(i), Fetch: func(context.Context) (string, error) {
			running <- struct{}{}
			<-release

			return "ok", nil
		}}
	}

	before := runtime.NumGoroutine()

	done := make(chan struct{})

	go func() {
		defer close(done)

		_, _ = All(context.Background(), sources, Options{Limit: 2})
	}()

	<-running
	<-running

	// The peak over the next 100ms, while both fetches are held. Sampling rather than one reading, because a
	// loop that spawns everything may not have finished spawning at the first look. NumGoroutine counts the
	// whole process, so this is a margin, not an exact count: a limit that holds adds a handful, one that does
	// not adds about a thousand.
	grew := 0
	for range 100 {
		grew = max(grew, runtime.NumGoroutine()-before)
		time.Sleep(time.Millisecond)
	}

	close(release)
	<-done

	require.Less(t, grew, 50, "a limit of 2 started %d goroutines", grew)
}

// TestFirstErrorDoesNotStartQueuedSourcesAfterAFailure is the check errgroup does not make for you. With a
// limit, sources wait for a slot; once one has failed, the ones still waiting must not run on a cancelled
// context, because a source that does not check ctx would do its whole fetch for nothing.
func TestFirstErrorDoesNotStartQueuedSourcesAfterAFailure(t *testing.T) {
	var ran atomic.Int32

	ignoresCtx := Source{Name: "later", Fetch: func(context.Context) (string, error) {
		ran.Add(1)

		return "wasted", nil
	}}

	failing := Source{Name: "first", Fetch: func(context.Context) (string, error) {
		return "", errors.New("down")
	}}

	_, err := FirstError(context.Background(), []Source{failing, ignoresCtx, ignoresCtx}, Options{Limit: 1})
	require.ErrorContains(t, err, "down")
	require.Zero(t, ran.Load(), "a source queued behind the failure ran anyway")
}
