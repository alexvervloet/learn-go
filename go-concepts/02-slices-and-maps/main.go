package main

import "fmt"

func section(n int, title string) {
	fmt.Printf("\n%d. %s\n%s\n", n, title, "-----------------------------------------")
}

func main() {
	fmt.Println("Lesson 02 — slices and maps")

	section(1, "A slice is a header over a shared array")
	demoSlices()

	section(2, "Maps: comma-ok, randomised order, unaddressable elements")
	demoMaps()

	section(3, "The slices and maps packages replace most of the above")
	demoStdlib()

	fmt.Println("\nRun `go test ./02-slices-and-maps` to see each claim verified.")
}
