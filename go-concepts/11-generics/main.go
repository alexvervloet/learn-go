package main

import "fmt"

func section(n int, title string) {
	fmt.Printf("\n%d. %s\n%s\n", n, title, "-----------------------------------------")
}

func main() {
	fmt.Println("Lesson 11 — generics")

	section(1, "Type parameters and inference")
	demoBasics()

	section(2, "Constraints: comparable, cmp.Ordered, type sets, ~")
	demoConstraints()

	section(3, "Generic types, and why methods cannot add parameters")
	demoTypes()

	section(4, "Containers: Set, Result, Optional, Cache")
	demoContainers()

	section(5, "Iterators: iter.Seq and the yield contract")
	demoIterators()

	section(6, "What generics cost")
	demoCosts()

	fmt.Println("\nRun `go test ./11-generics` to see each claim verified.")
}
