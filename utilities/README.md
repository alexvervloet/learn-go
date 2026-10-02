# Utilities

Three standalone packages, no services, no network. `go test ./...` runs everything.

| Package | Subject |
|---|---|
| [`cliflags/`](cliflags/) | A CLI with `flag`: subcommands, a custom `flag.Value`, environment precedence |
| [`aggregate/`](aggregate/) | Fetching from several sources at once, bounded, cancellable |
| [`pipeline/`](pipeline/) | The channel stage pattern, and the three rules that make one correct |

## cliflags

`cobra` is what most Go CLIs use, and it is twenty thousand lines to get subcommands, completion and generated
docs. The standard `flag` package gets you a flag set, typed parsing, a usage message and subcommands in about
fifty. Knowing where the line is means knowing when the dependency is worth it.

What you give up: shell completion, nested subcommands without writing the recursion, man pages, and a usage
message you will want to replace.

Four things a tutorial usually skips:

**Subcommands are a `FlagSet` each.** That is all `flag` offers, and it is enough.

**A repeatable flag needs a custom `flag.Value`.** `Set` appends instead of assigning, which is the whole
difference between `-tag a -tag b` giving two tags and giving one.

**Environment precedence comes from the DEFAULT.** The environment value is the flag's default, so a flag
overwrites it and an absent flag leaves it. Reading the environment after parsing and overwriting gets it
backwards, and a user cannot then override a variable their shell profile set. A variable that is set and does
not parse, `TOOL_WORKERS=sixteen`, falls back to the default under `Parse`, which runs with 4 workers and tells
nobody. `ParseWith(..., ParseOptions{StrictEnv: true})` makes it an error naming the variable, the same as the
flag would be, and is the form new code should use.

**`-h` is not a failure.** `flag` returns `ErrHelp`, and every tutorial ignores it, which is why so many Go
programs exit 2 when you ask for help and break a script that checks the code.

`ContinueOnError`, not the default `ExitOnError`, because the default kills the process on a bad flag: fine in
a `main`, impossible in a test, and a library that can kill its caller.

`TestFlagsAfterPositionalArgumentsAreNotParsed` pins a real trap: `flag` stops at the first non-flag argument,
so `tool serve up -v` leaves `-v` in `Args` and `Verbose` false. That differs from GNU getopt and surprises
everyone once.

## aggregate

Three sources at 100ms each is 300ms sequentially and should be 100ms. That is the motivation, and it is where
most explanations stop.

What comes after is the interesting part:

**`All` returns partial results.** Three sources where one is down gives two answers and one error, because an
aggregate usually wants the data it can get. `FirstError` cancels everything on the first failure, for when
partial data is useless. Having both named makes it a decision rather than an accident.

**`Limit` bounds the fan-out.** No limit is right for a handful of sources and wrong for a thousand: one
goroutine and one connection per item, until the pool exhausts or the remote rate-limits you.
`TestTheLimitBoundsConcurrency` runs 50 sources at a limit of 4 and asserts the peak never exceeded 4.

**The context is checked AFTER acquiring the gate.** Without that, a cancelled aggregate with a limit of 2 and
100 sources still runs all 100, two at a time, because every goroutine was already started.

**`errors.Join`, not a string of messages.** The joined error still matches `errors.Is` against each part, so a
caller can ask "was one of these a deadline" without parsing prose.

One detail worth noticing: `All` writes results into `results[i]` from each goroutine with no mutex. Different
indexes are different memory, so that is correct. `FirstError` writes into a map and genuinely needs the mutex,
because concurrent map writes are a runtime panic rather than a subtle race.

## pipeline

A slice-based transform reads everything, transforms everything, then writes everything, and holds all of it.
A pipeline holds one item per stage.

Three rules make one correct:

1. **The sender closes the channel.** A receiver closing it makes a send on a closed channel, which panics, and
   the sender has no way to know.
2. **Every stage selects on the context.** Without it, a consumer that stops reading leaves every upstream stage
   blocked on a send nobody will receive, forever, and nothing reports it.
3. **Fan-in needs a `WaitGroup`.** The merged channel can only close when every input is drained. Closing after
   the first finishes truncates the output silently, which looks like a data problem rather than a concurrency
   one.

Rule two is the one that bites: a pipeline that works in a test that reads everything leaks in production the
first time a caller returns early. `TestAbandoningAPipelineDoesNotLeak` takes three values from a thousand,
cancels, and counts goroutines back to the baseline.

The channels are unbuffered on purpose. A buffer lets the generator run ahead, which is sometimes what you want
and is a decision; unbuffered means memory is bounded by the number of stages.

**Fan-out costs order.** `TestFanOutLosesOrder` asserts the values are all there and sorts before comparing,
because four workers reading one channel finish in whatever order they finish. If order matters this is the
wrong pattern, and the right one carries an index and reorders at the end.

The ETL half shows the other policy question: a bad record is reported and the rest keep going, because a job
over ten thousand rows that dies on row seven has done nothing useful and told you about one problem. A
pipeline where a bad record means the source is corrupt should stop instead, and the distinction is whether the
records are independent.

The stages themselves are `func(Record) (Record, error)` and know nothing about channels, contexts or
goroutines. That is what makes them testable on their own.
