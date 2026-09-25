package main

import "fmt"

// section prints a numbered banner so the output of `go run` reads as a guided
// tour rather than a wall of text.
func section(n int, title string) {
	fmt.Printf("\n%d. %s\n%s\n", n, title, "-----------------------------------------")
}

func main() {
	fmt.Println("Lesson 01 — types and zero values")

	section(1, "Every type has a zero value")
	demoZeroValues()

	section(2, "nil slices are usable, nil maps are read-only")
	demoNilBehaviour()

	section(3, "Design for a useful zero value")
	demoUsefulZeroValue()

	section(4, "Declarations and the shadowing trap")
	demoDeclarations()

	section(5, "Untyped constants and iota")
	demoConstants()

	section(6, "Conversions are explicit, and can truncate or overflow")
	demoConversions()

	fmt.Println("\nRun `go test ./01-types-and-zero-values` to see each claim verified.")
}
