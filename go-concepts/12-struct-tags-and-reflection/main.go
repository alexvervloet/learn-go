package main

import "fmt"

func section(n int, title string) {
	fmt.Printf("\n%d. %s\n%s\n", n, title, "-----------------------------------------")
}

func main() {
	fmt.Println("Lesson 12 — struct tags and reflection")

	section(1, "Struct tags: syntax, parsing, and what vet catches")
	demoTags()

	section(2, "JSON tags: omitempty vs omitzero, and custom marshalling")
	demoJSONTags()

	section(3, "reflect: Type, Value, Kind")
	demoBasics()

	section(4, "Settability: why Unmarshal takes a pointer")
	demoSettability()

	section(5, "A tag-driven validator")
	demoValidator()

	section(6, "What reflection costs")
	demoCosts()

	fmt.Println("\nRun `go test ./12-struct-tags-and-reflection` to see each claim verified.")
}
