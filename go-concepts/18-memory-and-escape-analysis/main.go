package main

import "fmt"

func section(n int, title string) {
	fmt.Printf("\n%d. %s\n%s\n", n, title, "-----------------------------------------")
}

func main() {
	fmt.Println("Lesson 18 — memory and escape analysis")

	section(1, "What escapes, and what does not")
	demoEscapes()

	section(2, "Reading the compiler's decisions")
	demoAnalysis()

	section(3, "Stack growth")
	demoStacks()

	section(4, "The garbage collector")
	demoCollector()

	section(5, "Reducing GC pressure")
	demoPressure()

	fmt.Println("\nRun `go test ./18-memory-and-escape-analysis` to see each claim verified.")
	fmt.Println("Run `go build -gcflags=-m -o /dev/null ./18-memory-and-escape-analysis` for the raw output.")
}
