package main

// Benchmark and allocation-test sinks. See lesson 17's sinks_test.go for why
// they live in a _test.go file: a variable written only from tests is unused
// in the package itself, and staticcheck says so.
var (
	sinkUser     User
	sinkUserPtr  *User
	sinkFloat    float64
	sinkIntValue int
	sinkFunc     func() int
	sinkID       int64
	sinkItems    []Item
	sinkItemPtrs []*Item
	sinkEvents   []Event
	sinkInterned []InternedEvent
)
