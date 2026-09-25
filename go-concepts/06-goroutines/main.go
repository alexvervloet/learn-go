package main

import "fmt"

func section(n int, title string) {
	fmt.Printf("\n%d. %s\n%s\n", n, title, "-----------------------------------------")
}

func main() {
	fmt.Println("Lesson 06 — goroutines")

	section(1, "Starting goroutines, and what they cost")
	demoStarting()

	section(2, "The scheduler: concurrency, parallelism, GOMAXPROCS")
	demoScheduling()

	section(3, "Goroutine leaks, and the three shapes they take")
	demoLeaks()

	section(4, "The loop variable, before and after Go 1.22")
	demoLoopVar()

	fmt.Println("\nRun `go test -race ./06-goroutines` to see each claim verified.")
}
