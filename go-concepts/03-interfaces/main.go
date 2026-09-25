package main

import "fmt"

func section(n int, title string) {
	fmt.Printf("\n%d. %s\n%s\n", n, title, "-----------------------------------------")
}

func main() {
	fmt.Println("Lesson 03 — interfaces")

	section(1, "Structural typing and consumer-defined interfaces")
	demoSatisfaction()

	section(2, "The typed-nil trap")
	demoTypedNil()

	section(3, "Method sets: value vs pointer receivers")
	demoMethodSets()

	section(4, "Type assertions, type switches, embedding")
	demoAssertions()

	section(5, "io.Writer: one function, six destinations")
	demoWriters()

	fmt.Println("\nRun `go test ./03-interfaces` to see each claim verified.")
}
