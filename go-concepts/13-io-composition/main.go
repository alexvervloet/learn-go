package main

import "fmt"

func section(n int, title string) {
	fmt.Printf("\n%d. %s\n%s\n", n, title, "-----------------------------------------")
}

func main() {
	fmt.Println("Lesson 13 — io composition")

	section(1, "The Read and Write contracts")
	demoContract()

	section(2, "io.Copy and its fast paths")
	demoCopy()

	section(3, "The composition toolkit")
	demoCompose()

	section(4, "bufio: batching, and the Scanner token limit")
	demoBuffered()

	section(5, "io.Pipe")
	demoPipes()

	section(6, "Writing your own Reader and Writer")
	demoCustom()

	fmt.Println("\nRun `go test ./13-io-composition` to see each claim verified.")
}
