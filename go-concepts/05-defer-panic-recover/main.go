package main

import "fmt"

func section(n int, title string) {
	fmt.Printf("\n%d. %s\n%s\n", n, title, "-----------------------------------------")
}

func main() {
	fmt.Println("Lesson 05 — defer, panic, recover")

	section(1, "defer: LIFO, argument evaluation, and the loop trap")
	demoOrdering()

	section(2, "Named returns: panic-to-error and the close-error pattern")
	demoNamedReturns()

	section(3, "What panics, and when panicking is correct")
	demoPanics()

	section(4, "Where recover works, and where it silently does not")
	demoRecovery()

	section(5, "Recovering at a boundary: HTTP middleware and a parser")
	demoBoundaries()

	fmt.Println("\nRun `go test ./05-defer-panic-recover` to see each claim verified.")
}
