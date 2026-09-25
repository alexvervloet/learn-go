package main

import "fmt"

func section(n int, title string) {
	fmt.Printf("\n%d. %s\n%s\n", n, title, "-----------------------------------------")
}

func main() {
	fmt.Println("Lesson 17 — benchmarks and pprof")

	section(1, "Writing a benchmark")
	demoWriting()

	section(2, "The three ways a benchmark lies")
	demoLies()

	section(3, "Where allocations come from")
	demoAllocations()

	section(4, "Profiling")
	demoProfiling()

	section(5, "net/http/pprof, and exposing it safely")
	demoHTTPPprof()

	fmt.Println("\nRun `go test -bench . -benchmem -run '^$' ./17-benchmarks-and-pprof` for the numbers.")
}
