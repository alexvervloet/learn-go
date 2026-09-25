package main

import "fmt"

func section(n int, title string) {
	fmt.Printf("\n%d. %s\n%s\n", n, title, "-----------------------------------------")
}

func main() {
	fmt.Println("Lesson 04 — errors")

	section(1, "Creating and wrapping: %w keeps the cause, %v loses it")
	demoCreating()

	section(2, "Sentinels and errors.Is")
	demoSentinels()

	section(3, "Custom types and errors.As")
	demoCustom()

	section(4, "errors.Join for multi-error validation")
	demoJoining()

	section(5, "Retry, deferred close, and when to panic")
	demoPatterns()

	fmt.Println("\nRun `go test ./04-errors` to see each claim verified.")
}
