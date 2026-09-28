// Package pipeline is the channel-based stage pattern: a generator, some stages, a sink.
//
// # What a pipeline is for
//
// Processing a stream that does not fit in memory, or one whose stages have different costs. A slice-based
// version reads everything, transforms everything, then writes everything, and holds all of it at once. A
// pipeline holds one item per stage.
//
// # The three rules that make one correct
//
//  1. The sender closes the channel. A receiver closing it makes a send on a closed channel, which panics, and
//     the sender has no way to know.
//  2. Every stage takes a done or context and selects on it. Without that, a consumer that stops reading leaks
//     every goroutine upstream of it, forever, and nothing reports it.
//  3. A fan-in needs a WaitGroup, because the merged channel can only be closed when every input is done.
//
// The second is the one that bites. A pipeline that works in a test that reads everything leaks in production
// the first time a caller returns early.
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Generate sends the values and closes.
//
// # Why the channel is unbuffered
//
// Because a buffer changes the backpressure. With one, the generator runs ahead by the buffer size, which is
// sometimes what you want and is a decision. Unbuffered means the generator runs exactly as fast as the slowest
// downstream stage, which is the default worth having: memory is bounded by the number of stages.
func Generate(ctx context.Context, values ...int) <-chan int {
	out := make(chan int)

	go func() {
		// The close is deferred in the SENDER, which is rule one. A receiver cannot close it: a send on a
		// closed channel panics and the sender cannot know it happened.
		defer close(out)

		for _, v := range values {
			select {
			case out <- v:
			case <-ctx.Done():
				// Return, not continue. A cancelled pipeline that keeps generating is a pipeline that
				// finishes its work and throws it away.
				return
			}
		}
	}()

	return out
}

// Square is one stage.
//
// Every stage has the same shape: take a channel, return a channel, close the output when the input closes.
// That uniformity is the point, because it means stages compose without knowing about each other.
func Square(ctx context.Context, in <-chan int) <-chan int {
	out := make(chan int)

	go func() {
		defer close(out)

		// `for v := range in` handles the input closing. The select inside handles the CONSUMER going away,
		// and it is the half people leave out: without it, a stage blocks forever on a send nobody will
		// receive, and the goroutine leaks with everything upstream of it.
		for v := range in {
			select {
			case out <- v * v:
			case <-ctx.Done():
				return
			}
		}
	}()

	return out
}

// Filter keeps the values a predicate accepts.
func Filter(ctx context.Context, in <-chan int, keep func(int) bool) <-chan int {
	out := make(chan int)

	go func() {
		defer close(out)

		for v := range in {
			if !keep(v) {
				continue
			}

			select {
			case out <- v:
			case <-ctx.Done():
				return
			}
		}
	}()

	return out
}

// FanOut runs n copies of a stage over one input.
//
// # What this buys and what it costs
//
// Throughput, when the stage is slow and the work is independent. It costs ORDER: n workers reading one channel
// and writing another produce results in whatever order they finish, which is why FanIn's output is unordered
// and why the tests here sort before comparing.
//
// If order matters, this is the wrong pattern and the right one is an index carried alongside each value and a
// reorder buffer at the end. Most pipelines do not need it, and the ones that do need to say so.
func FanOut(ctx context.Context, in <-chan int, n int, stage func(context.Context, <-chan int) <-chan int) []<-chan int {
	outs := make([]<-chan int, 0, n)

	for range n {
		outs = append(outs, stage(ctx, in))
	}

	return outs
}

// FanIn merges channels into one.
//
// # The WaitGroup is not optional
//
// The merged channel can only be closed when EVERY input is drained, and that means counting. A version that
// closes after the first input finishes truncates the output silently, which is a bug that looks like a data
// problem.
func FanIn(ctx context.Context, ins ...<-chan int) <-chan int {
	out := make(chan int)

	var wg sync.WaitGroup

	for _, in := range ins {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for v := range in {
				select {
				case out <- v:
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	// A separate goroutine to wait and close, because Wait blocks and the caller needs the channel now. This
	// tiny goroutine is the standard shape and it is what makes FanIn a function rather than a procedure the
	// caller has to finish.
	go func() {
		wg.Wait()
		close(out)
	}()

	return out
}

// Collect drains a channel into a slice.
//
// It also reports whether the context ended first, which is the difference between "the pipeline finished" and
// "the pipeline was cut off", and a caller that cannot tell them apart will report partial results as complete.
func Collect(ctx context.Context, in <-chan int) ([]int, error) {
	var out []int

	for {
		select {
		case v, ok := <-in:
			if !ok {
				return out, nil
			}

			out = append(out, v)

		case <-ctx.Done():
			return out, ctx.Err()
		}
	}
}

// ---------------------------------------------------------------------------
// An ETL pipeline over records, which is the shape the Python original had
// ---------------------------------------------------------------------------

// Record is one row moving through the pipeline.
type Record struct {
	ID    int
	Name  string
	Score int
}

// ErrInvalid is returned for a record that cannot be transformed.
var ErrInvalid = errors.New("pipeline: invalid record")

// Stage is a transform that can fail.
type Stage func(Record) (Record, error)

// Normalise trims and title-cases the name.
func Normalise(r Record) (Record, error) {
	name := strings.TrimSpace(r.Name)
	if name == "" {
		return Record{}, fmt.Errorf("%w: record %d has no name", ErrInvalid, r.ID)
	}

	r.Name = strings.ToUpper(name[:1]) + strings.ToLower(name[1:])

	return r, nil
}

// Grade rejects an out-of-range score.
func Grade(r Record) (Record, error) {
	if r.Score < 0 || r.Score > 100 {
		return Record{}, fmt.Errorf("%w: record %d has score %d", ErrInvalid, r.ID, r.Score)
	}

	return r, nil
}

// Outcome is a record that made it through, or the error that stopped it.
type Outcome struct {
	Record Record
	Err    error
}

// Transform runs records through the stages, reporting failures rather than stopping.
//
// # Why a bad record does not stop the pipeline
//
// Because an ETL job over ten thousand rows that dies on row seven has done nothing useful and told you about
// one problem. Collecting the failures processes the other 9,999 and tells you about all of them, which is one
// run instead of seven.
//
// That is a choice and not a law: a pipeline where a bad record means the SOURCE is corrupt should stop. The
// distinction is whether the records are independent.
func Transform(ctx context.Context, in <-chan Record, stages ...Stage) <-chan Outcome {
	out := make(chan Outcome)

	go func() {
		defer close(out)

		for r := range in {
			outcome := Outcome{Record: r}

			for _, stage := range stages {
				next, err := stage(outcome.Record)
				if err != nil {
					outcome = Outcome{Record: r, Err: err}

					break
				}

				outcome.Record = next
			}

			select {
			case out <- outcome:
			case <-ctx.Done():
				return
			}
		}
	}()

	return out
}

// Load drains outcomes into the successes and the failures.
func Load(ctx context.Context, in <-chan Outcome) (loaded []Record, failed []error, err error) {
	for {
		select {
		case o, ok := <-in:
			if !ok {
				sort.Slice(loaded, func(i, j int) bool { return loaded[i].ID < loaded[j].ID })

				return loaded, failed, nil
			}

			if o.Err != nil {
				failed = append(failed, o.Err)

				continue
			}

			loaded = append(loaded, o.Record)

		case <-ctx.Done():
			return loaded, failed, ctx.Err()
		}
	}
}

// Records sends records and closes.
func Records(ctx context.Context, records ...Record) <-chan Record {
	out := make(chan Record)

	go func() {
		defer close(out)

		for _, r := range records {
			select {
			case out <- r:
			case <-ctx.Done():
				return
			}
		}
	}()

	return out
}
