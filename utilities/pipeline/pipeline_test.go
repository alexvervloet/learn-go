package pipeline

import (
	"context"
	"runtime"
	"sort"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

// TestAPipelineComposes is the basic shape.
func TestAPipelineComposes(t *testing.T) {
	ctx := context.Background()

	numbers := Generate(ctx, 1, 2, 3, 4, 5)
	squared := Square(ctx, numbers)
	even := Filter(ctx, squared, func(v int) bool { return v%2 == 0 })

	got, err := Collect(ctx, even)
	require.NoError(t, err)
	require.Equal(t, []int{4, 16}, got, "1,4,9,16,25 filtered to the even ones")
}

// TestOrderSurvivesASingleChain is worth pinning before fan-out breaks it.
func TestOrderSurvivesASingleChain(t *testing.T) {
	ctx := context.Background()

	got, err := Collect(ctx, Square(ctx, Generate(ctx, 1, 2, 3, 4, 5)))
	require.NoError(t, err)
	require.Equal(t, []int{1, 4, 9, 16, 25}, got, "one stage, one goroutine, order preserved")
}

// TestFanOutLosesOrder is the cost of the pattern, asserted rather than mentioned.
func TestFanOutLosesOrder(t *testing.T) {
	ctx := context.Background()

	in := Generate(ctx, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10)
	workers := FanOut(ctx, in, 4, Square)
	merged := FanIn(ctx, workers...)

	got, err := Collect(ctx, merged)
	require.NoError(t, err)

	require.Len(t, got, 10, "every value came through exactly once")

	sort.Ints(got)
	require.Equal(t, []int{1, 4, 9, 16, 25, 36, 49, 64, 81, 100}, got,
		"the same values; only the order is unspecified, which is why this sorts before comparing")
}

// TestFanInWaitsForEveryInput is the WaitGroup doing its job.
//
// A FanIn that closes after the first input finishes truncates the output silently, which is a bug that looks
// like a data problem rather than a concurrency one.
func TestFanInWaitsForEveryInput(t *testing.T) {
	ctx := context.Background()

	fast := Generate(ctx, 1)
	slowCh := make(chan int)

	go func() {
		defer close(slowCh)

		time.Sleep(50 * time.Millisecond)

		slowCh <- 2
		slowCh <- 3
	}()

	got, err := Collect(ctx, FanIn(ctx, fast, slowCh))
	require.NoError(t, err)

	sort.Ints(got)
	require.Equal(t, []int{1, 2, 3}, got, "the slow input's values are all there")
}

// TestAbandoningAPipelineDoesNotLeak is the rule people leave out.
//
// A consumer that stops reading leaves every upstream stage blocked on a send nobody will receive. Without the
// select on ctx.Done, those goroutines live until the process ends, and nothing reports it.
//
// The assertion counts goroutines rather than asserting a duration, which is the machine-independent number.
func TestAbandoningAPipelineDoesNotLeak(t *testing.T) {
	before := runtime.NumGoroutine()

	ctx, cancel := context.WithCancel(context.Background())

	// A long stream, so the stages are certainly still working when the consumer walks away.
	values := make([]int, 1000)
	for i := range values {
		values[i] = i
	}

	in := Generate(ctx, values...)
	squared := Square(ctx, in)
	filtered := Filter(ctx, squared, func(int) bool { return true })

	// Take three and leave.
	for range 3 {
		<-filtered
	}

	cancel()

	// Poll for the goroutines to finish. A goroutine that has been unblocked has not necessarily returned
	// yet, so a single immediate check fails a few percent of the time.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= before {
			return
		}

		runtime.Gosched()
		time.Sleep(5 * time.Millisecond)
	}

	require.LessOrEqual(t, runtime.NumGoroutine(), before,
		"three stages are still blocked on a send nobody is receiving")
}

// TestCollectReportsBeingCutShort is the difference a caller has to see.
func TestCollectReportsBeingCutShort(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	blocked := make(chan int) // nothing ever sends

	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	got, err := Collect(ctx, blocked)
	require.ErrorIs(t, err, context.Canceled,
		"a caller that cannot tell 'finished' from 'cut off' reports partial results as complete")
	require.Empty(t, got)
}

// TestACancelledGeneratorStopsSending is rule two at the source.
func TestACancelledGeneratorStopsSending(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	values := make([]int, 10_000)
	for i := range values {
		values[i] = i
	}

	out := Generate(ctx, values...)

	require.Equal(t, 0, <-out)

	cancel()

	// Drain whatever is in flight. The channel must CLOSE, which only happens if the generator returned.
	drained := 0

	for range out {
		drained++
	}

	require.Less(t, drained, len(values),
		"the generator returned rather than finishing work nobody wanted: %d of %d sent", drained, len(values))
}

// ---------------------------------------------------------------------------
// The ETL pipeline
// ---------------------------------------------------------------------------

// TestABadRecordDoesNotStopTheRest is the ETL policy.
func TestABadRecordDoesNotStopTheRest(t *testing.T) {
	ctx := context.Background()

	in := Records(ctx,
		Record{ID: 1, Name: "  alex  ", Score: 80},
		Record{ID: 2, Name: "", Score: 50},
		Record{ID: 3, Name: "SAM", Score: 150},
		Record{ID: 4, Name: "jo", Score: 95},
	)

	loaded, failed, err := Load(ctx, Transform(ctx, in, Normalise, Grade))
	require.NoError(t, err)

	require.Len(t, loaded, 2, "the two good records went through")
	require.Len(t, failed, 2, "and both failures are reported, not just the first")

	require.Equal(t, "Alex", loaded[0].Name, "normalised: trimmed and title-cased")
	require.Equal(t, "Jo", loaded[1].Name)

	for _, err := range failed {
		require.ErrorIs(t, err, ErrInvalid)
	}

	require.ErrorContains(t, failed[0], "record 2 has no name")
	require.ErrorContains(t, failed[1], "record 3 has score 150")
}

// TestStagesRunInOrderAndStopAtTheFirstFailure is the per-record contract.
func TestStagesRunInOrderAndStopAtTheFirstFailure(t *testing.T) {
	ctx := context.Background()

	var graded int

	counting := func(r Record) (Record, error) {
		graded++

		return Grade(r)
	}

	in := Records(ctx, Record{ID: 1, Name: "", Score: 999})

	_, failed, err := Load(ctx, Transform(ctx, in, Normalise, counting))
	require.NoError(t, err)

	require.Len(t, failed, 1)
	require.ErrorContains(t, failed[0], "no name", "the FIRST failure is reported")
	require.Zero(t, graded, "and the later stage never ran on a record the earlier one rejected")
}

// TestLoadIsDeterministic is what makes an ETL output diffable.
func TestLoadIsDeterministic(t *testing.T) {
	ctx := context.Background()

	for range 5 {
		in := Records(ctx,
			Record{ID: 3, Name: "c", Score: 1},
			Record{ID: 1, Name: "a", Score: 1},
			Record{ID: 2, Name: "b", Score: 1},
		)

		loaded, _, err := Load(ctx, Transform(ctx, in, Normalise, Grade))
		require.NoError(t, err)

		require.Equal(t, []int{1, 2, 3}, []int{loaded[0].ID, loaded[1].ID, loaded[2].ID},
			"sorted on load, so two runs over the same input produce the same file")
	}
}

// TestAnEmptyPipelineIsFine is the boundary.
func TestAnEmptyPipelineIsFine(t *testing.T) {
	ctx := context.Background()

	loaded, failed, err := Load(ctx, Transform(ctx, Records(ctx), Normalise))
	require.NoError(t, err)
	require.Empty(t, loaded)
	require.Empty(t, failed)
}

// TestNormaliseAndGradeAreOrdinaryFunctions is worth stating.
//
// A stage is `func(Record) (Record, error)`. It knows nothing about channels, contexts or goroutines, which is
// what makes it testable on its own and reusable outside a pipeline.
func TestNormaliseAndGradeAreOrdinaryFunctions(t *testing.T) {
	r, err := Normalise(Record{ID: 1, Name: "  aLEX  "})
	require.NoError(t, err)
	require.Equal(t, "Alex", r.Name)

	_, err = Normalise(Record{ID: 2, Name: "   "})
	require.ErrorIs(t, err, ErrInvalid)

	_, err = Grade(Record{ID: 3, Score: -1})
	require.ErrorIs(t, err, ErrInvalid)

	ok, err := Grade(Record{ID: 4, Score: 100})
	require.NoError(t, err)
	require.Equal(t, 100, ok.Score, "100 is in range; an off-by-one here rejects a perfect score")
}

// TestNormaliseKeepsMultiByteNamesIntact is the byte slice that cut é in half.
func TestNormaliseKeepsMultiByteNamesIntact(t *testing.T) {
	for in, want := range map[string]string{
		"élodie": "Élodie",
		"ÅSA":    "Åsa",
		" zoë ":  "Zoë",
		"x":      "X",
	} {
		got, err := Normalise(Record{ID: 1, Name: in})
		require.NoError(t, err)
		require.Equal(t, want, got.Name)
		require.True(t, utf8.ValidString(got.Name), "%q came out as invalid UTF-8", in)
	}
}
