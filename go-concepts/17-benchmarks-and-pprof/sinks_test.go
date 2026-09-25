package main

// Benchmark sinks.
//
// A benchmark whose result is discarded can have its work removed entirely by
// the compiler, giving an impossible sub-nanosecond number. Assigning to a
// package-level variable prevents that, because the compiler cannot prove
// nobody reads it.
//
// They live in a _test.go file because that is where they are written. A
// variable assigned only from tests is unused in the package itself, and
// staticcheck's `unused` check reports it, which is right: putting them in the
// package would mean carrying dead variables in every build.
//
// The three that the DEMOS also write (sinkInt, sinkString, sinkSlice) stay in
// lies.go for that reason.
var (
	sinkStrings   []string
	sinkMap       map[int]string
	sinkAny       []any
	sinkRecord    record
	sinkRecordPtr *record
)
