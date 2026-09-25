package main

import "fmt"

func section(n int, title string) {
	fmt.Printf("\n%d. %s\n%s\n", n, title, "-----------------------------------------")
}

func main() {
	fmt.Println("Lesson 08 — select and timeouts")

	section(1, "select: random choice, default, non-blocking operations")
	demoBasics()

	section(2, "Timeouts, timers, and deadlines")
	demoTimeouts()

	section(3, "Disabling a case with nil, and the busy closed-channel loop")
	demoNilCases()

	section(4, "Pipelines: generate, fan-out, fan-in, or-done, tee, bridge")
	demoPipelines()

	section(5, "A worker pool with results, errors and cancellation")
	demoWorkerPool()

	fmt.Println("\nRun `go test -race ./08-select-and-timeouts` to see each claim verified.")
}
